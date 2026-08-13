//go:build !windows

package ffmpeg

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts ffmpeg in its own process group so a kill also reaches
// any child processes it spawns (e.g. a shell wrapper's external commands);
// otherwise those children can keep stdout's write end open and Wait/Scan would
// hang past the deadline.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// killProcessTree kills the whole group. The negative pid is what makes the
// signal apply to the group rather than just the leader.
func killProcessTree(cmd *exec.Cmd) error {
	return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}
