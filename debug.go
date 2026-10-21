package main

import (
	"encoding/json"
	"log"
	"net/http"
	"net/http/pprof"
	"runtime"

	"github.com/ieuforu/comic/internal/chat"
)

// debugRoutes serves profiling on a separate listener. Exposing pprof on the
// public port would let anyone CPU-sample the process (and /debug/pprof/cmdline
// leaks arguments), so it is opt-in through DEBUG_ADDR and meant for localhost or
// a private network only.
func debugRoutes(hub *chat.Hub) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	// A tiny JSON snapshot: easier to read in a load test than pprof output.
	mux.HandleFunc("/debug/stats", func(w http.ResponseWriter, r *http.Request) {
		var mem runtime.MemStats

		runtime.ReadMemStats(&mem)

		stats := map[string]any{
			"goroutines":  runtime.NumGoroutine(),
			"connections": hub.ConnectionCount(),
			"users":       hub.UserCount(),
			"heapAllocMB": float64(mem.HeapAlloc) / (1 << 20),
			"sysMB":       float64(mem.Sys) / (1 << 20),
			"numGC":       mem.NumGC,
		}

		w.Header().Set("Content-Type", "application/json")

		if err := json.NewEncoder(w).Encode(stats); err != nil {
			log.Println("写 stats 失败:", err)
		}
	})

	return mux
}
