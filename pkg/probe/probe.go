package probe

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mcnairstudios/tvproxy-streams/pkg/scanner"
)

func PathHash(path string) string {
	h := sha256.Sum256([]byte(path))
	return fmt.Sprintf("%x", h)[:16]
}

type AudioTrack struct {
	Codec    string `json:"codec"`
	Layout   string `json:"layout,omitempty"`
	Language string `json:"language,omitempty"`
	Channels int    `json:"channels,omitempty"`
}

type Info struct {
	VideoCodec  string       `json:"video_codec,omitempty"`
	AudioCodec  string       `json:"audio_codec,omitempty"`
	Resolution  string       `json:"resolution,omitempty"`
	Width       int          `json:"width,omitempty"`
	Height      int          `json:"height,omitempty"`
	Duration    float64      `json:"duration,omitempty"`
	AudioLayout string       `json:"audio_layout,omitempty"`
	AudioTracks []AudioTrack `json:"audio_tracks,omitempty"`
	Container   string       `json:"container,omitempty"`
	ProbedAt    string       `json:"probed_at,omitempty"`
}

type Cache struct {
	mu       sync.RWMutex
	probes   map[string]*Info
	cacheDir string
}

func NewCache(cacheDir string) *Cache {
	c := &Cache{
		probes:   make(map[string]*Info),
		cacheDir: cacheDir,
	}
	c.loadFromDisk()
	return c
}

func (c *Cache) Get(path string) *Info {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.probes[path]
}

func (c *Cache) Set(path string, info *Info) {
	c.mu.Lock()
	c.probes[path] = info
	c.mu.Unlock()
	c.saveToDisk(path, info)
}

func (c *Cache) ProbeWorker(ctx context.Context, roots []scanner.ScanRoot, items func() []scanner.MediaItem) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			c.probeNext(roots, items())
		}
	}
}

func (c *Cache) probeNext(roots []scanner.ScanRoot, items []scanner.MediaItem) {
	for _, item := range items {
		if c.Get(item.Path) != nil {
			continue
		}
		fullPath := resolveFullPath(roots, item.Path)
		if fullPath == "" {
			continue
		}
		info := FFProbe(fullPath)
		if info == nil {
			info = &Info{ProbedAt: time.Now().UTC().Format(time.RFC3339)}
		}
		c.Set(item.Path, info)
		log.Printf("probed: %s (%s %s %s)", item.Path, info.VideoCodec, info.Resolution, info.AudioLayout)
		return
	}
}

func resolveFullPath(roots []scanner.ScanRoot, relPath string) string {
	for _, root := range roots {
		rootName := filepath.Base(root.Path)
		if strings.HasPrefix(relPath, rootName+"/") {
			full := filepath.Join(filepath.Dir(root.Path), relPath)
			if _, err := os.Stat(full); err == nil {
				return full
			}
		}
	}
	return ""
}

func (c *Cache) loadFromDisk() {
	if c.cacheDir == "" {
		return
	}
	entries, err := os.ReadDir(c.cacheDir)
	if err != nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(c.cacheDir, e.Name()))
		if err != nil {
			continue
		}
		var info Info
		if json.Unmarshal(data, &info) == nil {
			key := strings.ReplaceAll(strings.TrimSuffix(e.Name(), ".json"), "_", "/")
			c.probes[key] = &info
		}
	}
}

func (c *Cache) saveToDisk(path string, info *Info) {
	if c.cacheDir == "" {
		return
	}
	os.MkdirAll(c.cacheDir, 0755)
	key := strings.ReplaceAll(strings.ReplaceAll(path, "/", "_"), "\\", "_")
	data, _ := json.MarshalIndent(info, "", "  ")
	os.WriteFile(filepath.Join(c.cacheDir, key+".json"), data, 0644)
}

type ffprobeOutput struct {
	Streams []ffprobeStream `json:"streams"`
	Format  ffprobeFormat   `json:"format"`
}

type ffprobeStream struct {
	CodecType     string            `json:"codec_type"`
	CodecName     string            `json:"codec_name"`
	Width         int               `json:"width"`
	Height        int               `json:"height"`
	Channels      int               `json:"channels"`
	ChannelLayout string            `json:"channel_layout"`
	Profile       string            `json:"profile"`
	Tags          map[string]string `json:"tags"`
}

type ffprobeFormat struct {
	Duration   string `json:"duration"`
	FormatName string `json:"format_name"`
}

func FFProbe(path string) *Info {
	cmd := exec.Command("ffprobe",
		"-hide_banner", "-v", "quiet",
		"-print_format", "json",
		"-show_streams", "-show_format",
		path,
	)
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var result ffprobeOutput
	if json.Unmarshal(out, &result) != nil {
		return nil
	}

	info := &Info{ProbedAt: time.Now().UTC().Format(time.RFC3339)}
	for _, s := range result.Streams {
		if s.CodecType == "video" && info.VideoCodec == "" {
			info.VideoCodec = normalizeVideoCodec(s.CodecName)
			info.Width = s.Width
			info.Height = s.Height
			info.Resolution = classifyResolution(s.Width, s.Height)
		}
		if s.CodecType == "audio" {
			track := AudioTrack{
				Codec:    normalizeAudioCodec(s.CodecName),
				Layout:   classifyAudioLayout(s.Channels, s.ChannelLayout, s.CodecName, s.Profile),
				Channels: s.Channels,
			}
			if lang, ok := s.Tags["language"]; ok && lang != "" && lang != "und" {
				track.Language = lang
			}
			info.AudioTracks = append(info.AudioTracks, track)
			if info.AudioCodec == "" {
				info.AudioCodec = track.Codec
				info.AudioLayout = track.Layout
			}
		}
	}
	if result.Format.Duration != "" {
		if d, err := strconv.ParseFloat(result.Format.Duration, 64); err == nil {
			info.Duration = d
		}
	}
	if result.Format.FormatName != "" {
		info.Container = normalizeContainer(result.Format.FormatName)
	}
	return info
}

func normalizeVideoCodec(codec string) string {
	switch strings.ToLower(codec) {
	case "h264", "avc":
		return "H264"
	case "hevc", "h265":
		return "HEVC"
	case "av1":
		return "AV1"
	case "vp9":
		return "VP9"
	case "mpeg2video":
		return "MPEG2"
	case "mpeg4":
		return "MPEG4"
	default:
		return strings.ToUpper(codec)
	}
}

func normalizeAudioCodec(codec string) string {
	switch strings.ToLower(codec) {
	case "aac", "aac_latm":
		return "AAC"
	case "ac3":
		return "AC3"
	case "eac3":
		return "EAC3"
	case "truehd":
		return "TrueHD"
	case "dts":
		return "DTS"
	case "flac":
		return "FLAC"
	case "mp2":
		return "MP2"
	case "mp3":
		return "MP3"
	case "opus":
		return "Opus"
	case "vorbis":
		return "Vorbis"
	default:
		return strings.ToUpper(codec)
	}
}

func normalizeContainer(format string) string {
	parts := strings.Split(format, ",")
	f := strings.TrimSpace(parts[0])
	switch f {
	case "matroska", "webm":
		return "matroska"
	case "mov", "mp4", "m4a", "3gp", "3g2", "mj2":
		return "mp4"
	case "mpegts":
		return "mpegts"
	case "avi":
		return "avi"
	default:
		return f
	}
}

func classifyResolution(w, h int) string {
	if h >= 2160 || w >= 3840 {
		return "4K"
	}
	if h >= 1080 || w >= 1920 {
		return "1080p"
	}
	if h >= 720 || w >= 1280 {
		return "720p"
	}
	if h >= 480 || w >= 720 {
		return "SD"
	}
	if h > 0 {
		return fmt.Sprintf("%dp", h)
	}
	return ""
}

func classifyAudioLayout(channels int, layout, codec, profile string) string {
	codecLower := strings.ToLower(codec)
	profileLower := strings.ToLower(profile)

	if codecLower == "truehd" {
		if strings.Contains(profileLower, "atmos") || strings.Contains(layout, "7.1") {
			return "Atmos"
		}
		return "TrueHD"
	}
	if codecLower == "eac3" {
		if strings.Contains(profileLower, "atmos") {
			return "Atmos"
		}
		return "DD+"
	}
	if codecLower == "ac3" {
		if channels >= 6 {
			return "DD 5.1"
		}
		return "DD"
	}
	if codecLower == "dts" {
		if strings.Contains(profileLower, "hd ma") || strings.Contains(profileLower, "hd-ma") {
			return "DTS-HD MA"
		}
		if strings.Contains(profileLower, "hd") {
			return "DTS-HD"
		}
		return "DTS"
	}

	switch channels {
	case 8:
		return "7.1"
	case 6:
		return "5.1"
	case 2:
		return "Stereo"
	case 1:
		return "Mono"
	default:
		if channels > 0 {
			return fmt.Sprintf("%dch", channels)
		}
		return ""
	}
}
