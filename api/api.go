package api

import (
	"errors"
	"sort"
	"sync"

	"ontology/qnt"
)

// Four pairwise distinct, errors.Is-decidable sentinels.
var (
	ErrBadConfig = errors.New("api: maxValues must be positive")
	ErrTooMany   = errors.New("api: multiset capacity exceeded")
	ErrAbsent    = qnt.ErrAbsent
	ErrEmpty     = qnt.ErrEmpty
)

type API struct {
	mu  sync.RWMutex
	eng *qnt.Engine
	max int
}

var selfCheckOps = []struct {
	ins bool
	v   int64
}{
	{true, 10}, {true, 30}, {true, 20}, {true, 40}, {true, 10},
	{false, 30}, {true, 50}, {false, 10},
}

func New(maxValues int) (*API, error) {
	if maxValues <= 0 {
		return nil, ErrBadConfig
	}
	return &API{eng: qnt.New(), max: maxValues}, nil
}

func (a *API) Insert(v int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.eng.Count() >= a.max {
		return ErrTooMany
	}
	a.eng.Insert(v)
	return nil
}
func (a *API) Delete(v int64) error     { a.mu.Lock(); defer a.mu.Unlock(); return a.eng.Delete(v) }
func (a *API) Median() (float64, error) { a.mu.RLock(); defer a.mu.RUnlock(); return a.eng.Median() }
func (a *API) QuantileP90() (int64, error) {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.eng.QuantileP90()
}
func (a *API) Count() int { a.mu.RLock(); defer a.mu.RUnlock(); return a.eng.Count() }

// SelfCheck replays a built-in sequence against a sorted batch mirror and
// verifies invariants 1-4 plus logarithmic descent; nil means all passed.
func (a *API) SelfCheck() error {
	probe, err := New(8)
	if err != nil {
		return err
	}
	var ms []int64
	for _, op := range selfCheckOps {
		if op.ins {
			if err := probe.Insert(op.v); err != nil {
				return err
			}
			ms = append(ms, op.v)
		} else {
			if err := probe.Delete(op.v); err != nil {
				return err
			}
			ms = removeOne(ms, op.v)
		}
		sort.Slice(ms, func(i, j int) bool { return ms[i] < ms[j] })
		if err := matchBatch(probe, ms); err != nil {
			return err
		}
	}
	if err := rejectionChecks(); err != nil {
		return err
	}
	return qnt.CheckLogDescent()
}

// matchBatch verifies invariants 1 (batch equality), 2 (median<=p90), 3 (Kth).
func matchBatch(a *API, s []int64) error {
	n := len(s)
	if a.Count() != n {
		return errors.New("api: count mismatch")
	}
	for k := 1; k <= n; k++ {
		got, err := a.eng.Kth(k)
		if err != nil || got != s[k-1] {
			return errors.New("api: Kth sequence mismatch")
		}
	}
	med, merr := a.Median()
	p90, perr := a.QuantileP90()
	if merr != nil || perr != nil || med != batchMedian(s) || p90 != batchP90(s) || med > float64(p90) {
		return errors.New("api: quantile mismatch or median above p90")
	}
	return nil
}

// batchMedian covers both parities: for odd n the two indices coincide.
// Values convert to float64 before adding so int64 extremes cannot overflow.
func batchMedian(s []int64) float64 {
	return (float64(s[(len(s)-1)/2]) + float64(s[len(s)/2])) / 2
}
func batchP90(s []int64) int64 { return s[(90*len(s)+99)/100-1] }

func removeOne(s []int64, v int64) []int64 {
	for i, x := range s {
		if x == v {
			return append(append([]int64{}, s[:i]...), s[i+1:]...)
		}
	}
	return s
}

// rejectionChecks proves the four failures are distinct and leave no trace (4).
func rejectionChecks() error {
	if _, err := New(0); !errors.Is(err, ErrBadConfig) {
		return errors.New("api: missing ErrBadConfig")
	}
	a, _ := New(1)
	if _, err := a.Median(); !errors.Is(err, ErrEmpty) {
		return errors.New("api: missing ErrEmpty")
	}
	if err := a.Insert(7); err != nil {
		return err
	}
	if err := a.Insert(8); !errors.Is(err, ErrTooMany) || a.Count() != 1 {
		return errors.New("api: overflow changed state")
	}
	if err := a.Delete(99); !errors.Is(err, ErrAbsent) || a.Count() != 1 {
		return errors.New("api: absent delete changed state")
	}
	if err := a.Insert(7); !errors.Is(err, ErrTooMany) {
		return errors.New("api: not still full after rejection")
	}
	if err := a.Delete(7); err != nil || a.Count() != 0 {
		return errors.New("api: recovery after rejection failed")
	}
	return nil
}
