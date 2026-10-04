package cbs

import (
	"errors"
	"fmt"
	"math"
	"math/big"
	"sort"
	"strings"
	"sync"
	"testing"
)

func errKind(err error) ErrKind {
	var e *Error
	if errors.As(err, &e) {
		return e.Kind
	}
	return -1
}

func mustOK(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("期望成功，得到错误: %v", err)
	}
}

func mustKind(t *testing.T, err error, kind ErrKind) {
	t.Helper()
	if errKind(err) != kind {
		t.Fatalf("期望错误 %s，得到 %v", kind, err)
	}
}

func mustState(t *testing.T, l *Ledger, id string, st State, q, d, w int64) {
	t.Helper()
	v, err := l.State(id)
	mustOK(t, err)
	if v.State != st || v.Budget != q || v.Deadline.Cmp(big.NewInt(d)) != 0 || v.Work != w {
		t.Fatalf("State(%s) = (%s, q=%d, d=%s, w=%d)，期望 (%s, q=%d, d=%d, w=%d)",
			id, v.State, v.Budget, v.Deadline, v.Work, st, q, d, w)
	}
}

func mustTotal(t *testing.T, l *Ledger, want string) {
	t.Helper()
	if got := l.Total().RatString(); got != want {
		t.Fatalf("Total() = %s，期望 %s", got, want)
	}
}

func mustNext(t *testing.T, l *Ledger, wantID string, wantOK bool) {
	t.Helper()
	id, ok := l.Next()
	if id != wantID || ok != wantOK {
		t.Fatalf("Next() = (%q, %v)，期望 (%q, %v)", id, ok, wantID, wantOK)
	}
}

// dump 返回整个账本的确定性文本快照，用于重放一致性比对。
func dump(l *Ledger) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "clock=%d total=%s", l.Clock(), l.Total().RatString())
	if id, ok := l.Next(); ok {
		fmt.Fprintf(&sb, " next=%s", id)
	} else {
		sb.WriteString(" next=<none>")
	}
	var ids []string
	l.mu.Lock()
	for id := range l.servers {
		ids = append(ids, id)
	}
	l.mu.Unlock()
	sort.Strings(ids)
	for _, id := range ids {
		v, err := l.State(id)
		if err != nil {
			panic(err)
		}
		fmt.Fprintf(&sb, " | %s %s q=%d d=%s w=%d", id, v.State, v.Budget, v.Deadline, v.Work)
	}
	return sb.String()
}

// 规格书中的完整示例走查。
func TestSpecWalkthrough(t *testing.T) {
	l := NewLedger()
	mustOK(t, l.Add("S1", 3, 5))
	mustOK(t, l.Add("S2", 2, 5))
	mustOK(t, l.Add("S3", 1, 10))

	mustOK(t, l.Wake("S1", 0, 1))
	mustOK(t, l.Wake("S2", 0, 4)) // 3/5 + 2/5 = 1，恰为上限
	mustTotal(t, l, "1")
	mustState(t, l, "S1", Ready, 3, 5, 1)
	mustState(t, l, "S2", Ready, 2, 5, 4)

	mustOK(t, l.Run("S1", 0, 1)) // S1 与 S2 并列 d=5，编号小者先运行
	mustState(t, l, "S1", Idle, 2, 5, 0)
	if c := l.Clock(); c != 1 {
		t.Fatalf("clock = %d，期望 1", c)
	}

	// 2·5=10 < (5−1)·3=12，零松弛未到，S1 仍占用 3/5。
	mustKind(t, l.Wake("S3", 1, 1), ErrBandwidth)
	mustState(t, l, "S3", Released, 1, 0, 0) // 拒绝不改变任何状态
	if c := l.Clock(); c != 1 {
		t.Fatalf("被拒绝的 Wake 改变了时钟: %d", c)
	}

	// 时钟 2 时 10 ≥ (5−2)·3=9，S1 已释放，带宽可被 S3 使用。
	mustOK(t, l.Wake("S3", 2, 1))
	mustState(t, l, "S3", Ready, 1, 12, 1)
	mustTotal(t, l, "1/2") // S2 2/5 + S3 1/10，S1 已释放

	// S2 恰耗尽预算：q 立即补回 2，d 变 10，w 剩 2，时钟 4。
	mustOK(t, l.Run("S2", 2, 2))
	mustState(t, l, "S2", Ready, 2, 10, 2)
	if c := l.Clock(); c != 4 {
		t.Fatalf("clock = %d，期望 4", c)
	}

	// S1 已释放须重新接纳：0.4 + 0.1 + 0.6 超过 1，被拒。
	mustKind(t, l.Wake("S1", 4, 1), ErrBandwidth)
	mustState(t, l, "S1", Released, 2, 5, 0)
	mustNext(t, l, "S2", true) // d=10 < d=12
}

// 零松弛恰等于：q·P == (d−now)·Q 时算已释放。
func TestZeroSlackExactEquality(t *testing.T) {
	l := NewLedger()
	mustOK(t, l.Add("A", 2, 4))
	mustOK(t, l.Add("B", 1, 100)) // 仅用于推进时钟

	mustOK(t, l.Wake("A", 0, 1))
	mustOK(t, l.Run("A", 0, 1)) // q=1, d=4, w=0, clock=1
	mustOK(t, l.Wake("B", 2, 1))

	// q·P = 1·4 = 4，(d−now)·Q = (4−2)·2 = 4，恰相等 → 已释放。
	mustState(t, l, "A", Released, 1, 4, 0)
	mustTotal(t, l, "1/100") // A 不再占用
}

// 差一仍占用：q·P = (d−now)·Q − 1 时仍为空闲占用。
func TestZeroSlackOffByOne(t *testing.T) {
	l := NewLedger()
	mustOK(t, l.Add("A", 2, 5))
	mustOK(t, l.Add("B", 1, 100))

	mustOK(t, l.Wake("A", 0, 1))
	mustOK(t, l.Run("A", 0, 1)) // q=1, d=5, w=0, clock=1
	mustOK(t, l.Wake("B", 2, 1))

	// q·P = 1·5 = 5，(d−now)·Q = (5−2)·2 = 6，差一 → 仍占用。
	mustState(t, l, "A", Idle, 1, 5, 0)
	mustTotal(t, l, "41/100") // 2/5 + 1/100
}

// 空闲占用者被唤醒时不重置 q 与 d。
func TestIdleWakeKeepsBudgetDeadline(t *testing.T) {
	l := NewLedger()
	mustOK(t, l.Add("A", 3, 5))

	mustOK(t, l.Wake("A", 0, 2))
	mustOK(t, l.Run("A", 0, 2)) // q=1, d=5, w=0, clock=2；1·5=5 < (5−2)·3=9 → 空闲占用
	mustState(t, l, "A", Idle, 1, 5, 0)

	// 时钟 3 时 1·5=5 < (5−3)·3=6，仍空闲占用；唤醒保持 q、d。
	mustOK(t, l.Wake("A", 3, 7))
	mustState(t, l, "A", Ready, 1, 5, 7)
	if c := l.Clock(); c != 3 {
		t.Fatalf("clock = %d，期望 3", c)
	}
}

// 已释放者被唤醒时重置 q=Q、d=now+P。
func TestReleasedWakeResetsBudgetDeadline(t *testing.T) {
	l := NewLedger()
	mustOK(t, l.Add("A", 3, 5))
	mustOK(t, l.Add("B", 1, 100))

	mustOK(t, l.Wake("A", 0, 2))
	mustOK(t, l.Run("A", 0, 2)) // q=1, d=5, w=0, clock=2
	mustOK(t, l.Wake("B", 5, 1))

	// q·P = 5 ≥ (5−5)·3 = 0 → 已释放。
	mustState(t, l, "A", Released, 1, 5, 0)
	mustOK(t, l.Wake("A", 5, 9)) // 重新接纳：q=Q、d=now+P
	mustState(t, l, "A", Ready, 3, 10, 9)
}

// Wake 被拒时已释放者保留旧 q、d，之后带宽释放重试可通过。
func TestRejectedWakeKeepsOldStateAndRetry(t *testing.T) {
	l := NewLedger()
	mustOK(t, l.Add("A", 3, 5))
	mustOK(t, l.Add("B", 2, 5))
	mustOK(t, l.Add("C", 2, 10))

	// C 先被唤醒再变为已释放，留下旧 q=1、d=10。
	mustOK(t, l.Wake("C", 0, 1))
	mustOK(t, l.Run("C", 0, 1))  // q=1, d=10, w=0, clock=1
	mustOK(t, l.Wake("A", 5, 1)) // 时钟 5：C 的 1·10=10 ≥ (10−5)·2=10 → 已释放
	mustOK(t, l.Wake("B", 5, 4)) // A 3/5 + B 2/5 = 1

	// C 已释放须重新接纳，但总带宽已满 → 拒绝；旧 q、d 保留。
	mustKind(t, l.Wake("C", 5, 1), ErrBandwidth)
	mustState(t, l, "C", Released, 1, 10, 0)

	// A 运行完进入空闲占用，时钟到 7 时 A 释放带宽（2·5=10 ≥ (10−7)·3=9）。
	mustOK(t, l.Run("A", 5, 1)) // A 与 B 并列 d=10，A 编号小
	mustOK(t, l.Run("B", 6, 1)) // 推进时钟到 7
	mustState(t, l, "A", Released, 2, 10, 0)

	// 他人释放带宽后，同一次 Wake 重试通过。
	mustOK(t, l.Wake("C", 7, 1))
	mustState(t, l, "C", Ready, 2, 17, 1)
}

// q 恰在 w 归零的同一单位耗尽时也照常补满并后移 d。
func TestReplenishExactOnExhaustion(t *testing.T) {
	l := NewLedger()
	mustOK(t, l.Add("A", 3, 5))

	mustOK(t, l.Wake("A", 0, 3))
	mustOK(t, l.Run("A", 0, 3)) // 第 3 个单位 q 归 0：补满 q=3，d=5+5=10
	mustState(t, l, "A", Idle, 3, 10, 0)
	if c := l.Clock(); c != 3 {
		t.Fatalf("clock = %d，期望 3", c)
	}
}

// δ 跨越多次补充：d 一次加多个 P。
func TestReplenishMultiplePeriods(t *testing.T) {
	l := NewLedger()
	mustOK(t, l.Add("A", 3, 5))

	mustOK(t, l.Wake("A", 0, 8))
	// q 序列：3,2,1,0→(q=3,d=10),2,1,0→(q=3,d=15),2,1
	mustOK(t, l.Run("A", 0, 8))
	mustState(t, l, "A", Idle, 1, 15, 0)
}

// 总带宽恰等于 1 通过（非平凡分母的既约分数求和）。
func TestTotalExactlyOne(t *testing.T) {
	l := NewLedger()
	mustOK(t, l.Add("A", 1, 2))
	mustOK(t, l.Add("B", 1, 3))
	mustOK(t, l.Add("C", 1, 6))

	mustOK(t, l.Wake("A", 0, 1))
	mustOK(t, l.Wake("B", 0, 1))
	mustOK(t, l.Wake("C", 0, 1)) // 1/2 + 1/3 + 1/6 = 1
	mustTotal(t, l, "1")

	// 再加任何带宽都会被拒。
	mustOK(t, l.Add("D", 1, 100))
	mustKind(t, l.Wake("D", 0, 1), ErrBandwidth)
}

// 已释放服务器的带宽立即被他人使用。
func TestReleasedBandwidthReusedImmediately(t *testing.T) {
	l := NewLedger()
	mustOK(t, l.Add("A", 1, 1))
	mustOK(t, l.Add("B", 1, 1))

	mustOK(t, l.Wake("A", 0, 1))
	mustOK(t, l.Run("A", 0, 1)) // q 补回 1，d=2，w=0，clock=1
	// 1·1=1 ≥ (2−1)·1=1 → 已释放，带宽立即可用。
	mustState(t, l, "A", Released, 1, 2, 0)
	mustTotal(t, l, "0")

	mustOK(t, l.Wake("B", 1, 1))
	mustTotal(t, l, "1")
	mustState(t, l, "B", Ready, 1, 2, 1)
}

// 非最早截止被拒；并列时按编号字节序。
func TestNotEarliestDeadline(t *testing.T) {
	l := NewLedger()
	mustOK(t, l.Add("A", 1, 10))
	mustOK(t, l.Add("B", 1, 5))
	mustOK(t, l.Add("C", 1, 10))

	mustOK(t, l.Wake("A", 0, 1)) // d=10
	mustOK(t, l.Wake("B", 0, 1)) // d=5
	mustOK(t, l.Wake("C", 0, 1)) // d=10

	mustKind(t, l.Run("A", 0, 1), ErrNotEarliest) // B 的 d=5 最早
	mustNext(t, l, "B", true)
	mustOK(t, l.Run("B", 0, 1)) // B 运行完，w=0

	// A、C 均 d=10 并列，字节序 A 最小。
	mustNext(t, l, "A", true)
	mustKind(t, l.Run("C", 1, 1), ErrNotEarliest)
	mustOK(t, l.Run("A", 1, 1))
	mustOK(t, l.Run("C", 2, 1))
	mustNext(t, l, "", false)
}

// 乘积溢出 64 位：零松弛判定必须使用 128 位精度。
func TestZeroSlackOverflow64Bit(t *testing.T) {
	l := NewLedger()
	mustOK(t, l.Add("A", 1000, 1_000_000))

	// 累加 w = 1e13（每次 Wake 最多 1e9）。
	for i := int64(0); i < 10_000; i++ {
		mustOK(t, l.Wake("A", 0, 1_000_000_000))
	}
	// 一次运行 1e13 个单位：补充 k = 1e10 次，d = 1e6 + 1e10·1e6。
	mustOK(t, l.Run("A", 0, 10_000_000_000_000))

	v, err := l.State("A")
	mustOK(t, err)
	wantD := big.NewInt(10_000_000_001_000_000) // 1e16 + 1e6
	if v.Budget != 1000 || v.Deadline.Cmp(wantD) != 0 || v.Work != 0 {
		t.Fatalf("State(A) = (q=%d, d=%s, w=%d)，期望 (q=1000, d=%s, w=0)",
			v.Budget, v.Deadline, v.Work, wantD)
	}
	// (d−now)·Q = (1e16+1e6−1e13)·1000 ≈ 9.99e18，超过 int64 上限。
	product := new(big.Int).Sub(v.Deadline, big.NewInt(l.Clock()))
	product.Mul(product, big.NewInt(1000))
	if product.Cmp(big.NewInt(math.MaxInt64)) <= 0 {
		t.Fatalf("乘积 %s 未超过 int64，测试未构造出溢出场景", product)
	}
	// q·P = 1e9 远小于该乘积 → 空闲占用；64 位溢出回绕会误判为已释放。
	if v.State != Idle {
		t.Fatalf("State(A) = %s，期望空闲占用（乘积 %s 溢出 64 位）", v.State, product)
	}
	mustTotal(t, l, "1/1000")
}

// 拒绝原因按优先级只报第一个。
func TestErrorPriority(t *testing.T) {
	l := NewLedger()
	mustOK(t, l.Add("A", 1, 2))
	mustOK(t, l.Wake("A", 0, 1))
	mustOK(t, l.Run("A", 0, 1)) // clock=1，A 空闲占用

	// 参数非法优先于编号不存在。
	mustKind(t, l.Wake("", 0, 1), ErrInvalidParam)
	mustKind(t, l.Run("", 1, 1), ErrInvalidParam)
	mustKind(t, l.Remove(""), ErrInvalidParam)
	// 编号不存在优先于时钟回退。
	mustKind(t, l.Wake("nope", 0, 1), ErrNotFound)
	mustKind(t, l.Run("nope", 0, 1), ErrNotFound)
	mustKind(t, l.Remove("nope"), ErrNotFound)
	// 时钟回退。
	mustKind(t, l.Wake("A", 0, 1), ErrClockRewind)
	mustKind(t, l.Run("A", 0, 1), ErrClockRewind)
	// 占用中。
	mustKind(t, l.Remove("A"), ErrBusy)
}

// Add 只可能报参数非法、编号重复、容量已满。
func TestAddErrors(t *testing.T) {
	l := NewLedger()
	mustKind(t, l.Add("", 1, 1), ErrInvalidParam)
	mustKind(t, l.Add("A", 0, 1), ErrInvalidParam)
	mustKind(t, l.Add("A", 2, 1), ErrInvalidParam) // Q > P
	mustKind(t, l.Add("A", 1, MaxQP+1), ErrInvalidParam)
	mustOK(t, l.Add("A", 1, 1))
	mustKind(t, l.Add("A", 1, 1), ErrDuplicate)

	for i := 1; i < MaxServers; i++ {
		mustOK(t, l.Add(fmt.Sprintf("S%02d", i), 1, MaxQP))
	}
	// 容量已满时，编号重复仍优先报告。
	mustKind(t, l.Add("A", 1, 1), ErrDuplicate)
	mustKind(t, l.Add("extra", 1, 1), ErrCapacityFull)
}

// Run 的参数非法与不可运行。
func TestRunErrors(t *testing.T) {
	l := NewLedger()
	mustOK(t, l.Add("A", 2, 5))
	mustOK(t, l.Wake("A", 0, 3))

	mustKind(t, l.Run("A", 0, 0), ErrInvalidParam) // δ < 1
	mustKind(t, l.Run("A", 0, 4), ErrNotRunnable)  // δ > w
	mustOK(t, l.Run("A", 0, 3))
	mustKind(t, l.Run("A", 3, 1), ErrNotRunnable) // w=0，非就绪
}

// Wake 的参数非法：空编号、work 越界、累加后 w 超过 1e15。
func TestWakeInvalidParams(t *testing.T) {
	l := NewLedger()
	mustOK(t, l.Add("A", 1, MaxQP))

	mustKind(t, l.Wake("A", 0, 0), ErrInvalidParam)
	mustKind(t, l.Wake("A", 0, MaxWork+1), ErrInvalidParam)
	mustKind(t, l.Wake("A", -1, 1), ErrInvalidParam)
	mustKind(t, l.Wake("A", MaxBacklog+1, 1), ErrInvalidParam)

	// 累加 w 到恰好 1e15。
	for i := int64(0); i < MaxBacklog/MaxWork; i++ {
		mustOK(t, l.Wake("A", 0, MaxWork))
	}
	mustState(t, l, "A", Ready, 1, MaxQP, MaxBacklog)
	mustKind(t, l.Wake("A", 0, 1), ErrInvalidParam) // 再累加即越界
}

// Remove 仅已释放者可删。
func TestRemove(t *testing.T) {
	l := NewLedger()
	mustOK(t, l.Add("A", 1, 2))
	mustOK(t, l.Add("B", 1, 100))

	mustOK(t, l.Wake("A", 0, 1))
	mustKind(t, l.Remove("A"), ErrBusy) // 就绪
	mustOK(t, l.Run("A", 0, 1))         // q 补回 1，d=4，clock=1；2 < (4−1)·1=3 → 空闲占用
	mustKind(t, l.Remove("A"), ErrBusy)

	mustOK(t, l.Wake("B", 2, 1)) // 推进时钟到 2：2 ≥ (4−2)·1=2 → 已释放
	mustOK(t, l.Remove("A"))
	if _, err := l.State("A"); errKind(err) != ErrNotFound {
		t.Fatalf("删除后 State(A) 期望编号不存在，得到 %v", err)
	}
	mustTotal(t, l, "1/100")
}

// 并发调用：所有操作与查询可并发，结果等价于某个串行顺序
// （互斥锁串行化；配合 -race 验证无数据竞争，不变量始终成立）。
func TestConcurrentAccess(t *testing.T) {
	l := NewLedger()
	ids := []string{"a", "b", "c", "d"}
	for i, id := range ids {
		mustOK(t, l.Add(id, int64(1+i), 8))
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				id := ids[(g+i)%len(ids)]
				switch i % 5 {
				case 0:
					_ = l.Wake(id, l.Clock(), 1+int64(i%7))
				case 1:
					_ = l.Run(id, l.Clock(), 1)
				case 2:
					_, _ = l.State(id)
				case 3:
					_ = l.Total()
				case 4:
					_, _ = l.Next()
				}
			}
		}(g)
	}
	wg.Wait()
	checkInvariants(t, l)
}
