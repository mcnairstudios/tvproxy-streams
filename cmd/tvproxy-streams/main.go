package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mcnairstudios/tvproxy-streams/pkg/mtls"
	"github.com/mcnairstudios/tvproxy-streams/pkg/playlist"
	"github.com/mcnairstudios/tvproxy-streams/pkg/probe"
	"github.com/mcnairstudios/tvproxy-streams/pkg/scanner"
)

type Library struct {
	mu         sync.RWMutex
	items      []scanner.MediaItem
	etag       string
	roots      []scanner.ScanRoot
	probeCache *probe.Cache
	baseURL    string
}

func (l *Library) Scan() {
	items := scanner.ScanRoots(l.roots)
	etag := computeLibraryETag(items)
	l.mu.Lock()
	l.items = items
	l.etag = etag
	l.mu.Unlock()

	log.Printf("scanned %d items (%d movies, %d episodes, %d files) etag=%s",
		len(items), countType(items, scanner.TypeMovie), countType(items, scanner.TypeSeries), countType(items, scanner.TypeFiles), etag[:12])
}

func (l *Library) Items() []scanner.MediaItem {
	l.mu.RLock()
	defer l.mu.RUnlock()
	out := make([]scanner.MediaItem, len(l.items))
	copy(out, l.items)
	return out
}

func (l *Library) ETag() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.etag
}

func (l *Library) FindByID(id string) (scanner.MediaItem, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	for _, item := range l.items {
		if probe.PathHash(item.Path) == id {
			return item, true
		}
	}
	return scanner.MediaItem{}, false
}

func (l *Library) ProbedCount() int {
	l.mu.RLock()
	defer l.mu.RUnlock()
	n := 0
	for _, item := range l.items {
		if l.probeCache.Get(item.Path) != nil {
			n++
		}
	}
	return n
}

func countType(items []scanner.MediaItem, t scanner.MediaType) int {
	n := 0
	for _, item := range items {
		if item.Type == t {
			n++
		}
	}
	return n
}

func computeLibraryETag(items []scanner.MediaItem) string {
	h := sha256.New()
	for _, item := range items {
		fmt.Fprintf(h, "%s|%s|%s|%s|%d|%d\n", item.Type, item.Path, item.Name, item.Group, item.Season, item.Episode)
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

func parseScanDirs(envVal, defaultMediaDir string) []scanner.ScanRoot {
	if envVal != "" {
		var roots []scanner.ScanRoot
		for _, entry := range strings.Split(envVal, ",") {
			parts := strings.SplitN(strings.TrimSpace(entry), ":", 2)
			if len(parts) != 2 {
				log.Printf("invalid SCAN_DIRS entry (expected path:type): %s", entry)
				continue
			}
			t := scanner.MediaType(parts[1])
			if t != scanner.TypeMovie && t != scanner.TypeSeries && t != scanner.TypeFiles {
				log.Printf("invalid type %q in SCAN_DIRS (must be movie, series, or files): %s", parts[1], entry)
				continue
			}
			roots = append(roots, scanner.ScanRoot{Path: parts[0], Type: t})
		}
		return roots
	}

	return []scanner.ScanRoot{
		{Path: defaultMediaDir + "/movies", Type: scanner.TypeMovie},
		{Path: defaultMediaDir + "/tv", Type: scanner.TypeSeries},
		{Path: defaultMediaDir + "/other", Type: scanner.TypeFiles},
	}
}

func main() {
	configDir := "/config"
	if v := os.Getenv("CONFIG_DIR"); v != "" {
		configDir = v
	}

	if len(os.Args) > 1 {
		runCLI(os.Args[1:], configDir)
		return
	}

	runServer(configDir)
}

func runCLI(args []string, configDir string) {
	store := mtls.NewStore(configDir)

	switch args[0] {
	case "token":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: tvproxy-streams token <email>")
			os.Exit(1)
		}
		email := args[1]
		token := store.GenerateToken(email, 10*time.Minute)
		fmt.Printf("Enrollment token for %s (expires in 10 minutes):\n  %s\n", email, token)

	case "clients":
		clients := store.Clients()
		if len(clients) == 0 {
			fmt.Println("No enrolled clients.")
			return
		}
		fmt.Printf("%-40s %-30s %s\n", "FINGERPRINT", "EMAIL", "ENROLLED")
		for _, c := range clients {
			fp := c.Fingerprint
			if len(fp) > 38 {
				fp = fp[:38]
			}
			enrolled := c.EnrolledAt
			if t, err := time.Parse(time.RFC3339, enrolled); err == nil {
				enrolled = t.Format("2006-01-02 15:04")
			}
			fmt.Printf("%-40s %-30s %s\n", fp, c.Email, enrolled)
		}

	case "revoke":
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: tvproxy-streams revoke <email>")
			os.Exit(1)
		}
		n := store.Revoke(args[1])
		if n > 0 {
			fmt.Printf("Revoked %d client certificate(s) for %s.\n", n, args[1])
		} else {
			fmt.Printf("No enrolled client found for %s.\n", args[1])
		}

	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\nusage: tvproxy-streams [token <email> | clients | revoke <email>]\n", args[0])
		os.Exit(1)
	}
}

func runServer(configDir string) {
	port := 8090
	mediaDir := "/media"
	baseURL := ""
	probeDir := "/data/probes"
	tlsEnabled := false

	if v := os.Getenv("PORT"); v != "" {
		fmt.Sscanf(v, "%d", &port)
	}
	if v := os.Getenv("MEDIA_DIR"); v != "" {
		mediaDir = v
	}
	if v := os.Getenv("BASE_URL"); v != "" {
		baseURL = v
	}
	if v := os.Getenv("PROBE_DIR"); v != "" {
		probeDir = v
	}
	if v := os.Getenv("TLS"); v == "true" || v == "1" {
		tlsEnabled = true
	}
	if baseURL == "" {
		scheme := "http"
		if tlsEnabled {
			scheme = "https"
		}
		baseURL = fmt.Sprintf("%s://localhost:%d", scheme, port)
	}

	roots := parseScanDirs(os.Getenv("SCAN_DIRS"), mediaDir)
	for _, r := range roots {
		log.Printf("scan root: %s [%s]", r.Path, r.Type)
	}

	probeCache := probe.NewCache(probeDir)
	lib := &Library{
		roots:      roots,
		probeCache: probeCache,
		baseURL:    baseURL,
	}
	lib.Scan()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go probeCache.ProbeWorker(ctx, roots, lib.Items)

	fsWatcher, err := scanner.NewWatcher(roots, 2*time.Second, func() {
		lib.Scan()
	})
	if err != nil {
		log.Printf("filesystem watcher failed, falling back to 5-minute poll: %v", err)
		rescanTicker := time.NewTicker(5 * time.Minute)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-rescanTicker.C:
					lib.Scan()
				}
			}
		}()
	} else {
		log.Println("filesystem watcher active")
		go fsWatcher.Run(ctx.Done())
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/playlist.m3u", func(w http.ResponseWriter, r *http.Request) {
		etag := lib.ETag()
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		playlist.ServeM3U(lib.Items(), probeCache, lib.baseURL, w)
	})
	mux.HandleFunc("/api/library", func(w http.ResponseWriter, r *http.Request) {
		etag := lib.ETag()
		w.Header().Set("ETag", etag)
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		playlist.ServeJSON(lib.Items(), w, r)
	})
	mux.HandleFunc("/api/status", func(w http.ResponseWriter, r *http.Request) {
		playlist.ServeStatus(lib.Items(), lib.ProbedCount(), w)
	})
	mux.HandleFunc("/stream/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/stream/")
		if id == "" {
			http.Error(w, "missing stream id", http.StatusBadRequest)
			return
		}
		item, ok := lib.FindByID(id)
		if !ok {
			http.NotFound(w, r)
			return
		}
		for _, root := range roots {
			rootName := filepath.Base(root.Path)
			if strings.HasPrefix(item.Path, rootName+"/") {
				full := filepath.Join(filepath.Dir(root.Path), item.Path)
				if _, err := os.Stat(full); err == nil {
					http.ServeFile(w, r, full)
					return
				}
			}
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<html><body>
			<h1>TVProxy Streams</h1>
			<ul>
				<li><a href="/playlist.m3u">M3U Playlist</a></li>
				<li><a href="/api/library">Library JSON</a></li>
				<li><a href="/api/library?type=movie">Movies</a></li>
				<li><a href="/api/library?type=series">TV Series</a></li>
				<li><a href="/api/library?type=files">Files</a></li>
				<li><a href="/api/status">Status</a></li>
			</ul>
		</body></html>`)
	})

	addr := fmt.Sprintf(":%d", port)

	var handler http.Handler = mux

	if tlsEnabled {
		mtlsServer, err := mtls.Setup(configDir)
		if err != nil {
			log.Fatalf("mTLS setup failed: %v", err)
		}

		mux.HandleFunc("/enroll", mtlsServer.EnrollHandler)

		protected := mtlsServer.RequireClientCert(mux)
		handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/enroll" {
				mux.ServeHTTP(w, r)
				return
			}
			protected.ServeHTTP(w, r)
		})

		srv := &http.Server{
			Addr:      addr,
			Handler:   handler,
			TLSConfig: mtlsServer.TLSConfig(),
		}

		go func() {
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			<-sigCh
			log.Println("shutting down...")
			cancel()
			srv.Shutdown(context.Background())
		}()

		log.Printf("tvproxy-streams listening on %s (mTLS enabled)", addr)
		if err := srv.ListenAndServeTLS(mtlsServer.CertPath, mtlsServer.KeyPath); err != http.ErrServerClosed {
			log.Fatal(err)
		}
	} else {
		srv := &http.Server{Addr: addr, Handler: handler}

		go func() {
			sigCh := make(chan os.Signal, 1)
			signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
			<-sigCh
			log.Println("shutting down...")
			cancel()
			srv.Shutdown(context.Background())
		}()

		log.Printf("tvproxy-streams listening on %s", addr)
		if err := srv.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatal(err)
		}
	}
}
