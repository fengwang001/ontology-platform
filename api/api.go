// Package api 是计数布隆过滤器的对外入口：包装参数校验（hash）与过滤器
// 实现（cbf）。依赖方向单向：api → cbf → hash，不存在反向依赖。
package api

import (
	"ontology/cbf"
	"ontology/hash"
)

// Filter 对外暴露计数布隆过滤器；内部状态全部留在 cbf，不可从外部直接触及。
type Filter struct {
	inner *cbf.Filter
}

// New 构造过滤器：m 必须为素数且 m > k，k >= 1，maxCount 在 1..255。
// 任一参数非法都返回可判定的 hash.ErrInvalidParameters，且无对象产生。
func New(m, k int, maxCount uint8) (*Filter, error) {
	p, err := hash.New(m, k)
	if err != nil {
		return nil, err
	}
	f, err := cbf.New(p, maxCount)
	if err != nil {
		return nil, err
	}
	return &Filter{inner: f}, nil
}

// Insert 插入键；计数器溢出时返回 cbf.ErrOverflow 且整体不生效。
func (f *Filter) Insert(x int64) error { return f.inner.Insert(x) }

// Delete 删除确已插入的键；删除未插入键返回 cbf.ErrDeleteUninserted。
func (f *Filter) Delete(x int64) error { return f.inner.Delete(x) }

// Contains 允许假阳性、绝不假阴性；可被多 goroutine 并发调用。
func (f *Filter) Contains(x int64) bool { return f.inner.Contains(x) }

// SelfCheck 对内置操作序列核验四条不变量，通过返回 nil。
func (f *Filter) SelfCheck() error { return cbf.SelfCheck() }

// CheckAccessComplexity 只给出通过与否：多档 m 下一次操作访问的计数器个数
// 恒等于 k。不泄露非导出计数器的数值。
func (f *Filter) CheckAccessComplexity() error { return cbf.CheckAccessComplexity() }
