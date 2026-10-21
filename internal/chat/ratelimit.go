package chat

import (
	"log"
	"os"
	"strconv"
	"strings"

	"golang.org/x/time/rate"
)

// RateLimitMode decides what happens when a client sends chats too fast.
//
//	log     observe only: count and log overages, deliver everything. The default,
//	        because a threshold nobody has measured yet must not break real users.
//
//	enforce drop the excess and tell the client.
//	off     no limiter at all.
type RateLimitMode string

const (
	RateModeLog     RateLimitMode = "log"
	RateModeEnforce RateLimitMode = "enforce"
	RateModeOff     RateLimitMode = "off"
)

// RateConfig is per-connection, not global: ten people chatting at once must not
// share one bucket.
type RateConfig struct {
	Mode   RateLimitMode
	PerSec float64
	Burst  int
}

// DefaultRateConfig is deliberately generous: five messages per second is roughly
// ten times what a person can type, and the burst allows pasting a few lines.
func DefaultRateConfig() RateConfig {
	return RateConfig{Mode: RateModeLog, PerSec: 5, Burst: 10}
}

// loadRateConfig reads RATE_MODE, RATE_PER_SECOND and RATE_BURST, falling back to
// the defaults above with a log line when a value is unusable.
func loadRateConfig() RateConfig {
	cfg := DefaultRateConfig()

	switch mode := RateLimitMode(strings.ToLower(os.Getenv("RATE_MODE"))); mode {
	case RateModeLog, RateModeEnforce, RateModeOff:
		cfg.Mode = mode
	case "":
		// keep default
	default:
		log.Printf("警告: 未知 RATE_MODE=%q，使用 %s", mode, cfg.Mode)
	}

	if raw := os.Getenv("RATE_PER_SECOND"); raw != "" {
		if value, err := strconv.ParseFloat(raw, 64); err == nil && value > 0 {
			cfg.PerSec = value
		} else {
			log.Printf("警告: RATE_PER_SECOND=%q 无效，使用 %.1f", raw, cfg.PerSec)
		}
	}

	if raw := os.Getenv("RATE_BURST"); raw != "" {
		if value, err := strconv.Atoi(raw); err == nil && value > 0 {
			cfg.Burst = value
		} else {
			log.Printf("警告: RATE_BURST=%q 无效，使用 %d", raw, cfg.Burst)
		}
	}

	return cfg
}

// newLimiter builds the per-connection bucket. A nil limiter means unlimited.
func (c RateConfig) newLimiter() *rate.Limiter {
	if c.Mode == RateModeOff {
		return nil
	}

	return rate.NewLimiter(rate.Limit(c.PerSec), c.Burst)
}
