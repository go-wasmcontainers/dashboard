package engine

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/go-wasmcontainers/engine/api"
	"github.com/go-wasmcontainers/engine/api/inproc"
)

func newTest(h http.HandlerFunc) *HTTP {
	return New(&http.Client{Transport: &inproc.Transport{Handler: h}}, "http://engine/")
}

func TestEndpoints(t *testing.T) {
	stream := make(chan string, 4)
	e := newTest(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1.47/volumes":
			io.WriteString(w, `{"Volumes":[{"Name":"data","Driver":"local","UsageData":{"Size":5,"RefCount":1}}],"Warnings":null}`)
		case "/v1.47/system/df":
			io.WriteString(w, `{"LayersSize":42,"Images":[{"Id":"sha256:a"}],"Volumes":[]}`)
		case "/v1.47/events":
			w.(http.Flusher).Flush()
			for {
				select {
				case s := <-stream:
					io.WriteString(w, s)
					w.(http.Flusher).Flush()
				case <-r.Context().Done():
					return
				}
			}
		case "/v1.47/containers/c1/json":
			io.WriteString(w, `{"Id":"c1"}`)
		case "/v1.47/images/hello/json":
			io.WriteString(w, `{"Id":"sha256:h"}`)
		case "/v1.47/containers/c1/wait":
			w.(http.Flusher).Flush()
			io.WriteString(w, `{"StatusCode":0}`)
		case "/v1.47/containers/gone/wait":
			w.WriteHeader(http.StatusNotFound)
			io.WriteString(w, `{"message":"no such container: gone"}`)
		default:
			http.NotFound(w, r)
		}
	})
	ctx := context.Background()
	vols, err := e.Volumes(ctx)
	if err != nil || len(vols) != 1 || vols[0].UsageData.Size != 5 {
		t.Errorf("Volumes = %+v, %v", vols, err)
	}
	df, err := e.DiskUsage(ctx)
	if err != nil || df.LayersSize != 42 || len(df.Images) != 1 {
		t.Errorf("DiskUsage = %+v, %v", df, err)
	}
	if raw, err := e.InspectContainer(ctx, "c1"); err != nil || string(raw) != `{"Id":"c1"}` {
		t.Errorf("InspectContainer = %s, %v", raw, err)
	}
	if raw, err := e.InspectImage(ctx, "hello"); err != nil || string(raw) != `{"Id":"sha256:h"}` {
		t.Errorf("InspectImage = %s, %v", raw, err)
	}
	wait, err := e.WaitStopped(ctx, "c1")
	if err != nil || wait() != nil {
		t.Errorf("WaitStopped: %v", err)
	}
	if _, err := e.WaitStopped(ctx, "gone"); !api.IsNotFound(err) {
		t.Errorf("WaitStopped(gone) = %v", err)
	}

	// Events: two events, then a broken line ends the stream.
	stream <- `{"Type":"container","Action":"start","Actor":{"ID":"c1","Attributes":{"name":"web"}},"time":1}` + "\n"
	stream <- `{"Type":"image","Action":"pull"}` + "\n"
	stream <- "{oops\n"
	var got []string
	err = e.Events(ctx, func(ev Event) { got = append(got, ev.Type+"/"+ev.Action+"/"+ev.Actor.Attributes["name"]) })
	if err == nil || !strings.HasPrefix(err.Error(), "bad event") || strings.Join(got, ",") != "container/start/web,image/pull/" {
		t.Errorf("Events = %v, %v", got, err)
	}
	// Cancelled while streaming.
	cctx, cancel := context.WithCancel(ctx)
	stream <- `{"Type":"container","Action":"die"}` + "\n"
	err = e.Events(cctx, func(Event) { cancel() })
	if !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled Events = %v", err)
	}
}

func TestErrors(t *testing.T) {
	status := 0
	body := ""
	e := newTest(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		io.WriteString(w, body)
	})
	ctx := context.Background()
	for _, c := range []struct {
		status      int
		body        string
		unsupported bool
		msg         string
	}{
		{404, "404 page not found\n", true, ""},
		{405, "Method Not Allowed\n", true, ""},
		{404, `{"message":"no such volume"}`, false, "no such volume"},
		{500, "boom\n", false, "boom"},
		{500, `{"message":"disk full"}`, false, "disk full"},
	} {
		status, body = c.status, c.body
		_, err := e.Volumes(ctx)
		if errors.Is(err, ErrUnsupported) != c.unsupported {
			t.Errorf("%d %q: %v", c.status, c.body, err)
		}
		var d *api.ErrDaemon
		if !c.unsupported && (!errors.As(err, &d) || d.Message != c.msg || d.Status != c.status) {
			t.Errorf("%d %q: %v", c.status, c.body, err)
		}
	}
	status, body = 200, ""
	if err := e.Events(ctx, func(Event) {}); err != nil {
		t.Errorf("an ended stream: %v", err)
	}
	status, body = 200, "{not json"
	if _, err := e.DiskUsage(ctx); err == nil {
		t.Error("bad JSON accepted")
	}
	// No engine at all.
	dead := New(&http.Client{Transport: deadRT{}}, "http://engine")
	if _, err := dead.Volumes(ctx); err == nil || !strings.Contains(err.Error(), "cannot connect to the engine") {
		t.Errorf("dead = %v", err)
	}
	if err := dead.Events(ctx, func(Event) {}); err == nil {
		t.Error("dead events")
	}
	bad := New(http.DefaultClient, "http://bad host")
	if _, err := bad.Volumes(ctx); err == nil {
		t.Error("a bad URL was requested")
	}
}

type deadRT struct{}

func (deadRT) RoundTrip(*http.Request) (*http.Response, error) { return nil, errors.New("refused") }
