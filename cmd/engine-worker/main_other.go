//go:build !(js && wasm)

// Command engine-worker is the demo page's engine, a browser program
// (GOOS=js GOARCH=wasm, run in a Web Worker); natively it only says so.
package main

import (
	"fmt"
	"os"
)

var osExit = os.Exit

func main() {
	fmt.Fprintln(os.Stderr, "engine-worker runs in a browser Web Worker: build it with GOOS=js GOARCH=wasm (go run ./cmd/site build does).")
	osExit(2)
}
