# dashboard

[![Docs](https://img.shields.io/badge/docs-hugo%20%2B%20hextra-0079A8)](https://go-wasmcontainers.github.io/docs/)
[![License: BSD-3-Clause](https://img.shields.io/badge/license-BSD--3--Clause-blue)](LICENSE)
[![CI](https://github.com/go-wasmcontainers/dashboard/actions/workflows/ci.yml/badge.svg)](https://github.com/go-wasmcontainers/dashboard/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-wasmcontainers/dashboard.svg)](https://pkg.go.dev/github.com/go-wasmcontainers/dashboard)
![status](https://img.shields.io/badge/status-M1%20in%20progress-yellow)

**A graphical dashboard for the [go-wasmcontainers](https://go-wasmcontainers.github.io/)
engine**, in pure Go: containers, images, volumes and a terminal running
`wctr`, in one window, built with
[go-widgets](https://github.com/go-widgets/toolkit). It is meant to become the
main view of the browser [playground](https://go-wasmcontainers.github.io/playground/):
a student does everything from the buttons or from `wctr` in the terminal,
and both stay in sync, because both speak to the same engine through the same
API (compatible with the Docker Engine API).

Every button names the command line it stands for (“this button =
`wctr rm -f web`”), and, when asked, writes it in the terminal too.

> **Status: M1 in progress.** This first part is the model: the viewmodels,
> the engine client, and the bridge that carries the API between the page and
> the engine's Web Worker. The views (go-widgets) and the demo page come next.

## Architecture

| Package | What it is |
|---|---|
| [`vm`](vm) | the viewmodels: containers (filter, search, sort, start/stop/restart/remove), container detail (inspect tree, environment, mounts, logs with follow), images (pull with progress, remove, run dialog mirroring `wctr run`), volumes, status bar, terminal (the engine's `cli/lineedit` editor in front of `wctr`, run in-process). Pure Go over an interface; no widget. |
| [`engine`](engine) | the `Engine` interface and its HTTP implementation over the engine's `api.Client`, plus volumes, disk usage and the event stream, which answer `ErrUnsupported` when the engine does not serve them yet |
| [`bridge`](bridge) | HTTP over `postMessage`: the page's `http.RoundTripper` and the worker's server around the engine's handler; responses stream (log follow, wait, pull) |
| [`internal/images`](internal/images) | the demo images (`hello`, `clock`, `fail`, built for `wasip1`) and the static registry that serves them |
| [`integration`](integration) | the viewmodels against a real engine (wazero, in-process), through the bridge |

The dashboard keeps up with the engine through `GET /events` when the engine
serves it, and polls every 2 s otherwise (engine v0.3.0 does not serve it
yet); the views that need endpoints the engine does not serve yet (volumes,
disk usage) say what they need instead of showing invented data, and fill in
by themselves once it does.

## Security

- Everything a container provides (names, labels, commands, inspect values,
  logs) is text: one-line fields go through `vm.Clean` (control and format
  characters removed or replaced, length bounded); logs and terminal output go
  to the go-widgets `Terminal`, whose screen model has no reply channel and
  discards every OSC/DCS/APC string (no clipboard write, no links, no title).
- Lists, the pull log, the inspect tree, pending log output and the bridge's
  per-response buffer are bounded.
- The bridge carries a path only (never a scheme or host), a fixed set of
  methods, bounded bodies and headers, and a bounded number of requests in
  flight. The dashboard talks to nothing but the engine's API.
- Fuzzed: the bridge server (`FuzzServer`), the inspect tree (`FuzzInspectTree`),
  `Clean` (`FuzzClean`).

## Testing

```sh
go test -race ./...
```

CI: gofmt, vet, build, `-race`, exact statement coverage of `vm`, `engine`
and `bridge` (every block, read by `covblocks`), fuzz smoke, govulncheck judged
by vulngate, js/wasm vet and build, and a cross-build matrix.

## License

BSD-3-Clause — see [LICENSE](LICENSE).
