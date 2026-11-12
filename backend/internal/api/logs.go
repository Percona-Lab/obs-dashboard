package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// logHistory is how many past lines a new viewer receives first.
const logHistory = 500

// LogSource is what the log stream needs from the sink.
type LogSource interface {
	Subscribe(n int) (tail []string, lines <-chan string, cancel func())
}

// logStreamHandler serves the server log as server-sent events: one "reset"
// event holding the recent lines, then one "line" event per new line.
func logStreamHandler(src LogSource) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("X-Accel-Buffering", "no")
		w.Header().Set("Connection", "keep-alive")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}

		tail, lines, cancel := src.Subscribe(logHistory)
		defer cancel()

		if tail == nil {
			tail = []string{}
		}
		reset, err := json.Marshal(tail)
		if err != nil {
			http.Error(w, "encode log", http.StatusInternalServerError)
			return
		}
		fmt.Fprintf(w, "event: reset\ndata: %s\n\n", reset)
		flusher.Flush()

		heartbeat := time.NewTicker(25 * time.Second)
		defer heartbeat.Stop()

		for {
			select {
			case <-r.Context().Done():
				return
			case <-heartbeat.C:
				fmt.Fprint(w, ": ping\n\n")
				flusher.Flush()
			case line, ok := <-lines:
				if !ok {
					return
				}
				payload, err := json.Marshal(line)
				if err != nil {
					continue
				}
				fmt.Fprintf(w, "event: line\ndata: %s\n\n", payload)
				flusher.Flush()
			}
		}
	}
}
