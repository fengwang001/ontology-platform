package join

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
)

// ---- 测试辅助 ----

func discardJoiner(t *testing.T, max int64) *Joiner {
	t.Helper()
	return New(Options{
		MaxResultTuples: max,
		Logger:          slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
}

func mustAccept(t *testing.T, j *Joiner, b Batch) []JoinDelta {
	t.Helper()
	res := j.Apply(b)
	if !res.Accepted {
		t.Fatalf("expected batch accepted, got rejection: %v", res.Err)
	}
	return res.Deltas
}

func mustReject(t *testing.T, j *Joiner, b Batch, reason RejectReason) *RejectError {
	t.Helper()
	res := j.Apply(b)
	if res.Accepted {
		t.Fatalf("expected batch rejected with %s, but it was accepted; deltas=%v", reason, res.Deltas)
	}
	if res.Err == nil {
		t.Fatalf("rejected batch returned nil error")
	}
	if res.Err.Reason != reason {
		t.Fatalf("expected reject reason %s, got %s (%v)", reason, res.Err.Reason, res.Err)
	}
	if !errors.Is(res.Err, sentinel(reason)) {
		t.Fatalf("errors.Is mismatch for reason %s", reason)
	}
	return res.Err
}

func sentinel(r RejectReason) error {
	switch r {
	case ReasonNullValue:
		return ErrNullValue
	case ReasonInvalidDelta:
		return ErrInvalidDelta
	case ReasonDeleteNonexistent:
		return ErrDeleteNonexistent
	case ReasonResultTooLarge:
		return ErrResultTooLarge
	case ReasonMultiplicityOverflow:
		return ErrMultiplicityOverflow
	default:
		return nil
	}
}

// ch 构造一条非空变更。
func ch(key, value string, delta int64) RowChange {
	return RowChange{Key: key, Value: value, Delta: delta}
}

// sortedDeltas 复制并按 (Key,LeftValue,RightValue) 排序差分。
func sortedDeltas(d []JoinDelta) []JoinDelta {
	out := append([]JoinDelta(nil), d...)
	sort.Slice(out, func(i, j int) bool { return out[i].JoinTuple.less(out[j].JoinTuple) })
	return out
}

// ---- 基础语义：重数 = 两侧重数之积 ----

func TestMultiplicityIsProduct(t *testing.T) {
	j := discardJoiner(t, 0)
	// 左 (k,a) 重数 3，右 (k,x) 重数 2 -> 连接 (k,a,x) 重数 6。
	d := mustAccept(t, j, Batch{
		Left:  []RowChange{ch("k", "a", 3)},
		Right: []RowChange{ch("k", "x", 2)},
	})
	want := []JoinDelta{{JoinTuple: JoinTuple{"k", "a", "x"}, Delta: 6}}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("product delta = %v, want %v", d, want)
	}
	_, _, result := j.Snapshot()
	if result[JoinTuple{"k", "a", "x"}] != 6 {
		t.Fatalf("materialized multiplicity = %d, want 6", result[JoinTuple{"k", "a", "x"}])
	}
}

// ---- 同时改两张表：插入、互换、删除的差分只含净变化 ----

func TestSimultaneousChangeBothSides(t *testing.T) {
	j := discardJoiner(t, 0)
	// 初始：左 {a:1}，右 {x:1} -> {(a,x):1}
	mustAccept(t, j, Batch{Left: []RowChange{ch("k", "a", 1)}, Right: []RowChange{ch("k", "x", 1)}})

	// 同批：左删 a 插 b，右删 x 插 y。
	d := mustAccept(t, j, Batch{
		Left:  []RowChange{ch("k", "a", -1), ch("k", "b", 1)},
		Right: []RowChange{ch("k", "x", -1), ch("k", "y", 1)},
	})
	// 全量结果只剩 (b,y):1；差分中 (b,x) +1-1 抵消为 0，必须不出现。
	want := []JoinDelta{
		{JoinTuple: JoinTuple{"k", "a", "x"}, Delta: -1},
		{JoinTuple: JoinTuple{"k", "b", "y"}, Delta: 1},
	}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("simultaneous-change deltas = %v, want %v", d, want)
	}
	left, right, result := j.Snapshot()
	if !reflect.DeepEqual(left, map[Row]int64{{Key: "k", Value: "b"}: 1}) {
		t.Fatalf("left = %v", left)
	}
	if !reflect.DeepEqual(right, map[Row]int64{{Key: "k", Value: "y"}: 1}) {
		t.Fatalf("right = %v", right)
	}
	if !reflect.DeepEqual(result, map[JoinTuple]int64{{"k", "b", "y"}: 1}) {
		t.Fatalf("result = %v", result)
	}
}

func TestSimultaneousInsertsOnBothSides(t *testing.T) {
	j := discardJoiner(t, 0)
	// 两侧同批全新插入，(新左,新右) 不得遗漏。
	d := mustAccept(t, j, Batch{
		Left:  []RowChange{ch("k", "a", 1), ch("k", "b", 1)},
		Right: []RowChange{ch("k", "x", 1), ch("k", "y", 1)},
	})
	want := []JoinDelta{
		{JoinTuple: JoinTuple{"k", "a", "x"}, Delta: 1},
		{JoinTuple: JoinTuple{"k", "a", "y"}, Delta: 1},
		{JoinTuple: JoinTuple{"k", "b", "x"}, Delta: 1},
		{JoinTuple: JoinTuple{"k", "b", "y"}, Delta: 1},
	}
	if !reflect.DeepEqual(d, want) {
		t.Fatalf("deltas = %v, want %v", d, want)
	}
}

// ---- 输出有序性：按 (Key,LeftValue,RightValue) ----

func TestDeltasAreSorted(t *testing.T) {
	j := discardJoiner(t, 0)
	d := mustAccept(t, j, Batch{
		Left:  []RowChange{ch("k2", "b", 1), ch("k1", "a", 1), ch("k1", "b", 1)},
		Right: []RowChange{ch("k2", "z", 1), ch("k1", "y", 1), ch("k1", "x", 1)},
	})
	sd := sortedDeltas(d)
	if !reflect.DeepEqual(d, sd) {
		t.Fatalf("deltas not sorted:\n got %v\nwant %v", d, sd)
	}
}

// ---- 各类非法输入：原因可区分，且状态不变 ----

func TestRejectNullValues(t *testing.T) {
	cases := []struct {
		name string
		b    Batch
		side string
	}{
		{"null key left", Batch{Left: []RowChange{ch("", "a", 1)}}, "left"},
		{"null value left", Batch{Left: []RowChange{ch("k", "", 1)}}, "left"},
		{"null key right", Batch{Right: []RowChange{ch("", "x", 1)}}, "right"},
		{"null value right", Batch{Right: []RowChange{ch("k", "", 1)}}, "right"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			j := discardJoiner(t, 0)
			e := mustReject(t, j, tc.b, ReasonNullValue)
			if e.Side != tc.side {
				t.Fatalf("error side = %q, want %q", e.Side, tc.side)
			}
			l, r, res := j.Snapshot()
			if len(l)+len(r)+len(res) != 0 {
				t.Fatalf("rejected batch changed state: %v %v %v", l, r, res)
			}
		})
	}
}

func TestRejectZeroDelta(t *testing.T) {
	j := discardJoiner(t, 0)
	mustAccept(t, j, Batch{Left: []RowChange{ch("k", "a", 1)}})
	e := mustReject(t, j, Batch{Left: []RowChange{ch("k", "a", 0)}}, ReasonInvalidDelta)
	if e.Side != "left" {
		t.Fatalf("error side = %q, want left", e.Side)
	}
	// 右表零增量同样可区分。
	mustReject(t, j, Batch{Right: []RowChange{ch("k", "x", 0)}}, ReasonInvalidDelta)
}

func TestRejectDeleteNonexistent(t *testing.T) {
	j := discardJoiner(t, 0)
	mustAccept(t, j, Batch{Left: []RowChange{ch("k", "a", 2)}, Right: []RowChange{ch("k", "x", 1)}})

	before := snapshotMaps(j)
	// 删除量超过现存重数：批后负重数 -1。
	e := mustReject(t, j, Batch{Left: []RowChange{ch("k", "a", -3)}}, ReasonDeleteNonexistent)
	if e.Side != "left" || e.Key != "k" || e.Value != "a" {
		t.Fatalf("unexpected error location: side=%q key=%q value=%q", e.Side, e.Key, e.Value)
	}
	mustReject(t, j, Batch{Right: []RowChange{ch("k", "x", -1), ch("k", "y", -1)}}, ReasonDeleteNonexistent)

	after := snapshotMaps(j)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("state changed after rejected delete:\nbefore=%v\nafter =%v", before, after)
	}
}

func TestRejectResultTooLarge(t *testing.T) {
	j := discardJoiner(t, 1) // 只允许 1 个不同结果元组
	mustAccept(t, j, Batch{Left: []RowChange{ch("k", "a", 1)}, Right: []RowChange{ch("k", "x", 1)}})

	before := snapshotMaps(j)
	// 新增右行 y 将产生第二个结果元组 (a,y)，超限拒绝。
	mustReject(t, j, Batch{Right: []RowChange{ch("k", "y", 1)}}, ReasonResultTooLarge)
	after := snapshotMaps(j)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("state changed after rejected oversize batch:\nbefore=%v\nafter =%v", before, after)
	}

	// 恰好达到上限的批（替换 x 为 y，结果元组数仍为 1）应通过。
	mustAccept(t, j, Batch{Right: []RowChange{ch("k", "x", -1), ch("k", "y", 1)}})
}

func TestRejectedBatchAtomicAcrossBothSides(t *testing.T) {
	j := discardJoiner(t, 0)
	mustAccept(t, j, Batch{Left: []RowChange{ch("k", "a", 1)}, Right: []RowChange{ch("k", "x", 1)}})
	before := snapshotMaps(j)
	// 左侧合法、右侧非法：整批拒绝，左表也不得变化。
	b := Batch{
		Left:  []RowChange{ch("k", "b", 1)},
		Right: []RowChange{ch("k", "z", -5)},
	}
	mustReject(t, j, b, ReasonDeleteNonexistent)
	after := snapshotMaps(j)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("non-atomic batch:\nbefore=%v\nafter =%v", before, after)
	}
}

type tripleMaps struct {
	l, r map[Row]int64
	res  map[JoinTuple]int64
}

func snapshotMaps(j *Joiner) tripleMaps {
	l, r, res := j.Snapshot()
	return tripleMaps{l, r, res}
}

// ---- 差分 vs 全量求差：随机批次序列下逐批一致 ----

func TestDeltaMatchesFullRecompute(t *testing.T) {
	j := discardJoiner(t, 0)

	keys := []string{"k1", "k2", "k3"}
	lvals := []string{"la", "lb"}
	rvals := []string{"rx", "ry"}

	refL := map[Row]int64{}
	refR := map[Row]int64{}
	// 外部物化视图：仅按顺序累加每批返回的差分，最终必须始终等于全量重算。
	external := map[JoinTuple]int64{}

	rng := rand.New(rand.NewSource(20260928))
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }
	randomChanges := func(vals []string) []RowChange {
		n := rng.Intn(4)
		cs := make([]RowChange, n)
		for i := range cs {
			d := int64(rng.Intn(6) - 2) // [-2,3]
			if d == 0 {
				d = 1
			}
			cs[i] = ch(pick(keys), pick(vals), d)
		}
		return cs
	}

	const iterations = 3000
	for iter := 0; iter < iterations; iter++ {
		b := Batch{Left: randomChanges(lvals), Right: randomChanges(rvals)}

		oldJoin := FullJoin(refL, refR)
		res := j.Apply(b)

		// 用参考表独立判定该批是否应因负重数被拒绝。
		shouldReject := !postBatchNonNegative(refL, b.Left) || !postBatchNonNegative(refR, b.Right)

		if shouldReject {
			if res.Accepted {
				t.Fatalf("iter %d: expected rejection, got accepted: %v", iter, b)
			}
			if res.Err.Reason != ReasonDeleteNonexistent {
				t.Fatalf("iter %d: unexpected reason %s", iter, res.Err.Reason)
			}
			// 拒绝：参考表与物化状态都不变。
			l, r, got := j.Snapshot()
			if !reflect.DeepEqual(l, refL) || !reflect.DeepEqual(r, refR) || !reflect.DeepEqual(got, oldJoin) {
				t.Fatalf("iter %d: state drift after rejection", iter)
			}
			continue
		}

		if !res.Accepted {
			t.Fatalf("iter %d: unexpected rejection: %v", iter, res.Err)
		}

		// 期望差分 = 批后全量 - 批前全量（稀疏、非零、有序）。
		newL := applyNet(refL, b.Left)
		newR := applyNet(refR, b.Right)
		newJoin := FullJoin(newL, newR)
		want := diffJoins(oldJoin, newJoin)
		if !reflect.DeepEqual(res.Deltas, want) {
			t.Fatalf("iter %d: delta mismatch\nbatch=%v\ngot =%v\nwant=%v", iter, b, res.Deltas, want)
		}

		// 下游只按顺序应用差分：external 必须等于当前全量结果。
		for _, d := range res.Deltas {
			external[d.JoinTuple] += d.Delta
			if external[d.JoinTuple] == 0 {
				delete(external, d.JoinTuple)
			}
		}
		if !reflect.DeepEqual(external, newJoin) {
			t.Fatalf("iter %d: delta-applied external view != full recompute", iter)
		}

		// 物化快照与全量一致。
		l, r, got := j.Snapshot()
		if !reflect.DeepEqual(l, newL) || !reflect.DeepEqual(r, newR) || !reflect.DeepEqual(got, newJoin) {
			t.Fatalf("iter %d: snapshot != full recompute", iter)
		}
		for tuple, m := range got {
			if m < 0 {
				t.Fatalf("iter %d: negative multiplicity %d at %v", iter, m, tuple)
			}
		}

		refL, refR = newL, newR
	}
}

// postBatchNonNegative 判断批后单侧表是否出现负重数（同批同净变更先聚合）。
func postBatchNonNegative(cur map[Row]int64, changes []RowChange) bool {
	net := map[Row]int64{}
	for _, c := range changes {
		net[c.Row()] += c.Delta
	}
	for r, d := range net {
		if cur[r]+d < 0 {
			return false
		}
	}
	return true
}

// applyNet 在参考表副本上聚合净变更，归零删除。
func applyNet(cur map[Row]int64, changes []RowChange) map[Row]int64 {
	out := make(map[Row]int64, len(cur))
	for r, m := range cur {
		out[r] = m
	}
	net := map[Row]int64{}
	for _, c := range changes {
		net[c.Row()] += c.Delta
	}
	for r, d := range net {
		if d == 0 {
			continue
		}
		nv := out[r] + d
		if nv == 0 {
			delete(out, r)
		} else {
			out[r] = nv
		}
	}
	return out
}

// diffJoins 返回 new-old 的稀疏有序差分（只含非零项）。
func diffJoins(old, new map[JoinTuple]int64) []JoinDelta {
	all := map[JoinTuple]int64{}
	for t, m := range old {
		all[t] -= m
	}
	for t, m := range new {
		all[t] += m
	}
	var out []JoinDelta
	for t, d := range all {
		if d != 0 {
			out = append(out, JoinDelta{JoinTuple: t, Delta: d})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].JoinTuple.less(out[j].JoinTuple) })
	return out
}

// ---- 确定性：同一输入序列反复计算输出完全相同 ----

func TestDeterministicReplay(t *testing.T) {
	sequence := func() []Batch {
		return []Batch{
			{Left: []RowChange{ch("k", "a", 2)}, Right: []RowChange{ch("k", "x", 3)}},
			{Left: []RowChange{ch("k", "a", -1), ch("k", "b", 1)}, Right: []RowChange{ch("k", "y", 1)}},
			{Left: []RowChange{ch("k", "a", -5)}}, // 拒绝：负重数
			{Right: []RowChange{ch("k", "x", -3), ch("k", "y", -1)}},
			{Left: []RowChange{ch("k", "b", 0)}}, // 拒绝：非法符号
			{Left: []RowChange{ch("k", "b", -1)}},
			{Left: []RowChange{ch("", "a", 1)}}, // 拒绝：空值
		}
	}

	run := func() []BatchResult {
		j := discardJoiner(t, 0)
		var rs []BatchResult
		for _, b := range sequence() {
			rs = append(rs, j.Apply(b))
		}
		return rs
	}

	first := run()
	for trial := 0; trial < 5; trial++ {
		got := run()
		if !reflect.DeepEqual(got, first) {
			t.Fatalf("trial %d: replay output differs", trial)
		}
	}
}

// TestConcurrentApplyAndSnapshotV2 用清晰的 WaitGroup 划分写者/读者生命周期。
func TestConcurrentApplyAndSnapshotV2(t *testing.T) {
	j := discardJoiner(t, 0)
	const writers = 8
	const rounds = 200

	var seed Batch
	for w := 0; w < writers; w++ {
		k := fmt.Sprintf("vk%d", w)
		seed.Left = append(seed.Left, ch(k, "a", int64(rounds)))
		seed.Right = append(seed.Right, ch(k, "x", int64(rounds)))
	}
	mustAccept(t, j, seed)

	var writersWG, readersWG sync.WaitGroup
	stop := make(chan struct{})

	for r := 0; r < 4; r++ {
		readersWG.Add(1)
		go func() {
			defer readersWG.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				l, rt, res := j.Snapshot()
				if !reflect.DeepEqual(res, FullJoin(l, rt)) {
					t.Errorf("concurrent snapshot inconsistent")
					return
				}
			}
		}()
	}

	for w := 0; w < writers; w++ {
		writersWG.Add(1)
		go func(w int) {
			defer writersWG.Done()
			k := fmt.Sprintf("vk%d", w)
			localL, localR := int64(rounds), int64(rounds)
			rng := rand.New(rand.NewSource(int64(w) + 100))
			for i := 0; i < rounds; i++ {
				b := Batch{}
				if rng.Intn(2) == 0 || localL == 0 {
					b.Left = []RowChange{ch(k, "a", 1)}
					localL++
				} else {
					b.Left = []RowChange{ch(k, "a", -1)}
					localL--
				}
				if rng.Intn(2) == 0 || localR == 0 {
					b.Right = []RowChange{ch(k, "x", 1)}
					localR++
				} else {
					b.Right = []RowChange{ch(k, "x", -1)}
					localR--
				}
				if res := j.Apply(b); !res.Accepted {
					t.Errorf("writer %d rejected: %v", w, res.Err)
					return
				}
			}
		}(w)
	}

	writersWG.Wait()
	close(stop)
	readersWG.Wait()

	// 最终视图与全量一致。
	l, r, res := j.Snapshot()
	if !reflect.DeepEqual(res, FullJoin(l, r)) {
		t.Fatalf("final snapshot inconsistent with full recompute")
	}
}

// ---- 日志：输入、输出差分、判定依据均可见 ----

func TestLoggingContents(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	j := New(Options{Logger: logger})

	j.Apply(Batch{Left: []RowChange{ch("k", "a", 1)}, Right: []RowChange{ch("k", "x", 1)}})
	out := buf.String()
	for _, want := range []string{
		"join.apply.start",
		"join.apply.accepted",
		`decision=ok`,
		`left="[(key=\"k\", value=\"a\", delta=1)]"`,
		`delta=1`,
		"result_tuple_count=1",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("accepted log missing %q\nfull log:\n%s", want, out)
		}
	}

	buf.Reset()
	j.Apply(Batch{Left: []RowChange{ch("k", "a", -99)}})
	out = buf.String()
	for _, want := range []string{
		"join.apply.rejected",
		"decision=rejected",
		"reason=" + ReasonDeleteNonexistent.String(),
		"side=left",
		`deltas=[]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("rejected log missing %q\nfull log:\n%s", want, out)
		}
	}
}

// ---- 溢出保护（乘积/累加）----

func TestArithmeticOverflowGuards(t *testing.T) {
	const max = maxInt64
	const min = minInt64
	mulCases := []struct {
		a, b int64
		want int64
		ok   bool
	}{
		{3, 4, 12, true},
		{-3, 4, -12, true},
		{-3, -4, 12, true},
		{0, max, 0, true},
		{max, 1, max, true},
		{max, -1, -max, true},
		{min, 1, min, true},
		{min, -1, 0, false}, // |MinInt64|=2^63 正向不可表示
		{max, 2, 0, false},
		{max, max, 0, false},
		{min, 2, 0, false},
		{1 << 32, 1 << 31, 0, false},     // 幅度恰为 2^63 且为正
		{-(1 << 32), 1 << 31, min, true}, // 幅度恰为 2^63 且为负 = MinInt64
	}
	for _, c := range mulCases {
		got, ok := mulChecked(c.a, c.b)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("mulChecked(%d,%d)=(%d,%v), want (%d,%v)", c.a, c.b, got, ok, c.want, c.ok)
		}
	}

	addCases := []struct {
		a, b, want int64
		ok         bool
	}{
		{max, 1, 0, false},
		{min, -1, 0, false},
		{max, 0, max, true},
		{min, 0, min, true},
		{max, -1, max - 1, true},
		{min, 1, min + 1, true},
	}
	for _, c := range addCases {
		got, ok := addChecked(c.a, c.b)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("addChecked(%d,%d)=(%d,%v), want (%d,%v)", c.a, c.b, got, ok, c.want, c.ok)
		}
	}
}

func TestMultiplicityOverflow(t *testing.T) {
	j := discardJoiner(t, 0)
	mustAccept(t, j, Batch{Left: []RowChange{ch("k", "a", maxInt64)}})
	before := snapshotMaps(j)
	// maxInt64 × 2 溢出 int64，拒绝且状态不变。
	mustReject(t, j, Batch{Right: []RowChange{ch("k", "x", 2)}}, ReasonMultiplicityOverflow)
	after := snapshotMaps(j)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("overflowing batch changed state")
	}
}

// TestSameBatchInsertDeleteCancel：原子批次按净变更判定，同批 -1,+1 抵消为空操作。
func TestSameBatchInsertDeleteCancel(t *testing.T) {
	j := discardJoiner(t, 0)
	// 当前行不存在（重数 0），同批先删后增净变更为 0：接受、无差分、状态为空。
	d := mustAccept(t, j, Batch{Left: []RowChange{ch("k", "a", -1), ch("k", "a", 1)}})
	if len(d) != 0 {
		t.Fatalf("canceling batch should produce no deltas, got %v", d)
	}
	l, _, res := j.Snapshot()
	if len(l) != 0 || len(res) != 0 {
		t.Fatalf("canceling batch changed state: %v %v", l, res)
	}
}

// TestEmptyBatch：空批是合法空操作，返回空差分。
func TestEmptyBatch(t *testing.T) {
	j := discardJoiner(t, 0)
	d := mustAccept(t, j, Batch{})
	if len(d) != 0 {
		t.Fatalf("empty batch should produce no deltas, got %v", d)
	}
}
