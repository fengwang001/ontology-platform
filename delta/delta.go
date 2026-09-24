// Package delta 计算单侧快照相对祖先的变更集（新增/删除/字段级修改）。
package delta

import (
	"errors"
	"fmt"

	"ontology/doc"
)

// ErrContradictory 表示同一变更集中同一键既被删除又被修改/新增。
var ErrContradictory = errors.New("delta: 自相矛盾的变更集")

// FieldChange 记录单字段的旧值与新值；OK 标记存在性以区分删除。
type FieldChange struct {
	Old, New     doc.Value
	OldOK, NewOK bool
}

// Delta 是一侧相对祖先的变更集。
type Delta struct {
	Added   map[string]doc.Record
	Deleted map[string]bool
	Changed map[string]map[string]FieldChange
}

// New 返回空变更集。
func New() *Delta {
	return &Delta{
		Added:   map[string]doc.Record{},
		Deleted: map[string]bool{},
		Changed: map[string]map[string]FieldChange{},
	}
}

// Compute 由祖先快照与单侧快照推导变更集，一遍扫描键并集。
func Compute(anc, side doc.Set) *Delta {
	d := New()
	seen := map[string]bool{}
	for k, ar := range anc {
		seen[k] = true
		sr, ok := side[k]
		if !ok {
			d.Deleted[k] = true
			continue
		}
		if fc := diffRecord(ar, sr); len(fc) > 0 {
			d.Changed[k] = fc
		}
	}
	for k, sr := range side {
		if !seen[k] {
			d.Added[k] = sr
		}
	}
	return d
}

func diffRecord(a, s doc.Record) map[string]FieldChange {
	var out map[string]FieldChange
	for f, av := range a {
		sv, ok := s[f]
		if !ok {
			out = put(out, f, FieldChange{Old: av, OldOK: true})
		} else if sv != av {
			out = put(out, f, FieldChange{Old: av, OldOK: true, New: sv, NewOK: true})
		}
	}
	for f, sv := range s {
		if _, ok := a[f]; !ok {
			out = put(out, f, FieldChange{New: sv, NewOK: true})
		}
	}
	return out
}

func put(m map[string]FieldChange, f string, fc FieldChange) map[string]FieldChange {
	if m == nil {
		m = map[string]FieldChange{}
	}
	m[f] = fc
	return m
}

// Validate 检查变更集自相矛盾：同一键不得既删除又新增/修改。
// 返回的错误可用 errors.Is(err, ErrContradictory) 判定并指出该键。
func (d *Delta) Validate() error {
	for k := range d.Deleted {
		if _, ok := d.Added[k]; ok {
			return fmt.Errorf("键 %q 既删除又新增: %w", k, ErrContradictory)
		}
		if _, ok := d.Changed[k]; ok {
			return fmt.Errorf("键 %q 既删除又修改: %w", k, ErrContradictory)
		}
	}
	for k := range d.Added {
		if _, ok := d.Changed[k]; ok {
			return fmt.Errorf("键 %q 既新增又修改: %w", k, ErrContradictory)
		}
	}
	return nil
}
