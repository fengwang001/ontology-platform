package microgrid

import "sort"

// schedule stores accepted, not-yet-executed plans keyed by slot.
type schedule struct {
	plans map[int]SlotPlan
}

func newSchedule() *schedule { return &schedule{plans: map[int]SlotPlan{}} }

func (s *schedule) get(slot int) (SlotPlan, bool) {
	p, ok := s.plans[slot]
	return p, ok
}

// acceptedSlots returns all scheduled slots in ascending order.
func (s *schedule) acceptedSlots() []int {
	out := make([]int, 0, len(s.plans))
	for k := range s.plans {
		out = append(out, k)
	}
	sort.Ints(out)
	return out
}

// set writes one slot plan.
func (s *schedule) set(slot int, p SlotPlan) { s.plans[slot] = p }

// delete removes one slot plan.
func (s *schedule) delete(slot int) { delete(s.plans, slot) }

// removeBefore drops executed/past slots; cost is bounded by the number of
// removed slots, never by total executed history (past slots were already
// removed when their slot closed).
func (s *schedule) removeBefore(slot int) {
	for k := range s.plans {
		if k < slot {
			delete(s.plans, k)
		}
	}
}

// snapshot returns an independent copy of the plans.
func (s *schedule) snapshot() map[int]SlotPlan {
	cp := make(map[int]SlotPlan, len(s.plans))
	for k, v := range s.plans {
		cp[k] = v
	}
	return cp
}

// replaceInto builds a copy of current plans with [start, start+len(in))
// overwritten by in (including in-slot idle entries, which remove nothing
// but are valid scheduled actions). The returned map is never committed
// unless full revalidation succeeds.
func (s *schedule) replaceInto(start int, in []SlotPlan) map[int]SlotPlan {
	cp := s.snapshot()
	for i, p := range in {
		cp[start+i] = p
	}
	return cp
}

// commit replaces the backing map wholesale.
func (s *schedule) commit(plans map[int]SlotPlan) { s.plans = plans }

// dropSuffix removes the given slot and every later scheduled slot,
// returning the removed slots in ascending order.
func (s *schedule) dropSuffix(from int) []int {
	var out []int
	for k := range s.plans {
		if k >= from {
			out = append(out, k)
			delete(s.plans, k)
		}
	}
	sort.Ints(out)
	return out
}

// validatePlans walks every slot in plans in ascending order starting from
// soc and returns the first failing slot/reason under the fixed precedence
// 参数非法 > 维护锁定 > 模式不允许 > 越界 > 备用不足 > 预测缺失.
func validatePlans(p Params, bat *battery, fc *forecast, plans map[int]SlotPlan, soc int, mode Mode, locked bool) *RejectError {
	slots := make([]int, 0, len(plans))
	for k := range plans {
		slots = append(slots, k)
	}
	sort.Ints(slots)
	for _, t := range slots {
		pl := plans[t]
		load, hasLoad := fc.loadAt(t)

		// Malformed action: fixed precedence puts 参数非法 first.
		switch pl.Action {
		case Idle:
			if pl.Amount != 0 {
				return reject(ReasonInvalid, t, "idle amount must be zero")
			}
		case Charge, Discharge:
			if pl.Amount <= 0 {
				return reject(ReasonInvalid, t, "action amount must be positive")
			}
		default:
			return reject(ReasonInvalid, t, "unknown action")
		}

		// Maintenance lock precedes mode checks. Island discharge up to
		// the critical load is the sole exception.
		if locked && pl.Action == Discharge {
			allowed := mode == Island && hasLoad && pl.Amount <= load
			if !allowed {
				return reject(ReasonMaintenance, t, "discharge forbidden in maintenance lock")
			}
		}

		// Mode-specific caps precede physical bounds.
		if mode == Island {
			switch pl.Action {
			case Charge:
				if pl.Amount > fc.surplusAt(t) {
					return reject(ReasonMode, t, "charge exceeds local surplus")
				}
			case Discharge:
				if !hasLoad {
					return reject(ReasonMode, t, "island discharge without load forecast")
				}
				if pl.Amount > load {
					return reject(ReasonMode, t, "discharge exceeds critical load")
				}
			}
		}

		// Physical power caps and SoC bounds.
		switch pl.Action {
		case Charge:
			if pl.Amount > p.MaxCharge {
				return reject(ReasonBounds, t, "charge exceeds max charge")
			}
		case Discharge:
			if pl.Amount > p.MaxDischarge {
				return reject(ReasonBounds, t, "discharge exceeds max discharge")
			}
		}
		next := bat.nextSoC(soc, pl.Action, pl.Amount)
		if next < p.SoCLower || next > p.SoCUpper {
			return reject(ReasonBounds, t, "soc leaves bounds")
		}

		// Reserve: end-of-slot dischargeable energy must cover the next
		// ReserveSlots critical loads. Incomplete window is recorded as
		// 预测缺失, but only if reserve itself does not already fail.
		need, complete := fc.windowSum(t, p.ReserveSlots)
		if next-p.SoCLower < need {
			return reject(ReasonReserve, t, "reserve insufficient")
		}
		if !complete {
			return reject(ReasonNoForecast, t, "reserve window lacks forecast")
		}
		if !hasLoad {
			return reject(ReasonNoForecast, t, "planned slot lacks forecast")
		}

		soc = next
	}
	return nil
}
