package playlist

import (
	"encoding/json"
	"net/http"

	"github.com/mcnairstudios/tvproxy-streams/pkg/scanner"
)

func ServeJSON(items []scanner.MediaItem, w http.ResponseWriter, r *http.Request) {
	typeFilter := r.URL.Query().Get("type")
	seriesFilter := r.URL.Query().Get("series")

	var filtered []scanner.MediaItem
	for _, item := range items {
		if typeFilter != "" && string(item.Type) != typeFilter {
			continue
		}
		if seriesFilter != "" && item.Series != seriesFilter {
			continue
		}
		filtered = append(filtered, item)
	}
	if filtered == nil {
		filtered = []scanner.MediaItem{}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(filtered)
}

func ServeStatus(items []scanner.MediaItem, probedCount int, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"total":    len(items),
		"probed":   probedCount,
		"movies":   scanner.CountType(items, scanner.TypeMovie),
		"episodes": scanner.CountType(items, scanner.TypeSeries),
		"files":    scanner.CountType(items, scanner.TypeFiles),
	})
}
