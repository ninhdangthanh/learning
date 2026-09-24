package ratelimit

import (
	"context"
	"time"
)

type Result struct {
	Allowed    bool
	Limit      int64
	Remaining  int64
	ResetAfter time.Duration
	RetryAfter time.Duration
}

type Limiter interface {
	Name() string
	Allow(ctx context.Context, key string) (Result, error)
}

func resultFrom(limit int64, values []int64) Result {
	return Result{
		Allowed:    values[0] == 1,
		Limit:      limit,
		Remaining:  values[1],
		ResetAfter: time.Duration(values[2]) * time.Millisecond,
		RetryAfter: time.Duration(values[3]) * time.Millisecond,
	}
}
