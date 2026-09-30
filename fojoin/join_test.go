package fojoin

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// --- 测试辅助 -------------------------------------------------------------

func fmtRow(r Row) string {
	l, rr := "∅", "∅"
	if r.LeftID != nil {
		l = *r.LeftID
	}
	if r.RightID != nil {
		rr = *r.RightID
	}
	return fmt.Sprintf("(%s|%s|%s)", r.Key, l, rr)
}

func fmtEntry(e LogEntry) string {
	op := "-"
	if e.Add {
		op = "+"
	}
	return op + fmtRow(e.Row)
}

func fmtEntries(es []LogEntry) string {
	s := ""
	for i, e := range es {
		if i > 0 {
			s += " "
		}
		s += fmtEntry(e)
	}
	return s
}

func fmtRows(rs []Row) string {
	s := ""
	for i, r := range rs {
		if i > 0 {
			s += " "
		}
		s += fmtRow(r)
	}
	return s
}

func fmtChanges(cs []Change) string {
	s := ""
	for i, c := range cs {
		if i > 0 {
			s += " "
		}
		s += fmt.Sprintf("%s:%s(%s,%s)", c.Side, c.Op, c.Key, c.RowID)
	}
	return s
}

func applyOK(t *testing.T, m *Maintainer, batch ...Change) []LogEntry {
	t.Helper()
	t.Logf("输入变更: %s", fmtChanges(batch))
	entries, err := m.Apply(batch)
	if err != nil {
		t.Fatalf("Apply 意外失败: %v", err)
	}
	t.Logf("输出日志: %s", fmtEntries(entries))
	return entries
}

func wantEntries(t *testing.T, why string, got []LogEntry, want ...LogEntry) {
	t.Helper()
	t.Logf("判定依据: %s", why)
	if len(got) != len(want) {
		t.Fatalf("日志条数不符: got [%s], want [%s]", fmtEntries(got), fmtEntries(want))
	}
	for i := range want {
		if got[i].Add != want[i].Add || !equalRow(got[i].Row, want[i].Row) {
			t.Fatalf("日志[%d]不符: got %s, want %s", i, fmtEntry(got[i]), fmtEntry(want[i]))
		}
	}
}

func wantView(t *testing.T, m *Maintainer, want ...Row) {
	t.Helper()
	got := m.View()
	t.Logf("当前视图: %s", fmtRows(got))
	if len(got) != len(want) {
		t.Fatalf("视图行数不符: got [%s], want [%s]", fmtRows(got), fmtRows(want))
	}
	for i := range want {
		if !equalRow(got[i], want[i]) {
			t.Fatalf("视图[%d]不符: got %s, want %s", i, fmtRow(got[i]), fmtRow(want[i]))
		}
	}
	if err := m.SelfCheck(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
	t.Logf("自检通过: 增量视图与批量重算一致")
}

func add(row Row) LogEntry     { return LogEntry{Add: true, Row: row} }
func retract(row Row) LogEntry { return LogEntry{Add: false, Row: row} }

// --- 穿越零的补位与撤回切换 -------------------------------------------------

// TestZeroCrossingSwitch 验证：仅当某侧计数穿越零时才在补足行与配对行
// 之间切换，且切换时先撤回旧形态再输出新形态。
func TestZeroCrossingSwitch(t *testing.T) {
	m := New()

	// 左 0→1，右侧为空：只产生左行补足行（空位在右）。
	got := applyOK(t, m, Change{Left, Insert, "k", "l1"})
	wantEntries(t, "左 0→1 且右为空，只新增补足行 (k|l1|∅)", got,
		add(LeftOnly("k", "l1")))
	wantView(t, m, LeftOnly("k", "l1"))

	// 右 0→1：穿越零，先撤回左补足行，再输出配对行。
	got = applyOK(t, m, Change{Right, Insert, "k", "r1"})
	wantEntries(t, "右 0→1 穿越零：先撤回旧形态 -(k|l1|∅)，再输出新形态 +(k|l1|r1)", got,
		retract(LeftOnly("k", "l1")),
		add(Pair("k", "l1", "r1")))
	wantView(t, m, Pair("k", "l1", "r1"))

	// 左 1→0：穿越零，先撤回配对行，再输出右补足行（空位在左）。
	got = applyOK(t, m, Change{Left, Delete, "k", "l1"})
	wantEntries(t, "左 1→0 穿越零：先撤回 -(k|l1|r1)，再输出补足行 +(k|∅|r1)", got,
		retract(Pair("k", "l1", "r1")),
		add(RightOnly("k", "r1")))
	wantView(t, m, RightOnly("k", "r1"))

	// 右 1→0：键变空，只撤回补足行。
	got = applyOK(t, m, Change{Right, Delete, "k", "r1"})
	wantEntries(t, "右 1→0 且左为空，只撤回补足行 -(k|∅|r1)", got,
		retract(RightOnly("k", "r1")))
	wantView(t, m)
}

// TestCrossingWithMultiRows 验证多行情形下穿越零时撤回/输出的行数
// 与对侧行数一致，且与批量重算一致。
func TestCrossingWithMultiRows(t *testing.T) {
	m := New()
	applyOK(t, m,
		Change{Right, Insert, "k", "r1"},
		Change{Right, Insert, "k", "r2"})
	wantView(t, m, RightOnly("k", "r1"), RightOnly("k", "r2"))

	// 左 0→1：撤回 2 条右补足行，输出 2 条配对行。
	got := applyOK(t, m, Change{Left, Insert, "k", "l1"})
	wantEntries(t, "左 0→1：撤回全部右补足行后输出 l1×{r1,r2} 配对行", got,
		retract(RightOnly("k", "r1")),
		retract(RightOnly("k", "r2")),
		add(Pair("k", "l1", "r1")),
		add(Pair("k", "l1", "r2")))
	wantView(t, m, Pair("k", "l1", "r1"), Pair("k", "l1", "r2"))

	// 左 1→0：撤回 2 条配对行，恢复 2 条右补足行。
	got = applyOK(t, m, Change{Left, Delete, "k", "l1"})
	wantEntries(t, "左 1→0：撤回全部配对行后恢复右补足行", got,
		retract(Pair("k", "l1", "r1")),
		retract(Pair("k", "l1", "r2")),
		add(RightOnly("k", "r1")),
		add(RightOnly("k", "r2")))
	wantView(t, m, RightOnly("k", "r1"), RightOnly("k", "r2"))
}

// --- 两侧撤回的不同处理 -----------------------------------------------------

// TestRetractSidesDiffer 验证：左行撤回时补足行空位在右侧（(k|l|∅)），
// 右行撤回时补足行空位在左侧（(k|∅|r)）；且被撤回的必须是当前存在的行。
func TestRetractSidesDiffer(t *testing.T) {
	m := New()

	// 左行撤回：对侧为空，空位在右。
	applyOK(t, m, Change{Left, Insert, "a", "l1"}, Change{Left, Insert, "a", "l2"})
	wantView(t, m, LeftOnly("a", "l1"), LeftOnly("a", "l2"))
	got := applyOK(t, m, Change{Left, Delete, "a", "l1"})
	wantEntries(t, "左行撤回且右为空：撤回的补足行空位在右 -(a|l1|∅)", got,
		retract(LeftOnly("a", "l1")))
	wantView(t, m, LeftOnly("a", "l2"))

	// 右行撤回：对侧为空，空位在左。
	applyOK(t, m, Change{Right, Insert, "b", "r1"}, Change{Right, Insert, "b", "r2"})
	wantView(t, m, LeftOnly("a", "l2"), RightOnly("b", "r1"), RightOnly("b", "r2"))
	got = applyOK(t, m, Change{Right, Delete, "b", "r1"})
	wantEntries(t, "右行撤回且左为空：撤回的补足行空位在左 -(b|∅|r1)", got,
		retract(RightOnly("b", "r1")))
	wantView(t, m, LeftOnly("a", "l2"), RightOnly("b", "r2"))

	// 配对形态下左行撤回：撤回的是配对行 (c|l|r)。
	applyOK(t, m,
		Change{Left, Insert, "c", "l1"},
		Change{Right, Insert, "c", "r1"},
		Change{Right, Insert, "c", "r2"})
	wantView(t, m, LeftOnly("a", "l2"), RightOnly("b", "r2"),
		Pair("c", "l1", "r1"), Pair("c", "l1", "r2"))
	got = applyOK(t, m, Change{Left, Delete, "c", "l1"})
	wantEntries(t, "左 1→0：撤回配对行后补位在左 +(c|∅|r1) +(c|∅|r2)", got,
		retract(Pair("c", "l1", "r1")),
		retract(Pair("c", "l1", "r2")),
		add(RightOnly("c", "r1")),
		add(RightOnly("c", "r2")))

	// 配对形态下右行撤回：撤回的是配对行 (d|l|r)。
	applyOK(t, m,
		Change{Right, Insert, "d", "r1"},
		Change{Left, Insert, "d", "l1"},
		Change{Left, Insert, "d", "l2"})
	got = applyOK(t, m, Change{Right, Delete, "d", "r1"})
	wantEntries(t, "右 1→0：撤回配对行后补位在右 +(d|l1|∅) +(d|l2|∅)", got,
		retract(Pair("d", "l1", "r1")),
		retract(Pair("d", "l2", "r1")),
		add(LeftOnly("d", "l1")),
		add(LeftOnly("d", "l2")))
	wantView(t, m,
		LeftOnly("a", "l2"), RightOnly("b", "r2"),
		RightOnly("c", "r1"), RightOnly("c", "r2"),
		LeftOnly("d", "l1"), LeftOnly("d", "l2"))
}

// TestNonCrossingNoRebuild 验证：计数不穿越零时只增删对应行，
// 不做整键重建式输出（日志中不出现无关行的撤回）。
func TestNonCrossingNoRebuild(t *testing.T) {
	m := New()
	applyOK(t, m,
		Change{Left, Insert, "k", "l1"},
		Change{Left, Insert, "k", "l2"},
		Change{Right, Insert, "k", "r1"},
		Change{Right, Insert, "k", "r2"})
	wantView(t, m,
		Pair("k", "l1", "r1"), Pair("k", "l1", "r2"),
		Pair("k", "l2", "r1"), Pair("k", "l2", "r2"))

	// 左 2→3：只新增 l3 的 2 条配对行。
	got := applyOK(t, m, Change{Left, Insert, "k", "l3"})
	wantEntries(t, "左 2→3 未穿越零：只新增 l3 与右侧的配对行，无撤回", got,
		add(Pair("k", "l3", "r1")),
		add(Pair("k", "l3", "r2")))

	// 右 2→1：只撤回 r2 的 3 条配对行。
	got = applyOK(t, m, Change{Right, Delete, "k", "r2"})
	wantEntries(t, "右 2→1 未穿越零：只撤回 r2 参与的配对行，无重建", got,
		retract(Pair("k", "l1", "r2")),
		retract(Pair("k", "l2", "r2")),
		retract(Pair("k", "l3", "r2")))
	wantView(t, m,
		Pair("k", "l1", "r1"), Pair("k", "l2", "r1"), Pair("k", "l3", "r1"))
}

// --- 三类非法输入被拒且状态不变 ---------------------------------------------

// TestRejects 验证三类非法输入分别被互不相同的错误拒绝，
// 且任一批内任一条被拒则整批不生效（状态与日志均不变）。
func TestRejects(t *testing.T) {
	cases := []struct {
		name  string
		batch []Change
		want  error
		why   string
	}{
		{
			name:  "empty key",
			batch: []Change{{Left, Insert, "", "x1"}},
			want:  ErrEmptyKey,
			why:   "键为空必须拒绝",
		},
		{
			name: "duplicate insert",
			batch: []Change{
				{Left, Insert, "k", "l1"}, // 已存在 → 重复插入
			},
			want: ErrDuplicateInsert,
			why:  "重复插入同一行标识必须拒绝",
		},
		{
			name: "duplicate insert within batch",
			batch: []Change{
				{Right, Insert, "k", "r9"},
				{Right, Insert, "k", "r9"}, // 批内重复
			},
			want: ErrDuplicateInsert,
			why:  "同一批内重复插入同一行标识也必须拒绝",
		},
		{
			name:  "delete missing",
			batch: []Change{{Right, Delete, "k", "nope"}},
			want:  ErrMissingDelete,
			why:   "删除不存在的行标识必须拒绝",
		},
		{
			name: "atomic batch",
			batch: []Change{
				{Left, Insert, "k", "l2"},    // 合法
				{Right, Insert, "k", "r1"},   // 合法
				{Left, Delete, "k", "ghost"}, // 非法 → 整批不生效
			},
			want: ErrMissingDelete,
			why:  "批内任一条被拒则整批不生效",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := New()
			applyOK(t, m, Change{Left, Insert, "k", "l1"})
			viewBefore := m.View()
			logBefore := m.Log()

			t.Logf("输入变更: %s", fmtChanges(tc.batch))
			_, err := m.Apply(tc.batch)
			t.Logf("判定依据: %s；期望错误 %v，实际 %v", tc.why, tc.want, err)
			if !errors.Is(err, tc.want) {
				t.Fatalf("错误类别不符: got %v, want %v", err, tc.want)
			}
			// 三类错误互不相同，可用 errors.Is 区分。
			for _, other := range []error{ErrEmptyKey, ErrDuplicateInsert, ErrMissingDelete} {
				if other != tc.want && errors.Is(err, other) {
					t.Fatalf("错误类别不互斥: %v 同时匹配 %v", err, other)
				}
			}

			viewAfter := m.View()
			logAfter := m.Log()
			if !reflect.DeepEqual(viewBefore, viewAfter) {
				t.Fatalf("拒绝后视图被改变: before %s, after %s", fmtRows(viewBefore), fmtRows(viewAfter))
			}
			if len(logBefore) != len(logAfter) {
				t.Fatalf("拒绝后日志被改变: before %d 条, after %d 条", len(logBefore), len(logAfter))
			}
			t.Logf("状态不变: 视图 %s，日志 %d 条", fmtRows(viewAfter), len(logAfter))
		})
	}
}

// --- 并发只读一致 -----------------------------------------------------------

// TestConcurrentReadOnlyConsistent 验证视图与自检可被并发读取，
// 且并发只读同一实例得到的视图逐字段相同。
func TestConcurrentReadOnlyConsistent(t *testing.T) {
	m := New()
	applyOK(t, m,
		Change{Left, Insert, "a", "l1"},
		Change{Left, Insert, "a", "l2"},
		Change{Right, Insert, "a", "r1"},
		Change{Right, Insert, "b", "r9"},
		Change{Left, Insert, "c", "l7"})
	base := m.View()
	t.Logf("基准视图: %s", fmtRows(base))

	const readers = 8
	const rounds = 200
	var wg sync.WaitGroup
	errs := make(chan string, readers)
	for g := 0; g < readers; g++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				got := m.View()
				if !reflect.DeepEqual(base, got) {
					errs <- fmt.Sprintf("reader %d 第 %d 轮视图不一致: %s", id, i, fmtRows(got))
					return
				}
				if err := m.SelfCheck(); err != nil {
					errs <- fmt.Sprintf("reader %d 第 %d 轮自检失败: %v", id, i, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()
	close(errs)
	for msg := range errs {
		t.Fatal(msg)
	}
	t.Logf("判定依据: %d 个读者各读 %d 轮，视图均与基准逐字段相同且自检通过", readers, rounds)
}

// --- 日志重放与批量重算一致 ---------------------------------------------------

// TestLogReplayMatchesRecompute 验证下游按序应用输出日志得到的物化视图
// 与维护器视图、批量重算三者一致。
func TestLogReplayMatchesRecompute(t *testing.T) {
	m := New()
	var batches [][]Change
	rng := rand.New(rand.NewSource(42))

	// 维护一份合法操作空间，随机生成 60 批合法变更。
	alive := map[string]map[Side]map[string]bool{}
	nextID := 0
	for b := 0; b < 60; b++ {
		var batch []Change
		for n := rng.Intn(4); n >= 0; n-- {
			key := fmt.Sprintf("k%d", rng.Intn(4))
			side := Side(rng.Intn(2))
			if alive[key] == nil {
				alive[key] = map[Side]map[string]bool{Left: {}, Right: {}}
			}
			rows := alive[key][side]
			if len(rows) > 0 && rng.Intn(2) == 0 {
				var id string
				for id = range rows {
					break
				}
				batch = append(batch, Change{side, Delete, key, id})
				delete(rows, id)
			} else {
				id := fmt.Sprintf("r%d", nextID)
				nextID++
				batch = append(batch, Change{side, Insert, key, id})
				rows[id] = true
			}
		}
		if len(batch) > 0 {
			t.Logf("批 %d 输入: %s", b, fmtChanges(batch))
			if _, err := m.Apply(batch); err != nil {
				t.Fatalf("批 %d 意外失败: %v", b, err)
			}
			batches = append(batches, batch)
		}
	}

	// 1) 自检：增量视图 == 由状态批量重算。
	if err := m.SelfCheck(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}

	// 2) 下游重放输出日志 == 维护器视图。
	replayed := map[rowKey]struct{}{}
	for _, e := range m.Log() {
		k := toRowKey(e.Row)
		if e.Add {
			replayed[k] = struct{}{}
		} else {
			if _, ok := replayed[k]; !ok {
				t.Fatalf("日志重放撤回了不存在的行: %s", fmtEntry(e))
			}
			delete(replayed, k)
		}
	}
	if !reflect.DeepEqual(sortedRows(replayed), m.View()) {
		t.Fatalf("日志重放结果与视图不一致: replay %s, view %s",
			fmtRows(sortedRows(replayed)), fmtRows(m.View()))
	}

	// 3) 独立参照实现 Recompute == 维护器视图。
	want := Recompute(batches...)
	if !reflect.DeepEqual(want, m.View()) {
		t.Fatalf("批量重算与视图不一致: recompute %s, view %s", fmtRows(want), fmtRows(m.View()))
	}
	t.Logf("判定依据: 60 批随机变更后，自检 / 日志重放 / 批量重算三者一致，视图 %d 行", len(m.View()))
}
