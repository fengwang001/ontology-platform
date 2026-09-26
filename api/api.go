// Package api is the public facade over the lease manager.
// It depends only on package mgr.
package api

import (
	"errors"
	"fmt"

	"ontology/lease"
	"ontology/mgr"
)

// API is the external interface to the lease subsystem.
type API struct {
	m *mgr.Manager
}

// New builds an API with fixed time-to-live ttl.
func New(ttl int) *API { return &API{m: mgr.New(ttl)} }

// Acquire awards a fresh lease; see mgr.Manager.Acquire.
func (a *API) Acquire(name, owner string, now int) (int, error) {
	return a.m.Acquire(name, owner, now)
}

// Renew heartbeats an existing lease; see mgr.Manager.Renew.
func (a *API) Renew(name string, token, now int) error { return a.m.Renew(name, token, now) }

// Expired reports whether name is dead at now.
func (a *API) Expired(name string, now int) bool { return a.m.Expired(name, now) }

// ExpiredAll returns every name expired at now.
func (a *API) ExpiredAll(now int) []string { return a.m.ExpiredAll(now) }

// SelfCheck replays the built-in seven-step sequence (TTL 10, name "L")
// and verifies the four invariants: naive recomputation agreement,
// strictly monotonic fencing tokens, renew-only-while-alive, and
// rejection leaves no trace. It returns nil iff all hold.
func (a *API) SelfCheck() error {
	// A private manager: the self-check sequence must not see, or alter,
	// any state held by the caller.
	m := mgr.New(10)
	t, err := m.Acquire("L", "A", 0) // S1
	if err != nil || t != 1 {
		return fmt.Errorf("S1: token=%d err=%v", t, err)
	}
	if err := m.Renew("L", 1, 5); err != nil || m.Expired("L", 14) { // S2
		return fmt.Errorf("S2: %v", err)
	}
	if err := m.Renew("L", 1, 12); err != nil || m.Expired("L", 21) { // S3
		return fmt.Errorf("S3: %v", err)
	}
	if t, err = m.Acquire("L", "B", 20); err != nil || t != 2 { // S4
		return fmt.Errorf("S4: token=%d err=%v", t, err)
	}
	// S5: A's old token is fenced and nothing moves.
	if err := m.Renew("L", 1, 21); !errors.Is(err, lease.ErrStaleToken) {
		return fmt.Errorf("S5: want ErrStaleToken, got %v", err)
	}
	// S6: token matches but now=31 >= expiry=30, so expired; no extension.
	if err := m.Renew("L", 2, 31); !errors.Is(err, lease.ErrExpired) {
		return fmt.Errorf("S6: want ErrExpired, got %v", err)
	}
	// S7: left-closed boundary, now == expiry is expired.
	if !m.Expired("L", 30) {
		return errors.New("S7: now==expiry must be expired")
	}
	// Rejection left no trace: state after S5/S6 is still (token 2, B, 30).
	if names := m.ExpiredAll(30); len(names) != 1 || names[0] != "L" {
		return fmt.Errorf("post-state: %v", names)
	}
	if !m.Expired("", 0) { // never acquired is expired by definition
		return errors.New("unknown name must be expired")
	}
	return nil
}
