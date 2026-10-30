package bridge

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// echo answers each line of the request body as it arrives, then "eof".
func echo(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	sc := bufio.NewScanner(r.Body)
	for sc.Scan() {
		w.Write([]byte("got " + sc.Text() + "\n"))
	}
	if sc.Err() == nil {
		w.Write([]byte("eof\n"))
	}
}

// A body of unknown length streams: the handler gets each line while the
// client is still writing, and the client reads the answer before it
// writes the next -- an interactive process over the bridge.
func TestStreamedBody(t *testing.T) {
	_, _, c := pair(http.HandlerFunc(echo))
	pr, pw := io.Pipe()
	req, _ := http.NewRequest(http.MethodPost, "http://engine/exec/x/start", io.MultiReader(strings.NewReader("first\n"), pr))
	if !streams(req) {
		t.Fatal("a stdin-like body is not streamed")
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	out := bufio.NewReader(resp.Body)
	line := func() string {
		s, err := out.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	if l := line(); l != "got first\n" {
		t.Errorf("first = %q", l)
	}
	for _, s := range []string{"ls", strings.Repeat("x", chunkSize+10)} {
		if _, err := pw.Write([]byte(s + "\n")); err != nil {
			t.Fatal(err)
		}
		if l := line(); l != "got "+s+"\n" {
			t.Errorf("echo of %d bytes = %d bytes", len(s), len(l))
		}
	}
	pw.Close()
	if l := line(); l != "eof\n" {
		t.Errorf("after close = %q", l)
	}
	resp.Body.Close()
}

// Known-length bodies are not streamed, whatever their size.
func TestStreams(t *testing.T) {
	for _, c := range []struct {
		body io.Reader
		want bool
	}{
		{nil, false},
		{strings.NewReader("x"), false},
		{bytes.NewReader([]byte("x")), false},
		{io.MultiReader(strings.NewReader("x")), true},
	} {
		req, _ := http.NewRequest(http.MethodPost, "http://engine/", c.body)
		if got := streams(req); got != c.want {
			t.Errorf("%T: %v", c.body, got)
		}
	}
	req, _ := http.NewRequest(http.MethodPost, "http://engine/", nil)
	req.Body = http.NoBody
	if streams(req) {
		t.Error("NoBody streams")
	}
}

// Hanging up (closing the response) cancels the request and stops the
// pump; the handler's stdin ends.
func TestStreamedCancel(t *testing.T) {
	ended := make(chan error, 1)
	_, _, c := pair(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		_, err := io.Copy(io.Discard, r.Body)
		ended <- err
	}))
	pr, pw := io.Pipe()
	req, _ := http.NewRequest(http.MethodPost, "http://engine/x", io.MultiReader(pr))
	resp, err := c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	select {
	case err := <-ended:
		if err == nil {
			t.Error("the handler's stdin ended cleanly on a hang-up")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the handler never saw the hang-up")
	}
	// Writes after the hang-up go nowhere; closing ends the pump.
	go pw.Write([]byte("late\n"))
	pw.Close()
}

// The server side of a streamed body: chunks for nobody are ignored, an
// oversized chunk or too much unread input ends it with an error, and
// Close unblocks a handler reading it.
func TestStreamedServerEdges(t *testing.T) {
	var mu sync.Mutex
	var got []Message
	reads := make(chan error, 4)
	srv := NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.Copy(io.Discard, r.Body)
		reads <- err
	}), func(m Message) { mu.Lock(); got = append(got, m); mu.Unlock() })
	srv.Deliver(Message{T: TypeBody, ID: 99, Data: []byte("nobody")})
	srv.Deliver(Message{T: TypeEOF, ID: 99})

	srv.Deliver(Message{T: TypeRequest, ID: 1, Method: "POST", URL: "/a", Stream: true})
	srv.Deliver(Message{T: TypeBody, ID: 1, Data: make([]byte, chunkSize+1)})
	if err := <-reads; err == nil || !strings.Contains(err.Error(), "chunk over") {
		t.Errorf("oversized chunk: %v", err)
	}

	srv.Deliver(Message{T: TypeRequest, ID: 2, Method: "POST", URL: "/b", Stream: true})
	srv.Deliver(Message{T: TypeCancel, ID: 2})
	if err := <-reads; err == nil {
		t.Error("a cancelled body ended cleanly")
	}

	// The handler reads nothing until the buffer overflows.
	block := make(chan struct{})
	over := make(chan error, 1)
	srv2 := NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block
		_, err := io.Copy(io.Discard, r.Body)
		over <- err
	}), func(Message) {})
	srv2.Deliver(Message{T: TypeRequest, ID: 1, Method: "POST", URL: "/c", Stream: true})
	for n := 0; n <= MaxBuffered/chunkSize; n++ {
		srv2.Deliver(Message{T: TypeBody, ID: 1, Data: make([]byte, chunkSize)})
	}
	close(block)
	if err := <-over; err == nil || !strings.Contains(err.Error(), "unread") {
		t.Errorf("overflow: %v", err)
	}

	srv.Deliver(Message{T: TypeRequest, ID: 3, Method: "POST", URL: "/d", Stream: true})
	srv.Deliver(Message{T: TypeBody, ID: 3, Data: []byte("x")})
	time.Sleep(10 * time.Millisecond)
	srv.Close()
	if err := <-reads; !errors.Is(err, ErrClosed) {
		t.Errorf("close: %v", err)
	}
}
