// Package wm 负责单批校验与按高水位判定重复/生效，不依赖其他包。
package wm

import "errors"

// Rec 是上游至少一次投递来的一条记录。
type Rec struct {
	Partition int
	Offset    int64
	Key       string
	Val       int64
}

// 哨兵错误：批被整体拒绝时返回其一，调用方可用 errors.Is 判定类别。
var (
	// ErrIllegalRec：分区号为负、位点为负或 Key 为空。
	ErrIllegalRec = errors.New("wm: illegal record: negative partition/offset or empty key")
	// ErrOutOfOrder：同批内同一分区位点未严格递增（含相等）。
	ErrOutOfOrder = errors.New("wm: offsets for a partition must be strictly increasing within a batch")
)

// Validate 校验整批：先查全部记录合法性，再查同分区严格递增；
// 即使乱序出现在非法记录之前，也只报记录非法（固定优先级）。
func Validate(batch []Rec) error {
	for i := range batch {
		r := &batch[i]
		if r.Partition < 0 || r.Offset < 0 || r.Key == "" {
			return ErrIllegalRec
		}
	}
	last := make(map[int]int64)
	for i := range batch {
		r := &batch[i]
		if prev, ok := last[r.Partition]; ok && r.Offset <= prev {
			return ErrOutOfOrder
		}
		last[r.Partition] = r.Offset
	}
	return nil
}

// Dup 按水位判定：Offset <= W 即重复（含 Offset == W 的边界重投）。
func Dup(off, w int64) bool { return off <= w }

// Apply 给出一条记录在水位 w 下的判定与推进后的新水位。
// 重复：不生效，水位不变；生效：水位推进到该记录位点。
func Apply(w int64, r Rec) (dup bool, next int64) {
	if Dup(r.Offset, w) {
		return true, w
	}
	return false, r.Offset
}
