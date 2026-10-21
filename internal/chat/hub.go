package chat

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"golang.org/x/time/rate"
)

const (
	writeWait      = 10 * time.Second
	pongWait       = 60 * time.Second
	pingPeriod     = (pongWait * 9) / 10
	maxMessageSize = 512
	maxHistory     = 50
	// Per-client outbound queue. Bigger than the burst a client can absorb, so a
	// slow reader is dropped by policy rather than by accident.
	sendBufferSize = 1024
	// Up to this many queued events are coalesced into one WebSocket frame
	// (newline-delimited JSON), which turns N writes into one syscall per drain.
	maxBatch = 32
	// Inbound broadcast queue between per-client readers and the hub loop.
	broadcastBufferSize = 1024
)

// upgrader rejects cross-site handshakes. Accepting every origin lets any page a
// visitor has open drive their browser into this socket (cross-site WebSocket
// hijacking). Non-browser clients send no Origin header and are allowed; set
// ALLOWED_ORIGINS to add explicit ones.
var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		origin := r.Header.Get("Origin")
		if origin == "" {
			return true
		}

		if allowedOriginSet[strings.TrimSuffix(origin, "/")] {
			return true
		}

		// Same-host: compare hosts so http/https and ports do not matter to the
		// browser's own origin calculation for a first-party page.
		u, err := url.Parse(origin)
		if err != nil {
			return false
		}

		return strings.EqualFold(u.Host, r.Host)
	},
}

var allowedOriginSet = allowedOrigins()

// 统一事件契约
type EventMessage struct {
	Type        string         `json:"type"` // chat | snapshot | presence | typing | notice | profile
	ClientId    string         `json:"clientId,omitempty"`
	Sender      string         `json:"sender,omitempty"`
	Content     string         `json:"content,omitempty"`
	Time        string         `json:"time,omitempty"`
	History     []EventMessage `json:"history,omitempty"`
	Online      int            `json:"online,omitempty"`
	Connections int            `json:"connections,omitempty"`
	// Seq is the newest chat sequence number known to the server; a client stores
	// it and asks for `?since=<seq>` when it reconnects.
	Seq uint64 `json:"seq,omitempty"`
	// Truncated tells a reconnecting client that messages after its `since` were
	// already dropped from history, so a gap is expected.
	Truncated bool `json:"truncated,omitempty"`
	// Notice carries a short server-side message for the client UI (rate limiting).
	Notice   string `json:"notice,omitempty"`
	IsTyping bool   `json:"isTyping,omitempty"` // 关键：输入状态标记
	// Name carries a new display name in a `profile` event, so a renamed user's own
	// page can update without a reload.
	Name string `json:"name,omitempty"`
	// Me is only set on a snapshot and only for the client receiving it: the server's
	// authoritative view of that client's identity. The client adopts it on every
	// (re)connect, which is what keeps a rename from going stale in an open tab.
	Me *Profile `json:"me,omitempty"`
}

type Client struct {
	hub     *Hub
	conn    *websocket.Conn
	send    chan []byte
	profile Profile
	// sessionID identifies the connection; profile is refreshed from it on every
	// message so a rename does not need a reconnect to take effect.
	sessionID string
	// resolve re-derives the profile from the session id (nil in unit tests).
	resolve func(sessionID string) Profile
	// media validates image references (nil disables image messages).
	media *MediaStore
	// since is the last sequence number the client already has (0 = new client).
	since uint64
	// limiter is per-connection; nil disables rate limiting for this client.
	limiter *rate.Limiter
}

type Hub struct {
	mu         sync.RWMutex
	history    []EventMessage
	clients    map[*Client]bool
	broadcast  chan []byte
	register   chan *Client
	unregister chan *Client
	// profiles carries rename notifications into the event loop; the clients map is
	// owned by Run, so this cannot be fanned out from the caller's goroutine.
	profiles chan profileUpdate
	// stop/done let Shutdown hand the teardown to the Run goroutine instead of
	// reaching into its map from another one.
	stop chan struct{}
	done chan struct{}
	// Mirrors of the map size and distinct-session count, maintained by the Run
	// goroutine so observers never have to touch the map itself.
	connCount atomic.Int64
	userCount atomic.Int64
	// seq is the sequence number of the newest stored chat message. Only the Run
	// goroutine touches it, which is what keeps ordering global and race-free.
	seq uint64
	// sink persists chat messages; nil means memory-only.
	sink HistorySink
	// rateCfg holds the RateConfig so tests and the debug endpoint can read it
	// without racing the Run goroutine.
	rateCfg atomic.Value
	// rateLimited counts chats that exceeded the bucket (dropped in enforce mode,
	// only counted in log mode).
	rateLimited atomic.Int64
}

// profileUpdate is a rename to fan out.
type profileUpdate struct {
	prefix  string
	display string
}

// BroadcastProfile tells connected clients that a session's display name changed.
// ClientId carries the 5-character prefix, because that is what the operator writes
// in names.json; clients compare it with their own session id prefix.
func (h *Hub) BroadcastProfile(prefix, display string) {
	select {
	case h.profiles <- profileUpdate{prefix: prefix, display: display}:
	case <-h.done:
	default:
		// A full queue means renames are being spammed; the next client request still
		// sees the new name, so dropping is safe.
	}
}

// RateLimitedCount reports how many chat messages exceeded the rate limit.
func (h *Hub) RateLimitedCount() int64 { return h.rateLimited.Load() }

// SetRateLimit overrides the policy. Safe to call at any time; it takes effect for
// connections created afterwards.
func (h *Hub) SetRateLimit(cfg RateConfig) { h.rateCfg.Store(cfg) }

// RateLimit returns the active policy.
func (h *Hub) RateLimit() RateConfig {
	if cfg, ok := h.rateCfg.Load().(RateConfig); ok {
		return cfg
	}

	return DefaultRateConfig()
}

// Restore seeds the in-memory history from storage. Call it before Run.
func (h *Hub) Restore(history []EventMessage, lastSeq uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.history = append(make([]EventMessage, 0, maxHistory), history...)
	if lastSeq > h.seq {
		h.seq = lastSeq
	}
}

// SetHistorySink attaches persistence. Call it before Run.
func (h *Hub) SetHistorySink(sink HistorySink) {
	h.sink = sink
}

// ConnectionCount and UserCount are safe to call from any goroutine.
func (h *Hub) ConnectionCount() int64 { return h.connCount.Load() }

func (h *Hub) UserCount() int64 { return h.userCount.Load() }

func NewHub() *Hub {
	h := &Hub{
		history: make([]EventMessage, 0, maxHistory),
		clients: make(map[*Client]bool),
		// Buffered on purpose: an unbuffered channel makes the runtime hand the slice
		// header straight to the receiver (sendDirect/recvDirect), and for an element
		// type that holds pointers the race detector reports that handoff as a race.
		// A buffer also decouples readers from the hub when it is busy.
		broadcast:  make(chan []byte, broadcastBufferSize),
		register:   make(chan *Client),
		unregister: make(chan *Client),
		profiles:   make(chan profileUpdate, 64),
		stop:       make(chan struct{}),
		done:       make(chan struct{}),
	}

	h.rateCfg.Store(loadRateConfig())

	return h
}

// Shutdown closes every connection and stops the event loop. http.Server.Shutdown
// does not wait for hijacked (WebSocket) connections, so this has to happen here
// or a restart leaves clients hanging until their TCP timeout.
func (h *Hub) Shutdown() {
	close(h.stop)
	<-h.done
}

// onlineUsersLocked counts distinct sessions among connected clients. Callers
// must be the Run goroutine (it owns the clients map).
func (h *Hub) onlineUsersLocked() int {
	users := make(map[string]struct{}, len(h.clients))

	for client := range h.clients {
		users[client.profile.SessionID] = struct{}{}
	}

	return len(users)
}

// syncCounts recomputes the exported mirrors; called from the Run goroutine after
// any change to the client set.
func (h *Hub) syncCounts() {
	h.connCount.Store(int64(len(h.clients)))
	h.userCount.Store(int64(h.onlineUsersLocked()))
}

// historySinceLocked returns what a connecting client still needs.
//
// since == 0 means "new client": send the last maxHistory messages. Otherwise send
// only messages newer than since, and report truncation when the requested range
// has already been evicted, so the client knows a gap is possible instead of
// assuming it is up to date.
func (h *Hub) historySinceLocked(since uint64) ([]EventMessage, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if since == 0 {
		out := make([]EventMessage, len(h.history))
		copy(out, h.history)

		return out, false
	}

	out := make([]EventMessage, 0, len(h.history))
	oldest := uint64(0)

	if len(h.history) > 0 {
		oldest = h.history[0].Seq
	}

	for _, msg := range h.history {
		if msg.Seq > since {
			out = append(out, msg)
		}
	}

	truncated := oldest > since+1 && since < oldest

	return out, truncated
}

func (h *Hub) broadcastPresence() {
	// Online counts people, not sockets: one browser with three tabs is one user.
	msg := EventMessage{
		Type:        "presence",
		Online:      h.onlineUsersLocked(),
		Connections: len(h.clients),
	}
	if b, err := json.Marshal(msg); err == nil {
		for client := range h.clients {
			select {
			case client.send <- b:
			default:
				close(client.send)
				delete(h.clients, client)
			}
		}
	}
}

func (h *Hub) Run() {
	defer close(h.done)

	for {
		select {
		case <-h.stop:
			// Only close the send channel. The conn must be written and closed by
			// writePump, because gorilla allows a single concurrent writer — doing
			// it here raced with writePump (caught by `go test -race`).
			for client := range h.clients {
				close(client.send)
				delete(h.clients, client)
			}
			h.syncCounts()

			if h.sink != nil {
				if err := h.sink.Close(); err != nil {
					log.Printf("警告: 关闭历史存储失败: %v", err)
				}
			}

			log.Println("已关闭全部连接，事件循环退出")
			return

		case client := <-h.register:
			h.clients[client] = true
			h.syncCounts()
			log.Printf("客户端接入，当前在线: %d", len(h.clients))

			// historySinceLocked takes the read lock itself; Online counts people
			// (not sockets) so a reconnect from a second tab does not inflate it.
			historyCopy, truncated := h.historySinceLocked(client.since)

			profile := client.currentProfile()

			snapshot := EventMessage{
				Type:        "snapshot",
				History:     historyCopy,
				Online:      h.onlineUsersLocked(),
				Connections: len(h.clients),
				Seq:         h.seq,
				Truncated:   truncated,
				Me:          &profile,
			}

			if snapshotBytes, err := json.Marshal(snapshot); err == nil {
				select {
				case client.send <- snapshotBytes:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
			h.broadcastPresence()

		case client := <-h.unregister:
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				h.syncCounts()

				close(client.send)
				log.Printf("客户端退出，当前在线: %d", len(h.clients))
				h.broadcastPresence()
			}

		case update := <-h.profiles:
			payload, err := json.Marshal(EventMessage{
				Type:     "profile",
				ClientId: update.prefix,
				Name:     update.display,
			})
			if err != nil {
				continue
			}

			for client := range h.clients {
				select {
				case client.send <- payload:
				default:
				}
			}

		case rawMessage := <-h.broadcast:
			var msg EventMessage
			if err := json.Unmarshal(rawMessage, &msg); err == nil {
				// 仅有 chat 消息才入内存历史记录，typing 消息直接穿透广播
				if msg.Type == "chat" || msg.Type == "image" {
					// Sequence numbers are assigned here, in the single goroutine that
					// owns the history, so ordering is global and stable.
					h.seq++
					msg.Seq = h.seq

					if encoded, err := json.Marshal(msg); err == nil {
						rawMessage = encoded
					}

					h.mu.Lock()
					if len(h.history) >= maxHistory {
						h.history = h.history[1:]
					}
					h.history = append(h.history, msg)
					h.mu.Unlock()

					if h.sink != nil {
						h.sink.Append(msg)
					}
				}
			}

			// 广播给所有客户端
			for client := range h.clients {
				select {
				case client.send <- rawMessage:
				default:
					close(client.send)
					delete(h.clients, client)
				}
			}
		}
	}
}

// currentProfile re-derives the identity from the session id. A name captured at
// handshake would go stale as soon as the operator renames someone.
func (c *Client) currentProfile() Profile {
	if c.resolve != nil && c.sessionID != "" {
		return c.resolve(c.sessionID)
	}

	return c.profile
}

func (c *Client) readPump() {
	defer func() {
		// Run may already have exited during shutdown; a bare channel send would
		// block this goroutine forever and leak it.
		select {
		case c.hub.unregister <- c:
		case <-c.hub.done:
		}
		c.conn.Close()
	}()

	c.conn.SetReadLimit(maxMessageSize)
	c.conn.SetReadDeadline(time.Now().Add(pongWait))
	c.conn.SetPongHandler(func(string) error {
		c.conn.SetReadDeadline(time.Now().Add(pongWait))
		return nil
	})

	for {
		_, message, err := c.conn.ReadMessage()
		if err != nil {
			break
		}

		var event EventMessage
		if err := json.Unmarshal(message, &event); err != nil {
			continue
		}

		// Only these three are client-originated. Rejecting the rest stops a client
		// from injecting snapshot/presence frames of its own.
		if event.Type != "chat" && event.Type != "typing" && event.Type != "image" {
			continue
		}

		if event.Type == "image" {
			// An image message is just a reference; make sure the sender uploaded it,
			// otherwise anyone could point a message at someone else's upload.
			if c.media == nil || !c.media.Owns(strings.TrimSpace(event.Content), c.sessionID) {
				continue
			}
		}

		// Rate limiting only applies to chat: typing is inherently chatty and is
		// already debounced by the client.
		if event.Type == "chat" && c.limiter != nil && !c.limiter.Allow() {
			c.hub.rateLimited.Add(1)
			h := c.hub

			if h.RateLimit().Mode == RateModeEnforce {
				// Tell the sender once per overage; dropping silently would look like
				// a broken client.
				if payload, err := json.Marshal(EventMessage{
					Type:   "notice",
					Notice: "发送太快了，稍等一下再发",
				}); err == nil {
					select {
					case c.send <- payload:
					default:
					}
				}

				continue
			}

			// log mode: deliver as usual, the counter is the point.
			log.Printf("限流观察: 会话 %s 超过 %.1f 条/秒（仅记录，未拦截）", c.profile.SessionID[:6], h.RateLimit().PerSec)
		}

		// Identity is overwritten from the session, never taken from the payload:
		// that is what makes impersonation impossible.
		profile := c.currentProfile()
		event.Sender = profile.Display()
		event.ClientId = profile.SessionID
		event.Time = time.Now().Format("15:04:05")

		encoded, err := json.Marshal(event)
		if err != nil {
			continue
		}

		// Same lifecycle guard as register/unregister: after Run has exited nobody
		// drains this channel, so a bare send would block the goroutine forever.
		select {
		case c.hub.broadcast <- encoded:
		case <-c.hub.done:
			return
		}
	}
}

func (c *Client) writePump() {
	// Reused across writes; owned by this goroutine, so no locking is needed.
	var batch bytes.Buffer

	ticker := time.NewTicker(pingPeriod)
	defer func() {
		ticker.Stop()
		c.conn.Close()
	}()

	for {
		select {
		case message, ok := <-c.send:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if !ok {
				c.conn.WriteMessage(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down"),
				)
				return
			}

			// Everything already queued goes out in the same frame: under load this
			// is the difference between one syscall per message and one per drain.
			// A drain that only yields one message skips the buffer entirely, so the
			// common light-traffic case pays no copying cost.
			batch.Reset()
			batched := 1
			closed := false

		drain:
			for batched < maxBatch {
				select {
				case next, nextOK := <-c.send:
					if !nextOK {
						closed = true
						break drain
					}

					if batched == 1 {
						// Promote the first message into the buffer only once a second
						// one shows up, so a lone message is never copied.
						batch.Write(message)
					}

					batch.WriteByte('\n')
					batch.Write(next)
					batched++
				default:
					break drain
				}
			}

			payload := message
			if batched > 1 {
				payload = batch.Bytes()
			}

			if err := c.conn.WriteMessage(websocket.TextMessage, payload); err != nil {
				return
			}

			if closed {
				c.conn.WriteMessage(
					websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseGoingAway, "server shutting down"),
				)
				return
			}

		case <-ticker.C:
			c.conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				return
			}
		}
	}
}
