// Package run 负责外部排序的 run 生成：按内存阈值 M 顺序切块，
// 凑满 M 条就在块内升序排序后溢写为一个 run，最后一块可少于 M 条。
// 本包无状态、不依赖其他包。
package run

import (
	"errors"
	"sort"
)

// Key 是元组的连接键。合法 Key 必须 ≥ 0，负数为非法输入。
type Key int

// 哨兵错误：可判定、可比较。
var (
	// ErrBadThreshold：内存阈值 M < 1。
	ErrBadThreshold = errors.New("run: memory threshold M must be >= 1")
	// ErrNegativeKey：输入中出现负数键。
	ErrNegativeKey = errors.New("run: negative key")
)

// Run 是一次溢写产出的有序块：Keys 严格非降序排列。
type Run struct {
	Keys []Key
}

// MakeRuns 按 M 分块生成全部 run。
// 不变量：除最后一个 run 外每个恰好 M 条；最后一个 ≤ M 条（输入非空时 ≥1 条）；
// 每个 run 内部升序。M < 1 或出现负数键时整体失败，不产出任何 run。
func MakeRuns(keys []Key, m int) ([]Run, error) {
	if m < 1 {
		return nil, ErrBadThreshold
	}
	// 先全量校验：任一条非法则整批不生效（本包无状态，直接拒绝即可）。
	for _, k := range keys {
		if k < 0 {
			return nil, ErrNegativeKey
		}
	}
	runs := make([]Run, 0, (len(keys)+m-1)/m)
	for start := 0; start < len(keys); start += m {
		end := start + m
		if end > len(keys) {
			end = len(keys)
		}
		chunk := make([]Key, end-start)
		copy(chunk, keys[start:end])
		sort.Slice(chunk, func(i, j int) bool { return chunk[i] < chunk[j] })
		runs = append(runs, Run{Keys: chunk})
	}
	return runs, nil
}
