package service

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"

	"ontology/core"
	"ontology/naive"
)

var cust = core.Key{Type: "Customer", ID: "C-1"}

// 删除后复活：删除前、删除中、复活后三段历史必须可分别还原。
func TestDeleteResurrectThreePhases(t *testing.T) {
	e := NewEngine()
	if _, err := e.Put(cust, 5, "A", "v0"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Remove(cust, 8, "v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Put(cust, 12, "B", "v2"); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		sysQ uint64
		bizQ int64
		want core.Visibility
	}{
		{3, 6, core.Found},    // 删除前
		{3, 9, core.Deleted},  // 删除中
		{3, 13, core.Found},   // 复活后
		{1, 9, core.Found},    // as-of sys=1：删除尚未发生
		{2, 13, core.Deleted}, // as-of sys=2：复活尚未发生
		{3, 4, core.NotVisible},
	}
	for _, c := range cases {
		if got := e.Query(cust, c.sysQ, c.bizQ); got.Visibility != c.want {
			t.Fatalf("Query(sys=%d, biz=%d) = %s，期望 %s", c.sysQ, c.bizQ, got, c.want)
		}
	}

	tl := e.HistoryAsOf(cust, 3)
	if len(tl) != 3 {
		t.Fatalf("时间线应为 3 段，得到 %+v", tl)
	}
	if tl[0].Start != 5 || tl[0].End != 8 || tl[0].Version.Payload != "A" {
		t.Fatalf("删除前段异常: %+v", tl[0])
	}
	if tl[1].Start != 8 || tl[1].End != 12 || !tl[1].Version.Deleted {
		t.Fatalf("删除中段异常: %+v", tl[1])
	}
	if tl[2].Start != 12 || !tl[2].Open || tl[2].Version.Payload != "B" {
		t.Fatalf("复活后段异常: %+v", tl[2])
	}
}

// 乐观并发凭证的交错竞争：同一前序版本上只能有一个写入成功。
func TestConcurrentCompetingCredentials(t *testing.T) {
	e := NewEngine()
	const workers = 32
	const rounds = 20

	for round := 0; round < rounds; round++ {
		cred := core.CredentialOf(e.LatestSeq(cust))
		var wg sync.WaitGroup
		var mu sync.Mutex
		var succeeded, conflicted int
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				_, err := e.Put(cust, int64(100+round), fmt.Sprintf("w%d", w), cred)
				mu.Lock()
				defer mu.Unlock()
				if err == nil {
					succeeded++
				} else if err.Code == core.ErrConcurrencyConflict {
					conflicted++
				} else {
					t.Errorf("意外错误: %v", err)
				}
			}(w)
		}
		wg.Wait()
		if succeeded != 1 || conflicted != workers-1 {
			t.Fatalf("round %d: %d 成功 %d 冲突，期望恰好 1 成功 %d 冲突",
				round, succeeded, conflicted, workers-1)
		}
	}
	if got := e.LatestSeq(cust); got != rounds {
		t.Fatalf("最终版本号 = %d，期望 %d（每轮恰好一个写入生效）", got, rounds)
	}
}

// 重放同一组带时间戳与凭证的操作序列，必须得到完全相同的版本链与查询结果。
func TestReplayDeterminism(t *testing.T) {
	ops := []core.WriteRequest{
		{Key: cust, BizStart: 100, Payload: "a", Credential: "v0"},
		{Key: cust, BizStart: 300, Payload: "b", Credential: "v1"},
		{Key: cust, BizStart: 200, Delete: true, Credential: "v2"},
		{Key: cust, BizStart: 300, Payload: "b2", Credential: "v3"}, // 等起点覆盖
		{Key: cust, BizStart: 50, Payload: "x", Credential: "v4"},   // 应被拒：早于边界
		{Key: cust, BizStart: 400, Payload: "c", Credential: "v4"},
	}
	run := func() ([]core.Version, []core.QueryResult) {
		e := NewEngine()
		for _, op := range ops {
			e.Write(op)
		}
		var results []core.QueryResult
		for sysQ := uint64(0); sysQ <= e.LatestSeq(cust); sysQ++ {
			for _, bizQ := range []int64{0, 150, 250, 350, 450} {
				results = append(results, e.Query(cust, sysQ, bizQ))
			}
		}
		return e.Chain(cust), results
	}
	chain1, res1 := run()
	chain2, res2 := run()
	if len(chain1) != 5 { // 6 次写入中 1 次（早于边界）被拒
		t.Fatalf("版本链长度 = %d，期望 5", len(chain1))
	}
	for i := range chain1 {
		a, b := chain1[i], chain2[i]
		if a.Seq != b.Seq || a.BizStart != b.BizStart || a.Payload != b.Payload || a.Deleted != b.Deleted {
			t.Fatalf("重放版本链不一致: %+v vs %+v", a, b)
		}
	}
	for i := range res1 {
		if res1[i].Visibility != res2[i].Visibility {
			t.Fatalf("重放查询结果不一致: %s vs %s", res1[i], res2[i])
		}
	}
}

// 差分测试：与独立维护全部版本的朴素线性扫描模型，
// 在大量随机写入/删除/双时态查询序列下逐条对照。
// 每次操作打印输入、实际输出与据以判定的版本及时间依据。
func TestDifferentialAgainstNaive(t *testing.T) {
	for _, seed := range []int64{1, 7, 42, 2026} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			differential(t, seed, 1500)
		})
	}
}

func differential(t *testing.T, seed int64, ops int) {
	e := NewEngine()
	m := naive.New()
	rng := rand.New(rand.NewSource(seed))

	keys := []core.Key{
		{Type: "Customer", ID: "C-0"},
		{Type: "Customer", ID: "C-1"},
		{Type: "Order", ID: "O-0"},
		{Type: "Order", ID: "O-1"},
		{Type: "Order", ID: "O-2"},
	}
	ghost := core.Key{Type: "Customer", ID: "ghost"} // 永不写入，覆盖 NEVER_EXISTED

	sameVersion := func(a, b *core.Version) bool {
		if a == nil || b == nil {
			return a == b
		}
		return a.Seq == b.Seq && a.BizStart == b.BizStart && a.Payload == b.Payload && a.Deleted == b.Deleted
	}

	for i := 0; i < ops; i++ {
		k := keys[rng.Intn(len(keys))]
		switch r := rng.Intn(100); {
		case r < 60: // 写入或删除
			latest := m.LatestSeq(k)
			cred := core.CredentialOf(latest)
			if rng.Intn(100) < 20 && latest > 0 { // 20% 故意用过期凭证制造竞争
				cred = core.CredentialOf(uint64(rng.Int63n(int64(latest))))
			}
			boundary := m.MinBizStart(k) // 已提交的最早可追溯边界
			biz := boundary + rng.Int63n(120)
			if latest > 0 && rng.Intn(100) < 15 {
				biz = rng.Int63n(boundary + 1)
			}
			req := core.WriteRequest{Key: k, BizStart: biz, Credential: cred}
			if rng.Intn(100) < 25 {
				req.Delete = true
			} else {
				req.Payload = fmt.Sprintf("p%d", i)
			}

			v1, err1 := e.Write(req)
			v2, err2 := m.Write(req)
			t.Logf("op#%d WRITE %s biz=%d cred=%s del=%v | engine=%s naive=%s | 依据: latest=%d boundary=%d",
				i, k, req.BizStart, req.Credential, req.Delete,
				writeOutcome(v1, err1), writeOutcome(v2, err2), latest, boundary)

			if (err1 == nil) != (err2 == nil) {
				t.Fatalf("op#%d 写入口径不一致: engine err=%v, naive err=%v", i, err1, err2)
			}
			if err1 != nil && err1.Code != err2.Code {
				t.Fatalf("op#%d 拒绝原因不一致: engine=%s naive=%s", i, err1.Code, err2.Code)
			}
			if err1 == nil && !sameVersion(&v1, &v2) {
				t.Fatalf("op#%d 提交版本不一致: engine=%v naive=%v", i, v1, v2)
			}
		default: // 双时态查询
			if rng.Intn(100) < 5 {
				k = ghost
			}
			sysQ := uint64(rng.Int63n(int64(m.LatestSeq(k)) + 3))
			bizQ := rng.Int63n(200)
			r1 := e.Query(k, sysQ, bizQ)
			r2 := m.Query(k, sysQ, bizQ)
			t.Logf("op#%d QUERY %s sys<=%d biz=%d | engine=%s naive=%s | 依据: %s",
				i, k, sysQ, bizQ, r1, r2, basisOf(r2))

			if r1.Visibility != r2.Visibility {
				t.Fatalf("op#%d 查询标记不一致: engine=%s naive=%s", i, r1.Visibility, r2.Visibility)
			}
			if !sameVersion(r1.Version, r2.Version) {
				t.Fatalf("op#%d 命中版本不一致: engine=%v naive=%v", i, r1.Version, r2.Version)
			}
		}
	}
}

func writeOutcome(v core.Version, err *core.Error) string {
	if err != nil {
		return "REJECT(" + string(err.Code) + ")"
	}
	return "COMMIT(" + v.String() + ")"
}

func basisOf(r core.QueryResult) string {
	if r.Version == nil {
		return "无满足 (sys,biz) 坐标的版本"
	}
	v := r.Version
	return fmt.Sprintf("命中版本 seq=%d bizStart=%d deleted=%v", v.Seq, v.BizStart, v.Deleted)
}
