package ratelimit

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

//go:embed lua/fixed_window.lua
var fixedWindowSource string

var fixedWindowScript = redis.NewScript(fixedWindowSource)

type FixedWindowConfig struct {
	Limit  int64
	Window time.Duration
}

type FixedWindowLimiter struct {
	client redis.Scripter
	prefix string
	config FixedWindowConfig
}

func NewFixedWindow(client redis.Scripter, prefix string, config FixedWindowConfig) *FixedWindowLimiter {
	return &FixedWindowLimiter{client: client, prefix: prefix, config: config}
}

func (l *FixedWindowLimiter) Name() string {
	return "fixed_window"
}

func (l *FixedWindowLimiter) Allow(ctx context.Context, key string) (Result, error) {
	values, err := fixedWindowScript.Run(
		ctx,
		l.client,
		[]string{fmt.Sprintf("rl:fw:%s:%s", l.prefix, key)},
		l.config.Limit,
		l.config.Window.Milliseconds(),
		time.Now().UnixMilli(),
	).Int64Slice()
	if err != nil {
		return Result{}, err
	}
	return resultFrom(l.config.Limit, values), nil
}
