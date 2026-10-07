package incremental

import (
	"fmt"
	"sort"
	"sync"
)

// IncrementRecord 是一条已确认的增量记录：某个周期从 Start 到 End
// 输出的全部写入（按首次被接受的顺序）。记录被追加到账台即视为确认，
// 是“已确认结束位点”的事实来源。
type IncrementRecord struct {
	Chain    string
	Seq      uint64 // 链路内记录序号，从 1 开始连续递增
	Start    Cursor
	End      Cursor
	WriteIDs []string
}

// Ledger 保存各链路已确认的增量记录，追加是原子的：
// 一条记录要么完整可见，要么完全不存在。
type Ledger interface {
	// Append 追加一条已确认记录，Seq 由账台分配。
	Append(rec IncrementRecord) (IncrementRecord, error)
	// Records 返回链路全部已确认记录，按 Seq 升序。
	Records(chain string) ([]IncrementRecord, error)
}

// MemLedger 是 Ledger 的内存实现，支持故障注入以模拟历史记录缺口。
type MemLedger struct {
	mu      sync.RWMutex
	records map[string][]IncrementRecord
	dropped map[string]map[uint64]bool
}

// NewMemLedger 创建空的内存账台。
func NewMemLedger() *MemLedger {
	return &MemLedger{
		records: make(map[string][]IncrementRecord),
		dropped: make(map[string]map[uint64]bool),
	}
}

// Append 实现 Ledger。
func (l *MemLedger) Append(rec IncrementRecord) (IncrementRecord, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	recs := l.records[rec.Chain]
	rec.Seq = uint64(len(recs)) + 1
	l.records[rec.Chain] = append(recs, rec)
	return rec, nil
}

// Records 实现 Ledger。被 Drop 注入丢弃的记录对读者不可见。
func (l *MemLedger) Records(chain string) ([]IncrementRecord, error) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	var out []IncrementRecord
	for _, r := range l.records[chain] {
		if l.dropped[chain][r.Seq] {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// Drop 注入故障：丢弃链路上序号为 seq 的历史记录（模拟历史缺口）。
func (l *MemLedger) Drop(chain string, seq uint64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.dropped[chain] == nil {
		l.dropped[chain] = make(map[uint64]bool)
	}
	l.dropped[chain][seq] = true
}

// DeriveSafeCursor 基于已成功输出的历史增量记录推导安全起始位点。
//
// 从 GenesisCursor 出发沿记录链校验连续性：首条记录必须从
// GenesisCursor 开始，其后每条记录的 Start 必须等于前一条的 End。
// 返回连续前缀的末端位点——它一定不晚于真正已确认过的位点
// （宁可重复检查也不得遗漏）。若记录链存在缺口（含首条缺失、
// 序号不连续、位点不衔接），返回连续前缀末端位点与
// ErrKindHistoryGap 错误，错误中携带该安全位点。
func DeriveSafeCursor(chain string, records []IncrementRecord) (Cursor, *Error) {
	sorted := make([]IncrementRecord, len(records))
	copy(sorted, records)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Seq < sorted[j].Seq })

	cursor := GenesisCursor
	var expectSeq uint64 = 1
	for _, r := range sorted {
		if r.Seq != expectSeq {
			return cursor, gapError(chain, cursor,
				fmt.Sprintf("record seq %d missing before seq %d", expectSeq, r.Seq))
		}
		if r.Start != cursor {
			return cursor, gapError(chain, cursor,
				fmt.Sprintf("record seq %d starts at %d, expected %d", r.Seq, r.Start, cursor))
		}
		if r.End < r.Start {
			return cursor, gapError(chain, cursor,
				fmt.Sprintf("record seq %d has end %d before start %d", r.Seq, r.End, r.Start))
		}
		cursor = r.End
		expectSeq++
	}
	return cursor, nil
}

func gapError(chain string, safe Cursor, detail string) *Error {
	return &Error{
		Kind:          ErrKindHistoryGap,
		Chain:         chain,
		Detail:        detail,
		SafeCursor:    safe,
		HasSafeCursor: true,
	}
}
