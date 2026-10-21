package chat

import (
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// routes builds the handler tree separately from main so tests can mount it on
// httptest.Server without binding a real port.
// Deps are the collaborators the HTTP layer needs.
type Deps struct {
	Hub      *Hub
	Sessions *SessionStore
	Media    *MediaStore
	StaticFS fs.FS
}

func Routes(deps Deps) http.Handler {
	hub, store, staticFS := deps.Hub, deps.Sessions, deps.StaticFS

	fileServer := http.FileServer(http.FS(staticFS))
	mux := http.NewServeMux()

	store.routes(mux)
	mediaRoutes(mux, deps.Media, store)

	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		// The session cookie is the only identity. Without it there is nothing to
		// attribute messages to, so refuse the handshake instead of inventing a name.
		profile, ok := store.readProfile(r)
		if !ok {
			http.Error(w, "session required", http.StatusUnauthorized)
			return
		}

		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			log.Println("WS Upgrade 失败:", err)
			return
		}

		client := &Client{
			hub:       hub,
			conn:      conn,
			send:      make(chan []byte, sendBufferSize),
			profile:   profile,
			since:     parseSince(r.URL.Query().Get("since")),
			limiter:   hub.RateLimit().newLimiter(),
			sessionID: profile.SessionID,
			resolve:   store.profileFor,
			media:     deps.Media,
		}

		// Same reason as in readPump: after Shutdown, nobody drains register.
		select {
		case hub.register <- client:
		case <-hub.done:
			conn.Close()
			return
		}

		go client.writePump()
		go client.readPump()
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		// The SPA fallback must not swallow unknown API paths: a typo or a removed
		// endpoint should answer 404 JSON, not index.html with status 200.
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})

			return
		}

		path := strings.TrimPrefix(r.URL.Path, "/")
		if path == "" {
			path = "index.html"
		}

		if _, err := fs.Stat(staticFS, path); err != nil {
			// 文件不存在（例如刷新子路由），回退到 index.html 交给前端路由接管
			r.URL.Path = "/"
		}

		fileServer.ServeHTTP(w, r)
	})

	return mux
}

// allowedOrigins reads a comma-separated whitelist. An empty value means
// same-host only, which is what a browser sends for a first-party page.
// parseSince reads the reconnect cursor; anything unparsable means "new client".
func parseSince(value string) uint64 {
	if value == "" {
		return 0
	}

	seq, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0
	}

	return seq
}

func allowedOrigins() map[string]bool {
	list := map[string]bool{}

	for _, origin := range strings.Split(os.Getenv("ALLOWED_ORIGINS"), ",") {
		origin = strings.TrimSpace(strings.TrimSuffix(origin, "/"))
		if origin != "" {
			list[origin] = true
		}
	}

	return list
}
