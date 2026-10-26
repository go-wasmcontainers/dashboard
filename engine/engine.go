// Package engine is what the dashboard needs from a container engine: the
// calls of an API compatible with the Docker Engine API, as the engine of
// go-wasmcontainers serves them. The viewmodels depend on the Engine
// interface only, so they test against a fake as well as against a real
// engine served in-process.
//
// Endpoints a given engine does not serve yet (volumes, disk usage, the
// event stream) answer ErrUnsupported, told apart from an ordinary "not
// found" by the router's own 404 (a plain-text body, not the API's JSON
// error). The dashboard then says what it needs instead of inventing data,
// and lights the feature up by itself once the engine serves it.
package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-wasmcontainers/engine/api"
)

// ErrUnsupported is returned for an endpoint the engine does not serve.
var ErrUnsupported = errors.New("not served by this engine")

// Volume is one entry of GET /volumes (Docker Engine API v1.47, Volume).
type Volume struct {
	Name       string            `json:"Name"`
	Driver     string            `json:"Driver"`
	Mountpoint string            `json:"Mountpoint"`
	CreatedAt  string            `json:"CreatedAt"`
	Labels     map[string]string `json:"Labels"`
	Scope      string            `json:"Scope"`
	UsageData  *struct {
		Size     int64 `json:"Size"`
		RefCount int64 `json:"RefCount"`
	} `json:"UsageData"`
}

// DiskUsage is GET /system/df (SystemDataUsageResponse, the parts shown).
type DiskUsage struct {
	LayersSize int64                  `json:"LayersSize"`
	Images     []api.ImageSummary     `json:"Images"`
	Containers []api.ContainerSummary `json:"Containers"`
	Volumes    []Volume               `json:"Volumes"`
}

// Event is one message of GET /events (EventMessage).
type Event struct {
	Type   string `json:"Type"`
	Action string `json:"Action"`
	Actor  struct {
		ID         string            `json:"ID"`
		Attributes map[string]string `json:"Attributes"`
	} `json:"Actor"`
	Time int64 `json:"time"`
}

// Engine is the API the dashboard drives.
type Engine interface {
	Version(ctx context.Context) (api.SystemVersion, error)
	Containers(ctx context.Context, all bool) ([]api.ContainerSummary, error)
	Images(ctx context.Context) ([]api.ImageSummary, error)
	InspectContainer(ctx context.Context, id string) (json.RawMessage, error)
	InspectImage(ctx context.Context, ref string) (json.RawMessage, error)
	Start(ctx context.Context, id string) error
	Kill(ctx context.Context, id string) error
	// WaitStopped registers a wait for the container to stop; the
	// returned function blocks until it has.
	WaitStopped(ctx context.Context, id string) (func() error, error)
	Remove(ctx context.Context, id string, force bool) error
	Create(ctx context.Context, name string, req api.ContainerCreateRequest) (api.ContainerCreateResponse, error)
	Pull(ctx context.Context, ref string, progress func(api.JSONMessage)) error
	RemoveImage(ctx context.Context, ref string, force bool) ([]api.ImageDeleteResponseItem, error)
	// Logs returns the multiplexed log stream (see api.Demux).
	Logs(ctx context.Context, id string, follow bool) (io.ReadCloser, error)
	Volumes(ctx context.Context) ([]Volume, error)
	DiskUsage(ctx context.Context) (DiskUsage, error)
	// Events calls fn for each event until ctx ends or the stream does.
	Events(ctx context.Context, fn func(Event)) error
}

// HTTP is the Engine over an HTTP client: a unix socket, or the in-process
// transport (api/inproc) the browser uses.
type HTTP struct {
	*api.Client
	hc   *http.Client
	base string
}

var _ Engine = (*HTTP)(nil)

// New speaks to the engine at base (e.g. "http://engine") through hc.
func New(hc *http.Client, base string) *HTTP {
	base = strings.TrimSuffix(base, "/")
	return &HTTP{Client: api.NewClientWith(hc, base), hc: hc, base: base + "/v" + api.APIVersion}
}

// InspectContainer inspects a container.
func (h *HTTP) InspectContainer(ctx context.Context, id string) (json.RawMessage, error) {
	return h.Client.Inspect(ctx, id)
}

// WaitStopped waits for the container to be not running.
func (h *HTTP) WaitStopped(ctx context.Context, id string) (func() error, error) {
	w, err := h.Client.Wait(ctx, id, "not-running")
	if err != nil {
		return nil, err
	}
	return func() error { _, err := w.Result(); return err }, nil
}

// InspectImage inspects an image.
func (h *HTTP) InspectImage(ctx context.Context, ref string) (json.RawMessage, error) {
	return h.Client.ImageInspect(ctx, ref)
}

// Volumes lists volumes.
func (h *HTTP) Volumes(ctx context.Context) ([]Volume, error) {
	var out struct {
		Volumes []Volume `json:"Volumes"`
	}
	err := h.get(ctx, "/volumes", &out)
	return out.Volumes, err
}

// DiskUsage reports storage use.
func (h *HTTP) DiskUsage(ctx context.Context) (DiskUsage, error) {
	var out DiskUsage
	err := h.get(ctx, "/system/df", &out)
	return out, err
}

// maxEvent bounds one event line.
const maxEvent = 64 << 10

// Events streams events.
func (h *HTTP) Events(ctx context.Context, fn func(Event)) error {
	resp, err := h.open(ctx, "/events")
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 4096), maxEvent)
	for sc.Scan() {
		var ev Event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			return fmt.Errorf("bad event: %w", err)
		}
		fn(ev)
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return sc.Err()
}

func (h *HTTP) open(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.base+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cannot connect to the engine: %w", err)
	}
	if resp.StatusCode < 400 {
		return resp, nil
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	var e api.ErrorResponse
	if json.Unmarshal(b, &e) != nil || e.Message == "" {
		if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusMethodNotAllowed {
			return nil, fmt.Errorf("GET %s: %w", path, ErrUnsupported)
		}
		e.Message = strings.TrimSpace(string(b))
	}
	return nil, &api.ErrDaemon{Status: resp.StatusCode, Message: e.Message}
}

func (h *HTTP) get(ctx context.Context, path string, out any) error {
	resp, err := h.open(ctx, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(out)
}
