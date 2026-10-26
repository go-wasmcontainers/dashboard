package bridge

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// pair wires a transport to a server over direct calls (what postMessage
// does between the realms, minus the copy).
func pair(h http.Handler) (*Transport, *Server, *http.Client) {
	var srv *Server
	tr := NewTransport(func(m Message) { srv.Deliver(m) })
	srv = NewServer(h, tr.Deliver)
	return tr, srv, &http.Client{Transport: tr}
}

func TestRoundTrip(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /echo", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		w.Header().Set("X-Got", r.Header.Get("X-Send"))
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(r.URL.RawQuery + ":"))
		w.Write(b)
		w.Write(nil)
	})
	mux.HandleFunc("GET /empty", func(http.ResponseWriter, *http.Request) {})
	_, _, c := pair(mux)
	req, _ := http.NewRequest(http.MethodPost, "http://engine/echo?a=1", strings.NewReader("body"))
	req.Header.Set("X-Send", "v")
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 201 || string(b) != "a=1:body" || resp.Header.Get("X-Got") != "v" {
		t.Errorf("%d %q %v", resp.StatusCode, b, resp.Header)
	}
	resp, err = c.Get("http://engine/empty")
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("empty: %v %v", resp, err)
	}
	b, _ = io.ReadAll(resp.Body)
	if len(b) != 0 {
		t.Errorf("empty body = %q", b)
	}
}

func TestStreamingAndCancel(t *testing.T) {
	stop := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("GET /follow", func(w http.ResponseWriter, r *http.Request) {
		w.(http.Flusher).Flush()
		for i := 0; ; i++ {
			select {
			case <-r.Context().Done():
				close(stop)
				return
			case <-time.After(time.Millisecond):
				io.WriteString(w, "x")
			}
		}
	})
	_, _, c := pair(mux)
	resp, err := c.Get("http://engine/follow")
	if err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 3)
	if _, err := io.ReadFull(resp.Body, buf); err != nil || string(buf) != "xxx" {
		t.Fatalf("stream: %q %v", buf, err)
	}
	resp.Body.Close()
	resp.Body.Close()
	select {
	case <-stop:
	case <-time.After(5 * time.Second):
		t.Fatal("closing the body did not cancel the handler")
	}
	if _, err := resp.Body.Read(buf); err != ErrClosed {
		t.Errorf("read after close: %v", err)
	}

	// A context cancelled while the body streams ends it, and the handler.
	stop = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://engine/follow", nil)
	resp, err = c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Error("a cancelled stream read to EOF")
	}
	<-stop
}

func TestCancelBeforeHead(t *testing.T) {
	release := make(chan struct{})
	gone := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(gone)
		<-release
	})
	_, _, c := pair(h)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://engine/slow", nil)
	if _, err := c.Do(req); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v", err)
	}
	<-gone
	close(release)
}

func TestRefusals(t *testing.T) {
	ran := false
	h := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { ran = true })
	var got []Message
	var mu sync.Mutex
	srv := NewServer(h, func(m Message) { mu.Lock(); got = append(got, m); mu.Unlock() })
	big := map[string]string{}
	for i := 0; i <= MaxHeader; i++ {
		big[strings.Repeat("k", i+1)] = "v"
	}
	for i, m := range []Message{
		{Method: "CONNECT", URL: "/x"},
		{Method: "GET", URL: "http://evil.example/x"},
		{Method: "GET", URL: "//evil.example/x"},
		{Method: "GET", URL: "/x\r\nHost: y"},
		{Method: "POST", URL: "/x", Data: make([]byte, MaxBody+1)},
		{Method: "GET", URL: "/x", Header: big},
		{Method: "GET", URL: "/x", Header: map[string]string{"K": strings.Repeat("v", maxHeaderLen)}},
		{Method: "GET", URL: "/%zz"},
	} {
		m.T, m.ID = TypeRequest, i+1
		srv.Deliver(m)
	}
	mu.Lock()
	defer mu.Unlock()
	if ran {
		t.Error("a refused request ran")
	}
	var statuses []int
	for _, m := range got {
		if m.T == TypeHead {
			statuses = append(statuses, m.Status)
		}
	}
	want := []int{405, 400, 400, 400, 413, 431, 431, 400}
	if len(statuses) != len(want) {
		t.Fatalf("statuses = %v", statuses)
	}
	for i := range want {
		if statuses[i] != want[i] {
			t.Errorf("refusal %d: %d, want %d", i, statuses[i], want[i])
		}
	}
	srv.Deliver(Message{T: "http.other", ID: 1})
	srv.Deliver(Message{T: TypeCancel, ID: 99}) // unknown: nothing
}

func TestInFlightBoundAndClose(t *testing.T) {
	release := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	})
	var mu sync.Mutex
	status := map[int]int{}
	srv := NewServer(h, func(m Message) {
		if m.T == TypeHead {
			mu.Lock()
			status[m.ID] = m.Status
			mu.Unlock()
		}
	})
	for i := 1; i <= MaxInFlight+1; i++ {
		srv.Deliver(Message{T: TypeRequest, ID: i, Method: "GET", URL: "/"})
		if i == 1 {
			srv.Deliver(Message{T: TypeRequest, ID: 1, Method: "GET", URL: "/"}) // duplicate id: dropped
		}
	}
	mu.Lock()
	if status[MaxInFlight+1] != http.StatusServiceUnavailable {
		t.Errorf("over the bound: %d", status[MaxInFlight+1])
	}
	mu.Unlock()
	srv.Close()
	srv.Deliver(Message{T: TypeRequest, ID: 1000, Method: "GET", URL: "/"})
	deadline := time.Now().Add(5 * time.Second)
	for {
		mu.Lock()
		n, closed := len(status), status[1000]
		mu.Unlock()
		if n == MaxInFlight+2 && closed == http.StatusServiceUnavailable {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after Close: %d answered, new request %d", n, closed)
		}
		time.Sleep(time.Millisecond)
	}
	close(release)
}

func TestTransportEdges(t *testing.T) {
	tr := NewTransport(func(Message) {})
	c := &http.Client{Transport: tr}
	done := make(chan *http.Response)
	go func() {
		resp, _ := c.Get("http://engine/x")
		done <- resp
	}()
	for len(pendingIDs(tr)) == 0 {
		time.Sleep(time.Millisecond)
	}
	id := pendingIDs(tr)[0]
	tr.Deliver(Message{T: TypeEnd, ID: id, Err: ""}) // ended before a head
	resp := <-done
	if resp.StatusCode != http.StatusBadGateway {
		t.Errorf("status = %d", resp.StatusCode)
	}
	tr.Deliver(Message{T: TypeData, ID: 12345}) // unknown id: dropped

	// A second head is ignored; an error end surfaces; data past the bound fails.
	go func() {
		resp, _ := c.Get("http://engine/y")
		done <- resp
	}()
	for len(pendingIDs(tr)) == 0 {
		time.Sleep(time.Millisecond)
	}
	id = pendingIDs(tr)[0]
	tr.Deliver(Message{T: TypeHead, ID: id, Status: 200, Header: map[string]string{"A": "b"}})
	tr.Deliver(Message{T: TypeHead, ID: id, Status: 500})
	resp = <-done
	tr.Deliver(Message{T: TypeData, ID: id, Data: []byte("ab")})
	tr.Deliver(Message{T: TypeEnd, ID: id, Err: "engine crashed"})
	b, err := io.ReadAll(resp.Body)
	if string(b) != "ab" || err == nil || err.Error() != "bridge: engine crashed" || resp.Header.Get("A") != "b" {
		t.Errorf("body %q err %v", b, err)
	}
	tr.Deliver(Message{T: TypeData, ID: id, Data: []byte("late")})

	go func() {
		resp, _ := c.Get("http://engine/z")
		done <- resp
	}()
	for len(pendingIDs(tr)) == 0 {
		time.Sleep(time.Millisecond)
	}
	id = pendingIDs(tr)[0]
	tr.Deliver(Message{T: TypeHead, ID: id, Status: 200})
	resp = <-done
	tr.Deliver(Message{T: TypeData, ID: id, Data: make([]byte, MaxBuffered)})
	tr.Deliver(Message{T: TypeData, ID: id, Data: []byte("over")})
	tr.Deliver(Message{T: TypeData, ID: id, Data: []byte("after")})
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Error("an over-bound body read cleanly")
	}

	// Shutdown fails what is in flight.
	go func() {
		resp, _ := c.Get("http://engine/w")
		done <- resp
	}()
	for len(pendingIDs(tr)) == 0 {
		time.Sleep(time.Millisecond)
	}
	go func() {
		id := pendingIDs(tr)[0]
		tr.Deliver(Message{T: TypeHead, ID: id, Status: 200})
	}()
	resp = <-done
	go func() {
		resp, _ := c.Get("http://engine/v")
		done <- resp
	}()
	for len(pendingIDs(tr)) < 2 {
		time.Sleep(time.Millisecond)
	}
	tr.Shutdown()
	if r := <-done; r.StatusCode != http.StatusBadGateway {
		t.Errorf("shutdown status = %d", r.StatusCode)
	}
	if _, err := io.ReadAll(resp.Body); err != ErrClosed {
		t.Errorf("shutdown body: %v", err)
	}

	// Request bodies: read errors and the bound.
	if _, err := tr.RoundTrip(mustReq(http.MethodPost, io.NopCloser(errReader{}))); err == nil {
		t.Error("a failing body was sent")
	}
	if _, err := tr.RoundTrip(mustReq(http.MethodPost, io.NopCloser(strings.NewReader(strings.Repeat("x", MaxBody+1))))); err == nil {
		t.Error("an over-bound body was sent")
	}
	if !IsBridge(TypeData) || IsBridge("line") {
		t.Error("IsBridge")
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("read failed") }

func mustReq(method string, body io.ReadCloser) *http.Request {
	r, _ := http.NewRequest(method, "http://engine/x", nil)
	r.Body = body
	return r
}

func pendingIDs(t *Transport) []int {
	t.mu.Lock()
	defer t.mu.Unlock()
	var ids []int
	for id := range t.pending {
		ids = append(ids, id)
	}
	return ids
}
