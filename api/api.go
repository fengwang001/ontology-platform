// Package api 是滚动哈希子串查询器的对外入口：进程内存态、仅标准库。
// New 成功前调用查询返回 ErrNotBuilt；New 对空串失败且不改变既有状态。
package api

import (
	"errors"
	"sync/atomic"

	"ontology/query"
	"ontology/rhash"
)

// 三类可判定且互不相同的哨兵错误。
var (
	// ErrEmptyInput：New 收到长度为 0 的输入。
	ErrEmptyInput = errors.New("api: 输入为空串")
	// ErrNotBuilt：New 成功之前调用了 Equal/LCP/SelfCheck。
	ErrNotBuilt = errors.New("api: 查询器尚未构建")
	// ErrOutOfRange：端点越出 [0,n] 或 l>r。
	ErrOutOfRange = errors.New("api: 区间或下标越界")
)

// current 原子发布当前查询器；发布后只读，故并发查询无需加锁。
var current atomic.Pointer[query.Query]

// New 用 s 构建查询器。空串立即失败且不触碰既有状态（失败不留痕）。
func New(s []byte) error {
	if len(s) == 0 {
		return ErrEmptyInput
	}
	q := query.New(rhash.New(s))
	if err := q.SelfCheck(); err != nil { // 发布前自检，不过则不发布
		return err
	}
	current.Store(q)
	return nil
}

func built() (*query.Query, error) {
	q := current.Load()
	if q == nil {
		return nil, ErrNotBuilt
	}
	return q, nil
}

// mapErr 把下层哨兵统一翻译为 api 层哨兵，保证调用方可用 errors.Is 判定。
func mapErr(err error) error {
	switch {
	case errors.Is(err, query.ErrNotBuilt):
		return ErrNotBuilt
	case errors.Is(err, query.ErrOutOfRange):
		return ErrOutOfRange
	default:
		return err
	}
}

// Equal 报告两个子串是否相等；语义等于 bytes.Equal(s[l1:r1], s[l2:r2])。
func Equal(l1, r1, l2, r2 int) (bool, error) {
	q, err := built()
	if err != nil {
		return false, err
	}
	eq, err := q.Equal(l1, r1, l2, r2)
	return eq, mapErr(err)
}

// LCP 返回后缀 s[i:] 与 s[j:] 的最长公共前缀长度。
func LCP(i, j int) (int, error) {
	q, err := built()
	if err != nil {
		return 0, err
	}
	k, err := q.LCP(i, j)
	return k, mapErr(err)
}

// SelfCheck 对内置区间与后缀对核验四条不变量，通过返回 nil。
func SelfCheck() error {
	q, err := built()
	if err != nil {
		return err
	}
	return mapErr(q.SelfCheck())
}
