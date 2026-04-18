package playlist

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/mcnairstudios/tvproxy-streams/pkg/probe"
	"github.com/mcnairstudios/tvproxy-streams/pkg/scanner"
)

func sanitizeTag(s string) string {
	return strings.ReplaceAll(s, `"`, "'")
}

func ServeM3U(items []scanner.MediaItem, probeCache *probe.Cache, baseURL string, w http.ResponseWriter) {
	w.Header().Set("Content-Type", "audio/x-mpegurl")
	w.Header().Set("Cache-Control", "no-cache")

	fmt.Fprintln(w, "#EXTM3U")

	for _, item := range items {
		itemID := probe.PathHash(item.Path)
		streamURL := baseURL + "/stream/" + itemID

		displayName := item.Name
		if item.Type == scanner.TypeSeries && item.Season > 0 && item.Episode > 0 {
			displayName = fmt.Sprintf("%s - S%02dE%02d", item.Series, item.Season, item.Episode)
			if item.Name != "" && item.Name != item.Filename {
				displayName += " - " + item.Name
			}
		}

		var tags []string
		tags = append(tags, fmt.Sprintf(`tvp-id="%s"`, itemID))
		tags = append(tags, fmt.Sprintf(`tvg-name="%s"`, sanitizeTag(displayName)))
		tags = append(tags, fmt.Sprintf(`tvp-type="%s"`, item.Type))

		if item.Collection != "" {
			tags = append(tags, fmt.Sprintf(`tvp-collection="%s"`, sanitizeTag(item.Collection)))
		}
		if len(item.Tags) > 0 {
			tags = append(tags, fmt.Sprintf(`tvp-tags="%s"`, sanitizeTag(strings.Join(item.Tags, ","))))
		}

		switch item.Type {
		case scanner.TypeMovie:
			tags = append(tags, fmt.Sprintf(`group-title="%s"`, sanitizeTag(item.Group)))
		case scanner.TypeSeries:
			tags = append(tags, fmt.Sprintf(`group-title="%s"`, sanitizeTag(item.Series)))
			tags = append(tags, fmt.Sprintf(`tvp-series="%s"`, sanitizeTag(item.Series)))
			if item.Season > 0 {
				tags = append(tags, fmt.Sprintf(`tvp-season="%d"`, item.Season))
			}
			if item.SeasonName != "" {
				tags = append(tags, fmt.Sprintf(`tvp-season-name="%s"`, sanitizeTag(item.SeasonName)))
			}
			if item.Episode > 0 {
				tags = append(tags, fmt.Sprintf(`tvp-episode="%d"`, item.Episode))
			}
		case scanner.TypeFiles:
			if item.Group != "" {
				tags = append(tags, fmt.Sprintf(`group-title="%s"`, sanitizeTag(item.Group)))
			}
		}

		if p := probeCache.Get(item.Path); p != nil {
			if p.VideoCodec != "" {
				tags = append(tags, fmt.Sprintf(`tvp-vcodec="%s"`, p.VideoCodec))
			}
			if p.AudioCodec != "" {
				tags = append(tags, fmt.Sprintf(`tvp-acodec="%s"`, p.AudioCodec))
			}
			if p.Resolution != "" {
				tags = append(tags, fmt.Sprintf(`tvp-resolution="%s"`, p.Resolution))
			}
			if p.AudioLayout != "" {
				tags = append(tags, fmt.Sprintf(`tvp-audio="%s"`, p.AudioLayout))
			}
			if p.Duration > 0 {
				tags = append(tags, fmt.Sprintf(`tvp-duration="%.0f"`, p.Duration))
			}
			if p.Container != "" {
				tags = append(tags, fmt.Sprintf(`tvp-container="%s"`, p.Container))
			}
			if len(p.AudioTracks) > 1 {
				var trackParts []string
				for _, t := range p.AudioTracks {
					part := t.Codec
					if t.Language != "" {
						part += ":" + t.Language
					}
					if t.Layout != "" {
						part += ":" + t.Layout
					}
					trackParts = append(trackParts, part)
				}
				tags = append(tags, fmt.Sprintf(`tvp-audio-tracks="%s"`, strings.Join(trackParts, "|")))
			}
		}

		fmt.Fprintf(w, "#EXTINF:-1 %s,%s\n", strings.Join(tags, " "), sanitizeTag(displayName))
		fmt.Fprintln(w, streamURL)
	}
}
