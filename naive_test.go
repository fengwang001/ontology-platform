package main

import (
	"fmt"
	"math/big"
	"math/rand"
	"testing"
)

type naiveOp struct {
	kind  string
	key   string
	keys  []string
	size  int64
	now   int64
	valid bool
}

type naiveBilling struct {
	config  LifecycleConfig
	objects map[string]LifecycleObject
	maxNow  int64
	period  int64
	used    int64
}

type naiveResult struct {
	items []Charges
	total Charges
	err   error
}

func newNaiveBilling(config LifecycleConfig) *naiveBilling {
	return &naiveBilling{
		config:  config,
		objects: make(map[string]LifecycleObject),
	}
}

func naiveTier(size int64, days int64, price int64) *big.Int {
	result := new(big.Int).Mul(big.NewInt(size), big.NewInt(days))
	return result.Mul(result, big.NewInt(price))
}

func naiveSettle(config LifecycleConfig, object LifecycleObject, now int64) Charges {
	delta := now - object.LastAccess
	hotDays := minInt64(delta, config.CoolTransitionDays)
	coolDays := maxInt64(0, minInt64(delta, config.ColdTransitionDays)-config.CoolTransitionDays)
	coldDays := maxInt64(0, delta-config.ColdTransitionDays)

	charges := zeroCharges()
	charges.StorageFee.Add(charges.StorageFee, naiveTier(object.Size, hotDays, config.HotPricePerDay))
	charges.StorageFee.Add(charges.StorageFee, naiveTier(object.Size, coolDays, config.CoolPricePerDay))
	charges.StorageFee.Add(charges.StorageFee, naiveTier(object.Size, coldDays, config.ColdPricePerDay))

	if delta >= config.CoolTransitionDays && delta < config.ColdTransitionDays {
		missing := maxInt64(0, config.MinCoolStayDays-(delta-config.CoolTransitionDays))
		charges.EarlyExitFee = naiveTier(object.Size, missing, config.CoolPricePerDay)
	}
	if delta >= config.ColdTransitionDays {
		missing := maxInt64(0, config.MinColdStayDays-(delta-config.ColdTransitionDays))
		charges.EarlyExitFee = naiveTier(object.Size, missing, config.ColdPricePerDay)
	}
	return charges
}

func naiveValidKey(key string) bool {
	return key != "" && len(key) <= 64
}

func (b *naiveBilling) retrievalFee(size int64, object LifecycleObject, now int64) *big.Int {
	delta := now - object.LastAccess
	if delta < b.config.CoolTransitionDays {
		return big.NewInt(0)
	}

	price := b.config.CoolRetrievalPrice
	if delta >= b.config.ColdTransitionDays {
		price = b.config.ColdRetrievalPrice
	}
	currentPeriod := now / 30
	if currentPeriod != b.period {
		b.period = currentPeriod
		b.used = 0
	}
	remaining := maxInt64(0, b.config.FreeRetrievalPerPeriod-b.used)
	free := minInt64(size, remaining)
	b.used += free
	return naiveTier(size-free, 1, price)
}

func (b *naiveBilling) get(key string, now int64) (Charges, error) {
	object, ok := b.objects[key]
	if !ok {
		return Charges{}, ErrObjectNotFound
	}
	charges := naiveSettle(b.config, object, now)
	charges.RetrievalFee = b.retrievalFee(object.Size, object, now)
	object.LastAccess = now
	b.objects[key] = object
	b.maxNow = now
	return charges, nil
}

func (b *naiveBilling) run(op naiveOp) naiveResult {
	switch op.kind {
	case "put":
		if !op.valid {
			return naiveResult{err: ErrInvalidArgument}
		}
		if op.now < b.maxNow {
			return naiveResult{err: ErrClockRolledBack}
		}
		charges := zeroCharges()
		if old, ok := b.objects[op.key]; ok {
			charges = naiveSettle(b.config, old, op.now)
		}
		b.objects[op.key] = LifecycleObject{Size: op.size, LastAccess: op.now}
		b.maxNow = op.now
		return naiveResult{total: charges}
	case "delete":
		if !op.valid {
			return naiveResult{err: ErrInvalidArgument}
		}
		if op.now < b.maxNow {
			return naiveResult{err: ErrClockRolledBack}
		}
		if _, ok := b.objects[op.key]; !ok {
			return naiveResult{err: ErrObjectNotFound}
		}
		old := b.objects[op.key]
		charges := naiveSettle(b.config, old, op.now)
		delete(b.objects, op.key)
		b.maxNow = op.now
		return naiveResult{total: charges}
	case "get":
		if !op.valid {
			return naiveResult{err: ErrInvalidArgument}
		}
		if op.now < b.maxNow {
			return naiveResult{err: ErrClockRolledBack}
		}
		charges, err := b.get(op.key, op.now)
		return naiveResult{total: charges, err: err}
	case "getmany":
		if !op.valid {
			return naiveResult{err: ErrInvalidArgument}
		}
		if op.now < b.maxNow {
			return naiveResult{err: ErrClockRolledBack}
		}
		for _, key := range op.keys {
			if _, ok := b.objects[key]; !ok {
				return naiveResult{err: ErrObjectNotFound}
			}
		}
		result := naiveResult{items: make([]Charges, 0, len(op.keys)), total: zeroCharges()}
		for _, key := range op.keys {
			charges, _ := b.get(key, op.now)
			result.items = append(result.items, charges)
			result.total.StorageFee.Add(result.total.StorageFee, charges.StorageFee)
			result.total.EarlyExitFee.Add(result.total.EarlyExitFee, charges.EarlyExitFee)
			result.total.RetrievalFee.Add(result.total.RetrievalFee, charges.RetrievalFee)
		}
		return result
	default:
		return naiveResult{err: ErrInvalidArgument}
	}
}

func randomConfig(rng *rand.Rand) LifecycleConfig {
	cool := int64(rng.Intn(20) + 1)
	cold := cool + int64(rng.Intn(25)+1)
	return LifecycleConfig{
		HotPricePerDay:         int64(rng.Intn(6)),
		CoolPricePerDay:        int64(rng.Intn(6)),
		ColdPricePerDay:        int64(rng.Intn(6)),
		CoolTransitionDays:     cool,
		ColdTransitionDays:     cold,
		MinCoolStayDays:        int64(rng.Intn(31)),
		MinColdStayDays:        int64(rng.Intn(31)),
		CoolRetrievalPrice:     int64(rng.Intn(6)),
		ColdRetrievalPrice:     int64(rng.Intn(6)),
		FreeRetrievalPerPeriod: int64(rng.Intn(21)),
	}
}

func randomOps(rng *rand.Rand) []naiveOp {
	count := rng.Intn(40) + 10
	keys := []string{"a", "b", "c", "d"}
	ops := make([]naiveOp, 0, count)
	now := int64(0)
	for index := 0; index < count; index++ {
		switch rng.Intn(20) {
		case 0:
			now = maxInt64(0, now-int64(rng.Intn(3)+1))
		default:
			now += int64(rng.Intn(10))
		}
		if now > 1000 {
			now = int64(rng.Intn(1001))
		}

		op := naiveOp{kind: []string{"put", "get", "delete", "getmany"}[rng.Intn(4)], now: now, valid: true}
		switch op.kind {
		case "put":
			op.key = keys[rng.Intn(len(keys))]
			op.size = int64(rng.Intn(12) + 1)
		case "get", "delete":
			op.key = keys[rng.Intn(len(keys)-1)]
		case "getmany":
			op.keys = make([]string, rng.Intn(3)+1)
			for item := range op.keys {
				op.keys[item] = keys[rng.Intn(len(keys)-1)]
			}
		}

		if rng.Intn(10) == 0 {
			op.valid = false
			if op.kind == "put" {
				op.size = 0
			} else if rng.Intn(2) == 0 {
				op.key = ""
				op.keys = []string{""}
			} else {
				op.now = -1
			}
		}
		ops = append(ops, op)
	}
	return ops
}

func chargesEqual(left Charges, right Charges) bool {
	return left.StorageFee.Cmp(right.StorageFee) == 0 &&
		left.EarlyExitFee.Cmp(right.EarlyExitFee) == 0 &&
		left.RetrievalFee.Cmp(right.RetrievalFee) == 0
}

func runActual(billing *LifecycleBilling, op naiveOp) naiveResult {
	switch op.kind {
	case "put":
		charges, err := billing.Put(op.key, op.size, op.now)
		return naiveResult{total: charges, err: err}
	case "delete":
		charges, err := billing.Delete(op.key, op.now)
		return naiveResult{total: charges, err: err}
	case "get":
		charges, err := billing.Get(op.key, op.now)
		return naiveResult{total: charges, err: err}
	case "getmany":
		result, err := billing.GetMany(op.keys, op.now)
		return naiveResult{items: result.Items, total: result.Total, err: err}
	default:
		return naiveResult{err: ErrInvalidArgument}
	}
}

func stateMatches(t *testing.T, actual *LifecycleBilling, expected *naiveBilling) {
	t.Helper()
	if len(actual.objects) != len(expected.objects) {
		t.Fatalf("object count = %d, want %d", len(actual.objects), len(expected.objects))
	}
	for key, expectedObject := range expected.objects {
		actualObject, ok := actual.objects[key]
		if !ok {
			t.Fatalf("actual missing object %q", key)
		}
		if actualObject.size != expectedObject.Size || actualObject.lastAccess != expectedObject.LastAccess {
			t.Fatalf("object %q = (size=%d, la=%d), want (size=%d, la=%d)",
				key, actualObject.size, actualObject.lastAccess, expectedObject.Size, expectedObject.LastAccess)
		}
	}
	if actual.maxNow != expected.maxNow || actual.period != expected.period || actual.used != expected.used {
		t.Fatalf("state = (maxNow=%d, period=%d, used=%d), want (maxNow=%d, period=%d, used=%d)",
			actual.maxNow, actual.period, actual.used, expected.maxNow, expected.period, expected.used)
	}
}

func TestRandomSequencesMatchNaiveSimulation(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	for sequence := 0; sequence < 2000; sequence++ {
		config := randomConfig(rng)
		billing := mustBilling(t, config)
		naive := newNaiveBilling(config)
		ops := randomOps(rng)

		t.Run(fmt.Sprintf("sequence-%04d", sequence), func(t *testing.T) {
			t.Logf("input config=%+v opCount=%d", config, len(ops))
			for index, op := range ops {
				actual := runActual(billing, op)
				expected := naive.run(op)
				t.Logf("op %d input=%+v actual=(storage=%s early=%s retrieval=%s err=%v) expected=(storage=%s early=%s retrieval=%s err=%v) basis=naive tier storage/current-tier early exit/shared quota",
					index, op,
					actual.total.StorageFee, actual.total.EarlyExitFee, actual.total.RetrievalFee, actual.err,
					expected.total.StorageFee, expected.total.EarlyExitFee, expected.total.RetrievalFee, expected.err)

				if fmt.Sprint(actual.err) != fmt.Sprint(expected.err) {
					t.Fatalf("error = %v, want %v", actual.err, expected.err)
				}
				if actual.err == nil && !chargesEqual(actual.total, expected.total) {
					t.Fatalf("total charges = %+v, want %+v", actual.total, expected.total)
				}
				if len(actual.items) != len(expected.items) {
					t.Fatalf("item count = %d, want %d", len(actual.items), len(expected.items))
				}
				for itemIndex := range actual.items {
					if !chargesEqual(actual.items[itemIndex], expected.items[itemIndex]) {
						t.Fatalf("item %d charges = %+v, want %+v", itemIndex, actual.items[itemIndex], expected.items[itemIndex])
					}
				}
				stateMatches(t, billing, naive)
			}
		})
	}
}
