package tcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// Client is a concurrent-safe persistent TCP client.
type Client struct {
	conn      net.Conn
	token     string
	maxFrame  int
	writeMu   sync.Mutex
	mu        sync.Mutex
	pending   map[string]chan Response
	seq       atomic.Uint64
	done      chan struct{}
	err       error
	closeOnce sync.Once
}

// Dial connects to a TCP server.
func Dial(addr, token string, timeout time.Duration) (*Client, error) {
	conn, err := net.DialTimeout("tcp", addr, timeout)
	if err != nil {
		return nil, err
	}
	c := &Client{conn: conn, token: token, maxFrame: DefaultMaxFrame, pending: make(map[string]chan Response), done: make(chan struct{})}
	go c.readLoop()
	return c, nil
}

// Call sends one request and decodes data into out. It supports concurrent use.
func (c *Client) Call(ctx context.Context, action string, params map[string]any, out any) error {
	id := fmt.Sprintf("%d", c.seq.Add(1))
	ch := make(chan Response, 1)
	c.mu.Lock()
	select {
	case <-c.done:
		err := c.err
		c.mu.Unlock()
		if err == nil {
			err = errors.New("tcpserver: client closed")
		}
		return err
	default:
	}
	c.pending[id] = ch
	c.mu.Unlock()
	req := Request{ID: id, Action: action, Token: c.token, Params: params}
	c.writeMu.Lock()
	err := writeFrame(c.conn, req, c.maxFrame)
	c.writeMu.Unlock()
	if err != nil {
		c.remove(id)
		return err
	}
	select {
	case resp := <-ch:
		if resp.Code != 0 {
			return fmt.Errorf("tcpserver: status=%d: %s", resp.Status, resp.Msg)
		}
		if out != nil && len(resp.Data) > 0 {
			return json.Unmarshal(resp.Data, out)
		}
		return nil
	case <-ctx.Done():
		c.remove(id)
		return ctx.Err()
	case <-c.done:
		return c.terminalError()
	}
}

func (c *Client) remove(id string) { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }

func (c *Client) terminalError() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return c.err
	}
	return errors.New("tcpserver: client closed")
}

func (c *Client) readLoop() {
	for {
		frame, err := readFrame(c.conn, c.maxFrame)
		if err != nil {
			c.shutdown(err)
			return
		}
		var resp Response
		if err := json.Unmarshal(frame, &resp); err != nil {
			c.shutdown(err)
			return
		}
		c.mu.Lock()
		ch := c.pending[resp.ID]
		delete(c.pending, resp.ID)
		c.mu.Unlock()
		if ch != nil {
			ch <- resp
		}
	}
}

func (c *Client) shutdown(err error) {
	c.closeOnce.Do(func() {
		c.mu.Lock()
		c.err = err
		close(c.done)
		c.pending = make(map[string]chan Response)
		c.mu.Unlock()
		_ = c.conn.Close()
	})
}

// Close closes the persistent connection.
func (c *Client) Close() error { c.shutdown(nil); return nil }

// Done closes when the connection stops.
func (c *Client) Done() <-chan struct{} { return c.done }
