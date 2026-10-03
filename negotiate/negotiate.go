// Package negotiate 实现两端声明之间的版本区间与特性求交。
package negotiate

import "ontology/caps"

// 检视预算：一次协商最多检查 32 个特性的常数倍，与版本区间长度无关。
const maxInspected = caps.FeatureCount * 4

var inspectedCount uint64

// Inspected 返回自进程启动以来协商过程累计检视的特性次数（用于复杂度断言）。
func Inspected() uint64 {
	return atomicLoad(&inspectedCount)
}

// Outcome 是一次协商的完整结果与可复现诊断信息。
type Outcome struct {
	OK      bool   // 是否协商成功
	Ver     uint64 // 选中的最高可用版本（成功时）
	Enabled uint32 // 启用特性位集 E
	L, H    uint64 // 两端版本区间交集
	Q       uint32 // 必需特性并集（重协商时含使用中特性）
	Lower   uint64 // 必需特性窗口下界
	Upper   uint64 // 必需特性窗口上界（=选中版本）
	Common  uint32 // 两端 sup 交集
}

// Params 是一次协商的全部输入；extra 额外并入必需集（重协商时传使用中集合 U）。
type Params struct {
	Table  *caps.Table
	Client caps.Hello
	CR     uint8
	Server caps.Hello
	Extra  uint32
}

// Pick 按 NoVersion→Missing→Denied→Window 的固定次序求交。
// 调用方负责参数合法性校验；本函数是确定性的纯函数。
func Pick(p Params) (Outcome, *caps.Error) {
	out := Outcome{}
	client, server := p.Client, p.Server

	out.L = maxU64(client.Lo, server.Lo)
	out.H = minU64(client.Hi, server.Hi)
	if out.L > out.H {
		return out, caps.NewError(caps.ReasonNoVersion, -1,
			"L > H: no common protocol version")
	}

	out.Common = client.Sup & server.Sup
	rawQ := client.Req | server.Req | p.Extra
	if miss := firstMissing(rawQ, out.Common); miss >= 0 {
		return out, caps.NewError(caps.ReasonMissing, miss,
			"required feature missing from common support")
	}
	out.Q = rawQ

	if denied := firstDenied(p.Table, out.Q, p.CR); denied >= 0 {
		f, _ := p.Table.At(uint8(denied))
		return out, caps.NewError(caps.ReasonDenied, denied,
			"required feature role "+itoa(int(f.Role))+" > client role "+itoa(int(p.CR)))
	}

	out.Lower = out.L
	out.Upper = out.H
	for _, i := range caps.Bits(out.Q) {
		f, ok := p.Table.At(i)
		atomicAdd(&inspectedCount, 1)
		if !ok {
			continue
		}
		if f.MinV > out.Lower {
			out.Lower = f.MinV
		}
		if f.MaxV-1 < out.Upper {
			out.Upper = f.MaxV - 1
		}
	}
	if out.Lower > out.Upper {
		return out, caps.NewError(caps.ReasonWindow, -1,
			"required feature windows have no common version")
	}

	out.Ver = out.Upper
	out.Enabled = enabledAt(p.Table, out.Common, p.CR, out.Ver)
	out.OK = true
	return out, nil
}

// firstMissing 返回必需集 rawQ 中不在两端 sup 交集 common 的最小编号，无则 -1。
func firstMissing(rawQ, common uint32) int {
	missing := rawQ &^ common
	for i := 0; i < caps.FeatureCount; i++ {
		atomicAdd(&inspectedCount, 1)
		if missing&(1<<uint(i)) != 0 {
			return i
		}
	}
	return -1
}

// firstDenied 返回 Q 中所需角色高于 cr 的最小编号，无则 -1。
func firstDenied(t *caps.Table, q uint32, cr uint8) int {
	for _, i := range caps.Bits(q) {
		f, ok := t.At(i)
		atomicAdd(&inspectedCount, 1)
		if ok && f.Role > cr {
			return int(i)
		}
	}
	return -1
}

// enabledAt 求交集中角色不高于 cr 且在版本 v 可用的全部特性。
func enabledAt(t *caps.Table, common uint32, cr uint8, v uint64) uint32 {
	var e uint32
	for _, i := range caps.Bits(common) {
		f, ok := t.At(i)
		atomicAdd(&inspectedCount, 1)
		if ok && f.Role <= cr && t.AvailableAt(i, v) {
			e |= 1 << i
		}
	}
	return e
}

func maxU64(a, b uint64) uint64 {
	if a > b {
		return a
	}
	return b
}

func minU64(a, b uint64) uint64 {
	if a < b {
		return a
	}
	return b
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
