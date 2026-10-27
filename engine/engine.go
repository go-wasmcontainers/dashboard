// Package engine is what the dashboard needs from a container engine: the
// calls of an API compatible with the Docker Engine API, as the engine of
// go-wasmcontainers serves them. The viewmodels depend on the Engine
// interface only, so they test against a fake as well as against a real
// engine served in-process.
//
// Endpoints a given engine does not serve (an older engine: volumes, disk
// usage, the event stream, stats, networks) answer ErrUnsupported, told apart from an
// ordinary "not found" by the router's own 404 (a plain-text body, not the
// API's JSON error). The dashboard then says what it needs instead of
// inventing data.
package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/go-wasmcontainers/engine/api"
)

// ErrUnsupported is returned for an endpoint the engine does not serve.
var ErrUnsupported = errors.New("not served by this engine")

// Volume is one entry of GET /volumes.
type Volume = api.Volume

// DiskUsage is GET /system/df, with the engine's Storage addition.
type DiskUsage = api.SystemDataUsage

// Event is one message of GET /events.
type Event = api.EventMessage

// Network is one entry of GET /networks.
type Network = api.Network

// Stats is one reading of GET /containers/{id}/stats.
type Stats = api.ContainerStats

// Engine is the API the dashboard drives.
type Engine interface {
	Version(ctx context.Context) (api.SystemVersion, error)
	Containers(ctx context.Context, all bool) ([]api.ContainerSummary, error)
	Images(ctx context.Context) ([]api.ImageSummary, error)
	InspectContainer(ctx context.Context, id string) (json.RawMessage, error)
	InspectImage(ctx context.Context, ref string) (json.RawMessage, error)
	Start(ctx context.Context, id string) error
	Kill(ctx context.Context, id string) error
	// Stop stops a running container and returns once it has exited.
	Stop(ctx context.Context, id string) error
	// Restart stops the container if it runs, then starts it.
	Restart(ctx context.Context, id string) error
	Remove(ctx context.Context, id string, force bool) error
	Create(ctx context.Context, name string, req api.ContainerCreateRequest) (api.ContainerCreateResponse, error)
	Pull(ctx context.Context, ref string, progress func(api.JSONMessage)) error
	RemoveImage(ctx context.Context, ref string, force bool) ([]api.ImageDeleteResponseItem, error)
	// Logs returns the multiplexed log stream (see api.Demux).
	Logs(ctx context.Context, id string, follow bool) (io.ReadCloser, error)
	Volumes(ctx context.Context) ([]Volume, error)
	VolumeCreate(ctx context.Context, o api.VolumeCreateOptions) (Volume, error)
	VolumeInspect(ctx context.Context, name string) (json.RawMessage, error)
	VolumeRemove(ctx context.Context, name string, force bool) error
	VolumesPrune(ctx context.Context, all bool) (api.VolumePruneResponse, error)
	DiskUsage(ctx context.Context) (DiskUsage, error)
	Networks(ctx context.Context) ([]Network, error)
	NetworkCreate(ctx context.Context, o api.NetworkCreateRequest) (api.NetworkCreateResponse, error)
	NetworkInspect(ctx context.Context, ref string) (json.RawMessage, error)
	NetworkRemove(ctx context.Context, ref string) error
	// Events calls fn for each event since the given Unix time (0: from
	// now) until ctx ends or the stream does (the engine cuts off a slow
	// reader: reconnect with since and resynchronise).
	Events(ctx context.Context, since int64, fn func(Event)) error
	// Stats calls fn with one reading, or one a second while stream holds,
	// until ctx ends or the stream does.
	Stats(ctx context.Context, id string, stream bool, fn func(Stats)) error
}

// HTTP is the Engine over an HTTP client: a unix socket, or the bridge the
// browser uses.
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

// InspectImage inspects an image.
func (h *HTTP) InspectImage(ctx context.Context, ref string) (json.RawMessage, error) {
	return h.Client.ImageInspect(ctx, ref)
}

// Volumes lists volumes.
func (h *HTTP) Volumes(ctx context.Context) ([]Volume, error) {
	var out api.VolumeListResponse
	err := h.get(ctx, "/volumes", nil, &out)
	return out.Volumes, err
}

// Networks lists networks (ErrUnsupported from an engine without them).
func (h *HTTP) Networks(ctx context.Context) ([]Network, error) {
	var out []Network
	err := h.get(ctx, "/networks", nil, &out)
	return out, err
}

// DiskUsage reports storage use.
func (h *HTTP) DiskUsage(ctx context.Context) (DiskUsage, error) {
	var out DiskUsage
	err := h.get(ctx, "/system/df", nil, &out)
	return out, err
}

// Bounds on what a stream line may hold.
const (
	maxEvent = 64 << 10
	maxStats = 64 << 10
)

// Events streams events.
func (h *HTTP) Events(ctx context.Context, since int64, fn func(Event)) error {
	q := url.Values{}
	if since > 0 {
		q.Set("since", strconv.FormatInt(since, 10))
	}
	return h.stream(ctx, "/events", q, maxEvent, func(b []byte) error {
		var ev Event
		if err := json.Unmarshal(b, &ev); err != nil {
			return fmt.Errorf("bad event: %w", err)
		}
		fn(ev)
		return nil
	})
}

// Stats reads a container's stats.
func (h *HTTP) Stats(ctx context.Context, id string, stream bool, fn func(Stats)) error {
	q := url.Values{"stream": {"0"}}
	if stream {
		q.Set("stream", "1")
	}
	return h.stream(ctx, "/containers/"+url.PathEscape(id)+"/stats", q, maxStats, func(b []byte) error {
		var s Stats
		if err := json.Unmarshal(b, &s); err != nil {
			return fmt.Errorf("bad stats: %w", err)
		}
		fn(s)
		return nil
	})
}

// stream reads a newline-delimited JSON response line by line.
func (h *HTTP) stream(ctx context.Context, path string, q url.Values, max int, line func([]byte) error) error {
	resp, err := h.open(ctx, path, q)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 4096), max)
	for sc.Scan() {
		if len(strings.TrimSpace(sc.Text())) == 0 {
			continue
		}
		if err := line(sc.Bytes()); err != nil {
			return err
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return sc.Err()
}

func (h *HTTP) open(ctx context.Context, path string, q url.Values) (*http.Response, error) {
	u := h.base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
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

func (h *HTTP) get(ctx context.Context, path string, q url.Values, out any) error {
	resp, err := h.open(ctx, path, q)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(io.LimitReader(resp.Body, 64<<20)).Decode(out)
}
