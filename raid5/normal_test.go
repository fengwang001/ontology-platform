package raid5

import (
	"errors"
	"testing"
)

// TestWriteReadBasic 正常态：整带写 + 部分写 + 跨带写读回。
func TestWriteReadBasic(t *testing.T) {
	for _, n := range []int{3, 4, 5} {
		n := n
		t.Run("N"+itoa(n), func(t *testing.T) {
			stripes := 4
			disks := newMemDiskSet(n, stripes)
			jf := newMemJournalFile()
			v := reopen(t, disks, jf, n, stripes)

			// 整带写条带 0
			full := make([][]byte, n-1)
			for k := range full {
				full[k] = mkBlock(0xA0, k)
			}
			if err := v.Write(0, full); err != nil {
				t.Fatal(err)
			}
			// 部分写条带 1 的第 0 块（RMW，其余保持零）
			if err := v.Write((n - 1), [][]byte{mkBlock(0xB1, 7)}); err != nil {
				t.Fatal(err)
			}
			// 跨带写：从条带 1 最后一个数据槽起写 3 块，覆盖条带 1/2。
			start := (n - 1) + (n - 2)
			cross := [][]byte{mkBlock(0xC2, 1), mkBlock(0xC2, 2), mkBlock(0xC2, 3)}
			t.Logf("输入: 跨带写 start=%d 块数=%d", start, len(cross))
			if err := v.Write(start, cross); err != nil {
				t.Fatal(err)
			}

			got := readAll(t, v, n, stripes)
			if !bytesEqualBlocks(got[0], full[0]) {
				t.Fatal("stripe0 data0 mismatch")
			}
			if !bytesEqualBlocks(got[n-1], mkBlock(0xB1, 7)) {
				t.Fatal("stripe1 first block mismatch")
			}
			for i, b := range cross {
				if !bytesEqualBlocks(got[start+i], b) {
					t.Fatalf("cross write block %d mismatch", i)
				}
			}
			if !verifyAllStripes(t, disks, n, stripes) {
				t.Fatal("parity invariant broken")
			}
			t.Log("判定: 全部读回一致且每条带 parity==XOR(data)")
		})
	}
}

// TestPartialWriteCrashEachStep 对 N=3,4,5：部分条带写入，在流水线每个
// 断电点崩溃并重开，验证恢复后条带校验一致且数据符合恢复规则
// （数据已落盘则保留新数据，未完成则校验按数据重算，绝不写洞）。
func TestPartialWriteCrashEachStep(t *testing.T) {
	for _, n := range []int{3, 4, 5} {
		n := n
		for _, point := range []CrashPoint{CrashAfterIntent, CrashAfterData, CrashAfterParity, CrashAfterCommit} {
			point := point
			t.Run("N"+itoa(n)+"_"+crashName(point), func(t *testing.T) {
				stripes := 3
				disks := newMemDiskSet(n, stripes)
				jf := newMemJournalFile()
				v := reopen(t, disks, jf, n, stripes)

				// 先建立背景数据：整带写所有条带。
				for s := 0; s < stripes; s++ {
					blocks := make([][]byte, n-1)
					for k := range blocks {
						blocks[k] = mkBlock(byte(10+s), k)
					}
					if err := v.Write(s*(n-1), blocks); err != nil {
						t.Fatal(err)
					}
				}

				// 目标：部分写条带 1 的第一个数据块。
				target := (n - 1)
				newB := mkBlock(0xEE, 1)
				v.SetCrashHook(&CrashHook{Point: point, Stripe: 1})
				err := v.Write(target, [][]byte{newB})
				t.Logf("输入: 部分写 stripe=1 slot=0 断电点=%s 输出: err=%v",
					crashName(point), err)
				if !errors.Is(err, errCrashed) {
					t.Fatalf("expected errCrashed, got %v", err)
				}

				// 断电重开：应重放未完成意图（按数据重算校验）。
				v2 := reopen(t, disks, jf, n, stripes)
				if msg, ok := verifyStripeParity(t, disks, n, 1); !ok {
					t.Fatalf("恢复后校验不一致: %s", msg)
				}
				dst := [][]byte{make([]byte, BlockSize)}
				if err := v2.Read(target, dst); err != nil {
					t.Fatal(err)
				}
				dataNew := bytesEqualBlocks(dst[0], newB)
				t.Logf("判定: 断电点=%s 下，数据块为新值=%v（AfterData 之后应为 true）",
					crashName(point), dataNew)
				switch point {
				case CrashAfterIntent:
					// 数据未写，仍是旧数据，校验已按旧数据重算（无写洞）。
					if dataNew {
						t.Fatal("AfterIntent: data must remain old")
					}
				default:
					if !dataNew {
						t.Fatal("data block should be the new value after data flush")
					}
				}
				// 所有其它条带不受影响。
				if !verifyAllStripes(t, disks, n, stripes) {
					t.Fatal("other stripes corrupted by recovery")
				}
			})
		}
	}
}

// TestRepeatedPartialUpdatesRMW 连续多次部分写同一槽，每次后校验必须成立。
func TestRepeatedPartialUpdatesRMW(t *testing.T) {
	n, stripes := 4, 2
	disks := newMemDiskSet(n, stripes)
	jf := newMemJournalFile()
	v := reopen(t, disks, jf, n, stripes)
	for round := 0; round < 6; round++ {
		b := mkBlock(byte(round), round*3)
		if err := v.Write(2, [][]byte{b}); err != nil { // 条带0 槽2
			t.Fatal(err)
		}
		dst := [][]byte{make([]byte, BlockSize)}
		if err := v.Read(2, dst); err != nil {
			t.Fatal(err)
		}
		if !bytesEqualBlocks(dst[0], b) {
			t.Fatalf("round %d read mismatch", round)
		}
		if msg, ok := verifyStripeParity(t, disks, n, 0); !ok {
			t.Fatalf("round %d: %s", round, msg)
		}
		t.Logf("判定: RMW 第 %d 轮后读回与校验均一致", round)
	}
}

func bytesEqualBlocks(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func crashName(p CrashPoint) string {
	switch p {
	case CrashAfterIntent:
		return "AfterIntent"
	case CrashAfterData:
		return "AfterData"
	case CrashAfterParity:
		return "AfterParity"
	case CrashAfterCommit:
		return "AfterCommit"
	}
	return "None"
}
