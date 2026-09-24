// Package delta 描述单侧相对祖先的变更集，并检测自相矛盾的输入。
package delta

import (
	"errors"
	"fmt"
	"sort"

	"ontology/doc"
)

// ErrContradictory 表示同一键在同一侧既标删除又标修改。
var ErrContradictory = errors.New("delta: contradictory change")

// Change 是单侧对某个键的变更。
type Change struct {
	Deleted bool            // 整记录删除
	Added   bool            // 整记录新增（祖先中不存在）
	Set     map[string]any  // 字段被设置/修改为新值
	Unset   map[string]bool // 字段被移除
}

// Delta 是单侧相对祖先的全部变更：键 -> 变更。
type Delta map[string]Change

// Compute 由祖先与某侧快照计算变更集。
func Compute(ancestor, side doc.Set) Delta {
	d := make(Delta)
	for _, k := range doc.Keys(ancestor) {
		arec := ancestor[k]
		srec, ok := side[k]
		if !ok {
			d[k] = Change{Deleted: true}
			continue
		}
		c := Change{Set: map[string]any{}, Unset: map[string]bool{}}
		for _, f := range doc.FieldKeys(arec) {
			av := arec[f]
			sv, has := srec[f]
			if !has {
				c.Unset[f] = true
			} else if !doc.ValueEqual(av, sv, true, true) {
				c.Set[f] = sv
			}
		}
		for _, f := range doc.FieldKeys(srec) {
			if _, has := arec[f]; !has {
				c.Set[f] = srec[f]
			}
		}
		if len(c.Set) > 0 || len(c.Unset) > 0 {
			d[k] = c
		}
	}
	for _, k := range doc.Keys(side) {
		if _, ok := ancestor[k]; ok {
			continue
		}
		c := Change{Added: true, Set: map[string]any{}, Unset: map[string]bool{}}
		for _, f := range doc.FieldKeys(side[k]) {
			c.Set[f] = side[k][f]
		}
		d[k] = c
	}
	return d
}

// Validate 校验变更集自洽：同一键不得既删除又修改/新增。
// 返回的错误可用 errors.Is(err, ErrContradictory) 判定，并含该键名。
func Validate(d Delta) error {
	keys := make([]string, 0, len(d))
	for k := range d {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		c := d[k]
		if c.Deleted && (c.Added || len(c.Set) > 0 || len(c.Unset) > 0) {
			return fmt.Errorf("%w: key %q marked deleted and modified", ErrContradictory, k)
		}
	}
	return nil
}
