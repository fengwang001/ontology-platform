// Package keyset 实现基于邻键锁（next-key lock）的有序整数键集合。
package keyset

import (
	"fmt"
	"sort"
	"strings"
)

// ErrKind 标识操作被拒绝的原因类别。
type ErrKind int

const (
	// ErrTxNotFound 事务不存在。
	ErrTxNotFound ErrKind = iota
	// ErrTxCommitted 事务已提交。
	ErrTxCommitted
	// ErrTxAborted 事务已中止。
	ErrTxAborted
	// ErrInvalidMode 模式不是共享或排他。
	ErrInvalidMode
	// ErrRangeReversed 范围颠倒（lo > hi）。
	ErrRangeReversed
	// ErrRecordConflict 记录锁与他人冲突。
	ErrRecordConflict
	// ErrKeyExists 插入的键已存在。
	ErrKeyExists
	// ErrGapOccupied 插入的键落在他人持有的空隙锁内。
	ErrGapOccupied
)

// Error 是集合操作被拒绝时返回的结构化错误。
// 除 Kind 外，其余字段按 Kind 选择性填充。
type Error struct {
	Kind    ErrKind // 拒绝原因
	Tx      int64   // 相关事务
	Key     int64   // 相关键（冲突 / 已存在 / 间隙被占）
	Mode    Mode    // 非法模式（ErrInvalidMode）
	Lo, Hi  int64   // 颠倒的范围（ErrRangeReversed）
	Holders []int64 // 持锁事务（冲突 / 间隙被占），升序去重
}

// Error 返回人类可读的判定依据。
func (e *Error) Error() string {
	switch e.Kind {
	case ErrTxNotFound:
		return fmt.Sprintf("事务不存在: tx=%d", e.Tx)
	case ErrTxCommitted:
		return fmt.Sprintf("事务已提交: tx=%d", e.Tx)
	case ErrTxAborted:
		return fmt.Sprintf("事务已中止: tx=%d", e.Tx)
	case ErrInvalidMode:
		return fmt.Sprintf("非法模式: tx=%d mode=%d（仅支持共享/排他）", e.Tx, e.Mode)
	case ErrRangeReversed:
		return fmt.Sprintf("范围颠倒: tx=%d lo=%d > hi=%d", e.Tx, e.Lo, e.Hi)
	case ErrRecordConflict:
		return fmt.Sprintf("记录锁冲突: tx=%d key=%d 持锁事务=%s", e.Tx, e.Key, formatHolders(e.Holders))
	case ErrKeyExists:
		return fmt.Sprintf("键已存在: tx=%d key=%d", e.Tx, e.Key)
	case ErrGapOccupied:
		return fmt.Sprintf("间隙被占: tx=%d key=%d 持锁事务=%s", e.Tx, e.Key, formatHolders(e.Holders))
	default:
		return fmt.Sprintf("未知错误: kind=%d", e.Kind)
	}
}

func formatHolders(holders []int64) string {
	ss := make([]string, len(holders))
	for i, h := range holders {
		ss[i] = fmt.Sprintf("%d", h)
	}
	return "[" + strings.Join(ss, ",") + "]"
}

// sortHolders 升序排序并去重，保证结果确定。
func sortHolders(holders []int64) []int64 {
	sort.Slice(holders, func(i, j int) bool { return holders[i] < holders[j] })
	out := holders[:0]
	for i, h := range holders {
		if i == 0 || h != holders[i-1] {
			out = append(out, h)
		}
	}
	return out
}
