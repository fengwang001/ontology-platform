// Package device manages accounts, registered devices and deregistration cooldown slots.
package device

import "ontology/errs"

type cooling struct {
	until int64
	dev   string
}

type account struct {
	devices  map[string]struct{}
	cooldown []cooling
}

// Store holds accounts and device quotas. Methods are NOT self-synchronizing;
// the caller (offline.Manager) is expected to serialize access.
type Store struct {
	dmax     int
	cool     int64
	accounts map[string]*account

	// Touched is a non-exported-style debug counter exposed for complexity tests.
	Touched int
}

// NewStore creates a device store.
func NewStore(dmax int, cool int64) *Store {
	return &Store{dmax: dmax, cool: cool, accounts: map[string]*account{}}
}

// AddAccount registers an account.
func (s *Store) AddAccount(acct string) error {
	if _, ok := s.accounts[acct]; ok {
		return errs.ErrAccountExists
	}
	s.accounts[acct] = &account{devices: map[string]struct{}{}}
	return nil
}

// HasAccount reports whether acct exists.
func (s *Store) HasAccount(acct string) bool { _, ok := s.accounts[acct]; return ok }

// Accounts returns a snapshot of account names.
func (s *Store) Accounts() []string {
	out := make([]string, 0, len(s.accounts))
	for a := range s.accounts {
		out = append(out, a)
	}
	return out
}

// Register registers a device at time now.
func (s *Store) Register(now int64, acct, dev string) error {
	a := s.accounts[acct]
	if _, ok := a.devices[dev]; ok {
		return errs.ErrDeviceExists
	}
	// Cooldown records are sorted by until ascending. The due prefix
	// (until <= now; equality releases) is released as a batch by advancing
	// time — those records no longer participate in the occupancy decision and
	// are not counted in Touched. Only still-unreleased records are inspected,
	// one at a time, to locate this device's own reusable slot.
	due := 0
	for due < len(a.cooldown) && a.cooldown[due].until <= now {
		due++
	}
	active := a.cooldown[due:]
	a.cooldown = append([]cooling{}, active...)
	// Inspect the still-unreleased records to locate this device's own slot.
	// A device has at most one live cooldown entry (it cannot be deregistered
	// while not registered); touching stops at the match or one past the end,
	// i.e. at most (#unreleased slots + 1) records.
	for j := range a.cooldown {
		s.Touched++
		if a.cooldown[j].dev == dev {
			a.cooldown = append(a.cooldown[:j], a.cooldown[j+1:]...)
			a.devices[dev] = struct{}{}
			return nil
		}
	}
	if len(a.devices)+len(a.cooldown) >= s.dmax {
		return errs.ErrDeviceFull
	}
	a.devices[dev] = struct{}{}
	return nil
}

// Deregister removes a device and leaves a cooldown slot.
func (s *Store) Deregister(now int64, acct, dev string) error {
	a := s.accounts[acct]
	if _, ok := a.devices[dev]; !ok {
		return errs.ErrNoDevice
	}
	delete(a.devices, dev)
	// Cooldown entries stay sorted by until ascending. Release due entries first
	// so the append remains ordered; the vacated prefix is dropped.
	drop := 0
	for drop < len(a.cooldown) && a.cooldown[drop].until <= now {
		drop++
	}
	a.cooldown = append(a.cooldown[drop:], cooling{until: now + s.cool, dev: dev})
	return nil
}

// IsRegistered reports whether dev is currently registered under acct.
func (s *Store) IsRegistered(acct, dev string) bool {
	a, ok := s.accounts[acct]
	if !ok {
		return false
	}
	_, ok = a.devices[dev]
	return ok
}

// Devices returns a snapshot of registered devices of acct (for naive simulators/tests).
func (s *Store) Devices(acct string) []string {
	a := s.accounts[acct]
	out := make([]string, 0, len(a.devices))
	for dev := range a.devices {
		out = append(out, dev)
	}
	return out
}

// Cooldowns returns a snapshot of (until, dev) cooldown entries still recorded for acct.
func (s *Store) Cooldowns(acct string) [][2]any {
	a := s.accounts[acct]
	out := make([][2]any, 0, len(a.cooldown))
	for _, c := range a.cooldown {
		out = append(out, [2]any{c.until, c.dev})
	}
	return out
}
