package hls

import (
	"bufio"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Cue struct {
	Start    time.Duration
	End      time.Duration
	Settings string
	Text     string
}

const vttSegmentHeader = "WEBVTT\nX-TIMESTAMP-MAP=MPEGTS:900000,LOCAL:00:00:00.000\n"

var timingRe = regexp.MustCompile(
	`^(?:(\d+):)?(\d{2}):(\d{2})\.(\d{3})\s+-->\s+(?:(\d+):)?(\d{2}):(\d{2})\.(\d{3})\s*(.*)$`)

func ParseVTT(r io.Reader) ([]Cue, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 1024*1024), 1024*1024)
	var cues []Cue
	var cur *Cue
	for sc.Scan() {
		line := strings.TrimRight(sc.Text(), "\r")
		if cur == nil {
			if line == "" || strings.HasPrefix(line, "WEBVTT") {
				continue
			}
			m := timingRe.FindStringSubmatch(line)
			if m == nil {
				continue // cue identifier, NOTE, STYLE, REGION line — skip
			}
			cur = &Cue{
				Start:    vttTimestamp(m[1], m[2], m[3], m[4]),
				End:      vttTimestamp(m[5], m[6], m[7], m[8]),
				Settings: strings.TrimSpace(m[9]),
			}
			continue
		}
		if line == "" {
			cues = append(cues, *cur)
			cur = nil
			continue
		}
		if cur.Text != "" {
			cur.Text += "\n"
		}
		cur.Text += line
	}
	if cur != nil {
		cues = append(cues, *cur)
	}
	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("scan vtt: %w", err)
	}
	return cues, nil
}

func vttTimestamp(h, m, s, ms string) time.Duration {
	hh, _ := strconv.Atoi(h) // empty string → 0
	mm, _ := strconv.Atoi(m)
	ss, _ := strconv.Atoi(s)
	mss, _ := strconv.Atoi(ms)
	return time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute +
		time.Duration(ss)*time.Second + time.Duration(mss)*time.Millisecond
}

func formatVTTTime(d time.Duration) string {
	h := d / time.Hour
	m := (d % time.Hour) / time.Minute
	s := (d % time.Minute) / time.Second
	ms := (d % time.Second) / time.Millisecond
	return fmt.Sprintf("%02d:%02d:%02d.%03d", h, m, s, ms)
}

// SplitVTT distributes cues into ceil(total/segDur) VTT segment bodies.
// A cue is included in every segment its [Start,End) range overlaps.
func SplitVTT(cues []Cue, segDur, total time.Duration) []string {
	if segDur <= 0 || total <= 0 {
		return nil
	}
	n := int((total + segDur - 1) / segDur)
	segs := make([]string, n)
	for i := 0; i < n; i++ {
		lo := time.Duration(i) * segDur
		hi := lo + segDur
		var b strings.Builder
		b.WriteString(vttSegmentHeader)
		for _, c := range cues {
			if c.End <= lo || c.Start >= hi {
				continue
			}
			b.WriteString("\n")
			b.WriteString(formatVTTTime(c.Start))
			b.WriteString(" --> ")
			b.WriteString(formatVTTTime(c.End))
			if c.Settings != "" {
				b.WriteString(" " + c.Settings)
			}
			b.WriteString("\n")
			b.WriteString(c.Text)
			b.WriteString("\n")
		}
		segs[i] = b.String()
	}
	return segs
}
