package chat

import (
	"io/fs"
	"log"
	"net/http"
	"os"
	"path"
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
	History  *HistoryStore
	StaticFS fs.FS
	// IndexHTML is the SPA shell with the build id injected. When nil, Routes reads
	// index.html from StaticFS (which keeps tests and small embeddings working).
	IndexHTML []byte
}

func Routes(deps Deps) http.Handler {
	hub, store, staticFS := deps.Hub, deps.Sessions, deps.StaticFS

	indexHTML := deps.IndexHTML
	if indexHTML == nil {
		if data, err := fs.ReadFile(staticFS, "index.html"); err == nil {
			indexHTML = data
		}
	}

	fileServer := http.FileServer(http.FS(staticFS))
	mux := http.NewServeMux()

	serveIndex := func(w http.ResponseWriter, r *http.Request) {
		if indexHTML == nil {
			http.NotFound(w, r)

			return
		}

		// The shell must never be cached: it is the only place that points at the
		// hashed assets, so a stale copy would keep an old page alive across deploys.
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("X-Content-Type-Options", "nosniff")

		if r.Method == http.MethodHead {
			return
		}

		_, _ = w.Write(indexHTML)
	}

	store.routes(mux)
	historyRoutes(mux, deps.History, store)
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
		if r.URL.Path == "/api" || strings.HasPrefix(r.URL.Path, "/api/") {
			writeJSON(w, http.StatusNotFound, map[string]string{"error": "not found"})

			return
		}

		rel := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if rel == "" || rel == "index.html" {
			serveIndex(w, r)

			return
		}

		info, err := fs.Stat(staticFS, rel)
		if err == nil && info.IsDir() {
			http.NotFound(w, r)

			return
		}

		if err != nil {
			// Missing asset must 404: returning index.html would make the browser
			// parse HTML as JavaScript and hide the real problem.
			if strings.HasPrefix(rel, "assets/") || path.Ext(rel) != "" {
				http.NotFound(w, r)

				return
			}

			// Missing extension-less path (a client-side route): let the SPA handle it.
			serveIndex(w, r)

			return
		}

		if strings.HasPrefix(rel, "assets/") {
			// Vite fingerprints these names, so the bytes at a URL never change.
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=3600")
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
