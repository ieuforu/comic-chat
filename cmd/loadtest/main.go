// Command loadtest opens N WebSocket sessions, sends chat messages at a fixed
// rate and measures end-to-end broadcast latency (send -> receive the same
// message back from the server).
//
//	go run ./cmd/loadtest -url http://127.0.0.1:8080 -conns 100 -rate 2 -duration 10s
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/cookiejar"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
)

type event struct {
	Type    string `json:"type"`
	Content string `json:"content"`
	Seq     uint64 `json:"seq"`
}

type session struct {
	conn      *websocket.Conn
	mu        sync.Mutex
	pending   map[string]time.Time
	latencies []time.Duration
	received  *atomic.Int64
}

func newSession(conn *websocket.Conn, received *atomic.Int64) *session {
	return &session{conn: conn, pending: make(map[string]time.Time), received: received}
}

func (s *session) record(content string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.received.Add(1)

	if stamp, ok := s.pending[content]; ok {
		s.latencies = append(s.latencies, time.Since(stamp))
		delete(s.pending, content)
	}
}

func (s *session) track(content string, stamp time.Time) {
	s.mu.Lock()
	s.pending[content] = stamp
	s.mu.Unlock()
}

func (s *session) drain() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := s.latencies
	s.latencies = nil

	return out
}

func main() {
	base := flag.String("url", "http://127.0.0.1:8080", "server base URL")
	conns := flag.Int("conns", 100, "number of concurrent sessions")
	rate := flag.Float64("rate", 2, "messages per second per connection")
	duration := flag.Duration("duration", 10*time.Second, "test duration")
	rampup := flag.Duration("rampup", 3*time.Second, "spread connection establishment over this window")
	jsonOut := flag.Bool("json", false, "print a JSON summary")
	flag.Parse()

	deadline := time.Now().Add(*duration)

	var (
		sent     atomic.Int64
		received atomic.Int64
		errors   atomic.Int64
		mu       sync.Mutex
		all      []time.Duration
		errMsgs  = map[string]int{}
		wg       sync.WaitGroup
	)

	start := time.Now()

	// Connect gradually: firing hundreds of handshakes at once overflows the
	// listener's accept backlog (default somaxconn, 128 on macOS) and shows up as
	// "connection reset by peer" that has nothing to do with the chat code.
	stagger := time.Duration(0)
	if *conns > 1 && *rampup > 0 {
		stagger = *rampup / time.Duration(*conns)
	}

	for i := 0; i < *conns; i++ {
		wg.Add(1)

		if stagger > 0 {
			time.Sleep(stagger)
		}

		go func() {
			defer wg.Done()

			latencies, err := runSession(*base, *rate, deadline, &sent, &received)
			if err != nil {
				errors.Add(1)
				mu.Lock()
				errMsgs[err.Error()]++
				mu.Unlock()

				return
			}

			mu.Lock()
			all = append(all, latencies...)
			mu.Unlock()
		}()
	}

	wg.Wait()

	elapsed := time.Since(start)
	sort.Slice(all, func(i, j int) bool { return all[i] < all[j] })

	pick := func(q float64) float64 {
		if len(all) == 0 {
			return 0
		}

		return float64(all[int(float64(len(all)-1)*q)]) / float64(time.Millisecond)
	}

	summary := map[string]any{
		"connections":      *conns,
		"durationSeconds":  round(elapsed.Seconds()),
		"sent":             sent.Load(),
		"received":         received.Load(),
		"errors":           errors.Load(),
		"sendPerSecond":    round(float64(sent.Load()) / elapsed.Seconds()),
		"deliveriesPerSec": round(float64(received.Load()) / elapsed.Seconds()),
		"samples":          len(all),
		"p50ms":            round(pick(0.50)),
		"p95ms":            round(pick(0.95)),
		"p99ms":            round(pick(0.99)),
		"maxMs":            round(pick(1.0)),
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(summary); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}

		return
	}

	fmt.Printf("connections=%d duration=%.1fs\n", *conns, elapsed.Seconds())
	fmt.Printf("sent=%d (%.0f/s)  received=%d (%.0f/s)  errors=%d\n",
		sent.Load(), summary["sendPerSecond"], received.Load(), summary["deliveriesPerSec"], errors.Load())
	for msg, count := range errMsgs {
		fmt.Printf("error x%d: %s\n", count, msg)
	}

	fmt.Printf("latency  p50=%.1fms  p95=%.1fms  p99=%.1fms  max=%.1fms  (samples=%d)\n",
		summary["p50ms"], summary["p95ms"], summary["p99ms"], summary["maxMs"], len(all))
}

func round(v float64) float64 {
	return float64(int(v*100+0.5)) / 100
}

// runSession performs the real client flow: obtain a session cookie, then connect
// and chat. Each message carries a nanosecond timestamp so the receiver can
// compute the broadcast latency of that exact message.
func runSession(base string, rate float64, deadline time.Time, sent, received *atomic.Int64) ([]time.Duration, error) {
	jar, err := cookiejar.New(nil)
	if err != nil {
		return nil, err
	}

	client := &http.Client{Jar: jar, Timeout: 10 * time.Second}
	root := strings.TrimSuffix(base, "/")

	resp, err := client.Get(root + "/api/session")
	if err != nil {
		return nil, err
	}
	sessionURL := resp.Request.URL
	resp.Body.Close()

	header := http.Header{}
	for _, cookie := range jar.Cookies(sessionURL) {
		header.Add("Cookie", cookie.Name+"="+cookie.Value)
	}

	wsURL := "ws" + strings.TrimPrefix(root, "http") + "/ws"
	dialer := websocket.Dialer{HandshakeTimeout: 10 * time.Second}

	conn, _, err := dialer.Dial(wsURL, header)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	s := newSession(conn, received)

	readerDone := make(chan struct{})

	go func() {
		defer close(readerDone)

		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}

			// A frame may carry several newline-delimited events.
			for _, line := range strings.Split(string(data), "\n") {
				if line == "" {
					continue
				}

				var ev event
				if err := json.Unmarshal([]byte(line), &ev); err != nil || ev.Type != "chat" {
					continue
				}

				s.record(ev.Content)
			}
		}
	}()

	interval := time.Duration(float64(time.Second) / rate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var seq int64

	for time.Now().Before(deadline) {
		<-ticker.C

		seq++
		content := strconv.FormatInt(time.Now().UnixNano(), 10) + "-" + strconv.FormatInt(seq, 10)

		payload, err := json.Marshal(map[string]any{"type": "chat", "content": content})
		if err != nil {
			return nil, err
		}

		s.track(content, time.Now())

		if err := conn.WriteMessage(websocket.TextMessage, payload); err != nil {
			return s.drain(), nil
		}

		sent.Add(1)
	}

	// Give in-flight broadcasts a moment to arrive before stopping the reader.
	time.Sleep(750 * time.Millisecond)
	conn.Close()
	<-readerDone

	return s.drain(), nil
}
