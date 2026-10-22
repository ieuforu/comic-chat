package chat

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

// newTestHistory opens a store backed by a temp file, so "restart" can be simulated
// by opening the same path again.
func newTestHistory(t *testing.T, keep int) (*HistoryStore, string) {
	t.Helper()

	path := filepath.Join(t.TempDir(), "history.db")

	store, err := OpenHistory(path, keep)
	if err != nil {
		t.Fatalf("OpenHistory: %v", err)
	}

	return store, path
}

func msg(seq uint64, content string) EventMessage {
	return EventMessage{
		Type:     "chat",
		Seq:      seq,
		ClientId: "session-1",
		Sender:   "⚡ VOLT #abcde",
		Content:  content,
		Time:     "12:00:00",
	}
}

func TestHistoryPersistsAcrossRestart(t *testing.T) {
	store, path := newTestHistory(t, 0)

	for i := uint64(1); i <= 3; i++ {
		store.Append(msg(i, "m"))
	}

	// Close flushes the queue; a restart must see everything it acknowledged.
	if err := store.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	reopened, err := OpenHistory(path, 0)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	recent, maxSeq, err := reopened.LoadRecent(10)
	if err != nil {
		t.Fatalf("LoadRecent: %v", err)
	}

	if len(recent) != 3 {
		t.Fatalf("loaded %d messages, want 3", len(recent))
	}

	if maxSeq != 3 {
		t.Fatalf("maxSeq = %d, want 3", maxSeq)
	}

	// Ordering is ascending, and fields survive the round trip.
	for i, got := range recent {
		if got.Seq != uint64(i+1) {
			t.Fatalf("message %d has seq %d", i, got.Seq)
		}

		if got.Content != "m" || got.Sender != "⚡ VOLT #abcde" || got.ClientId != "session-1" {
			t.Fatalf("message %d lost fields: %+v", i, got)
		}

		if got.Type != "chat" {
			t.Fatalf("message %d type = %q", i, got.Type)
		}
	}
}

func waitForHistory(t *testing.T, store *HistoryStore, want int) {
	t.Helper()

	deadline := time.Now().Add(3 * time.Second)

	for time.Now().Before(deadline) {
		recent, _, err := store.LoadRecent(want)
		if err == nil && len(recent) >= want {
			return
		}

		time.Sleep(10 * time.Millisecond)
	}

	t.Fatalf("history did not reach %d messages in time", want)
}

func TestLoadBeforePagesBackwards(t *testing.T) {
	store, _ := newTestHistory(t, 0)
	defer store.Close()

	for i := uint64(1); i <= 5; i++ {
		store.Append(msg(i, "m"))
	}

	waitForHistory(t, store, 5)

	page, hasMore, err := store.LoadBefore(5, 2)
	if err != nil {
		t.Fatalf("LoadBefore: %v", err)
	}

	if got := []uint64{page[0].Seq, page[1].Seq}; got[0] != 3 || got[1] != 4 {
		t.Fatalf("first page seqs = %v, want [3 4]", got)
	}

	if !hasMore {
		t.Fatal("first page should report more history")
	}

	page, hasMore, err = store.LoadBefore(3, 2)
	if err != nil {
		t.Fatalf("LoadBefore second page: %v", err)
	}

	if got := []uint64{page[0].Seq, page[1].Seq}; got[0] != 1 || got[1] != 2 {
		t.Fatalf("second page seqs = %v, want [1 2]", got)
	}

	if hasMore {
		t.Fatal("second page should be the oldest")
	}

	page, hasMore, err = store.LoadBefore(1, 2)
	if err != nil {
		t.Fatalf("LoadBefore before first message: %v", err)
	}

	if len(page) != 0 || hasMore {
		t.Fatalf("before first message = %d messages, hasMore=%v", len(page), hasMore)
	}

	// The handler lets LoadBefore clamp oversized limits; the extra row must not
	// leak into the response.
	page, hasMore, err = store.LoadBefore(5, 1000)
	if err != nil {
		t.Fatalf("LoadBefore huge limit: %v", err)
	}

	if len(page) != 4 || hasMore {
		t.Fatalf("huge limit = %d messages, hasMore=%v", len(page), hasMore)
	}
}

func TestDeleteRemovesPersistedMessage(t *testing.T) {
	store, _ := newTestHistory(t, 0)
	defer store.Close()

	for i := uint64(1); i <= 3; i++ {
		store.Append(msg(i, "m"))
	}

	waitForHistory(t, store, 3)

	if got, ok, err := store.Message(2); err != nil || !ok || got.Seq != 2 {
		t.Fatalf("Message(2) = %+v/%v/%v", got, ok, err)
	}

	if err := store.Delete(2); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	recent, _, err := store.LoadRecent(10)
	if err != nil {
		t.Fatalf("LoadRecent: %v", err)
	}

	if len(recent) != 2 || recent[0].Seq != 1 || recent[1].Seq != 3 {
		t.Fatalf("remaining seqs = %+v, want [1 3]", recent)
	}

	if _, ok, err := store.Message(2); err != nil || ok {
		t.Fatalf("Message(2) after delete = %v/%v, want false/nil", ok, err)
	}
}

func TestLoadRecentReturnsNewestInAscendingOrder(t *testing.T) {
	store, _ := newTestHistory(t, 0)
	defer store.Close()

	for i := uint64(1); i <= 10; i++ {
		store.Append(msg(i, "m"))
	}

	term := make(chan struct{})
	go func() {
		// Append is async; polling keeps the test free of arbitrary sleeps where it
		// can be avoided.
		for {
			recent, _, err := store.LoadRecent(10)
			if err == nil && len(recent) == 10 {
				close(term)
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	select {
	case <-term:
	case <-time.After(3 * time.Second):
		t.Fatal("messages were not flushed in time")
	}

	recent, maxSeq, err := store.LoadRecent(3)
	if err != nil {
		t.Fatal(err)
	}

	if maxSeq != 10 {
		t.Fatalf("maxSeq = %d, want 10", maxSeq)
	}

	got := []uint64{recent[0].Seq, recent[1].Seq, recent[2].Seq}
	want := []uint64{8, 9, 10}

	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("newest 3 = %v, want %v", got, want)
		}
	}
}

func TestPruneKeepsOnlyNewest(t *testing.T) {
	store, path := newTestHistory(t, 5)

	for i := uint64(1); i <= 20; i++ {
		store.Append(msg(i, "m"))
	}

	// Wait for the batch to land before closing to check on-disk state.
	deadline := time.Now().Add(3 * time.Second)

	for time.Now().Before(deadline) {
		if recent, _, err := store.LoadRecent(100); err == nil && len(recent) >= 20 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	store.Close()

	reopened, err := OpenHistory(path, 5)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	recent, maxSeq, err := reopened.LoadRecent(100)
	if err != nil {
		t.Fatal(err)
	}

	if len(recent) != 5 {
		t.Fatalf("after prune %d rows, want 5", len(recent))
	}

	if maxSeq != 20 {
		t.Fatalf("maxSeq = %d, want 20", maxSeq)
	}

	if recent[0].Seq != 16 {
		t.Fatalf("oldest kept seq = %d, want 16", recent[0].Seq)
	}
}

func TestAppendNeverBlocksWhenQueueIsFull(t *testing.T) {
	// A store with no writer running: every Append must return immediately.
	store := &HistoryStore{queue: make(chan EventMessage, 1)}

	done := make(chan struct{})

	go func() {
		defer close(done)

		for i := 0; i < 100; i++ {
			store.Append(msg(uint64(i), "m"))
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Append blocked when the queue was full")
	}

	drops, _ := store.Stats()

	if drops == 0 {
		t.Fatal("expected some messages to be counted as dropped")
	}
}

// TestHubRestoresHistoryAndSequence covers the behaviour the whole feature exists
// for: after a restart the room still shows old messages and numbering continues.
func TestHubRestoresHistoryAndSequence(t *testing.T) {
	store, path := newTestHistory(t, 0)

	for i := uint64(1); i <= 4; i++ {
		store.Append(msg(i, "old"))
	}

	store.Close()

	reopened, err := OpenHistory(path, 0)
	if err != nil {
		t.Fatal(err)
	}

	history, lastSeq, err := reopened.LoadRecent(RestoreLimit)
	if err != nil {
		t.Fatal(err)
	}

	hub := NewHub()
	hub.Restore(history, lastSeq)
	hub.SetHistorySink(reopened)
	go hub.Run()

	staticFS := testStaticFS()
	srv := newServerForTest(t, hub, staticFS)
	defer hub.Shutdown()

	conn := dial(t, srv)
	snapshot := readUntil(t, conn, "snapshot")

	if len(snapshot.History) != 4 {
		t.Fatalf("restored %d messages, want 4", len(snapshot.History))
	}

	if snapshot.Seq != 4 {
		t.Fatalf("snapshot seq = %d, want 4", snapshot.Seq)
	}

	// The next message must continue at 5, not restart at 1.
	if err := conn.WriteMessage(1, []byte(`{"type":"chat","content":"after-restart"}`)); err != nil {
		t.Fatalf("write: %v", err)
	}

	live := readUntil(t, conn, "chat")

	if live.Seq != 5 {
		t.Fatalf("seq after restart = %d, want 5", live.Seq)
	}
}

// The image type must survive a restart: without the kind column a replayed image
// would come back as a chat message whose content is a media id.
func TestImageKindSurvivesRestart(t *testing.T) {
	store, path := newTestHistory(t, 0)

	store.Append(EventMessage{Type: "image", Seq: 1, ClientId: "s", Sender: "a", Content: "abc", Time: "1"})
	store.Append(EventMessage{Type: "chat", Seq: 2, ClientId: "s", Sender: "a", Content: "hi", Time: "2"})

	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenHistory(path, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()

	recent, _, err := reopened.LoadRecent(10)
	if err != nil {
		t.Fatal(err)
	}

	if len(recent) != 2 {
		t.Fatalf("loaded %d messages", len(recent))
	}

	if recent[0].Type != "image" {
		t.Fatalf("first message type = %q, want image", recent[0].Type)
	}

	if recent[1].Type != "chat" {
		t.Fatalf("second message type = %q, want chat", recent[1].Type)
	}
}

// Databases created before images existed must be migrated in place.
func TestKindColumnMigrationOnOldDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")

	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.Exec(`
		CREATE TABLE messages (
			seq        INTEGER PRIMARY KEY,
			session_id TEXT    NOT NULL,
			sender     TEXT    NOT NULL,
			content    TEXT    NOT NULL,
			time       TEXT    NOT NULL,
			created_at INTEGER NOT NULL
		);
		INSERT INTO messages (seq, session_id, sender, content, time, created_at)
		VALUES (7, 's', 'a', 'legacy text', '00:00:00', 1);
	`); err != nil {
		t.Fatal(err)
	}

	db.Close()

	store, err := OpenHistory(path, 0)
	if err != nil {
		t.Fatalf("migrating an old database must not fail: %v", err)
	}
	defer store.Close()

	recent, maxSeq, err := store.LoadRecent(10)
	if err != nil {
		t.Fatal(err)
	}

	if maxSeq != 7 {
		t.Fatalf("maxSeq = %d, want 7", maxSeq)
	}

	if len(recent) != 1 || recent[0].Content != "legacy text" {
		t.Fatalf("legacy row not readable: %+v", recent)
	}

	// Pre-existing rows default to chat.
	if recent[0].Type != "chat" {
		t.Fatalf("legacy row type = %q, want chat", recent[0].Type)
	}
}
