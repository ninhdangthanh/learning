package ws

import "time"

type Config struct {
	WriteWait      time.Duration
	PongWait       time.Duration
	PingPeriod     time.Duration
	MaxMessageSize int64
	SendBufferSize int
}

func DefaultConfig() Config {
	pongWait := 60 * time.Second
	return Config{
		WriteWait:      10 * time.Second,
		PongWait:       pongWait,
		PingPeriod:     pongWait * 9 / 10,
		MaxMessageSize: 4096,
		SendBufferSize: 16,
	}
}
