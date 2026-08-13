package engine

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type memLoader struct{ spec ChannelSpec }

func (m memLoader) Load(context.Context, int32) (ChannelSpec, int32, error) {
	return m.spec, 0, nil
}

func (m memLoader) RunningChannelIDs(context.Context) ([]int32, error) { return nil, nil }

func (m memLoader) SourceFor(context.Context, int32) (ItemSource, time.Duration, bool) {
	return nil, 0, false // hand-picked playlist
}

func TestSupervisorStartStop(t *testing.T) {
	setItemFloor(t, 10*time.Millisecond)
	streams := t.TempDir()
	spec := testSpec(t, streams)
	sup := NewSupervisor(memLoader{spec: spec}, &memStore{}, &fakeProc{})
	t.Cleanup(sup.StopAll)

	require.NoError(t, sup.Start(context.Background(), 1))
	assert.Error(t, sup.Start(context.Background(), 1), "double start must error")

	waitFor(t, 5*time.Second, func() bool {
		mgr, ok := sup.ManagerFor("movies")
		if !ok {
			return false
		}
		pl, _ := mgr.RenderMedia("v")
		return strings.Contains(pl, "v_0.ts")
	})

	st, ok := sup.Status(1)
	require.True(t, ok)
	assert.Equal(t, "running", st.State)
	assert.Equal(t, "movies", st.Slug)
	assert.NotEmpty(t, st.NowPlaying)

	require.NoError(t, sup.Stop(1))
	_, ok = sup.Status(1)
	assert.False(t, ok)
	_, ok = sup.ManagerFor("movies")
	assert.False(t, ok)

	assert.Error(t, sup.Stop(1), "stop when not running must error")
}
