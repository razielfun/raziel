package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
	"nhooyr.io/websocket"

	"github.com/raziel-ai/raziel/internal/sandbox"
)

// handleSandboxWs upgrades to WebSocket and attaches to the sandbox's persistent
// PTY session. If no session exists, one is started. Scrollback is replayed on
// attach so the client sees prior output. Multiple clients can attach simultaneously.
//
// Authentication: single-use token in ?token= query param (no Bearer header —
// browsers cannot set arbitrary headers on WebSocket upgrades).
//
// Protocol:
//   - Client → Server binary frame: raw stdin bytes
//   - Client → Server text frame:   {"type":"resize","cols":N,"rows":N}
//   - Server → Client binary frame: raw PTY output (replay + live)
//   - Server → Client text frame:   {"type":"exit","code":N}  (process exited)
func (s *Server) handleSandboxWs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "sandboxID")
	token := r.URL.Query().Get("token")
	tabID := r.URL.Query().Get("tabId")
	if tabID == "" {
		tabID = "0"
	}

	if token == "" {
		jsonBadRequest(w, "token query parameter is required")
		return
	}

	sandboxID, ok := s.wsTokens.Consume(token)
	if !ok {
		jsonError(w, http.StatusUnauthorized, "invalid or expired token", "UNAUTHORIZED", "Request a new token")
		return
	}
	if sandboxID != id {
		jsonError(w, http.StatusForbidden, "token does not match sandbox", "FORBIDDEN", "")
		return
	}

	sbx, err := s.sandboxProvider.Get(id)
	if err != nil {
		jsonNotFound(w)
		return
	}
	if sbx.State != sandbox.StateRunning {
		jsonError(w, http.StatusConflict, "sandbox is not running", "SANDBOX_NOT_RUNNING", "Start the sandbox first")
		return
	}

	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		s.log.Warn("ws accept", zap.Error(err))
		return
	}
	defer conn.CloseNow() //nolint:errcheck

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Get or start the persistent PTY session for this sandbox+tab
	sess, err := s.ptyManager.GetOrStart(id, tabID, sbx.WorkspacePath)
	if err != nil {
		s.log.Error("pty manager GetOrStart", zap.String("id", id), zap.Error(err))
		conn.Close(websocket.StatusInternalError, "failed to start PTY")
		return
	}

	// stdin pipe fed by incoming WS messages
	stdinPr, stdinPw := io.Pipe()
	resizeCh := make(chan [2]uint16, 4)

	// Read WS → stdin / resize channel
	go func() {
		defer stdinPw.Close()
		defer close(resizeCh)
		for {
			msgType, msg, err := conn.Read(ctx)
			if err != nil {
				return
			}
			if msgType == websocket.MessageText {
				var ctrl struct {
					Type string `json:"type"`
					Cols uint16 `json:"cols"`
					Rows uint16 `json:"rows"`
				}
				if len(msg) > 0 && msg[0] == '{' && json.Unmarshal(msg, &ctrl) == nil && ctrl.Type == "resize" && ctrl.Cols > 0 && ctrl.Rows > 0 {
					select {
					case resizeCh <- [2]uint16{ctrl.Cols, ctrl.Rows}:
					default:
					}
					continue
				}
			}
			stdinPw.Write(msg) //nolint:errcheck
		}
	}()

	// Keepalive to prevent Cloudflare 100s idle timeout
	go func() {
		t := time.NewTicker(30 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				conn.Write(ctx, websocket.MessageBinary, []byte{}) //nolint:errcheck
			case <-ctx.Done():
				return
			}
		}
	}()

	// send wraps conn.Write — all PTY output is binary frames
	send := func(chunk []byte) error {
		if len(chunk) == 0 {
			return nil
		}
		return conn.Write(ctx, websocket.MessageBinary, chunk)
	}

	exitCode, err := sess.Attach(ctx, stdinPr, send, resizeCh)
	if err != nil && ctx.Err() == nil {
		s.log.Warn("sandbox ws: attach ended", zap.String("id", id), zap.Error(err))
	}

	// If the PTY process actually exited (not just client disconnect), send exit frame
	if sess.ExitCode() != nil {
		msg, _ := json.Marshal(map[string]any{"type": "exit", "code": exitCode})
		conn.Write(ctx, websocket.MessageText, msg) //nolint:errcheck
	}

	conn.Close(websocket.StatusNormalClosure, "")
}
