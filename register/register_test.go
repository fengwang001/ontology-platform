package register

import (
	"errors"
	"fmt"
	"sync"
	"testing"
)

// logApply 打印一批写入、产生的变更日志、各键生效值与每条写入的判定依据。
func logApply(t *testing.T, r *Register, writes []Write, res *ApplyResult) {
	t.Helper()
	t.Log("写入批次:")
	for i, w := range writes {
		t.Logf("  [%d] key=%q seq=%d value=%q", i, w.Key, w.Seq, w.Value)
	}
	t.Log("变更日志:")
	for _, c := range res.Changes {
		t.Logf("  %s key=%q seq=%d value=%q", c.Op, c.Key, c.Seq, c.Value)
	}
	t.Log("判定依据:")
	for i, d := range res.Decisions {
		t.Logf("  [%d] key=%q -> %s", i, writes[i].Key, d)
	}
	t.Log("生效值（逐键最小序号核对）:")
	for _, e := range r.Snapshot() {
		t.Logf("  key=%q activeSeq=%d value=%q", e.Key, e.Seq, e.Value)
	}
	t.Logf("本批丢弃=%d 累计丢弃=%d", res.Discarded, r.Discarded())
}

// minSeq 独立地对一批写入做逐键取最小序号，作为结果核对的参照模型。
func minSeq(writes []Write) map[string]Write {
	want := make(map[string]Write)
	for _, w := range writes {
		if cur, ok := want[w.Key]; !ok || w.Seq < cur.Seq {
			want[w.Key] = w
		}
	}
	return want
}

func TestFirstWriteWins(t *testing.T) {
	r := New()
	writes := []Write{
		{Key: "a", Seq: 3, Value: "a@3"},
		{Key: "a", Seq: 1, Value: "a@1"},
		{Key: "a", Seq: 5, Value: "a@5"},
	}

	res1, err := r.Apply(writes[:1])
	if err != nil {
		t.Fatalf("Apply establish: %v", err)
	}
	logApply(t, r, writes[:1], res1)
	if got, ok := r.Get("a"); !ok || got.Seq != 3 || got.Value != "a@3" {
		t.Fatalf("首次建立后生效值异常: %+v ok=%v", got, ok)
	}

	res2, err := r.Apply(writes[1:2])
	if err != nil {
		t.Fatalf("Apply retract: %v", err)
	}
	logApply(t, r, writes[1:2], res2)
	if len(res2.Changes) != 2 || res2.Changes[0].Op != OpRetract || res2.Changes[1].Op != OpEstablish {
		t.Fatalf("期望先撤回后建立，实际: %+v", res2.Changes)
	}
	ret := res2.Changes[0]
	if ret.Key != "a" || ret.Seq != 3 || ret.Value != "a@3" {
		t.Fatalf("撤回必须恰好匹配当时已物化的那条，实际: %+v", ret)
	}
	got, _ := r.Get("a")
	if got.Seq != 1 || got.Value != "a@1" {
		t.Fatalf("撤回建立后期望 seq=1/a@1，实际: %+v", got)
	}

	res3, err := r.Apply(writes[2:])
	if err != nil {
		t.Fatalf("Apply discard: %v", err)
	}
	logApply(t, r, writes[2:], res3)
	if res3.Decisions[0] != DecisionDiscard || res3.Discarded != 1 || len(res3.Changes) != 0 {
		t.Fatalf("期望后写落败且无变更，实际: %+v", res3)
	}
	got, _ = r.Get("a")
	if got.Seq != 1 || got.Value != "a@1" {
		t.Fatalf("丢弃不应改变生效值，实际: %+v", got)
	}
	if r.Discarded() != 1 {
		t.Fatalf("累计丢弃数期望 1，实际 %d", r.Discarded())
	}
}

func TestEstablishAndRetractChain(t *testing.T) {
	r := New()
	// 单批内：a 建立(seq10) -> b 建立(seq7) -> a 撤回10建立(seq2) -> a 丢弃(seq8)。
	writes := []Write{
		{Key: "a", Seq: 10, Value: "a10"},
		{Key: "b", Seq: 7, Value: "b7"},
		{Key: "a", Seq: 2, Value: "a2"},
		{Key: "a", Seq: 8, Value: "a8"},
	}
	res, err := r.Apply(writes)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	logApply(t, r, writes, res)

	wantOps := []Op{OpEstablish, OpEstablish, OpRetract, OpEstablish}
	if len(res.Changes) != len(wantOps) {
		t.Fatalf("变更条数期望 %d，实际 %d: %+v", len(wantOps), len(res.Changes), res.Changes)
	}
	for i, op := range wantOps {
		if res.Changes[i].Op != op {
			t.Fatalf("changes[%d] 期望 %s，实际 %s", i, op, res.Changes[i].Op)
		}
	}
	// 撤回的必须是批内当时已物化的 a@10。
	if c := res.Changes[2]; c.Key != "a" || c.Seq != 10 || c.Value != "a10" {
		t.Fatalf("撤回记录不匹配: %+v", c)
	}

	// 用逐键取最小序号的独立模型核对结果。
	want := minSeq(writes)
	for _, e := range r.Snapshot() {
		w := want[e.Key]
		if e.Seq != w.Seq || e.Value != w.Value {
			t.Fatalf("核对失败 key=%q 期望 seq=%d value=%q，实际 %+v", e.Key, w.Seq, w.Value, e)
		}
	}
	if res.Decisions[3] != DecisionDiscard || r.Discarded() != 1 {
		t.Fatalf("期望 a@8 后写落败且累计丢弃 1，实际 decisions=%v discarded=%d",
			res.Decisions, r.Discarded())
	}
	if err := r.Check(); err != nil {
		t.Fatalf("自检失败: %v", err)
	}
}

func TestRejections(t *testing.T) {
	cases := []struct {
		name   string
		writes []Write
		reason error
		index  int
	}{
		{"空键", []Write{{Key: "", Seq: 1, Value: "x"}}, ErrEmptyKey, 0},
		{"序号为零", []Write{{Key: "b", Seq: 0, Value: "x"}}, ErrNonPositiveSeq, 0},
		{"序号为负", []Write{{Key: "b", Seq: -2, Value: "x"}}, ErrNonPositiveSeq, 0},
		{"序号与当前生效重复", []Write{{Key: "a", Seq: 5, Value: "same"}}, ErrSeqDuplicate, 0},
		{"混合批次中后条非法", []Write{
			{Key: "c", Seq: 1, Value: "ok"},
			{Key: "b", Seq: 0, Value: "bad"},
		}, ErrNonPositiveSeq, 1},
		{"批内建立后序号重复", []Write{
			{Key: "d", Seq: 9, Value: "first"},
			{Key: "d", Seq: 9, Value: "dup"},
		}, ErrSeqDuplicate, 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := New()
			if _, err := r.Apply([]Write{{Key: "a", Seq: 5, Value: "seed"}}); err != nil {
				t.Fatalf("seed: %v", err)
			}

			before := r.Snapshot()
			beforeDiscarded := r.Discarded()
			_, err := r.Apply(tc.writes)
			if err == nil {
				t.Fatalf("期望拒绝，实际成功")
			}
			var rej *RejectError
			if !errors.As(err, &rej) {
				t.Fatalf("期望 *RejectError，实际 %T: %v", err, err)
			}
			if !errors.Is(err, tc.reason) {
				t.Fatalf("原因期望 %v，实际 %v", tc.reason, rej.Reason)
			}
			if rej.Index != tc.index {
				t.Fatalf("拒绝下标期望 %d，实际 %d", tc.index, rej.Index)
			}
			t.Logf("拒绝: index=%d write={key=%q seq=%d value=%q} reason=%v",
				rej.Index, rej.Write.Key, rej.Write.Seq, rej.Write.Value, rej.Reason)

			// 整批不生效：状态逐字段不变。
			after := r.Snapshot()
			if len(after) != len(before) {
				t.Fatalf("拒绝后状态改变: before=%+v after=%+v", before, after)
			}
			for i := range before {
				if before[i] != after[i] {
					t.Fatalf("拒绝后状态改变: before=%+v after=%+v", before, after)
				}
			}
			if r.Discarded() != beforeDiscarded {
				t.Fatalf("拒绝后丢弃数改变: %d -> %d", beforeDiscarded, r.Discarded())
			}
			if err := r.Check(); err != nil {
				t.Fatalf("自检失败: %v", err)
			}
		})
	}
}

func TestSeqMonotonicNonIncrease(t *testing.T) {
	r := New()
	if _, err := r.Apply([]Write{{Key: "k", Seq: 100, Value: "v100"}}); err != nil {
		t.Fatal(err)
	}
	prev, _ := r.Get("k")
	for _, seq := range []int64{50, 50, 49, 100, 1, 1, 2} {
		res, err := r.Apply([]Write{{Key: "k", Seq: seq, Value: fmt.Sprintf("v%d", seq)}})
		if seq == prev.Seq {
			// 与当前生效序号重复必须被拒绝，且状态不变。
			if !errors.Is(err, ErrSeqDuplicate) {
				t.Fatalf("seq=%d 期望 ErrSeqDuplicate，实际 %v", seq, err)
			}
			t.Logf("seq=%d 拒绝=seq-duplicate 生效序号仍=%d", seq, prev.Seq)
			continue
		}
		if err != nil {
			t.Fatalf("seq=%d: %v", seq, err)
		}
		got, _ := r.Get("k")
		if got.Seq > prev.Seq {
			t.Fatalf("生效序号只减不增被破坏: %d -> %d", prev.Seq, got.Seq)
		}
		t.Logf("seq=%d 判定=%s 生效序号=%d", seq, res.Decisions[0], got.Seq)
		prev = got
	}
	if prev.Seq != 1 || prev.Value != "v1" {
		t.Fatalf("最终期望 seq=1/v1，实际 %+v", prev)
	}
}

func TestEmptyBatch(t *testing.T) {
	r := New()
	if _, err := r.Apply(nil); !errors.Is(err, ErrEmptyBatch) {
		t.Fatalf("空批次期望 ErrEmptyBatch，实际 %v", err)
	}
}

func TestConcurrentReadsAndWrites(t *testing.T) {
	r := New()

	// 预填多键，提供撤回素材。
	seed := make([]Write, 0, 16)
	for k := 0; k < 16; k++ {
		seed = append(seed, Write{Key: fmt.Sprintf("k%02d", k), Seq: 100, Value: "seed"})
	}
	if _, err := r.Apply(seed); err != nil {
		t.Fatal(err)
	}

	var writers sync.WaitGroup

	// 多个写协程：更小序号撤回建立、更大序号后写丢弃交替。
	for g := 0; g < 4; g++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for i := 0; i < 200; i++ {
				key := fmt.Sprintf("k%02d", i%16)
				seq := int64(100 - i/2) // 奇数轮更小序号，制造撤回
				if i%2 == 0 {
					seq = int64(100 + i) // 偶数轮更大序号，制造丢弃
				}
				_, _ = r.Apply([]Write{{Key: key, Seq: seq, Value: "v"}})
			}
		}()
	}

	// 只发非法批次的协程，验证并发拒绝不破坏状态。
	for g := 0; g < 2; g++ {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for i := 0; i < 100; i++ {
				_, _ = r.Apply([]Write{{Key: "", Seq: 1, Value: "bad"}})
			}
		}()
	}

	// 多个读协程与写入并发：单次快照必须内部一致，自检必须始终通过。
	var readers sync.WaitGroup
	stop := make(chan struct{})
	for g := 0; g < 4; g++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				snap := r.Snapshot()
				seen := make(map[string]EntryView, len(snap))
				for _, e := range snap {
					if e.Key == "" || e.Seq <= 0 || e.Seq > 100 {
						t.Errorf("快照条目非法: %+v", e)
						return
					}
					seen[e.Key] = e
				}
				// Get 与同一次逻辑视图的结果必须对得上（允许其后来自其他写入）。
				if e, ok := r.Get("k00"); ok && (e.Key != "k00" || e.Seq <= 0) {
					t.Errorf("Get 返回非法条目: %+v ok=%v", e, ok)
					return
				}
				if r.Discarded() < 0 {
					t.Errorf("丢弃数为负")
					return
				}
				if err := r.Check(); err != nil {
					t.Errorf("自检失败: %v", err)
					return
				}
			}
		}()
	}

	writers.Wait()
	close(stop)
	readers.Wait()

	final := r.Snapshot()
	t.Logf("并发结束: 键数=%d 累计丢弃=%d", len(final), r.Discarded())
	for _, e := range final {
		// 任一键生效序号随写入只减不增：不得超过初始 100。
		if e.Seq > 100 || e.Seq <= 0 {
			t.Fatalf("生效序号超出初始上界或非正: %+v", e)
		}
	}
	if err := r.Check(); err != nil {
		t.Fatalf("最终自检失败: %v", err)
	}

	// 写入全部停止后，并发读取同一实例的视图必须逐字段相同。
	const readerCount = 8
	views := make([][]EntryView, readerCount)
	discards := make([]int64, readerCount)
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < readerCount; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			views[idx] = r.Snapshot()
			discards[idx] = r.Discarded()
			if err := r.Check(); err != nil {
				t.Errorf("静止态自检失败: %v", err)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	for i := 1; i < readerCount; i++ {
		if len(views[i]) != len(views[0]) {
			t.Fatalf("静止态视图长度不一致: %d vs %d", len(views[0]), len(views[i]))
		}
		for j := range views[0] {
			if views[0][j] != views[i][j] {
				t.Fatalf("静止态并发视图逐字段不同: %+v vs %+v", views[0][j], views[i][j])
			}
		}
		if discards[i] != discards[0] {
			t.Fatalf("静止态丢弃数不一致: %d vs %d", discards[0], discards[i])
		}
	}
}
