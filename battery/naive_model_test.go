package battery_test

// naiveModel is an independently written reference implementation. It keeps the
// entire event history and recomputes every decision from scratch for every
// snapshot: the streaming implementation must agree with it on all random
// sequences. It deliberately uses different code structure and helpers so that
// mistakes shared by both are unlikely.

import (
	"errors"

	"ontology/battery"
)

type naiveModel struct {
	cfg battery.Config

	accepted []battery.Sample
	rejected []struct {
		s   battery.Sample
		err error
	}

	latched          map[battery.FaultCause]bool
	latchOrder       []battery.FaultCause
	sampleSinceLatch bool
	resetAttempts    []bool // permitted flag
	resetResult      []error

	// ocEpoch is the accepted-sample index at which overcurrent/delta runs
	// were last invalidated by a successful reset (exclusive lower bound).
	ocEpoch  int
	hasEpoch bool
}

func newNaive(cfg battery.Config) *naiveModel {
	return &naiveModel{cfg: cfg, latched: map[battery.FaultCause]bool{}}
}

func naiveLookup(tiers []battery.LimitTier, v int64) int32 {
	for _, t := range tiers {
		if v >= t.Lower && v < t.Upper {
			return t.LimitPermille
		}
	}
	panic("naive: value outside table domain")
}

func (n *naiveModel) rawLimits(idx int) (chg, dis int64, sensor bool) {
	s := n.accepted[idx]
	c := n.cfg
	hi, lo := s.CellVoltagesMV[0], s.CellVoltagesMV[0]
	for _, v := range s.CellVoltagesMV[1:] {
		if v > hi {
			hi = v
		}
		if v < lo {
			lo = v
		}
	}
	tMin, tMax, nt := int64(0), int64(0), 0
	for _, t := range s.Temperatures {
		if t == battery.InvalidTemperature || t < c.MinTemperature || t > c.MaxTemperature {
			continue
		}
		if nt == 0 {
			tMin, tMax = t, t
		} else {
			if t < tMin {
				tMin = t
			}
			if t > tMax {
				tMax = t
			}
		}
		nt++
	}
	chg = c.RatedChargeMA
	dis = c.RatedDischargeMA
	minPerm := func(a, b int64) int64 {
		if a < b {
			return a
		}
		return b
	}
	chg = minPerm(chg, c.RatedChargeMA*int64(naiveLookup(c.ChargeVoltageTable, hi))/1000)
	dis = minPerm(dis, c.RatedDischargeMA*int64(naiveLookup(c.DischargeVoltageTable, lo))/1000)
	if nt == 0 {
		return 0, 0, true
	}
	chg = minPerm(chg, minPerm(
		c.RatedChargeMA*int64(naiveLookup(c.ChargeTempTable, tMin))/1000,
		c.RatedChargeMA*int64(naiveLookup(c.ChargeTempTable, tMax))/1000))
	dis = minPerm(dis, minPerm(
		c.RatedDischargeMA*int64(naiveLookup(c.DischargeTempTable, tMin))/1000,
		c.RatedDischargeMA*int64(naiveLookup(c.DischargeTempTable, tMax))/1000))
	if nt < c.TempCount {
		chg /= 2
		dis /= 2
	}
	return
}

// runStart finds the earliest index of the uninterrupted run of cond ending
// at idx, and whether the run currently holds at idx.
func (n *naiveModel) runStart(idx int, cond func(battery.Sample) bool) (int, bool) {
	if !cond(n.accepted[idx]) {
		return 0, false
	}
	start := idx
	for start > 0 && cond(n.accepted[start-1]) {
		start--
	}
	return start, true
}

func (n *naiveModel) minMax(idx int) (lo, hi int64) {
	s := n.accepted[idx]
	lo, hi = s.CellVoltagesMV[0], s.CellVoltagesMV[0]
	for _, v := range s.CellVoltagesMV[1:] {
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	return
}

func (n *naiveModel) bans(idx int) (banC, banD bool) {
	c := n.cfg
	ovCond := func(s battery.Sample) bool {
		for _, v := range s.CellVoltagesMV {
			if v >= c.OverVoltageMV {
				return true
			}
		}
		return false
	}
	ovReleaseCond := func(s battery.Sample) bool {
		for _, v := range s.CellVoltagesMV {
			if v > c.OverVoltageMV-c.RecoveryHystMV {
				return false
			}
		}
		return true
	}
	uvCond := func(s battery.Sample) bool {
		for _, v := range s.CellVoltagesMV {
			if v <= c.UnderVoltageMV {
				return true
			}
		}
		return false
	}
	uvReleaseCond := func(s battery.Sample) bool {
		for _, v := range s.CellVoltagesMV {
			if v < c.UnderVoltageMV+c.RecoveryHystMV {
				return false
			}
		}
		return true
	}
	// Replay the two state machines over all accepted samples.
	for k := 0; k <= idx; k++ {
		if !banC {
			if st, ok := n.runStartAt(k, ovCond); ok && n.accepted[k].TimeMS-n.accepted[st].TimeMS >= c.ConfirmDurationMS {
				banC = true
			}
		} else if st, ok := n.runStartAt(k, ovReleaseCond); ok && n.accepted[k].TimeMS-n.accepted[st].TimeMS >= c.ConfirmDurationMS {
			banC = false
		}
		if !banD {
			if st, ok := n.runStartAt(k, uvCond); ok && n.accepted[k].TimeMS-n.accepted[st].TimeMS >= c.ConfirmDurationMS {
				banD = true
			}
		} else if st, ok := n.runStartAt(k, uvReleaseCond); ok && n.accepted[k].TimeMS-n.accepted[st].TimeMS >= c.ConfirmDurationMS {
			banD = false
		}
	}
	return
}

func (n *naiveModel) runStartAt(idx int, cond func(battery.Sample) bool) (int, bool) {
	if !cond(n.accepted[idx]) {
		return 0, false
	}
	start := idx
	for start > 0 && cond(n.accepted[start-1]) {
		start--
	}
	return start, true
}

func (n *naiveModel) deltaLatchedAt(idx int) bool {
	c := n.cfg
	cond := func(s battery.Sample) bool {
		lo, hi := s.CellVoltagesMV[0], s.CellVoltagesMV[0]
		for _, v := range s.CellVoltagesMV[1:] {
			if v < lo {
				lo = v
			}
			if v > hi {
				hi = v
			}
		}
		return hi-lo > c.VoltageDeltaLimitMV && s.CurrentMA > c.RestCurrentThresholdMA
	}
	from := 0
	if n.hasEpoch {
		from = n.ocEpoch
	}
	st, ok := n.runStartFrom(idx, from, cond)
	if !ok {
		return false
	}
	return n.accepted[idx].TimeMS-n.accepted[st].TimeMS >= c.ConfirmDurationMS
}

func (n *naiveModel) runStartFrom(idx, from int, cond func(battery.Sample) bool) (int, bool) {
	if !cond(n.accepted[idx]) {
		return 0, false
	}
	start := idx
	for start > from && cond(n.accepted[start-1]) {
		start--
	}
	return start, true
}

// overcurrentLatchAt recomputes the overcurrent latching condition up to idx.
// The run is invalidated whenever any sample is within its direction limit.
func (n *naiveModel) overcurrentLatchAt(idx int) bool {
	c := n.cfg
	start := -1
	from := 0
	if n.hasEpoch {
		from = n.ocEpoch
	}
	for k := from; k <= idx; k++ {
		chg, dis, sensor := n.rawLimits(k)
		banC, banD := n.bans(k)
		if banC || sensor {
			chg = 0
		}
		if banD || sensor {
			dis = 0
		}
		s := n.accepted[k]
		var lim, mag int64
		if s.CurrentMA > 0 {
			lim, mag = chg, s.CurrentMA
		} else if s.CurrentMA < 0 {
			lim, mag = dis, -s.CurrentMA
		}
		exceeding := mag > lim && mag-lim > 0
		if !exceeding {
			start = -1
			continue
		}
		if start == -1 {
			start = k
		}
		excess := mag - lim
		tier := c.CurrentTiers[0]
		for _, tt := range c.CurrentTiers {
			if excess > tt.Excess {
				tier = tt
			}
		}
		if s.TimeMS-n.accepted[start].TimeMS >= tier.ToleranceMS {
			return true
		}
	}
	return false
}

func (n *naiveModel) validate(s battery.Sample) error {
	c := n.cfg
	if len(s.CellVoltagesMV) != c.CellCount || len(s.Temperatures) != c.TempCount {
		return battery.ErrInvalidSample
	}
	for _, v := range s.CellVoltagesMV {
		if v < c.MinCellVoltageMV || v > c.MaxCellVoltageMV {
			return battery.ErrInvalidSample
		}
	}
	if s.CurrentMA < c.MinCurrentMA || s.CurrentMA > c.MaxCurrentMA {
		return battery.ErrInvalidSample
	}
	if s.TimeMS < 0 || (len(n.accepted) > 0 && s.TimeMS <= n.accepted[len(n.accepted)-1].TimeMS) {
		return battery.ErrTimeNotAdvancing
	}
	return nil
}

func (n *naiveModel) submit(s battery.Sample) (battery.Snapshot, error) {
	if err := n.validate(s); err != nil {
		n.rejected = append(n.rejected, struct {
			s   battery.Sample
			err error
		}{s, err})
		return battery.Snapshot{}, err
	}
	wasLatched := len(n.latchOrder) > 0
	n.accepted = append(n.accepted, s)
	idx := len(n.accepted) - 1

	if n.overcurrentLatchAt(idx) {
		n.addCause(battery.FaultOvercurrent)
	}
	if n.deltaLatchedAt(idx) {
		n.addCause(battery.FaultVoltageDelta)
	}
	if wasLatched {
		n.sampleSinceLatch = true
	}
	return n.snapshot(idx), nil
}

func (n *naiveModel) addCause(c battery.FaultCause) {
	if !n.latched[c] {
		n.latched[c] = true
		n.latchOrder = append(n.latchOrder, c)
	}
}

func (n *naiveModel) reset(permitted bool) (battery.Snapshot, error) {
	n.resetAttempts = append(n.resetAttempts, permitted)
	var err error
	defer func() { n.resetResult = append(n.resetResult, err) }()

	if !permitted {
		err = battery.ErrNoPermission
		return battery.Snapshot{}, err
	}
	if len(n.latchOrder) == 0 {
		err = battery.ErrNotLatched
		return battery.Snapshot{}, err
	}
	if !n.sampleSinceLatch {
		err = battery.ErrNoSampleSinceLatch
		return battery.Snapshot{}, err
	}
	idx := len(n.accepted) - 1
	s := n.accepted[idx]
	c := n.cfg
	lo, hi := n.minMax(idx)
	validTemp := false
	for _, t := range s.Temperatures {
		if t != battery.InvalidTemperature && t >= c.MinTemperature && t <= c.MaxTemperature {
			validTemp = true
		}
	}
	cur := s.CurrentMA
	if cur < 0 {
		cur = -cur
	}
	if hi >= c.OverVoltageMV || lo <= c.UnderVoltageMV ||
		hi-lo > c.VoltageDeltaLimitMV || !validTemp || cur > c.RestCurrentThresholdMA {
		err = battery.ErrRecoveryNotSatisfied
		return battery.Snapshot{}, err
	}
	n.latched = map[battery.FaultCause]bool{}
	n.latchOrder = nil
	n.sampleSinceLatch = false
	n.ocEpoch = len(n.accepted)
	n.hasEpoch = true
	err = nil
	return n.snapshot(idx), nil
}

func (n *naiveModel) snapshot(idx int) battery.Snapshot {
	c := n.cfg
	s := n.accepted[idx]
	chg, dis, sensor := n.rawLimits(idx)
	banC, banD := n.bans(idx)
	lo, hi := n.minMax(idx)
	balance := hi-lo > c.VoltageDeltaLimitMV
	if banC || sensor {
		chg = 0
	}
	if banD || sensor {
		dis = 0
	}
	latched := len(n.latchOrder) > 0
	if latched {
		chg, dis = 0, 0
	}
	causes := append([]battery.FaultCause(nil), n.latchOrder...)
	return battery.Snapshot{
		TimeMS:              s.TimeMS,
		AcceptedSamples:     int64(len(n.accepted)),
		AllowedChargeMA:     chg,
		AllowedDischargeMA:  dis,
		BanCharge:           banC,
		BanDischarge:        banD,
		BalanceRequest:      balance,
		SensorFault:         sensor,
		Latched:             latched,
		LatchCauses:         causes,
		HasSampleSinceLatch: n.sampleSinceLatch,
	}
}

var _ = errors.Is
