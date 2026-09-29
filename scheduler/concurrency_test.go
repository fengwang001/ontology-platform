package scheduler

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
)

type scheduleResult struct {
	id  string
	p   Placement
	err Reason
}

func balancedNodes() []Node {
	return []Node{
		{ID: "n-a", Zone: "a", Slots: 3},
		{ID: "n-b", Zone: "b", Slots: 3},
		{ID: "n-c", Zone: "c", Slots: 3},
		{ID: "n-d", Zone: "d", Slots: 3},
	}
}

func runConcurrent(t *testing.T, ids []string) map[string]scheduleResult {
	t.Helper()
	s := newTestScheduler(t, Config{}, balancedNodes())
	if err := s.AddGroup(Group{ID: "g", Skew: 1}); err != nil {
		t.Fatal(err)
	}
	results := make([]scheduleResult, len(ids))
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			<-start
			p, err := s.Schedule("g", id)
			results[i] = scheduleResult{id: id, p: p, err: ErrReason(err)}
		}(i, id)
	}
	close(start)
	wg.Wait()

	out := make(map[string]scheduleResult, len(ids))
	nodeUsed := map[string]int{}
	for _, r := range results {
		out[r.id] = r
		if r.err != "" {
			if r.err != ReasonNoNodeAvailable {
				t.Fatalf("unexpected failure reason for %s: %s", r.id, r.err)
			}
			continue
		}
		nodeUsed[r.p.NodeID]++
	}
	// 任何节点占用不超过容量。
	for id, used := range nodeUsed {
		if used > 3 {
			t.Fatalf("node %s over capacity: %d", id, used)
		}
		if observed, _ := s.UsedSlots(id); observed != used {
			t.Fatalf("node %s used = %d, result set says %d", id, observed, used)
		}
	}
	// 合格区计数差恒不超过 S。
	counts, _ := s.ZoneCounts("g")
	min, max := 1<<30, 0
	for _, c := range counts {
		if c < min {
			min = c
		}
		if c > max {
			max = c
		}
	}
	if max-min > 1 {
		t.Fatalf("zone counts = %v, diff %d > skew 1", counts, max-min)
	}
	return out
}

func runSerial(ids []string) map[string]scheduleResult {
	s, _ := New(Config{}, balancedNodes())
	_ = s.AddGroup(Group{ID: "g", Skew: 1})
	out := make(map[string]scheduleResult, len(ids))
	for _, id := range ids {
		p, err := s.Schedule("g", id)
		out[id] = scheduleResult{id: id, p: p, err: ErrReason(err)}
	}
	return out
}

func summarize(results map[string]scheduleResult) (map[string]int, map[string]int, int) {
	zoneCount := map[string]int{}
	nodeCount := map[string]int{}
	failed := 0
	for _, r := range results {
		if r.err != "" {
			failed++
			continue
		}
		zoneCount[r.p.Zone]++
		nodeCount[r.p.NodeID]++
	}
	return zoneCount, nodeCount, failed
}

func TestConcurrentScheduleSerializable(t *testing.T) {
	const n = 30 // 总容量 12，大部分请求应失败。
	ids := make([]string, n)
	for i := range ids {
		ids[i] = fmt.Sprintf("r%02d", i)
	}

	// 并发历史必须等价于某个串行顺序：副本标识只影响平局，不影响计数轮廓。
	concurrent := runConcurrent(t, ids)
	serial := runSerial(ids)

	cz, cn, cf := summarize(concurrent)
	sz, sn, sf := summarize(serial)
	if cf != sf {
		t.Fatalf("failure count concurrent=%d serial=%d", cf, sf)
	}
	for z, c := range sz {
		if cz[z] != c {
			t.Fatalf("zone %s count concurrent=%d serial=%d", z, cz[z], c)
		}
	}
	for node, c := range sn {
		if cn[node] != c {
			t.Fatalf("node %s count concurrent=%d serial=%d", node, cn[node], c)
		}
	}
	if cf != n-12 {
		t.Fatalf("failures = %d, want %d", cf, n-12)
	}
}

func TestDeterministicReplayWithFailures(t *testing.T) {
	// 相同的节点、操作与故障序列重放，放置结果完全一致。
	failEven := func(groupID, replicaID, nodeID string) error {
		if replicaID[len(replicaID)-1]%2 == 0 {
			return errors.New("injected")
		}
		return nil
	}
	run := func() map[string]Placement {
		s, _ := New(Config{Binder: failEven}, balancedNodes())
		_ = s.AddGroup(Group{ID: "g", Skew: 1})
		out := map[string]Placement{}
		for i := 0; i < 16; i++ {
			id := fmt.Sprintf("r%02d", i)
			p, err := s.Schedule("g", id)
			if err == nil {
				out[id] = p
			}
			_ = s.Bind("g", id)
			// 失败预留释放后立刻按固定顺序重新调度一个替补副本。
			if err != nil {
				if _, rerr := s.Schedule("g", "retry-"+id); rerr == nil {
					_ = s.Bind("g", "retry-"+id)
				}
			}
		}
		return out
	}
	first := run()
	second := run()
	if len(first) != len(second) {
		t.Fatalf("placement count differs: %d vs %d", len(first), len(second))
	}
	for id, p := range first {
		if second[id] != p {
			t.Fatalf("placement of %s differs on replay: %+v vs %+v", id, p, second[id])
		}
	}
}

func TestConcurrentBindsFailuresAndCapacity(t *testing.T) {
	nodes := balancedNodes()
	s := newTestScheduler(t, Config{}, nodes)
	if err := s.AddGroup(Group{ID: "g", Skew: 1}); err != nil {
		t.Fatal(err)
	}
	// 先占满 12 个槽位。
	for i := 0; i < 12; i++ {
		mustSchedule(t, s, "g", fmt.Sprintf("r%02d", i))
	}

	release := make(chan struct{})
	var started sync.WaitGroup
	binder := func(groupID, replicaID, nodeID string) error {
		// 初始 12 个偶数编号副本绑定失败，且所有初始绑定在栅栏处并发释放。
		if len(replicaID) == 3 && replicaID[0] == 'r' {
			started.Done()
			<-release
			if replicaID[len(replicaID)-1]%2 == 0 {
				return errors.New("injected bind failure")
			}
		}
		return nil
	}
	s.impl.binder = binder

	var wg sync.WaitGroup
	errs := make([]error, 12)
	started.Add(12)
	for i := 0; i < 12; i++ {
		id := fmt.Sprintf("r%02d", i)
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			errs[i] = s.Bind("g", id)
		}(i, id)
	}
	started.Wait()
	close(release)
	wg.Wait()

	bound := 0
	for _, err := range errs {
		if err == nil {
			bound++
		}
	}
	// 失败预留必须全部回退，且不影响任何节点容量约束。
	totalUsed := 0
	for _, n := range nodes {
		used, _ := s.UsedSlots(n.ID)
		if used > n.Slots {
			t.Fatalf("node %s over capacity: %d/%d", n.ID, used, n.Slots)
		}
		totalUsed += used
	}
	if totalUsed != bound {
		t.Fatalf("total used = %d, bound = %d", totalUsed, bound)
	}
	counts, _ := s.ZoneCounts("g")
	for _, c := range counts {
		if c > 3 {
			t.Fatalf("zone count %d exceeds per-zone capacity", c)
		}
	}
	// 回退出的槽位可以再次被调度并成功绑定。
	for _, n := range nodes {
		used, _ := s.UsedSlots(n.ID)
		for k := used; k < n.Slots; k++ {
			id := fmt.Sprintf("refill-%s-%d", n.ID, k)
			p, err := s.Schedule("g", id)
			if err != nil {
				t.Fatalf("refill schedule %s: %v", id, err)
			}
			if err := s.Bind("g", id); err != nil {
				t.Fatalf("refill bind %s on %s: %v", id, p.NodeID, err)
			}
		}
	}
}

func TestLogsContainInputsOutputsAndBasis(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	s := newTestScheduler(t, Config{
		Logger: logger,
		Binder: func(string, string, string) error { return errors.New("boom") },
	}, []Node{{ID: "n-a", Zone: "a", Slots: 2}})
	if err := s.AddGroup(Group{ID: "g", Skew: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Schedule("g", "r1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Bind("g", "r1"); err == nil {
		t.Fatal("want bind failure")
	}
	logs := buf.String()
	for _, want := range []string{
		`msg="schedule reserved"`,
		"output=",
		"input_group=",
		"input_replica=",
		"basis_zone_count=",
		"basis_min_count=",
		"basis_skew=",
		"basis_node=",
		`msg="bind failed and reservation released"`,
	} {
		if !bytes.Contains(buf.Bytes(), []byte(want)) {
			t.Fatalf("log missing %q\n--- logs ---\n%s", want, logs)
		}
	}
}
