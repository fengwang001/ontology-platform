package idb_test

import (
	"fmt"
	"testing"
	"time"

	"ontology/idb"
)

// 性能要求一：判定新事务能否立即开始，开销不得随已结束事务数增长。
// 验证方式：制造 N 个已结束事务后，测量“创建一个会被排队的新事务
// 并检查其未开始”的耗时；它与无历史事务的基线同阶（微秒级且不随 N 增长）。
// 调度器内部只保留未结束事务（scheduler.order 立即摘除已结束事务），
// 故扫描集合大小与历史事务数无关。
func BenchmarkAdmissionIndependentOfFinished(b *testing.B) {
	for _, n := range []int{100, 1000, 5000} {
		b.Run(fmt.Sprintf("finished=%d", n), func(b *testing.B) {
			k := idb.New()
			defer k.Close()
			c := openOne(b, k, "db", 1, "a")
			for i := 0; i < n; i++ {
				if err := idb.WithTx(c, idb.ReadOnly, []string{"a"},
					func(*idb.Transaction) error { return nil }); err != nil {
					b.Fatal(err)
				}
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				k.Pause()
				holder, err := c.Transaction(idb.ReadWrite, []string{"a"})
				if err != nil {
					b.Fatal(err)
				}
				if err := holder.Hold(); err != nil {
					b.Fatal(err)
				}
				k.Resume()
				for j := 0; j < 100; j++ {
					tx, err := c.Transaction(idb.ReadOnly, []string{"a"})
					if err != nil {
						b.Fatal(err)
					}
					if tx.State() != idb.Queued {
						b.Fatalf("new tx must be queued behind holder, got %v", tx.State())
					}
					_ = tx.Abort()
				}
				_ = holder.Unhold()
				_ = holder.Wait()
			}
		})
	}
}

// 性能要求二：提交时写入可见的开销不得随仓库中与本事务无关的键数增长。
// 验证方式：仓库预置 N 个无关键后，提交一个只写一个键的事务，
// 耗时应在微秒级且对 N 不敏感（MVCC 追加：提交只遍历本事务写入集合）。
func BenchmarkCommitIndependentOfUnrelatedKeys(b *testing.B) {
	for _, n := range []int{1000, 10000, 100000} {
		b.Run(fmt.Sprintf("unrelated=%d", n), func(b *testing.B) {
			k := idb.New()
			defer k.Close()
			c := openOne(b, k, "db", 1, "a")
			if err := idb.WithTx(c, idb.ReadWrite, []string{"a"}, func(tx *idb.Transaction) error {
				for i := 0; i < n; i++ {
					if err := idb.SyncPut(tx, "a", fmt.Sprintf("seed-%08d", i), "x", false); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				b.Fatal(err)
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if err := idb.WithTx(c, idb.ReadWrite, []string{"a"}, func(tx *idb.Transaction) error {
					return idb.SyncPut(tx, "a", "hot", fmt.Sprintf("v%d", i), false)
				}); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// TestAdmissionDoesNotScanFinished 用结构性断言直接验证：
// 无论已有多少已结束事务，调度器活动集合大小只反映当前未结束事务。
func TestAdmissionDoesNotScanFinished(t *testing.T) {
	k := testKernel(t)
	c := upgrade(t, k, "db", 1, mkStores("a"))
	for i := 0; i < 2000; i++ {
		if err := idb.WithTx(c, idb.ReadOnly, []string{"a"},
			func(*idb.Transaction) error { return nil }); err != nil {
			t.Fatal(err)
		}
	}
	active := idb.ActiveTxCountForTest(k, "db")
	if active != 0 {
		t.Fatalf("scheduler retains %d finished transactions", active)
	}

	k.Pause()
	t1, _ := c.Transaction(idb.ReadWrite, []string{"a"})
	t2, _ := c.Transaction(idb.ReadOnly, []string{"a"})
	if err := t1.Hold(); err != nil {
		t.Fatal(err)
	}
	if err := t2.Hold(); err != nil {
		t.Fatal(err)
	}
	k.Resume()
	waitState(t, t1, idb.Running)
	if got := idb.ActiveTxCountForTest(k, "db"); got != 2 {
		t.Fatalf("active=%d, want 2 (independent of 2000 finished)", got)
	}
	_ = t1.Unhold()
	_ = t2.Unhold()
	_ = t1.Wait()
	_ = t2.Wait()
}

func openOne(tb testing.TB, k *idb.Kernel, name string, v int, stores ...string) *idb.Connection {
	tb.Helper()
	c, err := k.Open(name, v)
	if err != nil {
		tb.Fatal(err)
	}
	vt := c.VersionChange()
	deadlineWait(vt)
	for _, s := range stores {
		if err := c.CreateObjectStore(s); err != nil {
			tb.Fatal(err)
		}
	}
	if err := vt.Commit(); err != nil {
		tb.Fatal(err)
	}
	_ = vt.Wait()
	return c
}

func deadlineWait(tx *idb.Transaction) {
	for i := 0; i < 2000; i++ {
		if tx.State() == idb.Running {
			return
		}
		time.Sleep(time.Millisecond)
	}
}
