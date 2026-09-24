// Package netmatch 提供 IP 地址与网段的解析及包含判定。
package netmatch

import (
	"errors"
	"net/netip"
	"strings"
)

var (
	// ErrBadAddr 表示地址文本无法解析。
	ErrBadAddr = errors.New("netmatch: invalid address")
	// ErrBadCIDR 表示网段文本非法（含前缀长度超出地址族位数）。
	ErrBadCIDR = errors.New("netmatch: invalid network")
)

// ParseAddr 解析 IPv4/IPv6 地址文本；剥离端口（含 [v6]:port）与区域标识。
func ParseAddr(s string) (netip.Addr, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return netip.Addr{}, ErrBadAddr
	}
	if s[0] == '[' {
		i := strings.IndexByte(s, ']')
		if i < 0 {
			return netip.Addr{}, ErrBadAddr
		}
		s = s[1:i]
	} else if strings.Count(s, ":") == 1 {
		s, _, _ = strings.Cut(s, ":")
	}
	if i := strings.IndexByte(s, '%'); i >= 0 {
		s = s[:i]
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}, ErrBadAddr
	}
	return a, nil
}

// ParsePrefix 解析前缀长度写法的网段；netip 已拒绝超出地址族位数的前缀。
func ParsePrefix(s string) (netip.Prefix, error) {
	p, err := netip.ParsePrefix(strings.TrimSpace(s))
	if err != nil {
		return netip.Prefix{}, ErrBadCIDR
	}
	return p, nil
}

// Contains 判定网段是否包含地址。IPv4 映射的 IPv6 地址与对应 IPv4 地址
// 视为同一地址：两个方向的网段都能命中。
func Contains(p netip.Prefix, a netip.Addr) bool {
	if p.Contains(a) {
		return true
	}
	if a.Is4In6() {
		return p.Contains(a.Unmap())
	}
	if a.Is4() && p.Addr().Is4In6() {
		var b [16]byte
		b[10], b[11] = 0xff, 0xff
		copy(b[12:], a.AsSlice())
		return p.Contains(netip.AddrFrom16(b))
	}
	return false
}
