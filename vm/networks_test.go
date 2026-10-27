package vm

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/go-wasmcontainers/engine/api"

	"github.com/go-wasmcontainers/dashboard/engine"
)

func TestNetworksView(t *testing.T) {
	h := newHarness(t)
	h.refresh()
	n := h.app.Networks
	if n.Supported.Get() != VolumesUnsupported || !strings.Contains(n.Message.Get(), "engine v0.2.0 does not") || n.Rows.Len() != 0 {
		t.Errorf("unsupported: %d %q", n.Supported.Get(), n.Message.Get())
	}
	h.f.set(func() { h.f.netErr = nil })
	h.refresh()
	if n.Supported.Get() != VolumesSupported || !strings.HasPrefix(n.Message.Get(), "No networks.") {
		t.Errorf("empty: %q", n.Message.Get())
	}

	many := map[string]api.NetworkEndpoint{}
	for i := 0; i < maxAttached+2; i++ {
		many[fmt.Sprintf("c%02d", i)] = api.NetworkEndpoint{Name: fmt.Sprintf("w%02d", i), IPv4Address: fmt.Sprintf("10.0.0.%d/24", i)}
	}
	h.f.set(func() {
		h.f.networks = []engine.Network{
			{Name: "class", ID: "sha256:0123456789abcdef", Driver: "relay",
				IPAM:       api.NetworkIPAM{Config: []map[string]string{{"Subnet": "10.42.2.0/24"}, {"Gateway": "x"}}},
				Containers: map[string]api.NetworkEndpoint{"abcdef0123456789": {IPv4Address: "10.42.2.2/24"}, "c2": {Name: "db\x1b[31m", IPv4Address: "10.42.2.3/24"}}},
			{Name: "bridge", Driver: "bridge", Containers: many},
		}
	})
	h.refresh()
	if n.Rows.Len() != 2 || n.Message.Get() != "" {
		t.Fatalf("rows = %v %q", n.Rows.Slice(), n.Message.Get())
	}
	b, c := n.Rows.At(0), n.Rows.At(1)
	if c != (NetworkRow{Name: "class", ID: "0123456789ab", Driver: "relay", Subnet: "10.42.2.0/24", Containers: 2,
		Attached: "abcdef012345 10.42.2.2/24, db�[31m 10.42.2.3/24"}) {
		t.Errorf("class row = %+v", c)
	}
	if b.Containers != maxAttached+2 || !strings.HasSuffix(b.Attached, ", +2 more") {
		t.Errorf("bounded attachments = %+v", b)
	}
	if cells := c.Cells(); len(cells) != len(NetworkColumns) || cells[4] != "2: "+c.Attached {
		t.Errorf("cells = %q", cells)
	}
	if cells := (NetworkRow{Name: "x"}).Cells(); cells[4] != "0" {
		t.Errorf("empty cells = %q", cells)
	}
	if _, ok := n.Find("nope"); ok {
		t.Error("Find invented a network")
	}

	// Select: inspect follows, and is read again on a refresh while selected.
	n.Selected.Set("class")
	h.until("inspect", func() bool { return n.Inspect.Len() > 0 && n.Inspect.At(1).Value == `"class"` })
	h.f.set(func() { h.f.netInspect = `{"Name":"class","Driver":"relay","Containers":{}}` })
	h.refresh()
	h.until("re-inspect", func() bool { return n.Inspect.Len() == 4 })
	h.f.setErr("network inspect bridge", errors.New("gone"))
	n.Selected.Set("bridge")
	h.until("inspect error", func() bool { return n.Inspect.Len() == 1 && n.Inspect.At(0).Value == "gone" })
	h.f.set(func() { h.f.netInspect = "{" })
	n.Selected.Set("class")
	h.until("bad inspect", func() bool {
		return n.Inspect.Len() == 1 && n.Inspect.At(0).Key == "error" && n.Inspect.At(0).Value != "gone"
	})
	n.Selected.Set("bridge")
	n.Selected.Set("") // a late answer is dropped
	h.settle()
	if n.Inspect.Len() != 0 {
		t.Errorf("inspect after deselect = %v", n.Inspect.Slice())
	}

	// Actions, their lines, and that each line is a command wctr has.
	n.NewName.Set(" lab ")
	if n.CreateLine() != "wctr network create lab" || n.RemoveLine("lab") != "wctr network rm lab" || n.InspectLine("a b") != "wctr network inspect 'a b'" {
		t.Error("network lines")
	}
	for _, l := range []string{n.CreateLine(), n.RemoveLine("lab"), n.InspectLine("lab")} {
		args := strings.Fields(strings.TrimPrefix(l, "wctr "))
		cmd, rest, err := h.app.CLI().root.Find(args)
		if err != nil || cmd.Name() != args[1] || len(rest) != 1 {
			t.Errorf("%q is not a wctr command: %v %v", l, cmd.Name(), err)
		}
	}
	n.Create()
	h.until("created", func() bool { return h.f.called("network create lab") && n.NewName.Get() == "" })
	n.Create() // no name: refused here, nothing sent
	if h.app.Error.Get() != "a network needs a name" {
		t.Errorf("empty create: %q", h.app.Error.Get())
	}
	h.f.setErr("network create x", errors.New("taken"))
	n.NewName.Set("x")
	n.Create()
	h.until("create error", func() bool { return h.app.Error.Get() == "taken" })
	if n.NewName.Get() != "x" {
		t.Error("a failed create cleared the name")
	}
	n.Remove("lab")
	h.until("removed", func() bool { return h.f.called("network rm lab") && h.app.LastCommand.Get() == "wctr network rm lab" })
	h.settle()

	// The selection goes when the network does; an error keeps the rows.
	n.Selected.Set("class")
	h.f.set(func() { h.f.networks = h.f.networks[1:] })
	h.refresh()
	if n.Selected.Get() != "" || n.Rows.Len() != 1 {
		t.Errorf("after removal: %q %d", n.Selected.Get(), n.Rows.Len())
	}
	h.f.set(func() { h.f.netErr = errors.New("boom") })
	h.refresh()
	if n.Message.Get() != "networks: boom" || n.Rows.Len() != 1 {
		t.Errorf("error: %q", n.Message.Get())
	}
	h.settle()
}

func TestContainerNetworks(t *testing.T) {
	var ins api.ContainerInspect
	if got := containerNetworks(ins); len(got) != 0 {
		t.Errorf("none = %q", got)
	}
	ins.HostConfig.NetworkMode = "class"
	if got := containerNetworks(ins); len(got) != 1 || got[0] != "class (no address: not running)" {
		t.Errorf("asked for = %q", got)
	}
	names := []string{"web", "web.class\x07"}
	for i := 0; i < maxAttached; i++ {
		names = append(names, fmt.Sprint("n", i))
	}
	ins.NetworkSettings.Networks = map[string]api.EndpointSettings{
		"class":  {IPAddress: "10.42.2.2", DNSNames: names},
		"bridge": {},
	}
	got := containerNetworks(ins)
	if len(got) != 2 || got[0] != "bridge" || !strings.HasPrefix(got[1], "class 10.42.2.2 (web, web.class�, n0") ||
		strings.Count(got[1], ",") != maxAttached-1 {
		t.Errorf("networks = %q", got)
	}
}

func TestDetailNetworks(t *testing.T) {
	h := newHarness(t)
	h.f.set(func() {
		h.f.containers = []api.ContainerSummary{{ID: "c1", Names: []string{"/web"}, State: "running"}}
		h.f.inspect = `{"Id":"c1","State":{"Status":"running","Running":true},"NetworkSettings":{"Networks":{"class":{"IPAddress":"10.42.2.2","DNSNames":["web"]}}}}`
	})
	h.refresh()
	d := h.app.Detail
	d.Open("c1")
	h.until("networks", func() bool { return d.Networks.Len() == 1 && d.Networks.At(0) == "class 10.42.2.2 (web)" })
	d.Close()
	if d.Networks.Len() != 0 {
		t.Error("Close kept the networks")
	}
	h.settle()
}
