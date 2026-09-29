package mview

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// renderView 将视图渲染为稳定的 "k=v" 有序串，供日志与断言使用。
func renderView(v View) string {
	if v == nil {
		return "-"
	}
	keys := make([]string, 0, len(v))
	for key := range v {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, v[key]))
	}
	return "{" + strings.Join(parts, " ") + "}"
}

// logOp 打印操作、已处理数、影子内容、当前视图与判定依据。
func logOp(t *testing.T, op string, s Status, basis string) {
	t.Helper()
	t.Logf("op=%-30s processed=%d rebuilding=%t shadow=%s view(gen=%d)=%s basis=%s",
		op, s.Processed, s.Rebuilding, renderView(s.Shadow), s.Generation,
		renderView(s.View), basis)
}

func assertView(t *testing.T, got View, want View, basis string) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("%s: view mismatch: got %s want %s", basis, renderView(got), renderView(want))
	}
}

func assertErrorIs(t *testing.T, err error, target error, basis string) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("%s: want error %v, got %v", basis, target, err)
	}
}

func TestInitialViewIsGenerationOne(t *testing.T) {
	r := New([]string{"a", "b", "a"})
	view, gen := r.Snapshot()
	assertView(t, view, View{"a": 2, "b": 1}, "initial snapshot")
	if gen != 1 {
		t.Fatalf("initial generation: got %d want 1", gen)
	}
	logOp(t, "New", r.Status(), "首代视图=从头重放 Replay")
}

// TestCrashResume 覆盖：分块重建 -> 块之间崩溃 -> 从上一检查点续跑
// -> 提交，且结果与从头重放完全一致（幂等可复现）。
func TestCrashResume(t *testing.T) {
	source := []string{"a", "b", "a", "c", "b", "a", "a"} // 7 个元素
	r := New([]string{"a", "b"})

	if err := r.BeginRebuild(source, 3); err != nil {
		t.Fatalf("begin: %v", err)
	}
	logOp(t, "BeginRebuild(chunk=3)", r.Status(), "影子为空，旧视图仍可读")

	advanced, err := r.Step() // 块1: a,b,a -> 检查点 {a:2 b:1}, processed=3
	if err != nil || !advanced {
		t.Fatalf("step1: advanced=%v err=%v", advanced, err)
	}
	logOp(t, "Step#1", r.Status(), "整块计数写入影子并落检查点")

	if err := r.Crash(); err != nil { // 块之间崩溃
		t.Fatalf("crash: %v", err)
	}
	logOp(t, "Crash", r.Status(), "只丢当前未完成块；processed/shadow 停在上一检查点")

	view, gen := r.Snapshot()
	assertView(t, view, View{"a": 1, "b": 1}, "crash 期间读到旧视图")
	if gen != 1 {
		t.Fatalf("generation stays 1 during crash, got %d", gen)
	}

	// 崩溃后未恢复直接提交必须被拒，且状态不变。
	assertErrorIs(t, r.Commit(), ErrRebuildIncomplete, "崩溃后未恢复即提交")
	logOp(t, "Commit(while crashed)->reject", r.Status(), "判定依据 ErrRebuildIncomplete，状态未变")

	advanced, err = r.Step() // 恢复：从检查点 processed=3 继续块2 c,b,a
	if err != nil || !advanced {
		t.Fatalf("resume step: advanced=%v err=%v", advanced, err)
	}
	logOp(t, "Step#2(resume)", r.Status(), "判定依据：从上一检查点继续，不重复计数块1")

	advanced, err = r.Step() // 块3（尾块）: a -> processed=7
	if err != nil || !advanced {
		t.Fatalf("final step: advanced=%v err=%v", advanced, err)
	}
	logOp(t, "Step#3(tail)", r.Status(), "尾块处理完，全部 7 个元素已落检查点")

	if advanced, err := r.Step(); err != nil || advanced {
		t.Fatalf("step past end: advanced=%v err=%v", advanced, err)
	}
	logOp(t, "Step#4(no-op)", r.Status(), "已处理完，重复步进不改变状态")

	if err := r.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	logOp(t, "Commit", r.Status(), "原子切换到新视图并递增代数")

	view, gen = r.Snapshot()
	assertView(t, view, Replay(source), "最终结果必须等于从头重放 Replay(source)")
	if gen != 2 {
		t.Fatalf("generation after rebuild: got %d want 2", gen)
	}

	// 可复现性：另起一个重建器一次性重放，结果必须一致。
	assertView(t, view, New(source).Status().View, "与全新一次性重放结果一致")
}

// TestOldViewReadableDuringRebuild 覆盖重建全程旧视图保持可读、影子不可见。
func TestOldViewReadableDuringRebuild(t *testing.T) {
	oldSource := []string{"x", "x"}
	newSource := []string{"y", "y", "y"}
	r := New(oldSource)

	if err := r.BeginRebuild(newSource, 2); err != nil {
		t.Fatalf("begin: %v", err)
	}
	for i := 1; ; i++ {
		advanced, err := r.Step()
		if err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
		logOp(t, fmt.Sprintf("Step#%d", i), r.Status(), "重建期间 Snapshot 必须仍是旧视图、旧代数")
		view, gen := r.Snapshot()
		assertView(t, view, View{"x": 2}, "重建中读到旧视图")
		if gen != 1 {
			t.Fatalf("重建中代数仍为 1, got %d", gen)
		}
		if !advanced {
			break
		}
	}

	if err := r.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	view, gen := r.Snapshot()
	assertView(t, view, Replay(newSource), "切换后读到新视图")
	if gen != 2 {
		t.Fatalf("切换后代数为 2, got %d", gen)
	}
	logOp(t, "Commit", r.Status(), "判定依据：提交后才可见新视图")
}

// TestRejectionPaths 覆盖全部必须整体拒绝、且一次失败不改变状态的场景。
func TestRejectionPaths(t *testing.T) {
	t.Run("invalid chunk size", func(t *testing.T) {
		r := New(nil)
		before := r.Status()
		assertErrorIs(t, r.BeginRebuild([]string{"a"}, 0), ErrInvalidChunkSize, "chunkSize=0")
		assertErrorIs(t, r.BeginRebuild([]string{"a"}, -1), ErrInvalidChunkSize, "chunkSize=-1")
		if after := r.Status(); !reflect.DeepEqual(after, before) {
			t.Fatalf("失败不得改变状态: before=%+v after=%+v", before, after)
		}
		logOp(t, "BeginRebuild(chunk=0)->reject", r.Status(), "判定依据 ErrInvalidChunkSize，状态未变")
	})

	t.Run("begin while rebuilding", func(t *testing.T) {
		r := New([]string{"a"})
		if err := r.BeginRebuild([]string{"b", "b"}, 1); err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := r.Step(); err != nil {
			t.Fatalf("step: %v", err)
		}
		before := r.Status()
		assertErrorIs(t, r.BeginRebuild([]string{"c"}, 1), ErrRebuildInProgress, "重复开始")
		if err := r.Crash(); err != nil {
			t.Fatalf("crash: %v", err)
		}
		assertErrorIs(t, r.BeginRebuild([]string{"c"}, 1), ErrRebuildInProgress, "崩溃待恢复时重复开始")
		after := r.Status()
		if after.Processed != before.Processed || after.Shadow["c"] != 0 {
			t.Fatalf("失败不得改变状态: before=%+v after=%+v", before, after)
		}
		logOp(t, "BeginRebuild(again)->reject", after, "判定依据 ErrRebuildInProgress")
	})

	t.Run("step commit crash without rebuild", func(t *testing.T) {
		r := New([]string{"a"})
		before := r.Status()
		_, err := r.Step()
		assertErrorIs(t, err, ErrNoRebuild, "空闲时步进")
		assertErrorIs(t, r.Commit(), ErrNoRebuild, "空闲时提交")
		assertErrorIs(t, r.Crash(), ErrNoRebuild, "空闲时崩溃")
		if after := r.Status(); !reflect.DeepEqual(after, before) {
			t.Fatalf("失败不得改变状态: before=%+v after=%+v", before, after)
		}
		logOp(t, "Step/Commit/Crash(idle)->reject", before, "判定依据 ErrNoRebuild，状态未变")
	})

	t.Run("commit before complete", func(t *testing.T) {
		r := New([]string{"a"})
		if err := r.BeginRebuild([]string{"b", "b", "b", "b"}, 2); err != nil {
			t.Fatalf("begin: %v", err)
		}
		if _, err := r.Step(); err != nil { // 只处理 1/2 块
			t.Fatalf("step: %v", err)
		}
		before := r.Status()
		assertErrorIs(t, r.Commit(), ErrRebuildIncomplete, "未处理完就提交")
		if after := r.Status(); !reflect.DeepEqual(after, before) {
			t.Fatalf("失败不得改变状态: before=%+v after=%+v", before, after)
		}
		view, gen := r.Snapshot()
		assertView(t, view, View{"a": 1}, "拒绝提交后旧视图仍可读")
		if gen != 1 {
			t.Fatalf("拒绝提交后代数不变, got %d", gen)
		}
		logOp(t, "Commit(incomplete)->reject", r.Status(), "判定依据 ErrRebuildIncomplete，可继续 Step")

		// 拒绝后仍可继续完成并正常提交，证明状态未被破坏。
		if _, err := r.Step(); err != nil {
			t.Fatalf("resume step: %v", err)
		}
		if err := r.Commit(); err != nil {
			t.Fatalf("commit after completion: %v", err)
		}
		logOp(t, "Commit(after completion)", r.Status(), "补齐后提交成功，证明失败未污染状态")
	})
}

// TestReplayEquivalenceAcrossChunkSizes 任意块大小（含多次崩溃）的重建
// 结果都必须等于从头重放，即始终可复现。
func TestReplayEquivalenceAcrossChunkSizes(t *testing.T) {
	source := []string{"a", "b", "a", "a", "c", "b", "a", "c", "b", "b", "a"}
	var lastStatus Status
	for _, chunk := range []int{1, 2, 3, 7, 100} {
		r := New([]string{"old"})
		if err := r.BeginRebuild(source, chunk); err != nil {
			t.Fatalf("chunk=%d begin: %v", chunk, err)
		}
		steps := 0
		for {
			advanced, err := r.Step()
			if err != nil {
				t.Fatalf("chunk=%d step: %v", chunk, err)
			}
			if advanced {
				steps++
			}
			if advanced && steps%2 == 0 { // 每隔一块崩一次，验证多次断点续跑
				if err := r.Crash(); err != nil {
					t.Fatalf("chunk=%d crash: %v", chunk, err)
				}
			}
			if !advanced {
				break
			}
		}
		if err := r.Commit(); err != nil {
			t.Fatalf("chunk=%d commit: %v", chunk, err)
		}
		view, _ := r.Snapshot()
		assertView(t, view, Replay(source), fmt.Sprintf("chunk=%d 多次崩溃后续跑结果", chunk))
		lastStatus = r.Status()
	}
	logOp(t, "ReplayEquivalence(chunks 1,2,3,7,100)", lastStatus, "判定依据：所有分块/崩溃组合 == Replay(source)")
}

// TestConcurrentReads 重建（含提交）期间并发查询：每一次读到的视图
// 必须对应某个完整提交边界，绝不出现半成品。
func TestConcurrentReads(t *testing.T) {
	r := New([]string{"z", "z"})
	newSource := make([]string, 0, 200)
	for i := 0; i < 100; i++ {
		newSource = append(newSource, "a", "b")
	}

	oldView := View{"z": 2}
	newView := Replay(newSource)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
					view, gen := r.Snapshot()
					switch gen {
					case 1:
						if !reflect.DeepEqual(view, oldView) {
							t.Errorf("读到非边界视图（gen=1）: %s", renderView(view))
							return
						}
					case 2:
						if !reflect.DeepEqual(view, newView) {
							t.Errorf("读到半成品视图（gen=2）: %s", renderView(view))
							return
						}
					default:
						t.Errorf("读到非法代数 %d", gen)
						return
					}
				}
			}
		}()
	}

	if err := r.BeginRebuild(newSource, 7); err != nil {
		t.Fatalf("begin: %v", err)
	}
	for {
		advanced, err := r.Step()
		if err != nil {
			t.Fatalf("step: %v", err)
		}
		if !advanced {
			break
		}
	}
	if err := r.Commit(); err != nil {
		t.Fatalf("commit: %v", err)
	}
	close(stop)
	wg.Wait()

	view, gen := r.Snapshot()
	assertView(t, view, newView, "并发结束后为新视图")
	if gen != 2 {
		t.Fatalf("最终代数: got %d want 2", gen)
	}
	logOp(t, "ConcurrentReads(8 readers)+Commit", r.Status(), "判定依据：每次读只可能是完整旧视图或完整新视图")
}
