// Package decay implements integer stepwise penalty decay.
//
// Every step applies p = floor(p*num/den) with integer arithmetic only.
// Decay is anchored to a base timestamp ("last") that advances solely by
// whole steps, so the phase never shifts to the query timestamp.
package decay

import (
	"errors"
	"fmt"
)

// Decayer holds decay parameters and the bounded step counter Z.
type Decayer struct {
	delta    int64
	num      int64
	den      int64
	pmax     int64
	z        int64
	mulSteps int64
}

// New validates parameters and computes Z once: the number of steps the
// maximum penalty needs to reach zero under stepwise floor decay.
func New(delta, num, den, pmax int64) (*Decayer, error) {
	if delta < 1 || delta > 1_000_000 {
		return nil, fmt.Errorf("decay: delta %d out of range [1,1e6]: %w", delta, ErrInvalidArgument)
	}
	if num < 1 || den <= num || den > 1000 {
		return nil, fmt.Errorf("decay: need 1<=num<den<=1000, got %d/%d: %w", num, den, ErrInvalidArgument)
	}
	if pmax < 1 || pmax > 1_000_000_000 {
		return nil, fmt.Errorf("decay: pmax %d out of range: %w", pmax, ErrInvalidArgument)
	}
	d := &Decayer{delta: delta, num: num, den: den, pmax: pmax}
	p := pmax
	for p > 0 {
		p = d.step(p)
		d.z++
	}
	return d, nil
}

// ErrInvalidArgument marks construction-time parameter errors.
var ErrInvalidArgument = errors.New("invalid argument")

// Delta returns the decay step length in milliseconds.
func (d *Decayer) Delta() int64 { return d.delta }

// Z returns the steps needed for Pmax to decay to zero.
func (d *Decayer) Z() int64 { return d.z }

// MulSteps returns the multiplication steps counted by the most recent call
// to Settle or StepsToBelow on this decayer.
func (d *Decayer) MulSteps() int64 { return d.mulSteps }

// step performs one floor-decay step and counts the multiplication.
func (d *Decayer) step(p int64) int64 {
	d.mulSteps++
	return p * d.num / d.den
}

// Settle decays p from base timestamp last toward t: k=floor((t-last)/delta)
// steps are applied and last advances by k*delta (never set to t). Steps
// beyond the point where p reached zero are skipped, so the multiplication
// count is at most Z+1 regardless of k. The caller guarantees t >= last.
func (d *Decayer) Settle(p, last, t int64) (np, nlast int64) {
	d.mulSteps = 0
	k := (t - last) / d.delta
	nlast = last + k*d.delta
	for s := int64(0); s < k && p > 0; s++ {
		p = d.step(p)
	}
	return p, nlast
}

// StepsToBelow returns the smallest positive j such that applying j stepwise
// floor-decay steps to p yields a value strictly below pr. Decay stops once
// zero is reached; the multiplication count is at most Z.
func (d *Decayer) StepsToBelow(p, pr int64) int64 {
	d.mulSteps = 0
	var j int64
	for {
		p = d.step(p)
		j++
		if p < pr {
			return j
		}
	}
}
