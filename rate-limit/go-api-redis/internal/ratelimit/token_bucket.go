package ratelimit

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

//go:embed lua/token_bucket.lua
var tokenBucketSource string

var tokenBucketScript = redis.NewScript(tokenBucketSource)

type TokenBucketConfig struct {
	Capacity        int64
	RefillPerSecond float64
}

type TokenBucketLimiter struct {
	client redis.Scripter
	prefix string
	config TokenBucketConfig
}

func NewTokenBucket(client redis.Scripter, prefix string, config TokenBucketConfig) *TokenBucketLimiter {
	return &TokenBucketLimiter{client: client, prefix: prefix, config: config}
}

func (l *TokenBucketLimiter) Name() string {
	return "token_bucket"
}

func (l *TokenBucketLimiter) Allow(ctx context.Context, key string) (Result, error) {
	values, err := tokenBucketScript.Run(
		ctx,
		l.client,
		[]string{fmt.Sprintf("rl:tb:%s:%s", l.prefix, key)},
		l.config.Capacity,
		l.config.RefillPerSecond,
		time.Now().UnixMilli(),
		1,
	).Int64Slice()
	if err != nil {
		return Result{}, err
	}
	return resultFrom(l.config.Capacity, values), nil
}
