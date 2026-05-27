//go:build linux

package pty

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/creack/pty"
)

const (
	scrollbackBytes = 64 * 1024 // 64 KB ring buffer per session
	readBufSize     = 4096
)

// Subscriber is a channel that receives PTY output chunks.
type Subscriber chan []byte

// Session is a persistent PTY process for one sandbox. It survives WebSocket
// disconnects — new connections replay the scrollback then receive live output.
type Session struct {
	mu          sync.Mutex
	ptmx        *os.File
	cmd         *os.File // unused after start, kept for close
	scrollback  []byte   // ring-ish: we just append and trim to cap
	subscribers map[Subscriber]struct{}
	exitCode    *int  // non-nil once process exits
	exitOnce    sync.Once
	exitCh      chan struct{} // closed when process exits
}

// Manager owns all active PTY sessions keyed by sandbox ID.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session
}

func NewManager() *Manager {
	return &Manager{sessions: make(map[string]*Session)}
}

// GetOrStart returns an existing session or starts a new one.
func (m *Manager) GetOrStart(sandboxID, workDir string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if s, ok := m.sessions[sandboxID]; ok {
		s.mu.Lock()
		alive := s.exitCode == nil
		s.mu.Unlock()
		if alive {
			return s, nil
		}
		// stale session — remove and restart
		delete(m.sessions, sandboxID)
	}

	s, err := startSession(sandboxID, workDir)
	if err != nil {
		return nil, err
	}
	m.sessions[sandboxID] = s

	// Remove from map when process exits
	go func() {
		<-s.exitCh
		m.mu.Lock()
		if m.sessions[sandboxID] == s {
			delete(m.sessions, sandboxID)
		}
		m.mu.Unlock()
	}()

	return s, nil
}

// Stop kills the session for a sandbox (called on sandbox destroy).
func (m *Manager) Stop(sandboxID string) {
	m.mu.Lock()
	s, ok := m.sessions[sandboxID]
	if ok {
		delete(m.sessions, sandboxID)
	}
	m.mu.Unlock()
	if ok {
		s.ptmx.Close()
	}
}

func startSession(sandboxID, workDir string) (*Session, error) {
	args := bwrapArgs(workDir)
	args = append(args, "/bin/bash")

	c := exec.Command("bwrap", args...)
	c.Dir = workDir
	c.Env = []string{
		"HOME=" + workDir,
		"TMPDIR=/tmp",
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"TERM=xterm-256color",
		"RAZIEL_SANDBOX=" + sandboxID,
		"RAZIEL_WORKSPACE=" + workDir,
	}

	ptmx, err := pty.Start(c)
	if err != nil {
		return nil, fmt.Errorf("pty start: %w", err)
	}

	s := &Session{
		ptmx:        ptmx,
		subscribers: make(map[Subscriber]struct{}),
		exitCh:      make(chan struct{}),
	}

	// Reader goroutine: write PTY output to scrollback + all subscribers
	go func() {
		buf := make([]byte, readBufSize)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				s.broadcast(chunk)
			}
			if err != nil {
				break
			}
		}
		// Process exited — collect exit code
		code := 0
		if werr := c.Wait(); werr != nil {
			if exit, ok := werr.(*exec.ExitError); ok {
				code = exit.ExitCode()
			}
		}
		s.exitOnce.Do(func() {
			s.mu.Lock()
			s.exitCode = &code
			// Close all subscriber channels so Attach loops unblock
			for sub := range s.subscribers {
				close(sub)
			}
			s.subscribers = make(map[Subscriber]struct{})
			s.mu.Unlock()
			close(s.exitCh)
		})
	}()

	return s, nil
}

// Write sends data to the PTY stdin (keyboard input from the browser).
func (s *Session) Write(p []byte) (int, error) {
	return s.ptmx.Write(p)
}

// Resize updates the PTY window size.
func (s *Session) Resize(cols, rows uint16) {
	pty.Setsize(s.ptmx, &pty.Winsize{Cols: cols, Rows: rows}) //nolint:errcheck
}

// Scrollback returns a copy of the current scrollback buffer.
func (s *Session) Scrollback() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]byte, len(s.scrollback))
	copy(out, s.scrollback)
	return out
}

// Subscribe adds a subscriber and returns a channel that will receive output
// chunks. The caller must call Unsubscribe when done.
func (s *Session) Subscribe() Subscriber {
	ch := make(Subscriber, 64)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.exitCode != nil {
		close(ch)
		return ch
	}
	s.subscribers[ch] = struct{}{}
	return ch
}

// Unsubscribe removes a subscriber.
func (s *Session) Unsubscribe(ch Subscriber) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.subscribers, ch)
}

// ExitCh returns a channel closed when the PTY process exits.
func (s *Session) ExitCh() <-chan struct{} {
	return s.exitCh
}

// ExitCode returns the exit code once the process has exited (nil if still running).
func (s *Session) ExitCode() *int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.exitCode
}

func (s *Session) broadcast(chunk []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Append to scrollback, trim if over cap
	s.scrollback = append(s.scrollback, chunk...)
	if len(s.scrollback) > scrollbackBytes {
		s.scrollback = s.scrollback[len(s.scrollback)-scrollbackBytes:]
	}

	// Fan out to all subscribers (non-blocking — drop if subscriber is slow)
	for sub := range s.subscribers {
		select {
		case sub <- chunk:
		default:
		}
	}
}

// Attach replays scrollback to w, then streams live output until ctx is done
// or the PTY process exits. stdin is read and forwarded to the PTY.
func (s *Session) Attach(ctx context.Context, stdin io.Reader, send func([]byte) error, resize <-chan [2]uint16) (int, error) {
	// Replay scrollback
	if sb := s.Scrollback(); len(sb) > 0 {
		if err := send(sb); err != nil {
			return 0, err
		}
	}

	sub := s.Subscribe()
	defer s.Unsubscribe(sub)

	// Forward stdin → PTY
	go func() {
		buf := make([]byte, readBufSize)
		for {
			n, err := stdin.Read(buf)
			if n > 0 {
				s.Write(buf[:n]) //nolint:errcheck
			}
			if err != nil {
				return
			}
		}
	}()

	// Forward resize events
	go func() {
		for {
			select {
			case sz, ok := <-resize:
				if !ok {
					return
				}
				s.Resize(sz[0], sz[1])
			case <-ctx.Done():
				return
			}
		}
	}()

	// Stream output until disconnect or process exit
	for {
		select {
		case chunk, ok := <-sub:
			if !ok {
				// channel closed = process exited; exit code sent as last frame
				if code := s.ExitCode(); code != nil {
					return *code, nil
				}
				return 0, nil
			}
			if err := send(chunk); err != nil {
				return 0, err
			}
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
}

func bwrapArgs(workDir string) []string {
	return []string{
		"--ro-bind", "/usr", "/usr",
		"--ro-bind", "/lib", "/lib",
		"--ro-bind", "/lib64", "/lib64",
		"--ro-bind", "/bin", "/bin",
		"--ro-bind", "/sbin", "/sbin",
		"--bind", workDir, workDir,
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
		"--unshare-pid",
		"--die-with-parent",
		"--chdir", workDir,
	}
}

// keepalive sends a no-op to prevent Cloudflare from closing idle connections.
func KeepAlive(ctx context.Context, interval time.Duration, send func([]byte) error) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-t.C:
			// empty binary frame = no-op for xterm.js
			send([]byte{}) //nolint:errcheck
		case <-ctx.Done():
			return
		}
	}
}
