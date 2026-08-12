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
