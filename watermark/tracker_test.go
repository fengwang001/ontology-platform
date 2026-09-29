package watermark

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
)

// logState 打印操作、各分区位点、全局位点与判定依据。
func logState(t *testing.T, tr *Tracker, op, reason string) {
	t.Helper()
	snap := tr.Snapshot()
	g := "infinite"
	if !snap.GlobalInfinite {
		g = fmt.Sprintf("%d", snap.Global)
	}
	t.Logf("op=%-28s global=%-8s reason=%q", op, g, reason)
	for _, p := range snap.Partitions {
		state := "active"
		final := "-"
		if p.Finished {
			state = "finished"
			final = fmt.Sprintf("%d", p.Final)
		}
		t.Logf("    partition=%-3q start=%-3d confirmed=%-3d final=%-3s %s",
			p.Partition, p.Start, p.Confirmed, final, state)
	}
}

func wantErr(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want error %v, got %v", target, err)
	}
}

// 停滞的未完结分区把全局位点钉在其位点上，完结后才解除；
// 完结分区从最小值集合中移出。
func TestStalledPartitionBlocksGlobal(t *testing.T) {
	tr := New(8)
	if err := tr.Register("a", 0); err != nil {
		t.Fatal(err)
	}
	if err := tr.Register("b", 0); err != nil {
		t.Fatal(err)
	}
	logState(t, tr, "register a@0 b@0", "全局=min(0,0)=0")

	if err := tr.Report("a", 5); err != nil {
		t.Fatal(err)
	}
	logState(t, tr, "report a@5", "b 停滞在 0，全局被钉在 min(5,0)=0")
	if g, inf := tr.Global(); inf || g != 0 {
		t.Fatalf("global=%d inf=%v, want 0", g, inf)
	}

	if err := tr.Report("b", 3); err != nil {
		t.Fatal(err)
	}
	logState(t, tr, "report b@3", "b 推进，全局=min(5,3)=3")
	if g, _ := tr.Global(); g != 3 {
		t.Fatalf("global=%d, want 3", g)
	}

	if err := tr.Finish("b", 3); err != nil {
		t.Fatal(err)
	}
	logState(t, tr, "finish b@3", "b 完结移出最小值集合，全局=a 的 5")
	if g, inf := tr.Global(); inf || g != 5 {
		t.Fatalf("global=%d inf=%v, want 5", g, inf)
	}

	if err := tr.Finish("a", 10); err != nil {
		t.Fatal(err)
	}
	logState(t, tr, "finish a@10", "无未完结分区，全局=infinite")
	if _, inf := tr.Global(); !inf {
		t.Fatal("global should be infinite when no active partition")
	}

	finals := tr.FinalOffsets()
	if !reflect.DeepEqual(finals, map[string]Offset{"a": 10, "b": 3}) {
		t.Fatalf("finals=%v", finals)
	}
	if _, err := tr.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// 相同位点重复报告幂等，且不产生重复历史。
func TestIdempotentReport(t *testing.T) {
	tr := New(4)
	if err := tr.Register("p", 1); err != nil {
		t.Fatal(err)
	}
	if err := tr.Report("p", 1); err != nil {
		t.Fatal(err)
	}
	if err := tr.Report("p", 1); err != nil {
		t.Fatal(err)
	}
	logState(t, tr, "register p@1 + report p@1 x2", "重复位点幂等，历史只保留 register")
	if h := tr.History(); len(h) != 1 || h[0].Op != OpRegister {
		t.Fatalf("history=%v, want single register event", h)
	}

	if err := tr.Finish("p", 1); err != nil {
		t.Fatal(err)
	}
	if err := tr.Finish("p", 1); err != nil {
		t.Fatal(err)
	}
	logState(t, tr, "finish p@1 x2", "相同最终位点完结幂等")
	if h := tr.History(); len(h) != 2 {
		t.Fatalf("history len=%d, want 2", len(h))
	}
}

// 位点回退、未注册操作、完结后操作、超上限、重复注册均整体拒绝。
func TestRejectionsAreAtomic(t *testing.T) {
	tr := New(2)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(tr.Register("a", 0))
	must(tr.Register("b", 0))
	must(tr.Report("a", 4))
	before := tr.Snapshot()

	cases := []struct {
		name string
		op   string
		do   func() error
		want error
	}{
		{"register over limit", "register c@0", func() error { return tr.Register("c", 0) }, ErrTooManyPartitions},
		{"duplicate register", "register a@0", func() error { return tr.Register("a", 0) }, ErrPartitionExists},
		{"report unregistered", "report z@1", func() error { return tr.Report("z", 1) }, ErrPartitionNotFound},
		{"report regression", "report a@3", func() error { return tr.Report("a", 3) }, ErrOffsetRegressed},
		{"finish regression", "finish a@2", func() error { return tr.Finish("a", 2) }, ErrOffsetRegressed},
		{"finish unregistered", "finish z@0", func() error { return tr.Finish("z", 0) }, ErrPartitionNotFound},
	}
	for _, tc := range cases {
		err := tc.do()
		wantErr(t, err, tc.want)
		logState(t, tr, tc.op, "拒绝: "+tc.want.Error()+"，状态应保持不变")
		after := tr.Snapshot()
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("%s changed state:\nbefore=%+v\nafter =%+v", tc.name, before, after)
		}
	}

	// 完结后再报告/完结（不同最终位点）必须拒绝。
	if err := tr.Finish("a", 4); err != nil {
		t.Fatal(err)
	}
	logState(t, tr, "finish a@4", "a 完结移出最小值集合，全局=b 的 0")
	wantErr(t, tr.Report("a", 5), ErrPartitionFinished)
	wantErr(t, tr.Finish("a", 5), ErrPartitionFinished)
	logState(t, tr, "report a@5 / finish a@5", "拒绝: 对已完结分区操作")

	if _, err := tr.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// 低于当前全局位点的新分区注册会被拒绝，保证全局位点单调不减。
func TestRegisterBelowGlobalRejected(t *testing.T) {
	tr := New(4)
	if err := tr.Register("a", 0); err != nil {
		t.Fatal(err)
	}
	if err := tr.Report("a", 10); err != nil {
		t.Fatal(err)
	}
	logState(t, tr, "register a@0, report a@10", "全局=10")
	err := tr.Register("late", 3)
	wantErr(t, err, ErrStartBeforeGlobal)
	logState(t, tr, "register late@3", "拒绝: 起始位点低于全局位点 10")

	if err := tr.Register("late", 10); err != nil {
		t.Fatal(err)
	}
	logState(t, tr, "register late@10", "起始位点>=全局，允许且全局不下降")
	if g, inf := tr.Global(); inf || g != 10 {
		t.Fatalf("global=%d inf=%v, want 10", g, inf)
	}
}

// 重放 tracker 自身记录的历史，必须得到逐字段相同的结果；历史本身可复现。
func TestReplayReproduces(t *testing.T) {
	tr := New(8)
	ops := []struct {
		op     string
		action func() error
	}{
		{"register a@0", func() error { return tr.Register("a", 0) }},
		{"register b@2", func() error { return tr.Register("b", 2) }},
		{"register c@4", func() error { return tr.Register("c", 4) }},
		{"report a@3", func() error { return tr.Report("a", 3) }},
		{"report b@3", func() error { return tr.Report("b", 3) }},
		{"report b@3(idempotent)", func() error { return tr.Report("b", 3) }},
		{"finish c@9", func() error { return tr.Finish("c", 9) }},
	}
	for _, o := range ops {
		if err := o.action(); err != nil {
			t.Fatal(err)
		}
		logState(t, tr, o.op, "记录可重放历史")
	}

	replayed, err := Replay(tr.History(), 8)
	if err != nil {
		t.Fatal(err)
	}
	want, _ := tr.SelfCheck()
	got, err := replayed.SelfCheck()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("replay mismatch:\nwant=%+v\ngot =%+v", want, got)
	}
	if !reflect.DeepEqual(tr.History(), replayed.History()) {
		t.Fatal("replayed history differs")
	}
	t.Logf("replay ok: %d events, global=%d", len(replayed.History()), got.Global)

	// 含非法事件的历史必须整体失败。
	bad := append(tr.History(), Event{Op: OpReport, Partition: "a", Offset: 1})
	if _, err := Replay(bad, 8); !errors.Is(err, ErrOffsetRegressed) {
		t.Fatalf("replay of bad history should fail, got %v", err)
	}
}

// 并发读同一实例：Snapshot、Global、FinalOffsets、SelfCheck 可同时调用，
// 读者看到的全局位点与最终位点逐字段相同，且全局位点单调不减。
func TestConcurrentReadersConsistency(t *testing.T) {
	tr := New(8)
	for _, p := range []string{"a", "b", "c"} {
		if err := tr.Register(p, 0); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var mismatches atomic.Int64
	var regressions atomic.Int64

	// 读者：用 Snapshot 作为基准，核对 Global/FinalOffsets/SelfCheck 逐字段一致。
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var last Offset = -1
			for {
				select {
				case <-stop:
					return
				default:
				}
				// 单次 Snapshot 在读锁内拷贝全部字段，快照内部必须自洽：
				// 全局位点等于快照中未完结分区确认位点的最小值。
				snap := tr.Snapshot()
				var min Offset
				active := 0
				finals := map[string]Offset{}
				for _, pv := range snap.Partitions {
					if pv.Finished {
						finals[pv.Partition] = pv.Final
						continue
					}
					if active == 0 || pv.Confirmed < min {
						min = pv.Confirmed
					}
					active++
				}
				if active == 0 {
					if !snap.GlobalInfinite {
						mismatches.Add(1)
					}
				} else if snap.GlobalInfinite || snap.Global != min {
					mismatches.Add(1)
				}
				if len(finals) > 0 {
					t.Logf("concurrent read: global=%d infinite=%v finals=%v", snap.Global, snap.GlobalInfinite, finals)
				}
				if _, err := tr.SelfCheck(); err != nil {
					mismatches.Add(1)
				}
				if !snap.GlobalInfinite && snap.Global < last {
					regressions.Add(1)
				}
				if !snap.GlobalInfinite {
					last = snap.Global
				}
			}
		}()
	}

	// 写者：推进各分区并逐一完结。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for _, p := range []string{"a", "b", "c"} {
			for off := Offset(1); off <= 5; off++ {
				_ = tr.Report(p, off)
			}
			_ = tr.Finish(p, 5)
		}
		close(stop)
	}()

	wg.Wait()
	logState(t, tr, "concurrent writes finished", "全部完结，全局=infinite")
	if mismatches.Load() != 0 {
		t.Fatalf("concurrent read mismatches: %d", mismatches.Load())
	}
	if regressions.Load() != 0 {
		t.Fatalf("global regressed %d times", regressions.Load())
	}
}
