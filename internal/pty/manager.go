//go:build linux

package pty

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
)

const (
	scrollbackBytes = 64 * 1024 // 64 KB ring buffer per session
	readBufSize     = 4096
)

// validUsername keeps the synthetic /etc/passwd entry safe; falls back to "user".
var (
	validUsername   = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)
	invalidNameChar = regexp.MustCompile(`[^a-z0-9_-]`)
)

func sanitizeUsername(name string) string {
	n := strings.ToLower(strings.TrimSpace(name))
	n = invalidNameChar.ReplaceAllString(n, "-")
	n = strings.TrimLeft(n, "-")
	if n == "" || !validUsername.MatchString(n) {
		return "user"
	}
	return n
}

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

// Manager owns all active PTY sessions keyed by "sandboxID:tabID".
// Each tab in the UI maps to an independent bash process sharing the
// same sandbox filesystem.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*Session // key = sandboxID + ":" + tabID
}

func NewManager() *Manager {
	return &Manager{sessions: make(map[string]*Session)}
}

func sessionKey(sandboxID, tabID string) string {
	return sandboxID + ":" + tabID
}

// GetOrStart returns an existing session for the given sandbox+tab pair,
// or starts a new independent process if none exists.
// agent, envVars, prompt, and started are only used when starting a new session.
// sessionID (== sandboxID) is the agent conversation id; started reports whether
// the agent has launched before, so a restart resumes instead of starting fresh.
func (m *Manager) GetOrStart(sandboxID, tabID, workDir, agent string, envVars map[string]string, prompt string, started bool, username string) (*Session, error) {
	key := sessionKey(sandboxID, tabID)
	m.mu.Lock()
	defer m.mu.Unlock()

	if s, ok := m.sessions[key]; ok {
		s.mu.Lock()
		alive := s.exitCode == nil
		s.mu.Unlock()
		if alive {
			return s, nil
		}
		// stale — remove and restart (scrollback preserved in s.scrollback
		// even after exit, so we keep the old session for log replay and
		// only start a new process if the caller explicitly wants one)
		delete(m.sessions, key)
	}

	s, err := startSession(sandboxID, tabID, workDir, agent, envVars, prompt, started, username)
	if err != nil {
		return nil, err
	}
	m.sessions[key] = s

	// Intentionally NOT deleted from the map on exit: the session is kept so its
	// scrollback stays retrievable via GetScrollback after the process exits —
	// e.g. a fast crash ("claude: command not found") whose error output the UI
	// fetches only after seeing the exit frame. A dead session is evicted lazily
	// in the stale-restart branch above, or eagerly by Stop on sandbox destroy.

	return s, nil
}

// GetScrollback returns the current scrollback buffer for a sandbox+tab.
// Works even after the PTY process has exited — the Session is retained in the
// map past exit (until restart or sandbox destroy) with its scrollback intact.
// Returns nil if no session has ever been started for this key.
func (m *Manager) GetScrollback(sandboxID, tabID string) []byte {
	key := sessionKey(sandboxID, tabID)
	m.mu.Lock()
	s, ok := m.sessions[key]
	m.mu.Unlock()
	if !ok {
		return nil
	}
	return s.Scrollback()
}

// Stop kills all PTY sessions belonging to a sandbox (called on sandbox destroy).
func (m *Manager) Stop(sandboxID string) {
	prefix := sandboxID + ":"
	m.mu.Lock()
	var toKill []*Session
	for key, s := range m.sessions {
		if len(key) > len(prefix) && key[:len(prefix)] == prefix {
			toKill = append(toKill, s)
			delete(m.sessions, key)
		}
	}
	m.mu.Unlock()
	for _, s := range toKill {
		s.ptmx.Close()
	}
}

// agentCmd returns the command+args to run for a given agent identifier.
// Extra tabs (tabID != "0") always get bash regardless of agent.
//
// sessionID equals the sandbox ID and is used as Claude Code's --session-id (a
// caller-supplied UUID). started reports whether the agent has been launched
// before in this sandbox: on first launch we start a new conversation (and pass
// the prompt as an argument); on later launches we resume the prior one.
func agentCmd(agent, sessionID, prompt string, started bool) []string {
	switch agent {
	case "claude-code":
		if started {
			return []string{"claude", "--resume", sessionID, "--dangerously-skip-permissions"}
		}
		c := []string{"claude", "--session-id", sessionID, "--dangerously-skip-permissions"}
		if prompt != "" {
			c = append(c, prompt)
		}
		return c
	case "codex":
		if started {
			return []string{"codex", "resume", "--last", "--dangerously-bypass-approvals-and-sandbox"}
		}
		c := []string{"codex", "--dangerously-bypass-approvals-and-sandbox"}
		if prompt != "" {
			c = append(c, prompt)
		}
		return c
	case "opencode":
		if started {
			return []string{"opencode", "--continue"}
		}
		c := []string{"opencode"}
		if prompt != "" {
			c = append(c, "--prompt", prompt)
		}
		return c
	default:
		return []string{"/bin/bash"}
	}
}

func startSession(sandboxID, tabID, workDir, agent string, envVars map[string]string, prompt string, started bool, username string) (*Session, error) {
	su, err := setupSandboxUser(workDir, username)
	if err != nil {
		return nil, fmt.Errorf("setup sandbox user: %w", err)
	}

	cmd := agentCmd(agent, sandboxID, prompt, started)
	args := bwrapArgs(workDir, su)
	args = append(args, cmd...)

	c := exec.Command("bwrap", args...)
	c.Dir = workDir
	home := "/home/" + su.name
	baseEnv := []string{
		"HOME=" + home,
		"USER=" + su.name,
		"LOGNAME=" + su.name,
		"TMPDIR=/tmp",
		// HOME-based prefixes so the non-root user can install global packages
		// (npm -g, pip --user, etc.) into its own writable HOME without root.
		"NPM_CONFIG_PREFIX=" + home + "/.npm-global",
		"PATH=" + home + "/.npm-global/bin:" + home + "/.local/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"TERM=xterm-256color",
		"RAZIEL_SANDBOX=" + sandboxID,
		"RAZIEL_TAB=" + tabID,
		"RAZIEL_WORKSPACE=" + workDir,
	}
	for k, v := range envVars {
		baseEnv = append(baseEnv, k+"="+v)
	}
	c.Env = baseEnv

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

// sandboxUID/GID is the in-namespace uid the sandbox runs as. bwrap maps it to
// host root (single-entry map "1000 0 1"), so host-root-owned binds (workspace,
// home) appear owned by 1000 inside and are fully writable — no host chown
// needed. Being non-root (uid != 0) means agents like Claude Code launch
// without the IS_SANDBOX escape hatch, while still able to install into their
// HOME-based tool prefix.
const (
	sandboxUID = 1000
	sandboxGID = 1000
)

// sandboxUser holds the resolved identity + paths for a sandbox's non-root user.
type sandboxUser struct {
	name   string
	home   string // host path backing the in-sandbox /home/<name>
	etcDir string // host dir holding synthetic passwd/group/hosts
}

// setupSandboxUser prepares the sandbox identity: the process runs as a real
// non-root user (uid 1000) inside a user namespace, mapped to host root so the
// root-owned workspace/home binds are writable. A synthetic /etc/passwd maps
// the Clerk username to uid 1000 (so whoami/$USER show it); /etc/hosts sets the
// hostname "raziel". Idempotent across restarts.
func setupSandboxUser(workDir, rawName string) (sandboxUser, error) {
	name := sanitizeUsername(rawName)
	sandboxDir := filepath.Dir(workDir) // ~/.raziel/sandboxes/<id>
	etcDir := filepath.Join(sandboxDir, ".raziel-etc")
	home := filepath.Join(sandboxDir, "home")

	if err := os.MkdirAll(etcDir, 0o755); err != nil {
		return sandboxUser{}, fmt.Errorf("etc dir: %w", err)
	}
	if err := os.MkdirAll(home, 0o755); err != nil {
		return sandboxUser{}, fmt.Errorf("home dir: %w", err)
	}

	passwd := fmt.Sprintf("root:x:0:0:root:/root:/bin/bash\n%s:x:%d:%d:%s:/home/%s:/bin/bash\n",
		name, sandboxUID, sandboxGID, name, name)
	group := fmt.Sprintf("root:x:0:\n%s:x:%d:\n", name, sandboxGID)
	hosts := "127.0.0.1 localhost raziel\n::1 localhost raziel\n"

	writes := []struct {
		path string
		data string
		mode os.FileMode
	}{
		{filepath.Join(etcDir, "passwd"), passwd, 0o644},
		{filepath.Join(etcDir, "group"), group, 0o644},
		{filepath.Join(etcDir, "hosts"), hosts, 0o644},
	}
	for _, wf := range writes {
		if err := os.WriteFile(wf.path, []byte(wf.data), wf.mode); err != nil {
			return sandboxUser{}, fmt.Errorf("write %s: %w", wf.path, err)
		}
	}

	return sandboxUser{name: name, home: home, etcDir: etcDir}, nil
}

func bwrapArgs(workDir string, su sandboxUser) []string {
	args := []string{
		"--ro-bind", "/usr", "/usr",
		"--ro-bind", "/lib", "/lib",
		"--ro-bind", "/lib64", "/lib64",
		"--ro-bind", "/bin", "/bin",
		"--ro-bind", "/sbin", "/sbin",
		// /etc essentials + synthetic identity files.
		"--ro-bind-try", "/etc/resolv.conf", "/etc/resolv.conf",
		"--ro-bind-try", "/etc/ssl", "/etc/ssl",
		"--ro-bind-try", "/etc/ca-certificates", "/etc/ca-certificates",
		"--ro-bind", filepath.Join(su.etcDir, "passwd"), "/etc/passwd",
		"--ro-bind", filepath.Join(su.etcDir, "group"), "/etc/group",
		"--ro-bind", filepath.Join(su.etcDir, "hosts"), "/etc/hosts",
		"--bind", workDir, workDir,
		"--bind", su.home, "/home/" + su.name,
		"--proc", "/proc",
		"--dev", "/dev",
		"--tmpfs", "/tmp",
		"--unshare-pid",
		"--unshare-uts",
		"--hostname", "raziel",
		// Non-root user namespace: in-ns uid 1000 maps to host root, so the
		// root-owned workspace/home binds are writable while the process is
		// genuinely non-root (Claude launches without IS_SANDBOX).
		"--unshare-user",
		"--uid", fmt.Sprintf("%d", sandboxUID),
		"--gid", fmt.Sprintf("%d", sandboxGID),
		"--die-with-parent",
		"--chdir", workDir,
	}
	return args
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
