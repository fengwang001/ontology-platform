package vrrp

import "sync"

// Device is a single VRRP-style state machine. It is safe for concurrent
// use: concurrent Handle calls behave as if executed in some serial
// order. Every event is processed in constant time regardless of history
// length or the size of the time span it crosses.
type Device struct {
	mu sync.Mutex

	id       string
	priority int
	preempt  bool
	interval int

	role  Role
	clock uint64

	watchBase     uint64
	watchInterval int
	skewSet       bool
	skewDeadline  uint64
	nextAdvert    uint64
}

// NewDevice validates cfg and returns a device in the init role.
func NewDevice(cfg Config) (*Device, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Device{
		id:       cfg.ID,
		priority: cfg.Priority,
		preempt:  cfg.Preempt,
		interval: cfg.AdvertIntervalMs,
		role:     RoleInit,
	}, nil
}

// Handle processes a single event. Rejected events leave state, timers
// and clock untouched.
func (d *Device) Handle(ev Event) (Result, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.handle(ev)
}

func (d *Device) handle(ev Event) (Result, error) {
	if err := d.validate(ev); err != nil {
		return Result{}, err
	}
	if ev.Now < d.clock {
		return Result{}, &Error{Kind: ErrClockRollback,
			Detail: "event time is before the last accepted event time"}
	}
	res, err := d.dispatch(ev)
	if err != nil {
		return Result{}, err
	}
	d.clock = ev.Now
	return res, nil
}

// validate checks argument level errors, the highest precedence
// rejection class.
func (d *Device) validate(ev Event) error {
	switch ev.Kind {
	case EvReceiveAdvert:
		adv := ev.Advert
		if adv.SenderID == "" {
			return &Error{Kind: ErrInvalidArgument, Detail: "advert sender id must not be empty"}
		}
		if adv.Priority < 0 || adv.Priority > OwnerPriority {
			return &Error{Kind: ErrInvalidArgument, Detail: "advert priority out of range [0,255]"}
		}
		if adv.IntervalMs < 1 || adv.IntervalMs > MaxAdvertIntervalMs {
			return &Error{Kind: ErrInvalidArgument, Detail: "advert interval out of range"}
		}
	case EvSetPriority:
		if ev.Priority < 1 || ev.Priority > OwnerPriority {
			return &Error{Kind: ErrInvalidArgument, Detail: "priority out of range [1,255]"}
		}
		if ev.Priority == OwnerPriority {
			return &Error{Kind: ErrInvalidArgument, Detail: "cannot become address owner at runtime"}
		}
		if d.priority == OwnerPriority {
			return &Error{Kind: ErrInvalidArgument, Detail: "address owner cannot change priority at runtime"}
		}
	case EvStart, EvStop, EvAdvanceTime, EvTakeAdvert, EvSetPreempt:
	default:
		return &Error{Kind: ErrInvalidArgument, Detail: "unknown event kind"}
	}
	return nil
}

func (d *Device) dispatch(ev Event) (Result, error) {
	switch ev.Kind {
	case EvSetPriority:
		d.priority = ev.Priority
		return Result{Reason: "set-priority: applied, affects future adverts and comparisons only"}, nil
	case EvSetPreempt:
		d.preempt = ev.Preempt
		return Result{Reason: "set-preempt: applied"}, nil
	case EvStart:
		return d.start(ev.Now), nil
	case EvStop:
		return d.stop(), nil
	}
	switch d.role {
	case RoleInit:
		// Advert-class events must report not-started; time advance is an
		// accepted no-op.
		if ev.Kind == EvReceiveAdvert || ev.Kind == EvTakeAdvert {
			return Result{}, &Error{Kind: ErrNotStarted, Detail: "device is in init state"}
		}
		return Result{Reason: "init: time advance ignored"}, nil
	case RoleBackup:
		return d.dispatchBackup(ev)
	case RoleMaster:
		return d.dispatchMaster(ev)
	}
	panic("vrrp: unreachable role")
}

func (d *Device) start(now uint64) Result {
	switch d.role {
	case RoleInit:
		if d.priority == OwnerPriority {
			d.role = RoleMaster
			d.nextAdvert = now + uint64(d.interval)
			return Result{Adverts: []Advert{d.selfAdvert()},
				Reason: "start: address owner becomes master immediately"}
		}
		d.enterBackup(now, d.interval)
		return Result{Reason: "start: enter backup, arm master-down watch with own interval"}
	default:
		return Result{Reason: "start: already started, ignored"}
	}
}

func (d *Device) stop() Result {
	if d.role == RoleMaster {
		adv := Advert{SenderID: d.id, Priority: YieldPriority, IntervalMs: d.interval}
		d.enterInit()
		return Result{Adverts: []Advert{adv},
			Reason: "stop: master yields with zero-priority advert, back to init"}
	}
	d.enterInit()
	return Result{Reason: "stop: back to init"}
}

func (d *Device) dispatchBackup(ev Event) (Result, error) {
	switch ev.Kind {
	case EvReceiveAdvert:
		return d.backupAdvert(ev.Advert, ev.Now)
	case EvAdvanceTime:
		return d.backupAdvance(ev.Now), nil
	case EvTakeAdvert:
		return Result{Reason: "backup: no advert to send"}, nil
	}
	panic("vrrp: unreachable backup event")
}

func (d *Device) backupAdvert(adv Advert, now uint64) (Result, error) {
	if adv.Priority == YieldPriority {
		d.skewSet = true
		d.skewDeadline = now + uint64(adv.IntervalMs)/4
		return Result{Reason: "backup: zero-priority advert arms promotion wait"}, nil
	}
	if err := d.checkConflicts(adv); err != nil {
		return Result{}, err
	}
	if better(adv, d.id, d.priority) {
		d.acceptAdvert(adv, now)
		return Result{Reason: "backup: accepted superior advert, watch reset"}, nil
	}
	if d.preempt {
		return Result{Reason: "backup: inferior advert ignored (preempt on)"}, nil
	}
	d.acceptAdvert(adv, now)
	return Result{Reason: "backup: accepted inferior advert (preempt off), watch reset"}, nil
}

func (d *Device) backupAdvance(now uint64) Result {
	// The promotion wait wins when both timers expire together.
	if d.skewSet && now >= d.skewDeadline {
		return d.promote(now, "backup: promotion wait expired, become master")
	}
	if now >= d.watchDeadline() {
		return d.promote(now, "backup: master-down watch expired, become master")
	}
	return Result{Reason: "backup: no timer expired"}
}

func (d *Device) dispatchMaster(ev Event) (Result, error) {
	switch ev.Kind {
	case EvReceiveAdvert:
		return d.masterAdvert(ev.Advert, ev.Now)
	case EvTakeAdvert:
		if ev.Now >= d.nextAdvert {
			d.nextAdvert = ev.Now + uint64(d.interval)
			return Result{Adverts: []Advert{d.selfAdvert()},
				Reason: "master: periodic advert due, next deadline from this take"}, nil
		}
		return Result{Reason: "master: periodic advert not due yet"}, nil
	case EvAdvanceTime:
		return Result{Reason: "master: time advance ignored, no backlog is emitted"}, nil
	}
	panic("vrrp: unreachable master event")
}

func (d *Device) masterAdvert(adv Advert, now uint64) (Result, error) {
	if d.priority == OwnerPriority {
		if adv.Priority == OwnerPriority {
			return Result{}, &Error{Kind: ErrAddressOwnerConflict,
				Detail: "received priority-255 advert while being address owner"}
		}
		return Result{Reason: "master: address owner ignores non-255 advert"}, nil
	}
	if adv.Priority == YieldPriority {
		d.nextAdvert = now + uint64(d.interval)
		return Result{Adverts: []Advert{d.selfAdvert()},
			Reason: "master: zero-priority probe, reply with advert"}, nil
	}
	if err := d.checkConflicts(adv); err != nil {
		return Result{}, err
	}
	if better(adv, d.id, d.priority) {
		d.enterBackup(now, adv.IntervalMs)
		return Result{Reason: "master: superior advert, demote to backup"}, nil
	}
	return Result{Reason: "master: inferior advert ignored"}, nil
}

// checkConflicts reports identifier and address owner conflicts for
// non-zero-priority adverts.
func (d *Device) checkConflicts(adv Advert) error {
	if adv.Priority == OwnerPriority && d.priority == OwnerPriority {
		return &Error{Kind: ErrAddressOwnerConflict,
			Detail: "received priority-255 advert while being address owner"}
	}
	if adv.Priority == d.priority && adv.SenderID == d.id {
		return &Error{Kind: ErrIdentifierConflict,
			Detail: "received advert with identical priority and identifier"}
	}
	return nil
}

// better reports whether the advert sender outranks the local device:
// higher priority, or equal priority with a larger identifier.
func better(adv Advert, id string, priority int) bool {
	return adv.Priority > priority ||
		(adv.Priority == priority && adv.SenderID > id)
}

func (d *Device) selfAdvert() Advert {
	return Advert{SenderID: d.id, Priority: d.priority, IntervalMs: d.interval}
}

func (d *Device) watchDeadline() uint64 {
	return d.watchBase + 3*uint64(d.watchInterval)
}

func (d *Device) acceptAdvert(adv Advert, now uint64) {
	d.watchBase = now
	d.watchInterval = adv.IntervalMs
	d.skewSet = false
}

func (d *Device) promote(now uint64, reason string) Result {
	d.role = RoleMaster
	d.skewSet = false
	d.nextAdvert = now + uint64(d.interval)
	return Result{Adverts: []Advert{d.selfAdvert()}, Reason: reason}
}

func (d *Device) enterBackup(now uint64, watchInterval int) {
	d.role = RoleBackup
	d.watchBase = now
	d.watchInterval = watchInterval
	d.skewSet = false
}

func (d *Device) enterInit() {
	d.role = RoleInit
	d.skewSet = false
}

// Snapshot returns the current observable state.
func (d *Device) Snapshot() Snapshot {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.snapshot()
}

func (d *Device) snapshot() Snapshot {
	return Snapshot{
		Role:          d.role,
		Clock:         d.clock,
		Priority:      d.priority,
		Preempt:       d.preempt,
		WatchBase:     d.watchBase,
		WatchInterval: d.watchInterval,
		SkewSet:       d.skewSet,
		SkewDeadline:  d.skewDeadline,
		NextAdvert:    d.nextAdvert,
	}
}
