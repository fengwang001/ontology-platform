package iproute

import (
	"fmt"
	"sync"
)

// Table is an IPv4 longest-prefix-match routing table.
// It is safe for concurrent use.
type Table struct {
	mu     sync.RWMutex
	routes map[prefix]string
}

// New returns an empty routing table.
func New() *Table {
	return &Table{routes: make(map[prefix]string)}
}

// Add inserts a route for cidr pointing at next.
// The cidr must be in canonical form (host bits zero) and must not
// already exist; next must be non-empty.
func (t *Table) Add(cidr string, next string) error {
	p, err := parseCIDR(cidr)
	if err != nil {
		return err
	}
	if next == "" {
		return ErrEmptyNext
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.routes[p]; ok {
		return fmt.Errorf("%w: %s", ErrDuplicateRoute, p.canonical())
	}
	t.routes[p] = next
	return nil
}

// Delete removes the route for cidr. It fails if the route does not exist.
func (t *Table) Delete(cidr string) error {
	p, err := parseCIDR(cidr)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.routes[p]; !ok {
		return fmt.Errorf("%w: %s", ErrRouteNotFound, p.canonical())
	}
	delete(t.routes, p)
	return nil
}

// Lookup finds the longest matching prefix for ip. It returns the next
// hop and the canonical form of the matched prefix, or ErrNoRoute.
func (t *Table) Lookup(ip string) (next string, matched string, err error) {
	addr, err := parseIPv4(ip)
	if err != nil {
		return "", "", err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	for bits := 32; bits >= 0; bits-- {
		p := prefix{addr: addr & maskFor(bits), bits: bits}
		if n, ok := t.routes[p]; ok {
			return n, p.canonical(), nil
		}
	}
	return "", "", fmt.Errorf("%w: %s", ErrNoRoute, ip)
}
