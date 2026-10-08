package ratelimit

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

//go:embed lua/sliding_window.lua
var slidingWindowSource string

var slidingWindowScript = redis.NewScript(slidingWindowSource)

type SlidingWindowConfig struct {
	Limit  int64
	Window time.Duration
}

type SlidingWindowLimiter struct {
	client redis.Scripter
	prefix string
	config SlidingWindowConfig
}

func NewSlidingWindow(client redis.Scripter, prefix string, config SlidingWindowConfig) *SlidingWindowLimiter {
	return &SlidingWindowLimiter{client: client, prefix: prefix, config: config}
}

func (l *SlidingWindowLimiter) Name() string {
	return "sliding_window_counter"
}

func (l *SlidingWindowLimiter) Allow(ctx context.Context, key string) (Result, error) {
	values, err := slidingWindowScript.Run(
		ctx,
		l.client,
		[]string{fmt.Sprintf("rl:sw:%s:%s", l.prefix, key)},
		l.config.Limit,
		l.config.Window.Milliseconds(),
		time.Now().UnixMilli(),
	).Int64Slice()
	if err != nil {
		return Result{}, err
	}
	return resultFrom(l.config.Limit, values), nil
}
