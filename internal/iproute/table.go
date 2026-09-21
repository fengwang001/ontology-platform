package iproute

import (
	"fmt"
	"sync"
)

// Table is an IPv4 longest-prefix-match routing table.
// It is safe for concurrent use.
type Table struct {
	mu     sync.RWMutex
	routes map[uint64]string // key: maskLen<<32 | network address
}

// New returns an empty routing table.
func New() *Table {
	return &Table{routes: make(map[uint64]string)}
}

// key packs a prefix into a sortable map key.
func key(p prefix) uint64 {
	return uint64(p.len)<<32 | uint64(p.addr)
}

// Add installs a route for cidr with the given next hop. The CIDR must be
// canonical (host bits zero); adding an existing prefix is an error.
func (t *Table) Add(cidr string, next string) error {
	p, err := parsePrefix(cidr)
	if err != nil {
		return err
	}
	if next == "" {
		return fmt.Errorf("%w: cannot add %s", ErrEmptyNext, p.canonical())
	}
	k := key(p)
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.routes[k]; exists {
		return fmt.Errorf("%w: %s", ErrDuplicateRoute, p.canonical())
	}
	t.routes[k] = next
	return nil
}

// Delete removes the route for cidr. Deleting a missing prefix is an error.
func (t *Table) Delete(cidr string) error {
	p, err := parsePrefix(cidr)
	if err != nil {
		return err
	}
	k := key(p)
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.routes[k]; !exists {
		return fmt.Errorf("%w: %s", ErrRouteNotFound, p.canonical())
	}
	delete(t.routes, k)
	return nil
}

// Lookup finds the most specific prefix containing ip. It returns the
// associated next hop and the canonical form of the matched prefix, or
// ErrNoRoute if nothing matches.
func (t *Table) Lookup(ip string) (next string, matched string, err error) {
	addr, err := parseIP(ip)
	if err != nil {
		return "", "", err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	for length := 32; length >= 0; length-- {
		p := prefix{addr: addr & mask(length), len: length}
		if n, ok := t.routes[key(p)]; ok {
			return n, p.canonical(), nil
		}
	}
	return "", "", fmt.Errorf("%w: %s", ErrNoRoute, ip)
}
