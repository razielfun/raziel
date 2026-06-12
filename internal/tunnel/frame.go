// Package tunnel implements raziel-agentd's reverse-tunnel transport (#86): the
// daemon dials a WebSocket OUT to the proxy and multiplexes many logical PTY/exec
// streams over that one connection, so a NAT'd / no-inbound box is reachable with
// zero inbound ports (ADR-0002 d.2/d.9). This file is the wire FRAME codec shared
// by the proxy and the daemon — pure encode/decode, no socket, no //go:build
// linux, so the protocol is unit-testable on any OS.
package tunnel

import (
	"encoding/binary"
	"fmt"
)

// FrameType discriminates the multiplexed messages on the tunnel.
type FrameType uint8

const (
	// FrameOpen opens a new logical stream (a PTY attach); Payload carries the
	// attach token the daemon authorizes via the PtyAuthorizer seam.
	FrameOpen FrameType = 1
	// FrameData carries stream bytes in either direction (terminal I/O).
	FrameData FrameType = 2
	// FrameClose closes a logical stream; Payload may carry a reason.
	FrameClose FrameType = 3
)

// Frame is one multiplexed message on the reverse tunnel.
type Frame struct {
	Type     FrameType
	StreamID uint32
	Payload  []byte
}

// headerLen is type(1) + streamID(4) + payloadLen(4).
const headerLen = 1 + 4 + 4

// EncodeFrame serializes a frame to its wire form:
//
//	[type:1][streamID:4 BE][payloadLen:4 BE][payload:payloadLen]
func EncodeFrame(f Frame) []byte {
	buf := make([]byte, headerLen+len(f.Payload))
	buf[0] = byte(f.Type)
	binary.BigEndian.PutUint32(buf[1:5], f.StreamID)
	binary.BigEndian.PutUint32(buf[5:9], uint32(len(f.Payload)))
	copy(buf[headerLen:], f.Payload)
	return buf
}

// DecodeFrame parses one wire frame. It errors on a short buffer or a
// payload-length that disagrees with the buffer (a truncated/forged frame),
// never panics or over-reads.
func DecodeFrame(b []byte) (Frame, error) {
	if len(b) < headerLen {
		return Frame{}, fmt.Errorf("tunnel: short frame header (%d < %d)", len(b), headerLen)
	}
	payloadLen := binary.BigEndian.Uint32(b[5:9])
	if int(payloadLen) != len(b)-headerLen {
		return Frame{}, fmt.Errorf("tunnel: payload length %d disagrees with buffer %d", payloadLen, len(b)-headerLen)
	}
	payload := make([]byte, payloadLen)
	copy(payload, b[headerLen:])
	return Frame{
		Type:     FrameType(b[0]),
		StreamID: binary.BigEndian.Uint32(b[1:5]),
		Payload:  payload,
	}, nil
}
