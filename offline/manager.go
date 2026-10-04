// Package offline composes device, license and playback into one serialized manager.
package offline

import (
	"ontology/device"
	"ontology/errs"
	"ontology/license"
	"ontology/playback"
	"sync"
)

// Params are the construction parameters.
type Params struct {
	Dmax int   // device quota per account, 1..100
	Cool int64 // deregistration cooldown seconds, 1..1e9
	Lr   int64 // rental length seconds, 1..1e9
	Lp   int64 // playback window seconds, 1..1e9
	Omax int   // valid licenses per account, 1..1e4
}

// Manager is the concurrency-safe entry point of the whole system.
type Manager struct {
	mu       sync.Mutex
	lastNow  int64
	devices  *device.Store
	licenses *license.Store
	play     *playback.Service
}

// New constructs a Manager. Invalid params return an error.
func New(p Params) (*Manager, error) {
	if p.Dmax < 1 || p.Dmax > 100 ||
		p.Cool < 1 || p.Cool > 1_000_000_000 ||
		p.Lr < 1 || p.Lr > 1_000_000_000 ||
		p.Lp < 1 || p.Lp > 1_000_000_000 ||
		p.Omax < 1 || p.Omax > 10_000 {
		return nil, errs.ErrInvalidParam
	}
	dev := device.NewStore(p.Dmax, p.Cool)
	lic := license.NewStore(p.Lr, p.Lp, p.Omax, dev)
	return &Manager{devices: dev, licenses: lic, play: playback.NewService(lic)}, nil
}

func validID(s string) bool {
	return s != ""
}

func validNow(now int64) bool { return now >= 0 && now <= 1_000_000_000_000 }

func (m *Manager) checkClock(now int64) error {
	if now < m.lastNow {
		return errs.ErrClockRewind
	}
	return nil
}

// AddAccount registers an account.
func (m *Manager) AddAccount(now int64, acct string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validNow(now) || !validID(acct) {
		return errs.ErrInvalidParam
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	if err := m.devices.AddAccount(acct); err != nil {
		return err
	}
	m.lastNow = now
	return nil
}

// AddTitle registers a title ending at titleEnd.
func (m *Manager) AddTitle(now int64, title string, titleEnd int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validNow(now) || !validID(title) || titleEnd <= now {
		return errs.ErrInvalidParam
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	if err := m.licenses.AddTitle(title, titleEnd); err != nil {
		return err
	}
	m.lastNow = now
	return nil
}

// SetTitleEnd moves the takedown time; end must be > now.
func (m *Manager) SetTitleEnd(now int64, title string, end int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validNow(now) || !validID(title) || end <= now {
		return errs.ErrInvalidParam
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	if err := m.licenses.SetTitleEnd(now, title, end); err != nil {
		return err
	}
	m.lastNow = now
	return nil
}

// Register registers a device.
func (m *Manager) Register(now int64, acct, dev string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validNow(now) || !validID(acct) || !validID(dev) {
		return errs.ErrInvalidParam
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	if !m.devices.HasAccount(acct) {
		return errs.ErrNoAccount
	}
	m.devices.Touched = 0
	if err := m.devices.Register(now, acct, dev); err != nil {
		return err
	}
	m.lastNow = now
	return nil
}

// Deregister removes a device and deletes its licenses.
func (m *Manager) Deregister(now int64, acct, dev string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validNow(now) || !validID(acct) || !validID(dev) {
		return errs.ErrInvalidParam
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	if !m.devices.HasAccount(acct) {
		return errs.ErrNoAccount
	}
	m.devices.Touched = 0
	if err := m.devices.Deregister(now, acct, dev); err != nil {
		return err
	}
	m.licenses.DeleteDeviceLicenses(acct, dev)
	m.lastNow = now
	return nil
}

// Download issues or renews an offline license.
func (m *Manager) Download(now int64, acct, dev, title string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validNow(now) || !validID(acct) || !validID(dev) || !validID(title) {
		return errs.ErrInvalidParam
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	if !m.devices.HasAccount(acct) || !m.licenses.HasTitle(title) {
		if !m.devices.HasAccount(acct) {
			return errs.ErrNoAccount
		}
		return errs.ErrNoTitle
	}
	if !m.devices.IsRegistered(acct, dev) {
		return errs.ErrNoDevice
	}
	end, _ := m.licenses.TitleEnd(title)
	if now >= end {
		return errs.ErrTitleEnded
	}
	r := m.licenses.Get(acct, dev, title)
	if r != nil {
		switch {
		case r.Played && m.licenses.IsValid(r, title, now):
			return errs.ErrAlreadyPlayed
		case !r.Played && m.licenses.IsValid(r, title, now):
			// Renewal path: reset counter and delegate (store also classifies,
			// but ordering above guarantees it takes the renewal branch).
		}
	}
	m.licenses.Touched = 0
	if err := m.licenses.Download(now, acct, dev, title); err != nil {
		return err
	}
	m.lastNow = now
	return nil
}

// Play permits playback and records first-play time.
func (m *Manager) Play(now int64, acct, dev, title string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validNow(now) || !validID(acct) || !validID(dev) || !validID(title) {
		return errs.ErrInvalidParam
	}
	if err := m.checkClock(now); err != nil {
		return err
	}
	if !m.devices.HasAccount(acct) || !m.licenses.HasTitle(title) {
		if !m.devices.HasAccount(acct) {
			return errs.ErrNoAccount
		}
		return errs.ErrNoTitle
	}
	if !m.devices.IsRegistered(acct, dev) {
		return errs.ErrNoDevice
	}
	if m.licenses.Get(acct, dev, title) == nil {
		return errs.ErrNoLicense
	}
	m.licenses.Touched = 0
	if err := m.play.Play(now, acct, dev, title); err != nil {
		return err
	}
	m.lastNow = now
	return nil
}

// Status is a read-only query.
func (m *Manager) Status(acct, dev, title string, now int64) (playback.Result, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !validNow(now) || !validID(acct) || !validID(dev) || !validID(title) {
		return playback.Result{}, false
	}
	return m.play.Status(acct, dev, title, now)
}

// Touched counters (reset per operation) for complexity verification.
func (m *Manager) DeviceTouched() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.devices.Touched
}

func (m *Manager) LicenseTouched() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.licenses.Touched
}
