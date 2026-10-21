package chat

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// The operator assigns names by editing names.json on the server; these tests cover
// that contract: prefix keys, full-id keys, live reload and failure handling.

func writeNames(t *testing.T, dir, body string) {
	t.Helper()

	path := filepath.Join(dir, namesFile)

	// Bump the mtime so the reload check cannot miss a same-second write.
	time.Sleep(10 * time.Millisecond)

	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write names.json: %v", err)
	}
}

func TestNamesFileIsCreatedAsTemplate(t *testing.T) {
	dir := t.TempDir()

	LoadNameOverrides(dir)

	data, err := os.ReadFile(filepath.Join(dir, namesFile))
	if err != nil {
		t.Fatalf("expected a template file: %v", err)
	}

	if string(data) == "" {
		t.Fatal("template should not be empty")
	}
}

func TestNameOverrideByPrefix(t *testing.T) {
	dir := t.TempDir()
	overrides := LoadNameOverrides(dir)
	overrides.interval = 0

	sessionID := "07444cf7df367f8e9c517a7584405f91"
	writeNames(t, dir, `{"names": {"07444": "张三"}}`)

	name, ok := overrides.Lookup(sessionID)

	if !ok || name != "张三" {
		t.Fatalf("lookup = %q/%v, want 张三/true", name, ok)
	}
}

func TestNameOverrideByFullSessionID(t *testing.T) {
	dir := t.TempDir()
	overrides := LoadNameOverrides(dir)
	overrides.interval = 0

	sessionID := "a1b2c3d4e5f60718293a4b5c6d7e8f90"
	writeNames(t, dir, `{"names": {"a1b2c3d4e5f60718293a4b5c6d7e8f90": "张三"}}`)
	if name, ok := overrides.Lookup(sessionID); !ok || name != "张三" {
		t.Fatalf("lookup = %q/%v", name, ok)
	}

	// The same prefix must not leak onto a different session.
	if _, ok := overrides.Lookup("a1b2cfffffffffffffffffffffffffff"); ok {
		t.Fatal("a full-id key must not match another session sharing the prefix")
	}
}

func TestNameOverrideAppliesToProfile(t *testing.T) {
	dir := t.TempDir()
	store := NewSessionStore(dir)
	store.names.interval = 0

	cookieValue := store.sign("07444cf7df367f8e9c517a7584405f91")
	writeNames(t, dir, `{"names": {"07444": "张三"}}`)
	sessionID, ok := store.parse(cookieValue)
	if !ok {
		t.Fatal("cookie should parse")
	}

	profile := store.profileFor(sessionID)

	if profile.Name != "张三" {
		t.Fatalf("name = %q, want 张三", profile.Name)
	}

	if profile.Emoji == "" {
		t.Fatal("emoji should still be derived")
	}

	if profile.Display() != profile.Emoji+" 张三 #07444" {
		t.Fatalf("display = %q", profile.Display())
	}
}

func TestCorruptNamesFileKeepsPreviousMapping(t *testing.T) {
	dir := t.TempDir()
	overrides := LoadNameOverrides(dir)
	overrides.interval = 0

	sessionID := "07444cf7df367f8e9c517a7584405f91"
	writeNames(t, dir, `{"names": {"07444": "张三"}}`)
	if _, ok := overrides.Lookup(sessionID); !ok {
		t.Fatal("expected the first mapping to load")
	}

	// A half-written file must not wipe the names already in effect.
	writeNames(t, dir, `{"names": {"07444": `)
	if name, ok := overrides.Lookup(sessionID); !ok || name != "张三" {
		t.Fatalf("mapping lost after a corrupt save: %q/%v", name, ok)
	}
}

func TestUnknownSessionStaysDerived(t *testing.T) {
	dir := t.TempDir()
	store := NewSessionStore(dir)
	store.names.interval = 0

	writeNames(t, dir, `{"names": {"zzzzz": "别人"}}`)
	sessionID := "07444cf7df367f8e9c517a7584405f91"
	profile := store.profileFor(sessionID)

	if profile.Name == "别人" {
		t.Fatal("an unrelated prefix must not rename this session")
	}

	if profile.Name != HERO_POOL[roleIndexFor(sessionID)].Name {
		t.Fatalf("name = %q, want the derived default", profile.Name)
	}
}

// A rename must reach connected clients, otherwise the renamed person keeps seeing
// their old name until they reload.
func TestRenameNotifiesConnectedClients(t *testing.T) {
	srv, hub, store := newTestServer(t)

	sessionID := "07444cf7df367f8e9c517a7584405f91"

	// The callback runs on its own goroutine, so guard the slice it appends to.
	var (
		mu       sync.Mutex
		notified [][2]string
	)

	store.OnNamesChanged(func(prefix, display string) {
		mu.Lock()
		notified = append(notified, [2]string{prefix, display})
		mu.Unlock()

		hub.BroadcastProfile(prefix, display)
	})

	conn := dial(t, srv)
	readUntil(t, conn, "snapshot")

	dir := store.names.path
	if dir == "" {
		t.Fatal("expected a names path")
	}

	writeNames(t, filepath.Dir(dir), `{"names": {"07444": "李四"}}`)

	// Force the reload window open, then trigger a lookup the way a request would.
	store.names.interval = 0
	if _, err := http.Get(srv.URL + "/api/session"); err != nil {
		t.Fatalf("session request: %v", err)
	}

	deadline := time.Now().Add(3 * time.Second)

	for {
		mu.Lock()
		count := len(notified)
		mu.Unlock()

		if count > 0 || time.Now().After(deadline) {
			if count == 0 {
				t.Fatal("rename did not produce a notification")
			}

			break
		}

		time.Sleep(10 * time.Millisecond)
	}

	// The client should receive a profile event carrying the new display name.
	msg := readUntil(t, conn, "profile")

	if msg.Name == "" || !strings.Contains(msg.Name, "李四") {
		t.Fatalf("profile event = %+v", msg)
	}

	if msg.ClientId != "07444" {
		t.Fatalf("profile prefix = %q, want 07444", msg.ClientId)
	}

	_ = sessionID
}

// A renamed client that reconnects must learn its new name from the snapshot,
// otherwise an already-open tab keeps showing the old one.
func TestSnapshotCarriesOwnRenamedProfile(t *testing.T) {
	srv, _, store := newTestServer(t)

	sessionID := "07444cf7df367f8e9c517a7584405f91"
	cookie := store.sign(sessionID)

	// Rename before the client connects.
	dir := filepath.Dir(store.names.path)
	writeNames(t, dir, `{"names": {"07444": "李四"}}`)
	store.names.interval = 0

	conn := dialWith(t, srv, &http.Cookie{Name: sessionCookie, Value: cookie})
	snapshot := readUntil(t, conn, "snapshot")

	if snapshot.Me == nil {
		t.Fatal("snapshot should carry the client's own profile")
	}

	if snapshot.Me.Name != "李四" {
		t.Fatalf("me.name = %q, want 李四", snapshot.Me.Name)
	}

	if snapshot.Me.SessionID != sessionID {
		t.Fatalf("me.sessionId = %q", snapshot.Me.SessionID)
	}
}

// A rename must apply to messages sent on an existing connection: the name is
// resolved per message, not frozen at handshake time.
func TestRenameAppliesToExistingConnection(t *testing.T) {
	srv, _, store := newTestServer(t)

	sessionID := "07444cf7df367f8e9c517a7584405f91"
	cookie := &http.Cookie{Name: sessionCookie, Value: store.sign(sessionID)}

	conn := dialWith(t, srv, cookie)
	readUntil(t, conn, "snapshot")

	// Send one message under the derived name.
	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"chat","content":"before"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}

	first := readUntil(t, conn, "chat")
	if strings.Contains(first.Sender, "李四") {
		t.Fatalf("unexpected override before the rename: %q", first.Sender)
	}

	// Rename while the connection stays open.
	writeNames(t, filepath.Dir(store.names.path), `{"names": {"07444": "李四"}}`)
	store.names.interval = 0

	if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"chat","content":"after"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}

	second := readUntil(t, conn, "chat")

	if !strings.Contains(second.Sender, "李四") {
		t.Fatalf("sender after rename = %q, want it to contain 李四", second.Sender)
	}
}
