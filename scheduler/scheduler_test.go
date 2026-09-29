package scheduler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func mustNew(t *testing.T, nodes []Node) *Scheduler {
	t.Helper()
	var buf bytes.Buffer
	s, err := New(nodes, WithLogger(log.New(&buf, "[test] ", log.LstdFlags)))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if t.Failed() && buf.Len() > 0 {
			t.Logf("scheduler log:\n%s", buf.String())
		}
	})
	return s
}

func zoneNodes() []Node {
	return []Node{
		{ID: "n-a1", Zone: "za", Slots: 2, Labels: map[string]string{"role": "store"}},
		{ID: "n-a2", Zone: "za", Slots: 2, Labels: map[string]string{"role": "store"}},
		{ID: "n-b1", Zone: "zb", Slots: 1, Labels: map[string]string{"role": "store"}},
		{ID: "n-c1", Zone: "zc", Slots: 2, Labels: map[string]string{"role": "other"}},
	}
}

func sched(t *testing.T, s *Scheduler, g, r string) Replica {
	t.Helper()
	got, err := s.Schedule(g, r)
	if err != nil {
		t.Fatalf("Schedule(%s,%s): %v", g, r, err)
	}
	return got
}

func bindOK(t *testing.T, s *Scheduler, g, r string) {
	t.Helper()
	if err := <-s.Bind(context.Background(), g, r, nil); err != nil {
		t.Fatalf("Bind(%s,%s): %v", g, r, err)
	}
}

func maxDiff(m map[string]int) int {
	mn, mx, first := 0, 0, true
	for _, v := range m {
		if first || v < mn {
			mn = v
		}
		if first || v > mx {
			mx = v
		}
		first = false
	}
	return mx - mn
}

// 基本打散：S=1 时副本在合格区之间轮转，区差不超过 1；
// 无匹配节点的区（zc）不参与最小值与放置。
func TestScheduleBalancesAcrossZones(t *testing.T) {
	s := mustNew(t, zoneNodes())
	if err := s.AddGroup(GroupSpec{ID: "g", Skew: 1, Needs: map[string]string{"role": "store"}}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		sched(t, s, "g", fmt.Sprintf("r%d", i))
	}
	st, _ := s.StatsOf("g")
	if st.ZoneCount["za"] != 2 || st.ZoneCount["zb"] != 1 {
		t.Fatalf("counts = %+v, want za=2 zb=1", st.ZoneCount)
	}
	if _, ok := st.ZoneCount["zc"]; ok {
		t.Fatalf("zc must not participate: %+v", st.ZoneCount)
	}
	// 第 4 个：zb 满且计数最小(1)，放 za 会使差=2 > S=1 => 不可调度。
	if _, err := s.Schedule("g", "r3"); !errors.Is(err, ErrUnschedulable) {
		t.Fatalf("want ErrUnschedulable, got %v", err)
	}
	st2, _ := s.StatsOf("g")
	if st2.NodeUsed["n-a1"]+st2.NodeUsed["n-a2"] != 2 || st2.NodeUsed["n-b1"] != 1 {
		t.Fatalf("state changed after rejected schedule: %+v", st2.NodeUsed)
	}
}

// 已满但计数最小的合格区阻止其他区放置；S 放宽后其他区可放置。
func TestFullMinZoneBlocksOthers(t *testing.T) {
	s := mustNew(t, zoneNodes())
	if err := s.AddGroup(GroupSpec{ID: "g", Skew: 1, Needs: map[string]string{"role": "store"}}); err != nil {
		t.Fatal(err)
	}
	sched(t, s, "g", "r0")
	sched(t, s, "g", "r1") // zb 满
	sched(t, s, "g", "r2")
	// za=2、zb=1（满，最小），再放 za 差=2 > 1：满的最小区阻止其他区。
	if _, err := s.Schedule("g", "r3"); !errors.Is(err, ErrUnschedulable) {
		t.Fatalf("want ErrUnschedulable, got %v", err)
	}
	// S=2 的独立场景：za 4 槽、zb 1 槽。满的最小区(zb)不阻止其他区，
	// za 可继续放到差=2，之后所有允许区都满。
	s2 := mustNew(t, []Node{
		{ID: "a1", Zone: "za", Slots: 4, Labels: map[string]string{"role": "store"}},
		{ID: "b1", Zone: "zb", Slots: 1, Labels: map[string]string{"role": "store"}},
	})
	if err := s2.AddGroup(GroupSpec{ID: "g2", Skew: 2, Needs: map[string]string{"role": "store"}}); err != nil {
		t.Fatal(err)
	}
	sched(t, s2, "g2", "r0") // za
	sched(t, s2, "g2", "r1") // zb，满
	sched(t, s2, "g2", "r2") // za=2
	sched(t, s2, "g2", "r3") // za=3，差 2，允许
	st, _ := s2.StatsOf("g2")
	if st.ZoneCount["za"] != 3 || st.ZoneCount["zb"] != 1 {
		t.Fatalf("g2 counts = %+v, want za=3 zb=1", st.ZoneCount)
	}
	if _, err := s2.Schedule("g2", "r4"); !errors.Is(err, ErrUnschedulable) {
		t.Fatalf("want ErrUnschedulable when full, got %v", err)
	}
}

// 区内取剩余槽位最多的节点，并列按标识升序。
func TestNodePickMostFreeThenID(t *testing.T) {
	nodes := []Node{
		{ID: "n1", Zone: "z", Slots: 3, Labels: map[string]string{"k": "v"}},
		{ID: "n2", Zone: "z", Slots: 5, Labels: map[string]string{"k": "v"}},
	}
	s := mustNew(t, nodes)
	if err := s.AddGroup(GroupSpec{ID: "g", Skew: 1, Needs: map[string]string{"k": "v"}}); err != nil {
		t.Fatal(err)
	}
	r := sched(t, s, "g", "r0")
	if r.Node != "n2" {
		t.Fatalf("first pick = %s, want n2 (more free slots)", r.Node)
	}
	bindOK(t, s, "g", "r0")
	// n2 余 4，n1 余 3 -> 仍取 n2。
	r1 := sched(t, s, "g", "r1")
	if r1.Node != "n2" {
		t.Fatalf("second pick = %s, want n2", r1.Node)
	}
}

// 绑定失败后计数与槽位回退，且释放后的预留不能再绑定。
func TestBindFailureRollsBack(t *testing.T) {
	s := mustNew(t, zoneNodes())
	if err := s.AddGroup(GroupSpec{ID: "g", Skew: 1, Needs: map[string]string{"role": "store"}}); err != nil {
		t.Fatal(err)
	}
	r := sched(t, s, "g", "r0")
	injected := errors.New("bind boom")
	if err := <-s.Bind(context.Background(), "g", "r0", func(context.Context, string, string, string) error {
		return injected
	}); !errors.Is(err, injected) {
		t.Fatalf("bind err = %v, want %v", err, injected)
	}
	st, _ := s.StatsOf("g")
	if st.ZoneCount[r.Zone] != 0 || st.NodeUsed[r.Node] != 0 {
		t.Fatalf("rollback failed: zone=%+v node=%+v", st.ZoneCount, st.NodeUsed)
	}
	r2 := sched(t, s, "g", "r1")
	if r2.Node != r.Node || r2.Zone != r.Zone {
		t.Fatalf("replacement = %+v, want same as %+v", r2, r)
	}
	if err := <-s.Bind(context.Background(), "g", "r0", nil); !errors.Is(err, ErrReservationReleased) {
		t.Fatalf("rebind released = %v, want ErrReservationReleased", err)
	}
}

// 已绑定预留重复绑定、绑定中的预留再绑定都被区分拒绝。
func TestRebindRejected(t *testing.T) {
	s := mustNew(t, zoneNodes())
	if err := s.AddGroup(GroupSpec{ID: "g", Skew: 1, Needs: map[string]string{"role": "store"}}); err != nil {
		t.Fatal(err)
	}
	sched(t, s, "g", "r0")
	releaseBind := make(chan struct{})
	gotErr := make(chan error, 1)
	go func() {
		gotErr <- <-s.Bind(context.Background(), "g", "r0", func(context.Context, string, string, string) error {
			<-releaseBind
			return nil
		})
	}()
	// 等待进入 binding 状态。
	for i := 0; i < 100; i++ {
		time.Sleep(time.Millisecond)
		err := <-s.Bind(context.Background(), "g", "r0", nil)
		if errors.Is(err, ErrBindingInFlight) {
			break
		}
		if i == 99 {
			t.Fatalf("want ErrBindingInFlight, got %v", err)
		}
	}
	close(releaseBind)
	if err := <-gotErr; err != nil {
		t.Fatalf("bind: %v", err)
	}
	if err := <-s.Bind(context.Background(), "g", "r0", nil); !errors.Is(err, ErrReservationBound) {
		t.Fatalf("rebind bound = %v, want ErrReservationBound", err)
	}
}

// 删除只减计数，不触发重新平衡。
func TestDeleteNoRebalance(t *testing.T) {
	s := mustNew(t, zoneNodes())
	if err := s.AddGroup(GroupSpec{ID: "g", Skew: 1, Needs: map[string]string{"role": "store"}}); err != nil {
		t.Fatal(err)
	}
	sched(t, s, "g", "r0")
	sched(t, s, "g", "r1")
	bindOK(t, s, "g", "r0")
	bindOK(t, s, "g", "r1")
	if err := s.Delete("g", "r1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	st, _ := s.StatsOf("g")
	if st.ZoneCount["zb"] != 0 || st.ZoneCount["za"] != 1 {
		t.Fatalf("after delete counts = %+v", st.ZoneCount)
	}
	next := sched(t, s, "g", "r2")
	if next.Zone != "zb" {
		t.Fatalf("next zone = %s, want zb (no rebalance)", next.Zone)
	}
	if err := s.Delete("g", "ghost"); !errors.Is(err, ErrUnknownReplica) {
		t.Fatalf("delete ghost = %v, want ErrUnknownReplica", err)
	}
}

// 并发调度等价于某个串行顺序：不超容量、区差<=S、成功集合与串行重放逐副本相同。
func TestConcurrentScheduleSerializable(t *testing.T) {
	for iter := 0; iter < 50; iter++ {
		s := mustNew(t, zoneNodes())
		if err := s.AddGroup(GroupSpec{ID: "g", Skew: 1, Needs: map[string]string{"role": "store"}}); err != nil {
			t.Fatal(err)
		}
		const n = 12
		var wg sync.WaitGroup
		var mu sync.Mutex
		placed := make(map[string]Replica)
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				r, err := s.Schedule("g", fmt.Sprintf("r%d", i))
				if err != nil {
					return
				}
				mu.Lock()
				placed[r.ID] = r
				mu.Unlock()
			}(i)
		}
		close(start)
		wg.Wait()
		st, _ := s.StatsOf("g")
		nodeCap := map[string]int{"n-a1": 2, "n-a2": 2, "n-b1": 1, "n-c1": 2}
		for id, used := range st.NodeUsed {
			if used > nodeCap[id] {
				t.Fatalf("iter %d node %s used %d > cap %d", iter, id, used, nodeCap[id])
			}
		}
		counts := map[string]int{}
		nodeCounts := map[string]int{}
		for _, r := range placed {
			counts[r.Zone]++
			nodeCounts[r.Node]++
		}
		for z, c := range counts {
			if st.ZoneCount[z] != c {
				t.Fatalf("iter %d zone %s stats=%d placed=%d", iter, z, st.ZoneCount[z], c)
			}
		}
		for id, c := range nodeCounts {
			if st.NodeUsed[id] != c {
				t.Fatalf("iter %d node %s stats=%d placed=%d", iter, id, st.NodeUsed[id], c)
			}
		}
		if maxDiff(counts) > 1 {
			t.Fatalf("iter %d skew violated: %+v", iter, counts)
		}

		ids := make([]string, 0, len(placed))
		for id := range placed {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		// 并发结果必须等价于某个串行顺序：穷举成功集合排列，
		// 至少有一个排列重放出逐副本相同的放置。
		found := false
		var tryOrder func(int)
		tryOrder = func(k int) {
			if found {
				return
			}
			if k == len(ids) {
				s2 := mustNew(t, zoneNodes())
				if err := s2.AddGroup(GroupSpec{ID: "g", Skew: 1, Needs: map[string]string{"role": "store"}}); err != nil {
					t.Fatal(err)
				}
				for _, id := range ids {
					r, err := s2.Schedule("g", id)
					if err != nil || r != placed[id] {
						return
					}
				}
				found = true
				return
			}
			for j := k; j < len(ids); j++ {
				ids[k], ids[j] = ids[j], ids[k]
				tryOrder(k + 1)
				ids[k], ids[j] = ids[j], ids[k]
			}
		}
		tryOrder(0)
		if !found {
			t.Fatalf("iter %d no serial order reproduces placements %+v", iter, placed)
		}
	}
}

// 并发调度+绑定（注入部分失败）：不超容量；只看绑定成功者区差<=S。
func TestConcurrentScheduleAndBind(t *testing.T) {
	s := mustNew(t, zoneNodes())
	if err := s.AddGroup(GroupSpec{ID: "g", Skew: 1, Needs: map[string]string{"role": "store"}}); err != nil {
		t.Fatal(err)
	}
	const n = 10
	var wg sync.WaitGroup
	start := make(chan struct{})
	bound := make(chan Replica, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			r, err := s.Schedule("g", fmt.Sprintf("r%d", i))
			if err != nil {
				return
			}
			fail := i%3 == 0
			berr := <-s.Bind(context.Background(), "g", r.ID, func(context.Context, string, string, string) error {
				if fail {
					return errors.New("injected")
				}
				return nil
			})
			if berr == nil {
				bound <- r
			}
		}(i)
	}
	close(start)
	wg.Wait()
	close(bound)

	st, _ := s.StatsOf("g")
	nodeCap := map[string]int{"n-a1": 2, "n-a2": 2, "n-b1": 1, "n-c1": 2}
	for id, used := range st.NodeUsed {
		if used > nodeCap[id] {
			t.Fatalf("node %s used %d > cap %d", id, used, nodeCap[id])
		}
	}
	boundCounts := map[string]int{}
	for r := range bound {
		boundCounts[r.Zone]++
	}
	// 调用方收到绑定结果与内部状态提交之间存在先发生但未完全可见的窗口，
	// 轮询等待统计与绑定成功集合一致。
	st, _ = s.StatsOf("g")
	for i := 0; i < 200 && !zoneStatsMatch(st.ZoneCount, boundCounts); i++ {
		time.Sleep(time.Millisecond)
		st, _ = s.StatsOf("g")
	}
	if !zoneStatsMatch(st.ZoneCount, boundCounts) {
		t.Fatalf("zone stats %+v vs bound %+v", st.ZoneCount, boundCounts)
	}
	// 注入故障后只保留绑定成功者；偏斜保证适用于“只含放置且绑定全成功”的序列，
	// 该性质在 TestConcurrentScheduleAllBound 中验证。
}

func zoneStatsMatch(stats, want map[string]int) bool {
	for z, c := range want {
		if stats[z] != c {
			return false
		}
	}
	for z, c := range stats {
		if c != 0 && want[z] != c {
			return false
		}
	}
	return true
}

// 并发调度且绑定全部成功：不超容量，最终合格区计数差恒不超过 S。
func TestConcurrentScheduleAllBound(t *testing.T) {
	for iter := 0; iter < 30; iter++ {
		s := mustNew(t, zoneNodes())
		if err := s.AddGroup(GroupSpec{ID: "g", Skew: 1, Needs: map[string]string{"role": "store"}}); err != nil {
			t.Fatal(err)
		}
		const n = 6
		var wg sync.WaitGroup
		start := make(chan struct{})
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start
				if _, err := s.Schedule("g", fmt.Sprintf("r%d", i)); err != nil {
					return
				}
				if err := <-s.Bind(context.Background(), "g", fmt.Sprintf("r%d", i), nil); err != nil {
					t.Errorf("iter %d bind: %v", iter, err)
				}
			}(i)
		}
		close(start)
		wg.Wait()
		st, _ := s.StatsOf("g")
		nodeCap := map[string]int{"n-a1": 2, "n-a2": 2, "n-b1": 1, "n-c1": 2}
		for id, used := range st.NodeUsed {
			if used > nodeCap[id] {
				t.Fatalf("iter %d node %s over capacity: %d", iter, id, used)
			}
		}
		if maxDiff(st.ZoneCount) > 1 {
			t.Fatalf("iter %d bound skew violated: %+v", iter, st.ZoneCount)
		}
	}
}

// 所有整体拒绝原因可区分，且拒绝不改变计数与槽位。
func TestValidationErrorsDistinct(t *testing.T) {
	if _, err := New([]Node{{ID: "n", Zone: "z", Slots: 0}}); !errors.Is(err, ErrInvalidSlots) {
		t.Fatalf("slots: %v", err)
	}
	if _, err := New([]Node{{ID: "n", Zone: "", Slots: 1}}); !errors.Is(err, ErrEmptyZone) {
		t.Fatalf("zone: %v", err)
	}
	if _, err := New([]Node{
		{ID: "n", Zone: "z", Slots: 1},
		{ID: "n", Zone: "z2", Slots: 1},
	}); !errors.Is(err, ErrDuplicateNode) {
		t.Fatalf("dup node: %v", err)
	}
	s := mustNew(t, zoneNodes())
	if err := s.AddGroup(GroupSpec{ID: "bad", Skew: 0}); !errors.Is(err, ErrInvalidSkew) {
		t.Fatalf("skew: %v", err)
	}
	if err := s.AddGroup(GroupSpec{ID: "bad", Skew: 1, Needs: map[string]string{"role": "nope"}}); !errors.Is(err, ErrNoMatchingNode) {
		t.Fatalf("no match: %v", err)
	}
	if _, err := s.Schedule("nogroup", "r"); !errors.Is(err, ErrUnknownGroup) {
		t.Fatalf("unknown group: %v", err)
	}
	if err := s.AddGroup(GroupSpec{ID: "g", Skew: 1, Needs: map[string]string{"role": "store"}}); err != nil {
		t.Fatal(err)
	}
	sched(t, s, "g", "r0")
	if _, err := s.Schedule("g", "r0"); !errors.Is(err, ErrDuplicateReplica) {
		t.Fatalf("dup replica: %v", err)
	}
	if err := s.Delete("g", "missing"); !errors.Is(err, ErrUnknownReplica) {
		t.Fatalf("delete missing: %v", err)
	}
	st, _ := s.StatsOf("g")
	if st.ZoneCount["za"] != 1 {
		t.Fatalf("rejections changed state: %+v", st.ZoneCount)
	}
}

// 日志打印输入、输出与判定依据。
func TestLoggerObservesDecisions(t *testing.T) {
	var buf bytes.Buffer
	s, err := New(zoneNodes(), WithLogger(log.New(&buf, "", 0)))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddGroup(GroupSpec{ID: "g", Skew: 1, Needs: map[string]string{"role": "store"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Schedule("g", "r0"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Schedule("g", "r1"); err != nil {
		t.Fatal(err)
	}
	logText := buf.String()
	for _, want := range []string{"input Schedule", "judge zone=", "output=", "eligible-zones=", "min-count="} {
		if !strings.Contains(logText, want) {
			t.Fatalf("log missing %q:\n%s", want, logText)
		}
	}
}

// 相同的节点、操作与故障序列，两次重放放置必须相同。
func TestDeterministicReplay(t *testing.T) {
	run := func() map[string]Replica {
		s := mustNew(t, zoneNodes())
		if err := s.AddGroup(GroupSpec{ID: "g", Skew: 1, Needs: map[string]string{"role": "store"}}); err != nil {
			t.Fatal(err)
		}
		out := make(map[string]Replica)
		for i := 0; i < 8; i++ {
			id := fmt.Sprintf("r%d", i)
			r, err := s.Schedule("g", id)
			if err != nil {
				continue
			}
			bindErr := <-s.Bind(context.Background(), "g", id, func(context.Context, string, string, string) error {
				if i%2 == 0 {
					return errors.New("deterministic fault")
				}
				return nil
			})
			if bindErr == nil {
				out[id] = r
			}
		}
		// 失败全部回退后再放置，节点选择也必须可重现。
		for i := 100; i < 103; i++ {
			id := fmt.Sprintf("r%d", i)
			if r, err := s.Schedule("g", id); err == nil {
				out[id] = r
			}
		}
		return out
	}
	first := run()
	second := run()
	if len(first) != len(second) {
		t.Fatalf("placed count differs: %d vs %d", len(first), len(second))
	}
	for id, r := range first {
		if r != second[id] {
			t.Fatalf("replay differs for %s: %+v vs %+v", id, r, second[id])
		}
	}
}
