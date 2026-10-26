// Package bridge carries HTTP requests between two JavaScript realms over
// postMessage: the dashboard runs on the page (it needs the canvas) and
// the engine in a Web Worker (it needs Atomics.wait), and the dashboard's
// API client — and wctr, run in the dashboard's terminal — reach the
// engine's API handler through this bridge. Responses stream, so the log
// follow, wait and pull endpoints work as over a socket.
//
// The protocol is five message types, all carrying the request's id:
//
//	page -> worker   http.req     {method, url, header, data}
//	page -> worker   http.cancel  {}
//	worker -> page   http.head    {status, header}
//	worker -> page   http.data    {data}
//	worker -> page   http.end     {err}
//
// The core here is plain Go over a post function, natively tested; the
// JavaScript glue (bridge_js.go) only converts Message to and from a JS
// object. The "http." prefix lets the messages share a worker's channel
// with other traffic (the playground's terminal messages).
//
// Security: the server accepts only a path (never a scheme or host, so a
// page cannot aim the engine's handler elsewhere), a fixed set of
// methods, a bounded body, a bounded header, and a bounded number of
// requests in flight; the client bounds what it buffers per response.
package bridge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
)

// Message types.
const (
	TypeRequest = "http.req"
	TypeCancel  = "http.cancel"
	TypeHead    = "http.head"
	TypeData    = "http.data"
	TypeEnd     = "http.end"
)

// Bounds.
const (
	MaxBody      = 1 << 20  // a request body
	MaxHeader    = 32       // header fields of a request or response
	MaxInFlight  = 64       // concurrent requests a server runs
	MaxBuffered  = 16 << 20 // response bytes a client holds unread
	maxHeaderLen = 4096
)

// Message is one message of the protocol.
type Message struct {
	T      string
	ID     int
	Method string
	URL    string
	Header map[string]string
	Data   []byte
	Status int
	Err    string
}

// IsBridge reports whether a message type belongs to the protocol.
func IsBridge(t string) bool { return strings.HasPrefix(t, "http.") }

// Server runs requests that arrive as messages through an http.Handler.
type Server struct {
	h    http.Handler
	post func(Message)

	mu     sync.Mutex
	reqs   map[int]context.CancelFunc
	closed bool
}

// NewServer serves h; post sends a message back to the client.
func NewServer(h http.Handler, post func(Message)) *Server {
	return &Server{h: h, post: post, reqs: map[int]context.CancelFunc{}}
}

var methods = map[string]bool{http.MethodGet: true, http.MethodHead: true, http.MethodPost: true, http.MethodDelete: true, http.MethodPut: true}

// Deliver handles a message from the client. It never blocks.
func (s *Server) Deliver(m Message) {
	switch m.T {
	case TypeRequest:
		s.start(m)
	case TypeCancel:
		s.mu.Lock()
		cancel := s.reqs[m.ID]
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
}

// Close cancels every request in flight and refuses new ones.
func (s *Server) Close() {
	s.mu.Lock()
	s.closed = true
	for _, c := range s.reqs {
		c()
	}
	s.mu.Unlock()
}

func (s *Server) fail(id, status int, msg string) {
	s.post(Message{T: TypeHead, ID: id, Status: status, Header: map[string]string{"Content-Type": "application/json"}})
	s.post(Message{T: TypeData, ID: id, Data: []byte(fmt.Sprintf("{\"message\":%q}\n", msg))})
	s.post(Message{T: TypeEnd, ID: id})
}

func (s *Server) start(m Message) {
	switch {
	case !methods[m.Method]:
		s.fail(m.ID, http.StatusMethodNotAllowed, "method not allowed over the bridge")
		return
	case !strings.HasPrefix(m.URL, "/") || strings.HasPrefix(m.URL, "//") || strings.ContainsAny(m.URL, "\\\r\n"):
		s.fail(m.ID, http.StatusBadRequest, "the bridge carries a path only")
		return
	case len(m.Data) > MaxBody:
		s.fail(m.ID, http.StatusRequestEntityTooLarge, "request body too large")
		return
	case len(m.Header) > MaxHeader:
		s.fail(m.ID, http.StatusRequestHeaderFieldsTooLarge, "too many header fields")
		return
	}
	req, err := http.NewRequest(m.Method, "http://engine"+m.URL, bytes.NewReader(m.Data))
	if err != nil {
		s.fail(m.ID, http.StatusBadRequest, "bad request: "+err.Error())
		return
	}
	for k, v := range m.Header {
		if len(k)+len(v) > maxHeaderLen {
			s.fail(m.ID, http.StatusRequestHeaderFieldsTooLarge, "header field too large")
			return
		}
		req.Header.Set(k, v)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	switch {
	case s.closed:
		s.mu.Unlock()
		cancel()
		s.fail(m.ID, http.StatusServiceUnavailable, "the engine is shutting down")
		return
	case len(s.reqs) >= MaxInFlight:
		s.mu.Unlock()
		cancel()
		s.fail(m.ID, http.StatusServiceUnavailable, "too many requests in flight")
		return
	case s.reqs[m.ID] != nil:
		s.mu.Unlock()
		cancel()
		return // a duplicate id: the first one owns it
	}
	s.reqs[m.ID] = cancel
	s.mu.Unlock()
	req = req.WithContext(ctx)
	req.RequestURI = m.URL
	req.RemoteAddr = "bridge"
	go func() {
		w := &writer{s: s, id: m.ID, header: http.Header{}}
		defer func() {
			w.WriteHeader(http.StatusOK) // a handler that wrote nothing
			s.post(Message{T: TypeEnd, ID: m.ID})
			s.mu.Lock()
			delete(s.reqs, m.ID)
			s.mu.Unlock()
			cancel()
		}()
		s.h.ServeHTTP(w, req)
	}()
}

type writer struct {
	s      *Server
	id     int
	header http.Header
	wrote  bool
}

func (w *writer) Header() http.Header { return w.header }

func (w *writer) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.wrote = true
	h := map[string]string{}
	for k, v := range w.header {
		if len(h) < MaxHeader && len(v) > 0 {
			h[k] = v[0]
		}
	}
	w.s.post(Message{T: TypeHead, ID: w.id, Status: code, Header: h})
}

func (w *writer) Write(p []byte) (int, error) {
	w.WriteHeader(http.StatusOK)
	if len(p) > 0 {
		w.s.post(Message{T: TypeData, ID: w.id, Data: append([]byte(nil), p...)})
	}
	return len(p), nil
}

// Flush implements http.Flusher: every Write is already posted.
func (w *writer) Flush() { w.WriteHeader(http.StatusOK) }

// Transport is the client side: an http.RoundTripper whose requests go
// out as messages and whose responses come back through Deliver.
type Transport struct {
	post func(Message)

	mu      sync.Mutex
	next    int
	pending map[int]*call
}

// NewTransport sends requests with post.
func NewTransport(post func(Message)) *Transport {
	return &Transport{post: post, pending: map[int]*call{}}
}

type call struct {
	head     chan Message
	answered bool // guarded by Transport.mu
	body     *body
}

// ErrClosed is the error of a body read after the server went away.
var ErrClosed = errors.New("bridge: the engine closed the connection")

// RoundTrip implements http.RoundTripper.
func (t *Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	var data []byte
	if req.Body != nil {
		b, err := io.ReadAll(io.LimitReader(req.Body, MaxBody+1))
		req.Body.Close()
		if err != nil {
			return nil, err
		}
		if len(b) > MaxBody {
			return nil, fmt.Errorf("bridge: request body over %d bytes", MaxBody)
		}
		data = b
	}
	h := map[string]string{}
	for k, v := range req.Header {
		if len(v) > 0 {
			h[k] = v[0]
		}
	}
	c := &call{head: make(chan Message, 1), body: newBody()}
	t.mu.Lock()
	t.next++
	id := t.next
	t.pending[id] = c
	t.mu.Unlock()
	c.body.cancel = func() { t.cancel(id) }
	t.post(Message{T: TypeRequest, ID: id, Method: req.Method, URL: req.URL.RequestURI(), Header: h, Data: data})
	select {
	case m := <-c.head:
		hdr := http.Header{}
		for k, v := range m.Header {
			hdr.Set(k, v)
		}
		go func() {
			select {
			case <-req.Context().Done():
				c.body.CloseWithError(req.Context().Err())
				c.body.Close()
			case <-c.body.done:
			}
		}()
		return &http.Response{
			Status: fmt.Sprintf("%d %s", m.Status, http.StatusText(m.Status)), StatusCode: m.Status,
			Proto: "HTTP/1.1", ProtoMajor: 1, ProtoMinor: 1,
			Header: hdr, Body: c.body, ContentLength: -1, Request: req,
		}, nil
	case <-req.Context().Done():
		c.body.Close()
		return nil, req.Context().Err()
	}
}

func (t *Transport) cancel(id int) {
	t.mu.Lock()
	_, ok := t.pending[id]
	delete(t.pending, id)
	t.mu.Unlock()
	if ok {
		t.post(Message{T: TypeCancel, ID: id})
	}
}

// Deliver handles a message from the server. It never blocks.
func (t *Transport) Deliver(m Message) {
	t.mu.Lock()
	c := t.pending[m.ID]
	if m.T == TypeEnd {
		delete(t.pending, m.ID)
	}
	t.mu.Unlock()
	if c == nil {
		return
	}
	switch m.T {
	case TypeHead:
		t.answer(c, m) // a second head is ignored
	case TypeData:
		c.body.push(m.Data)
	case TypeEnd:
		t.answer(c, Message{Status: http.StatusBadGateway}) // ended before any head
		if m.Err != "" {
			c.body.CloseWithError(errors.New("bridge: " + m.Err))
		} else {
			c.body.CloseWithError(io.EOF)
		}
	}
}

// answer hands the call its head, once; it never blocks (head holds one).
func (t *Transport) answer(c *call, m Message) {
	t.mu.Lock()
	first := !c.answered
	c.answered = true
	t.mu.Unlock()
	if first {
		c.head <- m
	}
}

// Shutdown fails every request in flight (the worker died).
func (t *Transport) Shutdown() {
	t.mu.Lock()
	calls := t.pending
	t.pending = map[int]*call{}
	t.mu.Unlock()
	for _, c := range calls {
		t.answer(c, Message{Status: http.StatusBadGateway})
		c.body.CloseWithError(ErrClosed)
	}
}

// body is a response body fed by Deliver: it buffers without blocking
// the sender, up to MaxBuffered bytes, and blocks the reader.
type body struct {
	mu     sync.Mutex
	cond   *sync.Cond
	chunks [][]byte
	size   int
	err    error
	cancel func()
	once   sync.Once
	done   chan struct{} // closed when err is set
}

func newBody() *body {
	b := &body{done: make(chan struct{})}
	b.cond = sync.NewCond(&b.mu)
	return b
}

func (b *body) push(p []byte) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.err != nil {
		return
	}
	if b.size+len(p) > MaxBuffered {
		b.setErr(fmt.Errorf("bridge: over %d unread response bytes", MaxBuffered))
		go b.Close()
		return
	}
	b.chunks = append(b.chunks, p)
	b.size += len(p)
	b.cond.Broadcast()
}

func (b *body) Read(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for len(b.chunks) == 0 && b.err == nil {
		b.cond.Wait()
	}
	if len(b.chunks) == 0 {
		return 0, b.err
	}
	n := copy(p, b.chunks[0])
	b.size -= n
	if n == len(b.chunks[0]) {
		b.chunks = b.chunks[1:]
	} else {
		b.chunks[0] = b.chunks[0][n:]
	}
	return n, nil
}

// CloseWithError ends the body: reads return what is buffered, then err.
func (b *body) CloseWithError(err error) {
	b.mu.Lock()
	b.setErr(err)
	b.mu.Unlock()
}

// setErr records the first error and wakes readers; b.mu is held.
func (b *body) setErr(err error) {
	if b.err == nil {
		b.err = err
		close(b.done)
	}
	b.cond.Broadcast()
}

// Close ends the request: the server's handler is cancelled.
func (b *body) Close() error {
	b.once.Do(func() {
		b.CloseWithError(ErrClosed)
		b.mu.Lock()
		b.chunks, b.size = nil, 0
		b.mu.Unlock()
		if b.cancel != nil {
			b.cancel()
		}
	})
	return nil
}
