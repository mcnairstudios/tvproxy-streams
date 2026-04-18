package playlist

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mcnairstudios/tvproxy-streams/pkg/probe"
	"github.com/mcnairstudios/tvproxy-streams/pkg/scanner"
)

func TestServeM3UMovie(t *testing.T) {
	items := []scanner.MediaItem{
		{Type: scanner.TypeMovie, Path: "movies/Film.mp4", Name: "Film", Group: "Movies", Filename: "Film.mp4"},
	}
	cache := probe.NewCache(t.TempDir())
	defer cache.Close()
	w := httptest.NewRecorder()
	ServeM3U(items, cache, "http://localhost:8090", w)

	body := w.Body.String()
	if !strings.Contains(body, "#EXTM3U") {
		t.Error("missing M3U header")
	}
	if !strings.Contains(body, `tvp-type="movie"`) {
		t.Error("missing tvp-type tag")
	}
	if !strings.Contains(body, `group-title="Movies"`) {
		t.Error("missing group-title")
	}
	if !strings.Contains(body, "http://localhost:8090/stream/") {
		t.Error("missing stream URL")
	}
	// Stream URL should use tvp-id hash, not URL-encoded path
	id := probe.PathHash("movies/Film.mp4")
	if !strings.Contains(body, "/stream/"+id) {
		t.Error("stream URL should use tvp-id")
	}
	// EXTINF should end with ,DisplayName per M3U convention
	if !strings.Contains(body, ",Film\n") {
		t.Error("EXTINF should have display name after comma")
	}
	if !strings.Contains(body, `tvg-name="Film"`) {
		t.Error("missing tvg-name")
	}
}

func TestServeM3USeries(t *testing.T) {
	items := []scanner.MediaItem{
		{Type: scanner.TypeSeries, Path: "tv/Show/S01/ep.mkv", Name: "Pilot", Series: "Show", Season: 1, Episode: 1, Filename: "ep.mkv"},
	}
	cache := probe.NewCache(t.TempDir())
	defer cache.Close()
	w := httptest.NewRecorder()
	ServeM3U(items, cache, "http://localhost:8090", w)

	body := w.Body.String()
	if !strings.Contains(body, `tvp-series="Show"`) {
		t.Error("missing tvp-series")
	}
	if !strings.Contains(body, `tvp-season="1"`) {
		t.Error("missing tvp-season")
	}
	if !strings.Contains(body, `tvp-episode="1"`) {
		t.Error("missing tvp-episode")
	}
	if !strings.Contains(body, `group-title="Show"`) {
		t.Error("group-title should be series name without prefix")
	}
	if strings.Contains(body, "TV|") {
		t.Error("group-title should NOT have TV| prefix")
	}
	// tvg-name should contain the formatted episode name
	if !strings.Contains(body, `tvg-name="Show - S01E01 - Pilot"`) {
		t.Error("tvg-name should contain formatted series episode name")
	}
}

func TestServeM3UTags(t *testing.T) {
	items := []scanner.MediaItem{
		{Type: scanner.TypeMovie, Path: "movies/SciFi/Film.mp4", Name: "Film", Group: "Movies", Tags: []string{"SciFi"}, Filename: "Film.mp4"},
	}
	cache := probe.NewCache(t.TempDir())
	defer cache.Close()
	w := httptest.NewRecorder()
	ServeM3U(items, cache, "http://localhost:8090", w)

	body := w.Body.String()
	if !strings.Contains(body, `tvp-tags="SciFi"`) {
		t.Error("missing tvp-tags")
	}
}

func TestServeM3UCollection(t *testing.T) {
	items := []scanner.MediaItem{
		{Type: scanner.TypeMovie, Path: "movies/Trilogy/Film1.mp4", Name: "Film One", Group: "Trilogy", Collection: "Trilogy", Filename: "Film1.mp4"},
	}
	cache := probe.NewCache(t.TempDir())
	defer cache.Close()
	w := httptest.NewRecorder()
	ServeM3U(items, cache, "http://localhost:8090", w)

	body := w.Body.String()
	if !strings.Contains(body, `tvp-collection="Trilogy"`) {
		t.Error("missing tvp-collection")
	}
	if !strings.Contains(body, `group-title="Trilogy"`) {
		t.Error("collection should be the group-title")
	}
	// Display name with space should be URL-encoded
	if !strings.Contains(body, `tvg-name="Film One"`) {
		t.Error("tvg-name should contain display name")
	}
}
