package transfer

import (
	"errors"
	"strconv"
	"sync"
	"testing"
)

func mustNew(t *testing.T, cd, r, u, wt int64) *System {
	t.Helper()
	s, err := New(cd, r, u, wt)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func (s *System) mustShard(t *testing.T, sid string, cap int) {
	t.Helper()
	if err := s.NewShard(sid, cap); err != nil {
		t.Fatalf("NewShard(%s): %v", sid, err)
	}
}

func (s *System) mustCreate(t *testing.T, now int64, shardID, char, name string) {
	t.Helper()
	if err := s.Create(now, shardID, char, name); err != nil {
		t.Fatalf("Create(%s,%s,%s)@%d: %v", shardID, char, name, now, err)
	}
}

func (s *System) charState(char string) (home, name string, pending, hasOrder bool) {
	c := s.chars[char]
	if c == nil {
		return "", "", false, false
	}
	return c.home, c.name, c.rename, c.order != nil
}

func wantErr(t *testing.T, got, want error, ctx string) {
	t.Helper()
	if !errors.Is(got, want) {
		t.Fatalf("%s: want %v, got %v", ctx, want, got)
	}
}

// TestWalkthrough 复现题面第一例与第二例关键序列。
func TestWalkthrough(t *testing.T) {
	s := mustNew(t, 1000, 500, 200, 100)
	s.mustShard(t, "1", 2)
	s.mustShard(t, "2", 3)
	s.mustCreate(t, 0, "1", "A", "neo")
	s.mustCreate(t, 0, "2", "B", "neo")
	s.mustCreate(t, 0, "2", "C", "trin")

	if err := s.Request(0, "B", "1"); err != nil {
		t.Fatalf("Request B->1: %v", err)
	}
	if sh1, _ := s.shards.Get("1"); sh1.Load() != 2 {
		t.Fatalf("load with reservation = %d, want 2", sh1.Load())
	}
	wantErr(t, s.Request(5, "C", "1"), ErrCapReached, "C->1 blocked by reservation")

	if err := s.Complete(50, "B"); err != nil {
		t.Fatalf("Complete B: %v", err)
	}
	home, nm, pending, order := s.charState("B")
	if home != "1" || nm != "neo" || !pending || order {
		t.Fatalf("B after complete: home=%s name=%s pending=%v order=%v", home, nm, pending, order)
	}
	b := s.chars["B"]
	if b.prev != "2" || b.prevDone != 50 || !b.coolSet || b.coolAt != 50 {
		t.Fatalf("B prev/cool wrong: %+v", b)
	}
	wantErr(t, s.Create(60, "2", "D", "neo"), ErrNameReserved, "D neo reserved")

	if err := s.Request(100, "B", "2"); err != nil {
		t.Fatalf("B return request: %v", err)
	}
	if !s.chars["B"].order.IsReturn {
		t.Fatal("order should be flagged as return")
	}
	if err := s.Complete(120, "B"); err != nil {
		t.Fatalf("B return complete: %v", err)
	}
	home, nm, pending, order = s.charState("B")
	if home != "2" || nm != "neo" || pending || order {
		t.Fatalf("B after return: home=%s name=%s pending=%v order=%v", home, nm, pending, order)
	}
	if b.prev != "" || b.coolAt != 50 {
		t.Fatalf("return must clear prev and keep coolStart: %+v", b)
	}
	wantErr(t, s.Request(130, "B", "1"), ErrCoolingDown, "B cooldown after return")
	if err := s.Request(1050, "B", "1"); err != nil {
		t.Fatalf("B at cooldown equality: %v", err)
	}

	// 第二例：回迁请求拖到 now=250（== prevDone+U）不再算回迁，报冷却中。
	s2 := mustNew(t, 1000, 500, 200, 100)
	s2.mustShard(t, "1", 2)
	s2.mustShard(t, "2", 3)
	s2.mustCreate(t, 0, "2", "B", "neo")
	if err := s2.Request(0, "B", "1"); err != nil {
		t.Fatal(err)
	}
	if err := s2.Complete(50, "B"); err != nil {
		t.Fatal(err)
	}
	wantErr(t, s2.Request(250, "B", "2"), ErrCoolingDown, "window equality is not return")
	if err := s2.Request(249, "B", "2"); err != nil {
		t.Fatalf("249 should be within return window: %v", err)
	}
}

// TestOrderExpiryEquality 迁移单过期取等与预留释放。
func TestOrderExpiryEquality(t *testing.T) {
	s := mustNew(t, 1000, 500, 200, 100)
	s.mustShard(t, "1", 2)
	s.mustShard(t, "2", 2)
	s.mustCreate(t, 0, "2", "B", "neo")
	s.mustCreate(t, 0, "2", "C", "tri")
	if err := s.Request(0, "B", "1"); err != nil {
		t.Fatal(err)
	}
	wantErr(t, s.Complete(100, "B"), ErrNoOrder, "expire equality")
	sh1, _ := s.shards.Get("1")
	if eff := s.effReserved("1", 100); eff != 0 || s.effLoad("1", 100) != 0 {
		t.Fatalf("expired reservation not released: effResv=%d effLoad=%d", eff, s.effLoad("1", 100))
	}
	if err := s.Request(100, "C", "1"); err != nil {
		t.Fatalf("C should use freed reservation: %v", err)
	}
	if sh1.Load() != 1 || sh1.Reserved() != 1 {
		t.Fatalf("load after C request = %d resv=%d", sh1.Load(), sh1.Reserved())
	}
}

// TestRetentionEqualityAndSelf 保留期取等与保留者本人取回。
func TestRetentionEqualityAndSelf(t *testing.T) {
	s := mustNew(t, 1000, 500, 200, 1000)
	s.mustShard(t, "1", 5)
	s.mustShard(t, "2", 5)
	s.mustCreate(t, 0, "1", "A", "x")
	s.mustCreate(t, 0, "2", "B", "neo")
	s.mustCreate(t, 0, "2", "B2", "kay")
	// 两张迁移单可分别发起。
	if err := s.Request(0, "B2", "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Request(0, "B", "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(10, "B"); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(11, "B2"); err != nil {
		t.Fatal(err)
	}
	// B2：外迁后在回迁窗口内回原服，本人保留不拦自己。
	if err := s.Request(12, "B2", "2"); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(13, "B2"); err != nil {
		t.Fatal(err)
	}
	if _, nm, pending, _ := s.charState("B2"); nm != "kay" || pending {
		t.Fatalf("B2 self reclaim failed: name=%s pending=%v", nm, pending)
	}

	// 服2 的 neo 为 B 保留到 510：509 仍保留，取等 510 释放。
	wantErr(t, s.Create(509, "2", "D", "neo"), ErrNameReserved, "before expire reserved")
	s.mustCreate(t, 510, "2", "D", "neo")
}

// TestRenameAndPendingLeavesNoRetention 改名释放旧名；待改名离开不留保留。
func TestRenameAndPendingLeavesNoRetention(t *testing.T) {
	s := mustNew(t, 1000, 500, 200, 1000)
	s.mustShard(t, "1", 5)
	s.mustShard(t, "2", 5)
	s.mustCreate(t, 0, "1", "A", "neo")
	s.mustCreate(t, 0, "2", "B", "neo")
	if err := s.Request(0, "B", "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(10, "B"); err != nil {
		t.Fatal(err)
	}
	if _, _, pending, _ := s.charState("B"); !pending {
		t.Fatal("B should be pending rename on collision")
	}
	if err := s.Request(20, "B", "2"); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(30, "B"); err != nil {
		t.Fatal(err)
	}
	if _, nm, pending2, _ := s.charState("B"); nm != "neo" || pending2 {
		t.Fatalf("B should reclaim neo: name=%s pending=%v", nm, pending2)
	}
	if err := s.Rename(40, "B", "morpheus"); err != nil {
		t.Fatal(err)
	}
	s.mustCreate(t, 40, "2", "E", "neo")
	// 回迁完成后 coolStart 仍为 30：50 冷却中；取等到 1030 才可迁。
	wantErr(t, s.Request(50, "B", "1"), ErrCoolingDown, "normal move still cooling")
	if err := s.Request(1030, "B", "1"); err != nil {
		t.Fatal(err)
	}
	wantErr(t, s.Rename(1031, "B", "x"), ErrFrozen, "rename while frozen")
}

// TestBlockersBeforeCooldown 阻断项先于冷却判定。
func TestBlockersBeforeCooldown(t *testing.T) {
	s := mustNew(t, 1000, 500, 200, 1000)
	s.mustShard(t, "1", 5)
	s.mustShard(t, "2", 5)
	s.mustCreate(t, 0, "2", "B", "neo")
	if err := s.Request(0, "B", "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.Complete(10, "B"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetBlockers("B", 2); err != nil {
		t.Fatal(err)
	}
	wantErr(t, s.Request(20, "B", "2"), ErrBlocked, "blocker precedes cooldown")
	if err := s.SetBlockers("B", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Request(20, "B", "2"); err != nil {
		t.Fatalf("return within window: %v", err)
	}
}

// TestRejectionOrder 校验拒绝次序、时钟回退与拒绝不推进时钟。
func TestRejectionOrder(t *testing.T) {
	s := mustNew(t, 1000, 500, 200, 1000)
	s.mustShard(t, "1", 1)
	wantErr(t, s.Create(-1, "1", "", "x"), ErrInvalidParam, "param first")
	s.mustCreate(t, 10, "1", "A", "a")
	wantErr(t, s.Create(5, "1", "Z", "z"), ErrClockRewind, "rewind")
	wantErr(t, s.Create(10, "nope", "A", "z"), ErrNoShard, "no shard before dup char")
	wantErr(t, s.Create(10, "1", "A", "z"), ErrCharExists, "dup char before cap")
	wantErr(t, s.Create(10, "1", "Z", "a"), ErrCapReached, "cap before occupied")
	if err := s.Rename(10, "A", "a2"); err != nil {
		t.Fatalf("clock must not advance on rejection: %v", err)
	}

	s2 := mustNew(t, 1000, 500, 200, 1000)
	s2.mustShard(t, "1", 5)
	s2.mustShard(t, "2", 5)
	s2.mustCreate(t, 0, "2", "B", "neo")
	wantErr(t, s2.Request(0, "", "1"), ErrInvalidParam, "req param")
	// 以下用 now=0 验存在性/同服次序；时钟仍为 0。
	wantErr(t, s2.Request(0, "NOPE", "1"), ErrNoChar, "req no char")
	wantErr(t, s2.Request(0, "B", "nope"), ErrNoShard, "req no dst")
	wantErr(t, s2.Request(0, "B", "2"), ErrSameShard, "req same shard")
	if err := s2.Request(10, "B", "1"); err != nil {
		t.Fatal(err)
	}
	// 已接受 now=10，之后 now=5 报时钟回退。
	wantErr(t, s2.Request(5, "B", "1"), ErrClockRewind, "req rewind")
	wantErr(t, s2.Request(10, "B", "1"), ErrActiveOrder, "req active order")
	if err := s2.Cancel(20, "B"); err != nil {
		t.Fatal(err)
	}
}

// TestParameterBounds New/NewShard 参数边界。
func TestParameterBounds(t *testing.T) {
	for _, d := range []int64{0, -1, MaxDuration + 1} {
		if _, err := New(d, 1, 1, 1); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("CD=%d: %v", d, err)
		}
		if _, err := New(1, d, 1, 1); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("R=%d: %v", d, err)
		}
		if _, err := New(1, 1, d, 1); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("U=%d: %v", d, err)
		}
		if _, err := New(1, 1, 1, d); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("Wt=%d: %v", d, err)
		}
	}
	s := mustNew(t, 1, 1, 1, 1)
	wantErr(t, s.NewShard("", 1), ErrInvalidParam, "empty sid")
	wantErr(t, s.NewShard("z", 0), ErrInvalidParam, "cap 0")
	wantErr(t, s.NewShard("z", MaxCap+1), ErrInvalidParam, "cap too big")
	if err := s.NewShard("z", MaxCap); err != nil {
		t.Fatal(err)
	}
}

// TestCancelReleasesReservation 取消迁移单释放预留并解冻。
func TestCancelReleasesReservation(t *testing.T) {
	s := mustNew(t, 1000, 500, 200, 1000)
	s.mustShard(t, "1", 1)
	s.mustShard(t, "2", 5)
	s.mustCreate(t, 0, "2", "B", "neo")
	s.mustCreate(t, 0, "2", "C", "tri")
	if err := s.Request(0, "B", "1"); err != nil {
		t.Fatal(err)
	}
	wantErr(t, s.Request(5, "C", "1"), ErrCapReached, "reserved cap")
	if err := s.Cancel(10, "B"); err != nil {
		t.Fatal(err)
	}
	if err := s.Request(10, "C", "1"); err != nil {
		t.Fatalf("after cancel C should fit: %v", err)
	}
	wantErr(t, s.Cancel(20, "B"), ErrNoOrder, "no order after cancel")
	if err := s.Complete(30, "C"); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentSerializable 并发调用下系统不崩溃，且每服负载永不越 cap。
func TestConcurrentSerializable(t *testing.T) {
	s := mustNew(t, 100, 200, 50, 300)
	for _, id := range []string{"1", "2", "3"} {
		s.mustShard(t, id, 6)
	}
	const goroutines = 16
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for k := 0; k < 200; k++ {
				// 每个操作一个全局唯一递增时间戳，避免跨 goroutine 时钟回退。
				now := int64(g*1000 + k)
				char := "g" + strconv.Itoa(g) + "c" + strconv.Itoa(k)
				dst := []string{"1", "2", "3"}[(g+k)%3]
				src := []string{"1", "2", "3"}[(g+k+1)%3]
				if err := s.Create(now, src, char, "n"+strconv.Itoa(g*200+k)); err != nil {
					continue
				}
				_ = s.Request(now+1, char, dst)
				_ = s.Complete(now+2, char)
			}
		}(g)
	}
	wg.Wait()
	// 并发结束后校验所有服负载不超 cap、名字唯一。
	for _, id := range []string{"1", "2", "3"} {
		sh, _ := s.shards.Get(id)
		if sh.Load() > sh.Cap() {
			t.Fatalf("shard %s load %d over cap %d", id, sh.Load(), sh.Cap())
		}
	}
	for _, nm := range []string{"1", "2", "3"} {
		holders := map[string]int{}
		s.names.EachName(nm, func(_, holder, _ string, _ int64) {
			if holder != "" {
				holders[holder]++
			}
		})
		for h, c := range holders {
			if c > 1 {
				t.Fatalf("shard %s holder %s count %d", nm, h, c)
			}
		}
	}
}
