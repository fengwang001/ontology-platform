// Package txn 负责单批事务记录的校验与幂等追加规则。
// 它不依赖其他包：校验是纯函数，幂等判定通过调用方提供的谓词完成。
package txn

import "errors"

// Rec 是上游投递的一条输出记录。
type Rec struct {
	Seq int
	Key string
	Val int64
}

// 三类可判定的哨兵错误，互不相同。
var (
	ErrEmptyTxID = errors.New("txn: txID must not be empty")
	ErrEmptyKey  = errors.New("txn: record key must not be empty")
	ErrDupSeq    = errors.New("txn: duplicate Seq within one batch")
)

// Committed 判定 (txID, seq) 是否已提交，由下层存储提供。
type Committed func(txID string, seq int) bool

// Validate 整批校验：txID 非空、每条 Key 非空、批内 Seq 唯一。
// 任一不合法即返回对应哨兵错误；纯函数，不触碰任何状态。
func Validate(txID string, recs []Rec) error {
	if txID == "" {
		return ErrEmptyTxID
	}
	seen := make(map[int]struct{}, len(recs))
	for _, r := range recs {
		if r.Key == "" {
			return ErrEmptyKey
		}
		if _, ok := seen[r.Seq]; ok {
			return ErrDupSeq
		}
		seen[r.Seq] = struct{}{}
	}
	return nil
}

// Plan 在已提交谓词上按序判定，返回本次真正需要追加的记录：
// 已提交的 (txID, Seq) 幂等跳过（首次提交者胜出，不论内容），其余保序返回。
// 调用方须先通过 Validate。
func Plan(txID string, recs []Rec, isCommitted Committed) []Rec {
	fresh := make([]Rec, 0, len(recs))
	for _, r := range recs {
		if isCommitted(txID, r.Seq) {
			continue
		}
		fresh = append(fresh, r)
	}
	return fresh
}
