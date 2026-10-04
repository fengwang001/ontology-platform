package flow

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"ontology/quarantine"
	"ontology/rule"
)

func putRule(t *testing.T, g *Gate, id, field string, lo, hi int64, sev rule.Severity) {
	t.Helper()
	if err := g.PutRule(rule.Rule{ID: id, Field: field, Lo: lo, Hi: hi, Severity: sev}); err != nil {
		t.Fatalf("PutRule %s: %v", id, err)
	}
}

func outSeqs(out []OutRecord) []int64 {
	s := make([]int64, len(out))
	for i, r := range out {
		s[i] = r.Seq
	}
	return s
}

// TestSpecWalkthrough 精确复现题目给出的示例（C=3,K=2,r1/r2,rv 演进）。
func TestSpecWalkthrough(t *testing.T) {
	g, err := New(3, 2, 5)
	if err != nil {
		t.Fatal(err)
	}
	putRule(t, g, "r1", "amt", 0, 100, rule.Block)
	putRule(t, g, "r2", "qty", 1, 10, rule.Warn)
	if g.RV() != 2 {
		t.Fatalf("rv = %d, want 2", g.RV())
	}

	if st, err := g.Ingest("a", map[string]int64{"amt": 50, "qty": 0}); err != nil || st != StatusPassed {
		t.Fatalf("ingest a#1: st=%v err=%v", st, err)
	}
	if st, err := g.Ingest("a", map[string]int64{"amt": 150, "qty": 1}); err != nil || st != StatusQuarantined {
		t.Fatalf("ingest a#2: st=%v err=%v", st, err)
	}
	if st, err := g.Ingest("a", map[string]int64{"amt": 10, "qty": 1}); err != nil || st != StatusHeld {
		t.Fatalf("ingest a#3: st=%v err=%v", st, err)
	}
	if _, err := g.Ingest("a", map[string]int64{"amt": 1, "qty": 1}); !errors.Is(err, ErrKeyFull) {
		t.Fatalf("4th a: err=%v want ErrKeyFull", err)
	}

	if st, err := g.Ingest("b", map[string]int64{"amt": -1}); err != nil || st != StatusQuarantined {
		t.Fatalf("ingest b: st=%v err=%v", st, err)
	}
	if got := g.Quarantined(); got != 3 {
		t.Fatalf("quarantine total = %d, want 3", got)
	}
	evalsBefore := g.Evals()
	if _, err := g.Ingest("c", map[string]int64{"amt": 200, "qty": 1}); !errors.Is(err, ErrFull) {
		t.Fatalf("ingest c bad at full: err=%v want ErrFull", err)
	}
	if g.Evals() != evalsBefore {
		t.Fatalf("rejected ingest changed evals: before=%d after=%d", evalsBefore, g.Evals())
	}
	if st, err := g.Ingest("c", map[string]int64{"amt": 20, "qty": 5}); err != nil || st != StatusPassed {
		t.Fatalf("ingest c good: st=%v err=%v", st, err)
	}
	if got := outSeqs(g.Out()); !reflect.DeepEqual(got, []int64{1, 5}) {
		t.Fatalf("Out seqs = %v, want [1 5]", got)
	}

	putRule(t, g, "r1", "amt", 0, 200, rule.Block)
	if g.RV() != 3 {
		t.Fatalf("rv = %d, want 3", g.RV())
	}

	if n, err := g.Reeval("a"); err != nil || n != 2 {
		t.Fatalf("Reeval a: n=%d err=%v", n, err)
	}
	if got := outSeqs(g.Out()); !reflect.DeepEqual(got, []int64{1, 5, 2, 3}) {
		t.Fatalf("Out seqs = %v, want [1 5 2 3]", got)
	}
	if n, err := g.Reeval("b"); err != nil || n != 0 {
		t.Fatalf("Reeval b: n=%d err=%v", n, err)
	}
	front, ok := g.Front("b")
	if !ok || front.Seq != 4 || front.RV != 3 {
		t.Fatalf("b front = %+v ok=%v, want seq4 rv3", front, ok)
	}
	if !reflect.DeepEqual(front.Violations, []string{"r1", "r2"}) {
		t.Fatalf("b violations = %v, want [r1 r2]", front.Violations)
	}

	if n, err := g.Fix("b", map[string]int64{"amt": 5}); err != nil || n != 1 {
		t.Fatalf("Fix b: n=%d err=%v", n, err)
	}
	out := g.Out()
	last := out[len(out)-1]
	if last.Seq != 4 || last.Forced || !reflect.DeepEqual(last.Violations, []string{"r2"}) || last.RV != 3 {
		t.Fatalf("Fix release = %+v, want seq4 warn[r2] rv3 forced=false", last)
	}
	if got := outSeqs(out); !reflect.DeepEqual(got, []int64{1, 5, 2, 3, 4}) {
		t.Fatalf("Out seqs = %v, want [1 5 2 3 4]", got)
	}
}

// TestHeldNotJudged 本可通过的记录被同键隔离者挡住：成为 Held 且不判定。
func TestHeldNotJudged(t *testing.T) {
	g, _ := New(5, 3, 5)
	putRule(t, g, "r1", "amt", 0, 10, rule.Block)
	g.Ingest("k", map[string]int64{"amt": 99}) // Q

	before := g.Evals()
	st, err := g.Ingest("k", map[string]int64{"amt": 5}) // 本可通过
	if err != nil || st != StatusHeld {
		t.Fatalf("st=%v err=%v, want Held", st, err)
	}
	if g.Evals() != before {
		t.Fatalf("Held 记录触发了判定: evals %d -> %d", before, g.Evals())
	}
	front, _ := g.Front("k")
	if front.State != quarantine.Quarantined || !reflect.DeepEqual(front.Violations, []string{"r1"}) {
		t.Fatalf("front = %+v, want Quarantined [r1]", front)
	}
	second, _ := g.zone.At("k", 1)
	if second.State != quarantine.Held || len(second.Violations) != 0 || second.RV != 0 {
		t.Fatalf("held entry = %+v, want 无任何判定痕迹", second)
	}
}

// TestReevalStopsAtFirstFailure 在首个不通过者处停下，其后记录不动。
func TestReevalStopsAtFirstFailure(t *testing.T) {
	g, _ := New(6, 6, 5)
	putRule(t, g, "r1", "amt", 0, 10, rule.Block)
	g.Ingest("k", map[string]int64{"amt": 11}) // seq1 Q
	g.Ingest("k", map[string]int64{"amt": 12}) // seq2 Held
	g.Ingest("k", map[string]int64{"amt": 30}) // seq3 Held

	putRule(t, g, "r1", "amt", 0, 11, rule.Block) // seq1(11) 放行，seq2(12) 拦下，seq3 不动
	before := g.Evals()
	n, err := g.Reeval("k")
	if err != nil || n != 1 {
		t.Fatalf("Reeval n=%d err=%v, want 1", n, err)
	}
	if g.Evals()-before != 2 {
		t.Fatalf("evals delta = %d, want 2 (1 放行 + 1 拦下)", g.Evals()-before)
	}
	if got := outSeqs(g.Out()); !reflect.DeepEqual(got, []int64{1}) {
		t.Fatalf("Out = %v, want [1]", got)
	}
	front, _ := g.Front("k")
	if front.Seq != 2 || front.RV != 2 || !reflect.DeepEqual(front.Violations, []string{"r1"}) {
		t.Fatalf("new front = %+v, want seq2 rv2 [r1]", front)
	}
	if third, ok := g.zone.At("k", 1); !ok || third.Seq != 3 || third.State != quarantine.Held {
		t.Fatalf("third = %+v ok=%v, want seq3 仍为 Held 且未动", third, ok)
	}
}

// TestReevalEvalComplexity 队首仍不通过时，evals 与 Held 条数无关（10 与 1000 两档同为 1）。
func TestReevalEvalComplexity(t *testing.T) {
	run := func(k int) int64 {
		g, _ := New(k, k, 5)
		putRule(t, g, "r1", "amt", 0, 0, rule.Block)
		g.Ingest("k", map[string]int64{"amt": 1})
		for i := 1; i < k; i++ {
			if st, err := g.Ingest("k", map[string]int64{"amt": 9}); err != nil || st != StatusHeld {
				t.Fatalf("fill %d: st=%v err=%v", i, st, err)
			}
		}
		if g.QueueLen("k") != k {
			t.Fatalf("len = %d, want %d", g.QueueLen("k"), k)
		}
		before := g.Evals()
		if n, err := g.Reeval("k"); err != nil || n != 0 {
			t.Fatalf("Reeval: n=%d err=%v", n, err)
		}
		return g.Evals() - before
	}
	if d := run(10); d != 1 {
		t.Fatalf("len 10 evals = %d, want 1", d)
	}
	if d := run(1000); d != 1 {
		t.Fatalf("len 1000 evals = %d, want 1", d)
	}
}

// TestFullCapacityDirectPass 容量恰满时可直接放行的记录不受影响。
func TestFullCapacityDirectPass(t *testing.T) {
	g, _ := New(2, 2, 5)
	putRule(t, g, "r1", "amt", 0, 10, rule.Block)
	g.Ingest("a", map[string]int64{"amt": 99})
	g.Ingest("b", map[string]int64{"amt": 99})
	if g.Quarantined() != 2 {
		t.Fatal("quarantine should be full")
	}
	st, err := g.Ingest("c", map[string]int64{"amt": 5})
	if err != nil || st != StatusPassed {
		t.Fatalf("direct pass at full: st=%v err=%v", st, err)
	}
	if _, err := g.Ingest("d", map[string]int64{"amt": 99}); !errors.Is(err, ErrFull) {
		t.Fatalf("blocked record at full: err=%v want ErrFull", err)
	}
	if g.Quarantined() != 2 || g.NextSeq() != 4 {
		t.Fatalf("total=%d nextSeq=%d, want 2 / 4 (拒绝不占号)", g.Quarantined(), g.NextSeq())
	}
}

// TestKeyFullBeforeFull 总容量也满时，超每键上限优先报 ErrKeyFull。
func TestKeyFullBeforeFull(t *testing.T) {
	g, _ := New(2, 1, 5)
	putRule(t, g, "r1", "amt", 0, 10, rule.Block)
	g.Ingest("a", map[string]int64{"amt": 99}) // a 队列达 K=1
	g.Ingest("b", map[string]int64{"amt": 99}) // 总容量达 C=2
	if _, err := g.Ingest("a", map[string]int64{"amt": 1}); !errors.Is(err, ErrKeyFull) {
		t.Fatalf("err = %v, want ErrKeyFull", err)
	}
}

// TestReleaseViolationList Release 取最近一次判定的全部违规（含 Warn），不重新判定。
func TestReleaseViolationList(t *testing.T) {
	g, _ := New(3, 3, 5)
	putRule(t, g, "r1", "amt", 0, 10, rule.Block)
	putRule(t, g, "r2", "qty", 0, 5, rule.Warn)
	g.Ingest("k", map[string]int64{"amt": 99, "qty": 99}) // Q: [r1,r2]
	g.Ingest("k", map[string]int64{"amt": 1, "qty": 1})   // Held

	putRule(t, g, "r1", "amt", 0, 1000, rule.Block) // 规则放宽，但 Release 不重判
	before := g.Evals()
	n, err := g.Release("k")
	if err != nil {
		t.Fatal(err)
	}
	if g.Evals() != before+1 { // 强制放行不判定，隐式 Reeval 对新队首判定 1 次
		t.Fatalf("evals delta = %d, want 1", g.Evals()-before)
	}
	if n != 1 {
		t.Fatalf("implicit reeval released = %d, want 1", n)
	}
	out := g.Out()
	if r := out[0]; !r.Forced || !reflect.DeepEqual(r.Violations, []string{"r1", "r2"}) || r.RV != 2 {
		t.Fatalf("forced out = %+v, want Forced [r1 r2] rv2", r)
	}
	if r := out[1]; r.Seq != 2 || r.Forced {
		t.Fatalf("held released = %+v, want seq2 non-forced", r)
	}
	if _, err := g.Release("nokey"); !errors.Is(err, ErrNoQueue) {
		t.Fatalf("Release nokey: err=%v want ErrNoQueue", err)
	}
}

// TestDiscardNewFrontAndBan Discard 后新队首被首次判定；封禁恰达 Dmax，封禁不影响存量。
func TestDiscardNewFrontAndBan(t *testing.T) {
	g, _ := New(5, 5, 2)
	putRule(t, g, "r1", "amt", 0, 10, rule.Block)
	g.Ingest("k", map[string]int64{"amt": 99}) // seq1 Q
	g.Ingest("k", map[string]int64{"amt": 1})  // seq2 Held（本可通过）

	before := g.Evals()
	n, err := g.Discard("k") // 丢 seq1，隐式重判：seq2 通过
	if err != nil || n != 1 {
		t.Fatalf("Discard#1: n=%d err=%v", n, err)
	}
	if g.Evals()-before != 1 {
		t.Fatalf("evals delta = %d, want 1（新队首首次判定）", g.Evals()-before)
	}
	if dr := g.Dropped(); len(dr) != 1 || dr[0].Seq != 1 {
		t.Fatalf("dropped = %+v, want [seq1]", dr)
	}
	if got := outSeqs(g.Out()); !reflect.DeepEqual(got, []int64{2}) {
		t.Fatalf("Out = %v, want [2]", got)
	}
	if g.Banned("k") {
		t.Fatal("1 次丢弃不应封禁（Dmax=2）")
	}

	// 第二次 Discard 无队列可做：先再制造一条。
	g.Ingest("k", map[string]int64{"amt": 99}) // seq3 Q（未封禁前接收）
	if _, err := g.Discard("k"); err != nil {
		t.Fatal(err)
	}
	if !g.Banned("k") {
		t.Fatal("恰达 Dmax 应封禁")
	}
	// 封禁后新 Ingest 被拒，不占号。
	if _, err := g.Ingest("k", map[string]int64{"amt": 1}); !errors.Is(err, ErrBanned) {
		t.Fatalf("banned ingest: err=%v want ErrBanned", err)
	}
	if _, err := g.Discard("nokey"); !errors.Is(err, ErrNoQueue) {
		t.Fatalf("Discard nokey: err=%v want ErrNoQueue", err)
	}
}

// TestErrorPrecedence 拒绝次序：参数非法 > ErrBanned > ErrKeyFull > ErrFull。
func TestErrorPrecedence(t *testing.T) {
	g, _ := New(1, 1, 1)
	putRule(t, g, "r1", "amt", 0, 10, rule.Block)
	g.Ingest("k", map[string]int64{"amt": 99})
	g.Discard("k") // 达 Dmax=1，封禁

	cases := []struct {
		name string
		fn   func() error
		want error
	}{
		{"invalid beats banned", func() error {
			_, err := g.Ingest("", map[string]int64{"amt": 1})
			return err
		}, ErrInvalidParam},
		{"invalid beats everything", func() error {
			_, err := g.Ingest("k", map[string]int64{})
			return err
		}, ErrInvalidParam},
		{"banned beats keyfull/full", func() error {
			g2, _ := New(1, 1, 1)
			putRule(t, g2, "r1", "amt", 0, 10, rule.Block)
			g2.Ingest("k", map[string]int64{"amt": 99})
			g2.Discard("k")
			_, err := g2.Ingest("k", map[string]int64{"amt": 1})
			return err
		}, ErrBanned},
		{"reeval no queue", func() error {
			_, err := g.Reeval("empty")
			return err
		}, ErrNoQueue},
		{"invalid beats no queue", func() error {
			_, err := g.Fix("", map[string]int64{"x": 1})
			return err
		}, ErrInvalidParam},
		{"fix too many fields", func() error {
			_, err := g.Fix("k", map[string]int64{})
			return err
		}, ErrInvalidParam},
		{"drop missing rule", func() error { return g.DropRule("nope") }, ErrRuleNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.fn(); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

// TestBatchInteractionRollback 批内同键相互作用：前条入队使后条 Held；整批回滚。
func TestBatchInteractionRollback(t *testing.T) {
	g, _ := New(3, 2, 5)
	putRule(t, g, "r1", "amt", 0, 10, rule.Block)

	// 全可接受：a(不合格,Q) a(合格,Held) b(合格,Pass)
	statuses, err := g.IngestBatch([]Item{
		{Key: "a", Fields: map[string]int64{"amt": 99}},
		{Key: "a", Fields: map[string]int64{"amt": 1}},
		{Key: "b", Fields: map[string]int64{"amt": 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Status{StatusQuarantined, StatusHeld, StatusPassed}
	if !reflect.DeepEqual(statuses, want) {
		t.Fatalf("statuses = %v, want %v", statuses, want)
	}
	if g.NextSeq() != 4 || g.Quarantined() != 2 {
		t.Fatalf("seq=%d total=%d, want 4 / 2", g.NextSeq(), g.Quarantined())
	}

	// 整批拒绝：第 0 条非法参数（最小下标）。
	snapshotSeq, snapshotEvals, snapshotTotal := g.NextSeq(), g.Evals(), g.Quarantined()
	_, err = g.IngestBatch([]Item{
		{Key: "", Fields: map[string]int64{"amt": 1}},
		{Key: "a", Fields: map[string]int64{"amt": 1}},
	})
	var be *BatchError
	if !errors.As(err, &be) || be.Index != 0 || !errors.Is(be.Err, ErrInvalidParam) {
		t.Fatalf("err=%v, want BatchError{0,Invalid}", err)
	}

	// 整批拒绝：下标 2 处 ErrKeyFull（模拟期间 a 又加 1 条，到达 K=2）。
	_, err = g.IngestBatch([]Item{
		{Key: "c", Fields: map[string]int64{"amt": 99}}, // 入区（区剩 1 空位后满）
		{Key: "d", Fields: map[string]int64{"amt": 1}},  // 合格直放（不受容量影响）
		{Key: "a", Fields: map[string]int64{"amt": 1}},  // a 已有2条 → ErrKeyFull
	})
	if !errors.As(err, &be) || be.Index != 2 || !errors.Is(be.Err, ErrKeyFull) {
		t.Fatalf("err=%v, want BatchError{2,KeyFull}", err)
	}
	// 容量满在批模拟中也逐条累计：第 0 条入区占最后空位，第 2 条新键不合格则会是 ErrFull。
	_, err = g.IngestBatch([]Item{
		{Key: "c", Fields: map[string]int64{"amt": 99}},
		{Key: "d", Fields: map[string]int64{"amt": 99}},
	})
	if !errors.As(err, &be) || be.Index != 1 || !errors.Is(be.Err, ErrFull) {
		t.Fatalf("err=%v, want BatchError{1,Full}", err)
	}

	if g.NextSeq() != snapshotSeq || g.Evals() != snapshotEvals || g.Quarantined() != snapshotTotal {
		t.Fatalf("回滚不彻底: seq %d/%d evals %d/%d total %d/%d",
			g.NextSeq(), snapshotSeq, g.Evals(), snapshotEvals, g.Quarantined(), snapshotTotal)
	}

	// 批大小边界。
	if _, err := g.IngestBatch(nil); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("empty batch: err=%v", err)
	}
	big := make([]Item, 101)
	for i := range big {
		big[i] = Item{Key: "z", Fields: map[string]int64{"amt": 1}}
	}
	if _, err := g.IngestBatch(big); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("101 batch: err=%v", err)
	}
}

// TestReevalAllKeyOrder ReevalAll 按键字节序处理。
func TestReevalAllKeyOrder(t *testing.T) {
	g, _ := New(10, 10, 5)
	putRule(t, g, "r1", "amt", 0, 10, rule.Block)
	g.Ingest("b", map[string]int64{"amt": 99})
	g.Ingest("a", map[string]int64{"amt": 99})
	putRule(t, g, "r1", "amt", 0, 1000, rule.Block)
	if n := g.ReevalAll(); n != 2 {
		t.Fatalf("ReevalAll = %d, want 2", n)
	}
	if got := outSeqs(g.Out()); !reflect.DeepEqual(got, []int64{2, 1}) {
		t.Fatalf("Out = %v, want 按键字节序 [2(a) 1(b)]", got)
	}
}

// TestConcurrentSafe 并发混合调用不崩溃、不变量保持成立（-race 下检测数据竞争）。
func TestConcurrentSafe(t *testing.T) {
	g, err := New(64, 8, 1000)
	if err != nil {
		t.Fatal(err)
	}
	putRule(t, g, "r1", "amt", 0, 10, rule.Block)

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 300; i++ {
				key := string(rune('a' + (w+i)%6))
				switch i % 7 {
				case 0:
					g.Ingest(key, map[string]int64{"amt": int64((w + i) % 20)})
				case 1:
					g.IngestBatch([]Item{
						{Key: key, Fields: map[string]int64{"amt": 1}},
						{Key: string(rune('a' + (w+i+1)%6)), Fields: map[string]int64{"amt": 2}},
					})
				case 2:
					g.Reeval(key)
				case 3:
					g.ReevalAll()
				case 4:
					g.Fix(key, map[string]int64{"amt": 5})
				case 5:
					g.Release(key)
				case 6:
					g.Discard(key)
				}
				_ = g.Out()
				_ = g.Dropped()
			}
		}(w)
	}
	wg.Wait()

	if g.Quarantined() > 64 {
		t.Fatalf("quarantine exceeded capacity: %d", g.Quarantined())
	}
	// 每条被接受记录恰在 Out、Dropped、隔离区三者之一；Out∪Dropped 的 seq 互不重复。
	seen := map[int64]int{}
	for _, r := range g.Out() {
		seen[r.Seq]++
	}
	for _, r := range g.Dropped() {
		seen[r.Seq]++
	}
	for seq, n := range seen {
		if n != 1 {
			t.Fatalf("seq %d appears %d times in Out∪Dropped", seq, n)
		}
	}
	if int64(len(seen))+int64(g.Quarantined()) != g.NextSeq()-1 {
		t.Fatalf("accepted accounting broken: out+dropped=%d held=%d accepted=%d",
			len(seen), g.Quarantined(), g.NextSeq()-1)
	}
}

// TestFixOverLimitRejected Fix 合并后超 16 字段时拒绝且状态不变。
func TestFixOverLimitRejected(t *testing.T) {
	g, _ := New(5, 5, 5)
	putRule(t, g, "r1", "fa", 0, 10, rule.Block)
	fields := map[string]int64{}
	for i := 0; i < 16; i++ {
		fields["f"+string(rune('a'+i))] = 99
	}
	if st, _ := g.Ingest("k", fields); st != StatusQuarantined {
		t.Fatalf("setup st = %v", st)
	}
	front, _ := g.Front("k")

	// 新增第 17 个字段：拒绝。
	if _, err := g.Fix("k", map[string]int64{"zzz": 1}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Fix over limit: err=%v", err)
	}
	front2, _ := g.Front("k")
	if !reflect.DeepEqual(front2.Fields, front.Fields) {
		t.Fatal("被拒绝的 Fix 修改了队首字段")
	}
	// 覆盖已有字段且修复成功：隐式重判放行。
	patch := map[string]int64{"fa": 1}
	if n, err := g.Fix("k", patch); err != nil || n != 1 {
		t.Fatalf("Fix existing: n=%d err=%v", n, err)
	}
	if r := g.Out()[0]; r.Seq != front.Seq || r.Forced {
		t.Fatalf("fixed release = %+v", r)
	}
}

// TestNewValidation 构造参数非法时返回 ErrInvalidParam。
func TestNewValidation(t *testing.T) {
	cases := [][3]int{
		{0, 0, 1}, {100001, 1, 1}, {2, 3, 1}, {1, 1, 0}, {1, 1, 1001},
	}
	for _, p := range cases {
		if _, err := New(p[0], p[1], p[2]); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("New(%v) err=%v, want ErrInvalidParam", p, err)
		}
	}
	if _, err := New(1, 1, 1); err != nil {
		t.Fatalf("New(1,1,1): %v", err)
	}
}
