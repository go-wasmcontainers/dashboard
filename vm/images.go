package vm

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	units "github.com/docker/go-units"
	"github.com/go-wasmcontainers/engine/api"
	"github.com/go-wasmcontainers/engine/cli/lineedit"
	"github.com/go-widgets/mvvm"
)

// ImageRow is one repository:tag of an image, as `wctr images` lists it.
type ImageRow struct {
	Ref, ID, Digest, Size, Created string
	SizeBytes, CreatedAt           int64
	Containers                     int64
}

// Key identifies the row (one image can carry several tags).
func (r ImageRow) Key() string { return r.Ref + "@" + r.ID }

// Image table columns.
const (
	ColRef = iota
	ColImageID
	ColDigest
	ColSize
	ColImageCreated
	NumImageCols
)

// ImageColumns are the column titles.
var ImageColumns = [NumImageCols]string{"Repository:Tag", "Image ID", "Digest", "Size", "Created"}

// Cells is the row as table cells, in ImageColumns order.
func (r ImageRow) Cells() []string {
	return []string{r.Ref, ShortID(r.ID), r.Digest, r.Size, r.Created}
}

// MaxImages bounds the rows kept.
const MaxImages = 2000

// maxPullLog bounds the pull progress lines kept.
const maxPullLog = 200

// Images is the images view, with its pull and run dialogs.
type Images struct {
	app *App
	all []ImageRow

	Rows     *mvvm.ObservableList[ImageRow]
	Search   *mvvm.Observable[string]
	Selected *mvvm.Observable[string] // row Key

	// PullRef is the pull dialog's reference; Pulling says a pull runs;
	// PullLog is its progress, one line per message.
	PullRef *mvvm.Observable[string]
	Pulling *mvvm.Observable[bool]
	PullLog *mvvm.ObservableList[string]

	Run *RunForm
}

func newImages(a *App) *Images {
	im := &Images{
		app:      a,
		Rows:     mvvm.NewObservableList[ImageRow](),
		Search:   mvvm.NewObservable(""),
		Selected: mvvm.NewObservable(""),
		PullRef:  mvvm.NewObservable(""),
		Pulling:  mvvm.NewObservable(false),
		PullLog:  mvvm.NewObservableList[string](),
	}
	im.Run = newRunForm(a)
	im.Search.SubscribeChanged(im.refilter)
	return im
}

func (im *Images) apply(list []api.ImageSummary, now time.Time) {
	im.all = im.all[:0]
	for _, s := range list {
		digest := ""
		if len(s.RepoDigests) > 0 {
			if _, d, ok := strings.Cut(s.RepoDigests[0], "@"); ok {
				digest = ShortID(d)
			}
		}
		tags := s.RepoTags
		if len(tags) == 0 {
			tags = []string{"<none>:<none>"}
		}
		for _, t := range tags {
			if len(im.all) == MaxImages {
				break
			}
			im.all = append(im.all, ImageRow{
				Ref: Clean(t, maxField), ID: Clean(s.ID, 128), Digest: digest,
				Size: Size(s.Size), SizeBytes: s.Size,
				Created: Ago(now, time.Unix(s.Created, 0)), CreatedAt: s.Created, Containers: s.Containers,
			})
		}
	}
	sort.SliceStable(im.all, func(i, j int) bool { return im.all[i].Ref < im.all[j].Ref })
	im.refilter()
}

func (im *Images) refilter() {
	q := strings.ToLower(strings.TrimSpace(im.Search.Get()))
	var rows []ImageRow
	for _, r := range im.all {
		if q == "" || strings.Contains(strings.ToLower(r.Ref+" "+r.ID), q) {
			rows = append(rows, r)
		}
	}
	Sync(im.Rows, rows, ImageRow.Key)
	if _, ok := im.Find(im.Selected.Get()); !ok {
		im.Selected.Set("")
	}
}

// Find returns the row with key.
func (im *Images) Find(key string) (ImageRow, bool) {
	for _, r := range im.all {
		if r.Key() == key {
			return r, true
		}
	}
	return ImageRow{}, false
}

// RemoveLine is the command line of Remove.
func (im *Images) RemoveLine(r ImageRow, force bool) string {
	if force {
		return Line("rmi", "-f", r.Ref)
	}
	return Line("rmi", r.Ref)
}

// Remove removes the image by its reference (by ID when it has none).
func (im *Images) Remove(r ImageRow, force bool) {
	ref := r.Ref
	if strings.HasPrefix(ref, "<none>") {
		ref = r.ID
	}
	im.app.Do(im.RemoveLine(r, force), func(ctx context.Context) error {
		_, err := im.app.eng.RemoveImage(ctx, ref, force)
		return err
	})
}

// PullLine is the pull dialog's command line.
func (im *Images) PullLine() string { return Line("pull", strings.TrimSpace(im.PullRef.Get())) }

// Pull pulls PullRef, streaming progress into PullLog.
func (im *Images) Pull() {
	ref := strings.TrimSpace(im.PullRef.Get())
	if ref == "" || im.Pulling.Get() {
		return
	}
	im.Pulling.Set(true)
	im.PullLog.Clear()
	im.app.Do(im.PullLine(), func(ctx context.Context) error {
		err := im.app.eng.Pull(ctx, ref, func(m api.JSONMessage) {
			line := Clean(strings.TrimSpace(m.ID+" "+m.Status), maxField)
			im.app.post(func() { im.logPull(line) })
		})
		im.app.post(func() {
			if err != nil {
				im.logPull("error: " + Clean(err.Error(), maxField))
			} else {
				im.logPull("done")
			}
			im.Pulling.Set(false)
		})
		return err
	})
}

func (im *Images) logPull(line string) {
	if im.PullLog.Len() == maxPullLog {
		im.PullLog.RemoveAt(0)
	}
	im.PullLog.Append(line)
}

// RunForm is the run dialog: the options of `wctr run -d`.
type RunForm struct {
	app *App
	// Image, Name, Command (shell words), Env (one KEY=value per line or
	// comma), Entrypoint, Workdir and Memory mirror wctr run's arguments.
	Image, Name, Command, Env   *mvvm.Observable[string]
	Entrypoint, Workdir, Memory *mvvm.Observable[string]
	AutoRemove                  *mvvm.Observable[bool]
	// Line is the command line the form stands for, Error what is wrong
	// with it ("" when it can run).
	Line, Error *mvvm.Observable[string]
	// Started is the ID of the last container the form started.
	Started *mvvm.Observable[string]
}

func newRunForm(a *App) *RunForm {
	f := &RunForm{
		app: a, Image: mvvm.NewObservable(""), Name: mvvm.NewObservable(""), Command: mvvm.NewObservable(""),
		Env: mvvm.NewObservable(""), Entrypoint: mvvm.NewObservable(""), Workdir: mvvm.NewObservable(""),
		Memory: mvvm.NewObservable(""), AutoRemove: mvvm.NewObservable(false),
		Line: mvvm.NewObservable(""), Error: mvvm.NewObservable(""), Started: mvvm.NewObservable(""),
	}
	for _, o := range []mvvm.Changeable{f.Image, f.Name, f.Command, f.Env, f.Entrypoint, f.Workdir, f.Memory, f.AutoRemove} {
		o.SubscribeChanged(f.update)
	}
	f.update()
	return f
}

// Reset prefills the form for image.
func (f *RunForm) Reset(image string) {
	for _, o := range []*mvvm.Observable[string]{f.Name, f.Command, f.Env, f.Entrypoint, f.Workdir, f.Memory} {
		o.Set("")
	}
	f.AutoRemove.Set(false)
	f.Image.Set(image)
}

func (f *RunForm) update() {
	_, _, line, err := f.Request()
	f.Line.Set(line)
	if err != nil {
		f.Error.Set(err.Error())
	} else {
		f.Error.Set("")
	}
}

// envList parses the Env field.
func envList(s string) ([]string, error) {
	var env []string
	for _, f := range strings.FieldsFunc(s, func(r rune) bool { return r == '\n' || r == ',' }) {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		k, _, ok := strings.Cut(f, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("environment %q: write KEY=value", f)
		}
		env = append(env, f)
	}
	return env, nil
}

// Request is what the form asks the engine for, and the equivalent
// command line; err says what is wrong with the form.
func (f *RunForm) Request() (name string, req api.ContainerCreateRequest, line string, err error) {
	image := strings.TrimSpace(f.Image.Get())
	name = strings.TrimSpace(f.Name.Get())
	args := []string{"run", "-d"}
	if name != "" {
		args = append(args, "--name", name)
	}
	env, eerr := envList(f.Env.Get())
	for _, e := range env {
		args = append(args, "-e", e)
	}
	if f.AutoRemove.Get() {
		args = append(args, "--rm")
	}
	ep := strings.TrimSpace(f.Entrypoint.Get())
	if ep != "" {
		args = append(args, "--entrypoint", ep)
		req.Entrypoint = api.StrSlice{ep}
	}
	if w := strings.TrimSpace(f.Workdir.Get()); w != "" {
		args = append(args, "-w", w)
		req.WorkingDir = w
	}
	mem := strings.TrimSpace(f.Memory.Get())
	var merr error
	if mem != "" {
		args = append(args, "-m", mem)
		req.HostConfig.Memory, merr = units.RAMInBytes(mem)
		if merr != nil {
			merr = fmt.Errorf("memory %q: %v", mem, merr)
		}
	}
	args = append(args, image)
	cmd, cerr := lineedit.Split(f.Command.Get())
	args = append(args, cmd...)
	line = Line(args...)
	req.Image, req.Env, req.HostConfig.AutoRemove = image, env, f.AutoRemove.Get()
	if len(cmd) > 0 {
		req.Cmd = cmd
	}
	switch {
	case image == "":
		err = errors.New("choose an image")
	case eerr != nil:
		err = eerr
	case merr != nil:
		err = merr
	case cerr != nil:
		err = fmt.Errorf("command: %v", cerr)
	}
	return name, req, line, err
}

// Start creates and starts the container, pulling the image first when
// the engine does not have it, as wctr run does.
func (f *RunForm) Start() bool {
	name, req, line, err := f.Request()
	if err != nil {
		f.Error.Set(err.Error())
		return false
	}
	eng := f.app.eng
	f.app.Do(line, func(ctx context.Context) error {
		created, err := eng.Create(ctx, name, req)
		if api.IsNotFound(err) && strings.Contains(err.Error(), "no such image") {
			if err := eng.Pull(ctx, req.Image, nil); err != nil {
				return err
			}
			created, err = eng.Create(ctx, name, req)
		}
		if err != nil {
			return err
		}
		if err := eng.Start(ctx, created.ID); err != nil {
			return err
		}
		f.app.post(func() { f.Started.Set(created.ID) })
		return nil
	})
	return true
}
