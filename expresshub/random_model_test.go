package expresshub

import (
	"fmt"
	"math/rand"
	"sort"
	"strconv"
	"testing"
)

type modelOp struct {
	kind     string
	add      AddParcelInput
	seal     SealBagInput
	dispatch DispatchBagInput
	verify   VerifyBagInput
}

type opResult struct {
	code   ErrorCode
	add    AddResult
	sealID int64
	verify VerifyResult
}

func TestRandomSequencesAgainstNaiveModel(t *testing.T) {
	cfg := Config{MaxItems: 3, MaxWeightGrams: 12, DwellLimitSec: 5}
	for seed := int64(0); seed < 300; seed++ {
		t.Run("seed-"+strconv.FormatInt(seed, 10), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			system, _ := New(cfg)
			model := newNaiveModel(cfg)
			destinations := []string{"A", "B", "C"}
			knownBags := []int64{}
			now := int64(0)

			for step := 0; step < 120; step++ {
				now += int64(rng.Intn(4))
				op := modelOp{}
				switch rng.Intn(10) {
				case 0, 1, 2, 3, 4:
					op.kind = "add"
					waybill := "w" + strconv.Itoa(rng.Intn(20))
					if rng.Intn(20) == 0 {
						waybill = ""
					}
					op.add = AddParcelInput{
						Waybill:     waybill,
						Destination: destinations[rng.Intn(len(destinations))],
						WeightGrams: int64(rng.Intn(15) + 1),
						Category:    Category(rng.Intn(3) + 1),
						At:          now,
					}
				case 5, 6:
					op.kind = "seal"
					op.seal = SealBagInput{
						Destination: destinations[rng.Intn(len(destinations))],
						At:          now,
					}
				case 7:
					op.kind = "dispatch"
					id := int64(1)
					if len(knownBags) > 0 {
						id = knownBags[rng.Intn(len(knownBags))]
					}
					if rng.Intn(10) == 0 {
						id = 999
					}
					op.dispatch = DispatchBagInput{
						BagID:     id,
						VehicleID: "V" + strconv.Itoa(rng.Intn(3)),
						At:        now,
					}
				default:
					op.kind = "verify"
					id := int64(1)
					if len(knownBags) > 0 {
						id = knownBags[rng.Intn(len(knownBags))]
					}
					scanned := []string{}
					for _, waybill := range []string{"w0", "w1", "w2", "w3", "w4", "x", "y"} {
						if rng.Intn(2) == 0 {
							scanned = append(scanned, waybill)
						}
					}
					if rng.Intn(15) == 0 && len(scanned) > 0 {
						scanned = append(scanned, scanned[0])
					}
					destination := destinations[rng.Intn(len(destinations))]
					if rng.Intn(8) == 0 {
						destination = "Z"
					}
					op.verify = VerifyBagInput{
						BagID:       id,
						Destination: destination,
						Scanned:     scanned,
						At:          now,
					}
				}

				got := runSystemOp(system, op)
				want := runNaiveTestOp(model, op)
				t.Logf("seed=%d step=%d input=%#v output=%+v reason=random-naive-reference", seed, step, op, got)
				if resultsDiffer(got, want) {
					t.Fatalf("result mismatch got=%+v want=%+v", got, want)
				}
				if got.code == "" {
					knownBags = appendUnique(knownBags, acceptedBagID(got))
				}
				assertStateMatches(t, system, model)
			}
		})
	}
}

func runSystemOp(system *System, op modelOp) opResult {
	switch op.kind {
	case "add":
		out, err := system.AddParcel(op.add)
		return opResult{code: codeOf(err), add: out}
	case "seal":
		out, err := system.SealBag(op.seal)
		return opResult{code: codeOf(err), sealID: out.BagID}
	case "dispatch":
		_, err := system.DispatchBag(op.dispatch)
		if err != nil {
			return opResult{code: codeOf(err)}
		}
		return opResult{sealID: op.dispatch.BagID}
	default:
		out, err := system.VerifyBag(op.verify)
		if err != nil {
			return opResult{code: codeOf(err)}
		}
		return opResult{sealID: op.verify.BagID, verify: out}
	}
}

func runNaiveTestOp(model *naiveModel, op modelOp) opResult {
	switch op.kind {
	case "add":
		out, code := model.add(op.add)
		return opResult{code: code, add: out}
	case "seal":
		id, code := model.seal(op.seal)
		return opResult{code: code, sealID: id}
	case "dispatch":
		id, code := model.dispatch(op.dispatch)
		if code != "" {
			id = 0
		}
		return opResult{code: code, sealID: id}
	default:
		out, code := model.verify(op.verify)
		if code != "" {
			return opResult{code: code}
		}
		return opResult{code: code, sealID: op.verify.BagID, verify: out}
	}
}

func resultsDiffer(got, want opResult) bool {
	if got.code != want.code || got.sealID != want.sealID {
		return true
	}
	if got.code != "" {
		return false
	}
	if got.add != want.add {
		return true
	}
	return fmt.Sprint(got.verify) != fmt.Sprint(want.verify)
}

func acceptedBagID(result opResult) int64 {
	switch {
	case result.add.BagID != 0:
		return result.add.BagID
	case result.sealID != 0:
		return result.sealID
	case result.verify.BagID != 0:
		return result.verify.BagID
	default:
		return 0
	}
}

func appendUnique(values []int64, value int64) []int64 {
	if value <= 0 {
		return values
	}
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func assertStateMatches(t *testing.T, system *System, model *naiveModel) {
	t.Helper()
	for _, expected := range model.bags {
		actual, ok, err := system.Bag(expected.id)
		if err != nil || !ok {
			t.Fatalf("bag %d missing: ok=%v err=%v", expected.id, ok, err)
		}
		if actual.Status != expected.status ||
			actual.Destination != expected.destination ||
			len(actual.Items) != len(expected.items) ||
			actual.TotalWeight != expected.weight ||
			actual.FirstAddedAt != expected.firstAt ||
			actual.SealedAt != expected.sealedAt ||
			actual.DispatchedAt != expected.dispatchAt ||
			actual.VerifiedAt != expected.verifiedAt ||
			actual.VehicleID != expected.vehicle {
			t.Fatalf("bag %d mismatch actual=%+v expected=%+v", expected.id, actual, expected)
		}
		for i, item := range expected.items {
			if actual.Items[i].Waybill != item.waybill ||
				actual.Items[i].Destination != item.destination ||
				actual.Items[i].WeightGrams != item.weight ||
				actual.Items[i].Category != item.category ||
				actual.Items[i].BagID != item.bagID {
				t.Fatalf("bag %d item %d mismatch actual=%+v expected=%+v", expected.id, i, actual.Items[i], item)
			}
		}
		if expected.status == BagStatusVerified {
			bag, _, _ := system.Bag(expected.id)
			_ = bag
		}
	}
	for waybill, expected := range model.parcels {
		actual, ok, err := system.Location(waybill)
		if err != nil || !ok {
			t.Fatalf("parcel %s missing: ok=%v err=%v", waybill, ok, err)
		}
		wantStatus := parcelStatusFromModel(model, expected)
		if actual.BagID != expected.bagID ||
			actual.Destination != expected.destination ||
			actual.Status != wantStatus {
			t.Fatalf("parcel %s mismatch actual=%+v expected=%+v", waybill, actual, expected)
		}
	}
	activeWaybills := make([]string, 0, len(model.parcels))
	for waybill := range model.parcels {
		activeWaybills = append(activeWaybills, waybill)
	}
	sort.Strings(activeWaybills)
	if got := len(system.active); got != len(model.parcels) {
		t.Fatalf("active count = %d, want %d (%v)", got, len(model.parcels), activeWaybills)
	}
}

func parcelStatusFromModel(model *naiveModel, parcel naiveParcel) LocationStatus {
	if parcel.pending {
		return LocationPendingInvestigation
	}
	target := model.bag(parcel.bagID)
	switch target.status {
	case BagStatusOpen:
		return LocationInOpenBag
	case BagStatusSealed:
		return LocationInSealedBag
	case BagStatusDispatched, BagStatusVerified:
		return LocationInDispatchedBag
	}
	return LocationPendingInvestigation
}
