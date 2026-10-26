package main

import (
	"bytes"
	"errors"
	"net"
	"strings"
	"testing"
)

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
