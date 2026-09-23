// Package hashagg 实现带溢出的哈希聚合主流程：
// 建表 → 超预算时选最大分区溢出部分聚合状态 → 并发回读逐分区合并 → 确定性输出。
package hashagg

import (
	"errors"
	"math"
	"sort"
	"sync"

	"ontology/acc"
	"ontology/hashpart"
	"ontology/row"
)

// Stats 是运行计数器快照（内部计数器非导出，经此结构读出）。
type Stats struct {
	PeakResident int   // 历史最大驻留分组数，不得超过预算
	Spills       int   // 溢出总次数
	SpillsByPart []int // 各分区溢出次数
	RowsWritten  int64 // 分区写出行数
	RowsRead     int64 // 分区读回行数
	RowOps       int64 // 总行处理次数（入表 + 回读合并）
	Skipped      int64 // 被拒绝的 NaN 行数
}

// Agg 是带溢出的哈希聚合器。分区数构造时固定，与数据规模无关。
type Agg struct {
	numParts  int
	budget    int
	table     []map[string]*acc.State
	spiller   *hashpart.Spiller
	readDelay func(part int) // 测试钩子：打乱回读完成顺序

	resident    int
	peak        int
	spills      []int
	rowsWritten int64
	rowsRead    int64
	rowOps      int64
	skipped     int64
	failed      error
}

// New 创建聚合器：numParts 个分区、内存预算 budget 个驻留分组、溢出目录 dir。
func New(numParts, budget int, dir string) (*Agg, error) {
	if numParts < 1 || budget < 1 {
		return nil, errors.New("hashagg: 分区数与预算必须为正")
	}
	sp, err := hashpart.NewSpiller(dir, numParts)
	if err != nil {
		return nil, err
	}
	a := &Agg{numParts: numParts, budget: budget, spiller: sp, spills: make([]int, numParts)}
	a.table = make([]map[string]*acc.State, numParts)
	for p := range a.table {
		a.table[p] = make(map[string]*acc.State)
	}
	return a, nil
}

// SetFailOnWrite 注入故障：第 k 次（1 起）写分区失败。供演示与测试。
func (a *Agg) SetFailOnWrite(k int) { a.spiller.SetFailOnWrite(k) }

// SetReadDelay 设置回读钩子（每分区协程读段前调用），用于打乱完成顺序验证确定性。
func (a *Agg) SetReadDelay(fn func(part int)) { a.readDelay = fn }

// Add 处理一行：NaN 拒绝并计入跳过数；-0.0 归一化为 +0.0。
func (a *Agg) Add(r row.Row) error {
	if a.failed != nil {
		return a.failed
	}
	if math.IsNaN(r.Val) {
		a.skipped++
		return nil
	}
	v := r.Val
	if v == 0 {
		v = 0 // ±0.0 视为相等：归一化为 +0.0
	}
	p := hashpart.Partition(r.Key, a.numParts)
	st, ok := a.table[p][r.Key]
	if !ok {
		if a.resident >= a.budget {
			if err := a.spill(); err != nil {
				a.failed = err
				a.spiller.Cleanup()
				return err
			}
		}
		st = &acc.State{}
		a.table[p][r.Key] = st
		a.resident++
		if a.resident > a.peak {
			a.peak = a.resident
		}
	}
	st.Add(v)
	a.rowOps++
	return nil
}

// spill 把当前最大分区的部分聚合状态落盘并从内存清除。
func (a *Agg) spill() error {
	victim := 0
	for p := range a.table {
		if len(a.table[p]) > len(a.table[victim]) {
			victim = p
		}
	}
	m := a.table[victim]
	if len(m) == 0 {
		return errors.New("hashagg: 无可溢出分区")
	}
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	payloads := make([][]byte, 0, len(keys))
	for _, k := range keys {
		payloads = append(payloads, acc.Encode(k, *m[k]))
	}
	if err := a.spiller.WriteSegment(victim, payloads); err != nil {
		return err
	}
	a.rowsWritten += int64(len(payloads))
	a.spills[victim]++
	a.table[victim] = make(map[string]*acc.State)
	a.resident -= len(m)
	return nil
}

// Stats 返回计数器快照。
func (a *Agg) Stats() Stats {
	total := 0
	for _, n := range a.spills {
		total += n
	}
	return Stats{
		PeakResident: a.peak,
		Spills:       total,
		SpillsByPart: append([]int(nil), a.spills...),
		RowsWritten:  a.rowsWritten,
		RowsRead:     a.rowsRead,
		RowOps:       a.rowOps,
		Skipped:      a.skipped,
	}
}
