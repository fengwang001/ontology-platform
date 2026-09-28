package filterview

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func dumpJournal(t *testing.T, journal []JournalEntry) {
	t.Helper()
	for i, e := range journal {
		switch e.Op.Kind {
		case Insert:
			t.Logf("输入[%d] INSERT key=%q value=%d", i, e.Op.New.Key, e.Op.New.Value)
		case Delete:
			t.Logf("输入[%d] DELETE key=%q value=%d", i, e.Op.Old.Key, e.Op.Old.Value)
		case Update:
			t.Logf("输入[%d] UPDATE key=%q %d -> %d", i, e.Op.Old.Key, e.Op.Old.Value, e.Op.New.Value)
		}
		t.Logf("  判定: %s", e.Basis)
		if len(e.Changes) == 0 {
			t.Logf("  输出: <无>")
		}
		for _, c := range e.Changes {
			t.Logf("  输出: #%d %s key=%q value=%d", c.Seq, c.Kind, c.Row.Key, c.Row.Value)
		}
	}
}

func assertErrIs(t *testing.T, err error, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("期望错误 %v，实际 %v", target, err)
	}
}

func keysOf(rows []Row) []string {
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Key)
	}
	return out
}

func TestInvalidParams(t *testing.T) {
	cases := []struct {
		low, high int64
		desc      string
	}{
		{10, 10, "空区间"},
		{20, 10, "倒置区间"},
	}
	for _, c := range cases {
		t.Run(c.desc, func(t *testing.T) {
			if _, err := New(c.low, c.high); !errors.Is(err, ErrInvalidParams) {
				t.Fatalf("New(%d,%d) err=%v，期望 ErrInvalidParams", c.low, c.high, err)
			}
		})
	}
}

func TestInsertDeleteFiltering(t *testing.T) {
	v, err := New(10, 20)
	if err != nil {
		t.Fatal(err)
	}

	j, err := v.Apply([]Op{
		{Kind: Insert, New: Row{Key: "a", Value: 10}},
		{Kind: Insert, New: Row{Key: "b", Value: 20}},
		{Kind: Insert, New: Row{Key: "c", Value: 15}},
		{Kind: Insert, New: Row{Key: "d", Value: 9}},
	})
	if err != nil {
		t.Fatal(err)
	}
	dumpJournal(t, j)

	if got := keysOf(v.Snapshot()); strings.Join(got, ",") != "a,c" {
		t.Fatalf("视图键集合=%v，期望 a,c", got)
	}
	if log := v.Log(); len(log) != 2 || log[0].Kind != Add || log[1].Row.Key != "c" {
		t.Fatalf("初始日志=%+v，期望仅 a、c 两条 ADD", log)
	}

	j, err = v.Apply([]Op{
		{Kind: Delete, Old: Row{Key: "a", Value: 10}},
		{Kind: Delete, Old: Row{Key: "b", Value: 20}},
	})
	if err != nil {
		t.Fatal(err)
	}
	dumpJournal(t, j)

	if got := keysOf(v.Snapshot()); strings.Join(got, ",") != "c" {
		t.Fatalf("删除后视图键集合=%v，期望 c", got)
	}
	last := v.Log()[len(v.Log())-1]
	if last.Kind != Retract || last.Row.Key != "a" {
		t.Fatalf("末条日志=%+v，期望 RETRACT a", last)
	}
}

func TestUpdateFourCases(t *testing.T) {
	v, _ := New(10, 20)
	setup := []Op{
		{Kind: Insert, New: Row{Key: "inA", Value: 11}},
		{Kind: Insert, New: Row{Key: "inB", Value: 12}},
		{Kind: Insert, New: Row{Key: "inC", Value: 13}},
		{Kind: Insert, New: Row{Key: "outD", Value: 0}},
	}
	if _, err := v.Apply(setup); err != nil {
		t.Fatal(err)
	}

	batch := []Op{
		{Kind: Update, Old: Row{Key: "inA", Value: 11}, New: Row{Key: "inA", Value: 11}},
		{Kind: Update, Old: Row{Key: "inB", Value: 12}, New: Row{Key: "inB", Value: 18}},
		{Kind: Update, Old: Row{Key: "inC", Value: 13}, New: Row{Key: "inC", Value: 20}},
		{Kind: Update, Old: Row{Key: "outD", Value: 0}, New: Row{Key: "outD", Value: 10}},
	}
	j, err := v.Apply(batch)
	if err != nil {
		t.Fatal(err)
	}
	dumpJournal(t, j)

	if len(j[0].Changes) != 0 {
		t.Fatalf("情形1 应无输出，得到 %+v", j[0].Changes)
	}
	c := j[1].Changes
	if len(c) != 2 || c[0].Kind != Retract || c[0].Row.Value != 12 ||
		c[1].Kind != Add || c[1].Row.Value != 18 || c[0].Seq >= c[1].Seq {
		t.Fatalf("情形2 应先撤回12再写入18，得到 %+v", c)
	}
	c = j[2].Changes
	if len(c) != 1 || c[0].Kind != Retract || c[0].Row.Value != 13 {
		t.Fatalf("情形3 应撤回13，得到 %+v", c)
	}
	c = j[3].Changes
	if len(c) != 1 || c[0].Kind != Add || c[0].Row.Value != 10 {
		t.Fatalf("情形4 应写入10，得到 %+v", c)
	}

	if got := keysOf(v.Snapshot()); strings.Join(got, ",") != "inA,inB,outD" {
		t.Fatalf("更新后视图=%v，期望 inA,inB,outD", got)
	}
}

func TestRejectedBatchNoSideEffect(t *testing.T) {
	type rejectCase struct {
		name string
		ops  []Op
		want error
	}

	cases := []rejectCase{
		{"空标识-插入", []Op{{Kind: Insert, New: Row{Value: 10}}}, ErrEmptyKey},
		{"空标识-删除", []Op{{Kind: Delete, Old: Row{Value: 10}}}, ErrEmptyKey},
		{"空标识-更新", []Op{{Kind: Update, Old: Row{Value: 10}, New: Row{Value: 11}}}, ErrEmptyKey},
		{"更新标识不同", []Op{{Kind: Update, Old: Row{Key: "a", Value: 11}, New: Row{Key: "b", Value: 12}}}, ErrKeyMismatch},
		{"主键冲突", []Op{
			{Kind: Insert, New: Row{Key: "a", Value: 11}},
			{Kind: Insert, New: Row{Key: "a", Value: 12}},
		}, ErrKeyExists},
		{"主键不存在-删除", []Op{{Kind: Delete, Old: Row{Key: "ghost", Value: 11}}}, ErrKeyNotFound},
		{"主键不存在-更新", []Op{{Kind: Update, Old: Row{Key: "ghost", Value: 11}, New: Row{Key: "ghost", Value: 12}}}, ErrKeyNotFound},
		{"前像不等", []Op{
			{Kind: Insert, New: Row{Key: "bm", Value: 11}},
			{Kind: Update, Old: Row{Key: "bm", Value: 99}, New: Row{Key: "bm", Value: 12}},
		}, ErrBeforeImageDiff},
		{"未知操作类型", []Op{{Kind: OpKind(99)}}, ErrInvalidParams},
	}

	for _, rc := range cases {
		t.Run(rc.name, func(t *testing.T) {
			v, _ := New(10, 20)
			if _, err := v.Apply([]Op{{Kind: Insert, New: Row{Key: "a", Value: 11}}}); err != nil {
				t.Fatal(err)
			}
			beforeSrc := fmt.Sprint(v.SourceSnapshot())
			beforeView := fmt.Sprint(v.Snapshot())
			beforeLog := fmt.Sprint(v.Log())

			j, err := v.Apply(rc.ops)
			assertErrIs(t, err, rc.want)
			t.Logf("拒绝原因: %v", err)
			dumpJournal(t, j)

			if fmt.Sprint(v.SourceSnapshot()) != beforeSrc {
				t.Fatal("源表被拒绝批次改变")
			}
			if fmt.Sprint(v.Snapshot()) != beforeView {
				t.Fatal("视图被拒绝批次改变")
			}
			if fmt.Sprint(v.Log()) != beforeLog {
				t.Fatal("日志被拒绝批次改变")
			}
		})
	}
}

func TestDeterminism(t *testing.T) {
	seq := [][]Op{
		{
			{Kind: Insert, New: Row{Key: "z", Value: 15}},
			{Kind: Insert, New: Row{Key: "a", Value: 20}},
			{Kind: Insert, New: Row{Key: "m", Value: 10}},
		},
		{
			{Kind: Update, Old: Row{Key: "z", Value: 15}, New: Row{Key: "z", Value: 16}},
			{Kind: Delete, Old: Row{Key: "a", Value: 20}},
		},
		{
			{Kind: Update, Old: Row{Key: "m", Value: 10}, New: Row{Key: "m", Value: 30}},
		},
	}

	run := func() string {
		v, _ := New(10, 20)
		out := ""
		for _, batch := range seq {
			j, err := v.Apply(batch)
			if err != nil {
				t.Fatal(err)
			}
			out += fmt.Sprint(j)
		}
		return out + "|" + fmt.Sprint(v.Snapshot()) + "|" + fmt.Sprint(v.Log())
	}

	first := run()
	for i := 0; i < 5; i++ {
		if got := run(); got != first {
			t.Fatalf("第 %d 次运行输出不一致", i)
		}
	}
}

func TestConcurrentReads(t *testing.T) {
	v, _ := New(0, 100)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 500; i++ {
			key := fmt.Sprintf("k%d", i%30)
			newVal := int64(i % 200)
			if cur, ok := v.Get(key); ok {
				v.Apply([]Op{{Kind: Update, Old: cur, New: Row{Key: key, Value: newVal}}})
			} else {
				v.Apply([]Op{{Kind: Insert, New: Row{Key: key, Value: newVal}}})
			}
		}
		close(stop)
	}()

	for r := 0; r < 8; r++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				v.mu.RLock()
				view := make([]Row, 0)
				want := make(map[string]Row)
				for _, row := range v.source {
					if v.Matches(row.Value) {
						view = append(view, row)
						want[row.Key] = row
					}
				}
				v.mu.RUnlock()
				if len(view) != len(want) {
					t.Errorf("快照不一致：视图 %d 行，期望 %d 行", len(view), len(want))
					return
				}
				for _, row := range view {
					expected, ok := want[row.Key]
					if !ok || expected != row {
						t.Errorf("快照不一致：视图行 %+v，期望 %+v", row, expected)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
}

func TestLogOrderingRebuildsView(t *testing.T) {
	v, _ := New(10, 20)
	batches := [][]Op{
		{{Kind: Insert, New: Row{Key: "a", Value: 10}}, {Kind: Insert, New: Row{Key: "b", Value: 5}}},
		{{Kind: Update, Old: Row{Key: "a", Value: 10}, New: Row{Key: "a", Value: 20}}},
		{{Kind: Update, Old: Row{Key: "b", Value: 5}, New: Row{Key: "b", Value: 15}}},
		{{Kind: Delete, Old: Row{Key: "b", Value: 15}}},
	}
	for _, b := range batches {
		if _, err := v.Apply(b); err != nil {
			t.Fatal(err)
		}
	}

	downstream := map[string]Row{}
	for _, c := range v.Log() {
		switch c.Kind {
		case Add:
			downstream[c.Row.Key] = c.Row
		case Retract:
			delete(downstream, c.Row.Key)
		}
	}

	want := v.Snapshot()
	if len(downstream) != len(want) {
		t.Fatalf("下游重建 %d 行，视图 %d 行", len(downstream), len(want))
	}
	for _, row := range want {
		if got, ok := downstream[row.Key]; !ok || got != row {
			t.Fatalf("下游缺少正确行：%+v", row)
		}
	}
}
