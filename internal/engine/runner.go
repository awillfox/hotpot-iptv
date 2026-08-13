// Package engine runs channels: one Runner goroutine per running channel.
package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"hotpot-iptv/internal/ffmpeg"
	"hotpot-iptv/internal/hls"
)

type Item struct {
	Path  string
	Abs   string
	Probe ffmpeg.ProbeResult
}

type ChannelSpec struct {
	ID          int32
	Slug        string
	Items       []Item
	Video       ffmpeg.VideoSettings
	SegmentSec  int
	Window      int
	StreamsPath string
}

type Store interface {
	SaveState(ctx context.Context, channelID, itemPos int32, startedAt time.Time, status, lastErr string) error
	LogEvent(ctx context.Context, channelID int32, level, message string)
}

type ProcessRunner interface {
	Run(ctx context.Context, args []string, opts ffmpeg.RunOpts) error
}

const (
	maxConsecutiveFailures = 5
	backoffBase            = time.Minute
	backoffCap             = 5 * time.Minute
	tailInterval           = 200 * time.Millisecond
)

// minItemInterval floors how fast the runner may cycle items. Real ffmpeg paces
// itself with -re, so an item occupies its own duration and this never engages
// during normal playback. It exists for the pathological case: an input that
// ffmpeg exits on immediately (0-duration, or undecodable but still exit 0)
// turns the loop into a hot spin — measured at ~2600 items/sec, each iteration
// creating an output directory. A var, not a const, so tests can shorten it.
var minItemInterval = time.Second

type Runner struct {
	spec     ChannelSpec
	startPos int32
	store    Store
	proc     ProcessRunner
	mgr      *hls.Manager

	itemSeq    int64
	nowPlaying atomic.Value // string
	offsetUs   atomic.Int64
	pos        atomic.Int32
}

func NewRunner(spec ChannelSpec, startPos int32, store Store, proc ProcessRunner) *Runner {
	probes := make([]ffmpeg.ProbeResult, 0, len(spec.Items))
	for _, it := range spec.Items {
		probes = append(probes, it.Probe)
	}
	rends := hls.ComputeRenditions(probes)
	mgr := hls.NewManager(rends, spec.SegmentSec, spec.Window, hls.VideoParams{
		Width: spec.Video.Width, Height: spec.Video.Height, BitrateK: spec.Video.BitrateK,
	})
	r := &Runner{spec: spec, startPos: startPos, store: store, proc: proc, mgr: mgr}
	r.nowPlaying.Store("")
	return r
}

func (r *Runner) Manager() *hls.Manager { return r.mgr }

func (r *Runner) NowPlaying() (string, int64, int32) {
	return r.nowPlaying.Load().(string), r.offsetUs.Load(), r.pos.Load()
}

// Run loops the playlist until ctx is cancelled, persisting position and
// applying the retry/skip/backoff error policy.
func (r *Runner) Run(ctx context.Context) {
	pos := r.startPos
	if int(pos) >= len(r.spec.Items) || pos < 0 {
		pos = 0
	}
	consecFails := 0
	backoff := backoffBase

	for ctx.Err() == nil {
		iterStart := time.Now()
		item := r.spec.Items[pos]
		r.pos.Store(pos)
		r.nowPlaying.Store(item.Path)
		_ = r.store.SaveState(ctx, r.spec.ID, pos, time.Now(), "running", "")

		err := r.playItem(ctx, item)
		if err != nil && ctx.Err() == nil {
			r.store.LogEvent(ctx, r.spec.ID, "warn", fmt.Sprintf("item %s failed, retrying: %v", item.Path, err))
			err = r.playItem(ctx, item)
		}
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			consecFails++
			r.store.LogEvent(ctx, r.spec.ID, "error", fmt.Sprintf("item %s skipped: %v", item.Path, err))
			if consecFails >= maxConsecutiveFailures {
				_ = r.store.SaveState(ctx, r.spec.ID, pos, time.Time{}, "error", err.Error())
				select {
				case <-ctx.Done():
				case <-time.After(backoff):
				}
				backoff *= 2
				if backoff > backoffCap {
					backoff = backoffCap
				}
				consecFails = 0
				continue // retry from the same position after backoff
			}
		} else {
			consecFails = 0
			backoff = backoffBase
		}
		// The backoff branch above continues past this, having already slept a
		// minute or more.
		if d := time.Since(iterStart); d < minItemInterval {
			select {
			case <-ctx.Done():
			case <-time.After(minItemInterval - d):
			}
		}
		pos = (pos + 1) % int32(len(r.spec.Items))
	}
	_ = r.store.SaveState(context.Background(), r.spec.ID, pos, time.Time{}, "stopped", "")
}

func (r *Runner) playItem(ctx context.Context, item Item) error {
	r.itemSeq++
	dirName := fmt.Sprintf("%06d", r.itemSeq)
	outDir := filepath.Join(r.spec.StreamsPath, r.spec.Slug, dirName)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", outDir, err)
	}
	// Safe here: this dir is newer than anything on air, so it is never a candidate.
	r.sweepStaleItemDirs()

	rends := r.mgr.Renditions()
	trackMap := hls.MapTracks(rends, item.Probe)

	// 1. Subtitles first: extract + split before the encode starts.
	segDur := time.Duration(r.spec.SegmentSec) * time.Second
	total := time.Duration(item.Probe.DurationMs) * time.Millisecond
	subSegs := map[string][]string{} // rendition key -> segment file names
	for _, rend := range rends {
		if rend.Kind != hls.KindSubs {
			continue
		}
		var cues []hls.Cue
		if idx := trackMap[rend.Key]; idx >= 0 {
			vttPath := filepath.Join(outDir, rend.Key+".vtt")
			args := ffmpeg.BuildSubExtractArgs(item.Abs, item.Probe.Subs[idx], vttPath)
			if err := r.proc.Run(ctx, args, ffmpeg.RunOpts{DisableStallWatch: true}); err != nil {
				r.store.LogEvent(ctx, r.spec.ID, "warn",
					fmt.Sprintf("subtitle extract failed for %s/%s: %v", item.Path, rend.Key, err))
			} else if f, err := os.Open(vttPath); err == nil {
				cues, _ = hls.ParseVTT(f)
				f.Close()
			}
		}
		bodies := hls.SplitVTT(cues, segDur, total)
		names := make([]string, 0, len(bodies))
		for i, body := range bodies {
			name := fmt.Sprintf("%s_%d.vtt", rend.Key, i)
			if err := os.WriteFile(filepath.Join(outDir, name), []byte(body), 0o644); err != nil {
				return fmt.Errorf("write vtt segment: %w", err)
			}
			names = append(names, name)
		}
		subSegs[rend.Key] = names
	}

	// 2. New file boundary.
	r.mgr.MarkDiscontinuity()

	// 3. Tail CSV lists → append segments live; subs advance in lockstep with video.
	tailCtx, stopTails := context.WithCancel(ctx)
	tailsDone := make(chan struct{}, len(rends))
	tails := 0
	videoSegIdx := 0
	for _, rend := range rends {
		if rend.Kind == hls.KindSubs {
			continue
		}
		rend := rend
		tails++
		go func() {
			defer func() { tailsDone <- struct{}{} }()
			ffmpeg.TailCSV(tailCtx, filepath.Join(outDir, rend.Key+".csv"), tailInterval, func(e ffmpeg.SegmentEntry) {
				r.appendAndClean(rend.Key, dirName+"/"+e.URI, e.Duration())
				// videoSegIdx is only ever touched from this single video-tail
				// goroutine (subs are excluded above), so it needs no lock.
				if rend.Kind == hls.KindVideo {
					for key, names := range subSegs {
						if videoSegIdx < len(names) {
							r.appendAndClean(key, dirName+"/"+names[videoSegIdx], e.Duration())
						}
					}
					videoSegIdx++
				}
			})
		}()
	}

	// 4. Encode (realtime because of -re).
	spec := ffmpeg.EncodeSpec{
		InputPath: item.Abs, OutDir: outDir, SegmentSec: r.spec.SegmentSec,
		DurationMs: item.Probe.DurationMs, Video: r.spec.Video,
		Renditions: rends, TrackMap: trackMap,
	}
	runErr := r.proc.Run(ctx, ffmpeg.BuildEncodeArgs(spec), ffmpeg.RunOpts{
		OnProgress: func(us int64) { r.offsetUs.Store(us) },
	})

	// Tailers must fully stop (drain any final CSV rows) before playItem
	// returns, so callers observing state after playItem see the complete item.
	stopTails()
	for i := 0; i < tails; i++ {
		<-tailsDone
	}
	return runErr
}

// itemDirNum parses an item directory name. Only all-digit names are ours; any
// other entry under the channel dir belongs to somebody else and is left alone.
func itemDirNum(name string) (int64, bool) {
	if name == "" {
		return 0, false
	}
	for _, c := range name {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(name, 10, 64)
	return n, err == nil
}

// sweepStaleItemDirs removes item directories that no playlist can reach any
// more. appendAndClean only deletes evicted *segments*; the directory and its
// non-playlist residue (the CSV segment lists, the whole-file VTT) would
// otherwise accumulate forever on a 24/7 channel.
//
// Item dirs are numbered in strictly increasing order, so any dir below the
// oldest one still referenced by a live window is unreachable. Comparing
// numerically rather than lexically keeps this correct past 999999 items, where
// %06d stops being fixed-width.
func (r *Runner) sweepStaleItemDirs() {
	live := r.mgr.LiveURIs()
	if len(live) == 0 {
		return // nothing on air yet, so nothing can be proven unreachable
	}
	oldest := int64(-1)
	for _, uri := range live {
		dir, _, ok := strings.Cut(uri, "/")
		if !ok {
			continue
		}
		if n, valid := itemDirNum(dir); valid && (oldest < 0 || n < oldest) {
			oldest = n
		}
	}
	if oldest < 0 {
		return
	}
	root := filepath.Join(r.spec.StreamsPath, r.spec.Slug)
	ents, err := os.ReadDir(root)
	if err != nil {
		return
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		if n, valid := itemDirNum(e.Name()); valid && n < oldest {
			_ = os.RemoveAll(filepath.Join(root, e.Name()))
		}
	}
}

// appendAndClean appends a segment and deletes any evicted segment files.
func (r *Runner) appendAndClean(key, uri string, dur float64) {
	for _, old := range r.mgr.Append(key, uri, dur) {
		if strings.Contains(old, "..") {
			continue
		}
		_ = os.Remove(filepath.Join(r.spec.StreamsPath, r.spec.Slug, old))
	}
}
