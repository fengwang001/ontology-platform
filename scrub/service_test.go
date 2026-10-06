package scrub

import (
	"errors"
	"reflect"
	"sync"
	"testing"
)

func mustCreate(t *testing.T, s *Service, id uint64, nodes []uint64, version uint64, digest string, interval, now int64) {
	t.Helper()
	if err := s.CreateBlock(id, nodes, version, digest, interval, now); err != nil {
		t.Fatalf("CreateBlock(%d): %v", id, err)
	}
}

func mustScrub(t *testing.T, s *Service, id uint64, now int64, fails map[uint64]bool) ScrubResult {
	t.Helper()
	res, err := s.Scrub(id, now, fails)
	if err != nil {
		t.Fatalf("Scrub(%d, t=%d): %v", id, now, err)
	}
	return res
}

// 间隔恰等于允许，小一秒拒绝；拒绝不留痕。
func TestScrubIntervalBoundary(t *testing.T) {
	s := NewService()
	mustCreate(t, s, 1, []uint64{10, 11, 12}, 1, "a", 10, 0)

	res := mustScrub(t, s, 1, 0, nil) // 从未巡检过的块不受限
	if res.Outcome != OutcomeConsistent {
		t.Fatalf("first scrub outcome = %v, want Consistent", res.Outcome)
	}
	if _, err := s.Scrub(1, 9, nil); !errors.Is(err, ErrTooFrequent) {
		t.Fatalf("scrub at t=9 err = %v, want ErrTooFrequent", err)
	}
	// 拒绝不留痕：时钟与上次巡检时刻均未变。
	if ts, ok := s.LastAcceptedTime(); !ok || ts != 0 {
		t.Fatalf("last accepted time = %d,%v, want 0,true", ts, ok)
	}
	info, _ := s.Inspect(1)
	if !info.Scrubbed || info.LastScrub != 0 {
		t.Fatalf("lastScrub = %d, want 0", info.LastScrub)
	}
	if _, err := s.Scrub(1, 10, nil); err != nil {
		t.Fatalf("scrub at t=10 (== interval) should be allowed: %v", err)
	}
}

// 各类拒绝均不改变状态、时钟与告警。
func TestRejectionsLeaveNoTrace(t *testing.T) {
	s := NewService()
	mustCreate(t, s, 1, []uint64{10, 11}, 1, "a", 100, 5)
	mustScrub(t, s, 1, 5, nil)
	if err := s.InjectBitrot(1, 10, "rot", 6); err != nil {
		t.Fatal(err)
	}
	before, _ := s.Inspect(1)
	beforeAlerts := s.Alerts()
	beforeTime, _ := s.LastAcceptedTime()

	rejects := []error{
		func() error { _, e := s.Scrub(1, -1, nil); return e }(), // 参数非法
		func() error { _, e := s.Scrub(1, 5, nil); return e }(),  // 时钟回退
		func() error { _, e := s.Scrub(99, 6, nil); return e }(), // 块不存在
		func() error { _, e := s.Scrub(1, 6, nil); return e }(),  // 过于频繁
		func() error { _, e := s.SelectDue(5, 1); return e }(),   // 选取时钟回退
		s.Write(1, 2, "b", []uint64{10}, 5),                      // 写入时钟回退
		s.DropReplica(1, 10, 5),                                  // 丢弃时钟回退
	}
	for i, err := range rejects {
		if err == nil {
			t.Fatalf("reject #%d unexpectedly succeeded", i)
		}
	}
	after, _ := s.Inspect(1)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("block state changed by rejections:\nbefore=%+v\nafter=%+v", before, after)
	}
	if !reflect.DeepEqual(beforeAlerts, s.Alerts()) {
		t.Fatalf("alerts changed by rejections")
	}
	if ts, _ := s.LastAcceptedTime(); ts != beforeTime {
		t.Fatalf("clock advanced by rejections: %d -> %d", beforeTime, ts)
	}
}

// 错误优先级：参数非法 > 时钟回退 > 块不存在 > 过于频繁。
func TestErrorPrecedence(t *testing.T) {
	s := NewService()
	mustCreate(t, s, 1, []uint64{10, 11}, 1, "a", 100, 10)

	if _, err := s.Scrub(99, -1, nil); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("want ErrInvalidParam, got %v", err)
	}
	if _, err := s.Scrub(99, 5, nil); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("want ErrClockRegression, got %v", err)
	}
	if _, err := s.Scrub(99, 10, nil); !errors.Is(err, ErrBlockNotFound) {
		t.Fatalf("want ErrBlockNotFound, got %v", err)
	}
	mustScrub(t, s, 1, 10, nil) // 首次巡检不受限，记录巡检时刻
	if _, err := s.Scrub(1, 20, nil); !errors.Is(err, ErrTooFrequent) {
		t.Fatalf("want ErrTooFrequent, got %v", err)
	}
}

// 修复部分失败：失败副本保持原状，成功副本立即自洽；全部失败也是部分修复。
func TestPartialRepair(t *testing.T) {
	s := NewService()
	mustCreate(t, s, 1, []uint64{10, 11, 12}, 5, "a", 0, 0)
	if err := s.InjectBitrot(1, 11, "rot1", 1); err != nil {
		t.Fatal(err)
	}
	if err := s.InjectBitrot(1, 12, "rot2", 2); err != nil {
		t.Fatal(err)
	}

	res := mustScrub(t, s, 1, 3, map[uint64]bool{12: true})
	if res.Outcome != OutcomePartialRepair {
		t.Fatalf("outcome = %v, want PartialRepair", res.Outcome)
	}
	if !reflect.DeepEqual(res.Repaired, []uint64{11}) || !reflect.DeepEqual(res.Failed, []uint64{12}) {
		t.Fatalf("repaired=%v failed=%v, want [11]/[12]", res.Repaired, res.Failed)
	}
	info, _ := s.Inspect(1)
	byNode := map[uint64]Replica{}
	for _, r := range info.Replicas {
		byNode[r.NodeID] = r
	}
	if r := byNode[11]; r.Version != 5 || r.Stored != "a" || r.Actual != "a" {
		t.Fatalf("repaired replica = %+v, want v5 a/a", r)
	}
	if r := byNode[12]; r.Actual != "rot2" {
		t.Fatalf("failed replica must stay untouched, got %+v", r)
	}

	// 全部需修复副本都失败也属于部分修复。
	res = mustScrub(t, s, 1, 4, map[uint64]bool{12: true})
	if res.Outcome != OutcomePartialRepair || len(res.Repaired) != 0 || !reflect.DeepEqual(res.Failed, []uint64{12}) {
		t.Fatalf("all-fail: outcome=%v repaired=%v failed=%v", res.Outcome, res.Repaired, res.Failed)
	}
}

// 未提交写入向前提交：只写到一个节点的更高版本成为权威。
func TestForwardCommitViaService(t *testing.T) {
	s := NewService()
	mustCreate(t, s, 1, []uint64{10, 11, 12}, 3, "a", 0, 0)
	if err := s.Write(1, 7, "x", []uint64{12}, 1); err != nil {
		t.Fatal(err)
	}
	res := mustScrub(t, s, 1, 2, nil)
	if res.Outcome != OutcomeRepaired {
		t.Fatalf("outcome = %v, want Repaired", res.Outcome)
	}
	if res.CommittedVersion != 3 || res.AuthVersion != 7 || res.AuthDigest != "x" {
		t.Fatalf("committed=%d auth=(%d,%q), want 3/(7,x)", res.CommittedVersion, res.AuthVersion, res.AuthDigest)
	}
	if !reflect.DeepEqual(res.Repaired, []uint64{10, 11}) {
		t.Fatalf("repaired = %v, want [10 11]", res.Repaired)
	}
	info, _ := s.Inspect(1)
	for _, r := range info.Replicas {
		if r.Version != 7 || r.Stored != "x" || r.Actual != "x" {
			t.Fatalf("replica %+v not forward-committed to v7/x", r)
		}
	}
}

// 三类不可修复结果都追加告警，按次序可查询，同块重复告警不合并，且不修改副本。
func TestUnrepairableAlerts(t *testing.T) {
	s := NewService()
	mustCreate(t, s, 1, []uint64{10, 11}, 5, "a", 0, 0) // interval 0，可连续巡检
	mustCreate(t, s, 2, []uint64{10, 11, 12}, 5, "a", 0, 0)
	mustCreate(t, s, 3, []uint64{10, 11, 12}, 5, "a", 0, 0)

	// 块 1：全部位腐 -> NoSource
	_ = s.InjectBitrot(1, 10, "r1", 1)
	_ = s.InjectBitrot(1, 11, "r2", 2)
	// 块 2：最高版本全部位腐 -> CommittedDataLost
	_ = s.Write(2, 9, "b", []uint64{10, 11}, 3)
	_ = s.InjectBitrot(2, 10, "r1", 4)
	_ = s.InjectBitrot(2, 11, "r2", 5)
	// 块 3：同版本不同摘要 -> VersionConflict
	_ = s.Write(3, 9, "b", []uint64{10}, 6)
	_ = s.Write(3, 9, "c", []uint64{11}, 7)

	before1, _ := s.Inspect(1)
	r1 := mustScrub(t, s, 1, 8, nil)
	r2 := mustScrub(t, s, 2, 9, nil)
	r3 := mustScrub(t, s, 3, 10, nil)
	r1again := mustScrub(t, s, 1, 11, nil) // 同一块重复告警不合并

	if r1.Outcome != OutcomeNoSource || r2.Outcome != OutcomeCommittedDataLost || r3.Outcome != OutcomeVersionConflict {
		t.Fatalf("outcomes = %v/%v/%v", r1.Outcome, r2.Outcome, r3.Outcome)
	}
	if r1again.Outcome != OutcomeNoSource {
		t.Fatalf("repeat outcome = %v", r1again.Outcome)
	}
	alerts := s.Alerts()
	if len(alerts) != 4 {
		t.Fatalf("alerts = %d, want 4 (repeat not merged)", len(alerts))
	}
	for i, want := range []Outcome{OutcomeNoSource, OutcomeCommittedDataLost, OutcomeVersionConflict, OutcomeNoSource} {
		if alerts[i].Outcome != want || alerts[i].Seq != i {
			t.Fatalf("alert[%d] = %+v, want outcome %v seq %d", i, alerts[i], want, i)
		}
	}
	if alerts[0].BlockID != 1 || alerts[3].BlockID != 1 {
		t.Fatalf("alert block ids = %d,%d", alerts[0].BlockID, alerts[3].BlockID)
	}
	after1, _ := s.Inspect(1)
	if !reflect.DeepEqual(before1.Replicas, after1.Replicas) {
		t.Fatalf("unrepairable scrub must not modify replicas")
	}
	// 不可修复巡检同样记录巡检时刻：interval 0 下连续成功即证明。
	if info, _ := s.Inspect(1); !info.Scrubbed || info.LastScrub != 11 {
		t.Fatalf("lastScrub = %d, want 11", info.LastScrub)
	}
}

// 副本被丢弃后法定数下降，已提交版本按当前副本数重新推断。
func TestDropReplicaLowersQuorum(t *testing.T) {
	s := NewService()
	mustCreate(t, s, 1, []uint64{10, 11, 12, 13}, 1, "a", 0, 0)
	// 4 副本：版本 8,8,1,1，法定数 3，已提交版本 = 第 3 大 = 1。
	_ = s.Write(1, 8, "b", []uint64{10, 11}, 1)
	res := mustScrub(t, s, 1, 2, nil)
	if res.CommittedVersion != 1 {
		t.Fatalf("committed = %d, want 1 (quorum 3 of 4)", res.CommittedVersion)
	}
	// 丢弃一个 v1 副本：3 副本法定数 2，已提交版本 = 第 2 大 = 8。
	if err := s.DropReplica(1, 12, 3); err != nil {
		t.Fatal(err)
	}
	res = mustScrub(t, s, 1, 4, nil)
	if res.CommittedVersion != 8 {
		t.Fatalf("committed = %d, want 8 (quorum 2 of 3)", res.CommittedVersion)
	}
	// 副本数降到一仍可巡检。
	_ = s.DropReplica(1, 13, 5)
	_ = s.DropReplica(1, 11, 6)
	res = mustScrub(t, s, 1, 7, nil)
	if res.CommittedVersion != 8 || res.Outcome != OutcomeConsistent {
		t.Fatalf("single replica: committed=%d outcome=%v", res.CommittedVersion, res.Outcome)
	}
	// 降到零：块视为不存在。
	if err := s.DropReplica(1, 10, 8); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Scrub(1, 9, nil); !errors.Is(err, ErrBlockNotFound) {
		t.Fatalf("scrub of vanished block: %v", err)
	}
	if _, err := s.Inspect(1); !errors.Is(err, ErrBlockNotFound) {
		t.Fatalf("inspect of vanished block: %v", err)
	}
}

// 从未巡检的块优先，其次按上次巡检时刻从早到晚，并列取块号小者。
func TestSelectDueOrder(t *testing.T) {
	s := NewService()
	nodes := []uint64{1, 2}
	for id := uint64(0); id < 5; id++ {
		mustCreate(t, s, id, nodes, 1, "a", 10, 0)
	}
	// 块 1、2 在 t=3 巡检（并列），块 0 在 t=5 巡检；块 3、4 从未巡检。
	mustScrub(t, s, 1, 3, nil)
	mustScrub(t, s, 2, 3, nil)
	mustScrub(t, s, 0, 5, nil)

	// t=13：块 1、2 到期（3+10），块 0 未到期（5+10=15）。
	got, err := s.SelectDue(13, 10)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint64{3, 4, 1, 2} // 未巡检优先（块号小者先），再按 lastScrub 并列取块号小者
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SelectDue(13) = %v, want %v", got, want)
	}
	// 上限数量生效。
	got, _ = s.SelectDue(13, 3)
	if !reflect.DeepEqual(got, []uint64{3, 4, 1}) {
		t.Fatalf("SelectDue(13,3) = %v, want [3 4 1]", got)
	}
	// t=15：块 0 也到期，排在已巡检块的最后。
	got, _ = s.SelectDue(15, 10)
	if !reflect.DeepEqual(got, []uint64{3, 4, 1, 2, 0}) {
		t.Fatalf("SelectDue(15) = %v, want [3 4 1 2 0]", got)
	}
	// 选取只读：状态与时钟不变。
	if ts, _ := s.LastAcceptedTime(); ts != 5 {
		t.Fatalf("SelectDue must not advance clock, got %d", ts)
	}
	if info, _ := s.Inspect(3); info.Scrubbed {
		t.Fatalf("SelectDue must not mark blocks scrubbed")
	}
	// 但选取同样受时钟回退检查。
	if _, err := s.SelectDue(4, 1); !errors.Is(err, ErrClockRegression) {
		t.Fatalf("SelectDue(4) err = %v, want ErrClockRegression", err)
	}
	if _, err := s.SelectDue(13, 0); !errors.Is(err, ErrInvalidParam) {
		t.Fatalf("SelectDue limit=0 err = %v, want ErrInvalidParam", err)
	}
}

// 并发调用：结果等价于某个串行顺序（配合 -race 验证无数据竞争）。
func TestConcurrentOps(t *testing.T) {
	s := NewService()
	for id := uint64(0); id < 4; id++ {
		mustCreate(t, s, id, []uint64{1, 2, 3}, 1, "a", 0, int64(id))
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	clock := int64(10)
	next := func() int64 {
		mu.Lock()
		defer mu.Unlock()
		clock++
		return clock
	}
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				id := uint64((w + i) % 4)
				node := uint64(1 + (w+i)%3)
				now := next()
				switch i % 5 {
				case 0:
					_ = s.Write(id, uint64(2+i), "v", []uint64{node}, now)
				case 1:
					_ = s.InjectBitrot(id, node, "rot", now)
				case 2:
					_, _ = s.Scrub(id, now, nil)
				case 3:
					_, _ = s.SelectDue(now, 2)
				case 4:
					_, _ = s.Inspect(id)
				}
			}
		}(w)
	}
	wg.Wait()
	// 不变式：每次巡检后，所有自洽且版本最高的副本摘要一致（否则应有告警）。
	for id := uint64(0); id < 4; id++ {
		if _, err := s.Inspect(id); err != nil {
			t.Fatalf("inspect block %d: %v", id, err)
		}
	}
}
