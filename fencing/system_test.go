package fencing

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
)

func ringCells(prefix string, maxRing int) []Cell {
	cells := make([]Cell, 0, maxRing+1)
	for i := 0; i <= maxRing; i++ {
		cells = append(cells, Cell{ID: fmt.Sprintf("%s%d", prefix, i), Ring: i})
	}
	return cells
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func mustErrKind(t *testing.T, err error, want ErrKind) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error kind %s, got nil", want)
	}
	k, ok := ErrKindOf(err)
	if !ok {
		t.Fatalf("error %v is not a fencing.Error", err)
	}
	if k != want {
		t.Fatalf("want error kind %s, got %s (%v)", want, k, err)
	}
}

func mustReachability(t *testing.T, s *System, merchantID, cellID string, want Reachability) {
	t.Helper()
	got, err := s.Reachable(merchantID, cellID)
	mustOK(t, err)
	if got != want {
		t.Fatalf("Reachable(%s,%s) = %s, want %s", merchantID, cellID, got, want)
	}
}

func newSystemWithMerchant(t *testing.T, maxRing int) *System {
	t.Helper()
	s := NewSystem()
	mustOK(t, s.RegisterMerchant(0, "m", "r", ringCells("c", maxRing)))
	return s
}

// 环距恰等于有效半径时仍可达，大一环则暂时不可达。
func TestRingEqualsEffectiveRadius(t *testing.T) {
	s := newSystemWithMerchant(t, 5)
	mustOK(t, s.SetMerchantLevel(1, "m", 2)) // 有效半径 = 5-2 = 3
	mustReachability(t, s, "m", "c3", ReachableNow)
	mustReachability(t, s, "m", "c4", UnreachableTemporary)
}

// 事件区间右端点恰等于当前时刻时，该事件不再计入。
func TestEventEndExclusive(t *testing.T) {
	s := newSystemWithMerchant(t, 4)
	mustOK(t, s.RegisterEvent(0, "e", "r", 10, 20, 1)) // [10,20) 内有效半径 3
	mustErrKind(t, s.PlaceOrder(15, "o1", "m", "c4"), ErrKindTemporarilyUnreachable)
	mustOK(t, s.PlaceOrder(20, "o2", "m", "c4")) // t=20 事件已结束，恢复可达
}

// 嵌套与重叠事件各自独立计入最大值。
func TestNestedOverlappingEventsTakeMax(t *testing.T) {
	s := newSystemWithMerchant(t, 5)
	mustOK(t, s.RegisterEvent(0, "e1", "r", 10, 100, 2))
	mustOK(t, s.RegisterEvent(0, "e2", "r", 20, 30, 5)) // 嵌套于 e1
	mustOK(t, s.RegisterEvent(0, "e3", "r", 25, 35, 3)) // 与 e2 重叠
	mustOK(t, s.SetMerchantLevel(26, "m", 0))           // 推进时钟：max(2,5,3)=5，半径 0
	mustReachability(t, s, "m", "c0", ReachableNow)
	mustReachability(t, s, "m", "c1", UnreachableTemporary)
	mustOK(t, s.SetMerchantLevel(32, "m", 0)) // e2 结束：max(2,3)=3，半径 2
	mustReachability(t, s, "m", "c2", ReachableNow)
	mustReachability(t, s, "m", "c3", UnreachableTemporary)
	mustOK(t, s.SetMerchantLevel(40, "m", 0)) // 只剩 e1：等级 2，半径 3
	mustReachability(t, s, "m", "c3", ReachableNow)
	mustOK(t, s.SetMerchantLevel(100, "m", 0)) // 全部结束：半径 5
	mustReachability(t, s, "m", "c5", ReachableNow)
}

// 提前终止后等级立即回落；重复终止与终止不存在事件分别报错。
func TestEarlyTerminationLowersLevel(t *testing.T) {
	s := newSystemWithMerchant(t, 3)
	mustOK(t, s.RegisterEvent(0, "e", "r", 10, 100, 3)) // 生效后半径 0
	mustOK(t, s.SetMerchantLevel(50, "m", 0))
	mustReachability(t, s, "m", "c1", UnreachableTemporary)
	mustOK(t, s.TerminateEvent(60, "e"))
	mustReachability(t, s, "m", "c3", ReachableNow) // 终止时刻起不再计入
	mustErrKind(t, s.TerminateEvent(61, "e"), ErrKindEventTerminated)
	mustErrKind(t, s.TerminateEvent(61, "ghost"), ErrKindEventNotFound)
}

// 商家等级与平台事件等级取较大者；事件结束后回落到商家等级。
func TestMerchantLevelVsPlatformMax(t *testing.T) {
	s := newSystemWithMerchant(t, 4)
	mustOK(t, s.SetMerchantLevel(0, "m", 1))
	mustOK(t, s.RegisterEvent(0, "e", "r", 10, 20, 3))
	mustOK(t, s.SetMerchantLevel(15, "m", 1)) // max(1,3)=3，半径 1
	mustReachability(t, s, "m", "c1", ReachableNow)
	mustReachability(t, s, "m", "c2", UnreachableTemporary)
	mustOK(t, s.SetMerchantLevel(20, "m", 1)) // 事件结束：等级 1，半径 3
	mustReachability(t, s, "m", "c3", ReachableNow)
	mustReachability(t, s, "m", "c4", UnreachableTemporary)
}

// 有效半径截到零时只有环距为零的单元可达。
func TestRadiusClampsToZero(t *testing.T) {
	s := newSystemWithMerchant(t, 2)
	mustOK(t, s.SetMerchantLevel(1, "m", 5)) // 半径 = max(2-5,0) = 0
	mustReachability(t, s, "m", "c0", ReachableNow)
	mustReachability(t, s, "m", "c1", UnreachableTemporary)
}

// 下单可达而接单时因收缩不可达：接单被拒，订单自动转为因收缩取消的终态。
func TestPlaceThenAcceptAutoCancel(t *testing.T) {
	s := newSystemWithMerchant(t, 2)
	mustOK(t, s.PlaceOrder(0, "o", "m", "c2"))
	mustOK(t, s.RegisterEvent(0, "e", "r", 5, 50, 1)) // 生效后半径 1
	mustErrKind(t, s.AcceptOrder(10, "o"), ErrKindTemporarilyUnreachable)
	if got := s.orders["o"].state; got != stCancelledShrink {
		t.Fatalf("order state = %s, want %s", got, stCancelledShrink)
	}
	// 取消为终态：再接单、送达均报已取消。
	mustErrKind(t, s.AcceptOrder(11, "o"), ErrKindOrderCancelled)
	mustErrKind(t, s.DeliverOrder(11, "o"), ErrKindOrderCancelled)
}

// 已接单订单不受之后任何收缩影响，可正常送达。
func TestAcceptedOrderImmuneToShrink(t *testing.T) {
	s := newSystemWithMerchant(t, 2)
	mustOK(t, s.PlaceOrder(0, "o", "m", "c2"))
	mustOK(t, s.AcceptOrder(1, "o"))
	mustOK(t, s.SetMerchantLevel(2, "m", 9)) // 半径 0，c2 已不可达
	mustReachability(t, s, "m", "c2", UnreachableTemporary)
	mustOK(t, s.DeliverOrder(3, "o"))
	if got := s.orders["o"].state; got != stDelivered {
		t.Fatalf("order state = %s, want %s", got, stDelivered)
	}
}

// 改址被拒保持原地址；改址成功不改变已接单状态；只允许改一次。
func TestChangeAddressRejectedKeepsOriginal(t *testing.T) {
	s := newSystemWithMerchant(t, 2)
	mustOK(t, s.PlaceOrder(0, "o", "m", "c2"))
	mustOK(t, s.AcceptOrder(1, "o"))
	mustErrKind(t, s.ChangeAddress(2, "o", "ghost"), ErrKindPermanentOutOfRange)
	if got := s.orders["o"].cell; got != "c2" {
		t.Fatalf("cell = %s, want c2", got)
	}
	mustOK(t, s.SetMerchantLevel(3, "m", 1)) // 半径 1
	mustErrKind(t, s.ChangeAddress(4, "o", "c2"), ErrKindTemporarilyUnreachable)
	if got := s.orders["o"].cell; got != "c2" {
		t.Fatalf("cell = %s, want c2", got)
	}
	mustOK(t, s.ChangeAddress(5, "o", "c1"))
	if got := s.orders["o"].cell; got != "c1" {
		t.Fatalf("cell = %s, want c1", got)
	}
	if got := s.orders["o"].state; got != stAccepted {
		t.Fatalf("order state = %s, want %s", got, stAccepted)
	}
	mustErrKind(t, s.ChangeAddress(6, "o", "c0"), ErrKindAddressChanged)
}

// 恢复时刻查询的三种结果：当前可达 / 未来某时刻恢复 / 商家等级导致无恢复。
func TestNextRecoveryOutcomes(t *testing.T) {
	s := newSystemWithMerchant(t, 3)
	mustOK(t, s.RegisterEvent(0, "e1", "r", 10, 40, 2))
	mustOK(t, s.RegisterEvent(0, "e2", "r", 30, 50, 3))
	rec, err := s.NextRecovery("m", "c3") // t=0 无事件生效，当前可达
	mustOK(t, err)
	if rec.Kind != RecoveryNow {
		t.Fatalf("kind = %s, want %s", rec.Kind, RecoveryNow)
	}
	mustOK(t, s.SetMerchantLevel(20, "m", 0)) // t=20：e1 生效，c3 暂时不可达
	rec, err = s.NextRecovery("m", "c3")
	mustOK(t, err)
	if rec.Kind != RecoveryAt || rec.At != 50 { // e1、e2 区间并集覆盖 [10,50)
		t.Fatalf("recovery = (%s,%d), want (%s,50)", rec.Kind, rec.At, RecoveryAt)
	}
	rec, err = s.NextRecovery("m", "c1") // c1 在半径 1 内，当前可达
	mustOK(t, err)
	if rec.Kind != RecoveryNow {
		t.Fatalf("kind = %s, want %s", rec.Kind, RecoveryNow)
	}
	mustOK(t, s.SetMerchantLevel(30, "m", 3)) // 商家等级 3 已使 c3 不可达
	rec, err = s.NextRecovery("m", "c3")
	mustOK(t, err)
	if rec.Kind != RecoveryNone {
		t.Fatalf("kind = %s, want %s", rec.Kind, RecoveryNone)
	}
	rec, err = s.NextRecovery("m", "ghost") // 不在基础范围
	mustOK(t, err)
	if rec.Kind != RecoveryPermanent {
		t.Fatalf("kind = %s, want %s", rec.Kind, RecoveryPermanent)
	}
}

// 两种不可达原因可程序化区分。
func TestUnreachableReasonsDistinct(t *testing.T) {
	s := newSystemWithMerchant(t, 1)
	mustOK(t, s.SetMerchantLevel(1, "m", 1)) // 半径 0
	errPerm := s.PlaceOrder(2, "o1", "m", "ghost")
	errTemp := s.PlaceOrder(2, "o2", "m", "c1")
	mustErrKind(t, errPerm, ErrKindPermanentOutOfRange)
	mustErrKind(t, errTemp, ErrKindTemporarilyUnreachable)
	k1, _ := ErrKindOf(errPerm)
	k2, _ := ErrKindOf(errTemp)
	if k1 == k2 {
		t.Fatalf("permanent and temporary errors must be distinguishable, both %s", k1)
	}
	mustReachability(t, s, "m", "ghost", UnreachablePermanent)
	mustReachability(t, s, "m", "c1", UnreachableTemporary)
}

// 拒绝次序：参数非法 > 时钟回退 > 对象不存在 > 状态类 > 永久不在范围 > 暂时不可达。
func TestErrorPrecedence(t *testing.T) {
	s := newSystemWithMerchant(t, 1)
	mustOK(t, s.PlaceOrder(1, "o", "m", "c1"))
	mustOK(t, s.AcceptOrder(2, "o"))
	mustOK(t, s.DeliverOrder(3, "o"))
	// 参数非法先于时钟回退：空订单号 + 回退时刻。
	mustErrKind(t, s.PlaceOrder(0, "", "m", "c1"), ErrKindInvalidParam)
	// 时钟回退先于对象不存在：合法参数 + 回退时刻 + 不存在商家。
	mustErrKind(t, s.PlaceOrder(0, "oX", "ghost", "c1"), ErrKindClockRollback)
	// 对象不存在先于状态类：不存在的订单（已送达订单的改址另测）。
	mustErrKind(t, s.ChangeAddress(4, "ghost", "c1"), ErrKindOrderNotFound)
	// 状态类先于永久不在范围：已送达订单改址到范围外单元。
	mustErrKind(t, s.ChangeAddress(5, "o", "ghost"), ErrKindOrderDelivered)
	// 永久不在范围先于暂时不可达：收缩期间下单到范围外单元。
	mustOK(t, s.SetMerchantLevel(6, "m", 1))
	mustErrKind(t, s.PlaceOrder(7, "o2", "m", "ghost"), ErrKindPermanentOutOfRange)
	mustErrKind(t, s.PlaceOrder(7, "o3", "m", "c1"), ErrKindTemporarilyUnreachable)
}

// 被拒绝的操作不推进时钟；时钟回退报错。
func TestClockRollbackAndRejectionKeepsClock(t *testing.T) {
	s := newSystemWithMerchant(t, 1)
	mustOK(t, s.PlaceOrder(10, "o1", "m", "c1"))
	mustErrKind(t, s.PlaceOrder(9, "o2", "m", "c1"), ErrKindClockRollback)
	mustErrKind(t, s.PlaceOrder(20, "o3", "m", "ghost"), ErrKindPermanentOutOfRange)
	// 上面被拒的 t=20 未推进时钟，t=15 仍被接受。
	mustOK(t, s.PlaceOrder(15, "o4", "m", "c1"))
	if s.clock != 15 {
		t.Fatalf("clock = %d, want 15", s.clock)
	}
}

// 并发调用：结果等价于某个串行顺序（全局锁串行化），-race 下验证。
func TestConcurrentLinearizable(t *testing.T) {
	s := newSystemWithMerchant(t, 3)
	var now atomic.Int64
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				t := now.Add(1)
				id := fmt.Sprintf("g%d-i%d", g, i)
				switch i % 6 {
				case 0:
					_ = s.RegisterEvent(t, "e"+id, "r", t, t+10, i%4)
				case 1:
					_ = s.PlaceOrder(t, "o"+id, "m", fmt.Sprintf("c%d", i%4))
				case 2:
					_ = s.AcceptOrder(t, "o"+fmt.Sprintf("g%d-i%d", g, i-1))
				case 3:
					_ = s.SetMerchantLevel(t, "m", i%3)
				case 4:
					_, _ = s.Reachable("m", "c2")
				case 5:
					_, _ = s.NextRecovery("m", "c3")
				}
			}
		}(g)
	}
	wg.Wait()
	if s.clock > now.Load() {
		t.Fatalf("clock = %d exceeds max issued time %d", s.clock, now.Load())
	}
}
