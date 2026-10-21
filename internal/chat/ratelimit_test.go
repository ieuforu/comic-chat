package chat

import (
	"testing"
)

func TestLoadRateConfigDefaults(t *testing.T) {
	t.Setenv("RATE_MODE", "")
	t.Setenv("RATE_PER_SECOND", "")
	t.Setenv("RATE_BURST", "")

	cfg := loadRateConfig()

	// Observing by default: an unmeasured threshold must not break real users.
	if cfg.Mode != RateModeLog {
		t.Fatalf("mode = %q, want log", cfg.Mode)
	}

	if cfg.PerSec != 5 || cfg.Burst != 10 {
		t.Fatalf("limits = %.1f/%d, want 5/10", cfg.PerSec, cfg.Burst)
	}
}

func TestLoadRateConfigOverrides(t *testing.T) {
	t.Setenv("RATE_MODE", "enforce")
	t.Setenv("RATE_PER_SECOND", "2.5")
	t.Setenv("RATE_BURST", "3")

	cfg := loadRateConfig()

	if cfg.Mode != RateModeEnforce || cfg.PerSec != 2.5 || cfg.Burst != 3 {
		t.Fatalf("cfg = %+v", cfg)
	}
}

func TestLoadRateConfigIgnoresGarbage(t *testing.T) {
	t.Setenv("RATE_MODE", "banana")
	t.Setenv("RATE_PER_SECOND", "-1")
	t.Setenv("RATE_BURST", "zero")

	cfg := loadRateConfig()

	if cfg.Mode != RateModeLog || cfg.PerSec != 5 || cfg.Burst != 10 {
		t.Fatalf("garbage should fall back to defaults, got %+v", cfg)
	}
}

func TestRateLimitOffHasNoLimiter(t *testing.T) {
	if limiter := (RateConfig{Mode: RateModeOff}).newLimiter(); limiter != nil {
		t.Fatal("off mode should not create a limiter")
	}
}

// TestRateLimitLogModeDeliversEverything is the behaviour that protects the test
// phase: overages are counted but nothing is dropped.
func TestRateLimitLogModeDeliversEverything(t *testing.T) {
	srv, hub, _ := newTestServer(t)

	// A deliberately tiny bucket so overages are certain within a fast test.
	hub.SetRateLimit(RateConfig{Mode: RateModeLog, PerSec: 0.001, Burst: 2})
	// The limiter is created per connection, so reconnect to pick up the new policy.
	conn := dial(t, srv)
	readUntil(t, conn, "snapshot")

	const sent = 6
	for i := 0; i < sent; i++ {
		if err := conn.WriteMessage(1, []byte(`{"type":"chat","content":"spam"}`)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	delivered := 0
	for delivered < sent {
		if msg := readUntil(t, conn, "chat"); msg.Content != "spam" {
			t.Fatalf("unexpected message %+v", msg)
		}
		delivered++
	}

	if delivered != sent {
		t.Fatalf("delivered %d, want %d", delivered, sent)
	}

	if hub.RateLimitedCount() == 0 {
		t.Fatal("overages should have been counted")
	}
}

// TestRateLimitEnforceModeDropsExcess is the actual protection.
func TestRateLimitEnforceModeDropsExcess(t *testing.T) {
	srv, hub, _ := newTestServer(t)

	hub.SetRateLimit(RateConfig{Mode: RateModeEnforce, PerSec: 0.001, Burst: 2})
	conn := dial(t, srv)
	readUntil(t, conn, "snapshot")

	// Two fit in the burst; the rest must be dropped and answered with a notice.
	for i := 0; i < 5; i++ {
		if err := conn.WriteMessage(1, []byte(`{"type":"chat","content":"m"}`)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	// Order is not guaranteed: the notice is written straight to this client's send
	// queue, while chats go through the hub and come back, so the notices can land
	// first. Count over the whole batch instead of assuming a sequence.
	chats, notices := 0, 0

	// presence/typing frames may interleave, and reads are bounded so a broken
	// server fails the test instead of hanging it.
	for i := 0; i < 12 && (chats < 2 || notices < 3); i++ {
		msg := readEvent(t, conn)

		switch msg.Type {
		case "chat":
			chats++
		case "notice":
			notices++

			if msg.Notice == "" {
				t.Fatal("notice carried no text")
			}
		case "presence", "typing":
			// unrelated to the assertion
		default:
			t.Fatalf("unexpected event %+v", msg)
		}
	}

	if chats != 2 {
		t.Fatalf("delivered %d chats, want 2 (the burst)", chats)
	}

	if notices != 3 {
		t.Fatalf("got %d notices, want 3 (one per dropped message)", notices)
	}

	if got := hub.RateLimitedCount(); got != 3 {
		t.Fatalf("rate limited count = %d, want 3", got)
	}
}

// Typing must not be limited: it is debounced by the client and carries no content.
func TestTypingIsNotRateLimited(t *testing.T) {
	srv, hub, _ := newTestServer(t)

	hub.SetRateLimit(RateConfig{Mode: RateModeEnforce, PerSec: 0.001, Burst: 1})
	conn := dial(t, srv)
	readUntil(t, conn, "snapshot")

	for i := 0; i < 6; i++ {
		if err := conn.WriteMessage(1, []byte(`{"type":"typing","isTyping":true}`)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	seen := 0
	for seen < 6 {
		if msg := readUntil(t, conn, "typing"); msg.Type != "typing" {
			t.Fatalf("unexpected %+v", msg)
		}
		seen++
	}

	if hub.RateLimitedCount() != 0 {
		t.Fatalf("typing should never be rate limited, count = %d", hub.RateLimitedCount())
	}
}

// A burst of exactly the configured size must pass: that is the point of a burst.
func TestBurstIsAllowedInFull(t *testing.T) {
	srv, hub, _ := newTestServer(t)

	hub.SetRateLimit(RateConfig{Mode: RateModeEnforce, PerSec: 0.001, Burst: 5})
	conn := dial(t, srv)
	readUntil(t, conn, "snapshot")

	for i := 0; i < 5; i++ {
		if err := conn.WriteMessage(1, []byte(`{"type":"chat","content":"b"}`)); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
	}

	for i := 0; i < 5; i++ {
		if msg := readUntil(t, conn, "chat"); msg.Content != "b" {
			t.Fatalf("unexpected %+v", msg)
		}
	}

	if hub.RateLimitedCount() != 0 {
		t.Fatalf("a full burst must not be limited, count = %d", hub.RateLimitedCount())
	}
}
