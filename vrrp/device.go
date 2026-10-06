package vrrp

import (
	"math"
	"sync"
)

// Device is a single VRRP-style role state machine. Every exported operation
// is linearized by mu and uses only a fixed number of fields.
type Device struct {
	mu sync.Mutex

	id               string
	priority         int
	preempt          bool
	advertIntervalMS uint64

	role Role

	hasClock         bool
	lastAcceptedTime uint64

	monitorActive   bool
	monitorDeadline uint64

	downWaitActive bool
	downWaitUntil  uint64

	masterDueActive bool
	masterDue       uint64
	pendingAdvert   bool
}

// NewDevice validates and copies the static identity and initial settings.
func NewDevice(config Config) (*Device, error) {
	if !validConfig(config) {
		return nil, ErrInvalidArgument
	}

	return &Device{
		id:               config.ID,
		priority:         config.Priority,
		preempt:          config.Preempt,
		advertIntervalMS: config.AdvertIntervalMS,
	}, nil
}

func validConfig(config Config) bool {
	return config.ID != "" &&
		config.Priority >= 1 && config.Priority <= 255 &&
		config.AdvertIntervalMS >= 1 && config.AdvertIntervalMS <= 1_000_000
}

func validAdvertisement(advert Advertisement) bool {
	return advert.SenderID != "" &&
		advert.Priority >= 0 && advert.Priority <= 255 &&
		advert.AdvertIntervalMS >= 1 && advert.AdvertIntervalMS <= 1_000_000
}

func saturatingSum(now, duration uint64) uint64 {
	if duration > math.MaxUint64-now {
		return math.MaxUint64
	}
	return now + duration
}

func (d *Device) checkTime(now uint64) error {
	if d.hasClock && now < d.lastAcceptedTime {
		return ErrClockRegression
	}
	return nil
}

func (d *Device) acceptTime(now uint64) {
	d.lastAcceptedTime = now
	d.hasClock = true
}

func (d *Device) ownAdvertisement() Advertisement {
	return Advertisement{
		SenderID:         d.id,
		Priority:         d.priority,
		AdvertIntervalMS: d.advertIntervalMS,
	}
}

func result(advertisements ...Advertisement) EventResult {
	return EventResult{Advertisements: advertisements}
}

func (d *Device) becomeMaster(now uint64, emitImmediately bool) EventResult {
	d.role = RoleMaster
	d.monitorActive = false
	d.downWaitActive = false
	d.masterDueActive = true
	d.masterDue = saturatingSum(now, d.advertIntervalMS)

	if emitImmediately {
		d.pendingAdvert = false
		return result(d.ownAdvertisement())
	}

	d.pendingAdvert = true
	return EventResult{}
}

func (d *Device) becomeBackup(now uint64, senderInterval uint64) {
	d.role = RoleBackup
	d.monitorActive = true
	d.monitorDeadline = saturatingSum(now, 3*senderInterval)
	d.downWaitActive = false
	d.masterDueActive = false
	d.pendingAdvert = false
}

func (d *Device) becomeInitialized() {
	d.role = RoleInitialize
	d.monitorActive = false
	d.downWaitActive = false
	d.masterDueActive = false
	d.pendingAdvert = false
}

func (d *Device) adoptBackupAdvertisement(now uint64, advert Advertisement) {
	higherPriority := advert.Priority > d.priority
	samePriorityGreaterID := advert.Priority == d.priority && advert.SenderID > d.id
	lowerPriority := advert.Priority < d.priority

	if higherPriority || samePriorityGreaterID || (lowerPriority && !d.preempt) {
		d.monitorActive = true
		d.monitorDeadline = saturatingSum(now, 3*advert.AdvertIntervalMS)
		d.downWaitActive = false
	}
}

func (d *Device) handleBackupAdvertisement(now uint64, advert Advertisement) (EventResult, error) {
	if advert.Priority == 255 && d.priority == 255 {
		return EventResult{}, ErrIdentityConflict
	}
	if advert.Priority == d.priority && advert.SenderID == d.id {
		return EventResult{}, ErrIdentityConflict
	}

	if advert.Priority == 0 {
		d.downWaitActive = true
		d.downWaitUntil = saturatingSum(now, advert.AdvertIntervalMS/4)
		return EventResult{}, nil
	}

	d.adoptBackupAdvertisement(now, advert)
	return EventResult{}, nil
}

func (d *Device) handleMasterAdvertisement(now uint64, advert Advertisement) (EventResult, error) {
	if d.priority == 255 {
		if advert.Priority == 255 {
			return EventResult{}, ErrIdentityConflict
		}
		return EventResult{}, nil
	}

	if advert.Priority == 0 {
		d.masterDueActive = true
		d.masterDue = saturatingSum(now, d.advertIntervalMS)
		d.pendingAdvert = false
		return result(d.ownAdvertisement()), nil
	}

	if advert.Priority == d.priority && advert.SenderID == d.id {
		return EventResult{}, ErrIdentityConflict
	}

	higherPriority := advert.Priority > d.priority
	samePriorityGreaterID := advert.Priority == d.priority && advert.SenderID > d.id
	if higherPriority || samePriorityGreaterID {
		d.becomeBackup(now, advert.AdvertIntervalMS)
	}

	return EventResult{}, nil
}

// Role returns the current role.
func (d *Device) Role() Role {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.role
}

// Priority returns the current priority.
func (d *Device) Priority() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.priority
}

// Preempt returns whether preemption is enabled.
func (d *Device) Preempt() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.preempt
}

// Start moves an initialized device to backup, or immediately to master for an
// address owner. Starting an already-started device is idempotent.
func (d *Device) Start(now uint64) (EventResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.checkTime(now); err != nil {
		return EventResult{}, err
	}
	d.acceptTime(now)

	if d.role != RoleInitialize {
		return EventResult{}, nil
	}

	if d.priority == 255 {
		return d.becomeMaster(now, true), nil
	}

	d.becomeBackup(now, d.advertIntervalMS)
	return EventResult{}, nil
}

// Stop returns a running device to initialize. A master publishes priority 0.
func (d *Device) Stop(now uint64) (EventResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.checkTime(now); err != nil {
		return EventResult{}, err
	}
	d.acceptTime(now)

	if d.role == RoleMaster {
		d.becomeInitialized()
		return result(Advertisement{
			SenderID:         d.id,
			Priority:         0,
			AdvertIntervalMS: d.advertIntervalMS,
		}), nil
	}

	d.becomeInitialized()
	return EventResult{}, nil
}

// ReceiveAdvertisement handles one advertisement from a peer.
func (d *Device) ReceiveAdvertisement(advert Advertisement, now uint64) (EventResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if !validAdvertisement(advert) {
		return EventResult{}, ErrInvalidArgument
	}
	if err := d.checkTime(now); err != nil {
		return EventResult{}, err
	}
	if d.role == RoleInitialize {
		return EventResult{}, ErrNotStarted
	}

	d.acceptTime(now)
	if d.role == RoleBackup {
		return d.handleBackupAdvertisement(now, advert)
	}
	return d.handleMasterAdvertisement(now, advert)
}

// AdvanceTime evaluates backup liveness timers. Master advertisements remain
// pull-based and are therefore not emitted here.
func (d *Device) AdvanceTime(now uint64) (EventResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.checkTime(now); err != nil {
		return EventResult{}, err
	}
	d.acceptTime(now)

	if d.role != RoleBackup {
		return EventResult{}, nil
	}

	if d.downWaitActive && now >= d.downWaitUntil {
		return d.becomeMaster(now, false), nil
	} else if d.monitorActive && now >= d.monitorDeadline {
		return d.becomeMaster(now, false), nil
	}

	return EventResult{}, nil
}

// TakeAdvertisements pulls at most one due advertisement.
func (d *Device) TakeAdvertisements(now uint64) (EventResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.checkTime(now); err != nil {
		return EventResult{}, err
	}
	d.acceptTime(now)

	if d.role != RoleMaster {
		return EventResult{}, nil
	}

	if d.pendingAdvert {
		d.pendingAdvert = false
		d.masterDueActive = true
		d.masterDue = saturatingSum(now, d.advertIntervalMS)
		return result(d.ownAdvertisement()), nil
	}

	if d.masterDueActive && now >= d.masterDue {
		d.masterDue = saturatingSum(now, d.advertIntervalMS)
		return result(d.ownAdvertisement()), nil
	}

	return EventResult{}, nil
}

// SetPriority changes the priority used by future comparisons and advertisements.
func (d *Device) SetPriority(priority int, now uint64) (EventResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if priority < 1 || priority > 255 {
		return EventResult{}, ErrInvalidArgument
	}
	if (d.priority == 255) != (priority == 255) {
		return EventResult{}, ErrInvalidArgument
	}
	if err := d.checkTime(now); err != nil {
		return EventResult{}, err
	}

	d.acceptTime(now)
	d.priority = priority
	return EventResult{}, nil
}

// SetPreempt changes future preemption behavior without changing the role.
func (d *Device) SetPreempt(preempt bool, now uint64) (EventResult, error) {
	d.mu.Lock()
	defer d.mu.Unlock()

	if err := d.checkTime(now); err != nil {
		return EventResult{}, err
	}

	d.acceptTime(now)
	d.preempt = preempt
	return EventResult{}, nil
}
