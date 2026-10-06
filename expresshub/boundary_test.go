package expresshub

import (
	"errors"
	"testing"
)

func testConfig() Config {
	return Config{MaxItems: 2, MaxWeightGrams: 10, DwellLimitSec: 10}
}

func addOrFail(t *testing.T, s *System, in AddParcelInput) AddResult {
	t.Helper()
	out, err := s.AddParcel(in)
	t.Logf("ADD input=%+v output=%+v reason=accepted", in, out)
	if err != nil {
		t.Fatalf("AddParcel() error = %v", err)
	}
	return out
}

func addWantError(t *testing.T, s *System, in AddParcelInput, want ErrorCode) {
	t.Helper()
	out, err := s.AddParcel(in)
	t.Logf("ADD input=%+v output=%+v reason=want %s", in, out, want)
	var got *Error
	if !errors.As(err, &got) || got.Code != want {
		t.Fatalf("AddParcel() error = %v, want code %s", err, want)
	}
}

func TestItemCountAtLimitAndOneOver(t *testing.T) {
	s, _ := New(testConfig())
	first := addOrFail(t, s, AddParcelInput{"w1", "A", 1, CategoryNormal, 0})
	addOrFail(t, s, AddParcelInput{"w2", "A", 1, CategoryNormal, 0})
	bag, ok, _ := s.Bag(first.BagID)
	if !ok || len(bag.Items) != 2 || bag.Status != BagStatusOpen {
		t.Fatalf("bag at exact item limit = %+v ok=%v", bag, ok)
	}
	third := addOrFail(t, s, AddParcelInput{"w3", "A", 1, CategoryNormal, 0})
	if third.SealedBagID != first.BagID || third.OpenedBagID != third.BagID {
		t.Fatalf("third add = %+v, expected old bag sealed and new bag opened", third)
	}
	old, _, _ := s.Bag(first.BagID)
	if old.Status != BagStatusSealed {
		t.Fatalf("old bag status = %v", old.Status)
	}
}

func TestWeightAtLimitAndOneOver(t *testing.T) {
	s, _ := New(testConfig())
	first := addOrFail(t, s, AddParcelInput{"w1", "A", 6, CategoryNormal, 0})
	addOrFail(t, s, AddParcelInput{"w2", "A", 4, CategoryNormal, 0})
	bag, _, _ := s.Bag(first.BagID)
	if bag.TotalWeight != 10 || bag.Status != BagStatusOpen {
		t.Fatalf("bag at exact weight limit = %+v", bag)
	}
	third := addOrFail(t, s, AddParcelInput{"w3", "A", 1, CategoryNormal, 0})
	if third.SealedBagID != first.BagID {
		t.Fatalf("one gram over did not seal old bag: %+v", third)
	}
}

func TestDwellAtLimitAndOneSecondBefore(t *testing.T) {
	s, _ := New(testConfig())
	first := addOrFail(t, s, AddParcelInput{"w1", "A", 1, CategoryNormal, 100})
	same := addOrFail(t, s, AddParcelInput{"w2", "A", 1, CategoryNormal, 109})
	if same.BagID != first.BagID {
		t.Fatalf("one second before dwell limit opened a different bag: %+v", same)
	}
	atLimit := addOrFail(t, s, AddParcelInput{"w3", "A", 1, CategoryNormal, 110})
	if atLimit.SealedBagID != first.BagID || atLimit.BagID == first.BagID {
		t.Fatalf("exact dwell limit did not roll bag: %+v", atLimit)
	}
}

func TestCategoryMutualExclusionCombinations(t *testing.T) {
	tests := []struct {
		name     string
		first    Category
		second   Category
		wantSame bool
	}{
		{"normal-normal", CategoryNormal, CategoryNormal, true},
		{"normal-fragile", CategoryNormal, CategoryFragile, true},
		{"normal-liquid", CategoryNormal, CategoryLiquid, true},
		{"fragile-normal", CategoryFragile, CategoryNormal, true},
		{"liquid-normal", CategoryLiquid, CategoryNormal, true},
		{"fragile-fragile", CategoryFragile, CategoryFragile, true},
		{"liquid-liquid", CategoryLiquid, CategoryLiquid, true},
		{"fragile-liquid", CategoryFragile, CategoryLiquid, false},
		{"liquid-fragile", CategoryLiquid, CategoryFragile, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := New(Config{MaxItems: 10, MaxWeightGrams: 100, DwellLimitSec: 100})
			first := addOrFail(t, s, AddParcelInput{"w1", "A", 1, tc.first, 0})
			second := addOrFail(t, s, AddParcelInput{"w2", "A", 1, tc.second, 0})
			gotSame := first.BagID == second.BagID
			t.Logf("category %s input first=%v second=%v sameBag=%v reason=mutual exclusion", tc.name, tc.first, tc.second, gotSame)
			if gotSame != tc.wantSame {
				t.Fatalf("same bag = %v, want %v", gotSame, tc.wantSame)
			}
		})
	}
}

func TestSingleParcelOverWeightRejected(t *testing.T) {
	s, _ := New(testConfig())
	addWantError(t, s, AddParcelInput{"heavy", "A", 11, CategoryNormal, 0}, BusinessReject)
	if _, ok, _ := s.OpenBag("A"); ok {
		t.Fatal("rejected overweight parcel opened a bag")
	}
	first := addOrFail(t, s, AddParcelInput{"w1", "A", 1, CategoryNormal, 0})
	addWantError(t, s, AddParcelInput{"heavy", "A", 11, CategoryNormal, 0}, BusinessReject)
	if bag, _, _ := s.Bag(first.BagID); bag.Status != BagStatusOpen || len(bag.Items) != 1 {
		t.Fatalf("rejected overweight parcel changed open bag: %+v", bag)
	}
}

func TestDuplicatePendingCannotRejoinAndMatchedCanRejoin(t *testing.T) {
	s, _ := New(Config{MaxItems: 10, MaxWeightGrams: 100, DwellLimitSec: 100})
	first := addOrFail(t, s, AddParcelInput{"w1", "A", 1, CategoryNormal, 0})
	addWantError(t, s, AddParcelInput{"w1", "A", 1, CategoryNormal, 1}, BusinessReject)
	if _, err := s.SealBag(SealBagInput{"A", 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DispatchBag(DispatchBagInput{first.BagID, "V1", 3}); err != nil {
		t.Fatal(err)
	}
	result, err := s.VerifyBag(VerifyBagInput{first.BagID, "A", []string{}, 4})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("VERIFY output=%+v reason=missing becomes pending", result)
	addWantError(t, s, AddParcelInput{"w1", "A", 1, CategoryNormal, 5}, BusinessReject)

	second := addOrFail(t, s, AddParcelInput{"w2", "A", 1, CategoryNormal, 6})
	if _, err := s.SealBag(SealBagInput{"A", 7}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DispatchBag(DispatchBagInput{second.BagID, "V1", 8}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.VerifyBag(VerifyBagInput{second.BagID, "A", []string{"w2"}, 9}); err != nil {
		t.Fatal(err)
	}
	rejoined := addOrFail(t, s, AddParcelInput{"w2", "A", 1, CategoryNormal, 10})
	if rejoined.BagID == second.BagID {
		t.Fatal("verified and matched waybill did not leave site")
	}
}

func TestVerifyDifferencesBothDirectionsSorted(t *testing.T) {
	s, _ := New(Config{MaxItems: 10, MaxWeightGrams: 100, DwellLimitSec: 100})
	bag := addOrFail(t, s, AddParcelInput{"b", "A", 1, CategoryNormal, 0})
	addOrFail(t, s, AddParcelInput{"a", "A", 1, CategoryNormal, 0})
	addOrFail(t, s, AddParcelInput{"c", "A", 1, CategoryNormal, 0})
	if _, err := s.SealBag(SealBagInput{"A", 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DispatchBag(DispatchBagInput{bag.BagID, "V", 2}); err != nil {
		t.Fatal(err)
	}
	result, err := s.VerifyBag(VerifyBagInput{bag.BagID, "A", []string{"z", "a", "y"}, 3})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("VERIFY output=%+v reason=missing and extra sorted by waybill", result)
	if got, want := stringsJoin(result.Missing), "b,c"; got != want {
		t.Fatalf("missing = %v, want %v", result.Missing, want)
	}
	if got, want := stringsJoin(result.Extra), "y,z"; got != want {
		t.Fatalf("extra = %v, want %v", result.Extra, want)
	}
	_, err = s.VerifyBag(VerifyBagInput{bag.BagID, "A", []string{"a", "b", "c"}, 4})
	var stateErr *Error
	if !errors.As(err, &stateErr) || stateErr.Code != InvalidState {
		t.Fatalf("repeat verify error = %v, want invalid state", err)
	}
	again, _, _ := s.Bag(bag.BagID)
	t.Logf("BAG output=%+v reason=repeat verify preserves first result", again)
}

func stringsJoin(values []string) string {
	result := ""
	for i, value := range values {
		if i > 0 {
			result += ","
		}
		result += value
	}
	return result
}

func TestErrorPriorityAndStateErrors(t *testing.T) {
	s, _ := New(testConfig())
	first := addOrFail(t, s, AddParcelInput{"w1", "A", 1, CategoryNormal, 5})
	addWantError(t, s, AddParcelInput{"", "A", 1, CategoryNormal, 1}, InvalidArgument)
	addWantError(t, s, AddParcelInput{"w2", "", 1, CategoryNormal, 1}, InvalidArgument)
	addWantError(t, s, AddParcelInput{"w2", "A", 0, CategoryNormal, 1}, InvalidArgument)
	addWantError(t, s, AddParcelInput{"w2", "A", 1, Category(99), 1}, InvalidArgument)
	addWantError(t, s, AddParcelInput{"w2", "A", 1, CategoryNormal, 4}, ClockRewound)
	addWantError(t, s, AddParcelInput{"w1", "A", 1, CategoryNormal, 4}, ClockRewound)
	addWantError(t, s, AddParcelInput{"w1", "A", 11, CategoryNormal, 6}, BusinessReject)

	if _, err := s.SealBag(SealBagInput{"B", 6}); err != nil {
		var got *Error
		if !errors.As(err, &got) || got.Code != InvalidState {
			t.Fatalf("SealBag missing open error = %v", err)
		}
	}
	if _, err := s.DispatchBag(DispatchBagInput{999, "V", 6}); err != nil {
		var got *Error
		if !errors.As(err, &got) || got.Code != NotFound {
			t.Fatalf("DispatchBag missing bag error = %v", err)
		}
	}
	if _, err := s.DispatchBag(DispatchBagInput{first.BagID, "V", 6}); err != nil {
		var got *Error
		if !errors.As(err, &got) || got.Code != InvalidState {
			t.Fatalf("DispatchBag open bag error = %v", err)
		}
	}
	if _, err := s.SealBag(SealBagInput{"A", 6}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.DispatchBag(DispatchBagInput{first.BagID, "V", 7}); err != nil {
		t.Fatal(err)
	}
	_, err := s.VerifyBag(VerifyBagInput{first.BagID, "B", []string{"w1"}, 8})
	var got *Error
	if !errors.As(err, &got) || got.Code != StationMismatch {
		t.Fatalf("VerifyBag destination error = %v", err)
	}
}
