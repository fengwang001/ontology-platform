package clearance

import (
	"bytes"

	"ontology/grant"
)

// 判定结果。
const (
	Allow     = "allow"
	Exclusive = "exclusive"
	None      = "none"
)

// 可播判定错误（errors.Is 可区分）。
var (
	ErrInvalidArgument = grant.ErrInvalidArgument
	ErrUnknownNode     = grant.ErrUnknownNode
	ErrNotALeaf        = grant.ErrNotALeaf
)

// Result 是一次 CanPlay 的结果。
type Result struct {
	Kind  string
	Grant *grant.Grant
}

// Judge 在给定登记处上执行只读可播判定。
type Judge struct{ r *grant.Registry }

// New 创建判定器。
func New(r *grant.Registry) *Judge { return &Judge{r: r} }

// CanPlay 判定 licensee 在时刻 t 能否播放 title 在 leaf 的内容。
func (j *Judge) CanPlay(title, licensee, leaf []byte, t int64) (Result, error) {
	if len(title) == 0 || len(licensee) == 0 || len(leaf) == 0 ||
		t < grant.MinTime || t > grant.MaxTime {
		return Result{}, grant.ErrInvalidArgument
	}
	active := j.r.ActiveAt(title, leaf, t)
	if active == nil {
		// ActiveAt 对未知叶同样返回 nil；区分“未知/非叶”需查询树，
		// 该职责由调用方通过 territory 校验，这里仅区分空授权与错误：
		// 通过 grant.Registry 的只读判定接口暴露校验。
		if err := j.r.CheckLeaf(leaf); err != nil {
			return Result{}, err
		}
		return Result{Kind: None}, nil
	}

	var blocking *grant.Grant
	for _, g := range active {
		if bytes.Equal(g.Licensee, licensee) {
			return Result{Kind: Allow, Grant: g}, nil
		}
		if g.Exclusive && (blocking == nil || bytes.Compare(g.ID, blocking.ID) < 0) {
			blocking = g
		}
	}
	if blocking != nil {
		return Result{Kind: Exclusive, Grant: blocking}, nil
	}
	return Result{Kind: None}, nil
}

// Holders 按 id 字节序列出此刻覆盖该叶的全部生效授权。
func (j *Judge) Holders(title, leaf []byte, t int64) ([]*grant.Grant, error) {
	if len(title) == 0 || len(leaf) == 0 || t < grant.MinTime || t > grant.MaxTime {
		return nil, grant.ErrInvalidArgument
	}
	if err := j.r.CheckLeaf(leaf); err != nil {
		return nil, err
	}
	return j.r.ActiveAt(title, leaf, t), nil
}
