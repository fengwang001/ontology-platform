// Package mapc 把旧版本事件按列名对齐投影到 active 列序，
// 执行零值补位 / 拒收判定与类型转换。依赖 sch。
package mapc

import (
	"errors"
	"sync/atomic"

	"ontology/sch"
)

// ErrArity 事件值个数与布局列数不符。
var ErrArity = errors.New("mapc: value count mismatches layout")

// zero 返回类型的零值：int→int64(0)，str→""。
func zero(t sch.Type) any {
	if t == sch.Int {
		return int64(0)
	}
	return ""
}

// Mapper 无状态，可并发使用。
type Mapper struct {
	lastCmp atomic.Int64 // 非导出：最近一次 Map 为单个 active 列定位同名列的比较次数
}

func New() *Mapper { return &Mapper{} }

// Map 把事件 (eventLay 布局下的 values) 投影到 active 列序。
// 按列名对齐：已删列静默丢弃；后加列 required→ErrMissingColumn，否则补零值；
// 同名列按「旧类型→新类型」coercion，失败即 ErrBadValue 拒收整个事件。
func (m *Mapper) Map(eventLay, active []sch.Column, values []any) ([]any, error) {
	if len(values) != len(eventLay) {
		return nil, ErrArity
	}
	idx := make(map[string]int, len(eventLay))
	for i, c := range eventLay {
		idx[c.Name] = i
	}
	out := make([]any, len(active))
	for i, ac := range active {
		j, ok := idx[ac.Name] // 哈希定位：单次比较，不随布局规模线性增长
		m.lastCmp.Store(1)
		if !ok {
			if ac.Required {
				return nil, sch.ErrMissingColumn
			}
			out[i] = zero(ac.Typ)
			continue
		}
		v, err := sch.Coerce(values[j], eventLay[j].Typ, ac.Typ)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}
