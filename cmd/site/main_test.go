package main

import (
	"bytes"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func stampedFile(t *testing.T, modified string) string {
	t.Helper()
	start := "\x30\x77\xaf\x0c\x92\x74\x08\x02\x41\xe1\xc1\x07\xe6\xd6\x18\xe6"
	end := "\xf9\x32\x43\x31\x86\x18\x20\x72\x00\x82\x42\x10\x41\x16\xd8\xf2"
	info := "path\tx/cmd/y\nmod\tx\tv0.4.2\t\nbuild\tvcs=git\nbuild\tvcs.revision=abc\nbuild\tvcs.modified=" + modified + "\n"
	f := filepath.Join(t.TempDir(), "y.wasm")
	if err := os.WriteFile(f, []byte("\x00asm"+start+info+end), 0o644); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestCheck(t *testing.T) {
	run := func(args ...string) (string, error) {
		var out bytes.Buffer
		cmd := RootCmd()
		cmd.SetOut(&out)
		cmd.SetErr(&out)
		cmd.SetArgs(append([]string{"check"}, args...))
		err := cmd.Execute()
		return out.String(), err
	}
	clean, dirty := stampedFile(t, "false"), stampedFile(t, "true")
	if out, err := run(clean); err != nil || !strings.Contains(out, "v0.4.2 at abc, vcs.modified=false") {
		t.Errorf("clean: %v %q", err, out)
	}
	if _, err := run("--revision", "abc", clean); err != nil {
		t.Errorf("clean at abc: %v", err)
	}
	if _, err := run("--revision", "def", clean); err == nil {
		t.Error("wrong revision passed")
	}
	if _, err := run(clean, dirty); err == nil || !strings.Contains(err.Error(), "modified") {
		t.Errorf("dirty passed: %v", err)
	}
	if _, err := run(filepath.Join(t.TempDir(), "absent.wasm")); err == nil {
		t.Error("missing file passed")
	}
	empty := filepath.Join(t.TempDir(), "e.wasm")
	if err := os.WriteFile(empty, []byte("\x00asm"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(empty); err == nil {
		t.Error("unstamped file passed")
	}
}

func TestCommands(t *testing.T) {
	var out bytes.Buffer
	cmd := RootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"build", "--out", t.TempDir(), "--version", "bad version"})
	if err := cmd.Execute(); err == nil {
		t.Error("a bad version built")
	}
	listen = func(string, string) (net.Listener, error) { return nil, errors.New("no port") }
	defer func() { listen = net.Listen }()
	cmd = RootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"serve", "--dir", t.TempDir()})
	if err := cmd.Execute(); err == nil || err.Error() != "no port" {
		t.Errorf("serve: %v", err)
	}
	// A server that stops at once.
	listen = func(n, a string) (net.Listener, error) {
		l, err := net.Listen(n, "127.0.0.1:0")
		if err == nil {
			l.Close()
		}
		return l, err
	}
	cmd = RootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"serve", "--dir", t.TempDir()})
	if err := cmd.Execute(); err == nil || !strings.Contains(out.String(), "serving") {
		t.Errorf("serve on a closed listener: %v %q", err, out.String())
	}
}
