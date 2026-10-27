package buildstamp

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const rev = "778f76aa90091bf10c91bebf740371a6258c54aa"

func stamped(modified string) []byte {
	info := "path\texample.com/p/cmd/x\nmod\texample.com/p\tv0.0.0\t\n" +
		"build\tGOOS=js\nbuild\tvcs=git\nbuild\tvcs.revision=" + rev + "\n"
	if modified != "" {
		info += "build\tvcs.modified=" + modified + "\n"
	}
	var b bytes.Buffer
	b.WriteString("\x00asm\x01\x00\x00\x00 code ")
	b.Write(infoStart)
	b.WriteString(info)
	b.Write(infoEnd)
	b.WriteString(" more data")
	return b.Bytes()
}

func TestClean(t *testing.T) {
	bi, err := Read(stamped("false"))
	if err != nil {
		t.Fatal(err)
	}
	if bi.Path != "example.com/p/cmd/x" {
		t.Errorf("path %q", bi.Path)
	}
	if err := Clean(bi, ""); err != nil {
		t.Errorf("clean: %v", err)
	}
	if err := Clean(bi, rev); err != nil {
		t.Errorf("clean at rev: %v", err)
	}
	if err := Clean(bi, "0123"); err == nil || !strings.Contains(err.Error(), "want 0123") {
		t.Errorf("other revision: %v", err)
	}
}

// The planted positive: the published v0.4.2 wasm said vcs.modified=true.
func TestDirty(t *testing.T) {
	for _, m := range []string{"true", ""} {
		bi, err := Read(stamped(m))
		if err != nil {
			t.Fatal(err)
		}
		if err := Clean(bi, ""); err == nil || !strings.Contains(err.Error(), "modified") {
			t.Errorf("vcs.modified=%q passed: %v", m, err)
		}
	}
}

func TestUnstamped(t *testing.T) {
	if _, err := Read([]byte("\x00asm no info")); !errors.Is(err, ErrNoInfo) {
		t.Errorf("no info: %v", err)
	}
	open := append(append([]byte{}, infoStart...), bytes.Repeat([]byte("x"), maxInfo+10)...)
	open = append(open, infoEnd...)
	if _, err := Read(open); err == nil || !strings.Contains(err.Error(), "no end") {
		t.Errorf("unterminated: %v", err)
	}
	bad := append(append(append([]byte{}, infoStart...), "path\tx\nmod\tbroken\n"...), infoEnd...)
	if _, err := Read(bad); err == nil {
		t.Error("malformed module line parsed")
	}
	bi, err := Read(append(append(append([]byte{}, infoStart...), "path\tx\n"...), infoEnd...))
	if err != nil {
		t.Fatal(err)
	}
	if err := Clean(bi, ""); err == nil || !strings.Contains(err.Error(), "vcs.revision") {
		t.Errorf("no vcs stamping passed: %v", err)
	}
}

// A real js/wasm binary, built here, reads back with this package's own
// module path: the sentinels are the ones the linker writes.
func TestRealWasm(t *testing.T) {
	if testing.Short() {
		t.Skip("builds a wasm binary")
	}
	out := filepath.Join(t.TempDir(), "x.wasm")
	cmd := exec.Command("go", "build", "-trimpath", "-o", out, "./testdata/hello")
	cmd.Env = append(os.Environ(), "GOOS=js", "GOARCH=wasm", "CGO_ENABLED=0")
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, b)
	}
	bin, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	bi, err := Read(bin)
	if err != nil {
		t.Fatal(err)
	}
	if bi.Path != "github.com/go-wasmcontainers/dashboard/internal/buildstamp/testdata/hello" {
		t.Errorf("path %q", bi.Path)
	}
	if v, ok := Setting(bi, "GOOS"); !ok || v != "js" {
		t.Errorf("GOOS %q %v", v, ok)
	}
}
