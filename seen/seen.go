// Package seen 提供按来源站点的已见下限表。
//
// 每个站点为每个来源站点 o 维护一个整数下限 f[o]：凡序号不超过
// f[o] 的变更均已见过。FIFO 链路加"败者也转发"保证每个来源的序号
// 在任意站点按 1,2,3,... 的次序首次到达，因此一个整数下限即可精确
// 去重，状态规模与历史长度无关。
package seen

import "fmt"

// Floor 是单个站点的已见下限表。
type Floor struct {
	m map[int]int64
}

// New 返回一张所有来源下限均为 0 的空表。
func New() *Floor {
	return &Floor{m: make(map[int]int64)}
}

// Get 返回来源 origin 的已见下限（未见过的来源为 0）。
func (f *Floor) Get(origin int) int64 {
	return f.m[origin]
}

// SetOwn 在本地写时直接把本站点来源的下限置为 seq。
func (f *Floor) SetOwn(origin int, seq int64) {
	if seq <= f.m[origin] {
		panic(fmt.Sprintf("seen: internal error: SetOwn origin=%d seq=%d 未严格前进（当前下限 %d）", origin, seq, f.m[origin]))
	}
	f.m[origin] = seq
}

// IsDup 报告序号 seq 是否不超过来源 origin 的已见下限。
func (f *Floor) IsDup(origin int, seq int64) bool {
	return seq <= f.m[origin]
}

// Advance 在确认非重复后把来源 origin 的下限推进到 seq。
// 若 seq 不等于下限+1，属于内部错误，以 panic 暴露而非静默处理。
func (f *Floor) Advance(origin int, seq int64) {
	if want := f.m[origin] + 1; seq != want {
		panic(fmt.Sprintf("seen: internal error: Advance origin=%d seq=%d，期望 %d", origin, seq, want))
	}
	f.m[origin] = seq
}

// Clone 返回下限表的拷贝（只读查询用）。
func (f *Floor) Clone() map[int]int64 {
	out := make(map[int]int64, len(f.m))
	for o, s := range f.m {
		out[o] = s
	}
	return out
}
