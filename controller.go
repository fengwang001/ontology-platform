package microgrid

import "sync"

// Controller is the concurrently callable dispatch controller. Every public
// method takes one mutex, so concurrent calls are equivalent to some serial
// order and all invariants are observed atomically.
type Controller struct {
	mu      sync.Mutex
	bat     *battery
	fc      *forecast
	sch     *schedule
	current int
	mode    Mode
	locked  bool
}

// New validates parameters and constructs a controller.
func New(p Params, initialSOC int) (*Controller, error) {
	if err := validateParams(p); err != nil {
		return nil, err
	}
	if initialSOC < p.SoCLower || initialSOC > p.SoCUpper {
		return nil, ErrInvalid
	}
	return &Controller{
		bat:     newBattery(p, initialSOC),
		fc:      newForecast(),
		sch:     newSchedule(),
		current: 0,
		mode:    GridTied,
	}, nil
}

func validateParams(p Params) error {
	switch {
	case p.Capacity <= 0:
		return ErrInvalid
	case p.SoCLower < 0 || p.SoCUpper < 0 || p.SoCLower > p.SoCUpper || p.SoCUpper > p.Capacity:
		return ErrInvalid
	case p.MaxCharge < 0 || p.MaxDischarge < 0:
		return ErrInvalid
	case p.LossDenominator <= 0 || p.LossNumerator < 0 || p.LossNumerator >= p.LossDenominator:
		return ErrInvalid
	case p.MaintenanceThreshold < 0 || p.ReserveSlots < 0 || p.Tolerance < 0:
		return ErrInvalid
	}
	return nil
}

func validateSlotPlan(p SlotPlan) bool {
	switch p.Action {
	case Idle:
		return p.Amount == 0
	case Charge, Discharge:
		return p.Amount > 0
	default:
		return false
	}
}

func signedPower(a Action, amount int) int {
	switch a {
	case Charge:
		return amount
	case Discharge:
		return -amount
	default:
		return 0
	}
}

func absInt(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

// revalidate walks all unexecuted plans from the current SoC and drops the
// suffix starting at the first failing slot. Cost is proportional only to
// the number of still-accepted slots, never to executed history.
func (c *Controller) revalidate(cause string) *Outcome {
	out := &Outcome{Locked: c.locked}
	err := validatePlans(c.bat.params, c.bat, c.fc, c.sch.plans, c.bat.soc, c.mode, c.locked)
	if err == nil {
		return out
	}
	dropped := c.sch.dropSuffix(err.Slot)
	out.appendRevocation(err.Slot, cause, dropped)
	return out
}

// SubmitPlan accepts or rejects a consecutive future plan segment. Overlap
// with existing plans is merged; the merged whole must revalidate, otherwise
// nothing changes.
func (c *Controller) SubmitPlan(start int, plans []SlotPlan) (*Outcome, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if start < 0 {
		return nil, ErrInvalid
	}
	if len(plans) == 0 {
		return nil, ErrInvalid
	}
	for i, p := range plans {
		if !validateSlotPlan(p) {
			return nil, reject(ReasonInvalid, start+i, "malformed slot plan")
		}
	}
	if start < c.current {
		return nil, reject(ReasonSlot, start, "plan starts in the past")
	}

	merged := c.sch.replaceInto(start, plans)
	if err := validatePlans(c.bat.params, c.bat, c.fc, merged, c.bat.soc, c.mode, c.locked); err != nil {
		return nil, err
	}
	c.sch.commit(merged)
	return &Outcome{Locked: c.locked}, nil
}

// UpdateLoad updates one slot's forecast; forecast updates never fail and
// always trigger suffix revalidation.
func (c *Controller) UpdateLoad(slot, load int) (*Outcome, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if slot < 0 || load < 0 {
		return nil, ErrInvalid
	}
	c.fc.setLoad(slot, load)
	return c.revalidate("forecast"), nil
}

// UpdateLoads updates a consecutive range of forecasts.
func (c *Controller) UpdateLoads(start int, loads []int) (*Outcome, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if start < 0 || len(loads) == 0 {
		return nil, ErrInvalid
	}
	for _, v := range loads {
		if v < 0 {
			return nil, ErrInvalid
		}
	}
	c.fc.setLoads(start, loads)
	return c.revalidate("forecast"), nil
}

// RegisterSurplus records local generation surplus for a future slot;
// surplus does not itself revoke plans.
func (c *Controller) RegisterSurplus(slot, surplus int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if slot < 0 || surplus < 0 {
		return ErrInvalid
	}
	if slot < c.current {
		return reject(ReasonSlot, slot, "surplus for past slot")
	}
	c.fc.setSurplus(slot, surplus)
	return nil
}

// RegisterActual closes the current slot with the measured action. Rejected
// registrations leave every state field untouched and do not advance time.
func (c *Controller) RegisterActual(slot int, a Action, amount int) (*Outcome, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !validateSlotPlan(SlotPlan{Action: a, Amount: amount}) {
		return nil, reject(ReasonInvalid, slot, "malformed actual action")
	}
	if slot != c.current {
		return nil, reject(ReasonSlot, slot, "actual slot is not current")
	}

	load, hasLoad := c.fc.loadAt(slot)
	// Maintenance-lock exception: island discharge up to critical load.
	if c.locked && a == Discharge {
		if !(c.mode == Island && hasLoad && amount <= load) {
			return nil, reject(ReasonMaintenance, slot, "discharge forbidden in maintenance lock")
		}
	}
	if c.mode == Island {
		switch a {
		case Charge:
			if amount > c.fc.surplusAt(slot) {
				return nil, reject(ReasonMode, slot, "charge exceeds local surplus")
			}
		case Discharge:
			if !hasLoad {
				return nil, reject(ReasonMode, slot, "island discharge without load forecast")
			}
			if amount > load {
				return nil, reject(ReasonMode, slot, "discharge exceeds critical load")
			}
		}
	}

	next := c.bat.nextSoC(c.bat.soc, a, amount)
	if next < c.bat.params.SoCLower || next > c.bat.params.SoCUpper {
		return nil, reject(ReasonBounds, slot, "soc leaves bounds")
	}

	planned, hadPlan := c.sch.get(slot)
	deviation := false
	if hadPlan {
		deviation = absInt(signedPower(a, amount)-signedPower(planned.Action, planned.Amount)) > c.bat.params.Tolerance
	} else {
		deviation = absInt(signedPower(a, amount)) > c.bat.params.Tolerance
	}

	// Commit only after every rejection path has been checked.
	c.bat.soc = next
	if a == Discharge && c.bat.addThroughput(amount) {
		c.locked = true
	}
	c.current++
	c.sch.removeBefore(c.current)

	out := &Outcome{Deviation: deviation, Locked: c.locked}
	if len(c.sch.plans) > 0 {
		cause := "actual"
		switch {
		case deviation:
			cause = "deviation"
		case c.locked:
			cause = "maintenance"
		}
		out.Revoked = c.revalidate(cause).Revoked
	}
	return out, nil
}

// SwitchMode changes mode at a slot boundary. Entering island revalidates
// against island rules; returning to grid revokes nothing.
func (c *Controller) SwitchMode(m Mode) (*Outcome, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if m != GridTied && m != Island {
		return nil, ErrInvalid
	}
	if m == c.mode {
		return &Outcome{Locked: c.locked}, nil
	}
	c.mode = m
	if m == Island {
		return c.revalidate("mode"), nil
	}
	return &Outcome{Locked: c.locked}, nil
}

// CompleteMaintenance clears throughput and releases the lock.
func (c *Controller) CompleteMaintenance() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.bat.throughput = 0
	c.locked = false
}

// CurrentSlot reports the current (next not-executed) slot number.
func (c *Controller) CurrentSlot() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.current
}

// SoC reports the current state of charge.
func (c *Controller) SoC() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bat.soc
}

// Mode reports the operating mode.
func (c *Controller) Mode() Mode {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.mode
}

// Locked reports maintenance-lock state.
func (c *Controller) Locked() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.locked
}

// Throughput reports accumulated discharge throughput.
func (c *Controller) Throughput() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bat.throughput
}

// PlanAt reports the accepted plan for a slot, if any.
func (c *Controller) PlanAt(slot int) (SlotPlan, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sch.get(slot)
}
