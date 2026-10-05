package override

import (
	"errors"
	"fmt"
	"testing"

	"ontology/playout"
	"ontology/slot"
)

func mustSched(t *testing.T, tl *slot.Timeline, id string, start, dur int64, fixed bool) {
	t.Helper()
	if err := tl.Schedule(0, id, start, dur, fixed); err != nil {
		t.Fatalf("Schedule(%s): %v", id, err)
	}
}

func mustOvr(t *testing.T, tl *slot.Timeline, id string, start, dur int64, mode Mode) {
	t.Helper()
	if err := Override(tl, 0, id, start, dur, mode); err != nil {
		t.Fatalf("Override(%s): %v", id, err)
	}
}

// exampleTimeline 构造题面示例：浮动 A[100,200)、浮动 B[200,260)、固定 C[300,400)，F=7。
func exampleTimeline(t *testing.T) *slot.Timeline {
	t.Helper()
	tl, err := slot.New(7)
	if err != nil {
		t.Fatal(err)
	}
	mustSched(t, tl, "A", 100, 100, false)
	mustSched(t, tl, "B", 200, 60, false)
	mustSched(t, tl, "C", 300, 100, true)
	return tl
}

func prog(id string, off int64) playout.Result {
	return playout.Result{Kind: playout.Program, ID: id, Offset: off}
}

func ovr(id string, off int64) playout.Result {
	return playout.Result{Kind: playout.Override, ID: id, Offset: off}
}

func fill(off int64) playout.Result {
	return playout.Result{Kind: playout.Filler, Offset: off}
}

func wantAt(t *testing.T, tl *slot.Timeline, tm int64, want playout.Result) {
	t.Helper()
	if got := playout.At(tl, tm); got != want {
		t.Errorf("At(%d) = %+v, want %+v", tm, got, want)
	}
}

func snapshot(tl *slot.Timeline, lo, hi int64) []playout.Result {
	out := make([]playout.Result, 0, hi-lo)
	for tm := lo; tm < hi; tm++ {
		out = append(out, playout.At(tl, tm))
	}
	return out
}

func TestPreemptPrecedence(t *testing.T) {
	tl := exampleTimeline(t)
	mustOvr(t, tl, "X", 150, 30, Preempt)
	if err := tl.Schedule(10, "D", 500, 10, false); err != nil { // 推进时钟到 10
		t.Fatal(err)
	}
	cases := []struct {
		name            string
		now, start, dur int64
		id              string
		want            error
	}{
		{"非法优先于回退", 9, 0, 0, "Y", slot.ErrInvalid},
		{"回退优先于已存在", 9, 600, 10, "X", slot.ErrClock},
		{"已存在优先于已过去", 10, 5, 10, "X", slot.ErrIDExists},
		{"已过去优先于冲突", 200, 160, 10, "Y", slot.ErrPast},
		{"冲突", 10, 160, 10, "Y", ErrConflict},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Override(tl, c.now, c.id, c.start, c.dur, Preempt); !errors.Is(err, c.want) {
				t.Errorf("got %v, want %v", err, c.want)
			}
		})
	}
}

// 题面示例二：顺延式插播 Y，s=150，d=30。
func TestShiftExample(t *testing.T) {
	tl := exampleTimeline(t)
	mustOvr(t, tl, "Y", 150, 30, Shift)
	// A 切成 [100,150)@0 与 [180,230)@50；B 后移到 [230,290)；空隙 40 吸收 30；C 不动。
	wantAt(t, tl, 149, prog("A", 49))
	wantAt(t, tl, 150, ovr("Y", 0))
	wantAt(t, tl, 179, ovr("Y", 29))
	wantAt(t, tl, 180, prog("A", 50)) // 续段偏移接着前段，不含插播时长
	wantAt(t, tl, 185, prog("A", 55))
	wantAt(t, tl, 229, prog("A", 99))
	wantAt(t, tl, 230, prog("B", 0))
	wantAt(t, tl, 289, prog("B", 59))
	wantAt(t, tl, 295, fill(5)) // 空隙起点 290，(295-290)%7=5
	wantAt(t, tl, 350, prog("C", 50))
}

// d=40 时空隙恰好吸收完，允许。
func TestShiftExactAbsorb(t *testing.T) {
	tl := exampleTimeline(t)
	mustOvr(t, tl, "Y", 150, 40, Shift)
	// A 切成 [100,150)@0 与 [190,240)@50；B 移到 [240,300)。
	wantAt(t, tl, 299, prog("B", 59))
	wantAt(t, tl, 300, prog("C", 0))
}

// d=41 时到达 C 仍剩 1，报挤占固定节目；被拒后时间线与时钟不变。
func TestShiftSqueezeRejectKeepsTimeline(t *testing.T) {
	tl := exampleTimeline(t)
	if err := tl.Schedule(10, "D", 500, 10, false); err != nil { // 推进时钟到 10
		t.Fatal(err)
	}
	before := snapshot(tl, 0, 500)
	if err := Override(tl, 100, "Y", 150, 41, Shift); !errors.Is(err, ErrFixedSqueeze) {
		t.Fatalf("got %v, want ErrFixedSqueeze", err)
	}
	after := snapshot(tl, 0, 500)
	for i := range before {
		if before[i] != after[i] {
			t.Fatalf("At(%d) changed after rejection: %+v -> %+v", i, before[i], after[i])
		}
	}
	if err := tl.Schedule(50, "E", 600, 10, false); err != nil { // 被拒不走钟：时钟仍为 10，50 合法
		t.Fatalf("clock moved after rejection: %v", err)
	}
}

// 先有抢占式插播 X[150,180)，顺延 s=120 因 X 终点大于 120 报插播冲突；s=180 允许。
func TestShiftConflictWithPreempt(t *testing.T) {
	tl := exampleTimeline(t)
	mustOvr(t, tl, "X", 150, 30, Preempt)
	if err := Override(tl, 0, "Y", 120, 10, Shift); !errors.Is(err, ErrConflict) {
		t.Errorf("s=120: got %v, want ErrConflict", err)
	}
	mustOvr(t, tl, "Z", 180, 10, Shift) // A 在 180 处被切分，续段 [190,210)@80
	wantAt(t, tl, 185, ovr("Z", 5))
	wantAt(t, tl, 195, prog("A", 85))
}

// s 落在固定节目段内（含恰等于段起点）报落在固定节目内。
func TestShiftFixedInterior(t *testing.T) {
	for _, s := range []int64{300, 350, 399} {
		tl := exampleTimeline(t)
		if err := Override(tl, 0, "Y", s, 10, Shift); !errors.Is(err, ErrFixedInterior) {
			t.Errorf("s=%d: got %v, want ErrFixedInterior", s, err)
		}
	}
}

// s 恰等于浮动段起点时该段整体后移。
func TestShiftAtFloatingStart(t *testing.T) {
	tl := exampleTimeline(t)
	mustOvr(t, tl, "Y", 200, 10, Shift) // B 整体后移到 [210,270)
	wantAt(t, tl, 205, ovr("Y", 5))
	wantAt(t, tl, 210, prog("B", 0))
	wantAt(t, tl, 215, prog("B", 5))
	wantAt(t, tl, 350, prog("C", 50)) // 空隙吸收 10，C 不动
}

// s 落在空隙中时，从 s 到下一段起点的长度作为第一个空隙。
func TestShiftInGap(t *testing.T) {
	tl := exampleTimeline(t)
	mustOvr(t, tl, "Y", 270, 10, Shift) // 首段空隙 [270,300) 长 30 吸收全部
	wantAt(t, tl, 275, ovr("Y", 5))
	wantAt(t, tl, 290, fill(3)) // 空隙起点 280，(290-280)%7=3
	wantAt(t, tl, 300, prog("C", 0))
}

// 已切分节目再次被切分。
func TestShiftResplit(t *testing.T) {
	tl := exampleTimeline(t)
	mustOvr(t, tl, "Y1", 150, 30, Shift) // A -> [100,150)@0, [180,230)@50；B -> [230,290)
	mustOvr(t, tl, "Y2", 200, 10, Shift) // A 续段再切 -> [180,200)@50, [210,240)@70；B -> [240,300)
	wantAt(t, tl, 195, prog("A", 65))
	wantAt(t, tl, 215, prog("A", 75))
	wantAt(t, tl, 245, prog("B", 5))
	wantAt(t, tl, 300, prog("C", 0))
}

// 多段连续后移。
func TestShiftMultiSegment(t *testing.T) {
	tl, err := slot.New(7)
	if err != nil {
		t.Fatal(err)
	}
	mustSched(t, tl, "P", 0, 100, false)
	mustSched(t, tl, "Q", 100, 100, false)
	mustSched(t, tl, "R", 200, 100, false)
	mustOvr(t, tl, "Y", 50, 30, Shift)
	// P 切成 [0,50)@0 与 [80,130)@50；Q -> [130,230)；R -> [230,330)。
	wantAt(t, tl, 100, prog("P", 70))
	wantAt(t, tl, 150, prog("Q", 20))
	wantAt(t, tl, 200, prog("Q", 70))
	wantAt(t, tl, 300, prog("R", 70))
}

// 顺延式拒绝次序：插播冲突 > 落在固定节目内 > 挤占固定节目。
func TestShiftPrecedence(t *testing.T) {
	build := func(t *testing.T) *slot.Timeline { // 固定 F1[100,200)、浮动 G[300,400)、固定 F2[500,600)
		tl, err := slot.New(7)
		if err != nil {
			t.Fatal(err)
		}
		mustSched(t, tl, "F1", 100, 100, true)
		mustSched(t, tl, "G", 300, 100, false)
		mustSched(t, tl, "F2", 500, 100, true)
		return tl
	}
	t.Run("冲突优先于落在固定节目内", func(t *testing.T) {
		tl := build(t)
		mustOvr(t, tl, "X", 150, 300, Preempt) // X[150,450) 覆盖 F1 尾部
		if err := Override(tl, 0, "Y", 120, 10, Shift); !errors.Is(err, ErrConflict) {
			t.Errorf("got %v, want ErrConflict", err)
		}
	})
	t.Run("落在固定节目内优先于挤占固定节目", func(t *testing.T) {
		tl := build(t)
		if err := Override(tl, 0, "Y", 150, 500, Shift); !errors.Is(err, ErrFixedInterior) {
			t.Errorf("got %v, want ErrFixedInterior", err)
		}
	})
	t.Run("挤占固定节目", func(t *testing.T) {
		tl := build(t)
		// s=250：空隙 50 吸收后 r=150，G 后移 150，空隙 100 吸收后 r=50，F2 被挤。
		if err := Override(tl, 0, "Y", 250, 200, Shift); !errors.Is(err, ErrFixedSqueeze) {
			t.Errorf("got %v, want ErrFixedSqueeze", err)
		}
	})
}

func TestCancelOverrides(t *testing.T) {
	t.Run("截断抢占段", func(t *testing.T) {
		tl := exampleTimeline(t)
		mustOvr(t, tl, "X", 150, 30, Preempt)
		if err := tl.Cancel(160, "X"); err != nil {
			t.Fatal(err)
		}
		wantAt(t, tl, 155, ovr("X", 5))
		wantAt(t, tl, 170, prog("A", 70)) // 被覆盖的节目重新露出
	})
	t.Run("截断顺延段不回退", func(t *testing.T) {
		tl := exampleTimeline(t)
		mustOvr(t, tl, "Y", 150, 30, Shift)
		if err := tl.Cancel(160, "Y"); err != nil {
			t.Fatal(err)
		}
		wantAt(t, tl, 155, ovr("Y", 5))
		wantAt(t, tl, 170, fill(3))       // 空隙起点为 Y 截断后的终点 160，(170-160)%7=3
		wantAt(t, tl, 185, prog("A", 55)) // 曾被顺延的段不退回
	})
	t.Run("截断多段节目", func(t *testing.T) {
		tl := exampleTimeline(t)
		mustOvr(t, tl, "Y", 150, 30, Shift) // A -> [100,150)@0, [180,230)@50
		if err := tl.Cancel(190, "A"); err != nil {
			t.Fatal(err)
		}
		wantAt(t, tl, 185, prog("A", 55))
		wantAt(t, tl, 195, fill(5)) // 续段截到 [180,190)，空隙起点 190，(195-190)%7=5
		if err := tl.Cancel(200, "A"); !errors.Is(err, slot.ErrEnded) {
			t.Errorf("got %v, want ErrEnded", err)
		}
	})
}

// 节目与插播共用一个标识空间。
func TestSharedIDNamespace(t *testing.T) {
	tl := exampleTimeline(t)
	if err := Override(tl, 0, "A", 500, 10, Preempt); !errors.Is(err, slot.ErrIDExists) {
		t.Errorf("program id reused by override: got %v", err)
	}
	mustOvr(t, tl, "X", 500, 10, Preempt)
	if err := tl.Schedule(0, "X", 600, 10, false); !errors.Is(err, slot.ErrIDExists) {
		t.Errorf("override id reused by schedule: got %v", err)
	}
	if err := tl.Cancel(0, "ZZ"); !errors.Is(err, slot.ErrIDNotFound) {
		t.Errorf("cancel unknown: got %v", err)
	}
}

// Schedule 不得与顺延式插播段相交，但可以与抢占式插播段相交。
func TestScheduleAgainstOverrides(t *testing.T) {
	tl := exampleTimeline(t)
	mustOvr(t, tl, "X", 500, 20, Preempt)
	mustOvr(t, tl, "Y", 600, 20, Shift)
	if err := tl.Schedule(0, "P1", 490, 30, false); err != nil { // 覆盖抢占段 [500,520)，允许
		t.Errorf("overlap preempt: got %v, want nil", err)
	}
	if err := tl.Schedule(0, "P2", 590, 30, false); !errors.Is(err, slot.ErrOverlap) { // 覆盖顺延段 [600,620)
		t.Errorf("overlap shift: got %v, want ErrOverlap", err)
	}
	if err := tl.Schedule(0, "P3", 620, 10, false); err != nil { // 与顺延段相接，允许
		t.Errorf("touch shift: got %v, want nil", err)
	}
}

// moved 计数器：顺延扫描查看的段数与受影响段数成正比，与其后未受影响段数无关。
func TestMovedBound(t *testing.T) {
	for _, n := range []int{100, 10000} {
		t.Run(fmt.Sprintf("trailing=%d", n), func(t *testing.T) {
			tl, err := slot.New(7)
			if err != nil {
				t.Fatal(err)
			}
			mustSched(t, tl, "P0", 0, 100, false)
			for i := 0; i < n; i++ {
				mustSched(t, tl, fmt.Sprintf("Q%d", i), int64(200+i*100), 100, false)
			}
			// P0 被切分，续段 [80,180)；空隙 [180,200) 吸收 20 后 r=0，其后 n 段不受影响。
			mustOvr(t, tl, "Y", 50, 30, Shift)
			if got := moved.Load(); got != 2 {
				t.Errorf("moved = %d, want 2（切分 1 + 吸收空隙时查看 1）", got)
			}
			if limit := int64(1 + 0 + 3); moved.Load() > limit {
				t.Errorf("moved = %d > 被切分或被后移段数加 3 = %d", moved.Load(), limit)
			}
		})
	}
	t.Run("consecutive", func(t *testing.T) { // 多段连续后移：moved = 切分 1 + 后移 n
		const n = 100
		tl, err := slot.New(7)
		if err != nil {
			t.Fatal(err)
		}
		mustSched(t, tl, "P0", 0, 100, false)
		for i := 0; i < n; i++ {
			mustSched(t, tl, fmt.Sprintf("Q%d", i), int64(110+i*100), 100, false)
		}
		mustOvr(t, tl, "Y", 50, 25, Shift)
		if got, want := moved.Load(), int64(n+1); got != want {
			t.Errorf("moved = %d, want %d", got, want)
		}
	})
}

// 题面示例一：抢占式插播 X[150,180)。
func TestPreemptExample(t *testing.T) {
	tl := exampleTimeline(t)
	mustOvr(t, tl, "X", 150, 30, Preempt)
	wantAt(t, tl, 50, fill(1))  // 垫片，50%7=1
	wantAt(t, tl, 270, fill(3)) // 空隙起点 260，(270-260)%7=3
	wantAt(t, tl, 160, ovr("X", 10))
	wantAt(t, tl, 190, prog("A", 90)) // 被覆盖后接续，偏移含被覆盖时长
	wantAt(t, tl, 149, prog("A", 49))
	wantAt(t, tl, 180, prog("A", 80))
}

func TestPreemptConflict(t *testing.T) {
	cases := []struct {
		name       string
		setup      func(t *testing.T, tl *slot.Timeline)
		start, dur int64
		want       error
	}{
		{"与抢占段相交", func(t *testing.T, tl *slot.Timeline) { mustOvr(t, tl, "X", 150, 30, Preempt) }, 160, 10, ErrConflict},
		{"与抢占段相接允许", func(t *testing.T, tl *slot.Timeline) { mustOvr(t, tl, "X", 150, 30, Preempt) }, 180, 10, nil},
		{"覆盖固定节目允许", nil, 320, 30, nil},
		{"与顺延段相交", func(t *testing.T, tl *slot.Timeline) { mustOvr(t, tl, "X", 500, 20, Shift) }, 510, 5, ErrConflict},
		{"与顺延段相接允许", func(t *testing.T, tl *slot.Timeline) { mustOvr(t, tl, "X", 500, 20, Shift) }, 520, 5, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tl := exampleTimeline(t)
			if c.setup != nil {
				c.setup(t, tl)
			}
			if err := Override(tl, 0, "Y", c.start, c.dur, Preempt); !errors.Is(err, c.want) {
				t.Errorf("got %v, want %v", err, c.want)
			}
		})
	}
}
