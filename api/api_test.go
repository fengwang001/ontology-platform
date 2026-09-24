package api_test

import (
	"maps"
	"math/rand/v2"
	"testing"

	"ontology/api"
)

func fill(e *api.Engine, kvs ...any) {
	for i := 0; i < len(kvs); i += 2 {
		e.Write(kvs[i].(string), kvs[i+1])
	}
}

// 第三节八步脚本（含甲乙丙结论）。
func TestEightStepScript(t *testing.T) {
	chk := func(e *api.Engine, s int64, want map[string]api.Val, werr error) {
		if got, err := e.AsOf(s); err != werr || (werr == nil && !maps.Equal(got, want)) {
			t.Errorf("AsOf(%d)=(%v,%v), want (%v,%v)", s, got, err, want, werr)
		}
	}
	e := api.New()
	fill(e, "a", 1, "b", 10, "a", 2)
	for s, w := range []map[string]api.Val{{}, {"a": 1}, {"a": 1, "b": 10}, {"a": 2, "b": 10}, {"a": 2, "b": 10}} {
		chk(e, int64(s), w, nil) // 甲：s=2 时 b 等号可见；s=4 收敛到最新
	}
	e.Compact(2)
	chk(e, 2, nil, api.ErrCompacted)                    // 不可达即报错
	chk(e, 3, map[string]api.Val{"a": 2, "b": 10}, nil) // 乙：基线保住 b=10
	chk(e, 4, map[string]api.Val{"a": 2, "b": 10}, nil)
	e2 := api.New()
	fill(e2, "a", 1, "b", 10, "a", 2)
	e2.Compact(3) // 丙：a 的基线降为 Seq=3 的值 2
	chk(e2, 3, nil, api.ErrCompacted)
	chk(e2, 4, map[string]api.Val{"a": 2, "b": 10}, nil)
}

// 不变量 1：随机操作序列下，AsOf 与朴素重放逐 key 逐值相同。
func TestAsOfMatchesNaiveReplay(t *testing.T) {
	for seed := uint64(1); seed <= 3; seed++ {
		rng := rand.New(rand.NewPCG(seed, 9))
		e := api.New()
		keys, vals := []string{}, []int{}
		upto := int64(-1)
		for i := 0; i < 300; i++ {
			if u := upto + 1 + int64(rng.IntN(3)); rng.IntN(4) == 0 && e.Compact(u) == nil && u > upto {
				upto = u
				continue
			}
			k, v := string(rune('a'+rng.IntN(6))), rng.IntN(100)
			e.Write(k, v)
			keys, vals = append(keys, k), append(vals, v)
		}
		for s := upto + 1; s <= e.MaxSeq()+2; s++ {
			want := map[string]api.Val{}
			for i, k := range keys {
				if int64(i+1) <= s { // 写入按 Seq 升序重放
					want[k] = vals[i]
				}
			}
			if got, err := e.AsOf(s); err != nil || !maps.Equal(got, want) {
				t.Fatalf("seed=%d AsOf(%d)=(%v,%v), replay=%v", seed, s, got, err, want)
			}
		}
	}
}

// 不变量 2：Compact 后所有 s>upto 的读与 compact 前完全相同。
func TestCompactPreservesReachableReads(t *testing.T) {
	for _, upto := range []int64{0, 1, 3, 5} {
		e := api.New()
		fill(e, "a", 1, "b", 10, "a", 2, "c", 7, "b", 11)
		pre := map[int64]map[string]api.Val{}
		for s := upto + 1; s <= 7; s++ {
			pre[s], _ = e.AsOf(s)
		}
		e.Compact(upto)
		for s, want := range pre {
			if got, err := e.AsOf(s); err != nil || !maps.Equal(got, want) {
				t.Errorf("upto=%d AsOf(%d) changed by compact", upto, s)
			}
		}
	}
}

// 不变量 3：s<=upto 确定性报 ErrCompacted，不返回旧数据或空视图。
func TestCompactedReadErrors(t *testing.T) {
	e := api.New()
	fill(e, "a", 1, "b", 2, "a", 3)
	e.Compact(2)
	for _, s := range []int64{0, 1, 2} {
		if got, err := e.AsOf(s); err != api.ErrCompacted || got != nil {
			t.Errorf("AsOf(%d)=(%v,%v), want (nil,ErrCompacted)", s, got, err)
		}
	}
}

// 不变量 4：被拒操作不改变任何状态（含不分配 Seq），之后仍可正常使用；四类哨兵互不相同。
func TestRejectedOpsLeaveNoTrace(t *testing.T) {
	e := api.New()
	fill(e, "x", 1)
	e.Compact(1)
	ops := []func() error{
		func() error { _, err := e.Write("", 9); return err },
		func() error { _, err := e.AsOf(-1); return err },
		func() error { return e.Compact(-1) },
		func() error { _, err := e.AsOf(1); return err }, // 不可达
	}
	wants := []error{api.ErrEmptyKey, api.ErrNegativeRead, api.ErrNegativeCompact, api.ErrCompacted}
	for i, op := range ops {
		if err := op(); err != wants[i] {
			t.Errorf("op %d: got %v, want %v", i, err, wants[i])
		}
	}
	if after, _ := e.AsOf(2); e.MaxSeq() != 1 || !maps.Equal(after, map[string]api.Val{"x": 1}) {
		t.Error("rejected ops changed state")
	}
	if s, err := e.Write("y", 2); err != nil || s != 2 {
		t.Error("engine unusable after rejects")
	}
	if err := api.New().SelfCheck(); err != nil {
		t.Fatal(err)
	}
	if wants[0] == wants[1] || wants[0] == wants[2] || wants[0] == wants[3] || wants[1] == wants[2] || wants[1] == wants[3] || wants[2] == wants[3] {
		t.Error("sentinels not distinct")
	}
}

// 并发：compact 掉前一半位点后，N 个 goroutine 读同一可达位点，视图逐 key 逐值相同。
func TestConcurrentAsOfConsistent(t *testing.T) {
	e := api.New()
	for i := 0; i < 50; i++ {
		e.Write(string(rune('a'+i%5)), i)
	}
	e.Compact(25)
	want, _ := e.AsOf(40)
	start, views := make(chan struct{}), make(chan map[string]api.Val, 16)
	for range 16 {
		go func() { <-start; got, _ := e.AsOf(40); views <- got }()
	}
	go func() { <-start; e.Write("z", 99); e.MaxSeq(); e.SelfCheck() }()
	close(start)
	for range 16 {
		if got := <-views; !maps.Equal(got, want) {
			t.Errorf("concurrent view differs: %v vs %v", got, want)
		}
	}
}
