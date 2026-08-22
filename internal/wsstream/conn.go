// Package wsstream adapts binary WebSocket messages to a concurrency-safe net.Conn.
package wsstream

import (
	"context"
	"net"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// Conn serializes writes because net.Conn callers such as yamux may write from
// several goroutines even though a WebSocket permits only one writer at a time.
type Conn struct {
	underlying net.Conn
	writeMu    sync.Mutex
	closeOnce  sync.Once
	closeErr   error
}

func New(ctx context.Context, websocketConn *websocket.Conn) *Conn {
	return &Conn{underlying: websocket.NetConn(ctx, websocketConn, websocket.MessageBinary)}
}

func (c *Conn) Read(contents []byte) (int, error) {
	return c.underlying.Read(contents)
}

func (c *Conn) Write(contents []byte) (int, error) {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.underlying.Write(contents)
}

func (c *Conn) Close() error {
	c.closeOnce.Do(func() { c.closeErr = c.underlying.Close() })
	return c.closeErr
}

func (c *Conn) LocalAddr() net.Addr  { return c.underlying.LocalAddr() }
func (c *Conn) RemoteAddr() net.Addr { return c.underlying.RemoteAddr() }

func (c *Conn) SetDeadline(deadline time.Time) error {
	return c.underlying.SetDeadline(deadline)
}

func (c *Conn) SetReadDeadline(deadline time.Time) error {
	return c.underlying.SetReadDeadline(deadline)
}

func (c *Conn) SetWriteDeadline(deadline time.Time) error {
	return c.underlying.SetWriteDeadline(deadline)
}
