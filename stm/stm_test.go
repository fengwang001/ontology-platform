package stm

import (
	"errors"
	"math/rand"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// readAll 在一个事务里读出所有变量的当前值。
func readAll(s *STM, vars ...*TVar) ([]int, error) {
	out := make([]int, len(vars))
	err := s.Atomically(func(tx *Txn) error {
		for i, v := range vars {
			out[i] = tx.Read(v)
		}
		return nil
	})
	return out, err
}

// TestTransferInvariant 大量执行体并发转账，事务体内（包括将被
// 作废重跑的执行）看到的账户和必须恒等于常数。
func TestTransferInvariant(t *testing.T) {
	s := New()
	const nAcc = 8
	const per = 100
	const total = nAcc * per
	accs := make([]*TVar, nAcc)
	for i := range accs {
		accs[i] = s.NewVar(per)
	}

	var bad atomic.Int64 // 观察到不变量被破坏的执行次数
	const workers = 16
	const iters = 500
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < iters; i++ {
				from, to := rng.Intn(nAcc), rng.Intn(nAcc)
				if from == to {
					continue
				}
				amt := rng.Intn(10) + 1
				err := s.Atomically(func(tx *Txn) error {
					sum := 0
					for _, a := range accs {
						sum += tx.Read(a)
					}
					if sum != total {
						bad.Add(1) // 任何一次执行看到不一致都记录
					}
					fb := tx.Read(accs[from])
					tb := tx.Read(accs[to])
					if fb >= amt {
						tx.Write(accs[from], fb-amt)
						tx.Write(accs[to], tb+amt)
					}
					return nil
				})
				if err != nil {
					t.Errorf("转账事务返回错误: %v", err)
					return
				}
			}
		}(int64(w))
	}
	wg.Wait()

	vals, err := readAll(s, accs...)
	if err != nil {
		t.Fatalf("最终对账事务失败: %v", err)
	}
	final := 0
	for _, v := range vals {
		final += v
	}
	t.Logf("输入: %d 个账户各 %d，%d 个执行体各 %d 次随机转账", nAcc, per, workers, iters)
	t.Logf("输出: 不一致执行次数=%d，最终各账户=%v，最终和=%d", bad.Load(), vals, final)
	t.Logf("判定依据: 任意执行（含将被重跑者）看到的和恒等于 %d，且最终和不变", total)
	if bad.Load() != 0 {
		t.Errorf("有 %d 次执行看到账户和不等于 %d", bad.Load(), total)
	}
	if final != total {
		t.Errorf("最终和 %d != %d", final, total)
	}
}

var errDone = errors.New("done")

// TestProducerConsumerRetry 消费者在没有物品时阻塞重试，
// 生产者每次提交写入都必须唤醒消费者，不丢失唤醒。
func TestProducerConsumerRetry(t *testing.T) {
	s := New()
	stock := s.NewVar(0)    // 库存
	consumed := s.NewVar(0) // 已消费总数
	const items = 200
	const consumers = 4

	var wg sync.WaitGroup
	for c := 0; c < consumers; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				err := s.Atomically(func(tx *Txn) error {
					if tx.Read(consumed) == items {
						return errDone
					}
					n := tx.Read(stock)
					if n == 0 {
						tx.Retry() // 阻塞到 stock 或 consumed 被写入
					}
					tx.Write(stock, n-1)
					tx.Write(consumed, tx.Read(consumed)+1)
					return nil
				})
				if errors.Is(err, errDone) {
					return
				}
				if err != nil {
					t.Errorf("消费者事务错误: %v", err)
					return
				}
			}
		}()
	}

	// 生产者：逐个生产，中间故意停顿，让消费者充分阻塞在重试上。
	for i := 0; i < items; i++ {
		if err := s.Atomically(func(tx *Txn) error {
			tx.Write(stock, tx.Read(stock)+1)
			return nil
		}); err != nil {
			t.Fatalf("生产者事务错误: %v", err)
		}
		if i%7 == 0 {
			time.Sleep(time.Millisecond)
		}
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("消费者未被全部唤醒（丢失唤醒或死锁）")
	}

	vals, _ := readAll(s, stock, consumed)
	t.Logf("输入: 生产 %d 件，%d 个消费者阻塞重试消费", items, consumers)
	t.Logf("输出: 剩余库存=%d，已消费=%d", vals[0], vals[1])
	t.Logf("判定依据: 已消费 == 生产数，库存 == 0，且全部消费者在超时前完成")
	if vals[0] != 0 || vals[1] != items {
		t.Errorf("库存=%d 已消费=%d，期望 0 和 %d", vals[0], vals[1], items)
	}
}

// TestOrElseRightSelected 左分支重试时右分支被选中，且看不到左分支的写。
func TestOrElseRightSelected(t *testing.T) {
	s := New()
	v := s.NewVar(0) // 左分支读它后重试
	x := s.NewVar(0) // 左分支先写它再重试
	w := s.NewVar(0) // 右分支写它

	var xSeenByRight int
	err := s.Atomically(OrElse(
		func(tx *Txn) error { // 左：写 x 后读 v 并重试
			tx.Write(x, 999)
			tx.Read(v)
			tx.Retry()
			return nil
		},
		func(tx *Txn) error { // 右：读 x（应看不到左的写），写 w
			xSeenByRight = tx.Read(x)
			tx.Write(w, 42)
			return nil
		},
	))
	if err != nil {
		t.Fatalf("事务返回错误: %v", err)
	}
	vals, _ := readAll(s, x, w)
	t.Logf("输入: 左分支写 x=999 后重试，右分支读 x 并写 w=42")
	t.Logf("输出: 右分支读到的 x=%d，提交后 x=%d，w=%d", xSeenByRight, vals[0], vals[1])
	t.Logf("判定依据: 右分支被选中；左分支的写被撤销（x 仍为 0，右分支读到 0）；w=42")
	if xSeenByRight != 0 {
		t.Errorf("右分支看到左分支的写 x=%d，期望 0", xSeenByRight)
	}
	if vals[0] != 0 || vals[1] != 42 {
		t.Errorf("提交后 x=%d w=%d，期望 0 和 42", vals[0], vals[1])
	}
}

// TestOrElseLeftReadSetWakes 两分支都重试时整体阻塞，读集为并集：
// 左分支读过的变量被写入后必须唤醒整个事务。
func TestOrElseLeftReadSetWakes(t *testing.T) {
	s := New()
	y := s.NewVar(0) // 仅左分支读它
	z := s.NewVar(0) // 仅右分支读它
	x := s.NewVar(0) // 左分支写它

	body := OrElse(
		func(tx *Txn) error { // 左：写 x，读 y，未就绪则重试
			tx.Write(x, 999)
			if tx.Read(y) == 0 {
				tx.Retry()
			}
			return nil
		},
		func(tx *Txn) error { // 右：读 z，未就绪则重试
			if tx.Read(z) == 0 {
				tx.Retry()
			}
			return nil
		},
	)

	finished := make(chan error, 1)
	go func() { finished <- s.Atomically(body) }()

	// 等事务进入阻塞，期间写无关变量不应唤醒它使其完成。
	time.Sleep(50 * time.Millisecond)
	vals, _ := readAll(s, x)
	t.Logf("阻塞期间读到的 x=%d（判定依据: 左分支的写已撤销，应为 0）", vals[0])
	if vals[0] != 0 {
		t.Errorf("左分支的写未撤销：x=%d，期望 0", vals[0])
	}
	select {
	case <-finished:
		t.Fatal("事务不应在完成条件满足前结束")
	default:
	}

	// 写左分支读过的 y：必须唤醒被阻塞的事务。
	if err := s.Atomically(func(tx *Txn) error {
		tx.Write(y, 1)
		return nil
	}); err != nil {
		t.Fatalf("唤醒写入失败: %v", err)
	}

	select {
	case err := <-finished:
		if err != nil {
			t.Fatalf("被唤醒的事务返回错误: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("左分支读过的变量被写入后事务未被唤醒")
	}

	vals, _ = readAll(s, x, y, z)
	t.Logf("输入: 左分支写 x=999 读 y 重试，右分支读 z 重试；随后另一事务写 y=1")
	t.Logf("输出: 唤醒后提交 x=%d y=%d z=%d", vals[0], vals[1], vals[2])
	t.Logf("判定依据: 读集为两分支并集，写 y 唤醒整体；重跑后左分支成功，x=999")
	if vals[0] != 999 {
		t.Errorf("唤醒重跑后 x=%d，期望 999（左分支成功提交）", vals[0])
	}
}

// TestRetryEmptyReadSet 读集为空的重试立即报永久阻塞并中止。
func TestRetryEmptyReadSet(t *testing.T) {
	s := New()
	v := s.NewVar(7)
	err := s.Atomically(func(tx *Txn) error {
		tx.Write(v, 99) // 写不入读集
		tx.Retry()
		return nil
	})
	vals, _ := readAll(s, v)
	t.Logf("输入: 事务只写不读后重试")
	t.Logf("输出: 错误=%v，v=%d", err, vals[0])
	t.Logf("判定依据: 错误为 ErrPermanentlyBlocked，写不生效（v 仍为 7）")
	if !errors.Is(err, ErrPermanentlyBlocked) {
		t.Errorf("错误=%v，期望 ErrPermanentlyBlocked", err)
	}
	if vals[0] != 7 {
		t.Errorf("v=%d，期望 7（中止后写不生效）", vals[0])
	}
}

// TestTooManyVars 一次执行访问超过 64 个不同变量即中止且不产生任何写。
func TestTooManyVars(t *testing.T) {
	s := New()
	vars := make([]*TVar, MaxVars+1)
	for i := range vars {
		vars[i] = s.NewVar(i)
	}
	err := s.Atomically(func(tx *Txn) error {
		tx.Write(vars[0], -1) // 此写必须被撤销
		for _, v := range vars {
			tx.Read(v)
		}
		return nil
	})
	vals, _ := readAll(s, vars[0])
	t.Logf("输入: 一次执行访问 %d 个不同变量（上限 %d）", MaxVars+1, MaxVars)
	t.Logf("输出: 错误=%v，vars[0]=%d", err, vals[0])
	t.Logf("判定依据: 错误为 ErrTooManyVars，事务中止且 vars[0] 仍为 0")
	if !errors.Is(err, ErrTooManyVars) {
		t.Errorf("错误=%v，期望 ErrTooManyVars", err)
	}
	if vals[0] != 0 {
		t.Errorf("vars[0]=%d，期望 0（中止后写不生效）", vals[0])
	}
}

// TestMisuse 句柄结束后使用、事务体内再启动事务、使用外部实例变量。
func TestMisuse(t *testing.T) {
	s := New()
	v := s.NewVar(1)

	// 1. 事务结束后仍使用其句柄。
	var leaked *Txn
	if err := s.Atomically(func(tx *Txn) error { leaked = tx; return nil }); err != nil {
		t.Fatalf("事务失败: %v", err)
	}
	func() {
		defer func() {
			r := recover()
			t.Logf("结束后使用句柄: panic=%v（判定依据: 应为 ErrTxnDone）", r)
			if r != ErrTxnDone {
				t.Errorf("panic=%v，期望 ErrTxnDone", r)
			}
		}()
		leaked.Read(v)
	}()

	// 2. 事务体内再启动事务。
	err := s.Atomically(func(tx *Txn) error {
		tx.Write(v, 100) // 必须被撤销
		return s.Atomically(func(inner *Txn) error { return nil })
	})
	vals, _ := readAll(s, v)
	t.Logf("嵌套启动事务: 错误=%v，v=%d（判定依据: ErrNested 且写不生效）", err, vals[0])
	if !errors.Is(err, ErrNested) {
		t.Errorf("错误=%v，期望 ErrNested", err)
	}
	if vals[0] != 1 {
		t.Errorf("v=%d，期望 1", vals[0])
	}

	// 3. 使用属于另一个内存实例的变量。
	other := New()
	foreign := other.NewVar(5)
	err = s.Atomically(func(tx *Txn) error {
		tx.Read(foreign)
		return nil
	})
	t.Logf("使用外部变量: 错误=%v（判定依据: 应为 ErrForeignVar）", err)
	if !errors.Is(err, ErrForeignVar) {
		t.Errorf("错误=%v，期望 ErrForeignVar", err)
	}
}

// TestDeterministicSequential 相同的顺序执行得到相同结果。
func TestDeterministicSequential(t *testing.T) {
	run := func() []int {
		s := New()
		a, b := s.NewVar(10), s.NewVar(20)
		for i := 0; i < 50; i++ {
			if err := s.Atomically(func(tx *Txn) error {
				av, bv := tx.Read(a), tx.Read(b)
				tx.Write(a, av+1)
				tx.Write(b, bv-1)
				return nil
			}); err != nil {
				t.Fatalf("事务失败: %v", err)
			}
		}
		vals, _ := readAll(s, a, b)
		return vals
	}
	first, second := run(), run()
	t.Logf("输入: 同一串顺序事务执行两遍；输出: 第一遍=%v，第二遍=%v", first, second)
	t.Logf("判定依据: 两遍结果完全相同")
	if first[0] != second[0] || first[1] != second[1] {
		t.Errorf("两遍结果不同: %v vs %v", first, second)
	}
}

// TestAbortSemantics 返回错误或发生恐慌：事务中止、写不生效、原样上抛。
func TestAbortSemantics(t *testing.T) {
	s := New()
	v := s.NewVar(1)

	sentinel := errors.New("业务错误")
	err := s.Atomically(func(tx *Txn) error {
		tx.Write(v, 100)
		return sentinel
	})
	vals, _ := readAll(s, v)
	t.Logf("返回错误: err=%v，v=%d（判定依据: 错误原样返回且写不生效）", err, vals[0])
	if err != sentinel {
		t.Errorf("err=%v，期望原样返回 %v", err, sentinel)
	}
	if vals[0] != 1 {
		t.Errorf("v=%d，期望 1", vals[0])
	}

	func() {
		defer func() {
			r := recover()
			vals, _ := readAll(s, v)
			t.Logf("发生恐慌: panic=%v，v=%d（判定依据: 恐慌原样上抛且写不生效）", r, vals[0])
			if r != "boom" {
				t.Errorf("panic=%v，期望 \"boom\"", r)
			}
			if vals[0] != 1 {
				t.Errorf("v=%d，期望 1", vals[0])
			}
		}()
		_ = s.Atomically(func(tx *Txn) error {
			tx.Write(v, 100)
			panic("boom")
		})
	}()
}
