package chat

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strings"
)

// sessionCookie is the only identity this service trusts. It is httpOnly so page
// scripts cannot read or forge it, and SameSite=Lax so it survives normal
// navigation between tabs on the same site while staying out of cross-site posts.
const sessionCookie = "sid"

// Profile is the public shape of an identity, returned to the client and used as
// the sender on every broadcast message.
type Profile struct {
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
	Emoji     string `json:"emoji"`
	RoleIndex int    `json:"roleIndex"`
}

// Display is the name embedded in every broadcast message. The short id suffix
// keeps two sessions with the same role distinguishable.
func (p Profile) Display() string {
	suffix := p.SessionID
	if len(suffix) > 5 {
		suffix = suffix[:5]
	}

	return p.Emoji + " " + p.Name + " #" + suffix
}

// SessionStore owns cookie signing and identity derivation.
//
// Identity is immutable by design: the name is derived from the session id, so it
// survives restarts with no per-user storage, and nothing in the API lets a client
// change it. That is what makes "one person, one identity" hold.
type SessionStore struct {
	secret []byte
	// names holds operator-assigned display names; nil means derive everything.
	names *NameOverrides
}

func NewSessionStore(dir string) *SessionStore {
	return &SessionStore{
		secret: loadOrCreateSecret(dir),
		names:  LoadNameOverrides(dir),
	}
}

func (s *SessionStore) sign(sessionID string) string {
	mac := hmac.New(sha256.New, s.secret)
	mac.Write([]byte(sessionID))

	return sessionID + "." + hex.EncodeToString(mac.Sum(nil))
}

// parse returns the session id when the signature is valid.
func (s *SessionStore) parse(value string) (string, bool) {
	sessionID, signature, ok := strings.Cut(value, ".")
	if !ok || len(sessionID) != 32 || signature == "" {
		return "", false
	}

	expected := s.sign(sessionID)[len(sessionID)+1:]
	if !hmac.Equal([]byte(signature), []byte(expected)) {
		return "", false
	}

	return sessionID, true
}

// profileFor derives the profile from the session id, so the same browser keeps the
// same name and colour across reconnects, restarts and deploys.
func (s *SessionStore) profileFor(sessionID string) Profile {
	idx := roleIndexFor(sessionID)
	role := HERO_POOL[idx]

	// An operator-assigned name wins over the derived one. Everything else (emoji,
	// colour) still comes from the session, so the identity stays recognisable.
	if s.names != nil {
		if name, ok := s.names.Lookup(sessionID); ok {
			return Profile{
				SessionID: sessionID,
				Name:      name,
				Emoji:     role.Emoji,
				RoleIndex: idx,
			}
		}
	}

	return Profile{
		SessionID: sessionID,
		Name:      role.Name,
		Emoji:     role.Emoji,
		RoleIndex: idx,
	}
}

// DisplayName resolves the current sender label for a session. History rows store
// a sender string from the time they were written; resolving through this method
// at read time makes an operator rename apply to old messages too, without a
// database migration or per-row writes.
func (s *SessionStore) DisplayName(sessionID string) string {
	if sessionID == "" {
		return ""
	}

	return s.profileFor(sessionID).Display()
}

// OnNamesChanged forwards name-file changes so the transport layer can tell clients.
func (s *SessionStore) OnNamesChanged(fn func(prefix, display string)) {
	if s.names == nil {
		return
	}

	s.names.SetOnChange(func(changed map[string]string) {
		for key := range changed {
			if len(key) >= 32 {
				// Full ids are rare in the file; resolve through the derived profile to
				// get the emoji/prefix formatting the client expects.
				fn(key[:5], s.profileFor(key).Display())

				continue
			}

			fn(key, s.profileFor(key).Display())
		}
	})
}

// readProfile resolves the request's identity without issuing a new cookie.
func (s *SessionStore) readProfile(r *http.Request) (Profile, bool) {
	cookie, err := r.Cookie(sessionCookie)
	if err != nil {
		return Profile{}, false
	}

	sessionID, ok := s.parse(cookie.Value)
	if !ok {
		return Profile{}, false
	}

	return s.profileFor(sessionID), true
}

func (s *SessionStore) issue(w http.ResponseWriter) Profile {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		log.Fatalf("无法生成会话标识: %v", err)
	}

	sessionID := hex.EncodeToString(buf)

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    s.sign(sessionID),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		// SameSite=none would require Secure=true; the deployment terminates TLS
		// at the proxy, so keep Lax and let the browser send it on same-site
		// requests and navigations.
		MaxAge: 60 * 60 * 24 * 180,
	})

	return s.profileFor(sessionID)
}

// ensureProfile returns the current identity, issuing a cookie on first visit.
func (s *SessionStore) ensureProfile(w http.ResponseWriter, r *http.Request) Profile {
	if profile, ok := s.readProfile(r); ok {
		return profile
	}

	return s.issue(w)
}

func (s *SessionStore) routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/session", func(w http.ResponseWriter, r *http.Request) {
		profile := s.ensureProfile(w, r)
		writeJSON(w, http.StatusOK, profile)
	})

}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)

	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Println("写响应失败:", err)
	}
}

// roleIndexFor derives a stable role from the session id.
func roleIndexFor(sessionID string) int {
	sum := sha256.Sum256([]byte(sessionID))

	return int(sum[0]) % len(HERO_POOL)
}
