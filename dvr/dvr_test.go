package dvr

import (
	"math"
	"testing"
)

// buildWindow seals segments with the given durations; disc marks the seqs
// carrying a discontinuity flag.
func buildWindow(limit int64, durs []int64, disc map[int64]bool) *Window {
	w := NewWindow(limit)
	var start int64
	for i, d := range durs {
		seq := int64(i)
		w.Add(Segment{Seq: seq, Start: start, Dur: d, Disc: disc[seq]})
		start += d
	}
	return w
}

func TestEviction(t *testing.T) {
	tests := []struct {
		name      string
		limit     int64
		durs      []int64
		wantTotal int64
		wantSeqs  []int64
	}{
		{"无淘汰", 10000, []int64{4000, 3000}, 7000, []int64{0, 1}},
		{"淘汰后恰等W", 10000, []int64{4000, 10000}, 10000, []int64{1}},
		{"连续淘汰多片", 10000, []int64{4000, 3000, 5000, 4000}, 12000, []int64{1, 2, 3}},
		{"单片大于W仍保留", 10, []int64{100}, 100, []int64{0}},
		{"总时长可大于W", 10000, []int64{6000, 6000}, 12000, []int64{0, 1}},
		{"逐片加入逐片淘汰", 5, []int64{3, 3, 3, 3}, 6, []int64{2, 3}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			w := buildWindow(tc.limit, tc.durs, nil)
			if w.total != tc.wantTotal {
				t.Fatalf("total=%d, want %d", w.total, tc.wantTotal)
			}
			if got := len(w.segs) - w.head; got != len(tc.wantSeqs) {
				t.Fatalf("window size=%d, want %d", got, len(tc.wantSeqs))
			}
			for i, seq := range tc.wantSeqs {
				if got := w.segs[w.head+i].Seq; got != seq {
					t.Fatalf("window[%d].Seq=%d, want %d", i, got, seq)
				}
			}
			// Invariant: total < W, or total minus the oldest < W.
			oldest := w.segs[w.head].Dur
			if w.total >= tc.limit && w.total-oldest >= tc.limit {
				t.Fatalf("invariant broken: total=%d oldest=%d W=%d", w.total, oldest, tc.limit)
			}
		})
	}
}

func TestPlaylist(t *testing.T) {
	// Timeline: [0,4000) [4000,7000) [7000,12000) [12000,16000), disc at 2.
	newWin := func() *Window {
		return buildWindow(10000, []int64{4000, 3000, 5000, 4000}, map[int64]bool{2: true})
	}
	tests := []struct {
		name     string
		from, to int64
		wantErr  error
		wantSeqs []int64
		wantMS   int64
		wantDS   int64
	}{
		{"整段区间", 5000, 13000, nil, []int64{1, 2, 3}, 1, 0},
		{"区间落在单片内", 7000, 8000, nil, []int64{2}, 2, 0},
		{"首片自身标记不计入", 7000, 12000, nil, []int64{2}, 2, 0},
		{"to截断到hi", 12000, 20000, nil, []int64{3}, 3, 1},
		{"端点落在片边界只含前片", 6000, 7000, nil, []int64{1}, 1, 0},
		{"from恰为lo", 4000, 4001, nil, []int64{1}, 1, 0},
		{"from恰为hi减一", 15999, 16000, nil, []int64{3}, 3, 1},
		{"已滑出窗口", 3999, 5000, ErrSlidOut, nil, 0, 0},
		{"尚未产生", 16000, 17000, ErrNotYet, nil, 0, 0},
		{"from为负", -1, 5, ErrInvalidParam, nil, 0, 0},
		{"from不小于to", 5000, 5000, ErrInvalidParam, nil, 0, 0},
		{"to超过上界", 0, 1_000_000_000_000_001, ErrInvalidParam, nil, 0, 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pl, err := newWin().Playlist(tc.from, tc.to)
			if err != tc.wantErr {
				t.Fatalf("err=%v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			if pl.MediaSequence != tc.wantMS || pl.DiscontinuitySequence != tc.wantDS {
				t.Fatalf("MS=%d DS=%d, want MS=%d DS=%d",
					pl.MediaSequence, pl.DiscontinuitySequence, tc.wantMS, tc.wantDS)
			}
			if len(pl.Segments) != len(tc.wantSeqs) {
				t.Fatalf("got %d segments, want %d", len(pl.Segments), len(tc.wantSeqs))
			}
			for i, seq := range tc.wantSeqs {
				if pl.Segments[i].Seq != seq {
					t.Fatalf("Segments[%d].Seq=%d, want %d", i, pl.Segments[i].Seq, seq)
				}
			}
		})
	}
}

func TestPlaylistErrorOrder(t *testing.T) {
	empty := NewWindow(10000)
	if _, err := empty.Playlist(0, 1); err != ErrNotYet {
		t.Fatalf("empty window: err=%v, want ErrNotYet", err)
	}
	// 参数非法优先于尚未产生。
	if _, err := empty.Playlist(-1, 1); err != ErrInvalidParam {
		t.Fatalf("param before notyet: err=%v, want ErrInvalidParam", err)
	}
	// 尚未产生优先于已滑出窗口：from >= hi 时不再检查 lo。
	w := buildWindow(10000, []int64{4000, 10000}, nil) // window=[seq1], lo=4000, hi=14000
	if _, err := w.Playlist(14000, 15000); err != ErrNotYet {
		t.Fatalf("notyet before slidout: err=%v, want ErrNotYet", err)
	}
}

func TestDiscontinuitySequence(t *testing.T) {
	// Disc at seq 2 and 5; eviction drops seq 0..3 (incl. Disc seq 2).
	// Window is seq 4..6, lo=16000, hi=28000.
	durs := []int64{4000, 4000, 4000, 4000, 4000, 4000, 4000}
	w := buildWindow(10000, durs, map[int64]bool{2: true, 5: true})
	pl, err := w.Playlist(20000, 28000) // first returned seq = 5 (itself Disc)
	if err != nil {
		t.Fatal(err)
	}
	if pl.DiscontinuitySequence != 1 {
		t.Fatalf("DS=%d, want 1 (own mark excluded, evicted seq 2 counted)", pl.DiscontinuitySequence)
	}
	pl, err = w.Playlist(24000, 28000) // first returned seq = 6
	if err != nil {
		t.Fatal(err)
	}
	if pl.DiscontinuitySequence != 2 {
		t.Fatalf("DS=%d, want 2 (both Disc seqs 2 and 5 counted)", pl.DiscontinuitySequence)
	}
}

func TestPlaylistProbes(t *testing.T) {
	for _, n := range []int{100, 10000} {
		t.Run(map[int]string{100: "n=100", 10000: "n=10000"}[n], func(t *testing.T) {
			durs := make([]int64, n)
			for i := range durs {
				durs[i] = 1
			}
			w := buildWindow(1_000_000_000, durs, nil)
			bound := 2 * int(math.Ceil(math.Log2(float64(n)+1)))
			for _, from := range []int64{0, int64(n / 3), int64(n / 2), int64(n - 1)} {
				if _, err := w.Playlist(from, from+1); err != nil {
					t.Fatal(err)
				}
				t.Logf("n=%d from=%d probes=%d bound=%d", n, from, w.probes, bound)
				if w.probes > bound {
					t.Fatalf("probes=%d exceeds 2*ceil(log2(n+1))=%d", w.probes, bound)
				}
			}
		})
	}
}
