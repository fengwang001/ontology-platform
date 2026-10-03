package keytable

import (
	"errors"
	"math/rand"
	"testing"

	"ontology/report"
)

var errSink = errors.New("sink fail")

// TestWindowBoundaryExactW 窗口边界恰等 W：now=W 属于窗口 1，now=W-1 仍属窗口 0。
func TestWindowBoundaryExactW(t *testing.T) {
	s, _ := New(1, 10, 1000, 10, 10)
	s.Record(999, "t", "k", 1) // cnt=1 放行
	s.Record(999, "t", "k", 1) // cnt=2 丢弃，窗口 0（⌊999/1000⌋=0）
	// now=W 恰为边界，不滚动。
	if d := s.Record(999, "t", "k", 4); len(d.Summaries) != 0 {
		t.Fatalf("同窗口 now=999 不应滚动: %+v", d.Summaries)
	}
	// now=W 恰为边界：⌊W/W⌋=1，进入窗口 1，交付窗口 0 的 1 条丢弃。
	d := s.Record(1000, "t", "k", 1)
	t.Logf("输入 now=1000=W（⌊now/W⌋=1）| 输出 summaries=%v 依据 win0≠win1 先 Rolled",
		d.Summaries)
	want := report.Summary{Tenant: "t", Key: "k", Window: 0, Dropped: 1, Reason: report.Rolled}
	if len(d.Summaries) != 1 || d.Summaries[0] != want {
		t.Fatalf("窗口边界摘要异常: %+v", d.Summaries)
	}
}

// TestRolledEvictedMutuallyExclusive 同一次 Record 不会既 Rolled 又 Evicted。
func TestRolledEvictedMutuallyExclusive(t *testing.T) {
	s, _ := New(1, 10, 1000, 2, 10)
	s.Record(0, "t", "a", 1)
	s.Record(0, "t", "a", 1) // a 在窗口 0 丢 1 条
	s.Record(0, "t", "b", 1)
	// 先在新窗口访问 a（Rolled），再让 a 变旧；c 到来时淘汰 a（Evicted）。
	s.Record(2000, "t", "a", 1) // a：Rolled{win0,1}，随后为最新
	s.Record(2000, "t", "b", 1) // b：新窗口重置，b 最新，a 最旧
	d := s.Record(2000, "t", "c", 1)
	t.Logf("输入 c 淘汰 a（a 已在窗口1滚动过）| 输出 summaries=%v 依据 a 窗口1无丢弃",
		d.Summaries)
	if len(d.Summaries) != 0 {
		t.Fatalf("同一次 Record 不得同时 Rolled+Evicted: %+v", d.Summaries)
	}
}

// TestFlushBasic Flush(2500,W=1000) 交付所有 win<2 且 dropped>0 的条目，Closed 且按字节序。
func TestFlushBasic(t *testing.T) {
	s, _ := New(0, 10, 1000, 10, 10)
	s.Record(0, "t2", "k2", 1) // 窗口0丢弃（N=0,M=10 第1条丢）
	s.Record(0, "t1", "k1", 1)
	s.Record(0, "t1", "k1", 1)
	s.Record(2000, "t1", "cur", 4) // 窗口2存在；sev=4 不产生 dropped，不交付
	var got []report.Summary
	n, err := s.Flush(2500, func(sm report.Summary) error {
		got = append(got, sm)
		return nil
	})
	t.Logf("输入 Flush now=2500 cur=2 | 输出 n=%d err=%v summaries=%v（字节序）", n, err, got)
	if err != nil || n != 2 {
		t.Fatalf("Flush n=%d err=%v want 2,nil", n, err)
	}
	wantOrder := []string{"t1/k1", "t2/k2"}
	for i, sm := range got {
		if sm.Reason != report.Closed {
			t.Fatalf("#%d Reason=%d want Closed", i, sm.Reason)
		}
		if sm.Tenant+"/"+sm.Key != wantOrder[i] {
			t.Fatalf("顺序#%d=%s want %s", i, sm.Tenant+"/"+sm.Key, wantOrder[i])
		}
	}
	if got[0].Dropped != 2 || got[0].Window != 0 || got[1].Dropped != 1 {
		t.Fatalf("摘要计数/窗口异常: %+v", got)
	}
	// 交付成功后 pending 清零，条目保留。
	if p, ok := s.Pending("t1", "k1"); !ok || p != 0 {
		t.Fatalf("交付后 pending=%d,%v", p, ok)
	}
	// 再 Flush 无新内容。
	if n, _ := s.Flush(3000, func(report.Summary) error { return nil }); n != 0 {
		t.Fatalf("重复 Flush 不应再交付, n=%d", n)
	}
}

// TestFlushSinkFailRetain sink 在第 j 项失败：失败项及其后保留，已交付项清零，可重试。
func TestFlushSinkFailRetain(t *testing.T) {
	s, _ := New(0, 10, 1000, 10, 10)
	// 三个可交付条目，字节序 a,b,c，各丢 1。
	s.Record(0, "t", "a", 1)
	s.Record(0, "t", "b", 1)
	s.Record(0, "t", "c", 1)
	var seen []string
	n, err := s.Flush(2000, func(sm report.Summary) error {
		seen = append(seen, sm.Key)
		if sm.Key == "b" {
			return errSink
		}
		return nil
	})
	t.Logf("输入 sink 在 b 失败 | 输出 n=%d err=%v seen=%v 依据 失败即停且保留 b,c",
		n, err, seen)
	if n != 1 || !errors.Is(err, errSink) {
		t.Fatalf("失败时 n=%d err=%v want 1,errSink", n, err)
	}
	if p, _ := s.Pending("t", "a"); p != 0 {
		t.Fatal("a 已成功交付应清零")
	}
	if p, _ := s.Pending("t", "b"); p != 1 {
		t.Fatalf("失败项 b 应原样保留 pending=%d", p)
	}
	if p, _ := s.Pending("t", "c"); p != 1 {
		t.Fatalf("失败项之后的 c 应原样保留 pending=%d", p)
	}
	// 重试：b 成功后继续 c，共再交付 2 条。
	var retry []string
	n, err = s.Flush(2000, func(sm report.Summary) error {
		retry = append(retry, sm.Key)
		return nil
	})
	t.Logf("输入 重试 Flush | 输出 n=%d err=%v keys=%v", n, err, retry)
	if n != 2 || err != nil || len(retry) != 2 || retry[0] != "b" || retry[1] != "c" {
		t.Fatalf("重试结果异常 n=%d err=%v keys=%v", n, err, retry)
	}
}

// TestFlushValidation nil sink 参数非法；时钟回退报错且不交付；Flush 推进时钟。
func TestFlushValidation(t *testing.T) {
	s, _ := New(0, 10, 1000, 10, 10)
	s.Record(100, "t", "a", 1)
	if n, err := s.Flush(2000, nil); n != 0 || !errors.Is(err, report.ErrInvalidArgument) {
		t.Fatalf("nil sink: n=%d err=%v", n, err)
	}
	if _, err := s.Flush(50, func(report.Summary) error { return nil }); !errors.Is(err, report.ErrClockSkew) {
		t.Fatalf("Flush 回退应报错, got %v", err)
	}
	// 回退后 pending 不变；Flush 成功推进时钟后，更早 now 的 Record 被拒。
	if p, _ := s.Pending("t", "a"); p != 1 {
		t.Fatalf("被拒 Flush 改了状态 pending=%d", p)
	}
	if n, _ := s.Flush(2000, func(report.Summary) error { return nil }); n != 1 {
		t.Fatalf("Flush 应交付 1 条, got %d", n)
	}
	if d := s.Record(1999, "t", "b", 1); !errors.Is(d.Err, report.ErrClockSkew) {
		t.Fatalf("Flush 后时钟应推进到 2000, got %+v", d)
	}
}

// TestAccountingIdentity 被接受记录数 = 放行数 + 已交付 Dropped + 当前 Pending。
func TestAccountingIdentity(t *testing.T) {
	s, _ := New(1, 3, 300, 3, 2)
	rng := rand.New(rand.NewSource(42))
	var accepted, kept, deliveredDrops int64
	keys := []string{"a", "b", "c", "d"}
	tenants := []string{"x", "y"}
	var now int64
	sink := func(sm report.Summary) error {
		deliveredDrops += sm.Dropped
		return nil
	}
	for step := 0; step < 1500; step++ {
		if rng.Intn(8) == 0 {
			fnow := now + int64(rng.Intn(900))
			n, err := s.Flush(fnow, sink)
			now = fnow
			t.Logf("Flush n=%d err=%v", n, err)
		}
		now += int64(rng.Intn(120))
		tn := tenants[rng.Intn(len(tenants))]
		k := keys[rng.Intn(len(keys))]
		sev := rng.Intn(6)
		d := s.Record(now, tn, k, sev)
		if d.Err != nil {
			t.Fatalf("不应拒绝: %+v", d)
		}
		accepted++
		if d.Kept {
			kept++
		}
		for _, sm := range d.Summaries {
			deliveredDrops += sm.Dropped
		}
	}
	n, _ := s.Flush(now+1000, sink)
	t.Logf("收尾 Flush n=%d", n)
	var pendingSum int64
	for _, tn := range tenants {
		for _, k := range keys {
			if p, ok := s.Pending(tn, k); ok {
				pendingSum += p
			}
		}
	}
	t.Logf("恒等式 %d = kept %d + delivered %d + pending %d",
		accepted, kept, deliveredDrops, pendingSum)
	if accepted != kept+deliveredDrops+pendingSum {
		t.Fatal("丢弃计数恒等式不成立")
	}
}
