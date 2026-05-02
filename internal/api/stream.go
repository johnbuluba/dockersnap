package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/johnbuluba/dockersnap/internal/instance"
)

// wantsStream returns true if the client requested NDJSON streaming.
func wantsStream(r *http.Request) bool {
	accept := r.Header.Get("Accept")
	return accept == "application/x-ndjson"
}

// streamProgress reads events from ch and writes them as newline-delimited JSON.
// It flushes after each event. Blocks until ch is closed.
func streamProgress(w http.ResponseWriter, ch <-chan instance.ProgressEvent) {
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)

	flusher, _ := w.(http.Flusher)

	for event := range ch {
		data, err := json.Marshal(event)
		if err != nil {
			continue
		}
		fmt.Fprintf(w, "%s\n", data)
		if flusher != nil {
			flusher.Flush()
		}
	}
}
