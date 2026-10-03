package keytable

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"

	"ontology/report"
)

// TestSpecExample 复刻规格：N=2,M=3,W=1000，同键 8 条 + 1500 滚动。
func TestSpecExample(t *testing.T) {
	s, err := New(2, 3, 1000, 10, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []bool{true, true, false, false, true, false, false, true}
	for i, w := range want {
		d := s.Record(100, "t1", "k", 1)
		t.Logf("输入 now=100 第%d条 sev=1 | 输出 kept=%v summaries=%v 依据 cnt 规则",
			i+1, d.Kept, d.Summaries)
		if d.Err != nil || d.Kept != w || len(d.Summaries) != 0 {
			t.Fatalf("第%d条: %+v", i+1, d)
		}
	}
	if d, ok := s.Pending("t1", "k"); !ok || d != 4 {
		t.Fatalf("Pending=%d,%v want 4,true", d, ok)
	}
	d := s.Record(1500, "t1", "k", 1)
	t.Logf("输入 now=1500（窗口 0→1）| 输出 kept=%v summaries=%v 依据 先 Rolled 再 cnt=1",
		d.Kept, d.Summaries)
	wantSum := report.Summary{Tenant: "t1", Key: "k", Window: 0, Dropped: 4, Reason: report.Rolled}
	if !d.Kept || len(d.Summaries) != 1 || d.Summaries[0] != wantSum {
		t.Fatalf("滚动结果异常: %+v", d)
	}
}

// TestSev4KeepsQuota sev=4 放行但不占 cnt；夹在 cnt=2 后，下一条 sev=1 的 cnt=3 被丢。
func TestSev4KeepsQuota(t *testing.T) {
	s, _ := New(2, 3, 1000, 10, 10)
	s.Record(0, "t", "k", 1) // cnt=1 放行
	s.Record(0, "t", "k", 1) // cnt=2 放行
	d4 := s.Record(0, "t", "k", 4)
	t.Logf("输入 sev=4 | 输出 kept=%v 依据 sev>=4 一律放行且不动 cnt", d4.Kept)
	if !d4.Kept || len(d4.Summaries) != 0 {
		t.Fatalf("sev=4 异常: %+v", d4)
	}
	d := s.Record(0, "t", "k", 1)
	t.Logf("输入 sev=1（sev=4 之后）| 输出 kept=%v pending 查询中验证 cnt=3 丢弃", d.Kept)
	if d.Kept {
		t.Fatal("sev=4 不应占用额度，下一条 sev=1 cnt=3 应丢弃")
	}
	if p, _ := s.Pending("t", "k"); p != 1 {
		t.Fatalf("pending=%d want 1", p)
	}
	// sev=5 同样放行，且使键成为最新访问。
	if d := s.Record(0, "t", "k", 5); !d.Kept {
		t.Fatal("sev=5 应放行")
	}
}

// TestEvictionByAccessOrder Kt=2：a 连来 5 条（丢 2）后 b、c，c 淘汰最久未用的 a。
func TestEvictionByAccessOrder(t *testing.T) {
	s, _ := New(2, 3, 1000, 2, 10)
	for i := 1; i <= 5; i++ {
		s.Record(0, "t", "a", 1)
	}
	s.Record(0, "t", "b", 1)
	d := s.Record(0, "t", "c", 1)
	wantSum := report.Summary{Tenant: "t", Key: "a", Window: 0, Dropped: 2, Reason: report.Evicted}
	t.Logf("输入 c（a 最久未用）| 输出 summaries=%v 依据 淘汰访问序最小的 a", d.Summaries)
	if len(d.Summaries) != 1 || d.Summaries[0] != wantSum {
		t.Fatalf("淘汰摘要异常: %+v", d.Summaries)
	}
	if _, ok := s.Pending("t", "a"); ok {
		t.Fatal("a 被淘汰后 Pending 应不存在")
	}
	// a 重新出现按全新键处理，cnt 从 1 计起直接放行，且不重复给摘要。
	d = s.Record(0, "t", "a", 1)
	t.Logf("输入 a 重新出现 | 输出 kept=%v summaries=%v 依据 额度重置 cnt=1",
		d.Kept, d.Summaries)
	if !d.Kept || len(d.Summaries) != 0 {
		t.Fatalf("重建 a 异常: %+v", d)
	}
}

// TestEvictionNotByKept 淘汰看访问序而非放行/丢弃：a 全放行但最久未用，仍被淘汰。
func TestEvictionNotByKept(t *testing.T) {
	s, _ := New(100, 100, 1000, 2, 10)
	for i := 0; i < 5; i++ {
		s.Record(0, "t", "a", 1) // 全部放行，a 最旧
	}
	s.Record(0, "t", "b", 4) // b 最新，sev=4 也算访问
	d := s.Record(0, "t", "c", 1)
	t.Logf("输入 c（a 无丢弃但最旧）| 输出 summaries=%d 条 依据 淘汰只看访问序", len(d.Summaries))
	if len(d.Summaries) != 0 {
		t.Fatalf("被淘汰的 a 无丢弃，不应有摘要: %+v", d.Summaries)
	}
	if _, ok := s.Pending("t", "a"); ok {
		t.Fatal("a 应已被淘汰")
	}
	// b 因 sev=4 被访问而保留。
	if _, ok := s.Pending("t", "b"); !ok {
		t.Fatal("b 应保留")
	}
}

// TestTenantIsolationAndLimit 一个租户满员淘汰不波及别的租户；新租户超 Tmax 报错。
func TestTenantIsolationAndLimit(t *testing.T) {
	s, _ := New(100, 100, 1000, 1, 2)
	s.Record(0, "t1", "a", 1)
	s.Record(0, "t2", "x", 1)
	// t1 满员（Kt=1）淘汰 a，不影响 t2 的 x。
	d := s.Record(0, "t1", "b", 1)
	t.Logf("输入 t1/b（t1 Kt=1）| 输出 summaries=%v；t2/x 须保留", d.Summaries)
	if _, ok := s.Pending("t2", "x"); !ok {
		t.Fatal("淘汰不得波及别的租户")
	}
	if _, ok := s.Pending("t1", "a"); ok {
		t.Fatal("t1 的 a 应被淘汰")
	}
	// 第三个租户触发租户上限。
	d = s.Record(0, "t3", "a", 1)
	if !errors.Is(d.Err, report.ErrTenantLimit) {
		t.Fatalf("t3 应触发租户上限, got %+v", d)
	}
	if _, ok := s.Pending("t3", "a"); ok {
		t.Fatal("被拒操作不得登记租户")
	}
}

// TestReplayDeterminism 相同输入序列重放得到相同放行与摘要。
func TestReplayDeterminism(t *testing.T) {
	type in struct {
		now         int64
		tenant, key string
		sev         int
	}
	rng := rand.New(rand.NewSource(7))
	var seq []in
	var now int64
	for i := 0; i < 800; i++ {
		now += int64(rng.Intn(200))
		seq = append(seq, in{now, "t" + itoa(int64(rng.Intn(2))),
			"k" + itoa(int64(rng.Intn(5))), rng.Intn(6)})
	}
	run := func() []naiveRec {
		s, _ := New(2, 4, 250, 3, 2)
		out := make([]naiveRec, 0, len(seq))
		for _, r := range seq {
			d := s.Record(r.now, r.tenant, r.key, r.sev)
			out = append(out, naiveRec{
				kept: d.Kept, summaries: d.Summaries,
				err: d.Err,
			})
		}
		var flushed []report.Summary
		_, _ = s.Flush(now+1000, func(sm report.Summary) error {
			flushed = append(flushed, sm)
			return nil
		})
		out = append(out, naiveRec{summaries: flushed})
		return out
	}
	r1, r2 := run(), run()
	t.Logf("重放 %d 条记录 + Flush，逐条比对", len(seq))
	if !reflect.DeepEqual(r1, r2) {
		t.Fatal("两次重放结果不一致")
	}
}
