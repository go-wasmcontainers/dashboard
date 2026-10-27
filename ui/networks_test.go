package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/go-widgets/webcanvas"

	"github.com/go-wasmcontainers/dashboard/vm"
)

// uiHost is a page that can join a class, recording what it was asked.
type uiHost struct {
	relay string
	used  []string
	joins []string
	left  int
}

func (h *uiHost) Relay() string { return h.relay }
func (h *uiHost) UseRelay(o string) error {
	h.used = append(h.used, o)
	return nil
}
func (h *uiHost) Join(code, name string) error {
	h.joins = append(h.joins, code+"|"+name)
	return nil
}
func (h *uiHost) Leave() error { h.left++; return nil }

func TestNetworksView(t *testing.T) {
	r := newRig(t)
	r.until("engine", func(p Probe) bool { return strings.HasPrefix(p.Status[0], "engine test") })
	r.s.Modifiers(webcanvas.Modifiers{Alt: true})
	r.s.KeyDown("4")
	r.s.Modifiers(webcanvas.Modifiers{})
	p := r.until("networks", func(p Probe) bool { return p.View == "Networks" && len(p.Networks) > 0 })
	if _, ok := p.Boxes["class.button"]; ok {
		t.Error("Join class offered on a page that cannot join")
	}
	r.s.KeyDown("F6") // the navigation
	r.s.KeyDown("F6") // the view's main widget
	if r.s.focus != r.s.nTable {
		t.Error("F6 did not reach the networks table")
	}

	// Create: type the name, hover shows the line, click.
	r.click("networks.name")
	r.typeText("lab")
	b := r.s.Probe().Boxes["networks.create"]
	r.s.Move(b.X+3, b.Y+3)
	if h := r.s.Probe().Hint; h != "Create = wctr network create lab" {
		t.Errorf("hover = %q", h)
	}
	r.click("networks.create")
	r.until("lab listed", func(p Probe) bool {
		return slices.Contains(p.Networks, "lab") && p.LastCommand == "wctr network create lab"
	})

	// Select it: inspect shows it, the line is `wctr network inspect lab`.
	r.clickRow(r.s.nTable, slices.Index(r.s.Probe().Networks, "lab"))
	r.until("inspect", func(p Probe) bool {
		return p.LastCommand == "wctr network inspect lab" && r.app.Networks.Inspect.Len() > 0
	})
	b = r.s.Probe().Boxes["networks.remove"]
	r.s.Move(b.X+3, b.Y+3)
	if h := r.s.Probe().Hint; h != "Remove = wctr network rm lab" {
		t.Errorf("hover = %q", h)
	}

	// A container on lab: the network lists it, its detail lists the network.
	r.s.Modifiers(webcanvas.Modifiers{Alt: true})
	r.s.KeyDown("5")
	r.s.Modifiers(webcanvas.Modifiers{})
	r.click("terminal")
	r.typeText("wctr run -d --name web --network lab clock -interval 20ms")
	r.s.KeyDown("Enter")
	r.until("web runs", func(p Probe) bool { return len(p.Containers) == 1 && !p.Running })
	r.s.KeyDown("F6")
	r.app.View.Set(vm.ViewContainers)
	r.clickRow(r.s.cTable, 0)
	r.until("container networks", func(p Probe) bool {
		return len(p.ContainerNetworks) == 1 && strings.HasPrefix(p.ContainerNetworks[0], "lab")
	})
	r.app.View.Set(vm.ViewNetworks)
	r.until("attached", func(p Probe) bool {
		row, ok := r.app.Networks.Find("lab")
		return ok && row.Containers == 1 && strings.HasPrefix(row.Attached, "web ")
	})
	r.frame()
}

func TestJoinClassDialog(t *testing.T) {
	r := newRig(t)
	r.until("engine", func(p Probe) bool { return strings.HasPrefix(p.Status[0], "engine test") })
	host := &uiHost{}
	r.app.Class.SetHost(host)
	r.app.View.Set(vm.ViewNetworks)
	p := r.until("class offered", func(p Probe) bool { return p.Boxes["class.button"].W > 0 })
	if p.Class != "Not in a class." {
		t.Errorf("class line = %q", p.Class)
	}

	// Step 1: the relay. The dialog opens on the relay field.
	r.click("class.button")
	p = r.s.Probe()
	if !p.ClassOpen || p.Focus != "class.relay" || p.Boxes["class.code"].W != 0 {
		t.Fatalf("step 1: %+v", p)
	}
	r.typeText("http://relay.example")
	r.click("class.ok")
	if p := r.s.Probe(); !strings.Contains(p.ClassError, "https://") || len(host.used) != 0 {
		t.Errorf("http relay: %q %v", p.ClassError, host.used)
	}
	r.app.Class.RelayInput.Set("")
	r.click("class.relay")
	r.typeText("https://relay.example")
	r.s.KeyDown("Tab") // the OK button
	r.s.KeyDown("Enter")
	r.click("class.ok")
	if len(host.used) == 0 || host.used[len(host.used)-1] != "https://relay.example" {
		t.Fatalf("relay not used: %v", host.used)
	}

	// After the page restarts the engine with it: step 2.
	host.relay = "https://relay.example"
	r.app.Class.SetHost(host)
	r.frame()
	p = r.s.Probe()
	if p.Boxes["class.code"].W == 0 || p.Boxes["class.relay"].W != 0 {
		t.Fatalf("step 2: %+v", p.Boxes)
	}
	r.click("class.name")
	r.typeText("ada")
	r.click("class.code")
	r.typeText("SECRET-CODE")
	if p := r.s.Probe(); p.CodeLength != 11 {
		t.Errorf("code length = %d", p.CodeLength)
	}
	// The code field shows bullets, to assistive technology too.
	if v := r.s.jCode.A11y().Value; v != strings.Repeat("•", 11) {
		t.Errorf("code a11y value = %q", v)
	}
	r.frame()
	r.s.KeyDown("Enter") // submit from the code field
	if len(host.joins) != 1 || host.joins[0] != "SECRET-CODE|ada" {
		t.Fatalf("joins = %v", host.joins)
	}
	p = r.s.Probe()
	if p.CodeLength != 0 {
		t.Error("the code stayed in its field")
	}
	if strings.Contains(strings.Join([]string{p.LastCommand, p.Hint, p.Error, p.ClassError, p.Class, p.Terminal}, "\n"), "SECRET") {
		t.Error("the code reached the screen")
	}

	// The engine reports: joined. The dialog closes, the status says so.
	r.app.Class.Report(vm.ClassStatus{State: "joined", Relay: "https://relay.example", Label: "ada", Domain: "class.lan", Network: "class", Subnet: "10.42.2.0/24"})
	p = r.until("joined", func(p Probe) bool { return !p.ClassOpen && strings.HasPrefix(p.Class, "class: you are ada.class.lan") })
	if r.s.classBtn.Label().Get() != "Leave class" {
		t.Errorf("button = %q", r.s.classBtn.Label().Get())
	}
	r.click("class.button")
	if host.left != 1 {
		t.Error("Leave class did not leave")
	}
	r.app.Class.Report(vm.ClassStatus{State: "left"})
	r.until("left", func(p Probe) bool { return p.Class == "Not in a class." })

	// Reopen, change relay, cancel.
	r.click("class.button")
	if p := r.s.Probe(); p.Focus != "class.relay" {
		t.Errorf("reopened on %q", p.Focus)
	}
	host.relay = "https://relay.example"
	r.app.Class.SetHost(host)
	r.frame()
	r.click("class.change")
	if host.used[len(host.used)-1] != "" {
		t.Errorf("change relay: %v", host.used)
	}
	r.click("class.code")
	r.click("class.cancel")
	if p := r.s.Probe(); p.ClassOpen || p.Focus != "class.button" {
		t.Errorf("cancel: open=%v focus=%q", p.ClassOpen, p.Focus)
	}
	r.app.Class.SetHost(nil)
	r.frame()
	if _, ok := r.s.Probe().Boxes["class.button"]; ok {
		t.Error("a host that went away still offers the dialog")
	}
}
