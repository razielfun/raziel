package tunnel

import "github.com/raziel-ai/raziel/internal/pty"

// Dispatcher routes inbound reverse-tunnel frames by StreamID. A FrameOpen is an
// attach request whose Payload is the #79 host token; the dispatcher authorizes
// it via the PtyAuthorizer seam (single-use, bound to this box) BEFORE starting
// any stream — the SAME seam the inbound WS uses (#81), so the reverse tunnel
// reuses the auth path. A forged / revoked / replayed token starts NO session and
// is answered with a FrameClose on that stream.
//
// Once a stream is open, FrameData/FrameClose for it route to the registered
// handlers; frames for an UNKNOWN stream (never opened, or already closed) are
// dropped — a stray/forged stream id must never panic the daemon or deliver bytes
// to a PTY that isn't there. Dispatch is pure routing: the actual PTY lives behind
// the injected callbacks, so this is unit-testable with fakes — no real socket,
// no //go:build linux PTY.
type Dispatcher struct {
	auth        pty.PtyAuthorizer
	startStream func(streamID uint32, grant pty.AttachGrant)
	onData      func(streamID uint32, b []byte)
	onClose     func(streamID uint32)
	open        map[uint32]struct{} // stream ids currently open
}

// NewDispatcher builds a dispatcher that authorizes opens via auth and hands an
// authorized stream to startStream (which wires it to a PTY).
func NewDispatcher(auth pty.PtyAuthorizer, startStream func(streamID uint32, grant pty.AttachGrant)) *Dispatcher {
	return &Dispatcher{
		auth:        auth,
		startStream: startStream,
		open:        make(map[uint32]struct{}),
	}
}

// OnData registers the sink for stream bytes (terminal I/O toward the PTY).
func (d *Dispatcher) OnData(fn func(streamID uint32, b []byte)) { d.onData = fn }

// OnClose registers the handler invoked when a stream is closed.
func (d *Dispatcher) OnClose(fn func(streamID uint32)) { d.onClose = fn }

// Handle processes one inbound frame. It returns a frame to send back on the
// tunnel (e.g. a FrameClose rejecting an unauthorized open), or nil.
func (d *Dispatcher) Handle(f Frame) *Frame {
	switch f.Type {
	case FrameOpen:
		// Authorize BEFORE starting a stream: a token is single-use and bound to
		// this box. A bad token starts no session and is rejected with a close.
		grant, err := d.auth.Authorize(string(f.Payload))
		if err != nil {
			return &Frame{Type: FrameClose, StreamID: f.StreamID, Payload: []byte("unauthorized")}
		}
		d.open[f.StreamID] = struct{}{}
		d.startStream(f.StreamID, grant)
		return nil

	case FrameData:
		// Route to the open stream; drop data for an unknown/closed stream.
		if _, ok := d.open[f.StreamID]; ok && d.onData != nil {
			d.onData(f.StreamID, f.Payload)
		}
		return nil

	case FrameClose:
		if _, ok := d.open[f.StreamID]; ok {
			delete(d.open, f.StreamID)
			if d.onClose != nil {
				d.onClose(f.StreamID)
			}
		}
		return nil

	default:
		return nil
	}
}
