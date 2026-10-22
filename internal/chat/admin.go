package chat

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
)

// adminRoutes exposes the minimum moderation primitive: delete one message by
// sequence number. It is disabled entirely unless ADMIN_TOKEN is set.
func adminRoutes(mux *http.ServeMux, hub *Hub, history *HistoryStore, media *MediaStore, token string) {
	if token == "" {
		return
	}

	mux.HandleFunc("POST /api/admin/delete", func(w http.ResponseWriter, r *http.Request) {
		provided := r.Header.Get("X-Admin-Token")
		if subtle.ConstantTimeCompare([]byte(provided), []byte(token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid admin token"})

			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, 1024)

		var req struct {
			Seq uint64 `json:"seq"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Seq == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "seq is required"})

			return
		}

		// Drop the attached image before the message row disappears, so the direct
		// /media/{id} URL cannot outlive a moderated message.
		if history != nil && media != nil {
			if msg, ok, err := history.Message(req.Seq); err == nil && ok && msg.Type == "image" {
				if err := media.Delete(msg.Content); err != nil {
					// The message is still removed below; log-only for the attachment.
					writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "delete media failed"})

					return
				}
			}
		}

		hub.Delete(req.Seq)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "seq": req.Seq})
	})
}
