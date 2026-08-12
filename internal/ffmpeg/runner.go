package ffmpeg

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

var ErrStalled = errors.New("ffmpeg stalled: no progress")

type Runner struct {
	FFmpegPath   string
	StallTimeout time.Duration
}

type RunOpts struct {
	OnProgress        func(outTimeUs int64)
	DisableStallWatch bool
}

// tailWriter keeps the last cap bytes written.
type tailWriter struct {
	buf []byte
	cap int
}

func (t *tailWriter) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > t.cap {
		t.buf = t.buf[len(t.buf)-t.cap:]
	}
	return len(p), nil
}

func (r Runner) Run(ctx context.Context, args []string, opts RunOpts) error {
	cmd := exec.CommandContext(ctx, r.FFmpegPath, args...)
	// Run ffmpeg in its own process group so a kill also reaches any
	// child processes it spawns (e.g. a shell script's external
	// commands); otherwise those children can keep stdout's write end
	// open and Wait/Scan would hang past the deadline.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	killGroup := func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
	cmd.Cancel = killGroup
	stderr := &tailWriter{cap: 4096}
	cmd.Stderr = stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", r.FFmpegPath, err)
	}

	var stalled atomic.Bool
	var watchdog *time.Timer
	if !opts.DisableStallWatch {
		watchdog = time.AfterFunc(r.StallTimeout, func() {
			stalled.Store(true)
			_ = killGroup()
		})
		defer watchdog.Stop()
	}

	sc := bufio.NewScanner(stdout)
	for sc.Scan() {
		line := sc.Text()
		if v, ok := strings.CutPrefix(line, "out_time_us="); ok {
			if watchdog != nil {
				watchdog.Reset(r.StallTimeout)
			}
			if us, err := strconv.ParseInt(v, 10, 64); err == nil && opts.OnProgress != nil {
				opts.OnProgress(us)
			}
		}
	}

	err = cmd.Wait()
	if stalled.Load() {
		return fmt.Errorf("%w (last stderr: %s)", ErrStalled, string(stderr.buf))
	}
	if err != nil {
		return fmt.Errorf("ffmpeg exited: %w (last stderr: %s)", err, string(stderr.buf))
	}
	return nil
}
