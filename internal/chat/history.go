package chat

import (
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	_ "modernc.org/sqlite" // pure-Go driver: keeps CGO_ENABLED=0 and a static binary
)

// HistorySink is what the hub needs from a history store. Keeping it an interface
// lets the hub run with persistence switched off (tests, or a read-only filesystem).
type HistorySink interface {
	Append(msg EventMessage)
	Close() error
}

// HistoryStore persists chat messages to SQLite.
//
// Writes go through one goroutine that batches them into a single transaction:
// committing per message from the hub's event loop would put disk latency on the
// broadcast path and stall every connected client.
type HistoryStore struct {
	db     *sql.DB
	queue  chan EventMessage
	done   chan struct{}
	closed sync.Once

	mu     sync.Mutex
	errors int
	drops  int
}

// RestoreLimit is how many messages a restart loads back into memory. It matches
// the in-memory cap, because replaying more than the hub can hold is pointless.
const RestoreLimit = maxHistory

const (
	historyPageDefault = 50
	historyPageMax     = 100
)

const (
	// Batches are small so a message is on disk quickly; the timer bounds how long a
	// partial batch waits.
	historyBatchSize  = 32
	historyFlushEvery = 50 * time.Millisecond
	historyQueueSize  = 1024
)

// OpenHistory opens (or creates) the database, applies the schema and prunes rows
// that can no longer be replayed anyway.
func OpenHistory(path string, keep int) (*HistoryStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	// SQLite writes are serialised anyway; more connections just adds lock churn.
	db.SetMaxOpenConns(1)

	if _, err := db.Exec(`
		PRAGMA journal_mode = WAL;
		PRAGMA synchronous = NORMAL;
		CREATE TABLE IF NOT EXISTS messages (
			seq        INTEGER PRIMARY KEY,
			session_id TEXT    NOT NULL,
			sender     TEXT    NOT NULL,
			content    TEXT    NOT NULL,
			time       TEXT    NOT NULL,
			created_at INTEGER NOT NULL,
			kind       TEXT    NOT NULL DEFAULT 'chat'
		);
		CREATE INDEX IF NOT EXISTS messages_created_at ON messages(created_at);
	`); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	// Existing databases predate the kind column (image messages need it, or a
	// restart would replay an image as a chat bubble full of hex).
	if err := ensureKindColumn(db); err != nil {
		db.Close()

		return nil, err
	}

	store := &HistoryStore{
		db:    db,
		queue: make(chan EventMessage, historyQueueSize),
		done:  make(chan struct{}),
	}

	if keep > 0 {
		if err := store.prune(keep); err != nil {
			log.Printf("警告: 历史清理失败: %v", err)
		}
	}

	go store.run()

	return store, nil
}

// ensureKindColumn adds the kind column to databases created before images existed.
// Without it a restart would replay an image as a chat bubble full of hex.
func ensureKindColumn(db *sql.DB) error {
	rows, err := db.Query(`PRAGMA table_info(messages)`)
	if err != nil {
		return err
	}

	hasKind := false

	for rows.Next() {
		var (
			cid       int
			name, typ string
			notNull   int
			dflt      sql.NullString
			pk        int
		)

		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			rows.Close()

			return err
		}

		if name == "kind" {
			hasKind = true
		}
	}

	rows.Close()

	if err := rows.Err(); err != nil {
		return err
	}

	if hasKind {
		return nil
	}

	_, err = db.Exec(`ALTER TABLE messages ADD COLUMN kind TEXT NOT NULL DEFAULT 'chat'`)

	return err
}

// DB exposes the connection pool so other stores (media) share it instead of
// opening a second SQLite connection to the same file.
func (s *HistoryStore) DB() *sql.DB { return s.db }

// LoadRecent returns the newest `limit` messages in ascending order plus the highest
// sequence number, which is what the hub needs to continue numbering after a restart.
func (s *HistoryStore) LoadRecent(limit int) ([]EventMessage, uint64, error) {
	rows, err := s.db.Query(
		`SELECT seq, session_id, sender, content, time, kind FROM messages ORDER BY seq DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, 0, fmt.Errorf("load history: %w", err)
	}
	defer rows.Close()

	reversed, err := readHistoryRows(rows)
	if err != nil {
		return nil, 0, err
	}

	var maxSeq uint64
	if len(reversed) > 0 {
		maxSeq = reversed[0].Seq
	}

	return reverseHistory(reversed), maxSeq, nil
}

// LoadBefore returns the page of messages immediately older than before, in
// ascending order, plus whether an older page still exists.
//
// The hub only keeps maxHistory in memory, so this is what makes older history
// reachable: the database is the source of truth, and a client pages backwards
// from the oldest seq it already has.
func (s *HistoryStore) LoadBefore(before uint64, limit int) ([]EventMessage, bool, error) {
	if before == 0 {
		return []EventMessage{}, false, nil
	}

	if limit <= 0 {
		limit = historyPageDefault
	}

	if limit > historyPageMax {
		limit = historyPageMax
	}

	// Fetch one extra row to distinguish "exactly the page size" from "there is
	// another page" without a second COUNT query.
	rows, err := s.db.Query(
		`SELECT seq, session_id, sender, content, time, kind
		 FROM messages WHERE seq < ? ORDER BY seq DESC LIMIT ?`,
		before, limit+1,
	)
	if err != nil {
		return nil, false, fmt.Errorf("load older history: %w", err)
	}
	defer rows.Close()

	reversed, err := readHistoryRows(rows)
	if err != nil {
		return nil, false, err
	}

	hasMore := len(reversed) > limit
	if hasMore {
		reversed = reversed[:limit]
	}

	return reverseHistory(reversed), hasMore, nil
}

// readHistoryRows scans the newest-first rows used by both LoadRecent and
// LoadBefore. Callers own rows and must close it.
func readHistoryRows(rows *sql.Rows) ([]EventMessage, error) {
	var reversed []EventMessage

	for rows.Next() {
		var (
			msg  EventMessage
			seq  int64
			kind string
		)

		if err := rows.Scan(&seq, &msg.ClientId, &msg.Sender, &msg.Content, &msg.Time, &kind); err != nil {
			return nil, fmt.Errorf("scan history: %w", err)
		}

		msg.Type = kind
		msg.Seq = uint64(seq)
		reversed = append(reversed, msg)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return reversed, nil
}

func reverseHistory(reversed []EventMessage) []EventMessage {
	out := make([]EventMessage, len(reversed))
	for i, msg := range reversed {
		out[len(reversed)-1-i] = msg
	}

	return out
}

// Message returns one persisted message by sequence number. Used by the admin
// delete path to clean up an attached image before the row disappears.
func (s *HistoryStore) Message(seq uint64) (EventMessage, bool, error) {
	rows, err := s.db.Query(
		`SELECT seq, session_id, sender, content, time, kind FROM messages WHERE seq = ?`,
		seq,
	)
	if err != nil {
		return EventMessage{}, false, fmt.Errorf("load message: %w", err)
	}
	defer rows.Close()

	messages, err := readHistoryRows(rows)
	if err != nil {
		return EventMessage{}, false, err
	}

	if len(messages) == 0 {
		return EventMessage{}, false, nil
	}

	return messages[0], true, nil
}

// Delete removes one persisted message. It is synchronous so an admin deletion
// is durable before the caller returns.
func (s *HistoryStore) Delete(seq uint64) error {
	if _, err := s.db.Exec(`DELETE FROM messages WHERE seq = ?`, seq); err != nil {
		return fmt.Errorf("delete message: %w", err)
	}

	return nil
}

// Append enqueues a message for persistence. It never blocks the caller: when the
// queue is full the message is dropped and counted, because a chat that keeps
// running is more useful than one that stalls on a slow disk.
func (s *HistoryStore) Append(msg EventMessage) {
	select {
	case s.queue <- msg:
	default:
		s.mu.Lock()
		s.drops++
		s.mu.Unlock()
	}
}

// Close flushes the queue and closes the database. Safe to call twice.
func (s *HistoryStore) Close() error {
	s.closed.Do(func() { close(s.queue) })
	<-s.done

	return s.db.Close()
}

// Stats reports dropped and failed writes, for the debug endpoint.
func (s *HistoryStore) Stats() (drops, failures int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.drops, s.errors
}

func (s *HistoryStore) run() {
	defer close(s.done)

	ticker := time.NewTicker(historyFlushEvery)
	defer ticker.Stop()

	batch := make([]EventMessage, 0, historyBatchSize)

	flush := func() {
		if len(batch) == 0 {
			return
		}

		if err := s.insert(batch); err != nil {
			s.mu.Lock()
			s.errors++
			s.mu.Unlock()
			log.Printf("警告: 历史写入失败: %v", err)
		}

		batch = batch[:0]
	}

	for {
		select {
		case msg, ok := <-s.queue:
			if !ok {
				flush()
				return
			}

			batch = append(batch, msg)

			// Drain whatever else is already queued before committing.
			for len(batch) < historyBatchSize {
				select {
				case next, nextOK := <-s.queue:
					if !nextOK {
						flush()
						return
					}
					batch = append(batch, next)
				default:
					goto commit
				}
			}

		commit:
			flush()

		case <-ticker.C:
			flush()
		}
	}
}

func (s *HistoryStore) insert(batch []EventMessage) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}

	stmt, err := tx.Prepare(
		`INSERT OR IGNORE INTO messages (seq, session_id, sender, content, time, created_at, kind)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		tx.Rollback() //nolint:errcheck // rollback failure cannot be acted on
		return err
	}
	defer stmt.Close()

	now := time.Now().UnixMilli()

	for _, msg := range batch {
		kind := msg.Type
		if kind == "" {
			kind = "chat"
		}

		if _, err := stmt.Exec(msg.Seq, msg.ClientId, msg.Sender, msg.Content, msg.Time, now, kind); err != nil {
			tx.Rollback() //nolint:errcheck

			return err
		}
	}

	return tx.Commit()
}

// prune keeps only the newest `keep` rows; older messages are not replayable
// anyway, so they are dead weight on disk.
func (s *HistoryStore) prune(keep int) error {
	var maxSeq sql.NullInt64
	if err := s.db.QueryRow(`SELECT MAX(seq) FROM messages`).Scan(&maxSeq); err != nil {
		return err
	}

	if !maxSeq.Valid {
		return nil
	}

	cutoff := maxSeq.Int64 - int64(keep)
	if cutoff <= 0 {
		return nil
	}

	_, err := s.db.Exec(`DELETE FROM messages WHERE seq <= ?`, cutoff)

	return err
}
