// Package compat 提供本体平台快照格式的兼容性校验能力。
//
// 包内按职责划分为四个协作部分：
//   - 版本号范围判定（version.go）
//   - 对象类型/属性类型系统（schema.go）
//   - 属性级兼容规则引擎（judge.go）
//   - 跨版本的传递性逐级核对（chain.go）
package compat

import "fmt"

// Version 是可比较的格式版本号。按 Major、Minor、Patch 字典序比较。
type Version struct {
	Major int
	Minor int
	Patch int
}

// Compare 返回 -1 / 0 / +1，语义同 strings.Compare。
func Compare(a, b Version) int {
	switch {
	case a.Major != b.Major:
		return sign(a.Major - b.Major)
	case a.Minor != b.Minor:
		return sign(a.Minor - b.Minor)
	default:
		return sign(a.Patch - b.Patch)
	}
}

func sign(x int) int {
	switch {
	case x < 0:
		return -1
	case x > 0:
		return 1
	default:
		return 0
	}
}

func (v Version) String() string {
	return fmt.Sprintf("%d.%d.%d", v.Major, v.Minor, v.Patch)
}

// Range 是闭区间 [Min, Max] 的版本号范围。
type Range struct {
	Min Version
	Max Version
}

// Contains 判断 v 是否落在闭区间内（边界视为可识别）。
func (r Range) Contains(v Version) bool {
	return Compare(v, r.Min) >= 0 && Compare(v, r.Max) <= 0
}
