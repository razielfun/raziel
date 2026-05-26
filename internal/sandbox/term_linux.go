//go:build linux

package sandbox

import (
	"os"

	"golang.org/x/sys/unix"
)

func setRawMode(f *os.File) (*unix.Termios, error) {
	old, err := unix.IoctlGetTermios(int(f.Fd()), unix.TCGETS)
	if err != nil {
		return nil, err
	}
	raw := *old
	raw.Iflag &^= unix.IGNBRK | unix.BRKINT | unix.PARMRK | unix.ISTRIP | unix.INLCR | unix.IGNCR | unix.ICRNL | unix.IXON
	raw.Oflag &^= unix.OPOST
	raw.Lflag &^= unix.ECHO | unix.ECHONL | unix.ICANON | unix.ISIG | unix.IEXTEN
	raw.Cflag &^= unix.CSIZE | unix.PARENB
	raw.Cflag |= unix.CS8
	raw.Cc[unix.VMIN] = 1
	raw.Cc[unix.VTIME] = 0
	unix.IoctlSetTermios(int(f.Fd()), unix.TCSETS, &raw) //nolint:errcheck
	return old, nil
}

func restoreMode(f *os.File, state *unix.Termios) {
	unix.IoctlSetTermios(int(f.Fd()), unix.TCSETS, state) //nolint:errcheck
}
