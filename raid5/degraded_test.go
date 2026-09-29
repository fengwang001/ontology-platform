package raid5

import (
	"errors"
	"sync"
	"testing"
)

// fillVolume 整带写满卷，每块内容可由 (stripe, slot) 确定性推导。
func fillVolume(t *testing.T, v *Volume, n, stripes int) {
	t.Helper()
	for s := 0; s < stripes; s++ {
		blocks := make([][]byte, n-1)
		for k := range blocks {
			blocks[k] = mkBlock(byte(0x40+s), k)
		}
		if err := v.Write(s*(n-1), blocks); err != nil {
			t.Fatal(err)
		}
	}
}

// TestDegradedRead 单盘失效后降级读：其余块异或还原，逐块比对原值。
func TestDegradedRead(t *testing.T) {
	for _, n := range []int{3, 4, 5} {
		n := n
		for _, failed := range []int{0, n - 1} {
			failed := failed
			t.Run("N"+itoa(n)+"_fail"+itoa(failed), func(t *testing.T) {
				stripes := 2 * n
				disks := newMemDiskSet(n, stripes)
				v := reopen(t, disks, newMemJournalFile(), n, stripes)
				fillVolume(t, v, n, stripes)
				if err := v.FailDisk(failed); err != nil {
					t.Fatal(err)
				}
				total := stripes * (n - 1)
				dst := make([][]byte, total)
				for i := range dst {
					dst[i] = make([]byte, BlockSize)
				}
				if err := v.Read(0, dst); err != nil {
					t.Fatalf("degraded read: %v", err)
				}
				for s := 0; s < stripes; s++ {
					for k := 0; k < n-1; k++ {
						want := mkBlock(byte(0x40+s), k)
						if !bytesEqualBlocks(dst[s*(n-1)+k], want) {
							t.Fatalf("stripe %d slot %d reconstructed mismatch", s, k)
						}
					}
				}
				t.Logf("判定: 盘 %d 失效后 %d 个逻辑块全部由 XOR 正确还原", failed, total)
			})
		}
	}
}

// TestDegradedWriteReplay 降级态写入后在每个断电点崩溃：
// 意图同时携带新数据与新校验，重开时整体重放，已确认写入不丢、条带一致。
func TestDegradedWriteReplay(t *testing.T) {
	for _, n := range []int{3, 4, 5} {
		n := n
		for _, point := range []CrashPoint{CrashAfterIntent, CrashAfterData, CrashAfterCommit} {
			point := point
			t.Run("N"+itoa(n)+"_"+crashName(point), func(t *testing.T) {
				stripes := 4
				disks := newMemDiskSet(n, stripes)
				jf := newMemJournalFile()
				v := reopen(t, disks, jf, n, stripes)
				fillVolume(t, v, n, stripes)
				if err := v.FailDisk(1 % n); err != nil {
					t.Fatal(err)
				}

				// 降级写：覆盖条带 2 的两个数据块（其中一个恰在失效盘）。
				s := 2
				parity, data := stripeMap(n, s)
				newVals := map[int][]byte{0: mkBlock(0x77, 0), 1: mkBlock(0x77, 1)}
				seg := [][]byte{newVals[0], newVals[1]}
				t.Logf("输入: 降级写 stripe=%d slots=0,1 parityDisk=%d dataDisks=%v 断电点=%s",
					s, parity, data, crashName(point))
				v.SetCrashHook(&CrashHook{Point: point, Stripe: s})
				err := v.Write(s*(n-1), seg)
				if !errors.Is(err, errCrashed) {
					t.Fatalf("want errCrashed got %v", err)
				}

				// 重开（仍降级）：整体重放未完成意图。
				v2 := reopen(t, disks, jf, n, stripes)
				dst := make([][]byte, 2)
				for i := range dst {
					dst[i] = make([]byte, BlockSize)
				}
				if err := v2.Read(s*(n-1), dst); err != nil {
					t.Fatal(err)
				}
				// AfterIntent：数据尚未落盘，恢复的是意图里携带的新值
				// （整体重放使“已形成意图的降级写”也达到一致终态）。
				for k := 0; k < 2; k++ {
					if !bytesEqualBlocks(dst[k], newVals[k]) {
						t.Fatalf("slot %d replay mismatch at %s", k, crashName(point))
					}
				}
				if msg, ok := verifyStripeParity(t, disks, n, s); !ok {
					t.Fatalf("降级重放后校验不一致: %s", msg)
				}
				msg, _ := verifyStripeParity(t, disks, n, s)
				t.Logf("判定: %s 后整体重放，新数据保留且 parity==XOR(data): %s",
					crashName(point), msg)
			})
		}
	}
}

// TestRebuildConcurrentWrites 换盘重建期间持续并发读写不中断：
// 重建完成后逐带校验一致，且重建中已重建块立即参与读取。
func TestRebuildConcurrentWrites(t *testing.T) {
	for _, n := range []int{3, 4, 5} {
		n := n
		t.Run("N"+itoa(n), func(t *testing.T) {
			stripes := 24
			disks := newMemDiskSet(n, stripes)
			v := reopen(t, disks, newMemJournalFile(), n, stripes)
			fillVolume(t, v, n, stripes)
			dead := n / 2
			if err := v.FailDisk(dead); err != nil {
				t.Fatal(err)
			}

			// 先验证降级读可用。
			probe := [][]byte{make([]byte, BlockSize)}
			if err := v.Read(0, probe); err != nil {
				t.Fatal(err)
			}

			fresh := newFreshMem(dead, stripes)
			if err := v.ReplaceDisk(dead, fresh); err != nil {
				t.Fatal(err)
			}

			stop := make(chan struct{})
			var wg sync.WaitGroup
			errs := make(chan error, 8)

			// 并发写者：不同 goroutine 写不同条带区间，另有一个写者
			// 持续覆盖随机条带（重建期间的写直接修复新盘对应槽）。
			for w := 0; w < 3; w++ {
				w := w
				wg.Add(1)
				go func() {
					defer wg.Done()
					for i := 0; ; i++ {
						select {
						case <-stop:
							return
						default:
						}
						s := (w*7 + i) % stripes
						blocks := make([][]byte, n-1)
						for k := range blocks {
							blocks[k] = mkBlock(byte(0x80+w), i*10+k)
						}
						if err := v.Write(s*(n-1), blocks); err != nil {
							errs <- err
							return
						}
					}
				}()
			}
			// 并发读者：重建期间读必须始终成功（已重建块直读，未重建 XOR）。
			wg.Add(1)
			go func() {
				defer wg.Done()
				dst := [][]byte{make([]byte, BlockSize)}
				for i := 0; ; i++ {
					select {
					case <-stop:
						return
					default:
					}
					if err := v.Read((i % (stripes * (n - 1))), dst); err != nil {
						errs <- err
						return
					}
				}
			}()

			v.RebuildDone()
			close(stop)
			wg.Wait()
			close(errs)
			for err := range errs {
				t.Fatalf("concurrent IO error during rebuild: %v", err)
			}

			// 再开一次卷：不应触发重建标记，且每带校验一致。
			if !verifyAllStripes(t, disks, n, stripes) {
				t.Fatal("post-rebuild parity mismatch")
			}
			if fresh.Rebuilding() {
				t.Fatal("rebuild marker not cleared")
			}
			// 重建后降级路径不再需要：直接读新盘与读卷一致。
			got := readAll(t, v, n, stripes)
			for s := 0; s < stripes; s++ {
				buf := make([]byte, BlockSize)
				if err := disks[dead].ReadBlock(s, buf); err != nil {
					t.Fatal(err)
				}
				_, data := stripeMap(n, s)
				for k, d := range data {
					if d == dead {
						if !bytesEqualBlocks(got[s*(n-1)+k], buf) {
							t.Fatalf("rebuilt disk content mismatch stripe %d", s)
						}
					}
				}
			}
			t.Logf("判定: N=%d 重建期间并发读写无错，重建后 %d 条带全部校验一致", n, stripes)
		})
	}
}

// TestRebuildResumeAfterCrash 重建过程中断电，重开后从未完成处续建，
// 最终逐带校验一致、内容与无中断重建相同。
func TestRebuildResumeAfterCrash(t *testing.T) {
	n, stripes := 5, 10
	run := func(crash bool) []Disk {
		disks := newMemDiskSet(n, stripes)
		jf := newMemJournalFile()
		v := reopen(t, disks, jf, n, stripes)
		fillVolume(t, v, n, stripes)
		dead := 2
		if err := v.FailDisk(dead); err != nil {
			t.Fatal(err)
		}
		fresh := newFreshMem(dead, stripes)
		if err := v.ReplaceDisk(dead, fresh); err != nil {
			t.Fatal(err)
		}
		if crash {
			// 模拟重建进行到一半“断电”：直接重新打开同一组盘与日志。
			reopen(t, disks, jf, n, stripes).RebuildDone()
		}
		v.RebuildDone()
		return disks
	}
	a := run(false)
	b := run(true)
	if !disksEqual(a, b) {
		t.Fatal("重建中断续建结果与连续重建不逐字节相同")
	}
	if !verifyAllStripes(t, b, n, stripes) {
		t.Fatal("resumed rebuild parity mismatch")
	}
	t.Log("判定: 重建中断后续建完成，各盘内容与连续重建逐字节相同，逐带校验一致")
}
