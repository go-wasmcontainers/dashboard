// The page: cross-origin isolation (a service worker on hosts that cannot
// send the headers), the engine's Web Worker, then the dashboard's wasm on
// the canvas. The dashboard finds the worker as window.__wctrEngine and
// speaks the engine's API to it over postMessage (package bridge).
"use strict";

(function () {
  const status = document.getElementById("status");
  const build = document.currentScript.dataset.build;
  const say = (s) => { status.hidden = false; status.textContent = s; };

  if (!window.crossOriginIsolated) {
    if (!("serviceWorker" in navigator)) {
      say("This browser cannot isolate the page (no service worker): containers need SharedArrayBuffer.");
      return;
    }
    const tried = sessionStorage.getItem("coi-reload");
    navigator.serviceWorker.register("sw.js").then(
      () => {
        if (tried) {
          say("Cross-origin isolation failed: containers need SharedArrayBuffer.");
          return;
        }
        sessionStorage.setItem("coi-reload", "1");
        if (navigator.serviceWorker.controller) {
          location.reload();
        } else {
          navigator.serviceWorker.addEventListener("controllerchange", () => location.reload());
        }
      },
      (err) => say("Service worker registration failed: " + err),
    );
    return;
  }
  sessionStorage.removeItem("coi-reload");

  const engine = new Worker("engine-worker.js?v=" + build);
  window.__wctrEngine = engine;
  const onReady = (e) => {
    const m = e.data;
    if (m && m.t === "fatal") {
      say("The engine failed: " + m.message);
      return;
    }
    if (!m || m.t !== "ready") return;
    engine.removeEventListener("message", onReady);
    say("Starting the dashboard…");
    const go = new Go();
    WebAssembly.instantiateStreaming(fetch("dashboard.wasm?v=" + build), go.importObject).then(
      (r) => {
        status.hidden = true;
        document.getElementById("screen").focus();
        go.run(r.instance);
      },
      (err) => say("The dashboard failed to load: " + err),
    );
  };
  engine.addEventListener("message", onReady);
  engine.addEventListener("error", (e) => say("The engine worker failed: " + (e.message || e)));
})();
