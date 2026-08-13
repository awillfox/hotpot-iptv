package ffmpeg

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunOK(t *testing.T) {
	r := Runner{FFmpegPath: "testdata/fake_ffmpeg.sh", StallTimeout: 5 * time.Second}
	var progress []int64
	err := r.Run(context.Background(), []string{"ok"}, RunOpts{
		OnProgress: func(us int64) { progress = append(progress, us) },
	})
	require.NoError(t, err)
	assert.Equal(t, []int64{1000000, 2000000, 3000000}, progress)
}

func TestRunFail(t *testing.T) {
	r := Runner{FFmpegPath: "testdata/fake_ffmpeg.sh", StallTimeout: 5 * time.Second}
	err := r.Run(context.Background(), []string{"fail"}, RunOpts{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "broken input")
}

func TestRunStall(t *testing.T) {
	r := Runner{FFmpegPath: "testdata/fake_ffmpeg.sh", StallTimeout: 300 * time.Millisecond}
	start := time.Now()
	err := r.Run(context.Background(), []string{"stall"}, RunOpts{})
	require.ErrorIs(t, err, ErrStalled)
	assert.Less(t, time.Since(start), 5*time.Second)
}

func TestRunContextCancel(t *testing.T) {
	r := Runner{FFmpegPath: "testdata/fake_ffmpeg.sh", StallTimeout: time.Minute}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	err := r.Run(ctx, []string{"stall"}, RunOpts{DisableStallWatch: true})
	require.Error(t, err) // killed by context
}

// ffmpeg is killed as a process tree, not just as a single process: a surviving
// child inherits stdout and would keep its write end open, hanging the reader
// past the deadline. Characterises the behaviour the process-group setup gives
// us, so the platform split below cannot quietly lose it.
func TestRunKillsChildProcessesToo(t *testing.T) {
	r := Runner{FFmpegPath: "testdata/fake_ffmpeg_child.sh", StallTimeout: time.Minute}
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- r.Run(ctx, []string{"child"}, RunOpts{DisableStallWatch: true}) }()

	select {
	case err := <-done:
		require.Error(t, err, "killed by context")
	case <-time.After(10 * time.Second):
		t.Fatal("Run hung: a surviving child still holds stdout open")
	}
}
