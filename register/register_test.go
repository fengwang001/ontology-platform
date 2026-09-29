package register

import (
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"testing"
)

func logWrites(t *testing.T, stage string, writes []Write) {
	t.Helper()
	t.Logf("[%s] 写入批次: %v", stage, writes)
}

func logChanges(t *testing.T, stage string, changes []Change, discarded int) {
	t.Helper()
	t.Logf("[%s] 变更日志: %v", stage, changes)
	t.Logf("[%s] 本批后写落败丢弃数: %d", stage, discarded)
}

func logView(t *testing.T, stage string, r *Register) {
	t.Helper()
	snap := r.Snapshot()
	keys := make([]string, 0, len(snap))
	for k := range snap {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		e := snap[k]
		t.Logf("[%s] 生效值: key=%s -> {seq:%d value:%q}", stage, k, e.Seq, e.Value)
	}
	t.Logf("[%s] 判定依据: 每键保留迄今最小逻辑序号，累计丢弃=%d", stage, r.Discarded())
}

func TestFirstWriteWinsAndLateLoss(t *testing.T) {
	r := New()

	batch1 := []Write{{Key: "a", Seq: 10, Value: "v10"}, {Key: "b", Seq: 20, Value: "v20"}}
	logWrites(t, "首写建立", batch1)
	changes, discarded, err := r.Apply(batch1)
	if err != nil {
		t.Fatalf("首写批次不应被拒: %v", err)
	}
	logChanges(t, "首写建立", changes, discarded)
	logView(t, "首写建立", r)

	want := []Change{
		{Kind: ChangeEstablish, Key: "a", Seq: 10, Value: "v10"},
		{Kind: ChangeEstablish, Key: "b", Seq: 20, Value: "v20"},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("首写日志 = %v, want %v", changes, want)
	}
	if discarded != 0 {
		t.Fatalf("首写丢弃数 = %d, want 0", discarded)
	}

	batch2 := []Write{{Key: "a", Seq: 11, Value: "v11-late"}, {Key: "a", Seq: 9, Value: "v9-early"}}
	logWrites(t, "后写落败+更早序号撤回", batch2)
	changes, discarded, err = r.Apply(batch2)
	if err != nil {
		t.Fatalf("批次不应被拒: %v", err)
	}
	logChanges(t, "后写落败+更早序号撤回", changes, discarded)
	logView(t, "后写落败+更早序号撤回", r)

	// seq=11 > 当前10: 后写落败丢弃; seq=9 < 当前10: 先撤回再建立。
	want = []Change{
		{Kind: ChangeWithdraw, Key: "a", Seq: 10, Value: "v10"},
		{Kind: ChangeEstablish, Key: "a", Seq: 9, Value: "v9-early"},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("撤回重建日志 = %v, want %v", changes, want)
	}
	if discarded != 1 || r.Discarded() != 1 {
		t.Fatalf("丢弃数 = 本批%d/累计%d, want 1/1", discarded, r.Discarded())
	}

	if e, ok := r.Lookup("a"); !ok || e != (Entry{Seq: 9, Value: "v9-early"}) {
		t.Fatalf("a 生效值 = %v,%v, want {9 v9-early}", e, ok)
	}
	if e, ok := r.Lookup("b"); !ok || e.Seq != 20 {
		t.Fatalf("b 生效值 = %v,%v, want seq20", e, ok)
	}
	if err := r.Verify(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
	t.Logf("[最终自检] 重放变更日志与物化视图一致: OK")
}

func TestWithdrawMatchesMaterialized(t *testing.T) {
	r := New()
	writes := []Write{
		{Key: "k", Seq: 5, Value: "five"},
		{Key: "k", Seq: 3, Value: "three"},
		{Key: "k", Seq: 1, Value: "one"},
	}
	logWrites(t, "连续撤回", writes)
	if _, _, err := r.Apply(writes[:1]); err != nil {
		t.Fatal(err)
	}
	changes, _, err := r.Apply(writes[1:])
	if err != nil {
		t.Fatal(err)
	}
	logChanges(t, "连续撤回", changes, 0)
	logView(t, "连续撤回", r)

	want := []Change{
		{Kind: ChangeWithdraw, Key: "k", Seq: 5, Value: "five"},
		{Kind: ChangeEstablish, Key: "k", Seq: 3, Value: "three"},
		{Kind: ChangeWithdraw, Key: "k", Seq: 3, Value: "three"},
		{Kind: ChangeEstablish, Key: "k", Seq: 1, Value: "one"},
	}
	if !reflect.DeepEqual(changes, want) {
		t.Fatalf("撤回必须恰好匹配当时物化条目: got %v want %v", changes, want)
	}
	if e, _ := r.Lookup("k"); e.Seq != 1 || e.Value != "one" {
		t.Fatalf("生效值 = %+v, want {1 one}", e)
	}
	if err := r.Verify(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

func TestRejectionReasonsAndAtomicity(t *testing.T) {
	cases := []struct {
		name   string
		writes []Write
		reason error
	}{
		{"空键", []Write{{Key: "", Seq: 1, Value: "x"}}, ErrEmptyKey},
		{"空白键", []Write{{Key: "   ", Seq: 1, Value: "x"}}, ErrEmptyKey},
		{"序号为零", []Write{{Key: "a", Seq: 0, Value: "x"}}, ErrInvalidSeq},
		{"序号为负", []Write{{Key: "a", Seq: -3, Value: "x"}}, ErrInvalidSeq},
		{"批内序号重复", []Write{
			{Key: "a", Seq: 7, Value: "x"},
			{Key: "a", Seq: 7, Value: "y"},
		}, ErrDuplicateSeq},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			if _, _, err := r.Apply([]Write{{Key: "a", Seq: 7, Value: "seed"}}); err != nil {
				t.Fatal(err)
			}
			logWrites(t, tc.name, tc.writes)
			_, _, err := r.Apply(tc.writes)
			if err == nil {
				t.Fatalf("%s: 期望拒绝，实际通过", tc.name)
			}
			if !errors.Is(err, tc.reason) {
				t.Fatalf("%s: 原因 = %v, want %v", tc.name, err, tc.reason)
			}
			var reject *RejectError
			if !errors.As(err, &reject) {
				t.Fatalf("%s: 错误应为 *RejectError, got %T", tc.name, err)
			}
			t.Logf("[%s] 拒绝原因可区分: %v (index=%d)", tc.name, err, reject.Index)

			snap := r.Snapshot()
			if len(snap) != 1 || snap["a"] != (Entry{Seq: 7, Value: "seed"}) {
				t.Fatalf("%s: 拒绝后状态被污染: %v", tc.name, snap)
			}
			if r.Discarded() != 0 {
				t.Fatalf("%s: 拒绝后丢弃计数被污染: %d", tc.name, r.Discarded())
			}
			if len(r.ChangeLog()) != 1 {
				t.Fatalf("%s: 拒绝后日志被污染: %v", tc.name, r.ChangeLog())
			}
			t.Logf("[%s] 整批不生效: 生效值=%v, 日志长度=1, 判定依据=先全量校验后应用",
				tc.name, snap)
		})
	}

	t.Run("重复当前生效序号", func(t *testing.T) {
		r := New()
		if _, _, err := r.Apply([]Write{{Key: "a", Seq: 7, Value: "seed"}}); err != nil {
			t.Fatal(err)
		}
		w := []Write{{Key: "a", Seq: 7, Value: "dup-of-effective"}}
		logWrites(t, "重复当前生效序号", w)
		_, _, err := r.Apply(w)
		if !errors.Is(err, ErrDuplicateSeq) {
			t.Fatalf("原因 = %v, want ErrDuplicateSeq", err)
		}
		t.Logf("[重复当前生效序号] 拒绝: %v", err)
		if e, _ := r.Lookup("a"); e.Value != "seed" {
			t.Fatalf("拒绝后生效值被污染: %+v", e)
		}
	})
}

func TestSeqMonotonicNonIncrease(t *testing.T) {
	r := New()
	var writes []Write
	seqs := []int64{100, 150, 80, 90, 60, 200, 40}
	for i, s := range seqs {
		writes = append(writes, Write{Key: "k", Seq: s, Value: fmt.Sprintf("v%d", i)})
	}
	logWrites(t, "序号只减不增", writes)

	changes, discarded, err := r.Apply(writes)
	if err != nil {
		t.Fatal(err)
	}
	logChanges(t, "序号只减不增", changes, discarded)

	prev := int64(1<<62 - 1)
	established := 0
	for _, c := range r.ChangeLog() {
		if c.Kind == ChangeEstablish {
			established++
			if c.Seq >= prev {
				t.Fatalf("生效序号只减不增被破坏: %d -> %d", prev, c.Seq)
			}
			prev = c.Seq
		}
	}
	if established != 4 {
		t.Fatalf("建立次数 = %d, want 4 (100,80,60,40)", established)
	}
	logView(t, "序号只减不增", r)
	if e, _ := r.Lookup("k"); e.Seq != 40 {
		t.Fatalf("最终生效序号 = %d, want 40 (逐键最小序号)", e.Seq)
	}
	// 150 负于 100；90 负于 80；200 负于 60。
	if discarded != 3 || r.Discarded() != 3 {
		t.Fatalf("丢弃数 = 本批%d/累计%d, want 3/3", discarded, r.Discarded())
	}
	if err := r.Verify(); err != nil {
		t.Fatal(err)
	}
}

func TestConcurrentReadersConsistentView(t *testing.T) {
	r := New()

	var seeds []Write
	for i := 0; i < 16; i++ {
		seeds = append(seeds, Write{Key: fmt.Sprintf("k%02d", i), Seq: 100, Value: "old"})
	}
	if _, _, err := r.Apply(seeds); err != nil {
		t.Fatal(err)
	}

	const readers = 16
	const iterations = 2000
	var wg sync.WaitGroup
	views := make([]map[string]Entry, readers)
	discarded := make([]int64, readers)
	start := make(chan struct{})

	for g := 0; g < readers; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			<-start
			// 每次 Snapshot 必须是某一时刻自洽的视图；写入并发推进时，
			// 任一键的生效序号在跨快照之间只减不增，丢弃数只增不减。
			prev := map[string]int64{}
			var prevDiscarded int64
			for it := 0; it < iterations; it++ {
				snap := r.Snapshot()
				for k, e := range snap {
					if old, ok := prev[k]; ok && e.Seq > old {
						t.Errorf("reader %d iter %d: 键 %s 生效序号增长 %d -> %d", g, it, k, old, e.Seq)
						return
					}
					prev[k] = e.Seq
				}
				for k := range prev {
					if _, ok := snap[k]; !ok {
						t.Errorf("reader %d iter %d: 键 %s 从视图中消失", g, it, k)
						return
					}
				}
				views[g] = snap
				d := r.Discarded()
				if d < prevDiscarded {
					t.Errorf("reader %d iter %d: 丢弃计数倒退 %d -> %d", g, it, prevDiscarded, d)
					return
				}
				prevDiscarded = d
				discarded[g] = d
				if err := r.Verify(); err != nil {
					t.Errorf("reader %d iter %d 自检失败: %v", g, it, err)
					return
				}
			}
		}(g)
	}

	writerDone := make(chan struct{})
	go func() {
		defer close(writerDone)
		<-start
		for s := int64(99); s > 0; s-- {
			key := fmt.Sprintf("k%02d", s%16)
			_, _, _ = r.Apply([]Write{{Key: key, Seq: s, Value: "early"}})
		}
	}()

	close(start)
	wg.Wait()
	<-writerDone

	final := r.Snapshot()
	for g := 0; g < readers; g++ {
		if !reflect.DeepEqual(views[g], final) {
			t.Fatalf("reader %d 最终视图与权威视图逐字段不一致", g)
		}
		if discarded[g] < 0 || discarded[g] > r.Discarded() {
			t.Fatalf("reader %d 丢弃计数异常: %d", g, discarded[g])
		}
	}
	t.Logf("[并发一致性] %d 个读 goroutine 与写入并发执行 Lookup/Snapshot/Discarded/Verify，最终视图逐字段相同",
		readers)
	logView(t, "并发一致性", r)

	// 逐键取最小序号核对：用变更日志里的全部 establish 重算每键最小序号。
	minSeq := map[string]int64{}
	for _, c := range r.ChangeLog() {
		if c.Kind != ChangeEstablish {
			continue
		}
		if s, ok := minSeq[c.Key]; !ok || c.Seq < s {
			minSeq[c.Key] = c.Seq
		}
	}
	for k, e := range final {
		if e.Seq != minSeq[k] {
			t.Fatalf("键 %s 生效序号 %d != 逐键最小序号 %d", k, e.Seq, minSeq[k])
		}
	}
	t.Logf("[逐键最小序号核对] 全部 %d 个键的生效序号均等于其所有已接受写入的最小序号", len(final))
}
