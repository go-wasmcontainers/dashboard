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
footer says “Stop = `wctr stop web`”; click it and the terminal shows
`# dashboard: wctr stop web` above its prompt.

**Try it:** <https://go-wasmcontainers.github.io/dashboard/> — a standalone
demo with its own engine in a Web Worker and three demo images (`hello`,
`clock`, `fail`). In the Terminal view: `wctr run -d --name web clock`, then
watch it in Containers.

> **Status: M1, on engine v0.8.0.** Containers (with stats), images, volumes,
> status bar and terminal, kept current by the engine's event stream. Tested
> with Playwright in Chromium (isolation headers and the service worker
> GitHub Pages needs); the Pages workflow also drives the live page in
> Chromium and Firefox.

## What it shows

| View | |
|---|---|
| **Containers** | name, image, state (coloured: running, exited 0, exited ≠ 0, created), status, created, ID; filter running/all, search, sort by any column; Start, Stop, Restart, Remove on the selected container (the engine's `/start`, `/stop`, `/restart` and `DELETE`: `wctr start`, `wctr stop`, `wctr restart`, `wctr rm`). Detail pane: **Logs** (follow on/off; ANSI colours rendered, stderr in red), **Inspect** (the inspect document as an indented tree, in its own key order), **Environment**, **Mounts** (`volume data -> /data (rw)`), **Stats** (`wctr stats`: a memory gauge against the container's limit, a sparkline of the last 60 readings, bytes written, layer and log size, pids; CPU shows `--` with the engine's reason, since a wasm guest's CPU is not measured and 0% would read as idle). |
| **Images** | repository:tag, image ID, digest, size, created; **Pull…** (progress lines), **Run…** (image, name, command, environment, entrypoint, working dir, memory, volumes `-v NAME:/path[:ro]`, `--rm`, mirroring `wctr run -d`, with the command line shown live as you type), Remove (Force). |
| **Volumes** | name, driver, mountpoint, created, size, how many containers use it; Create (`wctr volume create NAME`), the selected volume's inspect tree, Remove (Force: `wctr volume rm -f NAME`), Prune (All unused: `wctr volume prune -a -f`), with what it reclaimed. An engine older than v0.8.0 gets a message instead — never invented rows. |
| **Terminal** | `wctr` run in-process against the same engine, with the engine's line editor (`cli/lineedit`: history, Ctrl-R, Tab completion of commands, flags and container names). |
| status bar | engine version and API, counts, storage from `GET /system/df` (images and volumes, and where the engine keeps its state and how full it is — `IndexedDB 2MB of 1GB (persistent)`, or `none` for an in-memory engine), how it keeps up: `live (GET /events)`; when the engine cuts the stream (a slow reader) or the connection breaks, the dashboard resynchronises and reconnects from the last event's time, backing off; an engine without the stream is polled every 2 s. |

Wherever the wctr in use has no command for an action, the footer shows the
API call instead (`POST /containers/web/stop`, with a note), and switches to
the command line by itself once wctr has it.

## Look

- **Crisp on any display.** Text is the toolkit's anti-aliased OpenType face (Atkinson Hyperlegible; the terminals use JetBrains Mono). The canvas renders at device pixels: its backing store is the CSS size × `devicePixelRatio`, and it follows browser zoom and moves between displays (go-widgets/webcanvas `PixelRatioAware`, with every metric and face scaled through `toolkit.SetMetricScale`). Measured in the page with `getImageData` at ratio 2: a 2560-px backing store for 1280 CSS px, and title glyphs with 17 coverage levels (the 5×7 bitmap face had 2).
- **Icons** on the navigation and in the search fields are Iconoir glyphs (MIT, go-icons/iconoir), drawn through the toolkit's icon seam. They are rasterised at the device size of their box, cached per icon, size and colour, and recoloured with the row's ink.
- **Delimitation.** The sidebar has its own ground and a border line. The tables have a frame, a header separator, column separators and row lines. The border colour is at least 3:1 against every ground it touches, in both themes (WCAG 2.2 SC 1.4.11; asserted in `ui/theme_test.go`).

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

A full frame (1280×800) with 200 containers and 100 images: 0.86 ms
(containers), 1.07 ms (images), 0.71 ms (terminal) natively on one core
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
