//go:build windows

package ffmpeg

import (
	"fmt"
	"os/exec"
	"strconv"
	"syscall"
)

// setProcessGroup gives ffmpeg its own process group, the Windows counterpart
// of setpgid: it is what lets killProcessTree address the whole tree rather
// than only the process we launched.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
}

// killProcessTree terminates ffmpeg and everything it spawned. Windows has no
// "signal the process group" call, so this shells out to taskkill /T, which
// walks the parent/child tree. A surviving child would keep stdout's write end
// open and hang the reader, which is the whole point of killing the tree.
//
// Falls back to killing just the process if taskkill is unavailable, which is
// still better than leaving ffmpeg running.
func killProcessTree(cmd *exec.Cmd) error {
	pid := strconv.Itoa(cmd.Process.Pid)
	if err := exec.Command("taskkill", "/T", "/F", "/PID", pid).Run(); err != nil {
		if killErr := cmd.Process.Kill(); killErr != nil {
			return fmt.Errorf("taskkill: %w (direct kill also failed: %v)", err, killErr)
		}
	}
	return nil
}
