package cdp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
)

// message is one frame of the protocol in either direction: a command, the
// reply to one, or an event.
type message struct {
	ID        int64           `json:"id,omitempty"`
	SessionID string          `json:"sessionId,omitempty"`
	Method    string          `json:"method,omitempty"`
	Params    json.RawMessage `json:"params,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
	Error     *protocolError  `json:"error,omitempty"`
}

// protocolError is the error object Chrome answers a refused command with.
type protocolError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    string `json:"data,omitempty"`
}

func (e *protocolError) Error() string {
	if e.Data != "" {
		return e.Message + " (" + e.Data + ")"
	}
	return e.Message
}

// conn speaks the protocol over Chrome's debugging pipe: JSON messages, each
// ended by a NUL byte, commands written to the browser's fd 3 and replies and
// events read from its fd 4. There is no port to find and no WebSocket to
// frame: the pipe is the transport, and it dies with the process.
//
// Replies are routed to their command by id, events to the session that
// raised them.
type conn struct {
	w   io.Writer
	wmu sync.Mutex

	mu       sync.Mutex
	next     int64
	pending  map[int64]chan message
	sessions map[string]func(method string, params json.RawMessage)
	done     chan struct{}
	err      error
	closing  func(error) error
}

// newConn starts reading r. closing, when set, turns the reason the pipe
// closed into the error every pending and later call returns -- the driver
// uses it to add what the browser wrote to stderr before it died.
func newConn(r io.Reader, w io.Writer, closing func(error) error) *conn {
	c := &conn{
		w:        w,
		pending:  map[int64]chan message{},
		sessions: map[string]func(string, json.RawMessage){},
		done:     make(chan struct{}),
		closing:  closing,
	}
	go c.read(bufio.NewReaderSize(r, 1<<20))
	return c
}

func (c *conn) read(r *bufio.Reader) {
	for {
		frame, err := r.ReadBytes(0)
		if err != nil {
			if errors.Is(err, io.EOF) {
				err = errors.New("the browser closed the debugging pipe")
			}
			c.shut(err)
			return
		}
		var m message
		if err := json.Unmarshal(frame[:len(frame)-1], &m); err != nil {
			c.shut(fmt.Errorf("the browser sent a frame that is not JSON: %w", err))
			return
		}
		if m.ID != 0 {
			c.mu.Lock()
			reply := c.pending[m.ID]
			delete(c.pending, m.ID)
			c.mu.Unlock()
			if reply != nil {
				reply <- m
			}
			continue
		}
		c.mu.Lock()
		handler := c.sessions[m.SessionID]
		c.mu.Unlock()
		if handler != nil && m.Method != "" {
			handler(m.Method, m.Params)
		}
	}
}

// shut fails every pending call and every later one with err.
func (c *conn) shut(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.err != nil {
		return
	}
	if c.closing != nil {
		err = c.closing(err)
	}
	c.err = err
	close(c.done)
}

// listen hands the events of one session to fn, on the reading goroutine. fn
// must not block and must not call the connection. An empty session is the
// browser's own.
func (c *conn) listen(session string, fn func(method string, params json.RawMessage)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sessions[session] = fn
}

// call sends a command to a session and decodes its result into result, which
// may be nil. A refused command is an error carrying Chrome's own text.
func (c *conn) call(ctx context.Context, session, method string, params, result any) error {
	m := message{SessionID: session, Method: method}
	if params != nil {
		encoded, err := json.Marshal(params)
		if err != nil {
			return fmt.Errorf("cdp: %s: encode params: %w", method, err)
		}
		m.Params = encoded
	}
	reply := make(chan message, 1)
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return fmt.Errorf("cdp: %s: %w", method, err)
	}
	c.next++
	m.ID = c.next
	c.pending[m.ID] = reply
	c.mu.Unlock()

	frame, err := json.Marshal(m)
	if err != nil {
		c.forget(m.ID)
		return fmt.Errorf("cdp: %s: %w", method, err)
	}
	if err := c.write(ctx, append(frame, 0)); err != nil {
		c.forget(m.ID)
		return fmt.Errorf("cdp: %s: write: %w", method, err)
	}
	select {
	case r := <-reply:
		if r.Error != nil {
			return fmt.Errorf("cdp: %s: %w", method, r.Error)
		}
		if result != nil && len(r.Result) > 0 {
			if err := json.Unmarshal(r.Result, result); err != nil {
				return fmt.Errorf("cdp: %s: decode result: %w", method, err)
			}
		}
		return nil
	case <-ctx.Done():
		c.forget(m.ID)
		return fmt.Errorf("cdp: %s: %w", method, ctx.Err())
	case <-c.done:
		return fmt.Errorf("cdp: %s: %w", method, c.closed())
	}
}

func (c *conn) forget(id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pending, id)
}

func (c *conn) closed() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// write sends one frame whole. A pipe that supports deadlines gets the
// context's, so a browser that stopped reading cannot hang the test.
func (c *conn) write(ctx context.Context, frame []byte) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	if d, ok := c.w.(interface{ SetWriteDeadline(time.Time) error }); ok {
		deadline, _ := ctx.Deadline()
		_ = d.SetWriteDeadline(deadline)
	}
	_, err := c.w.Write(frame)
	return err
}
