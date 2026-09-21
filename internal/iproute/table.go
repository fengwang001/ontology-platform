package iproute

import (
	"fmt"
	"sync"
)

type Table struct {
	mu     sync.RWMutex
	routes map[prefix]string
}

func New() *Table {
	return &Table{routes: make(map[prefix]string)}
}

func (t *Table) Add(cidr string, next string) error {
	if next == "" {
		return ErrEmptyNext
	}
	p, err := parseCIDR(cidr)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.routes[p]; ok {
		return fmt.Errorf("%w: %s", ErrDuplicate, p)
	}
	t.routes[p] = next
	return nil
}

func (t *Table) Delete(cidr string) error {
	p, err := parseCIDR(cidr)
	if err != nil {
		return err
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, ok := t.routes[p]; !ok {
		return fmt.Errorf("%w: %s", ErrNotFound, p)
	}
	delete(t.routes, p)
	return nil
}

func (t *Table) Lookup(ip string) (next string, matched string, err error) {
	addr, err := parseIPv4(ip)
	if err != nil {
		return "", "", err
	}
	t.mu.RLock()
	defer t.mu.RUnlock()
	for length := 32; length >= 0; length-- {
		p := prefix{network: addr & maskFor(length), length: length}
		if hop, ok := t.routes[p]; ok {
			return hop, p.String(), nil
		}
	}
	return "", "", fmt.Errorf("%w: %s", ErrNoRoute, ip)
}
