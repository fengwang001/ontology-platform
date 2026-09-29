package rename

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

func newTestLogger() (*bytes.Buffer, *slog.Logger) {
	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return buf, logger
}

func mustSubmit(t *testing.T, ex *Executor, renames []Rename, hook FailureHook) {
	t.Helper()
	if err := ex.Submit(context.Background(), renames, hook); err != nil {
		t.Fatalf("Submit(%v) unexpected error: %v", renames, err)
	}
}

func assertNames(t *testing.T, ns *Namespace, want []string) {
	t.Helper()
	got := ns.Snapshot()
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("namespace = %v, want %v", got, want)
	}
}

func toSet(names []string) map[string]struct{} {
	set := make(map[string]struct{}, len(names))
	for _, name := range names {
		set[name] = struct{}{}
	}
	return set
}

// 互换两名是合法的：一个环恰用一个临时名；映射到自身为无操作。
func TestSwapAndSelfRename(t *testing.T) {
	ns := NewNamespace("a", "b", "c")
	buf, logger := newTestLogger()
	ex := NewExecutor(ns, "__tmp_", logger)

	mustSubmit(t, ex, []Rename{{Old: "a", New: "b"}, {Old: "b", New: "a"}, {Old: "c", New: "c"}}, nil)
	assertNames(t, ns, []string{"a", "b", "c"})

	if !strings.Contains(buf.String(), "temporary_names=[__tmp_0]") {
		t.Fatalf("expected exactly one temporary name in log, got:\n%s", buf.String())
	}

	// 互换后 a 位置上是原来的 b，可通过后续改名验证内容确实交换。
	mustSubmit(t, ex, []Rename{{Old: "a", New: "a2"}}, nil)
	assertNames(t, ns, []string{"a2", "b", "c"})
}

// 长链 a->b->c->d->e：先搬走占用目标的名字，从末端开始执行，不使用临时名。
func TestLongChain(t *testing.T) {
	ns := NewNamespace("a", "b", "c", "d")
	buf, logger := newTestLogger()
	ex := NewExecutor(ns, "__tmp_", logger)

	mustSubmit(t, ex, []Rename{
		{Old: "a", New: "b"},
		{Old: "b", New: "c"},
		{Old: "c", New: "d"},
		{Old: "d", New: "e"},
	}, nil)
	assertNames(t, ns, []string{"b", "c", "d", "e"})

	log := buf.String()
	if !strings.Contains(log, "steps=\"[{From:d To:e} {From:c To:d} {From:b To:c} {From:a To:b}]\"") ||
		!strings.Contains(log, "temporary_names=[]") {
		t.Fatalf("chain must use no temporary names and run in terminal-first order, got:\n%s", log)
	}

	if err := ex.Undo(context.Background()); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	assertNames(t, ns, []string{"a", "b", "c", "d"})
}

// 十个互不相交的环必须恰用十个临时名。
func TestTenDisjointCyclesUseTenTemporaryNames(t *testing.T) {
	var initial []string
	var renames []Rename
	for i := 0; i < 10; i++ {
		x := fmt.Sprintf("n%02dx", i)
		y := fmt.Sprintf("n%02dy", i)
		initial = append(initial, x, y)
		renames = append(renames, Rename{Old: x, New: y}, Rename{Old: y, New: x})
	}

	ns := NewNamespace(initial...)
	buf, logger := newTestLogger()
	ex := NewExecutor(ns, "__tmp_", logger)

	mustSubmit(t, ex, renames, nil)
	assertNames(t, ns, initial)

	wantTemps := "[__tmp_0 __tmp_1 __tmp_2 __tmp_3 __tmp_4 __tmp_5 __tmp_6 __tmp_7 __tmp_8 __tmp_9]"
	log := buf.String()
	if !strings.Contains(log, wantTemps) {
		t.Fatalf("expected exactly ten temporary names %s, got:\n%s", wantTemps, log)
	}

	plan, err := buildPlan(toSet(initial), renames, "__tmp_")
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if len(plan.temp) != 10 {
		t.Fatalf("expected 10 temporary names, got %d: %v", len(plan.temp), plan.temp)
	}
	for i, got := range plan.temp {
		if want := fmt.Sprintf("__tmp_%d", i); got != want {
			t.Fatalf("temp[%d] = %q, want %q", i, got, want)
		}
	}

	if err := ex.Undo(context.Background()); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	assertNames(t, ns, initial)
}

// 最小序号的临时名被占用或与本批名字冲突时顺延。
func TestTemporaryNameSkipsOccupied(t *testing.T) {
	// 互换 a/b 构成环。__tmp_0 已被占用；同时安排一条以 __tmp_1 为新名的
	// 无操作邻接映射使 __tmp_1 出现在本批名字中（它随后仍被 a 释放），
	// 因此破环临时名只能顺延到 __tmp_2。
	ns := NewNamespace("a", "b", "__tmp_0", "__tmp_1")
	buf, logger := newTestLogger()
	ex := NewExecutor(ns, "__tmp_", logger)

	mustSubmit(t, ex, []Rename{
		{Old: "__tmp_1", New: "__tmp_1"}, // 自映射：让 __tmp_1 进入“本批名字”集合
		{Old: "a", New: "b"},
		{Old: "b", New: "a"},
	}, nil)

	if !strings.Contains(buf.String(), "temporary_names=[__tmp_2]") {
		t.Fatalf("temporary name should skip to __tmp_2, got:\n%s", buf.String())
	}
}

// 在第 k 步注入失败：该步不生效，此前步骤逆序撤回，命名空间逐项复原。
func TestFailureAtStepKRollsBack(t *testing.T) {
	for k := 0; k < 4; k++ {
		t.Run(fmt.Sprintf("k=%d", k), func(t *testing.T) {
			ns := NewNamespace("a", "b", "c", "d")
			_, logger := newTestLogger()
			ex := NewExecutor(ns, "__tmp_", logger)

			injected := errors.New("injected failure")
			hook := func(stepIndex int, _, _ string) error {
				if stepIndex == k {
					return injected
				}
				return nil
			}
			err := ex.Submit(context.Background(), []Rename{
				{Old: "a", New: "b"},
				{Old: "b", New: "c"},
				{Old: "c", New: "d"},
				{Old: "d", New: "e"},
			}, hook)

			var execErr *ExecError
			if !errors.As(err, &execErr) || execErr.Step != k || !errors.Is(err, injected) {
				t.Fatalf("k=%d: expected *ExecError at step %d wrapping injected error, got %v", k, k, err)
			}
			assertNames(t, ns, []string{"a", "b", "c", "d"})
			if ex.LastBatchID() != 0 {
				t.Fatalf("failed batch must not be recorded as successful")
			}

			// 失败后仍可成功提交并撤销。
			mustSubmit(t, ex, []Rename{{Old: "a", New: "z"}}, nil)
			assertNames(t, ns, []string{"z", "b", "c", "d"})
			if err := ex.Undo(context.Background()); err != nil {
				t.Fatalf("Undo: %v", err)
			}
			assertNames(t, ns, []string{"a", "b", "c", "d"})
		})
	}
}

// 撤销只作用于最近一次成功批次且只能一次；其后已有新批次时撤销的是新批次，
// 不能再对更早的批次重复撤销。
func TestUndoAndDuplicateUndo(t *testing.T) {
	ns := NewNamespace("a", "b", "c")
	_, logger := newTestLogger()
	ex := NewExecutor(ns, "__tmp_", logger)

	var undoErr *UndoError
	if err := ex.Undo(context.Background()); !errors.As(err, &undoErr) || undoErr.Reason != "nothing_to_undo" {
		t.Fatalf("undo with no successful batch must be rejected, got %v", err)
	}

	mustSubmit(t, ex, []Rename{{Old: "a", New: "a1"}}, nil)
	mustSubmit(t, ex, []Rename{{Old: "b", New: "b1"}}, nil)

	if err := ex.Undo(context.Background()); err != nil {
		t.Fatalf("Undo latest: %v", err)
	}
	assertNames(t, ns, []string{"a1", "b", "c"}) // 只撤销最近一批

	if err := ex.Undo(context.Background()); !errors.As(err, &undoErr) || undoErr.Reason != "nothing_to_undo" {
		t.Fatalf("duplicate undo must be rejected with nothing_to_undo, got %v", err)
	}
	assertNames(t, ns, []string{"a1", "b", "c"})

	mustSubmit(t, ex, []Rename{{Old: "c", New: "c1"}}, nil)
	if err := ex.Undo(context.Background()); err != nil {
		t.Fatalf("Undo new batch: %v", err)
	}
	assertNames(t, ns, []string{"a1", "b", "c"})
	if err := ex.Undo(context.Background()); !errors.As(err, &undoErr) {
		t.Fatalf("undo after undoing new batch must be rejected, got %v", err)
	}
}

// 五类校验错误必须整体拒绝、原因可区分，且不改任何名字。
func TestValidationRejections(t *testing.T) {
	cases := []struct {
		name   string
		names  []string
		input  []Rename
		reason Reason
	}{
		{"empty old", []string{"a"}, []Rename{{Old: "", New: "x"}}, ReasonEmptyName},
		{"empty new", []string{"a"}, []Rename{{Old: "a", New: ""}}, ReasonEmptyName},
		{"duplicate old", []string{"a", "b"}, []Rename{{Old: "a", New: "x"}, {Old: "a", New: "y"}}, ReasonDuplicateOld},
		{"duplicate new", []string{"a", "b"}, []Rename{{Old: "a", New: "x"}, {Old: "b", New: "x"}}, ReasonDuplicateNew},
		{"old missing", []string{"a"}, []Rename{{Old: "a", New: "x"}, {Old: "b", New: "y"}}, ReasonOldNotFound},
		{"new occupied", []string{"a", "x"}, []Rename{{Old: "a", New: "x"}}, ReasonNewOccupied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ns := NewNamespace(tc.names...)
			_, logger := newTestLogger()
			ex := NewExecutor(ns, "__tmp_", logger)

			err := ex.Submit(context.Background(), tc.input, nil)
			var ve *ValidationError
			if !errors.As(err, &ve) || ve.Reason != tc.reason {
				t.Fatalf("want ValidationError reason %q, got %v", tc.reason, err)
			}
			assertNames(t, ns, tc.names)
			if ex.LastBatchID() != 0 {
				t.Fatal("rejected batch must not be recorded")
			}
		})
	}
}

// 互换时新名“已存在但在本批被搬走”是合法的，不应误报 new_already_occupied。
func TestOccupiedTargetMovedByBatchIsLegal(t *testing.T) {
	ns := NewNamespace("a", "b")
	_, logger := newTestLogger()
	ex := NewExecutor(ns, "__tmp_", logger)
	mustSubmit(t, ex, []Rename{{Old: "a", New: "b"}, {Old: "b", New: "a"}}, nil)
	assertNames(t, ns, []string{"a", "b"})
}

// 结果与映射项给出顺序无关：打乱后的同一批映射产生相同步骤序列。
func TestOrderIndependence(t *testing.T) {
	base := []Rename{
		{Old: "a", New: "b"}, {Old: "b", New: "a"},
		{Old: "c", New: "d"}, {Old: "d", New: "c"},
		{Old: "x", New: "y"}, {Old: "y", New: "z"},
	}
	shuffled := []Rename{base[5], base[1], base[3], base[0], base[4], base[2]}

	planA, errA := buildPlan(toSet([]string{"a", "b", "c", "d", "x", "y"}), base, "t")
	planB, errB := buildPlan(toSet([]string{"a", "b", "c", "d", "x", "y"}), shuffled, "t")
	if errA != nil || errB != nil {
		t.Fatalf("buildPlan: %v %v", errA, errB)
	}
	if !reflect.DeepEqual(planA.steps, planB.steps) {
		t.Fatalf("step order depends on input order:\n%v\n%v", planA.steps, planB.steps)
	}
	if !reflect.DeepEqual(planA.temp, planB.temp) {
		t.Fatalf("temporary names depend on input order:\n%v\n%v", planA.temp, planB.temp)
	}

	// 环 a<->b（最小名 a）先于环 c<->d，链最后；每环首步最小名 -> 临时名。
	want := []step{
		{From: "a", To: "t0"}, {From: "b", To: "a"}, {From: "t0", To: "b"},
		{From: "c", To: "t1"}, {From: "d", To: "c"}, {From: "t1", To: "d"},
		{From: "y", To: "z"}, {From: "x", To: "y"},
	}
	if !reflect.DeepEqual(planA.steps, want) {
		t.Fatalf("unexpected deterministic order:\n got %v\nwant %v", planA.steps, want)
	}
}

// 并发批次彼此串行生效；并发读者永远看不到临时名或半批状态。
func TestConcurrentBatchesAndReaders(t *testing.T) {
	ns := NewNamespace("a", "b", "c", "d", "e", "f")
	_, logger := newTestLogger()
	ex := NewExecutor(ns, "__tmp_", logger)

	stop := make(chan struct{})
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
				current := ns.Snapshot()
				sort.Strings(current)
				for _, name := range current {
					if strings.HasPrefix(name, "__tmp_") {
						t.Errorf("reader observed temporary name %q", name)
						close(stop)
						return
					}
				}
			}
		}
	}()

	batches := [][]Rename{
		{{Old: "a", New: "b"}, {Old: "b", New: "a"}},
		{{Old: "c", New: "d"}, {Old: "d", New: "c"}},
		{{Old: "e", New: "f"}, {Old: "f", New: "e"}},
	}
	var submitWg sync.WaitGroup
	for _, batch := range batches {
		submitWg.Add(1)
		batch := batch
		go func() {
			defer submitWg.Done()
			if err := ex.Submit(context.Background(), batch, nil); err != nil {
				t.Errorf("concurrent Submit: %v", err)
			}
		}()
	}
	submitWg.Wait()
	close(stop)
	wg.Wait()

	// 三个批次互不相交，全部串行成功；互换批次后名字集合不变。
	assertNames(t, ns, []string{"a", "b", "c", "d", "e", "f"})

	if err := ex.Undo(context.Background()); err != nil {
		t.Fatalf("Undo: %v", err)
	}
	assertNames(t, ns, []string{"a", "b", "c", "d", "e", "f"})
}

// 日志中必须打印输入、输出与判定依据。
func TestLogsContainInputOutputAndBasis(t *testing.T) {
	ns := NewNamespace("a", "b")
	buf, logger := newTestLogger()
	ex := NewExecutor(ns, "__tmp_", logger)

	mustSubmit(t, ex, []Rename{{Old: "a", New: "b"}, {Old: "b", New: "a"}}, nil)
	if err := ex.Undo(context.Background()); err != nil {
		t.Fatalf("Undo: %v", err)
	}

	log := buf.String()
	for _, want := range []string{
		"batch submitted",
		"input=",
		"plan derived",
		"batch committed",
		"output=",
		"basis=",
		"batch undone",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q, full log:\n%s", want, log)
		}
	}

	// 拒绝路径同样记录输入与原因。
	err := ex.Submit(context.Background(), []Rename{{Old: "a", New: ""}}, nil)
	if err == nil {
		t.Fatal("expected rejection")
	}
	if !strings.Contains(buf.String(), "batch rejected") ||
		!strings.Contains(buf.String(), "reason=empty_name") {
		t.Fatalf("rejection log missing reason, got:\n%s", buf.String())
	}
}
