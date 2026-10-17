package ws

import (
	"errors"
	"fmt"
	"time"
)

type Config struct {
	WriteWait      time.Duration
	PongWait       time.Duration
	PingPeriod     time.Duration
	MaxMessageSize int64
	SendBufferSize int
	MembershipWait time.Duration
	AllowedOrigins []string
}

func DefaultConfig() Config {
	return Config{
		WriteWait:      10 * time.Second,
		PongWait:       60 * time.Second,
		PingPeriod:     25 * time.Second,
		MaxMessageSize: 10 * 1024,
		SendBufferSize: 64,
		MembershipWait: 2 * time.Second,
	}
}

func (c Config) Validate() error {
	var problems []error
	if c.WriteWait <= 0 {
		problems = append(problems, fmt.Errorf("write wait must be positive, got %v", c.WriteWait))
	}
	if c.PongWait <= 0 {
		problems = append(problems, fmt.Errorf("pong wait must be positive, got %v", c.PongWait))
	}
	if c.PingPeriod <= 0 {
		problems = append(problems, fmt.Errorf("ping period must be positive, got %v", c.PingPeriod))
	}
	if c.PingPeriod >= c.PongWait {
		problems = append(problems, fmt.Errorf("ping period (%v) must be shorter than pong wait (%v)", c.PingPeriod, c.PongWait))
	}
	if c.MaxMessageSize <= 0 {
		problems = append(problems, fmt.Errorf("max message size must be positive, got %d", c.MaxMessageSize))
	}
	if c.SendBufferSize <= 0 {
		problems = append(problems, fmt.Errorf("send buffer size must be positive, got %d", c.SendBufferSize))
	}
	if c.MembershipWait <= 0 {
		problems = append(problems, fmt.Errorf("membership wait must be positive, got %v", c.MembershipWait))
	}
	return errors.Join(problems...)
}
