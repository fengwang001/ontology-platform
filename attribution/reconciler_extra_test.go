package attribution

import (
	"errors"
	"sync"
	"testing"
)

func TestDecisionOrder(t *testing.T) {
	// 过期先于重复：已归因锚点上点击且 g>A。
	r, _ := New(Config{V: 0, C: 0, A: 10, Tmin: 5, Lk: 0, Tu: 1000})
	b0 := []Event{
		NewImpression("u", "x", 100, 1),
		NewClick("u", "x", 100), // 归因，u=100
	}
	res0, _ := r.Submit(b0)
	logResults(t, b0, res0, nil)
	b1 := []Event{
		NewImpression("u", "a", 0, 1),
		NewClick("u", "a", 5),  // 归因（u 保持 100），锚点已归因
		NewClick("u", "a", 11), // g=11>A 过期（同时满足重复、串扰 t-u=-89，过期优先）
	}
	res1, err := r.Submit(b1)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, b1, res1, nil)
	wantOutcomes(t, res1, []Outcome{Counted, Attributed, Expired})

	// 过快先于重复先于串扰：已归因锚点不能再产生过快（锚点不移动），
	// 故用「未归因锚点」验证过快先于串扰：Tmin 很大、Tu 很小。
	r, _ = New(Config{V: 0, C: 0, A: 1_000_000_000, Tmin: 10, Lk: 0, Tu: 1000})
	b2 := []Event{
		NewImpression("u", "x", 100, 1),
		NewClick("u", "x", 110), // g=10 归因，u=110
		NewImpression("u", "a", 105, 1),
		NewClick("u", "a", 106), // g=1<Tmin 过快；虽 106<u 满足串扰，过快优先
	}
	res2, err := r.Submit(b2)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, b2, res2, nil)
	wantOutcomes(t, res2, []Outcome{Counted, Attributed, Counted, TooFast})

	// 重复先于串扰：t=1110 归因锚点 105（t-u=1000 恰等于 Tu）；
	// t=1111 已满足串扰（1111-1110<1000），但重复判定在前。
	b3 := []Event{
		NewClick("u", "a", 1110),
		NewClick("u", "a", 1111),
	}
	res3, err := r.Submit(b3)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, b3, res3, nil)
	wantOutcomes(t, res3, []Outcome{Attributed, Duplicate})
}

func TestInvalidArguments(t *testing.T) {
	bad := []Config{
		{V: -1}, {V: 1_000_000_001},
		{C: -1}, {C: 1_000_000_001},
		{A: -1}, {Tmin: -1}, {Lk: -1}, {Tu: -1},
	}
	for i, cfg := range bad {
		if _, err := New(cfg); !errors.Is(err, ErrInvalid) {
			t.Fatalf("第 %d 个非法参数应返回 ErrInvalid，got %v", i, err)
		}
	}
	r, _ := New(Config{})
	cases := [][]Event{
		{{Kind: Impression, User: "", Ad: "a", T: 0, V: 0}},
		{{Kind: Click, User: "u", Ad: "", T: 0}},
		{{Kind: Impression, User: "u", Ad: "a", T: -1, V: 0}},
		{{Kind: Impression, User: "u", Ad: "a", T: 1_000_000_000_000_001, V: 0}},
		{{Kind: Impression, User: "u", Ad: "a", T: 0, V: -1}},
		{{Kind: Impression, User: "u", Ad: "a", T: 0, V: 1_000_000_001}},
		{{Kind: Click, User: "u", Ad: "a", T: -1}},
		{{Kind: EventKind(9), User: "u", Ad: "a", T: 0}},
	}
	for i, batch := range cases {
		if _, err := r.Submit(batch); !errors.Is(err, ErrInvalid) {
			t.Fatalf("第 %d 个非法批次应返回 ErrInvalid，got %v", i, err)
		}
	}
	if s := r.Stats(); s != (Stats{}) {
		t.Fatalf("非法批次不得改变计数，got %+v", s)
	}
}

func TestOutOfOrderRejectedAtomically(t *testing.T) {
	r, _ := New(Config{V: 0, C: 0, A: 1_000_000_000, Tmin: 0, Lk: 0, Tu: 0})
	b1 := []Event{
		NewImpression("u", "a", 10, 1),
		NewClick("u", "a", 10),
	}
	res1, err := r.Submit(b1)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, b1, res1, nil)
	// 批内输入乱序但排序后合法。
	b2 := []Event{
		NewClick("u", "a", 21),
		NewImpression("u", "a", 20, 1), // 排序后先处理
	}
	res2, err := r.Submit(b2)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, b2, res2, nil)
	if res2[0].Outcome != Attributed || res2[1].Outcome != Counted {
		t.Fatalf("排序后同刻/先后关系错误：got %s,%s", res2[0].Outcome, res2[1].Outcome)
	}
	before := r.Stats()
	// 跨批 t 小于水位 21：整批拒绝，且含一个本应合法的事件也不生效。
	b3 := []Event{
		NewImpression("u", "a", 25, 1), // 合法但随整批回滚
		NewClick("u", "a", 5),          // 乱序
	}
	_, err = r.Submit(b3)
	if !errors.Is(err, ErrOutOfOrder) {
		t.Fatalf("应返回 ErrOutOfOrder，got %v", err)
	}
	logResults(t, b3, nil, err)
	if after := r.Stats(); after != before {
		t.Fatalf("被拒绝批次不得改变计数：before=%+v after=%+v", before, after)
	}
	// t 等于水位被接受。
	b4 := []Event{NewClick("u", "a", 21)}
	res4, err := r.Submit(b4)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, b4, res4, nil)
	if res4[0].Outcome != Duplicate {
		t.Fatalf("t 等于水位应被接受，锚点已归因为重复，got %s", res4[0].Outcome)
	}
}

func TestPairsIndependent(t *testing.T) {
	r, _ := New(Config{V: 1000, C: 60, A: 100, Tmin: 0, Lk: 50, Tu: 0})
	events := []Event{
		NewImpression("u1", "a", 0, 1000),
		NewImpression("u2", "a", 10, 1000), // 不同 user：独立
		NewImpression("u1", "b", 20, 1000), // 不同 ad：独立
		NewClick("u1", "a", 10),
		NewClick("u2", "a", 10),
		NewClick("u1", "b", 20),
	}
	results, err := r.Submit(events)
	if err != nil {
		t.Fatal(err)
	}
	logResults(t, events, results, nil)
	wantOutcomes(t, results, []Outcome{
		Counted, Counted, Counted, Attributed, Attributed, Attributed,
	})
}

func TestStatsInvariants(t *testing.T) {
	r, _ := New(Config{V: 5, C: 3, A: 20, Tmin: 1, Lk: 4, Tu: 7})
	batches := [][]Event{
		{
			NewImpression("u", "a", 0, 10),
			NewImpression("u", "a", 1, 10),
			NewClick("u", "a", 2),
			NewClick("u", "a", 2),
			NewImpression("u", "a", 3, 1),
			NewClick("u", "a", 100),
		},
		{
			NewClick("u", "b", 50),
			NewImpression("u", "b", 50, 10),
			NewClick("u", "b", 50),
			NewClick("u", "b", 51),
			NewClick("u", "b", 52),
		},
	}
	var impressions, clicks int64
	for _, b := range batches {
		res, err := r.Submit(b)
		if err != nil {
			t.Fatal(err)
		}
		logResults(t, b, res, nil)
		for _, e := range b {
			if e.Kind == Impression {
				impressions++
			} else {
				clicks++
			}
		}
		s := r.Stats()
		if s.Counted+s.Cooldown+s.NotVisible != impressions {
			t.Fatalf("曝光恒等式不成立：%+v impressions=%d", s, impressions)
		}
		if s.Attributed+s.Duplicate+s.Expired+s.TooFast+s.NoImpression+s.CrossTalk != clicks {
			t.Fatalf("点击恒等式不成立：%+v clicks=%d", s, clicks)
		}
		if s.Attributed > s.Counted {
			t.Fatalf("归因数 %d 超过已计数曝光数 %d", s.Attributed, s.Counted)
		}
	}
}

func TestConcurrentSubmitAndStats(t *testing.T) {
	r, _ := New(Config{V: 0, C: 2, A: 1_000_000_000, Tmin: 0, Lk: 2, Tu: 3})
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(off int) {
			defer wg.Done()
			user := "u" + string(rune('A'+off))
			for i := 0; i < 200; i++ {
				ts := int64(i * 10)
				batch := []Event{
					NewImpression(user, "a", ts, 1),
					NewClick(user, "a", ts+1),
				}
				res, err := r.Submit(batch)
				if err != nil {
					t.Errorf("并发 Submit 失败：%v", err)
					return
				}
				if len(res) != 2 {
					t.Errorf("原子批结果长度错误：%d", len(res))
					return
				}
				_ = r.Stats()
			}
		}(w)
	}
	wg.Wait()
	s := r.Stats()
	if s.Counted != 8*200 {
		t.Fatalf("已计数曝光数错误：got %d want %d", s.Counted, 8*200)
	}
	if s.Attributed > s.Counted {
		t.Fatalf("归因数超过已计数曝光数")
	}
}
