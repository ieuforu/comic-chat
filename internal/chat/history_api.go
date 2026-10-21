package chat

import (
	"log"
	"net/http"
	"strconv"
)

// historyPage is the JSON shape of GET /api/history. Messages are ascending, so
// the client can prepend the page directly.
type historyPage struct {
	Messages []EventMessage `json:"messages"`
	HasMore  bool           `json:"hasMore"`
}

// historyRoutes exposes the database-backed history that the hub no longer keeps
// in memory. The snapshot still carries the newest maxHistory messages; this
// endpoint pages backwards from the oldest seq a client already has.
func historyRoutes(mux *http.ServeMux, history *HistoryStore, sessions *SessionStore) {
	mux.HandleFunc("GET /api/history", func(w http.ResponseWriter, r *http.Request) {
		// History is chat content, so treat it like the WebSocket: a session is
		// required rather than allowing anonymous bulk reads.
		if _, ok := sessions.readProfile(r); !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "session required"})

			return
		}

		before, err := strconv.ParseUint(r.URL.Query().Get("before"), 10, 64)
		if err != nil || before == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "before must be a positive seq"})

			return
		}

		limit := historyPageDefault
		if raw := r.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed <= 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "limit must be a positive number"})

				return
			}

			limit = parsed
		}

		if history == nil {
			writeJSON(w, http.StatusOK, historyPage{Messages: []EventMessage{}, HasMore: false})

			return
		}

		messages, hasMore, err := history.LoadBefore(before, limit)
		if err != nil {
			log.Printf("读取更早历史失败: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "history unavailable"})

			return
		}

		writeJSON(w, http.StatusOK, historyPage{Messages: messages, HasMore: hasMore})
	})
}
