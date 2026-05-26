package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/go-chi/chi/v5"
	"go.uber.org/zap"
	"nhooyr.io/websocket"

	"github.com/raziel-ai/raziel/internal/sandbox"
)

// handleSandboxWs upgrades to WebSocket and attaches a PTY to the sandbox.
// The caller must provide a short-lived token issued by handleRegisterWsToken.
//
// Protocol (binary frames):
//   - Client → Server: raw stdin bytes OR {"type":"resize","cols":N,"rows":N} (text frame)
//   - Server → Client: raw stdout/stderr bytes (binary frames)
func (s *Server) handleSandboxWs(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "sandboxID")
	token := r.URL.Query().Get("token")

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

	// stdin pipe: WS → PTY
	pr, pw := io.Pipe()

	// stdout pipe: PTY → WS
	outPr, outPw := io.Pipe()

	// resize channel
	resizeCh := make(chan [2]uint16, 4)

	// Forward PTY output to WebSocket (binary frames)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := outPr.Read(buf)
			if n > 0 {
				if werr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					cancel()
					return
				}
			}
			if err != nil {
				cancel()
				return
			}
		}
	}()

	// Forward WebSocket messages → PTY stdin or resize channel
	go func() {
		defer pw.Close()
		defer close(resizeCh)
		for {
			msgType, msg, err := conn.Read(ctx)
			if err != nil {
				return
			}
			// Text frames: try JSON control (resize), otherwise treat as stdin
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
			// Binary frames and non-JSON text frames = stdin
			pw.Write(msg) //nolint:errcheck
		}
	}()

	exitCode, err := s.sandboxProvider.RunTTY(ctx, sbx, []string{"/bin/bash"}, nil, pr, outPw, resizeCh)
	outPw.Close()
	if err != nil {
		s.log.Warn("sandbox ws: RunTTY", zap.String("id", id), zap.Error(err))
	}

	msg, _ := json.Marshal(map[string]any{"type": "exit", "code": exitCode})
	conn.Write(ctx, websocket.MessageText, msg) //nolint:errcheck
	conn.Close(websocket.StatusNormalClosure, "")
}
