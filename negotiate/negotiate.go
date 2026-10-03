// Package negotiate 实现两端声明的版本区间与特性求交。
package negotiate

import "ontology/caps"

// Result 是一次成功协商的结果。
type Result struct {
	Version int
	Enabled uint32
	Scanned uint64 // 本次检视的特性数（非导出计数器的可读快照）
}

// scannedTotal 为包级非导出计数器，累计所有协商检视的特性数。
// 每次检视只做常数次位运算/比较，与版本区间长度无关。
var scannedTotal uint64

// Negotiate 按规则对两端声明做求交；Q 为额外必需位（重协商时传使用中集合）。
// 拒绝次序：参数非法 → ErrNoVersion → ErrMissing → ErrDenied → ErrWindow。
func Negotiate(table *caps.Table, client caps.Hello, cr int, server caps.Hello, extraReq uint32) (Result, *caps.Error) {
	if table == nil || !caps.ValidHello(client) || !caps.ValidHello(server) || !caps.ValidRole(cr) {
		return Result{}, caps.ErrInvalid
	}

	var scanned uint64
	scan := func(ok bool) bool { scanned++; return ok }

	// 版本区间求交。
	lower := client.Lo
	if server.Lo > lower {
		lower = server.Lo
	}
	upper := client.Hi
	if server.Hi < upper {
		upper = server.Hi
	}
	if lower > upper {
		return Result{}, caps.ErrNoVersion
	}

	q := (client.Req | server.Req | extraReq)
	commonSup := client.Sup & server.Sup

	// 必需特性须同时被两端支持：报编号最小者。
	for f := 0; f < caps.NumFeatures; f++ {
		b := uint32(1) << uint(f)
		if q&b != 0 && scan(commonSup&b == 0) {
			return Result{}, caps.ErrFeature(caps.CodeMissing, f)
		}
	}

	// 必需特性的角色门槛须被客户端角色满足：报编号最小者。
	for f := 0; f < caps.NumFeatures; f++ {
		b := uint32(1) << uint(f)
		if q&b != 0 && scan(table.SpecAt(f).Role > cr) {
			return Result{}, caps.ErrFeature(caps.CodeDenied, f)
		}
	}

	// 必需特性生效窗口聚合：lower=max(L, minV)，upper=min(H, maxV-1)。
	for f := 0; f < caps.NumFeatures; f++ {
		b := uint32(1) << uint(f)
		if q&b == 0 {
			continue
		}
		scan(true)
		s := table.SpecAt(f)
		if s.MinV > lower {
			lower = s.MinV
		}
		if s.MaxV-1 < upper {
			upper = s.MaxV - 1
		}
	}
	if lower > upper {
		return Result{}, caps.ErrWindow
	}

	// 版本取最高可用版本；启用集为两端 sup 交集中角色足够且在 v 可用者。
	v := upper
	var enabled uint32
	for f := 0; f < caps.NumFeatures; f++ {
		b := uint32(1) << uint(f)
		if commonSup&b != 0 &&
			scan(table.SpecAt(f).Role <= cr) &&
			table.AvailableAt(f, v) {
			enabled |= b
		}
	}

	// Q 必然包含于 E：前置判定已保证角色与窗口（v=upper >= 各 minV，v < 各 maxV）。
	scannedTotal += scanned
	return Result{Version: v, Enabled: enabled, Scanned: scanned}, nil
}
