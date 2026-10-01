package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"testing"
)

func mustNew(t *testing.T, wslow, total, capY int64) *Calculator {
	t.Helper()
	c, err := New(wslow, total, capY)
	if err != nil {
		t.Fatalf("New(%d,%d,%d) unexpected error: %v", wslow, total, capY, err)
	}
	return c
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func toMap(shares []Share) map[string]int64 {
	m := make(map[string]int64, len(shares))
	for _, s := range shares {
		m[s.ID] = s.Count
	}
	return m
}

func TestInvalidConfig(t *testing.T) {
	cases := [][3]int64{
		{0, 10, 10},
		{1_000_000_001, 10, 10},
		{100, 0, 0},
		{100, 1_000_001, 10},
		{100, 10, 0},
		{100, 10, 11},
		{-1, 10, 10},
	}
	for i, p := range cases {
		if _, err := New(p[0], p[1], p[2]); !errors.Is(err, ErrInvalidConfig) {
			t.Fatalf("case %d: got %v, want ErrInvalidConfig", i, err)
		}
	}
}

func TestSpecExamples(t *testing.T) {
	// Wslow=100,T=10000,Y=10000: a w10 join0, b w10 join50, now=100
	c := mustNew(t, 100, 10000, 10000)
	mustOK(t, c.AddHost("a", 10, 0))
	mustOK(t, c.AddHost("b", 10, 50))
	got, err := c.Shares(100)
	mustOK(t, err)
	want := []Share{{"a", 6667}, {"b", 3333}}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("got %v want %v", got, want)
	}

	// b 在 100 才登记：e=0，eff=max(1,0)=1 -> 9091/909
	c2 := mustNew(t, 100, 10000, 10000)
	mustOK(t, c2.AddHost("a", 10, 0))
	mustOK(t, c2.AddHost("b", 10, 100))
	got2, err := c2.Shares(100)
	mustOK(t, err)
	want2 := []Share{{"a", 9091}, {"b", 909}}
	if len(got2) != 2 || got2[0] != want2[0] || got2[1] != want2[1] {
		t.Fatalf("got %v want %v", got2, want2)
	}

	// 三台权重 1，余数并列 -> 差额 1 给 id 最小者
	c3 := mustNew(t, 100, 10000, 10000)
	mustOK(t, c3.AddHost("h1", 1, 0))
	mustOK(t, c3.AddHost("h2", 1, 0))
	mustOK(t, c3.AddHost("h3", 1, 0))
	got3, err := c3.Shares(100)
	mustOK(t, err)
	want3 := []Share{{"h1", 3334}, {"h2", 3333}, {"h3", 3333}}
	if len(got3) != 3 || got3[0] != want3[0] || got3[1] != want3[1] || got3[2] != want3[2] {
		t.Fatalf("got %v want %v", got3, want3)
	}
}

func TestCappingExample(t *testing.T) {
	// Wslow=100,T=10,Y=5: a w10 j0(eff10), b w10 j50(eff5), c w1 j0(eff1)
	c := mustNew(t, 100, 10, 5)
	mustOK(t, c.AddHost("c", 1, 0))
	mustOK(t, c.AddHost("a", 10, 0))
	mustOK(t, c.AddHost("b", 10, 50))
	got, err := c.Shares(100)
	mustOK(t, err)
	want := []Share{{"a", 5}, {"b", 4}, {"c", 1}}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("got %v want %v", got, want)
	}

	// Y=4 仅 a,b 健康 -> 容量不足
	c2 := mustNew(t, 100, 10, 4)
	mustOK(t, c2.AddHost("a", 10, 0))
	mustOK(t, c2.AddHost("b", 10, 0))
	if _, err := c2.Shares(100); !errors.Is(err, ErrInsufficientCapacity) {
		t.Fatalf("got %v want ErrInsufficientCapacity", err)
	}
}

func TestSlowStartBoundaries(t *testing.T) {
	// e == Wslow 取满权重；e == Wslow-1 取下取整；e=0 与下取整 0 时至少为 1
	c := mustNew(t, 100, 1000, 1000)
	mustOK(t, c.AddHost("a", 10, 0))  // now=100 时 e=100，eff=10
	mustOK(t, c.AddHost("b", 10, 1))  // e=99，eff=floor(990/100)=9
	mustOK(t, c.AddHost("c", 3, 99))  // e=1，eff=max(1,floor(3/100))=1
	mustOK(t, c.AddHost("d", 1, 100)) // e=0，eff=max(1,0)=1
	got, err := c.Shares(100)
	mustOK(t, err)
	m := toMap(got)
	// eff: a=10,b=9,c=1,d=1,E=21,T=1000 -> floor 476/428/47/47，余 4/12/13/13
	if m["a"] != 476 || m["b"] != 428 || m["c"] != 48 || m["d"] != 48 {
		t.Fatalf("got %v want a=476 b=428 c=48 d=48", m)
	}

	c.mu.Lock()
	ha, hb, hc, hd := c.hosts["a"], c.hosts["b"], c.hosts["c"], c.hosts["d"]
	if w := c.effectiveWeight(ha, 100); w != 10 {
		t.Fatalf("e==wslow eff=%d want 10", w)
	}
	if w := c.effectiveWeight(hb, 100); w != 9 {
		t.Fatalf("e=99 eff=%d want 9", w)
	}
	if w := c.effectiveWeight(hc, 100); w != 1 {
		t.Fatalf("floor=0 eff=%d want 1", w)
	}
	if w := c.effectiveWeight(hd, 100); w != 1 {
		t.Fatalf("e=0 eff=%d want 1", w)
	}
	hb.healthy = false
	if w := c.effectiveWeight(hb, 100); w != 0 {
		t.Fatalf("unhealthy eff=%d want 0", w)
	}
	c.mu.Unlock()
}

func TestHealthAndJoinSemantics(t *testing.T) {
	// 恢复后重新爬坡；设同值不重置；SetWeight 保留 join
	c := mustNew(t, 100, 1000, 1000)
	mustOK(t, c.AddHost("a", 10, 0))
	mustOK(t, c.SetHealth("a", false, 10))
	mustOK(t, c.SetWeight("a", 5, 20))     // 只改权重，join 保持 0
	mustOK(t, c.SetHealth("a", false, 30)) // 同值不改状态
	mustOK(t, c.SetHealth("a", true, 50))  // unhealthy->healthy：join=50
	c.mu.Lock()
	if c.hosts["a"].join != 50 || c.hosts["a"].weight != 5 {
		t.Fatalf("a join=%d weight=%d want join=50 weight=5", c.hosts["a"].join, c.hosts["a"].weight)
	}
	c.mu.Unlock()
	mustOK(t, c.AddHost("b", 5, 100)) // e=0，eff=1
	got, err := c.Shares(100)
	mustOK(t, err)
	// a e=50 eff=floor(250/100)=2；b eff=1；E=3 -> 666 余2 / 333 余1 -> 667/333
	if m := toMap(got); m["a"] != 667 || m["b"] != 333 {
		t.Fatalf("got %v want a=667 b=333", m)
	}

	// 不健康实例份额为 0；无健康实例报错
	mustOK(t, c.SetHealth("b", false, 110))
	got2, err := c.Shares(110)
	mustOK(t, err)
	if m := toMap(got2); m["a"] != 1000 || m["b"] != 0 {
		t.Fatalf("got %v want a=1000 b=0", m)
	}
	mustOK(t, c.SetHealth("a", false, 120))
	if _, err := c.Shares(130); !errors.Is(err, ErrNoHealthyHost) {
		t.Fatalf("got %v want ErrNoHealthyHost", err)
	}

	// 移除后同 id 再登记视为新实例，重新爬坡
	mustOK(t, c.RemoveHost("a", 140))
	mustOK(t, c.AddHost("a", 10, 200)) // join=200
	mustOK(t, c.SetHealth("b", true, 200))
	got3, err := c.Shares(200)
	mustOK(t, err)
	// a e=0 eff=1，b e=0 eff=1，各 500
	if m := toMap(got3); m["a"] != 500 || m["b"] != 500 {
		t.Fatalf("got %v want a=500 b=500", m)
	}
}

func TestCappingEdgeCases(t *testing.T) {
	// 份数恰等于 Y 不触顶：a 固定 5 后，b 第二轮得 5，恰好等于 Y
	c := mustNew(t, 100, 10, 5)
	mustOK(t, c.AddHost("a", 9, 0))
	mustOK(t, c.AddHost("b", 1, 0))
	got, err := c.Shares(100)
	mustOK(t, err)
	if m := toMap(got); m["a"] != 5 || m["b"] != 5 {
		t.Fatalf("got %v want a=5 b=5", m)
	}

	// Y+1 触顶：同样权重 T=12,Y=5 时 a 第一轮 11>5；固定后 Trem=7 全给 b=7>5
	// 再固定 5，Trem=2，A 空 -> 容量不足
	c2 := mustNew(t, 100, 12, 5)
	mustOK(t, c2.AddHost("a", 9, 0))
	mustOK(t, c2.AddHost("b", 1, 0))
	if _, err := c2.Shares(100); !errors.Is(err, ErrInsufficientCapacity) {
		t.Fatalf("got %v want ErrInsufficientCapacity", err)
	}

	// Y*健康数 == T：全部固定为 Y
	c3 := mustNew(t, 100, 12, 4)
	mustOK(t, c3.AddHost("a", 100, 0))
	mustOK(t, c3.AddHost("b", 100, 0))
	mustOK(t, c3.AddHost("z", 1, 0))
	got3, err := c3.Shares(100)
	mustOK(t, err)
	if m := toMap(got3); m["a"] != 4 || m["b"] != 4 || m["z"] != 4 {
		t.Fatalf("got %v want all 4 (sum 12)", m)
	}

	// Y*健康数 < T：容量不足
	c4 := mustNew(t, 100, 13, 4)
	mustOK(t, c4.AddHost("a", 100, 0))
	mustOK(t, c4.AddHost("b", 100, 0))
	mustOK(t, c4.AddHost("z", 1, 0))
	if _, err := c4.Shares(100); !errors.Is(err, ErrInsufficientCapacity) {
		t.Fatalf("got %v want ErrInsufficientCapacity", err)
	}
}

func TestMultiCapSameRoundAndThirdRound(t *testing.T) {
	// 同一轮多个实例同时触顶且一并固定：
	// T=20,Y=4；a,b eff=10，d eff=1。E=21：a,b=floor(200/21)=9 同时触顶，
	// 一并固定为 4，Trem=12，A={d}；第二轮 d 得 12>4 触顶 -> 固定4,Trem=8,A空
	// -> 容量不足。换成可承接余量的配置验证“一并固定”：
	c := mustNew(t, 100, 20, 4)
	mustOK(t, c.AddHost("a", 10, 0))
	mustOK(t, c.AddHost("b", 10, 0))
	mustOK(t, c.AddHost("m1", 1, 0))
	mustOK(t, c.AddHost("m2", 1, 0))
	mustOK(t, c.AddHost("m3", 1, 0))
	got, err := c.Shares(100)
	mustOK(t, err)
	m := toMap(got)
	// 第一轮 E=23：a,b=floor(200/23)=8 同触顶，各固定4，Trem=12
	// 第二轮 A=3 台 eff=1,E=3：各得 4，恰等于 Y，不触顶
	if m["a"] != 4 || m["b"] != 4 || m["m1"] != 4 || m["m2"] != 4 || m["m3"] != 4 {
		t.Fatalf("got %v want all 4", m)
	}

	// 第三轮场景：第一轮固定 a；第二轮 b 新超限；第三轮余数基于剩余 Trem/E。
	// T=30,Y=5；a eff=100 第一轮独大固定5，Trem=25；b eff=20、c eff=4、d eff=1，
	// E=25：b=floor(500/25)=20 超限固定5，Trem=20；第三轮 E=5(c4,d1)：
	// c=16>5 固定5，Trem=15，A={d}；第四轮 d=15>5 固定5，Trem=10 -> 容量不足。
	// 为得到成功三轮，给最后一台足够“名额容量”用多台小实例承接：
	c2 := mustNew(t, 100, 30, 5)
	mustOK(t, c2.AddHost("a", 100, 0))
	mustOK(t, c2.AddHost("b", 20, 0))
	mustOK(t, c2.AddHost("c", 4, 0))
	mustOK(t, c2.AddHost("d1", 1, 0))
	mustOK(t, c2.AddHost("d2", 1, 0))
	mustOK(t, c2.AddHost("d3", 1, 0))
	got2, err := c2.Shares(100)
	mustOK(t, err)
	m2 := toMap(got2)
	// 第一轮 E=127：a=floor(3000/127)=23 固定5，Trem=25
	// 第二轮 E=27：b=floor(500/27)=18 固定5，Trem=20
	// 第三轮 A={c4,d1,d2,d3}=E=7,Trem=20：c=floor(80/7)=11 固定5,Trem=15
	// 第四轮 A=三台 eff1：各 5，恰等于 Y
	if m2["a"] != 5 || m2["b"] != 5 || m2["c"] != 5 ||
		m2["d1"] != 5 || m2["d2"] != 5 || m2["d3"] != 5 {
		t.Fatalf("got %v want all 5 over four rounds", m2)
	}
}

func TestRemainderTieAndExtraBound(t *testing.T) {
	// 余数并列取 id 小者；差额票数不超过健康实例数
	c := mustNew(t, 100, 7, 7)
	for _, id := range []string{"z", "a", "m", "b"} {
		mustOK(t, c.AddHost(id, 1, 0))
	}
	got, err := c.Shares(100)
	mustOK(t, err)
	// 7/4：三张余数票，id 升序 a,b,m 得 2，z 得 1
	want := []Share{{"a", 2}, {"b", 2}, {"m", 2}, {"z", 1}}
	if len(got) != 4 {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %v want %v", got, want)
		}
	}
}

func TestValidationOrderAndNoStateChange(t *testing.T) {
	c := mustNew(t, 100, 10, 10)
	mustOK(t, c.AddHost("a", 1, 5))

	// 参数非法先于时间非法、时钟回退、状态非法
	if err := c.AddHost("", 1, -1); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("empty id: got %v", err)
	}
	if err := c.AddHost("b", 0, 0); !errors.Is(err, ErrInvalidArg) {
		t.Fatalf("bad weight: got %v", err)
	}
	if err := c.AddHost("b", 1, -1); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("bad time: got %v", err)
	}
	if err := c.AddHost("b", 1, 4); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("rollback: got %v", err)
	}
	if err := c.AddHost("a", 1, 6); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("dup add: got %v", err)
	}
	if err := c.SetWeight("nosuch", 1, 6); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("missing setweight: got %v", err)
	}
	if err := c.SetHealth("nosuch", true, 6); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("missing sethealth: got %v", err)
	}
	if err := c.RemoveHost("nosuch", 6); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("missing remove: got %v", err)
	}

	// 被拒绝的操作不改变状态：仍只有 a，maxNow 仍为 5
	c.mu.Lock()
	if len(c.hosts) != 1 || len(c.order) != 1 || c.maxNow != 5 {
		t.Fatalf("state mutated by rejected ops: hosts=%d order=%d maxNow=%d",
			len(c.hosts), len(c.order), c.maxNow)
	}
	c.mu.Unlock()

	// 时钟回退拒绝后，原时刻操作仍成功
	mustOK(t, c.AddHost("b", 1, 6))

	// Shares 报错（无健康实例 / 容量不足）也推进 maxNow
	mustOK(t, c.SetHealth("a", false, 7))
	mustOK(t, c.SetHealth("b", false, 7))
	if _, err := c.Shares(8); !errors.Is(err, ErrNoHealthyHost) {
		t.Fatalf("got %v want ErrNoHealthyHost", err)
	}
	if err := c.AddHost("c", 1, 7); !errors.Is(err, ErrClockRollback) {
		t.Fatalf("failing Shares must advance maxNow, got %v", err)
	}

	// 时间上界
	if _, err := c.Shares(1_000_000_000_000_001); !errors.Is(err, ErrInvalidTime) {
		t.Fatalf("time upper bound: got %v", err)
	}
}

func TestInvariantsAndDeterminism(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	for iter := 0; iter < 200; iter++ {
		wslow := int64(1 + rng.Intn(1000))
		total := int64(1 + rng.Intn(200))
		capY := int64(1 + rng.Intn(int(total)))
		c := mustNew(t, wslow, total, capY)
		ids := []string{"h0", "h1", "h2", "h3", "h4", "h5"}
		now := int64(0)
		rng.Shuffle(len(ids), func(i, j int) { ids[i], ids[j] = ids[j], ids[i] })
		for _, id := range ids {
			now += int64(rng.Intn(int(wslow) + 2))
			_ = c.AddHost(id, int64(1+rng.Intn(1_000_000)), now)
		}
		now += int64(rng.Intn(int(wslow) + 5))
		got1, err1 := c.Shares(now)
		got2, err2 := c.Shares(now)
		if (err1 != nil) != (err2 != nil) || err1 != err2 {
			t.Fatalf("nondeterministic error: %v vs %v", err1, err2)
		}
		if err1 != nil {
			continue
		}
		if len(got1) != len(c.hosts) {
			t.Fatalf("shares len %d want %d", len(got1), len(c.hosts))
		}
		var sum int64
		for i, s := range got1 {
			if i > 0 && got1[i-1].ID >= s.ID {
				t.Fatalf("not id-sorted: %v", got1)
			}
			if s.Count < 0 || s.Count > capY {
				t.Fatalf("share out of [0,Y]: %+v", s)
			}
			sum += s.Count
			if got2[i] != s {
				t.Fatalf("nondeterministic shares: %v vs %v", got1, got2)
			}
		}
		if sum != total {
			t.Fatalf("sum %d want %d", sum, total)
		}
	}
}

func TestConcurrent(t *testing.T) {
	c := mustNew(t, 100, 1000, 1000)
	var wg sync.WaitGroup
	var addMu sync.Mutex
	ts := int64(10)
	next := func() int64 {
		addMu.Lock()
		defer addMu.Unlock()
		ts++
		return ts
	}
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			id := fmt.Sprintf("h%d", g)
			_ = c.AddHost(id, 10, next())
			_, _ = c.Shares(next())
			_ = c.SetWeight(id, 20, next())
			_, _ = c.Shares(next())
		}(g)
	}
	wg.Wait()
	got, err := c.Shares(100)
	mustOK(t, err)
	var sum int64
	for _, s := range got {
		sum += s.Count
	}
	if sum != 1000 {
		t.Fatalf("sum %d want 1000", sum)
	}
}

// naiveHost 是按规格逐步抄写的朴素参考模型。
type naiveHost struct {
	id      string
	weight  int64
	healthy bool
	join    int64
	alive   bool
}

type naiveCalc struct {
	wslow, total, capY, maxNow int64
	hosts                      map[string]*naiveHost
}

func newNaive(wslow, total, capY int64) *naiveCalc {
	return &naiveCalc{wslow: wslow, total: total, capY: capY, hosts: map[string]*naiveHost{}}
}

func (n *naiveCalc) checkTime(now int64) error {
	if now < 0 || now > 1_000_000_000_000_000 {
		return ErrInvalidTime
	}
	if now < n.maxNow {
		return ErrClockRollback
	}
	return nil
}

func (n *naiveCalc) eff(h *naiveHost, now int64) int64 {
	if !h.healthy {
		return 0
	}
	e := now - h.join
	if e >= n.wslow {
		return h.weight
	}
	v := h.weight * e / n.wslow
	if v < 1 {
		return 1
	}
	return v
}

func (n *naiveCalc) add(id string, w, now int64) error {
	if id == "" || w < 1 || w > 1_000_000 {
		return ErrInvalidArg
	}
	if err := n.checkTime(now); err != nil {
		return err
	}
	if h, ok := n.hosts[id]; ok && h.alive {
		return ErrInvalidState
	}
	n.hosts[id] = &naiveHost{id: id, weight: w, healthy: true, join: now, alive: true}
	n.maxNow = now
	return nil
}

func (n *naiveCalc) setWeight(id string, w, now int64) error {
	if id == "" || w < 1 || w > 1_000_000 {
		return ErrInvalidArg
	}
	if err := n.checkTime(now); err != nil {
		return err
	}
	h, ok := n.hosts[id]
	if !ok || !h.alive {
		return ErrInvalidState
	}
	h.weight = w
	n.maxNow = now
	return nil
}

func (n *naiveCalc) setHealth(id string, healthy bool, now int64) error {
	if id == "" {
		return ErrInvalidArg
	}
	if err := n.checkTime(now); err != nil {
		return err
	}
	h, ok := n.hosts[id]
	if !ok || !h.alive {
		return ErrInvalidState
	}
	if h.healthy != healthy {
		h.healthy = healthy
		if healthy {
			h.join = now
		}
	}
	n.maxNow = now
	return nil
}

func (n *naiveCalc) remove(id string, now int64) error {
	if id == "" {
		return ErrInvalidArg
	}
	if err := n.checkTime(now); err != nil {
		return err
	}
	h, ok := n.hosts[id]
	if !ok || !h.alive {
		return ErrInvalidState
	}
	h.alive = false
	n.maxNow = now
	return nil
}

func (n *naiveCalc) shares(now int64) (map[string]int64, error) {
	if err := n.checkTime(now); err != nil {
		return nil, err
	}
	n.maxNow = now

	var ids []string
	healthy := map[string]bool{}
	for id, h := range n.hosts {
		if !h.alive {
			continue
		}
		ids = append(ids, id)
		if h.healthy {
			healthy[id] = true
		}
	}
	sort.Strings(ids)
	out := map[string]int64{}
	for _, id := range ids {
		out[id] = 0
	}

	var active []string
	for _, id := range ids {
		if healthy[id] {
			active = append(active, id)
		}
	}
	if len(active) == 0 {
		return nil, ErrNoHealthyHost
	}

	trem := n.total
	for len(active) > 0 {
		var eSum int64
		effM := map[string]int64{}
		for _, id := range active {
			v := n.eff(n.hosts[id], now)
			effM[id] = v
			eSum += v
		}
		base := map[string]int64{}
		remM := map[string]int64{}
		var assigned int64
		for _, id := range active {
			v := effM[id] * trem
			base[id] = v / eSum
			remM[id] = v % eSum
			assigned += base[id]
		}
		ranked := append([]string(nil), active...)
		sort.SliceStable(ranked, func(i, j int) bool {
			if remM[ranked[i]] != remM[ranked[j]] {
				return remM[ranked[i]] > remM[ranked[j]]
			}
			return ranked[i] < ranked[j]
		})
		for k := int64(0); k < trem-assigned; k++ {
			base[ranked[k]]++
		}

		var over, next []string
		for _, id := range active {
			if base[id] > n.capY {
				over = append(over, id)
			} else {
				next = append(next, id)
			}
		}
		if len(over) == 0 {
			for _, id := range active {
				out[id] = base[id]
			}
			return out, nil
		}
		for _, id := range over {
			out[id] = n.capY
			trem -= n.capY
		}
		active = next
	}
	if trem > 0 {
		return nil, ErrInsufficientCapacity
	}
	return out, nil
}

type opKind int

const (
	opAdd opKind = iota
	opSetWeight
	opSetHealthTrue
	opSetHealthFalse
	opRemove
	opShares
)

func TestRandomDifferential(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping 2000 random sequences in -short mode")
	}
	rng := rand.New(rand.NewSource(20261001))
	var totalChecks int
	for iter := 0; iter < 2000; iter++ {
		wslow := int64(1 + rng.Intn(500))
		total := int64(1 + rng.Intn(300))
		capY := int64(1 + rng.Intn(int(total)))
		c := mustNew(t, wslow, total, capY)
		ref := newNaive(wslow, total, capY)

		var log strings.Builder
		fmt.Fprintf(&log, "iter=%d wslow=%d total=%d capY=%d\n", iter, wslow, total, capY)

		now := int64(0)
		lastGood := int64(0)
		seed := func() int64 {
			now = lastGood + int64(1+rng.Intn(int(wslow)+3))
			lastGood = now
			return now
		}
		// 先登记 1~5 台健康实例，保证后续操作大多落在有效状态上。
		nHosts := 1 + rng.Intn(5)
		hostIDs := make([]string, nHosts)
		for i := range hostIDs {
			id := fmt.Sprintf("h%d", i)
			hostIDs[i] = id
			t1 := seed()
			w0 := int64(1 + rng.Intn(1_000_000))
			if err := c.AddHost(id, w0, t1); err != nil {
				t.Fatalf("seed add: %v", err)
			}
			if err := ref.add(id, w0, t1); err != nil {
				t.Fatalf("seed naive add: %v", err)
			}
			fmt.Fprintf(&log, "add(%q,%d,%d) -> <nil>\n", id, w0, t1)
		}
		nOps := 2 + rng.Intn(14)
		for k := 0; k < nOps; k++ {
			kind := opKind(rng.Intn(6))
			id := hostIDs[rng.Intn(len(hostIDs))]
			if kind == opAdd && rng.Intn(3) == 0 {
				id = fmt.Sprintf("x%d", rng.Intn(3))
			}
			w := int64(1 + rng.Intn(1_000_000))
			// 时间：约 85% 单调推进，其余故意非法/回退以覆盖拒绝路径
			switch rng.Intn(20) {
			case 0, 1:
				now = -1
			case 2:
				now = 1_000_000_000_000_001
			case 3:
				now = lastGood // 同值合法，验证设同值/同刻操作
			default:
				now = lastGood + int64(1+rng.Intn(int(wslow)+3))
			}

			var gotErr, refErr error
			var gotShares []Share
			var refShares map[string]int64

			switch kind {
			case opAdd:
				gotErr = c.AddHost(id, w, now)
				refErr = ref.add(id, w, now)
				fmt.Fprintf(&log, "add(%q,%d,%d) -> %v\n", id, w, now, gotErr)
			case opSetWeight:
				gotErr = c.SetWeight(id, w, now)
				refErr = ref.setWeight(id, w, now)
				fmt.Fprintf(&log, "setWeight(%q,%d,%d) -> %v\n", id, w, now, gotErr)
			case opSetHealthTrue:
				gotErr = c.SetHealth(id, true, now)
				refErr = ref.setHealth(id, true, now)
				fmt.Fprintf(&log, "setHealth(%q,true,%d) -> %v\n", id, now, gotErr)
			case opSetHealthFalse:
				gotErr = c.SetHealth(id, false, now)
				refErr = ref.setHealth(id, false, now)
				fmt.Fprintf(&log, "setHealth(%q,false,%d) -> %v\n", id, now, gotErr)
			case opRemove:
				gotErr = c.RemoveHost(id, now)
				refErr = ref.remove(id, now)
				fmt.Fprintf(&log, "remove(%q,%d) -> %v\n", id, now, gotErr)
			case opShares:
				gotShares, gotErr = c.Shares(now)
				refShares, refErr = ref.shares(now)
				fmt.Fprintf(&log, "shares(%d) -> %v err=%v\n", now, gotShares, gotErr)
			}

			if gotErr != refErr {
				t.Fatalf("iter=%d op=%d error mismatch: impl=%v naive=%v\n%s",
					iter, kind, gotErr, refErr, log.String())
			}
			if gotErr == nil && now >= 0 && now <= 1_000_000_000_000_000 {
				lastGood = now
			}
			if gotErr == nil && kind == opShares {
				totalChecks++
				if len(gotShares) != len(refShares) {
					t.Fatalf("iter=%d len mismatch: impl=%v naive=%v\n%s",
						iter, gotShares, refShares, log.String())
				}
				var sum int64
				for _, s := range gotShares {
					sum += s.Count
					if refShares[s.ID] != s.Count {
						t.Fatalf("iter=%d share %q impl=%d naive=%d\n%s",
							iter, s.ID, s.Count, refShares[s.ID], log.String())
					}
				}
				if sum != total {
					t.Fatalf("iter=%d sum=%d total=%d\n%s", iter, sum, total, log.String())
				}
				t.Logf("iter=%d input/op log:\n%s判定：与朴素实现一致，总和=%d",
					iter, log.String(), sum)
			}
			// 非法时间不应进入双方 maxNow（下一轮重新生成合法时间继续）
		}
	}
	t.Logf("random differential checks: %d successful Shares across 2000 sequences", totalChecks)
}
