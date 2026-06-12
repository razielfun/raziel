package tunnel_test

import (
	"errors"
	"testing"

	"github.com/raziel-ai/raziel/internal/pty"
	"github.com/raziel-ai/raziel/internal/tunnel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #86 D4 dispatch: the daemon reads frames off the reverse tunnel and routes each
// by StreamID. A FrameOpen is an attach request — its payload is the #79 host
// token, which MUST be authorized via the PtyAuthorizer seam (single-use, bound
// to this box) BEFORE any stream starts. A forged/unauthorized open starts NO
// session and is answered with a FrameClose. This is the same seam the inbound WS
// uses (#81), so the reverse tunnel reuses the auth path rather than reinventing
// it. Pure dispatch logic — fake authorizer + fake stream sink, no real socket or
// PTY, unit-tested on any OS.

// fakeAuthorizer stands in for pty.PtyAuthorizer.
type fakeAuthorizer struct {
	grant pty.AttachGrant
	err   error
	calls int
}

func (f *fakeAuthorizer) Authorize(token string) (pty.AttachGrant, error) {
	f.calls++
	if f.err != nil {
		return pty.AttachGrant{}, f.err
	}
	return f.grant, nil
}

func TestDispatchAuthorizesAnOpenBeforeStartingAStream(t *testing.T) {
	auth := &fakeAuthorizer{grant: pty.AttachGrant{Target: "box-1", UserID: "u1"}}
	started := []uint32{}
	d := tunnel.NewDispatcher(auth, func(streamID uint32, grant pty.AttachGrant) {
		started = append(started, streamID)
	})

	out := d.Handle(tunnel.Frame{Type: tunnel.FrameOpen, StreamID: 9, Payload: []byte("good-token")})

	assert.Equal(t, 1, auth.calls)
	assert.Equal(t, []uint32{9}, started) // stream 9 started after authorize
	assert.Nil(t, out)                    // no reject frame
}

func TestDispatchRejectsAnUnauthorizedOpenWithoutStartingAStream(t *testing.T) {
	auth := &fakeAuthorizer{err: errors.New("revoked or replayed token")}
	started := []uint32{}
	d := tunnel.NewDispatcher(auth, func(streamID uint32, grant pty.AttachGrant) {
		started = append(started, streamID)
	})

	out := d.Handle(tunnel.Frame{Type: tunnel.FrameOpen, StreamID: 9, Payload: []byte("forged")})

	assert.Empty(t, started)             // no session started for a bad token
	require.NotNil(t, out)               // answered with a close
	assert.Equal(t, tunnel.FrameClose, out.Type)
	assert.Equal(t, uint32(9), out.StreamID)
}

func TestDispatchRoutesDataToTheOpenStreamAndDropsUnknownStreams(t *testing.T) {
	auth := &fakeAuthorizer{grant: pty.AttachGrant{Target: "box-1", UserID: "u1"}}
	// Record bytes delivered to each started stream.
	delivered := map[uint32][]byte{}
	d := tunnel.NewDispatcher(auth, func(streamID uint32, grant pty.AttachGrant) {})
	d.OnData(func(streamID uint32, b []byte) {
		delivered[streamID] = append(delivered[streamID], b...)
	})

	// Open stream 5, then send it data.
	d.Handle(tunnel.Frame{Type: tunnel.FrameOpen, StreamID: 5, Payload: []byte("good")})
	d.Handle(tunnel.Frame{Type: tunnel.FrameData, StreamID: 5, Payload: []byte("ls -la")})
	assert.Equal(t, []byte("ls -la"), delivered[5])

	// Data for a stream that was never opened is dropped — no panic, no delivery.
	d.Handle(tunnel.Frame{Type: tunnel.FrameData, StreamID: 99, Payload: []byte("ghost")})
	assert.NotContains(t, delivered, uint32(99))
}

func TestDispatchClosesAnOpenStreamSoLaterDataIsDropped(t *testing.T) {
	auth := &fakeAuthorizer{grant: pty.AttachGrant{Target: "box-1", UserID: "u1"}}
	delivered := map[uint32][]byte{}
	closed := []uint32{}
	d := tunnel.NewDispatcher(auth, func(streamID uint32, grant pty.AttachGrant) {})
	d.OnData(func(streamID uint32, b []byte) { delivered[streamID] = append(delivered[streamID], b...) })
	d.OnClose(func(streamID uint32) { closed = append(closed, streamID) })

	d.Handle(tunnel.Frame{Type: tunnel.FrameOpen, StreamID: 5, Payload: []byte("good")})
	d.Handle(tunnel.Frame{Type: tunnel.FrameClose, StreamID: 5, Payload: nil})
	assert.Equal(t, []uint32{5}, closed)

	// After close, the stream is forgotten — later data is dropped.
	d.Handle(tunnel.Frame{Type: tunnel.FrameData, StreamID: 5, Payload: []byte("late")})
	assert.Empty(t, delivered[5])
}
