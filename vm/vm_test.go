package vm

import (
	"context"
	"errors"
	"io"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/go-wasmcontainers/engine/api"
	"github.com/go-widgets/mvvm"
	"github.com/spf13/cobra"

	"github.com/go-wasmcontainers/dashboard/engine"
)

func TestClean(t *testing.T) {
	for _, c := range []struct {
		in   string
		max  int
		want string
	}{
		{"web", 10, "web"},
		{"a\x1b[31mb", 10, "a�[31mb"},
		{"a\tb\nc", 10, "a b c"},
		{"x‮y‍", 10, "xy"},
		{"a\u009bb", 10, "a�b"},
		{"abcdef", 3, "abc…"},
	} {
		if got := Clean(c.in, c.max); got != c.want {
			t.Errorf("Clean(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTextHelpers(t *testing.T) {
	if ShortID("sha256:0123456789abcdef") != "0123456789ab" || ShortID("abc") != "abc" {
		t.Error("ShortID")
	}
	now := time.Unix(1000, 0)
	if Ago(now, time.Time{}) != "" || Ago(now, now.Add(-90*time.Second)) != "About a minute ago" {
		t.Errorf("Ago = %q", Ago(now, now.Add(-90*time.Second)))
	}
	if Size(1500) != "1.5kB" {
		t.Errorf("Size = %q", Size(1500))
	}
	for in, want := range map[string]string{"": "''", "web": "web", "K=v": "K=v", "a b": "'a b'", "it's": `'it'\''s'`} {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %q", in, got)
		}
	}
	if Line("rm", "-f", "my app") != "wctr rm -f 'my app'" {
		t.Error("Line")
	}
	var nilCLI *CLI
	if nilCLI.Has("ps") || NewCLI(nil).Has("ps") {
		t.Error("a nil CLI has commands")
	}
	c := NewCLI(&cobra.Command{Use: "wctr"})
	c.root.AddCommand(&cobra.Command{Use: "ps", Run: func(*cobra.Command, []string) {}})
	if !c.Has("ps") || c.Has("start") {
		t.Error("Has")
	}
	if got := c.Equivalent("POST /x", "start", "web"); got != "POST /x  (no `wctr start` yet)" {
		t.Errorf("Equivalent = %q", got)
	}
	if got := c.Equivalent("GET /x", "ps", "-a"); got != "wctr ps -a" {
		t.Errorf("Equivalent = %q", got)
	}
}

func TestSync(t *testing.T) {
	type row struct{ K, V string }
	l := mvvm.NewObservableList[row]()
	events := 0
	l.Subscribe(func(mvvm.ListEvent[row]) { events++ })
	key := func(r row) string { return r.K }
	step := func(want ...row) {
		t.Helper()
		Sync(l, want, key)
		if !slices.Equal(l.Slice(), want) {
			t.Fatalf("Sync = %v, want %v", l.Slice(), want)
		}
	}
	step(row{"a", "1"}, row{"b", "1"}, row{"c", "1"})
	events = 0
	step(row{"a", "1"}, row{"b", "1"}, row{"c", "1"})
	if events != 0 {
		t.Errorf("an unchanged sync emitted %d events", events)
	}
	step(row{"c", "2"}, row{"a", "1"}, row{"d", "1"}) // move+set, remove, insert
	step(row{"a", "1"}, row{"c", "2"})                // move unchanged
	step(row{"a", "2"}, row{"c", "2"})                // set in place
	step()
}

func cs(id, name, state, status string, created int64) api.ContainerSummary {
	return api.ContainerSummary{ID: id, Names: []string{"/" + name}, Image: "hello", Command: "/app.wasm",
		State: state, Status: status, Created: created}
}

func TestContainersView(t *testing.T) {
	h := newHarness(t)
	h.f.containers = []api.ContainerSummary{
		cs("c1", "web", "running", "Up 3 seconds", 1_700_000_000),
		cs("c2", "job", "exited", "Exited (3) 1 minute ago", 1_700_000_050),
		{ID: "c3noname0000000", State: "created", Status: "Created", Created: 1_699_999_000},
	}
	h.refresh()
	c := h.app.Containers
	if c.Rows.Len() != 3 || c.Rows.At(0).ID != "c2" {
		t.Fatalf("rows = %v", c.Rows.Slice())
	}
	if r, tot := c.Counts(); r != 1 || tot != 3 {
		t.Errorf("Counts = %d/%d", r, tot)
	}
	if !strings.Contains(h.app.Summary.Get(), "1 running · 3 containers") {
		t.Errorf("Summary = %q", h.app.Summary.Get())
	}
	if !strings.HasPrefix(h.app.EngineVersion.Get(), "engine v0.2.0 · API 1.47") {
		t.Errorf("EngineVersion = %q", h.app.EngineVersion.Get())
	}
	job, _ := c.Find("c2")
	if n, ok := job.ExitCode(); !ok || n != 3 {
		t.Errorf("ExitCode = %d %v", n, ok)
	}
	web, _ := c.Find("c1")
	if _, ok := web.ExitCode(); ok {
		t.Error("a running container has an exit code")
	}
	if _, ok := c.Find("nope"); ok {
		t.Error("Find invented a row")
	}
	anon, _ := c.Find("c3noname0000000")
	if anon.Ref() != "c3noname0000" || len(anon.Cells()) != NumContainerCols {
		t.Errorf("Ref = %q", anon.Ref())
	}
	c.ShowAll.Set(false)
	if c.Rows.Len() != 1 || c.Rows.At(0).Name != "web" {
		t.Errorf("running only = %v", c.Rows.Slice())
	}
	c.ShowAll.Set(true)
	c.Search.Set("JOB")
	if c.Rows.Len() != 1 {
		t.Errorf("search = %v", c.Rows.Slice())
	}
	c.Search.Set("")
	c.SortCol.Set(ColName)
	c.SortAsc.Set(true)
	if got := []string{c.Rows.At(0).Name, c.Rows.At(1).Name, c.Rows.At(2).Name}; !slices.Equal(got, []string{"", "job", "web"}) {
		t.Errorf("by name = %v", got)
	}
	// Equal keys fall back to the ID.
	c.SortCol.Set(ColImage)
	if c.Rows.At(1).ID != "c1" {
		t.Errorf("tie order = %v", c.Rows.At(1).ID)
	}
	c.Selected.Set("c2")
	h.f.set(func() { h.f.containers = h.f.containers[:1] })
	h.refresh()
	if c.Selected.Get() != "" {
		t.Error("the selection outlived its container")
	}

	// Lines and actions.
	if c.StartLine(job) != "wctr start job" || c.StopLine(web) != "wctr stop web" || c.RestartLine(web) != "wctr restart web" ||
		c.RemoveLine(web) != "wctr rm -f web" || c.RemoveLine(job) != "wctr rm job" {
		t.Errorf("lines: %q %q %q", c.StartLine(job), c.StopLine(web), c.RestartLine(web))
	}
	for _, act := range []struct {
		do   func()
		call string
	}{
		{func() { c.Start(job) }, "start c2"},
		{func() { c.Stop(web) }, "stop c1"},
		{func() { c.Remove(web) }, "rm -f c1"},
		{func() { c.Remove(job) }, "rm c2"},
		{func() { c.Restart(web) }, "restart c1"},
	} {
		act.do()
		h.until(act.call, func() bool { return h.f.called(act.call) })
	}
	h.settle()
	if h.app.LastCommand.Get() != c.RestartLine(web) || h.app.Error.Get() != "" {
		t.Errorf("LastCommand = %q, Error = %q", h.app.LastCommand.Get(), h.app.Error.Get())
	}
	h.f.setErr("restart c1", errors.New("restart failed"))
	c.Restart(web)
	h.until("restart error", func() bool { return h.app.Error.Get() == "restart failed" })
	h.settle()
}

func TestStorageLine(t *testing.T) {
	for _, c := range []struct {
		st   *api.StorageUsage
		want string
	}{
		{nil, "storage 1kB"},
		{&api.StorageUsage{Kind: "IndexedDB", Usage: 2000, Quota: 10000, Persistent: true}, "storage 1kB · IndexedDB 2kB of 10kB (persistent)"},
		{&api.StorageUsage{Kind: "directory /var/lib/wctr", Usage: 3000, Quota: -1}, "storage 1kB · directory /var/lib/wctr 3kB"},
		{&api.StorageUsage{Kind: "memory", Usage: -1, Quota: -1, Error: "quota\x1b exceeded"}, "storage 1kB · memory · error: quota� exceeded"},
	} {
		if got := storageLine(1000, c.st); got != c.want {
			t.Errorf("storageLine = %q, want %q", got, c.want)
		}
	}
}

func TestContainersBounded(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < MaxContainers+5; i++ {
		h.f.containers = append(h.f.containers, cs(string(rune('a'+i%26))+strings.Repeat("x", i/26)+"-"+string(rune('0'+i%10)), "n", "exited", "Exited (0)", int64(i)))
	}
	h.refresh()
	if _, n := h.app.Containers.Counts(); n != MaxContainers {
		t.Errorf("kept %d containers", n)
	}
}

func TestRefreshErrorsAndStorage(t *testing.T) {
	h := newHarness(t)
	h.f.images = []api.ImageSummary{{ID: "sha256:aaaa", RepoTags: []string{"hello:latest"}, Size: 2000}}
	h.refresh()
	if h.app.Storage.Get() != "images 2kB (disk usage requires GET /system/df)" {
		t.Errorf("Storage = %q", h.app.Storage.Get())
	}
	h.f.df = &engine.DiskUsage{LayersSize: 3000, Volumes: []engine.Volume{{Name: "v"}}}
	h.f.df.Volumes[0].UsageData = &struct {
		Size     int64 `json:"Size"`
		RefCount int64 `json:"RefCount"`
	}{Size: 1000}
	h.refresh()
	if h.app.Storage.Get() != "storage 4kB" {
		t.Errorf("Storage = %q", h.app.Storage.Get())
	}
	for _, call := range []string{"version", "containers", "images"} {
		h.f.setErr(call, errors.New(call+" down"))
		h.refresh()
		if h.app.Error.Get() != call+" down" || h.app.EngineVersion.Get() != "engine: unreachable" {
			t.Errorf("%s: Error = %q", call, h.app.Error.Get())
		}
		h.f.setErr(call, nil)
	}
	// Refreshes asked for while one runs coalesce.
	n := h.app.Refreshed.Get()
	gate := make(chan struct{})
	h.f.set(func() { h.f.gate = gate })
	h.app.Refresh()
	h.app.Refresh()
	h.app.Refresh()
	if !h.app.Refreshing() {
		t.Error("not refreshing")
	}
	h.f.set(func() { h.f.gate = nil })
	close(gate)
	h.until("coalesced refreshes", func() bool { return h.app.Refreshed.Get() == n+2 && !h.app.Refreshing() })
	h.settle()
	if h.app.CLI() == nil {
		t.Error("CLI")
	}
}

func TestImagesView(t *testing.T) {
	h := newHarness(t)
	h.f.images = []api.ImageSummary{
		{ID: "sha256:bbbb", RepoTags: []string{"zeta:1", "alpha:latest"}, RepoDigests: []string{"alpha@sha256:cafe"}, Size: 10, Created: 1_700_000_000},
		{ID: "sha256:cccc", Size: 5},
	}
	h.refresh()
	im := h.app.Images
	if im.Rows.Len() != 3 || im.Rows.At(0).Ref != "<none>:<none>" || im.Rows.At(1).Ref != "alpha:latest" {
		t.Fatalf("rows = %v", im.Rows.Slice())
	}
	alpha := im.Rows.At(1)
	if alpha.Digest != "cafe" || len(alpha.Cells()) != NumImageCols {
		t.Errorf("alpha = %+v", alpha)
	}
	im.Search.Set("zeta")
	if im.Rows.Len() != 1 {
		t.Errorf("search = %v", im.Rows.Slice())
	}
	im.Search.Set("")
	im.Selected.Set(alpha.Key())
	if r, ok := im.Find(alpha.Key()); !ok || r != alpha {
		t.Error("Find")
	}
	if im.RemoveLine(alpha, true) != "wctr rmi -f alpha:latest" || im.RemoveLine(alpha, false) != "wctr rmi alpha:latest" {
		t.Error("RemoveLine")
	}
	im.Remove(alpha, false)
	h.until("rmi", func() bool { return h.f.called("rmi alpha:latest") })
	im.Remove(im.Rows.At(0), true)
	h.until("rmi by id", func() bool { return h.f.called("rmi -f sha256:cccc") })
	h.settle()

	// Pull, with progress and its bound.
	im.Pull() // nothing to pull
	im.PullRef.Set(" clock ")
	if im.PullLine() != "wctr pull clock" {
		t.Error("PullLine")
	}
	h.f.set(func() {
		for i := 0; i < maxPullLog+3; i++ {
			h.f.pullMsgs = append(h.f.pullMsgs, api.JSONMessage{ID: "layer", Status: "Downloading"})
		}
	})
	im.Pull()
	im.Pull() // already pulling: ignored
	h.until("pull done", func() bool { return !im.Pulling.Get() })
	if im.PullLog.Len() != maxPullLog || im.PullLog.At(maxPullLog-1) != "done" {
		t.Errorf("pull log = %d lines, last %q", im.PullLog.Len(), im.PullLog.At(im.PullLog.Len()-1))
	}
	h.f.setErr("pull clock", errors.New("registry: 404"))
	im.Pull()
	h.until("pull failure", func() bool { return !im.Pulling.Get() })
	if last := im.PullLog.At(im.PullLog.Len() - 1); last != "error: registry: 404" {
		t.Errorf("last = %q", last)
	}
	h.settle()

	// More tags than the bound.
	many := api.ImageSummary{ID: "sha256:dddd"}
	for i := 0; i < MaxImages+1; i++ {
		many.RepoTags = append(many.RepoTags, "t:"+strings.Repeat("x", i%50)+string(rune('a'+i%26))+string(rune('a'+i/26%26))+string(rune('a'+i/676)))
	}
	h.f.set(func() { h.f.images = []api.ImageSummary{many, many} })
	h.refresh()
	if im.Rows.Len() > MaxImages {
		t.Errorf("kept %d images", im.Rows.Len())
	}
}

func TestRunForm(t *testing.T) {
	h := newHarness(t)
	f := h.app.Images.Run
	if f.Error.Get() != "choose an image" {
		t.Errorf("empty form error = %q", f.Error.Get())
	}
	f.Reset("hello")
	f.Name.Set("greeter")
	f.Env.Set("GREETING=hi there\nB=2, ")
	f.Command.Set(`friend "dear one"`)
	f.AutoRemove.Set(true)
	f.Entrypoint.Set("/app.wasm")
	f.Workdir.Set("/tmp")
	f.Memory.Set("64m")
	want := "wctr run -d --name greeter -e 'GREETING=hi there' -e B=2 --rm --entrypoint /app.wasm -w /tmp -m 64m hello friend 'dear one'"
	if f.Line.Get() != want || f.Error.Get() != "" {
		t.Fatalf("Line = %q, Error = %q", f.Line.Get(), f.Error.Get())
	}
	name, req, _, err := f.Request()
	if err != nil || name != "greeter" || req.HostConfig.Memory != 64<<20 || !req.HostConfig.AutoRemove ||
		!slices.Equal(req.Cmd, []string{"friend", "dear one"}) || req.WorkingDir != "/tmp" || len(req.Env) != 2 {
		t.Errorf("Request = %q %+v %v", name, req, err)
	}
	for _, bad := range []struct {
		field      *mvvm.Observable[string]
		value, err string
	}{
		{f.Env, "NOVALUE", `environment "NOVALUE": write KEY=value`},
		{f.Memory, "lots", `memory "lots"`},
		{f.Command, `"open`, "command: unterminated quote"},
	} {
		old := bad.field.Get()
		bad.field.Set(bad.value)
		if !strings.HasPrefix(f.Error.Get(), bad.err) {
			t.Errorf("%q: Error = %q", bad.value, f.Error.Get())
		}
		if f.Start() {
			t.Error("an invalid form started")
		}
		bad.field.Set(old)
	}
	if !f.Start() {
		t.Fatal("Start refused a valid form")
	}
	h.until("started", func() bool { return f.Started.Get() == "new1" })
	if !h.f.called("create greeter hello friend,dear one") || !h.f.called("start new1") {
		t.Errorf("calls = %v", h.f.calls)
	}
	h.settle()
	// An image the engine lacks is pulled first, as wctr run does.
	h.f.set(func() { h.f.noImage = true })
	f.Started.Set("")
	f.Start()
	h.until("pulled and started", func() bool { return f.Started.Get() == "new1" })
	if !h.f.called("pull hello") {
		t.Error("no pull")
	}
	h.settle()
	for _, e := range []string{"pull hello", "start new1", "create greeter hello friend,dear one"} {
		h.f.set(func() { h.f.noImage = e == "pull hello" })
		h.f.setErr(e, errors.New(e+" failed"))
		f.Start()
		h.until(e, func() bool { return h.app.Error.Get() == e+" failed" })
		h.settle()
		h.f.setErr(e, nil)
	}
}

func TestVolumesView(t *testing.T) {
	h := newHarness(t)
	h.refresh()
	v := h.app.Volumes
	if v.Supported.Get() != VolumesUnsupported || !strings.Contains(v.Message.Get(), "engine v0.2.0 does not yet") || v.Rows.Len() != 0 {
		t.Errorf("unsupported: %d %q", v.Supported.Get(), v.Message.Get())
	}
	h.f.volErr = nil
	h.refresh()
	if v.Supported.Get() != VolumesSupported || v.Message.Get() != "No volumes." {
		t.Errorf("empty: %q", v.Message.Get())
	}
	h.f.volumes = []engine.Volume{{Name: "data", Driver: "local"}, {Name: "cache"}}
	h.f.volumes[0].UsageData = &struct {
		Size     int64 `json:"Size"`
		RefCount int64 `json:"RefCount"`
	}{Size: 2048}
	h.refresh()
	if v.Rows.Len() != 2 || v.Rows.At(0).Name != "cache" || v.Rows.At(1).Size != "2.05kB" || v.Message.Get() != "" || len(v.Rows.At(0).Cells()) != len(VolumeColumns) {
		t.Errorf("rows = %v", v.Rows.Slice())
	}
	h.f.volErr = errors.New("boom")
	h.refresh()
	if v.Message.Get() != "volumes: boom" || v.Rows.Len() != 2 {
		t.Errorf("error: %q", v.Message.Get())
	}
}

func TestInspectTree(t *testing.T) {
	nodes, err := InspectTree([]byte(`{"b":1,"a":{"x":[true,null,"s\u001b"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, n := range nodes {
		got = append(got, strings.Repeat(" ", n.Depth)+n.Key+"="+n.Value)
	}
	want := []string{"={2}", " b=1", " a={1}", "  x=[3]", "   0=true", "   1=null", `   2="s\x1b"`}
	if !slices.Equal(got, want) {
		t.Errorf("tree = %q", got)
	}
	for _, bad := range []string{"", `{"a":`, `{"a":1`, `[1,`, `[1}`, `{1:2}`, strings.Repeat("[", 40) + strings.Repeat("]", 40)} {
		if _, err := InspectTree([]byte(bad)); err == nil {
			t.Errorf("InspectTree(%q) accepted", bad)
		}
	}
	big := "[" + strings.Repeat("1,", maxInspectNodes) + "1]"
	if _, err := InspectTree([]byte(big)); err == nil {
		t.Error("an unbounded tree")
	}
}

func TestDetail(t *testing.T) {
	h := newHarness(t)
	h.f.containers = []api.ContainerSummary{cs("c1", "job", "exited", "Exited (3)", 1)}
	h.f.logs = mux(1, "hello\n") + mux(2, "oops\n")
	h.refresh()
	d := h.app.Detail
	var out strings.Builder
	resets := 0
	d.LogOut = func(b []byte) { out.Write(b) }
	d.LogReset = func() { resets++; out.Reset() }
	d.Follow.Set(false) // no container yet: nothing streams
	d.Open("c1")
	d.Open("c1") // already open
	h.until("logs", func() bool { return strings.Contains(out.String(), "oops") })
	if out.String() != "hello\n\x1b[31moops\n\x1b[0m" {
		t.Errorf("logs = %q", out.String())
	}
	h.until("inspect", func() bool { return d.State.Get() != "" })
	if d.State.Get() != "exited · exit code 3" || d.Title.Get() != "job" || d.LogsLine.Get() != "wctr logs job" {
		t.Errorf("state = %q title %q line %q", d.State.Get(), d.Title.Get(), d.LogsLine.Get())
	}
	if !slices.Equal(d.Env.Slice(), []string{"A=1", "B=2"}) || !slices.Equal(d.Mounts.Slice(), []string{"/v -> /data"}) || d.Tree.Len() == 0 {
		t.Errorf("env %v mounts %v", d.Env.Slice(), d.Mounts.Slice())
	}
	n := d.Tree.Len()
	d.applyInspect([]byte(h.f.inspect)) // the same document: no list event
	if d.Tree.Len() != n {
		t.Error("re-applying changed the tree")
	}
	d.Follow.Set(true)
	h.until("follow", func() bool { return d.LogsLine.Get() == "wctr logs -f job" && strings.Contains(out.String(), "oops") })
	// A refresh re-inspects; an error shows; a gone container closes.
	h.f.set(func() {
		h.f.inspect = `{"State":{"Status":"running","Running":true,"Error":"bad\u001bthing"},"Config":{}}`
	})
	h.refresh()
	h.until("re-inspect", func() bool { return strings.HasPrefix(d.State.Get(), "running") })
	if d.State.Get() != "running · bad�thing" {
		t.Errorf("state = %q", d.State.Get())
	}
	h.f.set(func() { h.f.inspect = `{"State":` })
	h.refresh()
	h.until("bad inspect", func() bool { return strings.HasPrefix(d.State.Get(), "inspect: ") })
	h.f.setErr("inspect c1", errors.New("gone"))
	h.refresh()
	h.until("inspect error", func() bool { return d.State.Get() == "inspect: gone" })
	h.f.setErr("inspect c1", nil)
	// Log errors are reported in the pane.
	h.f.set(func() { h.f.logsErr = errors.New("stream broke") })
	d.Follow.Set(false)
	h.until("log error", func() bool {
		return strings.Contains(out.String(), "[logs: malformed multiplexed stream: stream broke]")
	})
	h.f.setErr("logs c1", errors.New("no logs"))
	d.Follow.Set(true)
	h.until("logs refused", func() bool { return strings.Contains(out.String(), "[logs: no logs]") })
	h.f.set(func() { h.f.containers = nil })
	h.refresh()
	if d.ID.Get() != "" || d.Tree.Len() != 0 {
		t.Error("the pane outlived its container")
	}
	d.refresh() // nothing open
	d.Open("")
	// Without a view attached, output is dropped quietly; the bound holds.
	d.LogOut, d.LogReset = nil, nil
	h.f.set(func() { h.f.containers = []api.ContainerSummary{cs("c1", "job", "exited", "Exited (3)", 1)} })
	h.refresh()
	d.Open("c1")
	d.Open("") // closed before the inspect answers: the answer is dropped
	h.settle()
	if d.State.Get() != "" {
		t.Errorf("a late inspect showed: %q", d.State.Get())
	}
	d.Open("c1")
	h.settle()
	d.pending.Store(maxPendingLog)
	logWriter{d: d}.Write([]byte("x"))
	if d.pending.Load() != maxPendingLog {
		t.Error("over the bound, output was queued")
	}
	d.pending.Store(0)
	d.Close()
}

// recShell records the lines it runs.
type recShell struct {
	lines []string
	code  int
	clear bool
	block chan struct{}
}

func (s *recShell) Run(ctx context.Context, line string, out io.Writer) (int, bool) {
	s.lines = append(s.lines, line)
	io.WriteString(out, "ran "+line+"\n")
	if s.block != nil {
		<-ctx.Done()
		return 130, false
	}
	return s.code, s.clear
}
