// Package verify 提供分区文件损坏检测与结果核对（与全内存聚合比对）。
package verify

import (
	"fmt"
	"math"

	"ontology/acc"
	"ontology/hashpart"
	"ontology/row"
)

// InMemory 全内存参考聚合，与 hashagg 相同的边界语义：跳过 NaN、-0.0 归一化为 +0.0。
func InMemory(rows []row.Row) map[string]acc.State {
	m := make(map[string]acc.State)
	for _, r := range rows {
		if math.IsNaN(r.Val) {
			continue
		}
		v := r.Val
		if v == 0 {
			v = 0
		}
		st := m[r.Key]
		st.Add(v)
		m[r.Key] = st
	}
	return m
}

// CheckFile 检测分区文件是否损坏；损坏时返回分类错误（*hashpart.FileError）。
func CheckFile(path string) error {
	_, err := hashpart.ReadFile(path)
	return err
}

// CompareGroups 核对聚合输出与全内存参考结果：键集合相同且每组状态逐位相同。
func CompareGroups(got []acc.Group, want map[string]acc.State) error {
	if len(got) != len(want) {
		return fmt.Errorf("组数不同: 得到 %d, 期望 %d", len(got), len(want))
	}
	for _, g := range got {
		w, ok := want[g.Key]
		if !ok {
			return fmt.Errorf("多出分组 %q", g.Key)
		}
		if err := compareState(g.Key, g.State, w); err != nil {
			return err
		}
	}
	return nil
}

// Identical 判断两次输出是否逐字节相同（分组顺序与每组 Sum 位模式）。
func Identical(a, b []acc.Group) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].Key != b[i].Key || compareState("", a[i].State, b[i].State) != nil {
			return false
		}
	}
	return true
}

func compareState(key string, got, want acc.State) error {
	if got.Count != want.Count ||
		math.Float64bits(got.Sum) != math.Float64bits(want.Sum) ||
		math.Float64bits(got.Min) != math.Float64bits(want.Min) ||
		math.Float64bits(got.Max) != math.Float64bits(want.Max) {
		return fmt.Errorf("分组 %q 状态不符: 得到 %+v, 期望 %+v", key, got, want)
	}
	return nil
}
