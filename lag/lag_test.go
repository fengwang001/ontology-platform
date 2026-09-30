package lag

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// logStep 打印每步输入、输出与判定依据。
func logStep(t *testing.T, rationale string, input any, output []LogEntry, err error) {
	t.Helper()
	t.Logf("判定依据: %s", rationale)
	t.Logf("输入: %+v", input)
	if err != nil {
		t.Logf("输出: 拒绝 (%v)", err)
		return
	}
	if len(output) == 0 {
		t.Logf("输出: (无日志条目)")
	}
	for _, e := range output {
		t.Logf("输出: seq=%d op=%v id=%s part=%q prev=%s", e.Seq, e.Op, e.ID, e.Partition, prevStr(e.Prev))
	}
}

func prevStr(p *int64) string {
	if p == nil {
		return "null"
	}
	return fmt.Sprintf("%d", *p)
}

func ptr(v int64) *int64 { return &v }

func wantPrev(t *testing.T, m *Maintainer, id string, want *int64) {
	t.Helper()
	e, ok := m.View()[id]
	if !ok {
		t.Fatalf("row %q not in view", id)
	}
	if !eqPtr(e.Prev, want) {
		t.Fatalf("row %q prev = %s, want %s", id, prevStr(e.Prev), prevStr(want))
	}
}

func mustApply(t *testing.T, m *Maintainer, rationale string, changes ...Change) []LogEntry {
	t.Helper()
	entries, err := m.Apply(changes)
	logStep(t, rationale, changes, entries, err)
	if err != nil {
		t.Fatalf("Apply 被拒绝: %v", err)
	}
	return entries
}

// 并列打破：排序键相同按标识升序排列，前驱按该顺序取。
func TestTieBreaking(t *testing.T) {
	m := New(0)
	// 乱序插入三行，排序键均为 7，标识决定先后：a < b < c。
	mustApply(t, m, "同键并列按标识升序，c 的前驱是 b",
		Insert(Row{ID: "c", Partition: "p", SortKey: 7, Value: 30}))
	mustApply(t, m, "a 插到最前，成为分区首行，前驱为空",
		Insert(Row{ID: "a", Partition: "p", SortKey: 7, Value: 10}))
	entries := mustApply(t, m, "b 落在 a 与 c 之间：先输出 b 的前驱(=10)，再修正 c 的前驱(=30->30? 否，30->b 的值 20)",
		Insert(Row{ID: "b", Partition: "p", SortKey: 7, Value: 20}))

	// b 的前驱是 a(10)；c 的前驱由 a(10) 修正为 b(20)。
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(entries), entries)
	}
	if entries[0].ID != "b" || !eqPtr(entries[0].Prev, ptr(10)) {
		t.Fatalf("entries[0] = %+v, want b with prev 10", entries[0])
	}
	if entries[1].ID != "c" || !eqPtr(entries[1].Prev, ptr(20)) {
		t.Fatalf("entries[1] = %+v, want c with prev 20", entries[1])
	}
	wantPrev(t, m, "a", nil)
	wantPrev(t, m, "b", ptr(10))
	wantPrev(t, m, "c", ptr(20))
	if err := m.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// 非法输入整体拒绝：错误类别互不相同，且拒绝后行、视图与日志序号不变。
func TestInvalidInputRejectedAtomically(t *testing.T) {
	m := New(3)
	mustApply(t, m, "预置两行",
		Insert(Row{ID: "a", Partition: "p", SortKey: 1, Value: 10}),
		Insert(Row{ID: "b", Partition: "p", SortKey: 2, Value: 20}))
	before := m.View()

	cases := []struct {
		name    string
		changes []Change
		wantErr error
	}{
		{"空标识", []Change{Insert(Row{ID: "", Partition: "p"})}, ErrEmptyID},
		{"重复标识", []Change{Insert(Row{ID: "a", Partition: "p", SortKey: 9})}, ErrDuplicateID},
		{"同批重复标识", []Change{
			Insert(Row{ID: "n1", Partition: "p", SortKey: 3}),
			Insert(Row{ID: "n1", Partition: "p", SortKey: 4}),
		}, ErrDuplicateID},
		{"删除不存在标识", []Change{Delete("ghost")}, ErrRowNotFound},
		{"空分区名", []Change{Insert(Row{ID: "n2", Partition: "", SortKey: 3})}, ErrEmptyPartition},
		{"行数超限", []Change{
			Insert(Row{ID: "n3", Partition: "p", SortKey: 3}),
			Insert(Row{ID: "n4", Partition: "p", SortKey: 4}),
		}, ErrTooManyRows},
		{"批内先插后删不存在", []Change{
			Insert(Row{ID: "n5", Partition: "p", SortKey: 3}),
			Delete("ghost2"),
		}, ErrRowNotFound},
	}
	for _, tc := range cases {
		entries, err := m.Apply(tc.changes)
		logStep(t, "非法输入须整体拒绝: "+tc.name, tc.changes, entries, err)
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("%s: err = %v, want %v", tc.name, err, tc.wantErr)
		}
		if entries != nil {
			t.Fatalf("%s: 拒绝时不应产生日志, got %+v", tc.name, entries)
		}
		after := m.View()
		if !reflect.DeepEqual(before, after) {
			t.Fatalf("%s: 拒绝后视图被改变: before=%v after=%v", tc.name, before, after)
		}
	}

	// 失败不留痕：后续合法提交的日志序号紧接拒绝前。
	entries := mustApply(t, m, "拒绝不产生日志序号，下一条从 seq=3 开始",
		Insert(Row{ID: "c", Partition: "p", SortKey: 3, Value: 30}))
	if entries[0].Seq != 3 {
		t.Fatalf("seq = %d, want 3 (拒绝不留痕)", entries[0].Seq)
	}
	if err := m.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// 同一组行以任意顺序插入，最终视图逐标识相同，且与批量重算一致。
func TestOrderIndependence(t *testing.T) {
	rows := []Row{
		{ID: "a", Partition: "p", SortKey: 2, Value: 10},
		{ID: "b", Partition: "p", SortKey: 1, Value: 20},
		{ID: "c", Partition: "p", SortKey: 2, Value: 30},
		{ID: "d", Partition: "q", SortKey: 1, Value: 40},
		{ID: "e", Partition: "q", SortKey: 1, Value: 50},
	}
	want := BatchView(rows)

	rng := rand.New(rand.NewSource(42))
	for trial := 0; trial < 20; trial++ {
		perm := rng.Perm(len(rows))
		m := New(0)
		for _, i := range perm {
			if _, err := m.Apply([]Change{Insert(rows[i])}); err != nil {
				t.Fatal(err)
			}
		}
		got := m.View()
		t.Logf("判定依据: 任意插入顺序最终视图相同; 插入顺序: %v", perm)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("perm %v: view = %v, want %v", perm, got, want)
		}
	}
}

// 随机变更流：每步与批量重算比对，并验证下游按序应用日志后视图一致。
func TestRandomStreamMatchesBatch(t *testing.T) {
	rng := rand.New(rand.NewSource(7))
	m := New(0)
	var ids []string
	downstream := make(map[string]ViewEntry)

	applyLog := func(entries []LogEntry) {
		t.Helper()
		for _, e := range entries {
			switch e.Op {
			case LogUpsert:
				cur := downstream[e.ID]
				cur.Partition = e.Partition
				cur.Prev = e.Prev
				downstream[e.ID] = cur
			case LogDelete:
				delete(downstream, e.ID)
			}
		}
	}

	for step := 0; step < 200; step++ {
		var changes []Change
		if len(ids) == 0 || rng.Intn(3) > 0 {
			id := fmt.Sprintf("r%d", step)
			changes = append(changes, Insert(Row{
				ID:        id,
				Partition: string(rune('A' + rng.Intn(3))),
				SortKey:   int64(rng.Intn(5)),
				Value:     int64(rng.Intn(3)), // 小值域制造前驱值不变的情形
			}))
			ids = append(ids, id)
		} else {
			victim := ids[rng.Intn(len(ids))]
			changes = append(changes, Delete(victim))
			next := ids[:0]
			for _, id := range ids {
				if id != victim {
					next = append(next, id)
				}
			}
			ids = next
		}

		entries, err := m.Apply(changes)
		logStep(t, fmt.Sprintf("随机第 %d 步，与批量重算比对", step), changes, entries, err)
		if err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		applyLog(entries)

		if err := m.SelfCheck(); err != nil {
			t.Fatalf("step %d: %v", step, err)
		}
		// 下游按序应用日志后的前驱值须与当前视图一致。
		view := m.View()
		if len(downstream) != len(view) {
			t.Fatalf("step %d: downstream %d rows, view %d rows", step, len(downstream), len(view))
		}
		for id, e := range view {
			d, ok := downstream[id]
			if !ok || !eqPtr(d.Prev, e.Prev) {
				t.Fatalf("step %d: row %q downstream prev=%s, view prev=%s",
					step, id, prevStr(d.Prev), prevStr(e.Prev))
			}
		}
	}
}

// 视图与自检可与提交并发执行（配合 -race 验证）。
func TestConcurrentViewAndSelfCheck(t *testing.T) {
	m := New(0)
	var wg sync.WaitGroup
	stop := make(chan struct{})

	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				_ = m.View()
				if err := m.SelfCheck(); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}

	for i := 0; i < 100; i++ {
		id := fmt.Sprintf("w%d", i)
		if _, err := m.Apply([]Change{Insert(Row{ID: id, Partition: "p", SortKey: int64(i), Value: int64(i)})}); err != nil {
			t.Fatal(err)
		}
		if i%3 == 0 {
			if _, err := m.Apply([]Change{Delete(id)}); err != nil {
				t.Fatal(err)
			}
		}
	}
	close(stop)
	wg.Wait()
	if err := m.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// 分区首行前驱为空，与前驱取值为 0 严格区分；不同分区互不影响。
func TestNullVsZeroAndPartitionIsolation(t *testing.T) {
	m := New(0)
	mustApply(t, m, "p 分区首行 x，前驱为空",
		Insert(Row{ID: "x", Partition: "p", SortKey: 1, Value: 0}))
	mustApply(t, m, "y 的前驱是 x，其值为 0（非空）",
		Insert(Row{ID: "y", Partition: "p", SortKey: 2, Value: 5}))
	mustApply(t, m, "q 分区首行 z，前驱为空，不受 p 影响",
		Insert(Row{ID: "z", Partition: "q", SortKey: 1, Value: 9}))

	if got := prevOf(t, m, "x"); got != nil {
		t.Fatalf("x prev = %s, want null", prevStr(got))
	}
	got := prevOf(t, m, "y")
	if got == nil || *got != 0 {
		t.Fatalf("y prev = %s, want 0 (non-null)", prevStr(got))
	}
	if got := prevOf(t, m, "z"); got != nil {
		t.Fatalf("z prev = %s, want null", prevStr(got))
	}
	if err := m.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

func prevOf(t *testing.T, m *Maintainer, id string) *int64 {
	t.Helper()
	e, ok := m.View()[id]
	if !ok {
		t.Fatalf("row %q not in view", id)
	}
	return e.Prev
}

// 前驱值未变的行不输出任何条目。
func TestNoOutputWhenPrevUnchanged(t *testing.T) {
	m := New(0)
	mustApply(t, m, "a(值 5) 为首行",
		Insert(Row{ID: "a", Partition: "p", SortKey: 1, Value: 5}))
	mustApply(t, m, "b 的前驱是 a，取值 5",
		Insert(Row{ID: "b", Partition: "p", SortKey: 3, Value: 8}))
	// 在 a、b 之间插入 m，取值也是 5：b 的前驱由 a(5) 变为 m(5)，
	// 前驱值未变，因此只为 m 输出一条，不为 b 输出。
	entries := mustApply(t, m, "m 取值同为 5，b 的前驱值未变，不输出 b",
		Insert(Row{ID: "m", Partition: "p", SortKey: 2, Value: 5}))
	if len(entries) != 1 || entries[0].ID != "m" {
		t.Fatalf("got %+v, want single entry for m", entries)
	}
	wantPrev(t, m, "b", ptr(5))

	// 删除 m：b 的前驱由 m(5) 变回 a(5)，仍未变，只输出被删行 m。
	entries = mustApply(t, m, "删除 m 后 b 的前驱值仍为 5，不输出 b",
		Delete("m"))
	if len(entries) != 1 || entries[0].ID != "m" || entries[0].Op != LogDelete {
		t.Fatalf("got %+v, want single delete entry for m", entries)
	}
	wantPrev(t, m, "b", ptr(5))
	if err := m.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}

// 删除后修正受影响行：先输出被删行，再修正后继。
func TestDeleteFixSuccessor(t *testing.T) {
	m := New(0)
	mustApply(t, m, "建立 a(1)->b(2)->c(3)，取值为 10/20/30",
		Insert(Row{ID: "a", Partition: "p", SortKey: 1, Value: 10}),
		Insert(Row{ID: "b", Partition: "p", SortKey: 2, Value: 20}),
		Insert(Row{ID: "c", Partition: "p", SortKey: 3, Value: 30}))

	entries := mustApply(t, m, "删除 b：先输出 b，再把 c 的前驱由 20 修正为 10",
		Delete("b"))
	if len(entries) != 2 {
		t.Fatalf("got %d entries, want 2: %+v", len(entries), entries)
	}
	if entries[0].Op != LogDelete || entries[0].ID != "b" {
		t.Fatalf("entries[0] = %+v, want delete of b", entries[0])
	}
	if entries[1].Op != LogUpsert || entries[1].ID != "c" || !eqPtr(entries[1].Prev, ptr(10)) {
		t.Fatalf("entries[1] = %+v, want c with prev 10", entries[1])
	}
	wantPrev(t, m, "c", ptr(10))

	// 删除首行 a：c 成为首行，前驱修正为空。
	entries = mustApply(t, m, "删除首行 a：c 的前驱修正为空",
		Delete("a"))
	if len(entries) != 2 || entries[1].ID != "c" || entries[1].Prev != nil {
		t.Fatalf("got %+v, want delete of a then c with null prev", entries)
	}
	wantPrev(t, m, "c", nil)
	if err := m.SelfCheck(); err != nil {
		t.Fatal(err)
	}
}
