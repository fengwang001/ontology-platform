package vrrp

// This file holds an independent, deliberately naive reference model of
// the same specification, used only by the differential tests. Unlike
// Device it keeps the full history of accepted events and recomputes its
// state from scratch on every call, and it implements time advance by
// stepping through the crossed interval millisecond by millisecond. Its
// cost grows with history length and time spans, which is exactly what
// the real implementation must avoid.

type naiveState struct {
	role          Role
	clock         uint64
	priority      int
	preempt       bool
	watchBase     uint64
	watchInterval int
	skewSet       bool
	skewDeadline  uint64
	nextAdvert    uint64
}

type naiveModel struct {
	cfg      Config
	accepted []Event
}

func newNaiveModel(cfg Config) *naiveModel {
	return &naiveModel{cfg: cfg}
}

func (m *naiveModel) handle(ev Event) (Result, error) {
	st := m.replay()
	if err := naiveValidate(m.cfg, st, ev); err != nil {
		return Result{}, err
	}
	if ev.Now < st.clock {
		return Result{}, &Error{Kind: ErrClockRollback, Detail: "naive: clock rollback"}
	}
	res, err := naiveStep(m.cfg, &st, ev)
	if err != nil {
		return Result{}, err
	}
	m.accepted = append(m.accepted, ev)
	return res, nil
}

func (m *naiveModel) snapshot() Snapshot {
	st := m.replay()
	return Snapshot{
		Role:          st.role,
		Clock:         st.clock,
		Priority:      st.priority,
		Preempt:       st.preempt,
		WatchBase:     st.watchBase,
		WatchInterval: st.watchInterval,
		SkewSet:       st.skewSet,
		SkewDeadline:  st.skewDeadline,
		NextAdvert:    st.nextAdvert,
	}
}

// replay recomputes the state by folding the whole accepted history.
func (m *naiveModel) replay() naiveState {
	st := naiveState{role: RoleInit, priority: m.cfg.Priority, preempt: m.cfg.Preempt}
	for _, ev := range m.accepted {
		if _, err := naiveStep(m.cfg, &st, ev); err != nil {
			panic("naive: accepted event rejected on replay")
		}
		st.clock = ev.Now
	}
	return st
}

func naiveValidate(cfg Config, st naiveState, ev Event) error {
	switch ev.Kind {
	case EvReceiveAdvert:
		adv := ev.Advert
		if adv.SenderID == "" || adv.Priority < 0 || adv.Priority > OwnerPriority ||
			adv.IntervalMs < 1 || adv.IntervalMs > MaxAdvertIntervalMs {
			return &Error{Kind: ErrInvalidArgument, Detail: "naive: bad advert"}
		}
	case EvSetPriority:
		if ev.Priority < 1 || ev.Priority > OwnerPriority ||
			ev.Priority == OwnerPriority || st.priority == OwnerPriority {
			return &Error{Kind: ErrInvalidArgument, Detail: "naive: bad priority change"}
		}
	case EvStart, EvStop, EvAdvanceTime, EvTakeAdvert, EvSetPreempt:
	default:
		return &Error{Kind: ErrInvalidArgument, Detail: "naive: unknown event"}
	}
	return nil
}

func naiveSuperior(cfg Config, st naiveState, adv Advert) bool {
	return adv.Priority > st.priority ||
		(adv.Priority == st.priority && adv.SenderID > cfg.ID)
}

func naiveSelfAdvert(cfg Config, st naiveState) Advert {
	return Advert{SenderID: cfg.ID, Priority: st.priority, IntervalMs: cfg.AdvertIntervalMs}
}

// naiveStep applies one accepted event to st, returning the externally
// visible result. Time advance is simulated by ticking through every
// millisecond of the crossed span.
func naiveStep(cfg Config, st *naiveState, ev Event) (Result, error) {
	switch ev.Kind {
	case EvSetPriority:
		st.priority = ev.Priority
		return Result{Reason: "naive: priority set"}, nil
	case EvSetPreempt:
		st.preempt = ev.Preempt
		return Result{Reason: "naive: preempt set"}, nil
	case EvStart:
		if st.role != RoleInit {
			return Result{Reason: "naive: already started"}, nil
		}
		if st.priority == OwnerPriority {
			st.role = RoleMaster
			st.nextAdvert = ev.Now + uint64(cfg.AdvertIntervalMs)
			return Result{Adverts: []Advert{naiveSelfAdvert(cfg, *st)},
				Reason: "naive: owner starts as master"}, nil
		}
		st.role = RoleBackup
		st.watchBase = ev.Now
		st.watchInterval = cfg.AdvertIntervalMs
		st.skewSet = false
		return Result{Reason: "naive: started as backup"}, nil
	case EvStop:
		if st.role == RoleMaster {
			adv := Advert{SenderID: cfg.ID, Priority: YieldPriority, IntervalMs: cfg.AdvertIntervalMs}
			st.role = RoleInit
			st.skewSet = false
			return Result{Adverts: []Advert{adv}, Reason: "naive: master yields"}, nil
		}
		st.role = RoleInit
		st.skewSet = false
		return Result{Reason: "naive: stopped"}, nil
	}

	switch st.role {
	case RoleInit:
		if ev.Kind == EvReceiveAdvert || ev.Kind == EvTakeAdvert {
			return Result{}, &Error{Kind: ErrNotStarted, Detail: "naive: not started"}
		}
		return Result{Reason: "naive: init no-op"}, nil
	case RoleBackup:
		return naiveBackupStep(cfg, st, ev)
	case RoleMaster:
		return naiveMasterStep(cfg, st, ev)
	}
	panic("naive: unreachable")
}

func naiveConflict(cfg Config, st naiveState, adv Advert) error {
	if adv.Priority == OwnerPriority && st.priority == OwnerPriority {
		return &Error{Kind: ErrAddressOwnerConflict, Detail: "naive: owner conflict"}
	}
	if adv.Priority == st.priority && adv.SenderID == cfg.ID {
		return &Error{Kind: ErrIdentifierConflict, Detail: "naive: identifier conflict"}
	}
	return nil
}

func naiveBackupStep(cfg Config, st *naiveState, ev Event) (Result, error) {
	switch ev.Kind {
	case EvReceiveAdvert:
		adv := ev.Advert
		if adv.Priority == YieldPriority {
			st.skewSet = true
			st.skewDeadline = ev.Now + uint64(adv.IntervalMs)/4
			return Result{Reason: "naive: skew armed"}, nil
		}
		if err := naiveConflict(cfg, *st, adv); err != nil {
			return Result{}, err
		}
		if naiveSuperior(cfg, *st, adv) || !st.preempt {
			st.watchBase = ev.Now
			st.watchInterval = adv.IntervalMs
			st.skewSet = false
			return Result{Reason: "naive: advert accepted"}, nil
		}
		return Result{Reason: "naive: inferior advert ignored"}, nil
	case EvAdvanceTime:
		// Tick every millisecond; the first tick that reaches a timer
		// decides, promotion wait checked first within each tick. The
		// promotion itself takes effect at the event time.
		fired := false
		for tick := st.clock + 1; tick <= ev.Now && !fired; tick++ {
			if st.skewSet && tick >= st.skewDeadline {
				fired = true
			} else if tick >= st.watchBase+3*uint64(st.watchInterval) {
				fired = true
			}
		}
		if fired {
			st.role = RoleMaster
			st.skewSet = false
			st.nextAdvert = ev.Now + uint64(cfg.AdvertIntervalMs)
			return Result{Adverts: []Advert{naiveSelfAdvert(cfg, *st)},
				Reason: "naive: promoted"}, nil
		}
		return Result{Reason: "naive: nothing expired"}, nil
	case EvTakeAdvert:
		return Result{Reason: "naive: backup has no advert"}, nil
	}
	panic("naive: unreachable backup event")
}

func naiveMasterStep(cfg Config, st *naiveState, ev Event) (Result, error) {
	switch ev.Kind {
	case EvReceiveAdvert:
		adv := ev.Advert
		if st.priority == OwnerPriority {
			if adv.Priority == OwnerPriority {
				return Result{}, &Error{Kind: ErrAddressOwnerConflict, Detail: "naive: owner conflict"}
			}
			return Result{Reason: "naive: owner ignores"}, nil
		}
		if adv.Priority == YieldPriority {
			st.nextAdvert = ev.Now + uint64(cfg.AdvertIntervalMs)
			return Result{Adverts: []Advert{naiveSelfAdvert(cfg, *st)},
				Reason: "naive: probe reply"}, nil
		}
		if err := naiveConflict(cfg, *st, adv); err != nil {
			return Result{}, err
		}
		if naiveSuperior(cfg, *st, adv) {
			st.role = RoleBackup
			st.watchBase = ev.Now
			st.watchInterval = adv.IntervalMs
			st.skewSet = false
			return Result{Reason: "naive: demoted"}, nil
		}
		return Result{Reason: "naive: ignored"}, nil
	case EvTakeAdvert:
		if ev.Now >= st.nextAdvert {
			st.nextAdvert = ev.Now + uint64(cfg.AdvertIntervalMs)
			return Result{Adverts: []Advert{naiveSelfAdvert(cfg, *st)},
				Reason: "naive: periodic advert"}, nil
		}
		return Result{Reason: "naive: not due"}, nil
	case EvAdvanceTime:
		return Result{Reason: "naive: master no-op"}, nil
	}
	panic("naive: unreachable master event")
}
