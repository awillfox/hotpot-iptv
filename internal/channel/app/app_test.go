package app_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"hotpot-iptv/internal/channel/app"
	"hotpot-iptv/internal/channel/app/command"
	"hotpot-iptv/internal/channel/domain/channel"
	"hotpot-iptv/internal/ffmpeg"
	"hotpot-iptv/pkg/testdb"
)

type fakeProber struct{ calls int }

func (f *fakeProber) Probe(_ context.Context, _ string) (ffmpeg.ProbeResult, error) {
	f.calls++
	return ffmpeg.ProbeResult{
		DurationMs: 60000, VideoCodec: "h264", Width: 1280, Height: 720,
		Audio: []ffmpeg.AudioTrack{{Index: 0, Lang: "eng", Codec: "aac", Channels: 2}},
	}, nil
}

func TestChannelLifecycle(t *testing.T) {
	pool := testdb.New(t)
	media := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(media, "a.mkv"), []byte("x"), 0o644))

	prober := &fakeProber{}
	a := app.NewApplication(pool, prober, media)
	ctx := context.Background()

	ch, err := a.Commands.Create.Handle(ctx, command.CreateInput{Name: "Movies HD", Number: 1})
	require.NoError(t, err)
	assert.Equal(t, "movies-hd", ch.Slug)       // auto-slugified
	assert.Equal(t, int32(1920), ch.VideoWidth) // defaults applied

	items, err := a.Commands.SetPlaylist.Handle(ctx, command.SetPlaylistInput{
		ChannelID: ch.ID, Paths: []string{"a.mkv"},
	})
	require.NoError(t, err)
	require.Len(t, items, 1)
	assert.Equal(t, 1, prober.calls)

	// Same file again: probe cache hit, no re-probe.
	_, err = a.Commands.SetPlaylist.Handle(ctx, command.SetPlaylistInput{
		ChannelID: ch.ID, Paths: []string{"a.mkv"},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, prober.calls)

	// Missing file rejected.
	_, err = a.Commands.SetPlaylist.Handle(ctx, command.SetPlaylistInput{
		ChannelID: ch.ID, Paths: []string{"nope.mkv"},
	})
	require.Error(t, err)

	// Traversal rejected.
	_, err = a.Commands.SetPlaylist.Handle(ctx, command.SetPlaylistInput{
		ChannelID: ch.ID, Paths: []string{"../etc/passwd"},
	})
	require.Error(t, err)

	require.NoError(t, a.Commands.Delete.Handle(ctx, ch.ID))
	_, err = a.Queries.Get.Handle(ctx, ch.ID)
	assert.ErrorIs(t, err, channel.ErrNotFound)
}
