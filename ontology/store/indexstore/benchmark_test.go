package indexstore

import (
	"fmt"
	"testing"
)

// 追赶完成后的索引必须与从主表完整重建逐项相同：对每个可能的初始前缀
// 长度都显式做一次“追赶 vs 完整重建”双路对照。
func TestCatchUpEqualsFullRebuildForEveryPrefix(t *testing.T) {
	entries := scenarioEntries()
	for applied := 0; applied <= len(entries); applied++ {
		// 路径 A：从 (applied, 0) 追赶。
		dA := fabricatePrefixCrash(t, entries, applied, 0)
		sA, _ := Open(dA, Options{})
		catchUpFully(t, sA, 2)

		// 路径 B：同现场下完整重建。
		dB := fabricatePrefixCrash(t, entries, applied, 0)
		sB, _ := Open(dB, Options{})
		sB.index.rebuildFrom(sB.tbl.snapshot(), len(entries))

		if !mapsEqual(indexMap(sA), indexMap(sB)) {
			t.Fatalf("applied=%d catchup=%v rebuild=%v",
				applied, indexMap(sA), indexMap(sB))
		}
		// 重建结果必须与主表自检一致。
		if diffs := sB.index.compareTo(sB.tbl.snapshot()); len(diffs) != 0 {
			t.Fatalf("rebuild diffs at applied=%d: %v", applied, diffs)
		}
	}
}

// 唯一冲突判定开销不随索引落后条数增长：
// 写入判定只做主表 map 的 O(1) 查询。这里用基准测量并在文档中引用
// “每次判定固定次数的 map 访问”的结构化证明。
func BenchmarkConflictDecisionVsLag(b *testing.B) {
	for _, lag := range []int{0, 64, 256, 1024} {
		b.Run(fmt.Sprintf("lag=%d", lag), func(b *testing.B) {
			d := NewDisk()
			s, _ := Open(d, Options{})
			for i := 0; i < lag; i++ {
				pk := fmt.Sprintf("row%d", i)
				mustPut(s, pk, fmt.Sprintf("sec%d", i), true)
			}
			mustPut(s, "holder", "hot", true)
			// 不追赶：索引水位 0，落后 lag+1 条。
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := s.Put("intruder", "hot", true); err == nil {
					b.Fatal("expected conflict")
				}
			}
		})
	}
}

// 追赶单条开销不随主表行数/索引条目数增长：在已经吸收前 n-1 条、
// 落后 1 条的现场上重放最后一条，测量纯 applyOne+提交成本。
func BenchmarkReplayOneEntry(b *testing.B) {
	for _, n := range []int{64, 256, 1024} {
		b.Run(fmt.Sprintf("rows=%d", n), func(b *testing.B) {
			d := NewDisk()
			s, _ := Open(d, Options{})
			for i := 0; i < n; i++ {
				mustPut(s, fmt.Sprintf("p%d", i), fmt.Sprintf("k%d", i), true)
			}
			entries := append([]LogEntry(nil), s.entries...)
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				b.StopTimer()
				disk := fabricatePrefixCrash(b, entries, n-1, n-1)
				st, _ := Open(disk, Options{})
				b.StartTimer()
				if _, _, err := st.CatchUp(1); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkApplyOneIsolated 隔离测量单条日志的幂等归并：预载 n 个索引
// 条目后，反复对同一条“新主键的键改键”日志做内存+磁盘键级提交。
// 只触及固定 3~5 个文件键，不扫描任何集合。
func BenchmarkApplyOneIsolated(b *testing.B) {
	for _, n := range []int{64, 1024, 16384} {
		b.Run(fmt.Sprintf("indexsize=%d", n), func(b *testing.B) {
			d := NewDisk()
			ix := newIndex(d)
			entries := make([]LogEntry, n)
			for i := 0; i < n; i++ {
				e := LogEntry{LSN: i + 1, Op: LogUpsert,
					PK:     fmt.Sprintf("p%d", i),
					NewSec: fmt.Sprintf("k%d", i), HasNew: true}
				entries[i] = e
				ix.applyOne(e)
			}
			// 待重放：p0 把 k0 改为 knew。
			target := LogEntry{LSN: n + 1, Op: LogUpsert, PK: "p0",
				OldSec: "k0", HasOld: true,
				NewSec: "knew", HasNew: true}
			b.ResetTimer()
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				target.LSN = n + 1 + i
				ix.applied = n + i // 让每条都“新”，但键状态保持等价
				ix.applyOne(target)
				// 复位，避免键漂移：把 knew 改回 k0（下一轮又被重放）。
				back := LogEntry{LSN: n + 2 + i, Op: LogUpsert, PK: "p0",
					OldSec: "knew", HasOld: true,
					NewSec: "k0", HasNew: true}
				ix.applyOne(back)
			}
		})
	}
}
