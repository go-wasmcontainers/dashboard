// The engine's worker: Go's wasm_exec.js glue, then the engine (engine.wasm,
// GOOS=js). The build id arrives as ?v=… and is kept on every asset.
"use strict";

const v = self.location.search;
importScripts("wasm_exec.js" + v);

const go = new Go();
WebAssembly.instantiateStreaming(fetch("engine.wasm" + v), go.importObject)
  .then((r) => go.run(r.instance))
  .catch((err) => postMessage({ t: "fatal", message: "engine: " + String(err) }));
