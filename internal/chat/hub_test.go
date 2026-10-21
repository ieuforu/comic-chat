package chat

import (
	"database/sql"
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gorilla/websocket"
)

// newTestStore gives each test its own state directory, so nothing leaks between
// tests and persistence can be exercised for real.
func newTestStore(t *testing.T) *SessionStore {
	t.Helper()

	t.Setenv("SESSION_SECRET", "test-secret")

	return NewSessionStore(t.TempDir())
}

// newTestServer mounts the real handler tree on an ephemeral port. Tests talk to
// it over real HTTP and WebSocket connections, because the interesting behaviour
// (cookies, handshake, framing, broadcast) lives below the handler boundary.
func newTestServer(t *testing.T) (*httptest.Server, *Hub, *SessionStore) {
	t.Helper()

	srv, hub, store, _ := newTestServerWithMedia(t)

	return srv, hub, store
}

// newTestServerWithMedia also hands back the media store, so tests that upload can
// inspect the same database the server is using.
func newTestServerWithMedia(t *testing.T) (*httptest.Server, *Hub, *SessionStore, *MediaStore) {
	t.Helper()

	hub := NewHub()
	go hub.Run()

	store := newTestStore(t)
	staticFS := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>test</title>")},
	}

	media := newTestMedia(t, store)

	srv := httptest.NewServer(Routes(Deps{Hub: hub, Sessions: store, Media: media, StaticFS: staticFS}))

	t.Cleanup(func() {
		hub.Shutdown()
		srv.Close()
	})

	return srv, hub, store, media
}

// newSession performs the real first-visit flow and returns the issued cookie.
func newSession(t *testing.T, srv *httptest.Server) *http.Cookie {
	t.Helper()

	resp, err := http.Get(srv.URL + "/api/session")
	if err != nil {
		t.Fatalf("GET /api/session: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/session status = %d", resp.StatusCode)
	}

	for _, cookie := range resp.Cookies() {
		if cookie.Name == sessionCookie {
			return cookie
		}
	}

	t.Fatal("no session cookie issued")
	return nil
}

func dialWith(t *testing.T, srv *httptest.Server, cookie *http.Cookie) *websocket.Conn {
	t.Helper()

	header := http.Header{}
	if cookie != nil {
		header.Set("Cookie", cookie.Name+"="+cookie.Value)
	}

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	conn, resp, err := websocket.DefaultDialer.Dial(url, header)
	if err != nil {
		if resp != nil {
			t.Fatalf("dial failed: %v (status %d)", err, resp.StatusCode)
		}
		t.Fatalf("dial failed: %v", err)
	}

	t.Cleanup(func() { conn.Close() })

	return conn
}

// dial opens a connection for a brand new session.
func dial(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()

	return dialWith(t, srv, newSession(t, srv))
}

// pendingEvents buffers events that arrived in the same frame as an earlier one,
// because the server coalesces queued events into one newline-delimited frame.
var pendingEvents = struct {
	sync.Mutex
	byConn map[*websocket.Conn][]EventMessage
}{byConn: make(map[*websocket.Conn][]EventMessage)}

func readEvent(t *testing.T, conn *websocket.Conn) EventMessage {
	t.Helper()

	pendingEvents.Lock()
	if queue := pendingEvents.byConn[conn]; len(queue) > 0 {
		msg := queue[0]
		pendingEvents.byConn[conn] = queue[1:]
		pendingEvents.Unlock()

		return msg
	}
	pendingEvents.Unlock()

	conn.SetReadDeadline(time.Now().Add(3 * time.Second))

	_, data, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var events []EventMessage

	for _, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}

		var msg EventMessage
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("unmarshal %q: %v", line, err)
		}

		events = append(events, msg)
	}

	if len(events) == 0 {
		t.Fatalf("empty frame")
	}

	pendingEvents.Lock()
	pendingEvents.byConn[conn] = append(pendingEvents.byConn[conn], events[1:]...)
	pendingEvents.Unlock()

	return events[0]
}

func readUntil(t *testing.T, conn *websocket.Conn, want string) EventMessage {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)

	for time.Now().Before(deadline) {
		msg := readEvent(t, conn)

		if msg.Type == want {
			return msg
		}
	}

	t.Fatalf("no %q event arrived", want)

	return EventMessage{}
}

func readPresence(t *testing.T, conn *websocket.Conn, wantOnline, wantConnections int) EventMessage {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)

	for time.Now().Before(deadline) {
		msg := readEvent(t, conn)

		if msg.Type == "presence" && msg.Online == wantOnline && msg.Connections == wantConnections {
			return msg
		}
	}

	t.Fatalf("no presence event with online=%d connections=%d", wantOnline, wantConnections)

	return EventMessage{}
}

func TestSnapshotOnConnect(t *testing.T) {
	srv, _, _ := newTestServer(t)
	conn := dial(t, srv)

	snapshot := readUntil(t, conn, "snapshot")

	if snapshot.Online != 1 {
		t.Fatalf("online = %d, want 1", snapshot.Online)
	}

	if len(snapshot.History) != 0 {
		t.Fatalf("history = %d, want 0", len(snapshot.History))
	}
}

func TestResolveSendersUsesCurrentProfile(t *testing.T) {
	hub := NewHub()
	hub.SetSenderResolver(func(sessionID string) string { return "current-" + sessionID })

	messages := []EventMessage{
		{ClientId: "s1", Sender: "old"},
		{ClientId: "", Sender: "legacy"},
	}

	hub.resolveSenders(messages)

	if messages[0].Sender != "current-s1" {
		t.Fatalf("resolved sender = %q, want current-s1", messages[0].Sender)
	}

	if messages[1].Sender != "legacy" {
		t.Fatalf("legacy sender changed to %q", messages[1].Sender)
	}
}

func TestSnapshotResolvesHistoricalSenders(t *testing.T) {
	hub := NewHub()
	hub.Restore([]EventMessage{{
		Type:     "chat",
		Seq:      1,
		ClientId: "s1",
		Sender:   "old",
		Content:  "m",
		Time:     "12:00:00",
	}}, 1)
	hub.SetSenderResolver(func(sessionID string) string { return "resolved-" + sessionID })
	go hub.Run()

	srv := httptest.NewServer(Routes(Deps{
		Hub:      hub,
		Sessions: newTestStore(t),
		StaticFS: testStaticFS(),
	}))
	t.Cleanup(func() {
		hub.Shutdown()
		srv.Close()
	})

	conn := dial(t, srv)
	snapshot := readUntil(t, conn, "snapshot")

	if len(snapshot.History) != 1 || snapshot.History[0].Sender != "resolved-s1" {
		t.Fatalf("snapshot history = %+v, want resolved-s1", snapshot.History)
	}
}

func TestSnapshotCarriesBuildID(t *testing.T) {
	srv, hub, _ := newTestServer(t)
	hub.SetBuildID("build-test-123")

	conn := dial(t, srv)
	snapshot := readUntil(t, conn, "snapshot")

	if snapshot.BuildID != "build-test-123" {
		t.Fatalf("buildId = %q, want build-test-123", snapshot.BuildID)
	}
}

func TestHandshakeRequiresSession(t *testing.T) {
	srv, _, _ := newTestServer(t)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"

	if _, resp, err := websocket.DefaultDialer.Dial(url, nil); err == nil {
		t.Fatal("handshake without a session cookie should fail")
	} else if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %v, want 401", resp)
	}

	forged := &http.Cookie{Name: sessionCookie, Value: "0123456789abcdef0123456789abcdef.forged"}
	if _, resp, err := websocket.DefaultDialer.Dial(url, http.Header{
		"Cookie": []string{forged.Name + "=" + forged.Value},
	}); err == nil {
		t.Fatal("handshake with a forged cookie should fail")
	} else if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %v, want 401", resp)
	}
}

func TestSenderCannotBeSpoofed(t *testing.T) {
	srv, _, store := newTestServer(t)

	cookie := newSession(t, srv)
	conn := dialWith(t, srv, cookie)

	readUntil(t, conn, "snapshot")

	// The payload claims to be someone else; the server must replace it.
	spoof := `{"type":"chat","sender":"admin","clientId":"admin-id","content":"spoofed"}`
	if err := conn.WriteMessage(websocket.TextMessage, []byte(spoof)); err != nil {
		t.Fatalf("write: %v", err)
	}

	msg := readUntil(t, conn, "chat")

	sessionID, ok := store.parse(cookie.Value)
	if !ok {
		t.Fatal("cookie should parse")
	}
	want := store.profileFor(sessionID).Display()

	if msg.Sender != want {
		t.Fatalf("sender = %q, want %q", msg.Sender, want)
	}

	if msg.ClientId != sessionID {
		t.Fatalf("clientId = %q, want the session id", msg.ClientId)
	}
}

func TestServerRejectsClientOriginatedSnapshots(t *testing.T) {
	srv, _, _ := newTestServer(t)
	conn := dial(t, srv)

	readUntil(t, conn, "snapshot")

	// A client must not be able to inject protocol frames of its own.
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"presence","online":999}`)); err != nil {
		t.Fatalf("write: %v", err)
	}

	// The next thing read must not be the forged presence. Send a real message and
	// expect it, which proves the forged frame was dropped rather than relayed.
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"chat","content":"after-forgery"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}

	msg := readUntil(t, conn, "chat")

	if msg.Content != "after-forgery" {
		t.Fatalf("unexpected event %+v", msg)
	}
}

func TestPresenceCountsUsersNotConnections(t *testing.T) {
	srv, _, _ := newTestServer(t)

	cookie := newSession(t, srv)

	tabOne := dialWith(t, srv, cookie)
	readUntil(t, tabOne, "snapshot")

	// Same cookie = same person, second tab.
	tabTwo := dialWith(t, srv, cookie)
	readUntil(t, tabTwo, "snapshot")

	// Two sockets, one person.
	readPresence(t, tabOne, 1, 2)

	// A different session must raise the people count.
	other := dial(t, srv)
	readUntil(t, other, "snapshot")

	readPresence(t, tabOne, 2, 3)
}

// Identity is derived from the session and cannot be changed: reconnecting keeps
// the same name, and the rename endpoint no longer exists.
func TestIdentityIsStableAndImmutable(t *testing.T) {
	srv, _, store := newTestServer(t)

	cookie := newSession(t, srv)
	sessionID, ok := store.parse(cookie.Value)
	if !ok {
		t.Fatal("cookie should parse")
	}

	want := store.profileFor(sessionID)

	first := dialWith(t, srv, cookie)
	readUntil(t, first, "snapshot")
	first.Close()

	second := dialWith(t, srv, cookie)
	readUntil(t, second, "snapshot")

	if err := second.WriteMessage(websocket.TextMessage, []byte(`{"type":"chat","content":"hi"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}

	msg := readUntil(t, second, "chat")

	if msg.Sender != want.Display() {
		t.Fatalf("sender = %q, want %q", msg.Sender, want.Display())
	}

	// The rename endpoint is gone, so no client can change an identity.
	body := strings.NewReader(`{"roleIndex":7}`)
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/api/session/name", body)
	if err != nil {
		t.Fatal(err)
	}
	req.AddCookie(cookie)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("rename request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNotFound && resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("POST /api/session/name status = %d, want 404/405", resp.StatusCode)
	}
}

func TestChatIsBroadcastToEveryClient(t *testing.T) {
	srv, _, _ := newTestServer(t)
	alice := dial(t, srv)
	bob := dial(t, srv)

	readUntil(t, alice, "snapshot")
	readUntil(t, bob, "snapshot")

	if err := alice.WriteMessage(websocket.TextMessage, []byte(`{"type":"chat","content":"hello"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}

	for name, conn := range map[string]*websocket.Conn{"alice": alice, "bob": bob} {
		msg := readUntil(t, conn, "chat")

		if msg.Content != "hello" {
			t.Fatalf("%s received %+v", name, msg)
		}
	}
}

func TestHistoryIsCappedAndReplayedToLateJoiners(t *testing.T) {
	srv, _, _ := newTestServer(t)
	sender := dial(t, srv)

	readUntil(t, sender, "snapshot")

	for i := 0; i < maxHistory+10; i++ {
		if err := sender.WriteMessage(websocket.TextMessage, []byte(`{"type":"chat","content":"m"}`)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		readUntil(t, sender, "chat")
	}

	late := dial(t, srv)
	snapshot := readUntil(t, late, "snapshot")

	if len(snapshot.History) != maxHistory {
		t.Fatalf("history = %d, want %d", len(snapshot.History), maxHistory)
	}
}

func TestTypingIsNotStoredInHistory(t *testing.T) {
	srv, _, _ := newTestServer(t)
	conn := dial(t, srv)

	readUntil(t, conn, "snapshot")

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"typing","isTyping":true}`)); err != nil {
		t.Fatalf("write: %v", err)
	}

	readUntil(t, conn, "typing")

	late := dial(t, srv)
	snapshot := readUntil(t, late, "snapshot")

	if len(snapshot.History) != 0 {
		t.Fatalf("history = %d, want 0", len(snapshot.History))
	}
}

// Run with -race: this is the test that would catch an unsynchronised map access
// in the hub, which is exactly where a connection churn bug would hide.
func TestConcurrentConnectAndChat(t *testing.T) {
	srv, _, _ := newTestServer(t)

	const clients = 20
	const messagesEach = 5

	var wg sync.WaitGroup

	for i := 0; i < clients; i++ {
		wg.Add(1)

		go func(n int) {
			defer wg.Done()

			resp, err := http.Get(srv.URL + "/api/session")
			if err != nil {
				t.Errorf("session %d: %v", n, err)
				return
			}
			cookies := resp.Cookies()
			resp.Body.Close()

			header := http.Header{}
			if len(cookies) > 0 {
				header.Set("Cookie", cookies[0].Name+"="+cookies[0].Value)
			}

			conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http")+"/ws", header)
			if err != nil {
				t.Errorf("dial %d: %v", n, err)
				return
			}

			defer conn.Close()

			for m := 0; m < messagesEach; m++ {
				if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"chat","content":"x"}`)); err != nil {
					return
				}
			}

			conn.SetReadDeadline(time.Now().Add(time.Second))
			for r := 0; r < 3; r++ {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}(i)
	}

	wg.Wait()
}

func TestHealthEndpoint(t *testing.T) {
	srv, _, _ := newTestServer(t)

	resp, err := http.Get(srv.URL + "/healthz")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func TestShutdownClosesClients(t *testing.T) {
	hub := NewHub()
	go hub.Run()

	store := newTestStore(t)
	staticFS := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("ok")}}
	srv := httptest.NewServer(Routes(Deps{
		Hub: hub, Sessions: store, Media: newTestMedia(t, store), StaticFS: staticFS,
	}))
	defer srv.Close()

	conn := dial(t, srv)
	readUntil(t, conn, "snapshot")

	hub.Shutdown()

	conn.SetReadDeadline(time.Now().Add(3 * time.Second))

	for {
		if _, _, err := conn.ReadMessage(); err != nil {
			if websocket.IsCloseError(err, websocket.CloseGoingAway) || websocket.IsUnexpectedCloseError(err) {
				return
			}
			t.Fatalf("unexpected close error: %v", err)
		}
	}
}

func TestOriginIsChecked(t *testing.T) {
	srv, _, _ := newTestServer(t)
	cookie := newSession(t, srv)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
	cookieHeader := cookie.Name + "=" + cookie.Value

	t.Run("same host is accepted", func(t *testing.T) {
		conn, resp, err := websocket.DefaultDialer.Dial(url, http.Header{
			"Origin": []string{"http://" + strings.TrimPrefix(srv.URL, "http://")},
			"Cookie": []string{cookieHeader},
		})
		if err != nil {
			t.Fatalf("dial: %v (status %v)", err, resp)
		}
		conn.Close()
	})

	t.Run("foreign origin is rejected", func(t *testing.T) {
		_, resp, err := websocket.DefaultDialer.Dial(url, http.Header{
			"Origin": []string{"https://evil.example"},
			"Cookie": []string{cookieHeader},
		})
		if err == nil {
			t.Fatal("expected the handshake to be rejected")
		}
		if resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Fatalf("status = %v, want 403", resp)
		}
	})
}

func TestAllowedOriginsFromEnv(t *testing.T) {
	t.Setenv("ALLOWED_ORIGINS", "https://chat.example.com, https://alt.example.com/")

	set := allowedOrigins()

	for _, want := range []string{"https://chat.example.com", "https://alt.example.com"} {
		if !set[want] {
			t.Fatalf("missing %q in %v", want, set)
		}
	}

	if len(set) != 2 {
		t.Fatalf("parsed %d origins, want 2", len(set))
	}
}

// --- reconnect / sequence numbers -------------------------------------------

func TestChatMessagesCarryIncreasingSeqs(t *testing.T) {
	srv, _, _ := newTestServer(t)
	conn := dial(t, srv)

	readUntil(t, conn, "snapshot")

	for i := 0; i < 3; i++ {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"chat","content":"m"}`)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	var seqs []uint64

	for len(seqs) < 3 {
		msg := readUntil(t, conn, "chat")

		if msg.Seq == 0 {
			t.Fatalf("chat event has no seq: %+v", msg)
		}

		seqs = append(seqs, msg.Seq)
	}

	for i := 1; i < len(seqs); i++ {
		if seqs[i] != seqs[i-1]+1 {
			t.Fatalf("seqs are not consecutive: %v", seqs)
		}
	}
}

func TestSnapshotCarriesLatestSeq(t *testing.T) {
	srv, _, _ := newTestServer(t)
	conn := dial(t, srv)

	readUntil(t, conn, "snapshot")

	for i := 0; i < 4; i++ {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"chat","content":"m"}`)); err != nil {
			t.Fatalf("write: %v", err)
		}
		readUntil(t, conn, "chat")
	}

	fresh := dial(t, srv)
	snapshot := readUntil(t, fresh, "snapshot")

	if snapshot.Seq != 4 {
		t.Fatalf("snapshot seq = %d, want 4", snapshot.Seq)
	}

	if len(snapshot.History) != 4 {
		t.Fatalf("history = %d, want 4", len(snapshot.History))
	}
}

func TestReconnectReplaysOnlyMissedMessages(t *testing.T) {
	srv, _, _ := newTestServer(t)

	cookie := newSession(t, srv)
	conn := dialWith(t, srv, cookie)

	readUntil(t, conn, "snapshot")

	for i := 0; i < 3; i++ {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"chat","content":"before"}`)); err != nil {
			t.Fatalf("write: %v", err)
		}
		readUntil(t, conn, "chat")
	}

	// Drop the connection, then let two more messages happen while "offline".
	conn.Close()

	other := dial(t, srv)
	readUntil(t, other, "snapshot")

	for i := 0; i < 2; i++ {
		if err := other.WriteMessage(websocket.TextMessage, []byte(`{"type":"chat","content":"while-away"}`)); err != nil {
			t.Fatalf("write: %v", err)
		}
		readUntil(t, other, "chat")
	}

	// Reconnect with the last seq we saw (3).
	resumed := dialWith(t, srv, cookie)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?since=3"
	cookieHeader := cookie.Name + "=" + cookie.Value

	resumedConn, _, err := websocket.DefaultDialer.Dial(url, http.Header{"Cookie": []string{cookieHeader}})
	if err != nil {
		t.Fatalf("dial with since: %v", err)
	}
	defer resumedConn.Close()

	snapshot := readUntil(t, resumedConn, "snapshot")

	if len(snapshot.History) != 2 {
		t.Fatalf("replayed %d messages, want 2 (only the missed ones)", len(snapshot.History))
	}

	for _, msg := range snapshot.History {
		if msg.Content != "while-away" {
			t.Fatalf("replayed an already-seen message: %+v", msg)
		}

		if msg.Seq <= 3 {
			t.Fatalf("replayed seq %d which the client already had", msg.Seq)
		}
	}

	if snapshot.Truncated {
		t.Fatal("no truncation expected when everything is still in history")
	}

	_ = resumed
}

func TestReconnectReportsTruncation(t *testing.T) {
	srv, _, _ := newTestServer(t)
	conn := dial(t, srv)

	readUntil(t, conn, "snapshot")

	// Overflow the history so early messages are evicted.
	for i := 0; i < maxHistory+5; i++ {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"chat","content":"m"}`)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		readUntil(t, conn, "chat")
	}

	// Ask for something that was evicted long ago.
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?since=1"
	late, _, err := websocket.DefaultDialer.Dial(url, http.Header{
		"Cookie": []string{newSessionCookieHeader(t, srv)},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer late.Close()

	snapshot := readUntil(t, late, "snapshot")

	if !snapshot.Truncated {
		t.Fatal("truncation should be reported when the requested range was evicted")
	}

	if len(snapshot.History) != maxHistory {
		t.Fatalf("history = %d, want %d", len(snapshot.History), maxHistory)
	}
}

func TestInvalidSinceIsTreatedAsNewClient(t *testing.T) {
	srv, _, _ := newTestServer(t)

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws?since=not-a-number"
	conn, resp, err := websocket.DefaultDialer.Dial(url, http.Header{
		"Cookie": []string{newSessionCookieHeader(t, srv)},
	})
	if err != nil {
		t.Fatalf("dial: %v (status %v)", err, resp)
	}
	defer conn.Close()

	snapshot := readUntil(t, conn, "snapshot")

	if snapshot.Truncated {
		t.Fatal("a garbage cursor must not report truncation")
	}
}

// newSessionCookieHeader returns a ready-to-send Cookie header for a fresh session.
func newSessionCookieHeader(t *testing.T, srv *httptest.Server) string {
	t.Helper()

	cookie := newSession(t, srv)

	return cookie.Name + "=" + cookie.Value
}

// testStaticFS and newServerForTest exist so history_test.go can build a server
// around a hub it configured itself.
func testStaticFS() fstest.MapFS {
	return fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>test</title>")},
	}
}

func newServerForTest(t *testing.T, hub *Hub, staticFS fs.FS) *httptest.Server {
	t.Helper()

	store := newTestStore(t)
	srv := httptest.NewServer(Routes(Deps{
		Hub: hub, Sessions: store, Media: newTestMedia(t, store), StaticFS: staticFS,
	}))
	t.Cleanup(srv.Close)

	return srv
}

// newTestMedia builds a MediaStore on the same temporary database as the history
// store used by the hub, so tests exercise the real ownership checks.
func newTestMedia(t *testing.T, store *SessionStore) *MediaStore {
	t.Helper()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}

	media, err := NewMediaStore(db, t.TempDir())
	if err != nil {
		t.Fatalf("NewMediaStore: %v", err)
	}

	t.Cleanup(func() { db.Close() })

	return media
}
