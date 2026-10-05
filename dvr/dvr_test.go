package dvr

import (
	"errors"
	"testing"

	"ontology/segment"
)

// chain 按给定时长构造媒体时间首尾相接的片序列并依次入窗。
func chain(w *Window, durs ...int64) {
	var seq, start int64
	for _, d := range durs {
		w.Add(segment.Segment{Seq: seq, Start: start, Dur: d})
		seq++
		start += d
	}
}

func windowSeqs(w *Window) []int64 {
	var out []int64
	for _, s := range w.segs[w.head:] {
		out = append(out, s.Seq)
	}
	return out
}

func equalInts(a, b []int64) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestEviction(t *testing.T) {
	tests := []struct {
		name string
		w    int64
		durs []int64
		want []int64 // 期望窗口内留存的 seq
	}{
		{"不淘汰：去掉最旧片后小于W", 10000, []int64{4000, 3000, 5000}, []int64{0, 1, 2}},
		{"示例：淘汰seq0后停止", 10000, []int64{4000, 3000, 5000, 4000}, []int64{1, 2, 3}},
		{"淘汰后恰等W允许", 10000, []int64{5000, 5000, 5000}, []int64{1, 2}},
		{"连续淘汰多片", 10000, []int64{1000, 2000, 3000, 4000, 5000}, []int64{2, 3, 4}},
		{"最后一片永不淘汰", 1, []int64{60000}, []int64{0}},
		{"单片超过W仍保留", 100, []int64{500, 600}, []int64{1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := NewWindow(tt.w)
			chain(w, tt.durs...)
			got := windowSeqs(w)
			if !equalInts(got, tt.want) {
				t.Fatalf("窗口留存 %v，期望 %v", got, tt.want)
			}
			// 不变量：窗口内总时长小于 W，或去掉最旧片后小于 W。
			if w.total >= tt.w && w.total-w.segs[w.head].Dur >= tt.w {
				t.Fatalf("淘汰不变量被破坏：total=%d oldest=%d W=%d",
					w.total, w.segs[w.head].Dur, tt.w)
			}
			t.Logf("durs=%v W=%d -> 留存 seq %v（判定依据：总时长减最旧片仍不小于 W 才淘汰）",
				tt.durs, tt.w, got)
		})
	}
}

func TestPlaylistErrors(t *testing.T) {
	t.Run("空窗口报尚未产生", func(t *testing.T) {
		w := NewWindow(10000)
		if _, err := w.Playlist(0, 1000); !errors.Is(err, ErrNotYetProduced) {
			t.Fatalf("err=%v，期望 ErrNotYetProduced", err)
		}
	})
	w := NewWindow(10000)
	chain(w, 4000, 3000, 5000, 4000) // 窗口 seq1..3，lo=4000，hi=16000
	t.Run("from不小于hi报尚未产生", func(t *testing.T) {
		if _, err := w.Playlist(16000, 17000); !errors.Is(err, ErrNotYetProduced) {
			t.Fatalf("err=%v，期望 ErrNotYetProduced", err)
		}
	})
	t.Run("from小于lo报已滑出窗口", func(t *testing.T) {
		if _, err := w.Playlist(3999, 5000); !errors.Is(err, ErrSlidOut) {
			t.Fatalf("err=%v，期望 ErrSlidOut", err)
		}
	})
	t.Run("尚未产生优先于已滑出窗口", func(t *testing.T) {
		// from 同时不满足两种情形时不可能，但空窗口只报尚未产生。
		empty := NewWindow(10000)
		if _, err := empty.Playlist(0, 1); !errors.Is(err, ErrNotYetProduced) {
			t.Fatalf("err=%v，期望 ErrNotYetProduced", err)
		}
	})
}

func TestPlaylistRanges(t *testing.T) {
	mkWindow := func() *Window {
		w := NewWindow(10000)
		// seq0 [0,4000) seq1 [4000,7000) seq2 [7000,12000) seq3 [12000,16000)
		// seq2 带标记，DiscBefore：seq1=0, seq2=0, seq3=1
		w.Add(segment.Segment{Seq: 0, Start: 0, Dur: 4000})
		w.Add(segment.Segment{Seq: 1, Start: 4000, Dur: 3000})
		w.Add(segment.Segment{Seq: 2, Start: 7000, Dur: 5000, Disc: true})
		w.Add(segment.Segment{Seq: 3, Start: 12000, Dur: 4000, DiscBefore: 1})
		return w
	}
	tests := []struct {
		name     string
		from, to int64
		wantSeqs []int64
		wantMS   int64
		wantDS   int64
	}{
		{"跨三片", 5000, 13000, []int64{1, 2, 3}, 1, 0},
		{"只命中一片且首片自身标记不计入", 7000, 8000, []int64{2}, 2, 0},
		{"to截到hi且统计已淘汰标记片", 12000, 20000, []int64{3}, 3, 1},
		{"区间端点落在片边界", 6000, 7000, []int64{1}, 1, 0},
		{"from恰在片起点", 4000, 4001, []int64{1}, 1, 0},
		{"from恰在片终点前一刻", 11999, 12000, []int64{2}, 2, 0},
		{"整窗", 4000, 16000, []int64{1, 2, 3}, 1, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := mkWindow().Playlist(tt.from, tt.to)
			if err != nil {
				t.Fatalf("意外错误：%v", err)
			}
			var seqs []int64
			for _, s := range got.Segments {
				seqs = append(seqs, s.Seq)
			}
			if !equalInts(seqs, tt.wantSeqs) || got.MediaSequence != tt.wantMS || got.DiscontinuitySequence != tt.wantDS {
				t.Fatalf("得到 seqs=%v MS=%d DS=%d，期望 seqs=%v MS=%d DS=%d",
					seqs, got.MediaSequence, got.DiscontinuitySequence, tt.wantSeqs, tt.wantMS, tt.wantDS)
			}
			t.Logf("[%d,%d) -> seqs=%v MS=%d DS=%d（判定依据：半开区间相交、to 截到 hi、DS 不计首片自身）",
				tt.from, tt.to, seqs, got.MediaSequence, got.DiscontinuitySequence)
		})
	}
}

// TestPlaylistProbes 验证定位首片的比较次数上界 2·ceil(log2(n+1))。
func TestPlaylistProbes(t *testing.T) {
	ceilLog2 := func(n int) int {
		r := 0
		for (1 << r) < n {
			r++
		}
		return r
	}
	for _, n := range []int{100, 10000} {
		w := NewWindow(1_000_000_000)
		for i := 0; i < n; i++ {
			w.Add(segment.Segment{Seq: int64(i), Start: int64(i), Dur: 1})
		}
		from := int64(n / 3)
		if _, err := w.Playlist(from, from+1); err != nil {
			t.Fatalf("n=%d 意外错误：%v", n, err)
		}
		bound := 2 * ceilLog2(n+1)
		if w.probes > bound {
			t.Fatalf("n=%d probes=%d 超过上界 %d", n, w.probes, bound)
		}
		t.Logf("n=%d probes=%d <= 2·ceil(log2(n+1))=%d", n, w.probes, bound)
	}
}

// TestEvictionAmortized 验证大量封片下底层数组有界（淘汰均摊 O(1)）。
func TestEvictionAmortized(t *testing.T) {
	w := NewWindow(1000)
	const adds = 200000
	for i := 0; i < adds; i++ {
		w.Add(segment.Segment{Seq: int64(i), Start: int64(i), Dur: 1})
	}
	live := w.Len()
	if live != 1000 {
		t.Fatalf("窗口片数=%d，期望 1000", live)
	}
	if bound := 2*live + 128; len(w.segs) > bound {
		t.Fatalf("底层数组长度 %d 超过界 %d，压缩失效", len(w.segs), bound)
	}
	t.Logf("%d 次入窗后底层数组 %d，窗口 %d 片（均摊常数触碰）", adds, len(w.segs), live)
}
