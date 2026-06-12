package tunnel_test

import (
	"testing"

	"github.com/raziel-ai/raziel/internal/tunnel"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// #86 D4: the daemon holds ONE reverse tunnel (a WebSocket dialed OUT to the
// proxy) and multiplexes many logical PTY/exec streams over it. Each message is a
// Frame carrying a Type (open/data/close), the StreamID it belongs to, and a
// payload. The proxy and the daemon share this codec, so it must round-trip
// exactly. Pure encode/decode — unit-tested on any OS (no //go:build linux), so
// the framing is provable without a live socket or a Linux PTY.

func TestFrameRoundTripsADataFrame(t *testing.T) {
	f := tunnel.Frame{Type: tunnel.FrameData, StreamID: 7, Payload: []byte("hello box")}

	encoded := tunnel.EncodeFrame(f)
	got, err := tunnel.DecodeFrame(encoded)

	require.NoError(t, err)
	assert.Equal(t, tunnel.FrameData, got.Type)
	assert.Equal(t, uint32(7), got.StreamID)
	assert.Equal(t, []byte("hello box"), got.Payload)
}

func TestFrameRoundTripsAllTypesAndEmptyPayload(t *testing.T) {
	for _, ft := range []tunnel.FrameType{tunnel.FrameOpen, tunnel.FrameData, tunnel.FrameClose} {
		f := tunnel.Frame{Type: ft, StreamID: 1, Payload: nil}
		got, err := tunnel.DecodeFrame(tunnel.EncodeFrame(f))
		require.NoError(t, err)
		assert.Equal(t, ft, got.Type)
		assert.Empty(t, got.Payload) // empty payload round-trips
	}
}

func TestDecodeRejectsAShortHeader(t *testing.T) {
	// A truncated frame (header cut off) must error cleanly — never panic or
	// over-read. A malicious proxy must not be able to crash the daemon.
	_, err := tunnel.DecodeFrame([]byte{0x02, 0x00, 0x00}) // 3 bytes < 9-byte header
	require.Error(t, err)
}

func TestDecodeRejectsAPayloadLengthThatDisagreesWithTheBuffer(t *testing.T) {
	// Forge a frame claiming a 1000-byte payload but supplying none.
	bad := tunnel.EncodeFrame(tunnel.Frame{Type: tunnel.FrameData, StreamID: 1, Payload: nil})
	bad[5], bad[6], bad[7], bad[8] = 0x00, 0x00, 0x03, 0xE8 // payloadLen = 1000
	_, err := tunnel.DecodeFrame(bad)
	require.Error(t, err) // truncated/forged — rejected, not over-read
}
