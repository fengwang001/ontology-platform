package routing

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

var base = time.Date(2026, 9, 30, 0, 0, 0, 0, time.UTC)

func at(sec int) time.Time { return base.Add(time.Duration(sec) * time.Second) }

func mustReceive(t *testing.T, tb *Table, now time.Time, n, p string, m int) {
	t.Helper()
	if err := tb.Receive(now, n, p, m); err != nil {
		t.Fatalf("Receive(%v, %q, %q, %d) 意外拒绝: %v", now, n, p, m, err)
	}
}

func TestNextHopAcceptedIncrease(t *testing.T) {
	tb := New(180*time.Second, 120*time.Second)
	mustReceive(t, tb, at(0), "n1", "p", 1)
	mustReceive(t, tb, at(10), "n1", "p", 5)
	got, ok := tb.NextHop("p")
	t.Logf("输入: n1 通告 p 度量 1 后又通告度量 5（当前下一跳度量变大）")
	t.Logf("输出: %+v ok=%v", got, ok)
	t.Logf("判定依据: 当前下一跳的通告总是接受（含变大），度量应为 6")
	if !ok || got.Metric != 6 || got.NextHop != "n1" {
		t.Fatalf("got %+v ok=%v, want metric=6 nextHop=n1", got, ok)
	}
}

func TestEqualMetricDoesNotReplace(t *testing.T) {
	tb := New(180*time.Second, 120*time.Second)
	mustReceive(t, tb, at(0), "n1", "p", 2)
	mustReceive(t, tb, at(10), "n2", "p", 2)
	got, ok := tb.NextHop("p")
	t.Logf("输入: n1 通告 p 度量 2，n2 随后通告等值度量 2")
	t.Logf("输出: %+v ok=%v", got, ok)
	t.Logf("判定依据: 其他邻居等值通告不替换，下一跳应保持 n1")
	if !ok || got.NextHop != "n1" || got.Metric != 3 {
		t.Fatalf("got %+v ok=%v, want nextHop=n1 metric=3", got, ok)
	}
}

func TestGarbageReplacedByBetterMetric(t *testing.T) {
	tb := New(180*time.Second, 120*time.Second)
	mustReceive(t, tb, at(0), "n1", "p", 1)
	mustReceive(t, tb, at(10), "n1", "p", 16) // 当前下一跳通告不可达，转垃圾
	if _, ok := tb.NextHop("p"); ok {
		t.Fatal("垃圾项不应出现在查询结果中")
	}
	mustReceive(t, tb, at(20), "n2", "p", 2) // 其他邻居更优度量
	got, ok := tb.NextHop("p")
	t.Logf("输入: p 经 n1 转垃圾后，n2 通告度量 2")
	t.Logf("输出: %+v ok=%v", got, ok)
	t.Logf("判定依据: 垃圾项被严格更小度量取代并恢复有效，下一跳改为 n2，度量 3")
	if !ok || got.NextHop != "n2" || got.Metric != 3 {
		t.Fatalf("got %+v ok=%v, want nextHop=n2 metric=3", got, ok)
	}
}

func TestGarbageRecoveredBySameNextHop(t *testing.T) {
	tb := New(180*time.Second, 120*time.Second)
	mustReceive(t, tb, at(0), "n1", "p", 1)
	mustReceive(t, tb, at(10), "n1", "p", 16)
	mustReceive(t, tb, at(20), "n1", "p", 3)
	got, ok := tb.NextHop("p")
	t.Logf("输入: p 经 n1 转垃圾后，n1 又通告度量 3")
	t.Logf("输出: %+v ok=%v", got, ok)
	t.Logf("判定依据: 垃圾项被当前下一跳接受 c<16 后恢复为有效，度量 4")
	if !ok || got.Metric != 4 || got.NextHop != "n1" {
		t.Fatalf("got %+v ok=%v, want metric=4 nextHop=n1", got, ok)
	}
}

func TestSweepCrossesTimeoutAndGarbage(t *testing.T) {
	tb := New(10*time.Second, 5*time.Second)
	mustReceive(t, tb, at(0), "n1", "p", 1)
	if err := tb.Sweep(at(100)); err != nil {
		t.Fatal(err)
	}
	_, ok := tb.NextHop("p")
	ads := tb.Advertisements("n2")
	t.Logf("输入: t=0 学习 p，T=10s G=5s，t=100 一次整理")
	t.Logf("输出: NextHop ok=%v, Advertisements=%+v", ok, ads)
	t.Logf("判定依据: 一次整理连续完成转垃圾与删除两步，表项应被删除")
	if ok {
		t.Fatal("表项应已被删除")
	}
	if len(ads) != 0 {
		t.Fatalf("删除后不应再有通告, got %+v", ads)
	}
}

func TestSweepGarbageSinceIsUpdatedAtPlusTimeout(t *testing.T) {
	tb := New(10*time.Second, 5*time.Second)
	mustReceive(t, tb, at(0), "n1", "p", 1)
	// t=12：超时（0+10<=12）转垃圾，垃圾起点为 10 而非 12；10+5=15>12 不删除。
	if err := tb.Sweep(at(12)); err != nil {
		t.Fatal(err)
	}
	ads := tb.Advertisements("n2")
	t.Logf("输入: t=0 学习 p，T=10s G=5s，t=12 整理")
	t.Logf("输出: %+v", ads)
	t.Logf("判定依据: 垃圾起点为「更新时刻加 T」即 t=10，10+5=15>12，表项保留且通告度量 16")
	if len(ads) != 1 || ads[0].Metric != Infinity {
		t.Fatalf("got %+v, want 单条度量 16 的垃圾通告", ads)
	}
	// t=15：10+5<=15，删除。
	if err := tb.Sweep(at(15)); err != nil {
		t.Fatal(err)
	}
	if ads := tb.Advertisements("n2"); len(ads) != 0 {
		t.Fatalf("t=15 时应删除, got %+v", ads)
	}
}

func TestAdvertisementsSplitHorizonPoisonReverse(t *testing.T) {
	tb := New(180*time.Second, 120*time.Second)
	mustReceive(t, tb, at(0), "n1", "a", 1)
	mustReceive(t, tb, at(0), "n2", "b", 2)
	mustReceive(t, tb, at(0), "n1", "c", 3)
	mustReceive(t, tb, at(1), "n1", "c", 16) // c 转垃圾
	ads := tb.Advertisements("n1")
	want := []Advertisement{
		{Prefix: "a", Metric: Infinity}, // 水平分割：下一跳是 n1
		{Prefix: "b", Metric: 3},        // 普通通告
		{Prefix: "c", Metric: Infinity}, // 毒性逆转：垃圾项
	}
	t.Logf("输入: a/c 下一跳 n1，b 下一跳 n2，c 已转垃圾；为 n1 生成通告")
	t.Logf("输出: %+v", ads)
	t.Logf("判定依据: 下一跳是 n1 或垃圾项度量为 16，按前缀升序，want %+v", want)
	if fmt.Sprintf("%+v", ads) != fmt.Sprintf("%+v", want) {
		t.Fatalf("got %+v, want %+v", ads, want)
	}
	ads2 := tb.Advertisements("n2")
	t.Logf("为 n2 生成通告: %+v（a 恢复真实度量 2，b 被水平分割）", ads2)
	if ads2[0].Metric != 2 || ads2[1].Metric != Infinity {
		t.Fatalf("got %+v", ads2)
	}
}

func TestMetricCappedAt16(t *testing.T) {
	tb := New(180*time.Second, 120*time.Second)
	mustReceive(t, tb, at(0), "n1", "p", 16)
	t.Logf("输入: 通告度量 16，c=min(16+1,16)=16")
	t.Logf("判定依据: 无表项时仅 c<16 才建表项，度量封顶 16")
	if _, ok := tb.NextHop("p"); ok {
		t.Fatal("c=16 不应建表项")
	}
	mustReceive(t, tb, at(0), "n1", "q", 15)
	got, ok := tb.NextHop("q")
	t.Logf("输入: 通告度量 15，c=min(15+1,16)=16，输出: %+v ok=%v", got, ok)
	if ok {
		t.Fatal("c=16 同样不应建表项")
	}
	mustReceive(t, tb, at(0), "n1", "r", 14)
	if got, ok := tb.NextHop("r"); !ok || got.Metric != 15 {
		t.Fatalf("got %+v ok=%v, want metric=15", got, ok)
	}
}

func TestRejectOrder(t *testing.T) {
	tb := New(180*time.Second, 120*time.Second)
	mustReceive(t, tb, at(100), "n1", "p", 1)
	cases := []struct {
		name string
		now  time.Time
		n, p string
		m    int
		want error
	}{
		{"时钟回拨优先于其他错误", at(99), "", "", 99, ErrClockBackward},
		{"邻居为空", at(100), "", "", 99, ErrEmptyNeighbor},
		{"前缀为空", at(100), "n1", "", 99, ErrEmptyPrefix},
		{"度量超界", at(100), "n1", "p", 17, ErrMetricRange},
		{"度量为负", at(100), "n1", "p", -1, ErrMetricRange},
	}
	for _, c := range cases {
		err := tb.Receive(c.now, c.n, c.p, c.m)
		t.Logf("输入: Receive(%v, %q, %q, %d) 输出: %v 判定依据: %s", c.now, c.n, c.p, c.m, err, c.name)
		if !errors.Is(err, c.want) {
			t.Fatalf("%s: got %v, want %v", c.name, err, c.want)
		}
	}
	if err := tb.Sweep(at(99)); !errors.Is(err, ErrClockBackward) {
		t.Fatalf("Sweep 时钟回拨: got %v", err)
	}
	got, ok := tb.NextHop("p")
	t.Logf("输出: 拒绝后表项 %+v ok=%v", got, ok)
	t.Logf("判定依据: 被拒绝的操作不得改变表项与更新时刻")
	if !ok || got.Metric != 2 || got.NextHop != "n1" {
		t.Fatalf("表项被错误修改: %+v ok=%v", got, ok)
	}
	// 更新时刻未被拒绝操作刷新：t=100+T 时仍应超时转垃圾。
	if err := tb.Sweep(at(100 + 180)); err != nil {
		t.Fatal(err)
	}
	if _, ok := tb.NextHop("p"); ok {
		t.Fatal("更新时刻不应被拒绝操作刷新，表项应已超时")
	}
}

func TestDeterministicReplay(t *testing.T) {
	run := func() []Advertisement {
		tb := New(10*time.Second, 5*time.Second)
		mustReceive(t, tb, at(0), "n1", "b", 1)
		mustReceive(t, tb, at(1), "n2", "a", 2)
		mustReceive(t, tb, at(2), "n1", "b", 16)
		if err := tb.Sweep(at(20)); err != nil {
			t.Fatal(err)
		}
		return tb.Advertisements("n3")
	}
	first := run()
	for i := 0; i < 5; i++ {
		if got := run(); fmt.Sprintf("%+v", got) != fmt.Sprintf("%+v", first) {
			t.Fatalf("第 %d 次重放不一致: %+v vs %+v", i, got, first)
		}
	}
	t.Logf("输出: %+v 判定依据: 相同操作序列重放结果完全相同", first)
}

func TestConcurrent(t *testing.T) {
	tb := New(10*time.Second, 5*time.Second)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				now := at(j)
				_ = tb.Receive(now, fmt.Sprintf("n%d", i), fmt.Sprintf("p%d", j%10), j%17)
				_ = tb.Sweep(now)
				_, _ = tb.NextHop(fmt.Sprintf("p%d", j%10))
				_ = tb.Advertisements(fmt.Sprintf("n%d", i))
			}
		}(i)
	}
	wg.Wait()
	for _, ad := range tb.Advertisements("nobody") {
		if ad.Metric > Infinity {
			t.Fatalf("度量超过 16: %+v", ad)
		}
	}
	t.Log("并发调用结束，度量均不超过 16（配合 -race 验证）")
}
