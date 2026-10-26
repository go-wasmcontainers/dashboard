//go:build !(js && wasm)

// Command dashboard is a browser program (GOOS=js GOARCH=wasm); natively
// it only says so. go-widgets/webcanvas, the canvas host the scene runs in,
// has no native window backend: the scene (package ui) is tested natively
// against a pixel buffer instead. Build the demo site with
//
//	go run ./cmd/site build --out dist
package main

import (
	"fmt"
	"os"
)

var osExit = os.Exit

func main() {
	fmt.Fprintln(os.Stderr, "dashboard is a browser program: build it with GOOS=js GOARCH=wasm, or the whole demo site with `go run ./cmd/site build --out dist`.")
	osExit(2)
}
