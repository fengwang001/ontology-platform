package filterview

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func changes(ch []Change) []string {
	out := make([]string, 0, len(ch))
	for _, c := range ch {
		kind := "+"
		if c.Kind == ViewDelete {
			kind = "-"
		}
		out = append(out, fmt.Sprintf("%s%s=%d", kind, c.Row.Key, c.Row.Value))
	}
	return out
}

func mustApply(t *testing.T, m *Maintainer, ops []Op) []Change {
	t.Helper()
	ch, err := m.Apply(ops)
	if err != nil {
		t.Fatalf("Apply(%v) unexpected error: %v", ops, err)
	}
	return ch
}

func assertReject(t *testing.T, m *Maintainer, ops []Op, want error, wantIndex int) {
	t.Helper()
	_, err := m.Apply(ops)
	if err == nil {
		t.Fatalf("Apply(%v) expected error %v, got nil", ops, want)
	}
	if !errors.Is(err, want) {
		t.Fatalf("Apply(%v) error = %v, want errors.Is %v", ops, err, want)
	}
	var re *RejectError
	if !errors.As(err, &re) {
		t.Fatalf("error %v is not *RejectError", err)
	}
	if re.Index != wantIndex {
		t.Fatalf("reject index = %d, want %d", re.Index, wantIndex)
	}
}

func assertState(t *testing.T, m *Maintainer, wantSource, wantView map[string]int64) {
	t.Helper()
	gotSource, gotView := m.Snapshot()
	if len(gotSource) != len(wantSource) {
		t.Fatalf("source size = %d (%v), want %d (%v)", len(gotSource), gotSource, len(wantSource), wantSource)
	}
	for k, v := range wantSource {
		r, ok := gotSource[k]
		if !ok || r.Value != v {
			t.Fatalf("source[%s] = %v ok=%v, want value %d", k, r, ok, v)
		}
	}
	if len(gotView) != len(wantView) {
		t.Fatalf("view size = %d (%v), want %d (%v)", len(gotView), gotView, len(wantView), wantView)
	}
	for k, v := range wantView {
		r, ok := gotView[k]
		if !ok || r.Value != v {
			t.Fatalf("view[%s] = %v ok=%v, want value %d", k, r, ok, v)
		}
	}
}

// 区间 [10, 20)：插入按值判定，删除按值撤回。
func TestInsertDeleteMembership(t *testing.T) {
	m, err := New(10, 20)
	if err != nil {
		t.Fatal(err)
	}

	ch := mustApply(t, m, []Op{
		{Kind: OpInsert, After: Row{Key: "a", Value: 5}},
		{Kind: OpInsert, After: Row{Key: "b", Value: 10}},
		{Kind: OpInsert, After: Row{Key: "c", Value: 19}},
		{Kind: OpInsert, After: Row{Key: "d", Value: 20}},
	})
	if got := changes(ch); strings.Join(got, ",") != "+b=10,+c=19" {
		t.Fatalf("insert changes = %v", got)
	}
	assertState(t, m,
		map[string]int64{"a": 5, "b": 10, "c": 19, "d": 20},
		map[string]int64{"b": 10, "c": 19})

	ch = mustApply(t, m, []Op{
		{Kind: OpDelete, Before: Row{Key: "a", Value: 5}},
		{Kind: OpDelete, Before: Row{Key: "b", Value: 10}},
		{Kind: OpDelete, Before: Row{Key: "c", Value: 19}},
		{Kind: OpDelete, Before: Row{Key: "d", Value: 20}},
	})
	if got := changes(ch); strings.Join(got, ",") != "-b=10,-c=19" {
		t.Fatalf("delete changes = %v", got)
	}
	assertState(t, m, map[string]int64{}, map[string]int64{})
}

// 更新四种情形：in->in 不变无输出；in->in 变化先撤旧再写新；in->out 撤回；out->in 写入；另验证 out->out。
func TestUpdateFourCases(t *testing.T) {
	m, _ := New(10, 20)
	mustApply(t, m, []Op{
		{Kind: OpInsert, After: Row{Key: "in1", Value: 11}},
		{Kind: OpInsert, After: Row{Key: "in2", Value: 11}},
		{Kind: OpInsert, After: Row{Key: "in3", Value: 11}},
		{Kind: OpInsert, After: Row{Key: "out1", Value: 0}},
		{Kind: OpInsert, After: Row{Key: "out2", Value: 0}},
	})

	ch := mustApply(t, m, []Op{{Kind: OpUpdate, Before: Row{Key: "in1", Value: 11}, After: Row{Key: "in1", Value: 11}}})
	if len(ch) != 0 {
		t.Fatalf("in->in unchanged should emit nothing, got %v", changes(ch))
	}

	ch = mustApply(t, m, []Op{{Kind: OpUpdate, Before: Row{Key: "in2", Value: 11}, After: Row{Key: "in2", Value: 12}}})
	if got := changes(ch); strings.Join(got, ",") != "-in2=11,+in2=12" {
		t.Fatalf("in->in changed changes = %v", got)
	}

	ch = mustApply(t, m, []Op{{Kind: OpUpdate, Before: Row{Key: "in3", Value: 11}, After: Row{Key: "in3", Value: 25}}})
	if got := changes(ch); strings.Join(got, ",") != "-in3=11" {
		t.Fatalf("in->out changes = %v", got)
	}

	ch = mustApply(t, m, []Op{{Kind: OpUpdate, Before: Row{Key: "out1", Value: 0}, After: Row{Key: "out1", Value: 15}}})
	if got := changes(ch); strings.Join(got, ",") != "+out1=15" {
		t.Fatalf("out->in changes = %v", got)
	}

	ch = mustApply(t, m, []Op{{Kind: OpUpdate, Before: Row{Key: "out2", Value: 0}, After: Row{Key: "out2", Value: 1}}})
	if len(ch) != 0 {
		t.Fatalf("out->out should emit nothing, got %v", changes(ch))
	}

	assertState(t, m,
		map[string]int64{"in1": 11, "in2": 12, "in3": 25, "out1": 15, "out2": 1},
		map[string]int64{"in1": 11, "in2": 12, "out1": 15})
}

// 值落在区间端点：Low 属于视图，High 不属于；恰在端点的更新进出视图。
func TestIntervalEndpoints(t *testing.T) {
	m, _ := New(10, 20)
	if !m.Contains(10) || m.Contains(20) || m.Contains(9) {
		t.Fatalf("Contains endpoint behavior wrong for [10,20)")
	}
	mustApply(t, m, []Op{
		{Kind: OpInsert, After: Row{Key: "lo", Value: 10}},
		{Kind: OpInsert, After: Row{Key: "hi", Value: 20}},
	})
	_, view := m.Snapshot()
	if _, ok := view["lo"]; !ok {
		t.Fatal("Low endpoint row must be in view")
	}
	if _, ok := view["hi"]; ok {
		t.Fatal("High endpoint row must not be in view")
	}

	ch := mustApply(t, m, []Op{{Kind: OpUpdate, Before: Row{Key: "lo", Value: 10}, After: Row{Key: "lo", Value: 20}}})
	if got := changes(ch); strings.Join(got, ",") != "-lo=10" {
		t.Fatalf("crossing Low->High changes = %v", got)
	}
	ch = mustApply(t, m, []Op{{Kind: OpUpdate, Before: Row{Key: "lo", Value: 20}, After: Row{Key: "lo", Value: 19}}})
	if got := changes(ch); strings.Join(got, ",") != "+lo=19" {
		t.Fatalf("crossing High->Low-1 changes = %v", got)
	}
}

// 各类非法输入均被拒绝，且拒绝原因与批内位置可区分。
func TestRejections(t *testing.T) {
	if _, err := New(10, 10); !errors.Is(err, ErrInvalidInterval) {
		t.Fatalf("equal bounds: %v", err)
	}
	if _, err := New(20, 10); !errors.Is(err, ErrInvalidInterval) {
		t.Fatalf("inverted bounds: %v", err)
	}

	m, _ := New(10, 20)
	mustApply(t, m, []Op{{Kind: OpInsert, After: Row{Key: "a", Value: 11}}})

	assertReject(t, m, []Op{{Kind: OpInsert, After: Row{Key: "", Value: 11}}}, ErrEmptyKey, 0)
	assertReject(t, m, []Op{
		{Kind: OpInsert, After: Row{Key: "b", Value: 11}},
		{Kind: OpInsert, After: Row{Key: "", Value: 11}},
	}, ErrEmptyKey, 1)
	assertReject(t, m, []Op{{Kind: OpInsert, After: Row{Key: "a", Value: 11}}}, ErrDuplicateKey, 0)
	assertReject(t, m, []Op{
		{Kind: OpInsert, After: Row{Key: "b", Value: 1}},
		{Kind: OpInsert, After: Row{Key: "b", Value: 2}},
	}, ErrDuplicateKey, 1)
	assertReject(t, m, []Op{{Kind: OpDelete, Before: Row{Key: "missing", Value: 1}}}, ErrKeyNotFound, 0)
	assertReject(t, m, []Op{{Kind: OpUpdate, Before: Row{Key: "missing", Value: 1}, After: Row{Key: "missing", Value: 2}}}, ErrKeyNotFound, 0)
	assertReject(t, m, []Op{{Kind: OpUpdate, Before: Row{Key: "a", Value: 99}, After: Row{Key: "a", Value: 12}}}, ErrBeforeMismatch, 0)
	assertReject(t, m, []Op{{Kind: OpDelete, Before: Row{Key: "a", Value: 99}}}, ErrBeforeMismatch, 0)
	assertReject(t, m, []Op{{Kind: OpUpdate, Before: Row{Key: "a", Value: 11}, After: Row{Key: "b", Value: 12}}}, ErrUpdateKeyMismatch, 0)
	assertReject(t, m, []Op{{Kind: OpKind(99)}}, ErrUnknownOp, 0)

	assertState(t, m, map[string]int64{"a": 11}, map[string]int64{"a": 11})
}

// 被拒绝的批不得部分生效：即使错误发生在批末尾，前面的合法操作也必须回滚。
func TestRejectedBatchIsAtomic(t *testing.T) {
	m, _ := New(10, 20)
	ch := mustApply(t, m, []Op{
		{Kind: OpInsert, After: Row{Key: "a", Value: 11}},
		{Kind: OpInsert, After: Row{Key: "b", Value: 1}},
	})
	if len(ch) != 1 {
		t.Fatalf("setup changes = %v", changes(ch))
	}

	_, err := m.Apply([]Op{
		{Kind: OpUpdate, Before: Row{Key: "a", Value: 11}, After: Row{Key: "a", Value: 12}},
		{Kind: OpDelete, Before: Row{Key: "b", Value: 2}},
		{Kind: OpInsert, After: Row{Key: "c", Value: 13}},
		{Kind: OpInsert, After: Row{Key: "a", Value: 14}},
	})
	var re *RejectError
	if !errors.As(err, &re) || !errors.Is(err, ErrBeforeMismatch) || re.Index != 1 {
		t.Fatalf("want ErrBeforeMismatch at index 1, got %v", err)
	}
	assertState(t, m,
		map[string]int64{"a": 11, "b": 1},
		map[string]int64{"a": 11})
}

// 下游按顺序应用净变化，始终重建出正确的过滤视图。
func TestDownstreamReplay(t *testing.T) {
	m, _ := New(10, 20)
	downstream := make(map[string]Row)

	apply := func(batch []Op) {
		t.Helper()
		ch, err := m.Apply(batch)
		if err != nil {
			t.Fatalf("Apply: %v", err)
		}
		for _, c := range ch {
			switch c.Kind {
			case ViewInsert:
				downstream[c.Row.Key] = c.Row
			case ViewDelete:
				delete(downstream, c.Row.Key)
			}
		}
		_, view := m.Snapshot()
		if fmt.Sprint(view) != fmt.Sprint(downstream) {
			t.Fatalf("downstream %v != view %v", downstream, view)
		}
	}

	apply([]Op{{Kind: OpInsert, After: Row{Key: "a", Value: 11}}, {Kind: OpInsert, After: Row{Key: "b", Value: 1}}})
	apply([]Op{{Kind: OpUpdate, Before: Row{Key: "b", Value: 1}, After: Row{Key: "b", Value: 19}}})
	apply([]Op{{Kind: OpUpdate, Before: Row{Key: "a", Value: 11}, After: Row{Key: "a", Value: 30}}})
	apply([]Op{{Kind: OpUpdate, Before: Row{Key: "b", Value: 19}, After: Row{Key: "b", Value: 18}}})
	apply([]Op{{Kind: OpDelete, Before: Row{Key: "b", Value: 18}}})
}

// 同一输入序列重复计算，净变化输出完全相同（确定性）。
func TestDeterministicReplay(t *testing.T) {
	batches := [][]Op{
		{{Kind: OpInsert, After: Row{Key: "x", Value: 11}}, {Kind: OpInsert, After: Row{Key: "y", Value: 0}}},
		{{Kind: OpUpdate, Before: Row{Key: "x", Value: 11}, After: Row{Key: "x", Value: 12}}, {Kind: OpUpdate, Before: Row{Key: "y", Value: 0}, After: Row{Key: "y", Value: 15}}},
		{{Kind: OpDelete, Before: Row{Key: "x", Value: 12}}},
	}
	run := func() string {
		m, _ := New(10, 20)
		var all []string
		for _, b := range batches {
			ch, err := m.Apply(b)
			if err != nil {
				t.Fatal(err)
			}
			all = append(all, changes(ch)...)
		}
		return strings.Join(all, "|")
	}
	first := run()
	for i := 0; i < 5; i++ {
		if got := run(); got != first {
			t.Fatalf("run %d = %q, want %q", i, got, first)
		}
	}
}

type bufLogger struct {
	mu    sync.Mutex
	lines []string
}

func (b *bufLogger) Printf(format string, args ...any) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = append(b.lines, fmt.Sprintf(format, args...))
}

func (b *bufLogger) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return strings.Join(b.lines, "\n")
}

// 日志中必须包含输入条目、输出条目与判定依据；被拒绝的批不产生日志。
func TestDecisionLog(t *testing.T) {
	m, _ := New(10, 20)
	l := &bufLogger{}
	m.SetLogger(l)

	_, err := m.Apply([]Op{
		{Kind: OpInsert, After: Row{Key: "a", Value: 11}},
		{Kind: OpInsert, After: Row{Key: "b", Value: 1}},
		{Kind: OpUpdate, Before: Row{Key: "a", Value: 11}, After: Row{Key: "a", Value: 25}},
	})
	if err != nil {
		t.Fatal(err)
	}
	out := l.String()
	for _, want := range []string{
		"INSERT input={Key:\"a\" Value:11}",
		"INSERT input={Key:\"b\" Value:1}",
		"UPDATE input=before {Key:\"a\" Value:11} -> after {Key:\"a\" Value:25}",
		"in->out: ViewDelete(old)",
		"ViewInsert {Key:\"a\" Value:11}",
		"committed",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("log missing %q:\n%s", want, out)
		}
	}

	before := len(l.lines)
	_, err = m.Apply([]Op{{Kind: OpInsert, After: Row{Key: "a", Value: 2}}})
	if !errors.Is(err, ErrDuplicateKey) {
		t.Fatalf("want duplicate, got %v", err)
	}
	if len(l.lines) != before {
		t.Fatalf("rejected batch must not emit logs; got %d new lines", len(l.lines)-before)
	}
}

// 并发写入与读取：race 检测下快照必须逐字段一致且永远满足“视图 == 源表过滤结果”。
func TestConcurrentSnapshotConsistency(t *testing.T) {
	m, _ := New(10, 20)
	stop := make(chan struct{})
	var wg sync.WaitGroup

	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			key := fmt.Sprintf("k%d", id)
			value := int64(10 + id)
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				next := int64(10 + ((id + i) % 20))
				if _, err := m.Apply([]Op{{Kind: OpUpdate, Before: Row{Key: key, Value: value}, After: Row{Key: key, Value: next}}}); err != nil {
					m.Apply([]Op{{Kind: OpInsert, After: Row{Key: key, Value: next}}})
				}
				value = next
			}
		}(w)
	}

	for r := 0; r < 200; r++ {
		source, view := m.Snapshot()
		count := 0
		for k, row := range source {
			want := m.Contains(row.Value)
			_, got := view[k]
			if want != got {
				t.Fatalf("key %s membership mismatch: want %v got %v", k, want, got)
			}
			if got && view[k] != row {
				t.Fatalf("view row %v != source row %v", view[k], row)
			}
			if want {
				count++
			}
		}
		if len(view) != count {
			t.Fatalf("view has %d rows, expected %d", len(view), count)
		}
	}
	close(stop)
	wg.Wait()
}
