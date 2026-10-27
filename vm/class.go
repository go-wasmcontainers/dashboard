package vm

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/go-widgets/mvvm"
)

// ClassStatus is what the engine reports about the class (the engine
// worker's {t:"class"} message).
type ClassStatus struct {
	// State is "joined", "left", "error", or "" before any report.
	State                                 string
	Relay, Label, Domain, Network, Subnet string
	Error                                 string
}

// ClassHost is the page around the dashboard, which owns the engine
// worker: the relay it was started with, restarting it with another, and
// the join and leave messages. Status reports come back through
// App.Class.Report. A host that cannot join a class is nil.
type ClassHost interface {
	// Relay is the relay origin the engine was started with ("" none).
	Relay() string
	// UseRelay remembers origin (not a secret) and restarts the engine
	// with it ("" forgets it); the page reloads.
	UseRelay(origin string) error
	// Join sends the join code, once, with the name asked for.
	Join(code, name string) error
	// Leave leaves the class.
	Leave() error
}

// Class is the "Join class" dialog and status. The join code is a secret:
// it lives in Code only until Join, is never part of a command line, a
// message, a log or the test hook, and is cleared whether or not the join
// succeeds.
type Class struct {
	app  *App
	host ClassHost

	// Available says whether this page can join a class at all.
	Available *mvvm.Observable[bool]
	// Open is the dialog's visibility.
	Open *mvvm.Observable[bool]
	// RelayInput, Name and Code are the dialog's fields.
	RelayInput, Name, Code *mvvm.Observable[string]
	// Relay is the relay the engine runs with ("" none: the dialog asks
	// for one first).
	Relay *mvvm.Observable[string]
	// Status is the last report; Busy while a join or leave is under way.
	Status *mvvm.Observable[ClassStatus]
	Busy   *mvvm.Observable[bool]
	// Error is the dialog's message.
	Error *mvvm.Observable[string]
}

func newClass(a *App) *Class {
	return &Class{app: a, Available: mvvm.NewObservable(false), Open: mvvm.NewObservable(false),
		RelayInput: mvvm.NewObservable(""), Name: mvvm.NewObservable(""), Code: mvvm.NewObservable(""),
		Relay: mvvm.NewObservable(""), Status: mvvm.NewObservable(ClassStatus{}),
		Busy: mvvm.NewObservable(false), Error: mvvm.NewObservable("")}
}

// SetHost connects the page; nil leaves the feature unavailable.
func (c *Class) SetHost(h ClassHost) {
	c.host = h
	c.Available.Set(h != nil)
	if h != nil {
		r, _ := RelayOrigin(h.Relay())
		c.Relay.Set(r)
	}
}

// relayHost is the host part the page accepts (web/app.js RELAY_RE): a
// DNS-shaped name or a bracketed IPv6 literal.
var relayHost = regexp.MustCompile(`^([a-z0-9.-]{1,253}|\[[0-9a-f:]{2,39}\])$`)

// RelayOrigin turns what the user typed into the relay's origin,
// https://host[:port]. A wss:// address names the same relay (its data
// channel) and maps to https://. Anything else is refused: another scheme,
// a user or password, a path, a query, a fragment, a host a name cannot
// be, a port out of range — the origin goes into the engine worker's URL
// and its CSP, so it must be exactly an origin.
func RelayOrigin(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", errors.New("enter the relay's address, https://host[:port]")
	}
	if len(s) > 255 {
		return "", errors.New("the relay address is too long")
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", errors.New("the relay address is not a URL")
	}
	switch {
	case u.Scheme != "https" && u.Scheme != "wss":
		return "", errors.New("the relay address must start with https:// (or wss://)")
	case u.User != nil:
		return "", errors.New("the relay address must not carry a user or password")
	case u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" || (u.Path != "" && u.Path != "/"):
		return "", errors.New("the relay address is just https://host[:port]")
	}
	host := strings.ToLower(u.Hostname())
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if !relayHost.MatchString(host) {
		return "", errors.New("the relay host has characters a host name cannot have")
	}
	o := "https://" + host
	if p := u.Port(); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || len(p) > 5 || n < 1 || n > 65535 {
			return "", errors.New("the relay port must be 1 to 65535")
		}
		o += ":" + p
	} else if strings.HasSuffix(u.Host, ":") {
		return "", errors.New("the relay port must be 1 to 65535")
	}
	return o, nil
}

// Show opens the dialog.
func (c *Class) Show() {
	c.Error.Set("")
	c.Open.Set(true)
}

// Cancel closes the dialog and forgets the code.
func (c *Class) Cancel() {
	c.Code.Set("")
	c.Error.Set("")
	c.Open.Set(false)
}

// Continue validates the relay address and restarts the engine with it.
func (c *Class) Continue() {
	if c.host == nil {
		c.Error.Set("this page cannot join a class")
		return
	}
	o, err := RelayOrigin(c.RelayInput.Get())
	if err != nil {
		c.Error.Set(err.Error())
		return
	}
	c.RelayInput.Set(o)
	if err := c.host.UseRelay(o); err != nil {
		c.Error.Set(Clean(err.Error(), maxField))
	}
}

// ChangeRelay forgets the relay (the engine restarts without it).
func (c *Class) ChangeRelay() {
	c.Code.Set("")
	if c.host == nil {
		return
	}
	if err := c.host.UseRelay(""); err != nil {
		c.Error.Set(Clean(err.Error(), maxField))
	}
}

// maxName bounds the name asked for (the engine refuses more than 256).
const maxName = 63

// Join sends the code and the name. The code leaves Code at once.
func (c *Class) Join() {
	code := c.Code.Get()
	c.Code.Set("")
	name := strings.TrimSpace(c.Name.Get())
	switch {
	case c.host == nil:
		c.Error.Set("this page cannot join a class")
		return
	case c.Relay.Get() == "":
		c.Error.Set("enter the relay's address first")
		return
	case code == "":
		c.Error.Set("enter the join code your teacher gave")
		return
	case len(code) > 256:
		c.Error.Set("that join code is too long")
		return
	case len(name) > maxName:
		c.Error.Set("the name is at most 63 characters")
		return
	}
	c.Error.Set("")
	c.Busy.Set(true)
	c.app.LastCommand.Set("join class at " + c.Relay.Get() + nameNote(name) + "; then: " + Line("network", "ls"))
	if err := c.host.Join(code, name); err != nil {
		c.Busy.Set(false)
		c.Error.Set(Clean(err.Error(), maxField))
	}
}

func nameNote(name string) string {
	if name == "" {
		return ""
	}
	return " as " + Quote(Clean(name, maxName))
}

// Leave leaves the class.
func (c *Class) Leave() {
	if c.host == nil {
		return
	}
	c.Busy.Set(true)
	c.app.LastCommand.Set("leave class " + c.Status.Get().Relay)
	if err := c.host.Leave(); err != nil {
		c.Busy.Set(false)
		c.app.Error.Set(Clean(err.Error(), maxField))
	}
}

// Report applies a status from the engine. Every string is the engine's
// and is cleaned; an error never carries the code (the engine's rule, and
// the code is not kept here to leak).
func (c *Class) Report(st ClassStatus) {
	st = ClassStatus{State: Clean(st.State, 16), Relay: Clean(st.Relay, 255), Label: Clean(st.Label, maxName),
		Domain: Clean(st.Domain, 253), Network: Clean(st.Network, 64), Subnet: Clean(st.Subnet, 64), Error: Clean(st.Error, maxField)}
	c.Busy.Set(false)
	c.Status.Set(st)
	switch st.State {
	case "joined":
		c.Error.Set("")
		c.Open.Set(false)
	case "left":
		c.Relay.Set("")
		c.RelayInput.Set("")
	case "error":
		c.Error.Set(st.Error)
		if !c.Open.Get() {
			c.app.Error.Set("class: " + st.Error)
		}
	}
	c.app.Refresh() // the class network comes or goes
}

// Summary is the one-line status ("" when not in a class).
func (c *Class) Summary() string {
	st := c.Status.Get()
	if st.State != "joined" {
		return ""
	}
	who := st.Label
	if st.Domain != "" {
		who += "." + st.Domain
	}
	s := "class: you are " + who + " on " + st.Relay + " · network " + st.Network
	if st.Subnet != "" {
		s += " · addresses " + st.Subnet
	}
	return s + " (run --network " + st.Network + ")"
}
