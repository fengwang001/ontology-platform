// Package trim 按可信代理清单从近端向远端裁剪转发链，定出最外层可信客户端地址。
package trim

import (
	"errors"
	"fmt"
	"net/netip"

	"ontology/hoplex"
	"ontology/netmatch"
)

var (
	ErrBadAddr      = netmatch.ErrBadAddr
	ErrBadCIDR      = netmatch.ErrBadCIDR
	ErrTooManyHops  = errors.New("trim: hop count over limit")
	ErrTooManyCIDRs = errors.New("trim: trusted network count over limit")
)

// AddrError 报告第 Hop 跳（合并序列从最远端起 1 计数；0 为直连对端）地址非法。
type AddrError struct {
	Hop  int
	Text string
}

func (e *AddrError) Error() string {
	return fmt.Sprintf("trim: hop %d: invalid address %q", e.Hop, e.Text)
}

func (e *AddrError) Unwrap() error { return ErrBadAddr }

// Result 是一次裁剪的结果。
type Result struct {
	Client    netip.Addr // HasClient 为 false 时无效
	HasClient bool       // 终止跳地址缺失时为 false
	StoppedAt int        // 裁剪终止的跳号（1 起）；直连为 0
}

// Trimmer 持有可信清单与上限；checks 统计网段包含判定调用次数。
type Trimmer struct {
	trusted []netip.Prefix
	maxHops int
	checks  int
}

// New 构造裁剪器；清单条数越过上限或网段非法即失败。
func New(maxHops, maxCIDRs int, cidrs []string) (*Trimmer, error) {
	if len(cidrs) > maxCIDRs {
		return nil, fmt.Errorf("%w: %d > %d", ErrTooManyCIDRs, len(cidrs), maxCIDRs)
	}
	t := &Trimmer{maxHops: maxHops}
	for _, c := range cidrs {
		p, err := netmatch.ParsePrefix(c)
		if err != nil {
			return nil, err
		}
		t.trusted = append(t.trusted, p)
	}
	return t, nil
}

// Trim 合并两种头部，从最近端向远端逐跳裁剪；peer 为直连对端地址文本。
func (t *Trimmer) Trim(peer, chainHeader, fwdHeader string) (Result, error) {
	hops := hoplex.Parse(chainHeader, fwdHeader)
	if len(hops) > t.maxHops {
		return Result{}, fmt.Errorf("%w: %d > %d", ErrTooManyHops, len(hops), t.maxHops)
	}
	addrs := make([]netip.Addr, len(hops))
	for i, h := range hops {
		if !h.HasAddr {
			continue
		}
		a, err := netmatch.ParseAddr(h.Addr)
		if err != nil {
			return Result{}, &AddrError{Hop: i + 1, Text: h.Addr}
		}
		addrs[i] = a
	}
	if len(hops) == 0 {
		a, err := netmatch.ParseAddr(peer)
		if err != nil {
			return Result{}, &AddrError{Hop: 0, Text: peer}
		}
		return Result{Client: a, HasClient: true}, nil
	}
	for i := len(hops) - 1; i >= 0; i-- {
		if !hops[i].HasAddr {
			return Result{StoppedAt: i + 1}, nil
		}
		if !t.trustedAddr(addrs[i]) {
			return Result{Client: addrs[i], HasClient: true, StoppedAt: i + 1}, nil
		}
	}
	return Result{Client: addrs[0], HasClient: true, StoppedAt: 1}, nil
}

func (t *Trimmer) trustedAddr(a netip.Addr) bool {
	for _, p := range t.trusted {
		t.checks++
		if netmatch.Contains(p, a) {
			return true
		}
	}
	return false
}
