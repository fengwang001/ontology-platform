// Package txn 负责单批事务输出记录的校验与追加规则。
// 它不依赖本工程其他包，也不持有任何状态。
package txn

import "errors"

// 三类可判定、互不相同的哨兵错误。
var (
	ErrEmptyTxID    = errors.New("txn: txID must not be empty")
	ErrEmptyKey     = errors.New("txn: record key must not be empty")
	ErrDuplicateSeq = errors.New("txn: duplicate Seq within one batch")
)

// Rec 是上游投递的一条输出记录。
type Rec struct {
	Seq int
	Key string
	Val int64
}

// RecID 是幂等键 (txID, Seq)：Seq 只在同一 txID 内区分记录。
type RecID struct {
	TxID string
	Seq  int
}

// ID 构造幂等键。
func ID(txID string, seq int) RecID { return RecID{TxID: txID, Seq: seq} }

// Validate 对整批做校验：任一不合法即返回对应哨兵错误。
// 它是纯函数，调用方必须在修改任何状态之前调用它（失败不留痕）。
func Validate(txID string, recs []Rec) error {
	if txID == "" {
		return ErrEmptyTxID
	}
	seen := make(map[int]struct{}, len(recs))
	for _, r := range recs {
		if r.Key == "" {
			return ErrEmptyKey
		}
		if _, dup := seen[r.Seq]; dup {
			return ErrDuplicateSeq
		}
		seen[r.Seq] = struct{}{}
	}
	return nil
}

// Fresh 按追加规则过滤：仅返回 committed 判定为「尚未提交」的记录，保持原序。
// committed 封装幂等集合的成员判定；命中的记录幂等跳过（不论内容是否相同，
// 首次提交者胜出）。只能在 Validate 通过之后调用。
func Fresh(txID string, recs []Rec, committed func(RecID) bool) []Rec {
	out := make([]Rec, 0, len(recs))
	for _, r := range recs {
		if committed(ID(txID, r.Seq)) {
			continue
		}
		out = append(out, r)
	}
	return out
}
