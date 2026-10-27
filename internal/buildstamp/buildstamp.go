// Package buildstamp reads the Go build information embedded in a binary of
// any format, js/wasm included, and judges whether it was built from a clean
// commit.
//
// debug/buildinfo reads ELF, Mach-O, PE, XCOFF and Plan 9 files only; a wasm
// module is "unrecognized file format". The linker writes the module
// information into the data as text between two 16-byte sentinels
// (cmd/go/internal/modload/build.go, infoStart and infoEnd), in the format
// runtime/debug.ParseBuildInfo reads, so this package finds the text between
// the sentinels and hands it to the standard parser.
package buildstamp

import (
	"bytes"
	"errors"
	"fmt"
	"runtime/debug"
)

// The sentinels around the module information (cmd/go/internal/modload).
var (
	infoStart = []byte("\x30\x77\xaf\x0c\x92\x74\x08\x02\x41\xe1\xc1\x07\xe6\xd6\x18\xe6")
	infoEnd   = []byte("\xf9\x32\x43\x31\x86\x18\x20\x72\x00\x82\x42\x10\x41\x16\xd8\xf2")
)

// maxInfo bounds the module information: a binary is untrusted input, and
// a start sentinel with no end nearby must not make the parser read megabytes.
const maxInfo = 1 << 20

// ErrNoInfo is returned for a binary that carries no module information.
var ErrNoInfo = errors.New("no Go module information in the binary")

// Read returns the build information embedded in bin.
func Read(bin []byte) (*debug.BuildInfo, error) {
	i := bytes.Index(bin, infoStart)
	if i < 0 {
		return nil, ErrNoInfo
	}
	rest := bin[i+len(infoStart):]
	if len(rest) > maxInfo {
		rest = rest[:maxInfo]
	}
	j := bytes.Index(rest, infoEnd)
	if j < 0 {
		return nil, fmt.Errorf("module information has no end within %d bytes", maxInfo)
	}
	return debug.ParseBuildInfo(string(rest[:j]))
}

// Setting returns the value of the build setting key, and whether it is set.
func Setting(bi *debug.BuildInfo, key string) (string, bool) {
	for _, s := range bi.Settings {
		if s.Key == key {
			return s.Value, true
		}
	}
	return "", false
}

// Clean reports an error unless bi was stamped from a version-controlled
// tree with no modification, at revision want when want is not empty.
func Clean(bi *debug.BuildInfo, want string) error {
	rev, ok := Setting(bi, "vcs.revision")
	if !ok {
		return errors.New("no vcs.revision: built without version-control stamping")
	}
	if mod, _ := Setting(bi, "vcs.modified"); mod != "false" {
		return fmt.Errorf("vcs.modified=%q at %s: built from a modified tree (module version %s)", mod, rev, bi.Main.Version)
	}
	if want != "" && rev != want {
		return fmt.Errorf("vcs.revision=%s, want %s", rev, want)
	}
	return nil
}
