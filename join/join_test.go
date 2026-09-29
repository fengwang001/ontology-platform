package join

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

func ins(side Side, id, key string) Change { return Change{side, Insert, Row{id, key}} }
func del(side Side, id, key string) Change { return Change{side, Delete, Row{id, key}} }

// logBatch 打印输入批、产生的输出条目与判定依据，并回放日志校验视图。
func logBatch(t *testing.T, j *Joiner, batch []Change) {
	t.Helper()
	before := len(j.Log())
	if err := j.Apply(batch); err != nil {
		t.Fatalf("Apply(%v) failed: %v", batch, err)
	}
	for _, c := range batch {
		t.Logf("input : %s %s id=%s key=%s", c.Side, c.Op, c.Row.ID, c.Row.Key)
	}
	for _, e := range j.Log()[before:] {
		t.Logf("output: seq=%d %s left=%s right=%q key=%s reason=%s",
			e.Seq, e.Op, e.LeftID, e.RightID, e.Key, e.Reason)
	}
	assertReplay(t, j)
}

// assertReplay 用空下游视图按顺序回放全部日志，必须与当前视图一致。
func assertReplay(t *testing.T, j *Joiner) {
	t.Helper()
	type pair struct{ l, r string }
	view := make(map[pair]string)
	for _, e := range j.Log() {
		p := pair{e.LeftID, e.RightID}
		if e.Op == Insert {
			view[p] = e.Key
		} else {
			delete(view, p)
		}
	}
	got := j.View()
	if len(got) != len(view) {
		t.Fatalf("replayed %d rows, View() has %d", len(view), len(got))
	}
	for _, row := range got {
		key, ok := view[pair{row.LeftID, row.RightID}]
		if !ok || key != row.Key {
			t.Fatalf("replayed view missing %+v", row)
		}
	}
}

func TestRightArrivesBeforeLeft(t *testing.T) {
	j := New(0)
	logBatch(t, j, []Change{ins(Right, "r1", "k"), ins(Right, "r2", "k")})
	if got := j.View(); len(got) != 0 {
		t.Fatalf("no left rows yet, view should be empty, got %v", got)
	}
	logBatch(t, j, []Change{ins(Left, "l1", "k")})
	want := []ViewRow{{"l1", "r1", "k"}, {"l1", "r2", "k"}}
	if got := j.View(); !reflect.DeepEqual(got, want) {
		t.Fatalf("view = %v, want %v", got, want)
	}
	// 左行到达时直接产生配对，不应出现空填充行。
	for _, e := range j.Log() {
		if e.RightID == "" {
			t.Fatalf("unexpected padding entry: %+v", e)
		}
	}
}

func TestPaddingRowLifecycle(t *testing.T) {
	j := New(0)
	logBatch(t, j, []Change{ins(Left, "l1", "k")})
	if got := j.View(); !reflect.DeepEqual(got, []ViewRow{{"l1", "", "k"}}) {
		t.Fatalf("view = %v, want one padding row", got)
	}
	// 右行到达：撤回空填充，补配对，两条相邻。
	logBatch(t, j, []Change{ins(Right, "r1", "k")})
	log := j.Log()
	if len(log) != 3 {
		t.Fatalf("log len = %d, want 3", len(log))
	}
	if log[1].Op != Delete || log[1].RightID != "" || log[2].Op != Insert || log[2].RightID != "r1" {
		t.Fatalf("want unpad then pair adjacent, got %v %v", log[1], log[2])
	}
	// 删除最后一条同键右行：撤回配对，补回空填充。
	logBatch(t, j, []Change{del(Right, "r1", "k")})
	if got := j.View(); !reflect.DeepEqual(got, []ViewRow{{"l1", "", "k"}}) {
		t.Fatalf("view = %v, want padding row back", got)
	}
	log = j.Log()
	if log[3].Op != Delete || log[3].RightID != "r1" || log[4].Op != Insert || log[4].RightID != "" {
		t.Fatalf("want unpair then pad adjacent, got %v %v", log[3], log[4])
	}
}

func TestOutputSortedByOppositeSideID(t *testing.T) {
	j := New(0)
	logBatch(t, j, []Change{
		ins(Right, "r2", "k"), ins(Right, "r1", "k"), ins(Left, "l1", "k"),
	})
	log := j.Log()
	if len(log) != 2 || log[0].RightID != "r1" || log[1].RightID != "r2" {
		t.Fatalf("pair entries not sorted by right id: %v", log)
	}
	// 一条右行变更影响多个左行时按左标识字典序输出。
	logBatch(t, j, []Change{ins(Left, "l3", "k"), ins(Left, "l2", "k")})
	before := len(j.Log())
	logBatch(t, j, []Change{ins(Right, "r0", "k")})
	var lefts []string
	for _, e := range j.Log()[before:] {
		if e.Op == Insert {
			lefts = append(lefts, e.LeftID)
		}
	}
	if !reflect.DeepEqual(lefts, []string{"l1", "l2", "l3"}) {
		t.Fatalf("entries not sorted by left id: %v", lefts)
	}
}

func TestRejections(t *testing.T) {
	cases := []struct {
		name   string
		batch  []Change
		reason RejectReason
	}{
		{"empty id", []Change{ins(Left, "", "k")}, ReasonEmptyID},
		{"empty key", []Change{ins(Right, "r1", "")}, ReasonEmptyKey},
		{"duplicate insert", []Change{ins(Left, "l1", "k"), ins(Left, "l1", "k")}, ReasonDuplicateID},
		{"delete missing", []Change{del(Left, "ghost", "k")}, ReasonMissingID},
		{"too many rows", []Change{ins(Left, "l1", "k"), ins(Right, "r1", "k"), ins(Right, "r2", "k")}, ReasonTooManyRows},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := New(2)
			err := j.Apply(tc.batch)
			re, ok := err.(*RejectError)
			if !ok {
				t.Fatalf("err = %v, want *RejectError", err)
			}
			if re.Reason != tc.reason {
				t.Fatalf("reason = %s, want %s", re.Reason, tc.reason)
			}
			t.Logf("rejected: %v", err)
			if j.Size() != 0 || len(j.Log()) != 0 || len(j.View()) != 0 {
				t.Fatalf("rejected batch changed state: size=%d log=%d view=%d",
					j.Size(), len(j.Log()), len(j.View()))
			}
		})
	}
}

func TestRejectedBatchLeavesPriorStateUntouched(t *testing.T) {
	j := New(0)
	logBatch(t, j, []Change{ins(Left, "l1", "k")})
	beforeLog := j.Log()
	beforeView := j.View()
	// 批内第一条合法、第二条非法：整批拒绝，已产生的日志不变。
	err := j.Apply([]Change{ins(Right, "r1", "k"), del(Right, "ghost", "k")})
	if err == nil {
		t.Fatal("want rejection")
	}
	t.Logf("rejected: %v", err)
	if !reflect.DeepEqual(j.Log(), beforeLog) || !reflect.DeepEqual(j.View(), beforeView) {
		t.Fatal("rejected batch mutated state or log")
	}
}

func TestDeterministic(t *testing.T) {
	script := [][]Change{
		{ins(Right, "r2", "k"), ins(Right, "r1", "k")},
		{ins(Left, "l1", "k"), ins(Left, "l2", "x")},
		{ins(Right, "r3", "x")},
		{del(Right, "r1", "k")},
		{del(Left, "l1", "k")},
		{del(Right, "r3", "x")},
	}
	run := func() []Entry {
		j := New(0)
		for _, b := range script {
			if err := j.Apply(b); err != nil {
				t.Fatalf("Apply failed: %v", err)
			}
		}
		return j.Log()
	}
	first := run()
	for i := 0; i < 5; i++ {
		if got := run(); !reflect.DeepEqual(got, first) {
			t.Fatalf("run %d differs from first", i)
		}
	}
}

func TestConcurrentReaders(t *testing.T) {
	j := New(0)
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var lastSeq int64
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, e := range j.Log() { // 逐行一致：序号严格递增
					if e.Seq <= lastSeq {
						panic(fmt.Sprintf("non-monotonic seq %d after %d", e.Seq, lastSeq))
					}
					lastSeq = e.Seq
				}
				_ = j.View()
			}
		}()
	}
	for i := 0; i < 200; i++ {
		id := fmt.Sprintf("r%d", i)
		if err := j.Apply([]Change{ins(Right, id, "k")}); err != nil {
			t.Fatal(err)
		}
		if i%3 == 0 {
			if err := j.Apply([]Change{del(Right, id, "k")}); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := j.Apply([]Change{ins(Left, "l1", "k")}); err != nil {
		t.Fatal(err)
	}
	close(stop)
	wg.Wait()
	assertReplay(t, j)
}
