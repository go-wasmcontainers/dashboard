package vm

import (
	"errors"
	"strings"
	"testing"
)

// fakeHost records what the dashboard asks of the page. It keeps the
// codes it was given so the test can check where they went.
type fakeHost struct {
	relay   string
	used    []string
	joins   []string // "code|name"
	leaves  int
	useErr  error
	joinErr error
	leavErr error
}

func (f *fakeHost) Relay() string { return f.relay }
func (f *fakeHost) UseRelay(o string) error {
	f.used = append(f.used, o)
	return f.useErr
}
func (f *fakeHost) Join(code, name string) error {
	f.joins = append(f.joins, code+"|"+name)
	return f.joinErr
}
func (f *fakeHost) Leave() error {
	f.leaves++
	return f.leavErr
}

func TestRelayOrigin(t *testing.T) {
	for in, want := range map[string]string{
		"https://relay.example":               "https://relay.example",
		" HTTPS://Relay.Example:8443/ ":       "https://relay.example:8443",
		"wss://relay.example:443":             "https://relay.example:443",
		"https://[::1]:9000":                  "https://[::1]:9000",
		"https://10.0.0.1":                    "https://10.0.0.1",
		"":                                    "enter the relay",
		"http://relay.example":                "must start with https://",
		"ws://relay.example":                  "must start with https://",
		"javascript:alert(1)":                 "must start with https://",
		"https://u:p@relay.example":           "user or password",
		"https://relay.example/path":          "just https://host",
		"https://relay.example?x=1":           "just https://host",
		"https://relay.example?":              "just https://host",
		"https://relay.example#f":             "just https://host",
		"https:relay.example":                 "just https://host",
		"https://":                            "characters a host name cannot have",
		"https://re lay.example":              "not a URL",
		"https://relay_x.example":             "characters a host name cannot have",
		"https://relay.example:0":             "port must be",
		"https://relay.example:65536":         "port must be",
		"https://relay.example:":              "port must be",
		"https://relay.example:x":             "not a URL",
		"https://relay.example\"onload=x":     "cannot have",
		"https://" + strings.Repeat("a", 260): "too long",
	} {
		got, err := RelayOrigin(in)
		if err != nil {
			got = err.Error()
		}
		if !strings.Contains(got, want) {
			t.Errorf("RelayOrigin(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestClassDialog(t *testing.T) {
	h := newHarness(t)
	c := h.app.Class
	// No host: nothing is available, and nothing is sent anywhere.
	if c.Available.Get() {
		t.Error("available without a host")
	}
	c.Continue()
	c.Code.Set("SECRET-1")
	c.Join()
	if c.Error.Get() != "this page cannot join a class" || c.Code.Get() != "" {
		t.Errorf("no host: %q %q", c.Error.Get(), c.Code.Get())
	}
	c.ChangeRelay()
	c.Leave()

	host := &fakeHost{relay: "javascript:x"}
	c.SetHost(host)
	if !c.Available.Get() || c.Relay.Get() != "" {
		t.Errorf("a bad remembered relay was kept: %q", c.Relay.Get())
	}
	c.Show()
	if !c.Open.Get() {
		t.Error("Show")
	}
	// Step 1: the relay.
	c.RelayInput.Set("http://relay.example")
	c.Continue()
	if !strings.Contains(c.Error.Get(), "https://") || len(host.used) != 0 {
		t.Errorf("http relay: %q %v", c.Error.Get(), host.used)
	}
	c.RelayInput.Set("wss://Relay.Example")
	c.Continue()
	if c.RelayInput.Get() != "https://relay.example" || len(host.used) != 1 || host.used[0] != "https://relay.example" {
		t.Errorf("continue: %q %v", c.RelayInput.Get(), host.used)
	}
	host.useErr = errors.New("storage blocked")
	c.Continue()
	if c.Error.Get() != "storage blocked" {
		t.Errorf("use error: %q", c.Error.Get())
	}
	host.useErr = nil

	// After the reload the page says which relay the engine runs with.
	host.relay = "https://relay.example"
	c.SetHost(host)
	c.Code.Set("SECRET-2")
	c.Name.Set(strings.Repeat("n", maxName+1))
	c.Join()
	if c.Error.Get() != "the name is at most 63 characters" || c.Code.Get() != "" || len(host.joins) != 0 {
		t.Errorf("long name: %q %q", c.Error.Get(), host.joins)
	}
	c.Name.Set(" ada ")
	c.Join()
	if c.Error.Get() != "enter the join code your teacher gave" || len(host.joins) != 0 {
		t.Errorf("no code: %q", c.Error.Get())
	}
	c.Code.Set(strings.Repeat("x", 257))
	c.Join()
	if c.Error.Get() != "that join code is too long" || c.Code.Get() != "" {
		t.Errorf("long code: %q", c.Error.Get())
	}
	c.Code.Set("SECRET-3")
	c.Join()
	if len(host.joins) != 1 || host.joins[0] != "SECRET-3|ada" || c.Code.Get() != "" || !c.Busy.Get() {
		t.Errorf("join: %q %q %v", host.joins, c.Code.Get(), c.Busy.Get())
	}
	if l := h.app.LastCommand.Get(); l != "join class at https://relay.example as ada; then: wctr network ls" {
		t.Errorf("join line = %q", l)
	}
	// The secret went to the host and nowhere else the dashboard shows.
	for _, s := range []string{h.app.LastCommand.Get(), h.app.Error.Get(), c.Error.Get(), c.Summary(), strings.Join(h.app.Terminal.pending, "")} {
		if strings.Contains(s, "SECRET") {
			t.Errorf("the code leaked into %q", s)
		}
	}
	// An error from the engine, dialog open: said in the dialog.
	c.Report(ClassStatus{State: "error", Error: "wrong join code\x1b[2J"})
	if c.Busy.Get() || c.Error.Get() != "wrong join code�[2J" || h.app.Error.Get() != "" {
		t.Errorf("error report: %q %q", c.Error.Get(), h.app.Error.Get())
	}
	host.joinErr = errors.New("engine not running")
	c.Code.Set("SECRET-4")
	c.Join()
	if c.Busy.Get() || c.Error.Get() != "engine not running" || c.Code.Get() != "" {
		t.Errorf("join error: %q", c.Error.Get())
	}
	c.Name.Set("")
	host.joinErr = nil
	c.Code.Set("SECRET-5")
	c.Join()
	if l := h.app.LastCommand.Get(); l != "join class at https://relay.example; then: wctr network ls" {
		t.Errorf("join line without a name = %q", l)
	}

	// Joined: the dialog closes and the summary says who and where.
	c.Report(ClassStatus{State: "joined", Relay: "https://relay.example", Label: "ada", Domain: "class.lan", Network: "class", Subnet: "10.42.2.0/24"})
	if c.Open.Get() || c.Summary() != "class: you are ada.class.lan on https://relay.example · network class · addresses 10.42.2.0/24 (run --network class)" {
		t.Errorf("joined: %v %q", c.Open.Get(), c.Summary())
	}
	c.Report(ClassStatus{State: "joined", Relay: "https://r", Label: "bob", Network: "class"})
	if c.Summary() != "class: you are bob on https://r · network class (run --network class)" {
		t.Errorf("summary = %q", c.Summary())
	}
	// An error with the dialog closed goes to the status line.
	c.Report(ClassStatus{State: "error", Error: "relay unreachable"})
	if h.app.Error.Get() != "class: relay unreachable" || c.Summary() != "" {
		t.Errorf("closed error: %q", h.app.Error.Get())
	}

	// Leave.
	c.Leave()
	if host.leaves != 1 || !c.Busy.Get() || !strings.HasPrefix(h.app.LastCommand.Get(), "leave class") {
		t.Errorf("leave: %d %q", host.leaves, h.app.LastCommand.Get())
	}
	c.Report(ClassStatus{State: "left"})
	if c.Relay.Get() != "" || c.Busy.Get() {
		t.Errorf("left: %q", c.Relay.Get())
	}
	host.leavErr = errors.New("no engine")
	c.Leave()
	if h.app.Error.Get() != "no engine" || c.Busy.Get() {
		t.Errorf("leave error: %q", h.app.Error.Get())
	}
	// Join with no relay; change relay; cancel.
	c.Code.Set("SECRET-6")
	c.Join()
	if c.Error.Get() != "enter the relay's address first" || c.Code.Get() != "" {
		t.Errorf("no relay: %q", c.Error.Get())
	}
	c.ChangeRelay()
	if host.used[len(host.used)-1] != "" {
		t.Errorf("change relay: %q", host.used)
	}
	host.useErr = errors.New("blocked")
	c.ChangeRelay()
	if c.Error.Get() != "blocked" {
		t.Errorf("change relay error: %q", c.Error.Get())
	}
	c.Code.Set("SECRET-7")
	c.Cancel()
	if c.Open.Get() || c.Code.Get() != "" || c.Error.Get() != "" {
		t.Error("Cancel")
	}
	h.settle()
}
