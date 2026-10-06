package delivery

import "sync"

// exception is the mutable state of one reported exception.
// Contact history size is never scanned for the undeliverable verdict:
// only cnt (total recorded contacts) and lastContactT (last timestamp)
// are read, making the verdict O(1) in the order's contact history.
type exception struct {
	id           int
	typ          ExceptionType
	phase        ExceptionPhase
	start        int
	cnt          int
	lastContactT int
	windowEnd    int
	evidenceID   string
	evidenceTime int
}

// orderState is the full mutable state of one order.
type orderState struct {
	id          string
	status      OrderStatus
	address     Address
	disposition Disposition
	liability   Liability
	addrChanges int
	compPaid    bool
	seq         int
	current     *exception
	windowEnd   int
}

func (o *orderState) snapshot() *Order {
	s := &Order{
		ID:           o.id,
		Status:       o.status,
		Address:      o.address,
		Disposition:  o.disposition,
		Liability:    o.liability,
		AddrChanges:  o.addrChanges,
		CompPaid:     o.compPaid,
		ExceptionSeq: o.seq,
		WindowEnd:    o.windowEnd,
	}
	if o.current != nil {
		s.Current = exceptionSnapshot(o.current)
	}
	return s
}

func exceptionSnapshot(e *exception) *Exception {
	return &Exception{
		ID:           e.id,
		Type:         e.typ,
		Phase:        e.phase,
		Start:        e.start,
		ContactCount: e.cnt,
		WindowEnd:    e.windowEnd,
		EvidenceID:   e.evidenceID,
		EvidenceTime: e.evidenceTime,
	}
}

// Platform is the concurrent-safe delivery exception system.
type Platform struct {
	cfg Config
	clk clock
	mu  sync.Mutex
	// orders is keyed by order id; window maturity is evaluated lazily
	// from the current order's own windowEnd, so expiry checks never
	// scan the platform-wide exception set.
	orders map[string]*orderState
}

// NewPlatform validates config and constructs an empty platform.
func NewPlatform(cfg Config) (*Platform, error) {
	if cfg.MinWaitSeconds < 0 || cfg.MinContacts < 0 || cfg.MinContactInterval < 0 ||
		cfg.CorrectionWindow < 0 || cfg.MaxCorrectionDist < 0 ||
		cfg.EvidenceTTLSeconds < 0 || cfg.RiderCompensation < 0 ||
		cfg.MerchantConfirmWindow < 0 {
		return nil, newError(ErrInvalidParam, "config values must be non-negative")
	}
	return &Platform{cfg: cfg, orders: map[string]*orderState{}}, nil
}
