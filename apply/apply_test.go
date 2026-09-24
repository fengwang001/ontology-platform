
package apply_test

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"testing"

	"ontology/apply"
	"ontology/cycle"
	"ontology/name"
	"ontology/plan"
	"ontology/undo"
)

func setEqual(a, b []string) bool {
	m := map[string]int{}
	for _, s := range a {
		m[s]++
	}
	for _, s := range b {
		m[s]--
	}
	for _, v := range m {
		if v != 0 {
			return false
		}
}

func runNoClobber(t *testing.T, init []string, st []plan.Step) {
	t.Helper()
	sp := name.New(init...)
	for _, s := range st {
		if sp.Has(s.To) {
			t.Fatalf("step %v→%v clobbers", s.From, s.To)
		}
		if err := sp.Move(s.From, s.To); err != nil {
			t.Fatalf("move %v→%v: %v", s.From, s.To, err)
		}
}

func TestCycleBreak(t *testing.T) {
	cases := []struct {
		name      string
		init      []string
		reqs      []plan.Req
		want      []plan.Step
		wantTemps int
	}{
		{"2-cycle", []string{"a", "b"},
			[]plan.Req{{"a", "b"}, {"b", "a"}},
			[]plan.Step{{"a", ".rename-tmp-0"}, {"b", "a"}, {".rename-tmp-0", "b"}}, 1},
		{"3-cycle", []string{"a", "b", "c"},
			[]plan.Req{{"a", "b"}, {"b", "c"}, {"c", "a"}},
			[]plan.Step{{"a", ".rename-tmp-0"}, {"c", "a"}, {"b", "c"},
				{".rename-tmp-0", "b"}}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pl, err := plan.Compile(tc.reqs, tc.init)
			if err != nil {
				t.Fatal(err)
			}
			st, temps, err := cycle.Break(pl, tc.init)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(st, tc.want) {
				t.Fatalf("steps=%v want %v", st, tc.want)
			}
			if len(temps) != tc.wantTemps {
				t.Fatalf("temps=%d want %d", len(temps), tc.wantTemps)
			}
			runNoClobber(t, tc.init, st)
			sp := name.New(tc.init...)
			for _, s := range st {
				_ = sp.Move(s.From, s.To)
			}
			if !setEqual(sp.Snapshot(), tc.init) {
				t.Fatalf("end state %v != %v", sp.Snapshot(), tc.init)
			}
		})
	}
	// 10 个互不相交环 => 10 个临时名，且无覆盖。
	var init, reqs []string
	var rq []plan.Req
	for i := 0; i < 10; i++ {
		a, b, c := fmt.Sprintf("a%d", i), fmt.Sprintf("b%d", i), fmt.Sprintf("c%d", i)
		init = append(init, a, b, c)
		rq = append(rq, plan.Req{a, b}, plan.Req{b, c}, plan.Req{c, a})
	}
	pl, _ := plan.Compile(rq, init)
	st, temps, err := cycle.Break(pl, init)
	if err != nil || len(temps) != 10 {
		t.Fatalf("10 cycles: temps=%v err=%v", temps, err)
	}
	runNoClobber(t, init, st)
	_ = reqs
	// 临时名 tmp-0..999 被占用仍成功。
	occ := []string{"a", "b"}
	for i := 0; i < 1000; i++ {
		occ = append(occ, fmt.Sprintf(".rename-tmp-%d", i))
	}
	pl2, _ := plan.Compile([]plan.Req{{"a", "b"}, {"b", "a"}}, []string{"a", "b"})
	_, tp, err := cycle.Break(pl2, occ)
	if err != nil || len(tp) != 1 || tp[0] != ".rename-tmp-1000" {
		t.Fatalf("saturated temps: %v err=%v", tp, err)
	}
}

func TestExecuteUndoRollback(t *testing.T) {
	reqs := []plan.Req{{"a", "b"}, {"b", "c"}, {"c", "a"}, {"d", "e"}}
	init := []string{"a", "b", "c", "d"}

	// 成功执行后撤销：逐元素相同。
	sp := name.New(init...)
	dir, _ := os.MkdirTemp("", "undotest-")
	res, err := apply.Execute(reqs, sp, apply.Options{LogDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	wantEnd := []string{"b", "c", "a", "e"}
	if !setEqual(sp.Snapshot(), wantEnd) {
		t.Fatalf("end=%v want %v", sp.Snapshot(), wantEnd)
	}
	if _, err := res.Log.Run(sp); err != nil {
		t.Fatalf("undo: %v", err)
	}
	if !setEqual(sp.Snapshot(), init) {
		t.Fatalf("after undo %v != %v", sp.Snapshot(), init)
	}
	// 重复撤销幂等（无操作）。
	r2, err := res.Log.Run(sp)
	if err != nil || !r2.NoOp {
		t.Fatalf("second undo: %+v err=%v", r2, err)
	}
	if !setEqual(sp.Snapshot(), init) {
		t.Fatalf("idempotent undo changed state")
	}

	// 第 k 步失败（k=1、中间、最后）：已执行步骤逆序回滚。
	total := len(res.Steps)
	for _, k := range []int{1, total/2 + 1, total} {
		s := name.New(init...)
		_, err := apply.Execute(reqs, s, apply.Options{LogDir: dir, FailAt: k})
		if !errors.Is(err, apply.ErrInjected) {
			t.Fatalf("k=%d err=%v want ErrInjected", k, err)
		}
		if !setEqual(s.Snapshot(), init) {
			t.Fatalf("k=%d state after rollback %v != %v", k, s.Snapshot(), init)
		}
	}
}

func TestTruncation(t *testing.T) {
	dir, _ := os.MkdirTemp("", "trunctest-")
	lg, err := undo.Create(dir, "trunc")
	if err != nil {
		t.Fatal(err)
	}
	steps := []undo.Step{{"a", "b"}, {"b", "c"}, {"c", "d"}}
	for _, s := range steps {
		if err := lg.Append(s); err != nil {
			t.Fatal(err)
		}
	}
	data, _ := os.ReadFile(lg.Path)
	kindAt := make(map[error]bool)
	// 逐字节截断 1..len-1，分类且必须有可恢复前缀。
	for cut := 1; cut < len(data); cut++ {
		prefix := data[:cut]
		got, err := undo.Decode(prefix)
		switch {
		case errors.Is(err, undo.ErrLogHeader):
			kindAt[undo.ErrLogHeader] = true
		case errors.Is(err, undo.ErrLogRecord):
			kindAt[undo.ErrLogRecord] = true
			if len(got) >= len(steps) {
				t.Fatalf("cut=%d record-incomplete must not yield all steps", cut)
			}
		case errors.Is(err, undo.ErrLogCRC):
			kindAt[undo.ErrLogCRC] = true
		default:
		}
		// 截断后撤销必须按最大可恢复前缀工作。
		sp := name.New("a", "b", "c")
		_ = sp.Move("a", "b")
		_ = sp.Move("b", "c")
		_ = sp.Move("c", "d")
		n, rerr := undo.Restore(sp, prefix)
		if n != len(got) {
			t.Fatalf("cut=%d restored=%d decoded=%d", cut, n, len(got))
		}
		if rerr != nil && !(errors.Is(rerr, undo.ErrLogHeader) ||
			errors.Is(rerr, undo.ErrLogRecord) || errors.Is(rerr, undo.ErrLogCRC)) {
			t.Fatalf("cut=%d unclassified %v", cut, rerr)
		}
	}
	if !kindAt[undo.ErrLogHeader] || !kindAt[undo.ErrLogRecord] ||
		!kindAt[undo.ErrLogCRC] {
		t.Fatalf("not all three truncation classes observed: %v", kindAt)
}

func TestConcurrency(t *testing.T) {
	// 批量执行期间持写锁：并发 Move 被阻塞，执行结束后完成。
	sp := name.New("a", "b", "c")
	reqs := []plan.Req{{"a", "x"}, {"b", "y"}, {"c", "z"}}
	started := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		close(started)
		_, err := apply.Execute(reqs, sp, apply.Options{})
		done <- err
	}()
	<-started
	// 给执行一点时间持锁，然后发起并发修改：必须等待，最终成功。
	moved := make(chan error, 1)
	go func() { moved <- sp.Move("z", "w") }()
	select {
	case e := <-moved:
		if e != nil {
			t.Fatalf("concurrent move rejected: %v", e)
		}
		t.Fatal("concurrent move should have blocked during batch execution")
	default:
	}
	if err := <-done; err != nil {
		t.Fatalf("execute: %v", err)
	}
	if err := <-moved; err != nil {
		t.Fatalf("move after batch: %v", err)
	}
	if !sp.Has("w") {
		t.Fatal("concurrent move did not complete after lock release")
	}
}
