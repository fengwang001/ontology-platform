package wm

import "testing"

func TestClassifyTable(t *testing.T) {
	cases := []struct {
		name     string
		preset   int64 // >0 表示先以该值推进水位
		ts       int64
		wantMain bool
		wantGap  int64
	}{
		{"first", 0, 10, true, 0},
		{"greater", 10, 15, true, 0},
		{"equal-boundary", 15, 15, true, 0},
		{"late", 15, 12, false, 3},
		{"late-far", 15, 10, false, 5},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var w Watermark
			if c.preset > 0 {
				w.Advance(c.preset)
			}
			mainRoad, gap := w.Classify(c.ts)
			if mainRoad != c.wantMain || gap != c.wantGap {
				t.Fatalf("ts=%d preset=%d: got main=%v gap=%d, want main=%v gap=%d",
					c.ts, c.preset, mainRoad, gap, c.wantMain, c.wantGap)
			}
			if !c.wantMain && gap <= 0 {
				t.Fatalf("late event gap must be > 0, got %d", gap)
			}
		})
	}
}

func TestWatermarkMonotonic(t *testing.T) {
	seq := []int64{10, 20, 20, 15, 25, 25, 24, 30}
	var w Watermark
	var runningMax, prevV int64
	havePrev := false
	for i, ts := range seq {
		oldV, seen := w.Value()
		mainRoad, _ := w.Classify(ts)
		changed := false
		if mainRoad {
			changed = w.Advance(ts)
		}
		v, ok := w.Value()
		if !ok {
			t.Fatalf("step %d: watermark undefined", i)
		}
		if havePrev && v < prevV {
			t.Fatalf("step %d: watermark went backwards %d -> %d", i, prevV, v)
		}
		prevV, havePrev = v, true
		if i == 0 || ts > runningMax {
			runningMax = ts
		}
		if v != runningMax {
			t.Fatalf("step %d ts=%d: wm=%d want running max %d", i, ts, v, runningMax)
		}
		wantChanged := !seen || ts > oldV
		if changed != wantChanged {
			t.Fatalf("step %d ts=%d: changed=%v want %v", i, ts, changed, wantChanged)
		}
	}
}

func TestChecksCountConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		var w Watermark
		for i := 0; i < m; i++ {
			ts := int64(i + 1)
			if mainRoad, _ := w.Classify(ts); !mainRoad {
				t.Fatalf("m=%d i=%d: increasing event wrongly late", m, i)
			}
			if w.checks != 0 {
				t.Fatalf("m=%d i=%d: checks=%d, want 0 (O(1))", m, i, w.checks)
			}
			w.Advance(ts)
		}
		// 再喂一个迟到事件：判定只看标量水位，检查的历史事件数仍须为 0。
		mainRoad, gap := w.Classify(0)
		if mainRoad || gap != int64(m) {
			t.Fatalf("m=%d: late classify got main=%v gap=%d, want side gap=%d", m, mainRoad, gap, m)
		}
		if w.checks != 0 {
			t.Fatalf("m=%d: late-event checks=%d, want 0, must not scan %d history events",
				m, w.checks, m)
		}
	}
}
