package bridge

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// FuzzServer sends arbitrary requests to a server: whatever arrives, the
// handler only ever sees a path on the engine's own host, and every
// request is answered with exactly one head and one end.
func FuzzServer(f *testing.F) {
	for _, s := range []struct{ m, u, k, v string }{
		{"GET", "/v1.47/containers/json?all=1", "Accept", "application/json"},
		{"POST", "//evil/x", "", ""},
		{"DELETE", "/images/a%2Fb", "X", strings.Repeat("y", 5000)},
		{"get", "http://x/y", "", ""},
	} {
		f.Add(s.m, s.u, s.k, s.v, []byte("{}"))
	}
	f.Fuzz(func(t *testing.T, method, url, k, v string, body []byte) {
		h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Host != "engine" || !strings.HasPrefix(r.URL.Path, "/") {
				t.Errorf("the handler saw %q", r.URL.String())
			}
			w.Write([]byte("ok"))
		})
		var mu sync.Mutex
		heads, ends := 0, 0
		done := make(chan struct{})
		srv := NewServer(h, func(m Message) {
			mu.Lock()
			defer mu.Unlock()
			switch m.T {
			case TypeHead:
				heads++
			case TypeEnd:
				ends++
				close(done)
			}
		})
		srv.Deliver(Message{T: TypeRequest, ID: 1, Method: method, URL: url, Header: map[string]string{k: v}, Data: body})
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("no end")
		}
		mu.Lock()
		defer mu.Unlock()
		if heads != 1 || ends != 1 {
			t.Fatalf("%d heads, %d ends", heads, ends)
		}
	})
}
