package testutil

import (
	"context"
	"net"
	"sync"
)

type BlackholeConn struct {
	net.Conn
	holed     chan struct{}
	closed    chan struct{}
	holeOnce  sync.Once
	closeOnce sync.Once
}

func NewBlackholeConn(conn net.Conn) *BlackholeConn {
	return &BlackholeConn{Conn: conn, holed: make(chan struct{}), closed: make(chan struct{})}
}

func DialBlackhole(ctx context.Context, network, addr string) (*BlackholeConn, error) {
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, network, addr)
	if err != nil {
		return nil, err
	}
	return NewBlackholeConn(conn), nil
}

func (c *BlackholeConn) Blackhole() {
	c.holeOnce.Do(func() { close(c.holed) })
}

func (c *BlackholeConn) Read(p []byte) (int, error) {
	if c.isHoled() {
		return c.blockUntilClosed()
	}
	n, err := c.Conn.Read(p)
	if c.isHoled() {
		return c.blockUntilClosed()
	}
	return n, err
}

func (c *BlackholeConn) Write(p []byte) (int, error) {
	if c.isHoled() {
		return len(p), nil
	}
	return c.Conn.Write(p)
}

func (c *BlackholeConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

func (c *BlackholeConn) isHoled() bool {
	select {
	case <-c.holed:
		return true
	default:
		return false
	}
}

func (c *BlackholeConn) blockUntilClosed() (int, error) {
	<-c.closed
	return 0, net.ErrClosed
}
