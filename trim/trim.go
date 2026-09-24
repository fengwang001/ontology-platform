// Package trim derives the trustworthy client address (deps: hoplex, netmatch).
package trim

import (
	"errors"
	"net/netip"
	"ontology/hoplex"
	"ontology/netmatch"
)

var (
	ErrBadAddress = netmatch.ErrBadAddress
	ErrBadPrefix  = netmatch.ErrBadPrefix
	ErrHopLimit   = errors.New("trim: hop count exceeds limit")
	ErrTrustLimit = errors.New("trim: trusted list size exceeds limit")
)

type Config struct{ MaxHops, MaxTrusted int }

type Trimmer struct {
	cfg        Config
	prefixes   []netip.Prefix
	trustSize  int
	matchCalls int
}

func New(c Config) *Trimmer { return &Trimmer{cfg: c} }

func (t *Trimmer) Trust(text string) error {
	if t.trustSize >= t.cfg.MaxTrusted {
		return ErrTrustLimit
	}
	p, err := netmatch.ParsePrefix(text)
	if err != nil {
		return err
	}
	t.prefixes, t.trustSize = append(t.prefixes, p), t.trustSize+1
	return nil
}

type Result struct {
	Client     netip.Addr
	Direct     bool
	Missing    bool
	StoppedAt  int // 1-based level from the nearest end
	Order      string
	MatchCalls int
}

func (t *Trimmer) trusted(addr netip.Addr) bool {
	for _, p := range t.prefixes {
		t.matchCalls++
		if netmatch.Contains(p, addr) {
			return true
		}
	}
	return false
}

func (t *Trimmer) Trim(chainText, pairsText, remoteText string) (*Result, error) {
	t.matchCalls = 0
	chain, err := hoplex.ParseChain(chainText)
	if err != nil {
		return nil, err
	}
	pairs, err := hoplex.ParsePairs(pairsText)
	if err != nil {
		return nil, err
	}
	if len(chain)+len(pairs) > t.cfg.MaxHops {
		return nil, ErrHopLimit
	}
	remote, err := netmatch.ParseAddress(remoteText)
	if err != nil {
		return nil, err
	}
	levels := max(len(chain), len(pairs))
	if levels == 0 {
		return &Result{Client: remote, Direct: true, Order: "direct"}, nil
	}
	order := "chain-only"
	switch {
	case len(pairs) == 0:
	case len(chain) > 0:
		order = "zip-from-nearest: chain then pairs at each level"
	default:
		order = "pairs-only"
	}
	for level := 0; level < levels; level++ {
		c, cOk := at(chain, level)
		p, pOk := at(pairs, level)
		if (cOk && !c.Present) || (pOk && !p.Present) {
			return t.result(remote, c, cOk, p, pOk, level+1, order, true)
		}
		if (!cOk || !t.trusted(c.Addr)) || (!pOk || !t.trusted(p.Addr)) {
			return t.result(remote, c, cOk, p, pOk, level+1, order, false)
		}
	}
	return t.result(remote, chain[0], len(chain) > 0, pairs[0], len(pairs) > 0,
		levels, order, false)
}

func at(hops []hoplex.Hop, level int) (hoplex.Hop, bool) {
	i := len(hops) - 1 - level
	if i < 0 {
		return hoplex.Hop{}, false
	}
	return hops[i], true
}
func (t *Trimmer) result(remote netip.Addr, c hoplex.Hop, cOk bool,
	p hoplex.Hop, pOk bool, level int, order string, missing bool) (*Result, error) {
	client := remote
	if cOk && c.Present {
		client = c.Addr
	} else if pOk && p.Present {
		client = p.Addr
	}
	return &Result{Client: client, Missing: missing, StoppedAt: level,
		Order: order, MatchCalls: t.matchCalls}, nil
}
