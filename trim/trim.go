// Package trim trims a forwarding hop chain against a trusted proxy
// list, walking from the nearest hop towards the farthest, and decides
// the outermost client address that can be trusted.
package trim

import (
	"errors"
	"fmt"
	"net/netip"

	"ontology/hoplex"
	"ontology/netmatch"
)

var (
	// ErrTooManyHops reports a merged hop chain beyond the hop limit.
	ErrTooManyHops = errors.New("trim: hop limit exceeded")
	// ErrTooManyCIDRs reports a trusted list beyond the list limit.
	ErrTooManyCIDRs = errors.New("trim: trusted list limit exceeded")
)

// AddrError reports an unparsable address at a hop (1-based, farthest
// hop is 1). Unwrap yields netmatch.ErrBadAddr.
type AddrError struct {
	Hop int
	Err error
}

func (e *AddrError) Error() string { return fmt.Sprintf("trim: hop %d: %v", e.Hop, e.Err) }
func (e *AddrError) Unwrap() error { return e.Err }

// Result is the outcome of one trim. Stop is the 0-based hop index
// (farthest is 0) where trimming stopped; -1 means no hops at all.
// HasAddr is false when the stop hop's address is missing.
type Result struct {
	Addr    netip.Addr
	HasAddr bool
	Stop    int
}

// Trimmer holds a trusted proxy list and the containment counter.
type Trimmer struct {
	maxHops  int
	prefixes []netip.Prefix
	contains int // unexported: number of containment checks performed
}

// New builds a Trimmer. Both limits are inclusive: exceeding them fails.
func New(maxHops, maxCIDRs int, cidrs []string) (*Trimmer, error) {
	if len(cidrs) > maxCIDRs {
		return nil, ErrTooManyCIDRs
	}
	t := &Trimmer{maxHops: maxHops}
	for _, c := range cidrs {
		p, err := netmatch.ParseCIDR(c)
		if err != nil {
			return nil, err
		}
		t.prefixes = append(t.prefixes, p)
	}
	return t, nil
}

// Client merges both headers (chain hops farther, forwarded hops
// nearer), then trims from the nearest hop towards the farthest.
// With no hops the client is the direct peer address.
func (t *Trimmer) Client(peer, chain, forwarded string) (Result, error) {
	hops := append(hoplex.ParseChain(chain), hoplex.ParseForwarded(forwarded)...)
	if len(hops) > t.maxHops {
		return Result{}, ErrTooManyHops
	}
	if len(hops) == 0 {
		a, err := netmatch.ParseAddr(peer)
		if err != nil {
			return Result{}, err
		}
		return Result{Addr: a, HasAddr: true, Stop: -1}, nil
	}
	var first netip.Addr
	for i := len(hops) - 1; i >= 0; i-- {
		if hops[i].Missing {
			return Result{Stop: i}, nil
		}
		a, err := netmatch.ParseAddr(hops[i].Addr)
		if err != nil {
			return Result{}, &AddrError{Hop: i + 1, Err: err}
		}
		if i == 0 {
			first = a
		}
		if !t.trusted(a) {
			return Result{Addr: a, HasAddr: true, Stop: i}, nil
		}
	}
	return Result{Addr: first, HasAddr: true, Stop: 0}, nil
}

func (t *Trimmer) trusted(a netip.Addr) bool {
	for _, p := range t.prefixes {
		t.contains++
		if netmatch.Contains(p, a) {
			return true
		}
	}
	return false
}
