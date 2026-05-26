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
// Protocol (text frames):
//   - Client → Server: raw stdin bytes OR {"type":"resize","cols":N,"rows":N}
//   - Server → Client: raw stdout/stderr bytes
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
		InsecureSkipVerify: true, // CORS handled by middleware
	})
	if err != nil {
		s.log.Warn("ws accept", zap.Error(err))
		return
	}
	defer conn.CloseNow() //nolint:errcheck

	ctx := conn.CloseRead(context.Background())

	pr, pw := io.Pipe()

	// Forward PTY output to WebSocket
	go func() {
		buf := make([]byte, 4096)
		for {
			n, err := pr.Read(buf)
			if n > 0 {
				if werr := conn.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Forward WebSocket messages to PTY stdin or handle resize
	go func() {
		for {
			_, msg, err := conn.Read(ctx)
			if err != nil {
				pw.Close() //nolint:errcheck
				return
			}
			// Check if it's a resize control frame
			if len(msg) > 0 && msg[0] == '{' {
				var ctrl struct {
					Type string `json:"type"`
					Cols int    `json:"cols"`
					Rows int    `json:"rows"`
				}
				if json.Unmarshal(msg, &ctrl) == nil && ctrl.Type == "resize" {
					// Resize is handled inside RunTTY via SIGWINCH; best-effort ignore here
					continue
				}
			}
			pw.Write(msg) //nolint:errcheck
		}
	}()

	// Run /bin/bash inside the sandbox with a PTY
	exitCode, err := s.sandboxProvider.RunTTY(ctx, sbx, []string{"/bin/bash"}, nil)
	if err != nil {
		s.log.Warn("sandbox ws: RunTTY", zap.String("id", id), zap.Error(err))
	}

	msg, _ := json.Marshal(map[string]any{"type": "exit", "code": exitCode})
	conn.Write(ctx, websocket.MessageText, msg) //nolint:errcheck
	conn.Close(websocket.StatusNormalClosure, "")
}
