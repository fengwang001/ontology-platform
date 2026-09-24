// Package api 是分层布隆去重的对外门面，仅依赖 dedup。
package api

import "ontology/dedup"

// 重新导出三类哨兵错误，供外部在不依赖 dedup 包时用 errors.Is 判定。
var (
	ErrInvalidM   = dedup.ErrInvalidM
	ErrInvalidCap = dedup.ErrInvalidCap
	ErrEmptyKey   = dedup.ErrEmptyKey
)

// Deduper 是对外去重器：方法签名严格按题面，错误判定经由哨兵错误。
type Deduper struct{ inner *dedup.Deduper }

// New 构造去重器；m/cap 非正时整体失败。
func New(m, cap int) (*Deduper, error) {
	d, err := dedup.New(m, cap)
	if err != nil {
		return nil, err
	}
	return &Deduper{inner: d}, nil
}

// Feed 逐键返回是否「新」；任一键为空串则整批拒绝、状态不变，返回 nil。
// 需取得具体哨兵错误时直接使用 dedup.Feed。
func (d *Deduper) Feed(keys []string) []bool {
	out, err := d.inner.Feed(keys)
	if err != nil {
		return nil
	}
	return out
}

// EstimateFP 返回当前对真新键的误判概率估计。
func (d *Deduper) EstimateFP() float64 { return d.inner.EstimateFP() }

// SelfCheck 用内置键序列核验四条不变量。
func (d *Deduper) SelfCheck() error { return d.inner.SelfCheck() }
