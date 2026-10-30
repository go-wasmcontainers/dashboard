# dashboard

[![Docs](https://img.shields.io/badge/docs-hugo%20%2B%20hextra-0079A8)](https://go-wasmcontainers.github.io/docs/)
[![License: BSD-3-Clause](https://img.shields.io/badge/license-BSD--3--Clause-blue)](LICENSE)
[![CI](https://github.com/go-wasmcontainers/dashboard/actions/workflows/ci.yml/badge.svg)](https://github.com/go-wasmcontainers/dashboard/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-wasmcontainers/dashboard.svg)](https://pkg.go.dev/github.com/go-wasmcontainers/dashboard)
![status](https://img.shields.io/badge/status-M1-green)

**A graphical dashboard for the [go-wasmcontainers](https://go-wasmcontainers.github.io/)
engine**, in pure Go: containers, images, volumes, networks and a terminal running
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

> **Status: M1, on engine v0.29.1.** Containers (with stats, networks and a
> shell inside them), images, volumes, networks (and joining a class), GPU surfaces, status bar and terminal, kept current by the engine's event stream. Tested
> with Playwright in Chromium (isolation headers and the service worker
> GitHub Pages needs); the Pages workflow also drives the live page in
> Chromium and Firefox.

## What it shows

| View | |
|---|---|
| **Containers** | name, image, state (coloured: running, exited 0, exited ≠ 0, created), status, created, ID; filter running/all, search, sort by any column; Start, Stop, Restart, Remove on the selected container (the engine's `/start`, `/stop`, `/restart` and `DELETE`: `wctr start`, `wctr stop`, `wctr restart`, `wctr rm`). Detail pane: **Logs** (follow on/off; ANSI colours rendered, stderr in red), **Inspect** (the inspect document as an indented tree, in its own key order), **Environment**, **Mounts** (`volume data -> /data (rw)`), **Stats** (`wctr stats`: a memory gauge against the container's limit, a sparkline of the last 60 readings, bytes written, layer and log size, pids; CPU shows `--` with the engine's reason, since a wasm guest's CPU is not measured and 0% would read as idle), **Shell** (see below). |
| **Images** | repository:tag, image ID, digest, size, created; **Pull…** (progress lines), **Run…** (image, name, command, environment, entrypoint, working dir, memory, volumes `-v NAME:/path[:ro]`, `--rm`, mirroring `wctr run -d`, with the command line shown live as you type), Remove (Force). |
| **Volumes** | name, driver, mountpoint, created, size, how many containers use it; Create (`wctr volume create NAME`), the selected volume's inspect tree, Remove (Force: `wctr volume rm -f NAME`), Prune (All unused: `wctr volume prune -a -f`), with what it reclaimed. An engine older than v0.8.0 gets a message instead — never invented rows. |
| **Networks** | name, ID, driver, subnet, and the containers attached with their addresses (`web 10.42.0.2/24`); Create (`wctr network create NAME`), the selected network's inspect tree (`wctr network inspect NAME`), Remove (`wctr network rm NAME`). A container's detail has a **Networks** tab: each network it is on, its address and DNS names. On a page that can join a class (the playground), the view also offers **Join class…**; see below. An engine older than v0.11.0 gets a message instead. |
| **Terminal** | `wctr` run in-process against the same engine, with the engine's line editor (`cli/lineedit`: history, Ctrl-R, Tab completion of commands, flags and container names). |
| **Surfaces** | one pane per GPU surface a container opens (`wasi-gfx:surface`, rendered by go-wasmcontainers/gpu in the engine's worker), titled `<container> — surface N`, with `W×H, F fps`, or `waiting for first frame` before the first. Frames are drawn scaled to fit with their aspect kept; a closed surface keeps its last frame, greyed, until **Dismiss**. See below. |
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
between controls; arrows in lists and tables; Alt+1…6 switch views. **While
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
| [`cmd/site`](cmd/site) | builds the demo site (`site build --out dist`) and serves it (`site serve --dir dist`). `site check [--revision SHA] FILE...` reads the Go build info, in wasm too ([go-fleettools/buildstamp](https://github.com/go-fleettools/buildstamp)), and fails unless the program was built from a clean commit; the Pages build runs it. |
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

#### A shell inside a container

**Open shell** (the containers toolbar, on a running container) opens the
detail's **Shell** tab. It is `wctr exec -it CONTAINER sh` (the line is
shown):
- `POST /containers/{id}/exec` runs `/bin/sh` with a terminal at the tab's size.
- `/exec/{id}/start` sends the keys as raw bytes, and the engine does the line editing and echo.
- The raw output streams into the tab's terminal, which, like every terminal here, discards OSC/DCS/APC.
- `/exec/{id}/resize` follows the tab's size.
- When the shell ends, the tab shows `exited with status N`, from `GET /exec/{id}/json`.
- An image without `/bin/sh` gets the engine's error. Try `wctr run -d --name box ghcr.io/go-wasmcontainers/shell:0.1.0 sleep 3600`. The demo page mirrors that image, pinned by digest, from its own origin.
- On the Shell tab, keys go to the shell before any shortcut, and the arrows do not switch tabs. F6 leaves it.

The engine reads an exec's stdin from the request body after its JSON
start object, a documented divergence from Docker's hijacked connection.
So the bridge streams request bodies of unknown length: `http.req` with
`stream:true`, then `http.body` chunks, then `http.eof`. A page that runs
the dashboard against its own engine worker needs the worker's
`bridge.ServeJS` from this version, and the executor needs `wasi.WASIX`
among its host modules (`cmd/engine-worker` is the reference).

#### GPU surfaces

The engine's worker posts its GPU surfaces to the page (go-wasmcontainers/gpu,
`webgpu.Browser{OnSurface}`), and the dashboard listens on
`window.__wctrEngine`. No page code is needed. The messages:

| Message | Meaning |
|---|---|
| `{t:"surface", op:"open", surface, container, pid, width, height}` | a pane opens; the size defaults to 640×480 and is clamped to 1920×1080 |
| `{t:"surface", op:"resize", surface, width, height}` | the surface changed size |
| `{t:"surface", op:"close", surface}` | the last frame stays, greyed, until **Dismiss** |
| `{t:"frame", surface, seq, width, height, bitmap}` | one present(); the ImageBitmap is transferred to the page |

Each frame becomes the page's to handle:
- Only the newest frame per surface is kept. An older one still waiting is closed and acknowledged as dropped.
- At the next scene frame, the waiting bitmap is drawn into the surface's backing canvas (an `OffscreenCanvas`), **closed at once**, and acknowledged with `{t:"frame-ack", surface, seq}`. The engine paces present() on these acks.
- The pane is redrawn from the backing canvas through go-widgets/webcanvas `Layered` (v0.6.0), scaled to fit with its aspect kept, at device pixels.

Robustness:
- Frames are presented and acknowledged even while another view is shown, so the guest never waits on a hidden pane.
- A frame for an unknown or closed surface, one out of order, or a message without a real `ImageBitmap` is dropped, closed and acknowledged.

Bounds: at most 16 panes, `surface` must be a u32, `seq` an integer from 1 to 2^53, and sizes are clamped.

The e2e checks this with 30 synthetic ImageBitmaps:
- every frame is acknowledged exactly once, the malformed frame included;
- the pane's centre pixel is the frame's colour, at a 4:3 aspect;
- once the surface closes, the frame is shown greyed;
- every bitmap ends closed: an `ImageBitmap`'s width reads 0 after `close()`.

**Input to the guest** (go-wasmcontainers/gpu v0.6.0, `webgpu.SurfaceInput`):
the pane forwards pointer and key events to the engine worker as
`{t:"input", surface, kind, …}`.

- **Pointers** (`pointer-down`, `pointer-up`, `pointer-move`) carry `x, y` in surface pixels. A canvas pixel's centre is scaled from the frame's letterboxed box, and an event outside the frame is dropped (`vm.ToSurface`).
- **Keys** (`key-down`, `key-up`) carry `code`, the `KeyboardEvent.code` such as "KeyA"; `text`, the key when it is one character, at most 64 bytes; and `alt`, `ctrl`, `meta`, `shift`.
- **Focus:** a press on a pane's frame gives that pane the focus, and its frame is drawn in the accent colour. Only the focused pane sends keys. Like a terminal, it takes every key except F6, which leaves it; a click on a widget takes the focus back.
- **Rate:** pointer moves are coalesced to one per surface per frame.
- A closed surface takes no input.

The code and the releases come from go-widgets/webcanvas `KeyCodeAware` and `KeyUpper`.

#### Join class

A page whose engine can join a class says so with `window.__wctrClass`.
Without that object the dashboard does not offer the dialog:

```js
window.__wctrClass = {
  relay: "https://relay.example:8443", // the relay this engine was started with, "" for none
  useRelay(origin) { /* remember origin ("" forgets it), restart the engine with it (reload) */ },
  status() { return lastClassMessage; }, // the worker's last {t:"class", ...}, or null
};
```

The dialog runs in two steps:

1. **The relay.** It accepts only `https://host[:port]`; `wss://host` names the same relay and is mapped to `https://`. Anything else is refused: credentials, a path, a query, a fragment, a malformed host or port. The page then restarts the engine with that origin, because the worker's CSP may reach exactly that origin and nothing else.
2. **Your name and the join code.** The code is a secret:
   - its field is masked;
   - it is never in the accessibility tree or on the clipboard (toolkit v0.345.2);
   - it is posted once to the engine worker as `{t:"join", code, name}` and the field is cleared at once;
   - it is never in a command line, a message, the terminal, the URL, storage, the DOM or the test hook.

   The e2e checks every one of those places, with a planted positive.

The worker's `{t:"class"}` reports drive the status line, for example
`class: you are ada.class.lan on https://relay… · network class · addresses 10.42.2.0/24 (run --network class)`.
**Leave class** posts `{t:"leave"}`. This is the protocol of
go-wasmcontainers/playground's engine worker (playground#14, engine v0.11.0).

**Planned, not built: the class's flow map.** The relay has a GraphView of
who talks to whom in a class. Showing it in the Networks view, for the class
network, needs one thing the relay does not give a browser yet: a
**relay-issued, short-lived, read-only ticket**, scoped to one class and
returned to the engine worker with the session.

- The page must never hold the join code or the session token, so the ticket is the only credential the view would use.
- The view would fetch the graph through the worker, whose CSP already reaches the relay. It would draw the graph natively, cleaning every label, rather than embed the relay's page in an iframe. An iframe would need a `frame-src` hole and would put a third-party document inside the page.
- Until the relay issues such a ticket, the view shows only what the engine knows: the class network's attached containers.

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
(Apple M4 Max, `BenchmarkFrame`). The Networks view with 50 networks:
0.94 ms. That figure was taken under a 1-minute load of 12, so it is
indicative, and the other views measured 0.94, 1.11 and 0.78 ms in the
same run. The views only redraw when something
changed: a refresh that finds the engine as it was emits no list event.

`dashboard.wasm` (`-trimpath -ldflags="-s -w"`) is 19.41 MB raw and 4.93 MB
gzipped (v0.4.2, go-widgets/toolkit v0.345.1). It was 22.12 MB and 5.53 MB
with toolkit v0.344.0, so toolkit v0.345.1 cuts it by 12.3 % raw and 11.0 %
gzipped.

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
