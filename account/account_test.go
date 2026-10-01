package account

import (
	"errors"
	"fmt"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
)

func mustAddAccount(t *testing.T, l *Ledger, id string, b, lo, hi int64, m int) {
	t.Helper()
	if err := l.AddAccount(id, b, lo, hi, m); err != nil {
		t.Fatalf("AddAccount(%s, b=%d, L=%d, H=%d, M=%d) = %v", id, b, lo, hi, m, err)
	}
}

func mustReserve(t *testing.T, l *Ledger, txID, accID string, d int64) {
	t.Helper()
	if err := l.Reserve(txID, accID, d); err != nil {
		t.Fatalf("Reserve(%s, %s, %d) = %v", txID, accID, d, err)
	}
}

// readInterval 读取账户可能区间：无未决项时区间退化为 [bal, bal]。
func readInterval(t *testing.T, l *Ledger, accID string) (int64, int64) {
	t.Helper()
	bal, err := l.Read(accID)
	if err == nil {
		return bal, bal
	}
	var ue *UncertainError
	if errors.As(err, &ue) {
		return ue.Lo, ue.Hi
	}
	t.Fatalf("Read(%s) = %v", accID, err)
	return 0, 0
}

// TestIndependentSides 对应需求用例：b=50, L=0, H=100，
// 同一事务先预留 -50 再预留 +50 后再预留 -1 仍因下界不足被拒。
// 判定依据：正负两侧独立求和、互不抵消；-50 与 +50 之后区间为
// [50-50, 50+50] = [0, 100]，再预留 -1 使下端变为 -1 < L=0。
func TestIndependentSides(t *testing.T) {
	l := NewLedger()
	mustAddAccount(t, l, "a", 50, 0, 100, 10)

	mustReserve(t, l, "tx1", "a", -50)
	lo, hi := readInterval(t, l, "a")
	t.Logf("输入: Reserve(tx1, a, -50) 输出: 接受 区间: [%d, %d] 依据: 下端 50-50=0 >= L=0", lo, hi)
	if lo != 0 || hi != 50 {
		t.Fatalf("区间 = [%d, %d], 期望 [0, 50]", lo, hi)
	}

	mustReserve(t, l, "tx1", "a", 50)
	lo, hi = readInterval(t, l, "a")
	t.Logf("输入: Reserve(tx1, a, +50) 输出: 接受 区间: [%d, %d] 依据: 上端 50+50=100 <= H=100；与负侧互不抵消", lo, hi)
	if lo != 0 || hi != 100 {
		t.Fatalf("区间 = [%d, %d], 期望 [0, 100]", lo, hi)
	}

	err := l.Reserve("tx1", "a", -1)
	t.Logf("输入: Reserve(tx1, a, -1) 输出: %v 依据: 下端 0-1=-1 < L=0", err)
	if !errors.Is(err, ErrBelowLower) {
		t.Fatalf("Reserve(tx1, a, -1) = %v, 期望 %v", err, ErrBelowLower)
	}

	lo, hi = readInterval(t, l, "a")
	if lo != 0 || hi != 100 {
		t.Fatalf("被拒后区间 = [%d, %d], 期望仍为 [0, 100]（被拒绝的操作不得改变状态）", lo, hi)
	}
}

// TestOneSidedCheck 验证接受时只检查所增减一侧的边界：
// 下端已贴 L 时正增量仍被接受，上端已贴 H 时负增量仍被接受。
func TestOneSidedCheck(t *testing.T) {
	l := NewLedger()
	mustAddAccount(t, l, "low", 50, 0, 200, 10)
	mustAddAccount(t, l, "high", 50, -100, 100, 10)

	mustReserve(t, l, "tx1", "low", -50)
	mustReserve(t, l, "tx2", "low", 30)
	lo, hi := readInterval(t, l, "low")
	t.Logf("输入: low 预留 -50 后 +30 输出: 接受 区间: [%d, %d] 依据: 正增量只查上端 50+30=80 <= H=200，不查已贴 L 的下端", lo, hi)
	if lo != 0 || hi != 80 {
		t.Fatalf("low 区间 = [%d, %d], 期望 [0, 80]", lo, hi)
	}

	mustReserve(t, l, "tx3", "high", 50)
	mustReserve(t, l, "tx4", "high", -30)
	lo, hi = readInterval(t, l, "high")
	t.Logf("输入: high 预留 +50 后 -30 输出: 接受 区间: [%d, %d] 依据: 负增量只查下端 50-30=20 >= L=-100，不查已贴 H 的上端", lo, hi)
	if lo != 20 || hi != 100 {
		t.Fatalf("high 区间 = [%d, %d], 期望 [20, 100]", lo, hi)
	}

	err := l.Reserve("tx5", "low", -1)
	t.Logf("输入: Reserve(tx5, low, -1) 输出: %v 依据: 下端 0-1=-1 < L=0", err)
	if !errors.Is(err, ErrBelowLower) {
		t.Fatalf("Reserve(tx5, low, -1) = %v, 期望 %v", err, ErrBelowLower)
	}
	err = l.Reserve("tx6", "high", 1)
	t.Logf("输入: Reserve(tx6, high, +1) 输出: %v 依据: 上端 100+1=101 > H=100", err)
	if !errors.Is(err, ErrAboveUpper) {
		t.Fatalf("Reserve(tx6, high, +1) = %v, 期望 %v", err, ErrAboveUpper)
	}
}

// TestPendingLimit 验证未决项恰达上限 M 时拒绝新预留，
// 中止或提交释放槽位后可再次预留；被拒的预留不改变状态。
func TestPendingLimit(t *testing.T) {
	l := NewLedger()
	mustAddAccount(t, l, "a", 50, 0, 100, 2)

	mustReserve(t, l, "tx1", "a", 10)
	mustReserve(t, l, "tx2", "a", 10)
	lo, hi := readInterval(t, l, "a")
	t.Logf("输入: M=2 预留两笔 +10 输出: 接受 区间: [%d, %d] 依据: 未决项数 2 == M", lo, hi)

	err := l.Reserve("tx3", "a", 10)
	t.Logf("输入: 第 3 笔 Reserve(tx3, a, +10) 输出: %v 依据: 未决项数已达 M=2", err)
	if !errors.Is(err, ErrTooManyPending) {
		t.Fatalf("Reserve(tx3, a, 10) = %v, 期望 %v", err, ErrTooManyPending)
	}
	lo, hi = readInterval(t, l, "a")
	if lo != 50 || hi != 70 {
		t.Fatalf("被拒后区间 = [%d, %d], 期望仍为 [50, 70]", lo, hi)
	}

	if err := l.Abort("tx1"); err != nil {
		t.Fatalf("Abort(tx1) = %v", err)
	}
	mustReserve(t, l, "tx3", "a", 10)
	t.Logf("输入: Abort(tx1) 后 Reserve(tx3, a, +10) 输出: 接受 依据: 中止释放了一个未决槽位")

	if err := l.Commit("tx2"); err != nil {
		t.Fatalf("Commit(tx2) = %v", err)
	}
	mustReserve(t, l, "tx4", "a", 10)
	t.Logf("输入: Commit(tx2) 后 Reserve(tx4, a, +10) 输出: 接受 依据: 提交同样释放未决槽位")
}

// TestPreciseRead 验证精确读：无未决项返回余额，有未决项报「余额不确定」并附区间。
func TestPreciseRead(t *testing.T) {
	l := NewLedger()
	mustAddAccount(t, l, "a", 50, 0, 100, 10)

	bal, err := l.Read("a")
	t.Logf("输入: Read(a)（无未决项） 输出: %d, %v 依据: 无未决项返回精确余额", bal, err)
	if err != nil || bal != 50 {
		t.Fatalf("Read(a) = %d, %v; 期望 50, nil", bal, err)
	}

	mustReserve(t, l, "tx1", "a", 10)
	mustReserve(t, l, "tx2", "a", -20)
	_, err = l.Read("a")
	var ue *UncertainError
	if !errors.As(err, &ue) {
		t.Fatalf("Read(a) err = %v, 期望 *UncertainError", err)
	}
	t.Logf("输入: Read(a)（有未决项） 输出: %v 依据: 区间 [50-20, 50+10]", err)
	if ue.Lo != 30 || ue.Hi != 60 {
		t.Fatalf("区间 = [%d, %d], 期望 [30, 60]", ue.Lo, ue.Hi)
	}

	if err := l.Commit("tx1"); err != nil {
		t.Fatalf("Commit(tx1) = %v", err)
	}
	_, err = l.Read("a")
	if !errors.As(err, &ue) || ue.Lo != 40 || ue.Hi != 60 {
		t.Fatalf("提交 tx1 后 Read(a) = %v, 期望区间 [40, 60]", err)
	}
	t.Logf("输入: Commit(tx1) 后 Read(a) 输出: %v 依据: tx2 的 -20 仍未决", err)

	if err := l.Abort("tx2"); err != nil {
		t.Fatalf("Abort(tx2) = %v", err)
	}
	bal, err = l.Read("a")
	t.Logf("输入: Abort(tx2) 后 Read(a) 输出: %d, %v 依据: 未决项清空，余额=50+10", bal, err)
	if err != nil || bal != 60 {
		t.Fatalf("Read(a) = %d, %v; 期望 60, nil", bal, err)
	}
}

// TestErrorOrder 验证预留的非法情形按固定顺序只报第一个：
// 事务已终结 > 账户不存在 > 增量为零 > 未决项达上限 > 边界不足。
func TestErrorOrder(t *testing.T) {
	l := NewLedger()
	mustAddAccount(t, l, "a", 50, 0, 100, 1)
	mustReserve(t, l, "done", "a", 10)
	if err := l.Commit("done"); err != nil {
		t.Fatalf("Commit(done) = %v", err)
	}
	mustReserve(t, l, "open", "a", 10) // 占满 M=1

	check := func(want error, txID, accID string, d int64, why string) {
		t.Helper()
		err := l.Reserve(txID, accID, d)
		t.Logf("输入: Reserve(%s, %s, %d) 输出: %v 依据: %s", txID, accID, d, err, why)
		if !errors.Is(err, want) {
			t.Fatalf("Reserve(%s, %s, %d) = %v, 期望 %v", txID, accID, d, err, want)
		}
	}
	check(ErrTxCommitted, "done", "zz", 0, "事务已提交优先于账户不存在与零增量")
	check(ErrAccountNotFound, "tx1", "zz", 0, "账户不存在优先于零增量")
	check(ErrZeroDelta, "tx1", "a", 0, "零增量优先于未决上限")
	check(ErrTooManyPending, "tx1", "a", 1000, "未决上限优先于边界检查")
	if err := l.Abort("open"); err != nil {
		t.Fatalf("Abort(open) = %v", err)
	}
	check(ErrTxAborted, "open", "a", 1, "事务已中止同样终结")

	if err := l.Commit("ghost"); !errors.Is(err, ErrTxNeverReserved) {
		t.Fatalf("Commit(ghost) = %v, 期望 %v", err, ErrTxNeverReserved)
	}
	if err := l.Abort("ghost"); !errors.Is(err, ErrTxNeverReserved) {
		t.Fatalf("Abort(ghost) = %v, 期望 %v", err, ErrTxNeverReserved)
	}
	if err := l.Commit("done"); !errors.Is(err, ErrTxFinished) {
		t.Fatalf("Commit(done) = %v, 期望 %v", err, ErrTxFinished)
	}
	if err := l.Abort("done"); !errors.Is(err, ErrTxFinished) {
		t.Fatalf("Abort(done) = %v, 期望 %v", err, ErrTxFinished)
	}
	t.Logf("输入: 对终结或从未预留事务的提交与中止 输出: 整体拒绝 依据: 事务编号已终结或不存在")

	bal, err := l.Read("a")
	if err != nil || bal != 60 {
		t.Fatalf("全部被拒后 Read(a) = %d, %v; 期望 60, nil（被拒操作不得改变状态）", bal, err)
	}
}

// TestCrossAccountTx 验证事务可跨账户预留，提交对全部账户原子生效，
// 且某次预留被拒不影响先前已接受的预留。
func TestCrossAccountTx(t *testing.T) {
	l := NewLedger()
	mustAddAccount(t, l, "a", 50, 0, 100, 10)
	mustAddAccount(t, l, "b", 100, 0, 200, 10)

	mustReserve(t, l, "tx1", "a", -20)
	mustReserve(t, l, "tx1", "b", 30)
	err := l.Reserve("tx1", "b", 1000)
	t.Logf("输入: tx1 跨 a/b 预留后 Reserve(tx1, b, +1000) 输出: %v 依据: 上端 100+30+1000 > H=200；先前预留不受影响", err)
	if !errors.Is(err, ErrAboveUpper) {
		t.Fatalf("Reserve(tx1, b, 1000) = %v, 期望 %v", err, ErrAboveUpper)
	}

	if err := l.Commit("tx1"); err != nil {
		t.Fatalf("Commit(tx1) = %v", err)
	}
	balA, errA := l.Read("a")
	balB, errB := l.Read("b")
	t.Logf("输入: Commit(tx1) 输出: a=%d b=%d 依据: 提交对全部账户原子生效", balA, balB)
	if errA != nil || balA != 30 || errB != nil || balB != 130 {
		t.Fatalf("提交后 a=%d(%v) b=%d(%v), 期望 a=30 b=130", balA, errA, balB, errB)
	}
}

// TestAddAccountValidation 验证账户参数校验：L<=b<=H 且 M>=1，编号不得重复。
func TestAddAccountValidation(t *testing.T) {
	l := NewLedger()
	if err := l.AddAccount("bad1", 50, 51, 100, 1); !errors.Is(err, ErrInvalidAccount) {
		t.Fatalf("L>b 应报参数非法, 得到 %v", err)
	}
	if err := l.AddAccount("bad2", 50, 0, 49, 1); !errors.Is(err, ErrInvalidAccount) {
		t.Fatalf("b>H 应报参数非法, 得到 %v", err)
	}
	if err := l.AddAccount("bad3", 50, 0, 100, 0); !errors.Is(err, ErrInvalidAccount) {
		t.Fatalf("M=0 应报参数非法, 得到 %v", err)
	}
	mustAddAccount(t, l, "a", 50, 0, 100, 1)
	if err := l.AddAccount("a", 50, 0, 100, 1); !errors.Is(err, ErrAccountExists) {
		t.Fatalf("重复编号应报账户已存在, 得到 %v", err)
	}

	mustAddAccount(t, l, "eq", 5, 5, 5, 1) // L=b=H 合法
	if err := l.Reserve("tx1", "eq", 1); !errors.Is(err, ErrAboveUpper) {
		t.Fatalf("L=b=H 时正增量应报上界超出, 得到 %v", err)
	}
	if err := l.Reserve("tx2", "eq", -1); !errors.Is(err, ErrBelowLower) {
		t.Fatalf("L=b=H 时负增量应报下界不足, 得到 %v", err)
	}
	t.Logf("输入: 非法参数与 L=b=H 边界账户 输出: 参数非法整体拒绝；边界账户拒绝一切非零预留 依据: L<=b<=H 且 M>=1")
}

// permutations 按字典序生成 items 的排列，最多 limit 个。
func permutations(items []int, limit int) [][]int {
	var result [][]int
	used := make([]bool, len(items))
	current := make([]int, 0, len(items))
	var dfs func()
	dfs = func() {
		if len(result) >= limit {
			return
		}
		if len(current) == len(items) {
			result = append(result, append([]int(nil), current...))
			return
		}
		for i := range items {
			if used[i] {
				continue
			}
			used[i] = true
			current = append(current, items[i])
			dfs()
			current = current[:len(current)-1]
			used[i] = false
		}
	}
	dfs()
	return result
}

// TestExhaustiveSubsetsAndOrders 对 1..10 个未决项穷举全部提交子集；
// 子集大小 <= 6 时穷举全部提交顺序，更大子集取字典序前 720 种顺序。
// 每次重放都对拍：区间始终含于 [L, H]，模型余额始终落在区间内，
// 最终余额等于初值加全部已提交增量之和。
func TestExhaustiveSubsetsAndOrders(t *testing.T) {
	const (
		b       = int64(500)
		lo      = int64(0)
		hi      = int64(1000)
		m       = 10
		permCap = 720
	)
	for n := 1; n <= 10; n++ {
		deltas := make([]int64, n)
		txIDs := make([]string, n)
		for i := range deltas {
			d := int64((i*37)%61) - 30
			if d == 0 {
				d = 7
			}
			deltas[i] = d
			txIDs[i] = fmt.Sprintf("tx%d", i)
		}
		replays := 0
		for mask := 0; mask < (1 << n); mask++ {
			var subset []int
			for i := 0; i < n; i++ {
				if mask&(1<<i) != 0 {
					subset = append(subset, i)
				}
			}
			for _, order := range permutations(subset, permCap) {
				replays++
				l := NewLedger()
				if err := l.AddAccount("a", b, lo, hi, m); err != nil {
					t.Fatalf("AddAccount = %v", err)
				}
				for i, d := range deltas {
					if err := l.Reserve(txIDs[i], "a", d); err != nil {
						t.Fatalf("n=%d 预留 tx%d d=%d 失败: %v", n, i, d, err)
					}
				}
				committed := make([]bool, n)
				model := b
				for _, i := range order {
					if err := l.Commit(txIDs[i]); err != nil {
						t.Fatalf("n=%d Commit(tx%d) = %v, 必然成功", n, i, err)
					}
					committed[i] = true
					model += deltas[i]
					ilo, ihi := readInterval(t, l, "a")
					if ilo < lo || ihi > hi {
						t.Fatalf("n=%d mask=%b 提交 tx%d 后区间 [%d, %d] 越出 [%d, %d]",
							n, mask, i, ilo, ihi, lo, hi)
					}
					if model < ilo || model > ihi {
						t.Fatalf("n=%d mask=%b 模型余额 %d 不在区间 [%d, %d] 内",
							n, mask, model, ilo, ihi)
					}
				}
				for i := 0; i < n; i++ {
					if !committed[i] {
						if err := l.Abort(txIDs[i]); err != nil {
							t.Fatalf("n=%d Abort(tx%d) = %v", n, i, err)
						}
					}
				}
				bal, err := l.Read("a")
				if err != nil {
					t.Fatalf("n=%d mask=%b 终态 Read = %v, 期望精确余额", n, mask, err)
				}
				if bal != model {
					t.Fatalf("对拍失败: n=%d mask=%b 最终余额=%d 模型=%d", n, mask, bal, model)
				}
				if bal < lo || bal > hi {
					t.Fatalf("n=%d mask=%b 最终余额 %d 越出 [%d, %d]", n, mask, bal, lo, hi)
				}
			}
		}
		t.Logf("输入: n=%d, deltas=%v 输出: 全部子集共 %d 次重放均满足 L<=余额<=H 且等于模型值 依据: 区间守恒与提交确定性", n, deltas, replays)
	}
}

// TestConcurrentConservation 并发预留/提交/中止/读：
// 余额始终在 [L, H] 内，最终余额等于初值加全部已提交增量之和。
func TestConcurrentConservation(t *testing.T) {
	const (
		numAccounts  = 3
		numWorkers   = 8
		opsPerWorker = 300
		b            = int64(10000)
		lo           = int64(0)
		hi           = int64(20000)
		m            = 2000
	)
	l := NewLedger()
	names := []string{"a0", "a1", "a2"}
	for _, name := range names {
		mustAddAccount(t, l, name, b, lo, hi, m)
	}

	var expected [numAccounts]int64 // 各账户已提交增量之和
	var wg sync.WaitGroup
	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(w) + 1))
			for i := 0; i < opsPerWorker; i++ {
				accIdx := rng.Intn(numAccounts)
				d := int64(rng.Intn(11) - 5)
				if d == 0 {
					d = 1
				}
				txID := fmt.Sprintf("w%d-tx%d", w, i)
				if err := l.Reserve(txID, names[accIdx], d); err != nil {
					continue // 边界或上限拒绝：不改变状态，事务未建立
				}
				if rng.Intn(2) == 0 {
					if err := l.Commit(txID); err != nil {
						t.Errorf("Commit(%s) = %v, 必然成功", txID, err)
						return
					}
					atomic.AddInt64(&expected[accIdx], d)
				} else {
					if err := l.Abort(txID); err != nil {
						t.Errorf("Abort(%s) = %v", txID, err)
						return
					}
				}
			}
		}(w)
	}

	stop := make(chan struct{})
	var readers sync.WaitGroup
	for r := 0; r < 2; r++ {
		readers.Add(1)
		go func() {
			defer readers.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				for _, name := range names {
					bal, err := l.Read(name)
					if err == nil {
						if bal < lo || bal > hi {
							t.Errorf("Read(%s) = %d 越出 [%d, %d]", name, bal, lo, hi)
							return
						}
						continue
					}
					var ue *UncertainError
					if errors.As(err, &ue) && (ue.Lo < lo || ue.Hi > hi) {
						t.Errorf("Read(%s) 区间 [%d, %d] 越出 [%d, %d]", name, ue.Lo, ue.Hi, lo, hi)
						return
					}
				}
			}
		}()
	}

	wg.Wait()
	close(stop)
	readers.Wait()

	for i, name := range names {
		bal, err := l.Read(name)
		if err != nil {
			t.Fatalf("Read(%s) = %v, 全部事务终结后应为精确余额", name, err)
		}
		want := b + atomic.LoadInt64(&expected[i])
		t.Logf("输入: %d 协程并发预留/提交/中止 输出: %s 余额=%d 期望=%d 依据: 余额=初值+已提交增量之和",
			numWorkers, name, bal, want)
		if bal != want {
			t.Errorf("%s 余额 = %d, 期望 %d（守恒被破坏）", name, bal, want)
		}
		if bal < lo || bal > hi {
			t.Errorf("%s 余额 %d 越出 [%d, %d]", name, bal, lo, hi)
		}
	}
}

// TestDeterministicReplay 相同操作序列重放结果完全相同。
func TestDeterministicReplay(t *testing.T) {
	readStr := func(l *Ledger, id string) string {
		bal, err := l.Read(id)
		if err != nil {
			return err.Error()
		}
		return fmt.Sprintf("%d", bal)
	}
	script := func(l *Ledger) []string {
		var out []string
		record := func(format string, args ...any) {
			out = append(out, fmt.Sprintf(format, args...))
		}
		record("add a: %v", l.AddAccount("a", 50, 0, 100, 3))
		record("add b: %v", l.AddAccount("b", 20, -10, 40, 2))
		record("reserve tx1 a -50: %v", l.Reserve("tx1", "a", -50))
		record("reserve tx1 a +50: %v", l.Reserve("tx1", "a", 50))
		record("reserve tx1 a -1: %v", l.Reserve("tx1", "a", -1))
		record("reserve tx2 a 0: %v", l.Reserve("tx2", "a", 0))
		record("reserve tx2 zz +5: %v", l.Reserve("tx2", "zz", 5))
		record("reserve tx2 b -30: %v", l.Reserve("tx2", "b", -30))
		record("reserve tx2 b -20: %v", l.Reserve("tx2", "b", -20))
		record("reserve tx3 b +5: %v", l.Reserve("tx3", "b", 5))
		record("read a: %s", readStr(l, "a"))
		record("commit tx2: %v", l.Commit("tx2"))
		record("abort tx1: %v", l.Abort("tx1"))
		record("commit tx1: %v", l.Commit("tx1"))
		record("abort tx9: %v", l.Abort("tx9"))
		record("read a: %s", readStr(l, "a"))
		record("read b: %s", readStr(l, "b"))
		record("commit tx3: %v", l.Commit("tx3"))
		record("read b: %s", readStr(l, "b"))
		return out
	}

	first := script(NewLedger())
	second := script(NewLedger())
	if len(first) != len(second) {
		t.Fatalf("两次重放步数不同: %d vs %d", len(first), len(second))
	}
	for i := range first {
		if first[i] != second[i] {
			t.Errorf("重放第 %d 步不一致: %q vs %q", i, first[i], second[i])
		}
	}
	for _, line := range first {
		t.Logf("重放: %s", line)
	}
	t.Logf("输入: 固定操作序列 输出: 两次重放 %d 步结果完全一致 依据: 实现无随机性与时钟依赖", len(first))
}
