// Package gset 负责分组集（grouping set）的表示与维度投影：
// 维度下标 0=A、1=B、2=C；它不依赖本模块其他包。
package gset

import "strconv"

// DimCount 固定三个维度 (A, B, C)。
const DimCount = 3

// bit[d] 是维度 d 在组 ID 中的位权。
// 组 ID 的二进制按 GROUPING(A)GROUPING(B)GROUPING(C) 由高位到低位书写，
// 故 A 权 4、B 权 2、C 权 1：(A)=0b011=3、(A,B)=0b001=1、()=0b111=7。
var bit = [DimCount]uint8{4, 2, 1}

// Group 是一个分组集，即 {A,B,C} 的一个子集。
// mask 的某位置 1 表示对应维度被聚合掉（GROUPING=1），mask 本身即组 ID。
type Group struct {
	mask uint8
}

// Parse 用维度下标构造分组集。下标越界、或同一组内维度重复时 ok=false。
// 空切片合法，表示组 ()（三比特全为 1，ID=7）。
func Parse(dims []int) (Group, bool) {
	var in uint8
	for _, d := range dims {
		if d < 0 || d >= DimCount {
			return Group{}, false
		}
		if in&bit[d] != 0 {
			return Group{}, false
		}
		in |= bit[d]
	}
	return Group{mask: ^in & 0x7}, true
}

// ID 返回组 ID（被聚合掉的维度对应比特为 1）。
func (g Group) ID() int { return int(g.mask) }

// Contains 报告维度 d 是否参与分组（GROUPING(d)==0）。
func (g Group) Contains(d int) bool { return g.mask&bit[d] == 0 }

// Grouping 返回 GROUPING(d)：参与分组为 0，被聚合掉为 1。
func (g Group) Grouping(d int) int {
	if g.Contains(d) {
		return 0
	}
	return 1
}

// Key 把三维取值 vals 投影到本组并编码成唯一键串。
// 仅参与分组的维度进入键；组 () 的投影键为空串。
// 采用长度前缀编码，取值中出现任意字符都不会产生歧义碰撞。
func (g Group) Key(vals [DimCount]string) string {
	buf := make([]byte, 0, 32)
	for d := 0; d < DimCount; d++ {
		if !g.Contains(d) {
			continue
		}
		buf = strconv.AppendInt(buf, int64(len(vals[d])), 10)
		buf = append(buf, ':')
		buf = append(buf, vals[d]...)
	}
	return string(buf)
}
