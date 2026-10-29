//go:build js && wasm

package bridge

import (
	"net/http"
	"syscall/js"
)

// ToJS converts m to the plain object postMessage carries.
func ToJS(m Message) js.Value {
	o := js.Global().Get("Object").New()
	o.Set("t", m.T)
	o.Set("id", m.ID)
	if m.Method != "" {
		o.Set("method", m.Method)
		o.Set("url", m.URL)
	}
	if m.Header != nil {
		h := js.Global().Get("Object").New()
		for k, v := range m.Header {
			h.Set(k, v)
		}
		o.Set("header", h)
	}
	if m.Data != nil {
		a := js.Global().Get("Uint8Array").New(len(m.Data))
		js.CopyBytesToJS(a, m.Data)
		o.Set("data", a)
	}
	if m.Status != 0 {
		o.Set("status", m.Status)
	}
	if m.Err != "" {
		o.Set("err", m.Err)
	}
	if m.Stream {
		o.Set("stream", true)
	}
	return o
}

// FromJS converts a received message; ok is false for anything that is
// not a well-formed bridge message (other traffic on the same channel).
func FromJS(v js.Value) (m Message, ok bool) {
	if v.Type() != js.TypeObject {
		return m, false
	}
	t := v.Get("t")
	id := v.Get("id")
	if t.Type() != js.TypeString || !IsBridge(t.String()) || id.Type() != js.TypeNumber {
		return m, false
	}
	m.T, m.ID = t.String(), id.Int()
	str := func(name string) string {
		if x := v.Get(name); x.Type() == js.TypeString {
			return x.String()
		}
		return ""
	}
	m.Method, m.URL, m.Err = str("method"), str("url"), str("err")
	if s := v.Get("status"); s.Type() == js.TypeNumber {
		m.Status = s.Int()
	}
	m.Stream = v.Get("stream").Type() == js.TypeBoolean && v.Get("stream").Bool()
	if h := v.Get("header"); h.Type() == js.TypeObject {
		keys := js.Global().Get("Object").Call("keys", h)
		m.Header = map[string]string{}
		for i := 0; i < keys.Length() && i < MaxHeader+1; i++ {
			k := keys.Index(i).String()
			if x := h.Get(k); x.Type() == js.TypeString {
				m.Header[k] = x.String()
			}
		}
	}
	if d := v.Get("data"); d.Type() == js.TypeObject && d.InstanceOf(js.Global().Get("Uint8Array")) {
		n := d.Length()
		if n > MaxBody && m.T == TypeRequest {
			n = MaxBody + 1 // enough for the server to refuse it
		}
		m.Data = make([]byte, n)
		js.CopyBytesToGo(m.Data, d.Call("subarray", 0, n))
	}
	return m, true
}

// ServeJS returns a server for h that answers through target (in a
// worker: self) and a handler to call with each message event's data; the
// handler reports whether the message was the bridge's.
func ServeJS(h http.Handler, target js.Value) (*Server, func(js.Value) bool) {
	s := NewServer(h, func(m Message) { target.Call("postMessage", ToJS(m)) })
	return s, func(data js.Value) bool {
		m, ok := FromJS(data)
		if ok {
			s.Deliver(m)
		}
		return ok
	}
}

// DialJS returns a transport to the engine behind worker (a Worker, or a
// MessagePort), listening on it for the bridge's replies.
func DialJS(worker js.Value) *Transport {
	t := NewTransport(func(m Message) { worker.Call("postMessage", ToJS(m)) })
	worker.Call("addEventListener", "message", js.FuncOf(func(_ js.Value, args []js.Value) any {
		if len(args) > 0 {
			if m, ok := FromJS(args[0].Get("data")); ok {
				t.Deliver(m) // never blocks; keeps the chunks in order
			}
		}
		return nil
	}))
	worker.Call("addEventListener", "error", js.FuncOf(func(js.Value, []js.Value) any {
		go t.Shutdown()
		return nil
	}))
	return t
}
