package ring

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"

	"ontology/change"
)

// mustWrite 断言 Write 成功且结果符合期望。
func mustWrite(t *testing.T, s *Sim, site int, key string, op change.Op, val int64, now int64, want Result) {
	t.Helper()
	got, err := s.Write(site, key, op, val, now)
	if err != nil {
		t.Fatalf("Write(%d,%q,%v,%d,%d) 意外报错: %v", site, key, op, val, now, err)
	}
	if got != want {
		t.Fatalf("Write(%d,%q,%v,%d,%d) = %v, 期望 %v", site, key, op, val, now, got, want)
	}
	t.Logf("输入 Write(%d,%q,%v,%d,%d) 输出 %v 判定依据: 与规则推导的期望 %v 一致",
		site, key, op, val, now, got, want)
}

// mustDeliver 断言 Deliver 成功且结果符合期望。
func mustDeliver(t *testing.T, s *Sim, from, to int, want Result) {
	t.Helper()
	got, err := s.Deliver(from, to)
	if err != nil {
		t.Fatalf("Deliver(%d,%d) 意外报错: %v", from, to, err)
	}
	if got != want {
		t.Fatalf("Deliver(%d,%d) = %v, 期望 %v", from, to, got, want)
	}
	t.Logf("输入 Deliver(%d,%d) 输出 %v 判定依据: 与规则推导的期望 %v 一致", from, to, got, want)
}

// changeID 唯一标识一条变更。
type changeID struct {
	origin int
	seq    int64
}

// neighborsOf 返回站点 from 的两个邻居（与 Sim 内部拓扑一致）。
func neighborsOf(n, from int) (int, int) {
	return from%n + 1, (from-2+n)%n + 1
}

// drainAll 以随机顺序排空所有连通链路，累计每条变更的出队次数与 Dup 次数。
func drainAll(t *testing.T, s *Sim, rng *rand.Rand, dequeued, dups map[changeID]int) {
	t.Helper()
	n := s.N()
	for {
		var ready [][2]int
		for from := 1; from <= n; from++ {
			a, b := neighborsOf(n, from)
			for _, to := range []int{a, b} {
				if s.LinkUp(from, to) && len(s.Pending(from, to)) > 0 {
					ready = append(ready, [2]int{from, to})
				}
			}
		}
		if len(ready) == 0 {
			return
		}
		pick := ready[rng.Intn(len(ready))]
		head := s.Pending(pick[0], pick[1])[0]
		res, err := s.Deliver(pick[0], pick[1])
		if err != nil {
			t.Fatalf("Deliver(%d,%d) 意外报错: %v", pick[0], pick[1], err)
		}
		id := changeID{head.Origin, head.Seq}
		dequeued[id]++
		if res == Dup {
			dups[id]++
		}
	}
}

// checkConverged 校验排空且连通后的全局不变量：
// 所有站点键表逐键相同；f_s[o] 等于 o 的写计数；
// 每条变更恰好出队 N+1 次且恰 2 次 Dup。
func checkConverged(t *testing.T, s *Sim, keys []string, totalWrites int, dequeued, dups map[changeID]int) {
	t.Helper()
	n := s.N()
	for _, key := range keys {
		rec0, ok0 := s.Get(1, key)
		for site := 2; site <= n; site++ {
			rec, ok := s.Get(site, key)
			if ok != ok0 || rec != rec0 {
				t.Fatalf("键 %q 在站点 1(%v,%v) 与站点 %d(%v,%v) 不一致", key, rec0, ok0, site, rec, ok)
			}
		}
	}
	for site := 1; site <= n; site++ {
		for o := 1; o <= n; o++ {
			if got, want := s.Floor(site, o), s.WriteCount(o); got != want {
				t.Fatalf("Floor(%d,%d)=%d, 期望等于写计数 %d", site, o, got, want)
			}
		}
	}
	if len(dequeued) != totalWrites {
		t.Fatalf("出队的变更条数 %d, 期望等于写入条数 %d", len(dequeued), totalWrites)
	}
	for id, cnt := range dequeued {
		if cnt != n+1 {
			t.Fatalf("变更 %+v 出队 %d 次, 期望 N+1=%d 次", id, cnt, n+1)
		}
		if d := dups[id]; d != 2 {
			t.Fatalf("变更 %+v Dup %d 次, 期望恰 2 次", id, d)
		}
	}
	t.Logf("收敛判定通过: %d 个站点键表一致, f 与写计数对齐, %d 条变更各出队 %d 次且各 2 次 Dup",
		n, totalWrites, n+1)
}

// 例一：同键同 ts 按 origin 决胜；败者仍转发；回环前已见的变更 Dup 不转发。
func TestExample1_OriginTiebreak(t *testing.T) {
	s, err := New(3, []int{1, 2, 3})
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, s, 1, "k", change.Put, 5, 10, Applied)
	mustWrite(t, s, 2, "k", change.Put, 7, 10, Applied)

	mustDeliver(t, s, 1, 2, Lost) // (10,2,1) > (10,1,1)，站点 2 记录不变
	if got := s.Pending(2, 3); len(got) != 2 || got[0].Origin != 2 || got[1].Origin != 1 {
		t.Fatalf("败者未转发: Pending(2,3)=%v, 期望 [c2,c1]", got)
	}
	t.Logf("判定依据: 败者 c1 仍被转发, Pending(2,3)=[c2,c1]")

	mustDeliver(t, s, 1, 3, Applied)
	mustDeliver(t, s, 2, 3, Applied) // c2 胜，值 7
	rec, ok := s.Get(3, "k")
	if !ok || rec.Op != change.Put || rec.Val != 7 || rec.Triple != (change.Triple{Ts: 10, Origin: 2, Seq: 1}) {
		t.Fatalf("Get(3,k)=%+v,%v, 期望 Put(7) 三元组 (10,2,1)", rec, ok)
	}
	mustDeliver(t, s, 2, 3, Dup) // f_3[1] 已为 1，c1 重复
	if got := s.Pending(3, 2); len(got) != 1 || got[0].Origin != 1 {
		t.Fatalf("Dup 不应转发: Pending(3,2)=%v, 期望仅 [c1]", got)
	}
	t.Logf("判定依据: Dup 不改状态也不转发, Pending(3,2) 保持 [c1]")
}

// 例二：排空后收敛到 7；本地 Lost 的写（ts 更小）仍被转发，且同样恰 N+1 次出队、2 次 Dup。
func TestExample2_LoserWriteStillForwarded(t *testing.T) {
	s, _ := New(3, []int{1, 2, 3})
	rng := rand.New(rand.NewSource(1))
	dequeued := make(map[changeID]int)
	dups := make(map[changeID]int)

	mustWrite(t, s, 1, "k", change.Put, 5, 10, Applied)
	mustWrite(t, s, 2, "k", change.Put, 7, 10, Applied)
	drainAll(t, s, rng, dequeued, dups)
	checkConverged(t, s, []string{"k"}, 2, dequeued, dups)
	for site := 1; site <= 3; site++ {
		rec, _ := s.Get(site, "k")
		if rec.Val != 7 {
			t.Fatalf("站点 %d 的 k=%d, 期望 7", site, rec.Val)
		}
	}

	// ts=5 小于现有的 10：本地 Lost，却照常入队 3→1 与 3→2。
	mustWrite(t, s, 3, "k", change.Put, 9, 5, Lost)
	if len(s.Pending(3, 1)) != 1 || len(s.Pending(3, 2)) != 1 {
		t.Fatalf("本地 Lost 的写未入队: Pending(3,1)=%v Pending(3,2)=%v", s.Pending(3, 1), s.Pending(3, 2))
	}
	drainAll(t, s, rng, dequeued, dups)
	checkConverged(t, s, []string{"k"}, 3, dequeued, dups)
	for site := 1; site <= 3; site++ {
		rec, _ := s.Get(site, "k")
		if rec.Val != 7 {
			t.Fatalf("站点 %d 的 k=%d, 期望仍为 7（旧写不得覆盖）", site, rec.Val)
		}
	}
	t.Logf("判定依据: 各站点 k 仍为 7, Floor(s,3)=1, 变更 (3,1) 同样恰出队 N+1 次、2 次 Dup")
}

// 例三：N=4，变更绕环一周回到来源得 Dup，共 N+1=5 次出队、2 次 Dup。
func TestExample3_WrapAroundExactCounts(t *testing.T) {
	s, _ := New(4, []int{1})
	mustWrite(t, s, 1, "k", change.Put, 5, 1, Applied)
	if len(s.Pending(1, 2)) != 1 || len(s.Pending(1, 4)) != 1 {
		t.Fatalf("初始入队应为 1→2 与 1→4")
	}
	mustDeliver(t, s, 1, 2, Applied)
	mustDeliver(t, s, 2, 3, Applied)
	mustDeliver(t, s, 3, 4, Applied)
	mustDeliver(t, s, 4, 1, Dup) // 变更回到来源，f_1[1] 已为 1
	mustDeliver(t, s, 1, 4, Dup) // 另一方向到达时 f_4[1] 已为 1
	for site := 1; site <= 4; site++ {
		rec, ok := s.Get(site, "k")
		if !ok || rec.Val != 5 {
			t.Fatalf("站点 %d 未收敛到 5: %+v", site, rec)
		}
		for o := 1; o <= 4; o++ {
			want := int64(0)
			if o == 1 {
				want = 1
			}
			if got := s.Floor(site, o); got != want {
				t.Fatalf("Floor(%d,%d)=%d, 期望 %d", site, o, got, want)
			}
		}
	}
	t.Logf("判定依据: 5=N+1 次出队、2 次 Dup, 与出队顺序无关")
}

// 例四：断链期间 Deliver 报 ErrDown（先于 ErrEmpty），队列保留；恢复后可继续出队。
func TestExample4_LinkDownBeforeEmpty(t *testing.T) {
	s, _ := New(4, []int{1})
	mustWrite(t, s, 1, "k", change.Put, 5, 1, Applied)
	mustDeliver(t, s, 1, 2, Applied) // 2→3 现有一条积压

	if err := s.SetLink(2, 3, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Deliver(2, 3); !errors.Is(err, ErrDown) {
		t.Fatalf("非空链路断开时应报 ErrDown, 得到 %v", err)
	}
	// 把 2→3 排空不可能（已断），改为验证空链路也报 ErrDown：断开 3→2（空）。
	if err := s.SetLink(3, 2, false); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Deliver(3, 2); !errors.Is(err, ErrDown) {
		t.Fatalf("空链路断开时应报 ErrDown 而非 ErrEmpty, 得到 %v", err)
	}
	if got := len(s.Pending(2, 3)); got != 1 {
		t.Fatalf("断链期间队列应保留, Pending(2,3) 长度=%d", got)
	}
	// 恢复后继续出队。
	if err := s.SetLink(2, 3, true); err != nil {
		t.Fatal(err)
	}
	mustDeliver(t, s, 2, 3, Applied)
	t.Logf("判定依据: ErrDown 先于 ErrEmpty, 断链期间队列保留, 恢复后可继续出队")
}

// 同站同 ts 按 seq 决胜：now 等于 c_s 不算回退，第二条写凭更大的 seq 胜出。
func TestSameSiteSameTsSeqTiebreak(t *testing.T) {
	s, _ := New(3, []int{1})
	mustWrite(t, s, 1, "k", change.Put, 1, 10, Applied) // (10,1,1)
	mustWrite(t, s, 1, "k", change.Put, 2, 10, Applied) // (10,1,2) 同 ts 同 origin, seq 决胜
	rec, _ := s.Get(1, "k")
	if rec.Val != 2 || rec.Triple != (change.Triple{Ts: 10, Origin: 1, Seq: 2}) {
		t.Fatalf("Get(1,k)=%+v, 期望 Put(2) 三元组 (10,1,2)", rec)
	}
	rng := rand.New(rand.NewSource(2))
	dequeued := make(map[changeID]int)
	dups := make(map[changeID]int)
	drainAll(t, s, rng, dequeued, dups)
	checkConverged(t, s, []string{"k"}, 2, dequeued, dups)
	for site := 1; site <= 3; site++ {
		if rec, _ := s.Get(site, "k"); rec.Val != 2 {
			t.Fatalf("站点 %d 的 k=%d, 期望 2（seq 决胜）", site, rec.Val)
		}
	}
	t.Logf("判定依据: 同 ts 同 origin 时 seq 大者胜, 收敛后各站点均为第二条写的值")
}

// Del 以墓碑形式保留三元组；后到的旧 Put（ts 更小）仲裁失败，墓碑不丢。
func TestDelTombstoneVsLateOldPut(t *testing.T) {
	s, _ := New(3, []int{1, 2})
	rng := rand.New(rand.NewSource(3))
	dequeued := make(map[changeID]int)
	dups := make(map[changeID]int)

	mustWrite(t, s, 1, "k", change.Put, 5, 10, Applied)
	drainAll(t, s, rng, dequeued, dups)
	mustWrite(t, s, 1, "k", change.Del, 0, 20, Applied) // 墓碑 (20,1,2)
	drainAll(t, s, rng, dequeued, dups)
	for site := 1; site <= 3; site++ {
		rec, ok := s.Get(site, "k")
		if !ok || rec.Op != change.Del || rec.Triple != (change.Triple{Ts: 20, Origin: 1, Seq: 2}) {
			t.Fatalf("站点 %d 应为 Del 墓碑 (20,1,2), 得到 %+v,%v", site, rec, ok)
		}
	}
	// 后到的旧 Put：ts=15 < 20，本地 Lost 但仍被转发。
	mustWrite(t, s, 2, "k", change.Put, 6, 15, Lost)
	drainAll(t, s, rng, dequeued, dups)
	checkConverged(t, s, []string{"k"}, 3, dequeued, dups)
	for site := 1; site <= 3; site++ {
		rec, ok := s.Get(site, "k")
		if !ok || rec.Op != change.Del {
			t.Fatalf("站点 %d 的墓碑被旧 Put 覆盖: %+v", site, rec)
		}
	}
	t.Logf("判定依据: Del 墓碑带三元组参与比较, 旧 Put 处处 Lost, 各站点保持墓碑")
}

// 权限与时钟拒绝：被拒调用不得改变任何状态。
func TestPermissionAndClockRejection(t *testing.T) {
	s, _ := New(4, []int{1, 3})
	mustWrite(t, s, 1, "k", change.Put, 1, 10, Applied)

	snapshot := func() string {
		var b string
		for site := 1; site <= 4; site++ {
			b += fmt.Sprintf("q%d=%d c1=%d c2=%d c3=%d c4=%d ", site, s.WriteCount(site),
				s.Floor(site, 1), s.Floor(site, 2), s.Floor(site, 3), s.Floor(site, 4))
			rec, ok := s.Get(site, "k")
			b += fmt.Sprintf("rec=%+v,%v ", rec, ok)
		}
		for from := 1; from <= 4; from++ {
			a, bb := neighborsOf(4, from)
			for _, to := range []int{a, bb} {
				b += fmt.Sprintf("p%d>%d=%v ", from, to, s.Pending(from, to))
			}
		}
		return b
	}
	before := snapshot()

	if _, err := s.Write(2, "k", change.Put, 9, 11); !errors.Is(err, ErrNotWritable) {
		t.Fatalf("站点 2 不在 W, 期望 ErrNotWritable, 得到 %v", err)
	}
	if _, err := s.Write(1, "k", change.Put, 9, 9); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("now=9 < c_1=10, 期望 ErrClockRegression, 得到 %v", err)
	}
	if after := snapshot(); after != before {
		t.Fatalf("被拒调用改变了状态\n前: %s\n后: %s", before, after)
	}
	if _, err := s.Write(1, "k", change.Put, 9, 10); err != nil {
		t.Fatalf("now == c_s 不算回退, 应允许, 得到 %v", err)
	}
	if after := snapshot(); after == before {
		t.Fatalf("合法写应当改变状态")
	}
	t.Logf("判定依据: 权限/时钟拒绝不改变状态, now==c_s 合法")
}

// 参数非法：站点越界、站点不相邻、op 非法、键非法、now 越界、构造参数非法。
func TestInvalidParams(t *testing.T) {
	if _, err := New(2, []int{1}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("N=2 应报参数非法, 得到 %v", err)
	}
	if _, err := New(9, []int{1}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("N=9 应报参数非法, 得到 %v", err)
	}
	if _, err := New(3, nil); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("W 为空应报参数非法, 得到 %v", err)
	}
	if _, err := New(3, []int{4}); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("W 含越界站点应报参数非法, 得到 %v", err)
	}

	s, _ := New(4, []int{1})
	key33 := string(make([]byte, 33))
	key32 := string(make([]byte, 32))
	cases := []struct {
		name string
		site int
		key  string
		op   change.Op
		now  int64
	}{
		{"站点0", 0, "k", change.Put, 0},
		{"站点越界", 5, "k", change.Put, 0},
		{"空键", 1, "", change.Put, 0},
		{"键超32字节", 1, key33, change.Put, 0},
		{"op非法", 1, "k", change.Op(7), 0},
		{"now为负", 1, "k", change.Put, -1},
		{"now超界", 1, "k", change.Put, 1_000_000_000_001},
	}
	for _, tc := range cases {
		if _, err := s.Write(tc.site, tc.key, tc.op, 1, tc.now); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("%s: 期望 ErrInvalidParam, 得到 %v", tc.name, err)
		}
		t.Logf("输入 Write(%d,%q,%v,%d) 输出 ErrInvalidParam 判定依据: %s", tc.site, tc.key, tc.op, tc.now, tc.name)
	}
	// 边界合法：32 字节键、now=0 与 now=1e12。
	mustWrite(t, s, 1, key32, change.Put, 1, 0, Applied)
	mustWrite(t, s, 1, "k", change.Put, 1, 1_000_000_000_000, Applied)

	// Deliver/SetLink 的相邻性校验。
	if _, err := s.Deliver(1, 1); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("Deliver(1,1) 应报参数非法, 得到 %v", err)
	}
	if _, err := s.Deliver(1, 3); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("N=4 时 1 与 3 不相邻, 应报参数非法, 得到 %v", err)
	}
	if _, err := s.Deliver(0, 2); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("站点越界应报参数非法, 得到 %v", err)
	}
	if err := s.SetLink(2, 4, true); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("N=4 时 2 与 4 不相邻, 应报参数非法, 得到 %v", err)
	}
	if _, err := s.Deliver(2, 1); !errors.Is(err, ErrEmpty) {
		t.Fatalf("空队列应报 ErrEmpty, 得到 %v", err)
	}
}

// 任意出队顺序下的收敛与 Dup 次数：多种子随机排空。
func TestArbitraryDeliveryOrderConverges(t *testing.T) {
	for seed := int64(0); seed < 20; seed++ {
		rng := rand.New(rand.NewSource(seed))
		s, _ := New(5, []int{1, 3, 5})
		writes := 0
		nows := make(map[int]int64)
		for i := 0; i < 12; i++ {
			site := []int{1, 3, 5}[rng.Intn(3)]
			nows[site] += int64(rng.Intn(5))
			key := []string{"a", "b", "k"}[rng.Intn(3)]
			op := change.Put
			if rng.Intn(4) == 0 {
				op = change.Del
			}
			if _, err := s.Write(site, key, op, int64(i), nows[site]); err != nil {
				t.Fatalf("seed=%d 第 %d 次写失败: %v", seed, i, err)
			}
			writes++
		}
		dequeued := make(map[changeID]int)
		dups := make(map[changeID]int)
		drainAll(t, s, rng, dequeued, dups)
		checkConverged(t, s, []string{"a", "b", "k"}, writes, dequeued, dups)
	}
	t.Logf("判定依据: 20 个种子的随机出队顺序下均收敛, 每条变更恰 N+1 次出队、2 次 Dup")
}

// f 与写计数对齐：多站点交错写并排空后，Floor(s,o) == q_o。
func TestFloorAlignsWithWriteCount(t *testing.T) {
	s, _ := New(4, []int{1, 2, 4})
	rng := rand.New(rand.NewSource(7))
	dequeued := make(map[changeID]int)
	dups := make(map[changeID]int)
	writes := 0
	for i := 0; i < 9; i++ {
		site := []int{1, 2, 4}[i%3]
		mustWrite(t, s, site, "k", change.Put, int64(i), int64(10+i), Applied)
		writes++
		if i%2 == 0 {
			drainAll(t, s, rng, dequeued, dups)
		}
	}
	drainAll(t, s, rng, dequeued, dups)
	checkConverged(t, s, []string{"k"}, writes, dequeued, dups)
	for o := 1; o <= 4; o++ {
		want := s.WriteCount(o)
		for site := 1; site <= 4; site++ {
			if got := s.Floor(site, o); got != want {
				t.Fatalf("Floor(%d,%d)=%d, 期望 %d", site, o, got, want)
			}
		}
	}
	t.Logf("判定依据: 全部站点每个来源的下限等于该来源的写计数")
}

// 并发调用：多 goroutine 同时写与投递，结果等价于某个串行顺序且最终收敛。
func TestConcurrentSafety(t *testing.T) {
	s, _ := New(4, []int{1, 2, 3, 4})
	const perSite = 25
	var writers sync.WaitGroup
	for site := 1; site <= 4; site++ {
		site := site
		writers.Add(1)
		go func() {
			defer writers.Done()
			for i := 1; i <= perSite; i++ {
				if _, err := s.Write(site, "k", change.Put, int64(site*1000+i), int64(i)); err != nil {
					t.Errorf("并发写失败: %v", err)
					return
				}
			}
		}()
	}
	// 投递 goroutine：随机挑链路，忽略 ErrEmpty，写完后停止。
	stop := make(chan struct{})
	var deliverers sync.WaitGroup
	for g := 0; g < 3; g++ {
		deliverers.Add(1)
		go func(seed int64) {
			defer deliverers.Done()
			rng := rand.New(rand.NewSource(seed))
			for {
				select {
				case <-stop:
					return
				default:
				}
				from := 1 + rng.Intn(4)
				a, b := neighborsOf(4, from)
				to := a
				if rng.Intn(2) == 0 {
					to = b
				}
				_, _ = s.Deliver(from, to)
			}
		}(int64(g))
	}
	// 只读查询并发。
	writers.Add(1)
	go func() {
		defer writers.Done()
		for i := 0; i < 200; i++ {
			_, _ = s.Get(1, "k")
			_ = s.Floor(1, 1)
			_ = s.Pending(1, 2)
		}
	}()
	writers.Wait()
	close(stop)
	deliverers.Wait()

	rng := rand.New(rand.NewSource(8))
	dequeued := make(map[changeID]int)
	dups := make(map[changeID]int)
	drainAll(t, s, rng, dequeued, dups)
	// 并发阶段已有部分投递发生，此处只校验键表收敛与 f 对齐；
	// 每条变更恰 N+1 次出队、2 次 Dup 的精确计数由单线程用例保证。
	rec0, ok0 := s.Get(1, "k")
	if !ok0 {
		t.Fatalf("收敛后站点 1 应有 k 的记录")
	}
	for site := 2; site <= 4; site++ {
		rec, ok := s.Get(site, "k")
		if ok != ok0 || rec != rec0 {
			t.Fatalf("并发后站点 %d 与站点 1 不一致: %+v,%v vs %+v,%v", site, rec, ok, rec0, ok0)
		}
	}
	for site := 1; site <= 4; site++ {
		for o := 1; o <= 4; o++ {
			if got, want := s.Floor(site, o), s.WriteCount(o); got != want {
				t.Fatalf("Floor(%d,%d)=%d, 期望 %d", site, o, got, want)
			}
		}
	}
	t.Logf("判定依据: 并发下无竞态（配合 -race）, 键表收敛且 f 与写计数对齐")
}

// 相同操作序列重放得到相同结果。
func TestReplayDeterminism(t *testing.T) {
	run := func() ([]string, map[string]Record, map[[2]int]int64) {
		s, _ := New(5, []int{1, 2, 4})
		rng := rand.New(rand.NewSource(42))
		var log []string
		nows := make(map[int]int64)
		for i := 0; i < 60; i++ {
			switch rng.Intn(3) {
			case 0:
				site := []int{1, 2, 4}[rng.Intn(3)]
				nows[site] += int64(rng.Intn(3))
				res, err := s.Write(site, "k", change.Put, int64(i), nows[site])
				log = append(log, fmt.Sprintf("W %v %v", res, err))
			case 1:
				from := 1 + rng.Intn(5)
				a, b := neighborsOf(5, from)
				to := []int{a, b}[rng.Intn(2)]
				res, err := s.Deliver(from, to)
				log = append(log, fmt.Sprintf("D %v %v", res, err))
			case 2:
				from := 1 + rng.Intn(5)
				a, _ := neighborsOf(5, from)
				err := s.SetLink(from, a, rng.Intn(2) == 0)
				log = append(log, fmt.Sprintf("L %v", err))
			}
		}
		tbl := make(map[string]Record)
		for site := 1; site <= 5; site++ {
			rec, ok := s.Get(site, "k")
			if ok {
				tbl[fmt.Sprint(site)] = rec
			}
		}
		fl := make(map[[2]int]int64)
		for site := 1; site <= 5; site++ {
			for o := 1; o <= 5; o++ {
				fl[[2]int{site, o}] = s.Floor(site, o)
			}
		}
		return log, tbl, fl
	}
	log1, tbl1, fl1 := run()
	log2, tbl2, fl2 := run()
	if !reflect.DeepEqual(log1, log2) || !reflect.DeepEqual(tbl1, tbl2) || !reflect.DeepEqual(fl1, fl2) {
		t.Fatalf("相同操作序列重放结果不一致")
	}
	t.Logf("判定依据: 两次重放的逐条结果、键表与下限表完全一致")
}
