package playout

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/override"
	"ontology/slot"
)

func build(t *testing.T, filler int64) *slot.Timeline {
	t.Helper()
	tl, err := slot.New(filler)
	if err != nil {
		t.Fatal(err)
	}
	return tl
}

func mustSched(t *testing.T, tl *slot.Timeline, id string, start, dur int64, fixed bool) {
	t.Helper()
	if err := tl.Schedule(0, id, start, dur, fixed); err != nil {
		t.Fatalf("Schedule(%s): %v", id, err)
	}
}

func wantAt(t *testing.T, tl *slot.Timeline, tm int64, want Result) {
	t.Helper()
	if got := At(tl, tm); got != want {
		t.Errorf("At(%d) = %+v, want %+v", tm, got, want)
	}
}

// 垫片空隙起点在两种插播下的差别：抢占式不改变空隙起点，顺延式段本身就是空隙起点。
func TestFillerGapStart(t *testing.T) {
	t.Run("抢占式不改变空隙起点", func(t *testing.T) {
		tl := build(t, 7)
		mustSched(t, tl, "A", 100, 100, false) // A[100,200)
		if err := override.Override(tl, 0, "X", 300, 100, override.Preempt); err != nil {
			t.Fatal(err)
		}
		wantAt(t, tl, 250, Result{Kind: Filler, Offset: (250 - 200) % 7}) // 抢占段不作空隙起点
		wantAt(t, tl, 350, Result{Kind: Override, ID: "X", Offset: 50})
		wantAt(t, tl, 500, Result{Kind: Filler, Offset: (500 - 200) % 7}) // 空隙起点仍为 A 的终点 200
	})
	t.Run("顺延式段成为空隙起点", func(t *testing.T) {
		tl := build(t, 7)
		mustSched(t, tl, "A", 100, 100, false)
		if err := override.Override(tl, 0, "Y", 300, 50, override.Shift); err != nil {
			t.Fatal(err)
		}
		wantAt(t, tl, 320, Result{Kind: Override, ID: "Y", Offset: 20})
		wantAt(t, tl, 400, Result{Kind: Filler, Offset: (400 - 350) % 7}) // 空隙起点为 Y 的终点 350
	})
	t.Run("无任何段时空隙起点为0", func(t *testing.T) {
		tl := build(t, 7)
		wantAt(t, tl, 0, Result{Kind: Filler, Offset: 0})
		wantAt(t, tl, 20, Result{Kind: Filler, Offset: 20 % 7})
	})
}

// probes 计数器：At 一次查询比较的段数不超过 2·ceil(log2(n+2))+2。
func TestProbesBound(t *testing.T) {
	ceilLog2 := func(x int64) int64 {
		n := int64(0)
		for (int64(1) << n) < x {
			n++
		}
		return n
	}
	for _, n := range []int64{100, 10000} {
		t.Run(fmt.Sprintf("n=%d", n), func(t *testing.T) {
			tl := build(t, 7)
			for i := int64(0); i < n; i++ {
				mustSched(t, tl, fmt.Sprintf("P%d", i), i*10, 8, false)
			}
			limit := 2*ceilLog2(n+2) + 2
			for _, tm := range []int64{0, 3, 11, n * 10 / 2, n*10 - 1, n*10 + 50} {
				At(tl, tm)
				if got := probes.Load(); got > limit {
					t.Errorf("At(%d): probes = %d > %d", tm, got, limit)
				}
			}
		})
	}
}

// ---- 逐秒展开的朴素模拟（对照组，小时间范围） ----

const naiveT = 2048

type ncell struct {
	kind  int // 0 空，1 常规节目，2 顺延式插播
	id    string
	off   int64
	fixed bool
}

type naive struct {
	f    int64
	now  int64
	base []ncell // 布局层：常规节目段 + 顺延式插播段
	ovr  []ncell // 覆盖层：抢占式插播段
	ids  map[string]bool
}

func newNaive(f int64) *naive {
	return &naive{f: f, base: make([]ncell, naiveT), ovr: make([]ncell, naiveT), ids: map[string]bool{}}
}

func (n *naive) schedule(now int64, id string, start, dur int64, fixed bool) error {
	if err := slot.ValidateSpan(id, now, start, dur); err != nil {
		return err
	}
	if now < n.now {
		return slot.ErrClock
	}
	if n.ids[id] {
		return slot.ErrIDExists
	}
	if start < now {
		return slot.ErrPast
	}
	end := start + dur
	if end > naiveT {
		panic("naive overflow")
	}
	for tm := start; tm < end; tm++ {
		if n.base[tm].kind != 0 {
			return slot.ErrOverlap
		}
	}
	for tm := start; tm < end; tm++ {
		n.base[tm] = ncell{kind: 1, id: id, off: tm - start, fixed: fixed}
	}
	n.ids[id] = true
	n.now = now
	return nil
}

func (n *naive) preempt(now int64, id string, start, dur int64) error {
	if err := slot.ValidateSpan(id, now, start, dur); err != nil {
		return err
	}
	if now < n.now {
		return slot.ErrClock
	}
	if n.ids[id] {
		return slot.ErrIDExists
	}
	if start < now {
		return slot.ErrPast
	}
	end := start + dur
	for tm := start; tm < end; tm++ {
		if n.ovr[tm].kind != 0 || n.base[tm].kind == 2 {
			return override.ErrConflict
		}
	}
	for tm := start; tm < end; tm++ {
		n.ovr[tm] = ncell{kind: 1, id: id, off: tm - start}
	}
	n.ids[id] = true
	n.now = now
	return nil
}

func (n *naive) shift(now int64, id string, s, d int64) error {
	if err := slot.ValidateSpan(id, now, s, d); err != nil {
		return err
	}
	if now < n.now {
		return slot.ErrClock
	}
	if n.ids[id] {
		return slot.ErrIDExists
	}
	if s < now {
		return slot.ErrPast
	}
	// 插播冲突：任一既有插播段终点大于 s，等价于存在 t >= s 的插播秒。
	for tm := s; tm < naiveT; tm++ {
		if n.ovr[tm].kind != 0 || n.base[tm].kind == 2 {
			return override.ErrConflict
		}
	}
	if c := n.base[s]; c.kind == 1 && c.fixed {
		return override.ErrFixedInterior
	}
	// 模拟：空隙逐秒吸收顺延量，r>0 时固定节目秒被挤则整体拒绝。
	r := d
	for p := s; p < naiveT && r > 0; p++ {
		c := n.base[p]
		switch {
		case c.kind == 0:
			r--
		case c.kind == 1 && c.fixed:
			return override.ErrFixedSqueeze
		}
	}
	nb := make([]ncell, naiveT)
	copy(nb, n.base[:s])
	for tm := s; tm < s+d; tm++ {
		nb[tm] = ncell{kind: 2, id: id, off: tm - s}
	}
	r = d
	for p := s; p < naiveT; p++ {
		c := n.base[p]
		if c.kind == 0 {
			if r > 0 {
				r--
			}
			continue
		}
		if q := p + r; q < naiveT {
			nb[q] = c
		}
	}
	n.base = nb
	n.ids[id] = true
	n.now = now
	return nil
}

func (n *naive) cancel(now int64, id string) error {
	if err := slot.ValidateIDNow(id, now); err != nil {
		return err
	}
	if now < n.now {
		return slot.ErrClock
	}
	if !n.ids[id] {
		return slot.ErrIDNotFound
	}
	alive := false
	for tm := now; tm < naiveT; tm++ {
		if n.base[tm].id == id || n.ovr[tm].id == id {
			alive = true
			break
		}
	}
	if !alive {
		return slot.ErrEnded
	}
	for tm := now; tm < naiveT; tm++ {
		if n.base[tm].id == id {
			n.base[tm] = ncell{}
		}
		if n.ovr[tm].id == id {
			n.ovr[tm] = ncell{}
		}
	}
	n.now = now
	return nil
}

func (n *naive) at(tm int64) Result {
	if c := n.ovr[tm]; c.kind != 0 {
		return Result{Kind: Override, ID: c.id, Offset: c.off}
	}
	switch c := n.base[tm]; c.kind {
	case 2:
		return Result{Kind: Override, ID: c.id, Offset: c.off}
	case 1:
		return Result{Kind: Program, ID: c.id, Offset: c.off}
	}
	gapStart := int64(0)
	for u := tm - 1; u >= 0; u-- {
		if n.base[u].kind != 0 {
			gapStart = u + 1
			break
		}
	}
	return Result{Kind: Filler, Offset: (tm - gapStart) % n.f}
}

// label 把错误归类为判定依据标签，用于比对两条实现。
func label(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, slot.ErrInvalid):
		return "参数非法"
	case errors.Is(err, slot.ErrClock):
		return "时钟回退"
	case errors.Is(err, slot.ErrIDExists):
		return "标识已存在"
	case errors.Is(err, slot.ErrIDNotFound):
		return "标识不存在"
	case errors.Is(err, slot.ErrPast):
		return "已过去"
	case errors.Is(err, slot.ErrOverlap):
		return "重叠"
	case errors.Is(err, slot.ErrEnded):
		return "已结束"
	case errors.Is(err, override.ErrConflict):
		return "插播冲突"
	case errors.Is(err, override.ErrFixedInterior):
		return "落在固定节目内"
	case errors.Is(err, override.ErrFixedSqueeze):
		return "挤占固定节目"
	}
	return "未知:" + err.Error()
}

// 1500 组随机操作序列与逐秒朴素模拟对照，日志打印输入、输出与判定依据。
func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261005))
	ids := []string{"A", "B", "C", "D", "E", "F", "G", "H"}
	for seq := 0; seq < 1500; seq++ {
		f := int64(1 + rng.Intn(8))
		tl := build(t, f)
		nv := newNaive(f)
		now := int64(0)
		for op := 0; op < 40; op++ {
			now += int64(rng.Intn(4))
			if rng.Intn(20) == 0 && now > 5 { // 偶发时钟回退
				now -= int64(rng.Intn(5))
			}
			id := ids[rng.Intn(len(ids))]
			start := now - 2 + int64(rng.Intn(120))
			if start < 0 {
				start = 0
			}
			dur := int64(1 + rng.Intn(30))
			var gerr, nerr error
			var desc string
			switch rng.Intn(10) {
			case 0, 1, 2, 3:
				fixed := rng.Intn(4) == 0
				gerr = tl.Schedule(now, id, start, dur, fixed)
				nerr = nv.schedule(now, id, start, dur, fixed)
				desc = fmt.Sprintf("Schedule(now=%d id=%s start=%d dur=%d fixed=%v)", now, id, start, dur, fixed)
			case 4, 5:
				gerr = override.Override(tl, now, id, start, dur, override.Preempt)
				nerr = nv.preempt(now, id, start, dur)
				desc = fmt.Sprintf("Override(preempt now=%d id=%s start=%d dur=%d)", now, id, start, dur)
			case 6, 7:
				gerr = override.Override(tl, now, id, start, dur, override.Shift)
				nerr = nv.shift(now, id, start, dur)
				desc = fmt.Sprintf("Override(shift now=%d id=%s start=%d dur=%d)", now, id, start, dur)
			default:
				gerr = tl.Cancel(now, id)
				nerr = nv.cancel(now, id)
				desc = fmt.Sprintf("Cancel(now=%d id=%s)", now, id)
			}
			gl, nl := label(gerr), label(nerr)
			t.Logf("seq=%d op=%d %s => 实现=%s 朴素=%s", seq, op, desc, gl, nl)
			if gl != nl {
				t.Fatalf("seq=%d op=%d %s: 实现判定 %s(%v)，朴素判定 %s(%v)", seq, op, desc, gl, gerr, nl, nerr)
			}
			for q := 0; q < 5; q++ {
				tm := int64(rng.Intn(700))
				got, want := At(tl, tm), nv.at(tm)
				if got != want {
					t.Fatalf("seq=%d op=%d %s: At(%d)=%+v，朴素=%+v", seq, op, desc, tm, got, want)
				}
			}
		}
	}
}

// 并发调用等价于某个串行顺序：并发排入重叠段时恰有一个成功。
func TestConcurrent(t *testing.T) {
	tl := build(t, 7)
	var wg sync.WaitGroup
	var wins atomic.Int64
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			if err := tl.Schedule(0, fmt.Sprintf("P%d", g), 100, 100, false); err == nil {
				wins.Add(1)
			}
		}(g)
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("重叠排入成功数 = %d, want 1", wins.Load())
	}
	// 第二阶段：并发插播、取消与查询（冒烟，配合 -race）。
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			id := fmt.Sprintf("P%d", g)
			_ = override.Override(tl, 0, "X"+id, 500+int64(g)*20, 10, override.Preempt)
			for i := 0; i < 50; i++ {
				At(tl, int64(i*7))
			}
			_ = tl.Cancel(0, id)
		}(g)
	}
	wg.Wait()
}
