// Package gset 表示 GROUPING SETS 中的单个分组集：
// 维度投影、GROUPING() 判定、组 ID 掩码计算。本包不依赖其他包。
package gset

import (
	"errors"
	"sort"
	"strings"
)

// NumDims 是事实流维度个数：0=A, 1=B, 2=C。
const NumDims = 3

// keySep 是投影键的分量分隔符；维度取值均为非空串，故无歧义。
const keySep = "\x1f"

// ErrDimOutOfRange 表示维度下标不在 [0, NumDims)。
var ErrDimOutOfRange = errors.New("gset: dimension index out of range")

var errDupDim = errors.New("gset: repeated dimension within a grouping set")

// Group 是一个分组集，即 {A,B,C} 的一个子集；mask 的 bit d=1
// 表示维度 d 参与分组。空 Group（mask=0）表示 () 全局聚合，合法。
type Group struct {
	mask uint8
	dims []int // 参与分组的维度下标，升序
}

// NewGroup 由维度下标构造分组集，允许空列表（表示 ()）。
// 下标越界或同一组内维度重复时返回错误。
func NewGroup(dims []int) (Group, error) {
	g := Group{dims: append([]int(nil), dims...)}
	for _, d := range dims {
		if uint(d) >= NumDims {
			return Group{}, ErrDimOutOfRange
		}
		if g.mask>>uint(d)&1 == 1 {
			return Group{}, errDupDim
		}
		g.mask |= 1 << uint(d)
	}
	sort.Ints(g.dims)
	return g, nil
}

// ID 返回组 ID：GROUPING 三元组 (A,B,C) 按高位到低位组成三比特数，
// 即 ID = GROUPING(A)*4 + GROUPING(B)*2 + GROUPING(C)，比特为 1 表示
// 该维度被聚合掉。例：(A)=(0,1,1)=0b011=3，(A,B)=(0,0,1)=0b001=1，
// ()=(1,1,1)=0b111=7。
func (g Group) ID() int {
	id := 0
	for d := 0; d < NumDims; d++ {
		if g.mask>>uint(d)&1 == 0 { // 维度 d 不参与分组 → GROUPING(d)=1
			id |= 1 << uint(NumDims-1-d)
		}
	}
	return id
}

// Grouping 返回维度 d 的 GROUPING()：参与分组为 0，被聚合掉为 1。
// d 必须在 [0, NumDims)。
func (g Group) Grouping(d int) int {
	return int(1 - g.mask>>uint(d)&1)
}

// Dims 返回参与分组的维度下标（升序拷贝）。
func (g Group) Dims() []int { return append([]int(nil), g.dims...) }

// Key 把事实取值元组投影到本分组集的键串。
// 空分组集 () 的键为空串（全局聚合只有一个键）。
func (g Group) Key(v [NumDims]string) string {
	if len(g.dims) == 0 {
		return ""
	}
	parts := make([]string, 0, len(g.dims))
	for _, d := range g.dims {
		parts = append(parts, v[d])
	}
	return strings.Join(parts, keySep)
}
