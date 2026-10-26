# dashboard

[![Docs](https://img.shields.io/badge/docs-hugo%20%2B%20hextra-0079A8)](https://go-wasmcontainers.github.io/docs/)
[![License: BSD-3-Clause](https://img.shields.io/badge/license-BSD--3--Clause-blue)](LICENSE)
[![CI](https://github.com/go-wasmcontainers/dashboard/actions/workflows/ci.yml/badge.svg)](https://github.com/go-wasmcontainers/dashboard/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-wasmcontainers/dashboard.svg)](https://pkg.go.dev/github.com/go-wasmcontainers/dashboard)
![status](https://img.shields.io/badge/status-M1-green)

**A graphical dashboard for the [go-wasmcontainers](https://go-wasmcontainers.github.io/)
engine**, in pure Go: containers, images, volumes and a terminal running
`wctr`, in one window, built with [go-widgets](https://github.com/go-widgets/toolkit)
and drawn on a browser canvas. It is meant to become the main view of the
[playground](https://go-wasmcontainers.github.io/playground/): a student does
everything from the buttons or from `wctr` in the terminal, and both stay in
sync, because both speak to the same engine through the same API (compatible
with the Docker Engine API).

Every action names the command line it stands for — hover a button and the
footer says “Stop = `wctr kill web`”; click it and the terminal shows
`# dashboard: wctr kill web` above its prompt.

**Try it:** <https://go-wasmcontainers.github.io/dashboard/> — a standalone
demo with its own engine in a Web Worker and three demo images (`hello`,
`clock`, `fail`). In the Terminal view: `wctr run -d --name web clock`, then
watch it in Containers.

> **Status: M1.** Containers, container detail, images (pull, run, remove),
> volumes (waiting for the engine), status bar and terminal work in Chromium
> (tested with Playwright, both with real isolation headers and with the
> service worker GitHub Pages needs).

## What it shows

| View | |
|---|---|
| **Containers** | name, image, state (coloured: running, exited 0, exited ≠ 0, created), status, created, ID; filter running/all, search, sort by any column; Start, Stop, Restart, Remove on the selected container. Detail pane: **Logs** (follow on/off; ANSI colours rendered, stderr in red), **Inspect** (the inspect document as an indented tree, in its own key order), **Environment**, **Mounts**. |
| **Images** | repository:tag, image ID, digest, size, created; **Pull…** (progress lines), **Run…** (image, name, command, environment, entrypoint, working dir, memory, `--rm`, mirroring `wctr run -d`, with the command line shown live as you type), Remove (Force). |
| **Volumes** | the engine's volumes once it serves `GET /volumes`; until then the view says what it needs — never invented rows. |
| **Terminal** | `wctr` run in-process against the same engine, with the engine's line editor (`cli/lineedit`: history, Ctrl-R, Tab completion of commands, flags and container names). |
| status bar | engine version and API, counts, storage (from `GET /system/df` once served; the images' total until then), how it keeps up (`GET /events` or polling). |

Wherever wctr has no command for an action yet (`start`, in engine v0.3.0),
the footer shows the API call instead — `POST /containers/web/start`, with
a note that wctr has no `start` yet — and switches to the command line by
itself once wctr has it.

## Keyboard

F6 moves between the navigation and the view's main widget; Tab / Shift-Tab
between controls; arrows in lists and tables; Alt+1…4 switch views. **While
the terminal has focus it gets every key it uses** (Ctrl-C/D/L/R/W/U, arrows,
Tab, Alt+key, …) before any of these shortcuts; F6 leaves it. (Browsers
reserve a few chords — Ctrl-W, Ctrl-T, Ctrl-N in Chrome — that no page can
take.)

## Architecture

| Package | What it is |
|---|---|
| [`vm`](vm) | the viewmodels: pure Go over an `Engine` interface, no widget; every action goes through the API |
| [`ui`](ui) | the go-widgets scene bound to the viewmodels; a `webcanvas.App`, tested natively against a pixel buffer |
| [`engine`](engine) | the `Engine` interface over the engine's `api.Client`; volumes, disk usage and events answer `ErrUnsupported` on the router's own 404, so the dashboard lights them up by itself when the engine serves them |
| [`bridge`](bridge) | HTTP over `postMessage` between the page (dashboard, canvas) and the engine's Web Worker; responses stream (log follow, wait, pull) |
| [`cmd/dashboard`](cmd/dashboard), [`cmd/engine-worker`](cmd/engine-worker) | the two `js/wasm` programs of the page |
| [`cmd/site`](cmd/site) | builds the demo site (`site build --out dist`) and serves it (`site serve --dir dist`) |
| [`integration`](integration), [`e2e`](e2e) | the viewmodels against a real engine (wazero) through the bridge; the page in real browsers (`go test -tags e2e ./e2e/`) |

go-widgets gained a terminal emulator for this ([`toolkit.Terminal`](https://github.com/go-widgets/toolkit)
over the pure-Go `vt.Screen`, v0.342.0). The scene runs where go-widgets/webcanvas
runs — a browser canvas; there is no native window build.

### In the playground

The dashboard attaches to an engine that already runs in a worker. The
worker serves the engine's API handler through the bridge
(`bridge.ServeJS(handler, self)` and one line in its `onmessage`); the page
sets `window.__wctrEngine` to that worker and runs `dashboard.wasm` on a
`<canvas id="screen">`. `cmd/engine-worker` is the reference.

## Security

- Everything a container provides is text: names, labels, commands and
  inspect values go through `vm.Clean` (control and format characters out,
  bounded); logs and terminal output go to the go-widgets `Terminal`, whose
  screen has no reply channel and discards every OSC/DCS/APC string (no
  clipboard write, no links, no title, no reports), and whose paste strips
  control characters.
- Bounded: lists, the pull log, the inspect tree (nodes, depth), pending log
  bytes, keys queued for the terminal, the bridge's buffers, bodies, headers
  and requests in flight.
- The bridge carries a path only (never a scheme or host) and a fixed set of
  methods. The page talks to nothing but its own origin (CSP
  `default-src 'self'`), and to the engine only through its API.
- Fuzzed: `FuzzServer` (bridge), `FuzzInspectTree`, `FuzzClean`; the terminal's
  parser is fuzzed in go-widgets.

## Performance

A full frame (1280×800) with 200 containers and 100 images: 0.83 ms
(containers), 0.89 ms (images), 0.71 ms (terminal) natively on one core
(Apple M4 Max, `BenchmarkFrame`). The views only redraw when something
changed: a refresh that finds the engine as it was emits no list event.

## Testing

```sh
go test -race ./...                 # viewmodels, bridge, client, scene, a real engine
go test -tags e2e ./e2e/            # the page in Chromium and Firefox (Playwright)
go run ./cmd/site build --out dist && go run ./cmd/site serve --dir dist --isolate
```

CI: gofmt, vet, build, `-race`, exact block coverage of `vm`, `engine` and
`bridge` (covblocks), fuzz smoke, govulncheck judged by vulngate, js/wasm vet
and build, a cross-build matrix; the Pages workflow publishes the demo and
checks the CDN serves the build.

## License

BSD-3-Clause — see [LICENSE](LICENSE).
