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
	// Killing ffmpeg as a tree rather than a single process is load-bearing:
	// a surviving child keeps stdout's write end open and Wait/Scan would hang
	// past the deadline. The mechanism differs per platform (setpgid+SIGKILL on
	// unix, CREATE_NEW_PROCESS_GROUP+taskkill on windows), so it lives in
	// proc_unix.go / proc_windows.go.
	setProcessGroup(cmd)
	killGroup := func() error { return killProcessTree(cmd) }
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
