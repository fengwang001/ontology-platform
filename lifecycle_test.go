package main

import (
	"errors"
	"math/big"
	"strings"
	"sync"
	"testing"
)

func testConfig() LifecycleConfig {
	return LifecycleConfig{
		HotPricePerDay:         10,
		CoolPricePerDay:        4,
		ColdPricePerDay:        1,
		CoolTransitionDays:     30,
		ColdTransitionDays:     90,
		MinCoolStayDays:        30,
		MinColdStayDays:        90,
		CoolRetrievalPrice:     2,
		ColdRetrievalPrice:     5,
		FreeRetrievalPerPeriod: 5,
	}
}

func mustBilling(t *testing.T, config LifecycleConfig) *LifecycleBilling {
	t.Helper()
	billing, err := NewLifecycleBilling(config)
	if err != nil {
		t.Fatalf("NewLifecycleBilling() error = %v", err)
	}
	return billing
}

func wantInt(value int64) *big.Int {
	return big.NewInt(value)
}

func assertCharges(t *testing.T, got Charges, storage int64, earlyExit int64, retrieval int64) {
	t.Helper()
	if got.StorageFee.Cmp(wantInt(storage)) != 0 ||
		got.EarlyExitFee.Cmp(wantInt(earlyExit)) != 0 ||
		got.RetrievalFee.Cmp(wantInt(retrieval)) != 0 {
		t.Fatalf("charges = (storage=%s, earlyExit=%s, retrieval=%s), want (%d, %d, %d)",
			got.StorageFee, got.EarlyExitFee, got.RetrievalFee, storage, earlyExit, retrieval)
	}
}

func TestSpecExample(t *testing.T) {
	billing := mustBilling(t, testConfig())

	putCharges, err := billing.Put("k", 10, 0)
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	assertCharges(t, putCharges, 0, 0, 0)

	getCharges, err := billing.Get("k", 40)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	assertCharges(t, getCharges, 3400, 800, 10)

	deleteCharges, err := billing.Delete("k", 200)
	if err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	assertCharges(t, deleteCharges, 6100, 200, 0)
}

func TestExtremeValuesDoNotOverflow(t *testing.T) {
	config := LifecycleConfig{
		HotPricePerDay:         1_000_000,
		CoolPricePerDay:        1_000_000,
		ColdPricePerDay:        1_000_000,
		CoolTransitionDays:     1_000_000,
		ColdTransitionDays:     1_000_000,
		MinCoolStayDays:        1_000_000,
		MinColdStayDays:        1_000_000,
		CoolRetrievalPrice:     1_000_000,
		ColdRetrievalPrice:     1_000_000,
		FreeRetrievalPerPeriod: 0,
	}
	if _, err := NewLifecycleBilling(config); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("config with A == B error = %v, want ErrInvalidArgument", err)
	}

	config.ColdTransitionDays = 1_000_000
	config.CoolTransitionDays = 999_999
	billing := mustBilling(t, config)
	mustPut(t, billing, "k", 1_000_000, 0)

	charges, err := billing.Delete("k", 1_000_000)
	if err != nil {
		t.Fatal(err)
	}
	storage, _ := new(big.Int).SetString("1000000000000000000", 10)
	earlyExit, _ := new(big.Int).SetString("1000000000000000000", 10)
	if charges.StorageFee.Cmp(storage) != 0 || charges.EarlyExitFee.Cmp(earlyExit) != 0 {
		t.Fatalf("charges = storage %s early %s, want %s and %s",
			charges.StorageFee, charges.EarlyExitFee, storage, earlyExit)
	}
}

func TestTierBoundariesAndEarlyExit(t *testing.T) {
	billing := mustBilling(t, testConfig())

	if _, err := billing.Put("cool-boundary", 2, 0); err != nil {
		t.Fatal(err)
	}
	charges, err := billing.Delete("cool-boundary", 30)
	if err != nil {
		t.Fatal(err)
	}
	assertCharges(t, charges, 600, 240, 0)

	billing = mustBilling(t, testConfig())
	if _, err := billing.Put("hot-boundary", 2, 0); err != nil {
		t.Fatal(err)
	}
	charges, err = billing.Delete("hot-boundary", 29)
	if err != nil {
		t.Fatal(err)
	}
	assertCharges(t, charges, 580, 0, 0)

	billing = mustBilling(t, testConfig())
	if _, err := billing.Put("cold-boundary", 2, 0); err != nil {
		t.Fatal(err)
	}
	charges, err = billing.Delete("cold-boundary", 90)
	if err != nil {
		t.Fatal(err)
	}
	assertCharges(t, charges, 1080, 180, 0)

	billing = mustBilling(t, testConfig())
	if _, err := billing.Put("min-cool", 2, 0); err != nil {
		t.Fatal(err)
	}
	charges, err = billing.Delete("min-cool", 60)
	if err != nil {
		t.Fatal(err)
	}
	assertCharges(t, charges, 840, 0, 0)

	billing = mustBilling(t, testConfig())
	if _, err := billing.Put("cold-no-cool-fee", 2, 0); err != nil {
		t.Fatal(err)
	}
	charges, err = billing.Delete("cold-no-cool-fee", 91)
	if err != nil {
		t.Fatal(err)
	}
	assertCharges(t, charges, 1082, 178, 0)
}

func TestFreeQuotaSharingAndPeriodReset(t *testing.T) {
	billing := mustBilling(t, testConfig())
	mustPut(t, billing, "a", 3, 0)
	mustPut(t, billing, "b", 4, 0)

	charges, err := billing.Get("a", 30)
	if err != nil {
		t.Fatal(err)
	}
	assertCharges(t, charges, 900, 360, 0)

	charges, err = billing.Get("b", 30)
	if err != nil {
		t.Fatal(err)
	}
	assertCharges(t, charges, 1200, 480, 4)

	charges, err = billing.Get("a", 59)
	if err != nil {
		t.Fatal(err)
	}
	assertCharges(t, charges, 870, 0, 0)

	charges, err = billing.Get("b", 60)
	if err != nil {
		t.Fatal(err)
	}
	assertCharges(t, charges, 1200, 480, 0)
	if billing.used != 4 || billing.period != 2 {
		t.Fatalf("quota = period %d used %d, want period 2 used 4", billing.period, billing.used)
	}
}

func TestHotGetDoesNotConsumeQuota(t *testing.T) {
	billing := mustBilling(t, testConfig())
	mustPut(t, billing, "hot", 10, 10)
	charges, err := billing.Get("hot", 20)
	if err != nil {
		t.Fatal(err)
	}
	assertCharges(t, charges, 1000, 0, 0)
	if billing.period != 0 || billing.used != 0 {
		t.Fatalf("quota = period %d used %d, want unchanged period 0 used 0", billing.period, billing.used)
	}
}

func TestOverwriteSettlesOldObjectAndResetsLastAccess(t *testing.T) {
	billing := mustBilling(t, testConfig())
	mustPut(t, billing, "k", 10, 0)
	charges, err := billing.Put("k", 7, 40)
	if err != nil {
		t.Fatal(err)
	}
	assertCharges(t, charges, 3400, 800, 0)

	deleteCharges, err := billing.Delete("k", 41)
	if err != nil {
		t.Fatal(err)
	}
	assertCharges(t, deleteCharges, 70, 0, 0)
}

func TestGetResetsLastAccessToHot(t *testing.T) {
	billing := mustBilling(t, testConfig())
	mustPut(t, billing, "k", 10, 0)
	if _, err := billing.Get("k", 40); err != nil {
		t.Fatal(err)
	}
	charges, err := billing.Delete("k", 41)
	if err != nil {
		t.Fatal(err)
	}
	assertCharges(t, charges, 100, 0, 0)
}

func TestGetManyRepeatedKeyAndAtomicMissingKey(t *testing.T) {
	billing := mustBilling(t, testConfig())
	mustPut(t, billing, "a", 10, 0)

	result, err := billing.GetMany([]string{"a", "missing", "a"}, 40)
	if !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("GetMany() error = %v, want ErrObjectNotFound", err)
	}
	if len(result.Items) != 0 {
		t.Fatalf("GetMany() changed result on rejection: %+v", result)
	}
	if billing.objects["a"].lastAccess != 0 || billing.used != 0 || billing.maxNow != 0 {
		t.Fatalf("rejected GetMany changed state: object=%+v used=%d maxNow=%d",
			billing.objects["a"], billing.used, billing.maxNow)
	}

	result, err = billing.GetMany([]string{"a", "a"}, 40)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Items) != 2 {
		t.Fatalf("len(items) = %d, want 2", len(result.Items))
	}
	assertCharges(t, result.Items[0], 3400, 800, 10)
	assertCharges(t, result.Items[1], 0, 0, 0)
	assertCharges(t, result.Total, 3400, 800, 10)
}

func TestRejectedOperationsDoNotChangeState(t *testing.T) {
	original := testConfig()
	if _, err := NewLifecycleBilling(LifecycleConfig{}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("invalid config error = %v", err)
	}

	billing := mustBilling(t, original)
	mustPut(t, billing, "k", 10, 10)

	if _, err := billing.Put("", 1, 11); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("empty key error = %v", err)
	}
	if _, err := billing.Put(strings.Repeat("x", 65), 1, 11); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("long key error = %v", err)
	}
	if _, err := billing.Put("bad-size", 0, 11); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("bad size error = %v", err)
	}
	if _, err := billing.Put("clock", 1, 9); !errors.Is(err, ErrClockRolledBack) {
		t.Fatalf("clock rollback error = %v", err)
	}
	if _, err := billing.Get("missing", 11); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("missing get error = %v", err)
	}
	if _, err := billing.Delete("missing", 11); !errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("missing delete error = %v", err)
	}

	if billing.maxNow != 10 || billing.used != 0 || billing.period != 0 {
		t.Fatalf("state changed after rejected operations: maxNow=%d period=%d used=%d",
			billing.maxNow, billing.period, billing.used)
	}
	if object := billing.objects["k"]; object.lastAccess != 10 || object.size != 10 {
		t.Fatalf("existing object changed: %+v", object)
	}
	if _, ok := billing.objects["clock"]; ok {
		t.Fatal("rejected put inserted object")
	}
}

func mustPut(t *testing.T, billing *LifecycleBilling, key string, size int64, now int64) {
	t.Helper()
	if _, err := billing.Put(key, size, now); err != nil {
		t.Fatalf("Put(%q, %d, %d) error = %v", key, size, now, err)
	}
}

func TestConcurrentOperationsAreSerializable(t *testing.T) {
	billing := mustBilling(t, testConfig())
	var wait sync.WaitGroup
	for worker := 0; worker < 64; worker++ {
		wait.Add(1)
		key := strings.Repeat("k", 1) + string(rune('a'+worker%26)) + string(rune('a'+worker/26))
		go func(key string) {
			defer wait.Done()
			if _, err := billing.Put(key, 3, 10); err != nil {
				t.Errorf("Put() error = %v", err)
				return
			}
			if _, err := billing.Get(key, 10); err != nil {
				t.Errorf("Get() error = %v", err)
				return
			}
			if _, err := billing.Delete(key, 10); err != nil {
				t.Errorf("Delete() error = %v", err)
			}
		}(key)
	}
	wait.Wait()
	if len(billing.objects) != 0 {
		t.Fatalf("objects = %d, want 0", len(billing.objects))
	}
	if billing.maxNow != 10 {
		t.Fatalf("maxNow = %d, want 10", billing.maxNow)
	}
}
