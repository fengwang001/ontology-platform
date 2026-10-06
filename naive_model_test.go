package smartlocker

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

func decisionLog(t *testing.T, format string, args ...any) {
	t.Helper()
	t.Logf(format, args...)
}

type naiveCell struct {
	id   string
	size Size
}

type naiveParcel struct {
	parcel   Parcel
	cellID   string
	code     string
	storedAt int64
	paidFee  int64
	wrong    int
	locked   bool
}

type cooledCodeState struct {
	code        string
	availableAt int64
}

type naiveLocker struct {
	cells      []naiveCell
	active     map[string]*naiveParcel
	byCode     map[string]*naiveParcel
	cooling    []cooledCodeState
	lastTime   int64
	nextCode   uint64
	policy     FeePolicy
	limit      int64
	cooldown   int64
	wrongLimit int
}

func newNaiveLocker(cfg Config) *naiveLocker {
	model := &naiveLocker{
		active:     make(map[string]*naiveParcel),
		byCode:     make(map[string]*naiveParcel),
		policy:     cfg.FeePolicy,
		limit:      cfg.StorageLimit,
		cooldown:   cfg.CodeCooldown,
		wrongLimit: cfg.WrongCodeLimit,
	}
	for id, size := range cfg.Cells {
		model.cells = append(model.cells, naiveCell{id: id, size: size})
	}
	sort.Slice(model.cells, func(i, j int) bool {
		if model.cells[i].size != model.cells[j].size {
			return model.cells[i].size < model.cells[j].size
		}
		return compareCellID(model.cells[i].id, model.cells[j].id) < 0
	})
	return model
}

func (model *naiveLocker) allocateCell(parcel Parcel) (string, bool, bool) {
	hasFit := false
	occupied := make(map[string]bool)
	for _, item := range model.active {
		occupied[item.cellID] = true
	}
	for _, cell := range model.cells {
		if cell.size < parcel.Size {
			continue
		}
		hasFit = true
		if !occupied[cell.id] {
			return cell.id, true, true
		}
	}
	return "", hasFit, false
}

func (model *naiveLocker) allocateCode(at int64) string {
	bestIndex := -1
	for index := range model.cooling {
		if model.cooling[index].availableAt <= at {
			if bestIndex == -1 ||
				model.cooling[index].availableAt < model.cooling[bestIndex].availableAt ||
				(model.cooling[index].availableAt == model.cooling[bestIndex].availableAt &&
					codeNumber(model.cooling[index].code) < codeNumber(model.cooling[bestIndex].code)) {
				bestIndex = index
			}
		}
	}
	if bestIndex != -1 {
		code := model.cooling[bestIndex].code
		model.cooling = append(model.cooling[:bestIndex], model.cooling[bestIndex+1:]...)
		return code
	}
	model.nextCode++
	return formatCodeNumber(model.nextCode)
}

func (model *naiveLocker) releaseCode(code string, at int64) {
	model.cooling = append(model.cooling, cooledCodeState{code: code, availableAt: at + model.cooldown})
}

func (model *naiveLocker) remove(item *naiveParcel, at int64) {
	model.releaseCode(item.code, at)
	delete(model.byCode, item.code)
	delete(model.active, item.parcel.Waybill)
}

func (model *naiveLocker) deposit(at int64, parcel Parcel) (DepositResult, string) {
	if !validParcelForModel(parcel) || at < 0 {
		return DepositResult{}, ErrInvalidArgument.Code
	}
	if at < model.lastTime {
		return DepositResult{}, ErrClockRewound.Code
	}
	if model.active[parcel.Waybill] != nil {
		return DepositResult{}, ErrDuplicateWaybill.Code
	}
	cellID, hasFit, free := model.allocateCell(parcel)
	if !hasFit {
		return DepositResult{}, ErrNoCompatibleCell.Code
	}
	if !free {
		return DepositResult{}, ErrAllFitCellsOccupied.Code
	}
	code := model.allocateCode(at)
	model.active[parcel.Waybill] = &naiveParcel{parcel: parcel, cellID: cellID, code: code, storedAt: at}
	model.byCode[code] = model.active[parcel.Waybill]
	model.lastTime = at
	return DepositResult{CellID: cellID, Code: code}, ""
}

func (model *naiveLocker) pickup(at int64, code, phone string) (PickupResult, string) {
	if at < 0 || code == "" || !isMainlandMobile(phone) {
		return PickupResult{}, ErrInvalidArgument.Code
	}
	if at < model.lastTime {
		return PickupResult{}, ErrClockRewound.Code
	}
	item := model.byCode[code]
	if item == nil {
		return PickupResult{}, ErrCodeNotFound.Code
	}
	if item.locked {
		return PickupResult{}, ErrParcelLocked.Code
	}
	if phoneSuffix(item.parcel.Phone) != phoneSuffix(phone) {
		item.wrong++
		if item.wrong >= model.wrongLimit {
			item.locked = true
		}
		return PickupResult{}, ErrPhoneMismatch.Code
	}
	if at-item.storedAt >= model.limit {
		return PickupResult{}, ErrParcelTimedOut.Code
	}
	want := feeDue(model.policy, item.storedAt, at, item.paidFee)
	if want > 0 {
		return PickupResult{}, ErrFeeDue.Code
	}
	result := PickupResult{Waybill: item.parcel.Waybill, CellID: item.cellID, FeePaid: item.paidFee}
	model.remove(item, at)
	model.lastTime = at
	return result, ""
}

func (model *naiveLocker) pay(at int64, waybill string, amount int64) (PaymentResult, string) {
	if at < 0 || waybill == "" || amount < 0 {
		return PaymentResult{}, ErrInvalidArgument.Code
	}
	if at < model.lastTime {
		return PaymentResult{}, ErrClockRewound.Code
	}
	item := model.active[waybill]
	if item == nil {
		return PaymentResult{}, ErrInvalidArgument.Code
	}
	want := feeDue(model.policy, item.storedAt, at, item.paidFee)
	if amount != want {
		return PaymentResult{}, ErrWrongPaymentAmount.Code
	}
	item.paidFee += amount
	model.lastTime = at
	return PaymentResult{Paid: item.paidFee, Due: want}, ""
}

func (model *naiveLocker) recycle(at int64, waybill string) (RecycleResult, string) {
	if at < 0 || waybill == "" {
		return RecycleResult{}, ErrInvalidArgument.Code
	}
	if at < model.lastTime {
		return RecycleResult{}, ErrClockRewound.Code
	}
	item := model.active[waybill]
	if item == nil {
		return RecycleResult{}, ErrInvalidArgument.Code
	}
	if at-item.storedAt < model.limit {
		return RecycleResult{}, ErrParcelNotTimedOut.Code
	}
	result := RecycleResult{Waybill: waybill, CellID: item.cellID}
	model.remove(item, at)
	model.lastTime = at
	return result, ""
}

func (model *naiveLocker) unlock(at int64, waybill string) (UnlockResult, string) {
	if at < 0 || waybill == "" {
		return UnlockResult{}, ErrInvalidArgument.Code
	}
	if at < model.lastTime {
		return UnlockResult{}, ErrClockRewound.Code
	}
	item := model.active[waybill]
	if item == nil {
		return UnlockResult{}, ErrInvalidArgument.Code
	}
	item.locked = false
	item.wrong = 0
	model.lastTime = at
	return UnlockResult{Waybill: waybill}, ""
}

func validParcelForModel(parcel Parcel) bool {
	return parcel.Waybill != "" && parcel.Size >= Small && parcel.Size <= Large && isMainlandMobile(parcel.Phone)
}

func normalizedSnapshot(snapshot Snapshot) []ParcelSnapshot {
	sort.Slice(snapshot.Active, func(i, j int) bool {
		return snapshot.Active[i].Waybill < snapshot.Active[j].Waybill
	})
	return snapshot.Active
}

func TestRandomSequenceMatchesNaiveModel(t *testing.T) {
	cfg := testConfig()
	cfg.WrongCodeLimit = 3
	cfg.StorageLimit = 45
	cfg.CodeCooldown = 5
	cfg.FeePolicy = FeePolicy{FreeDuration: 8, Period: 4, PeriodFee: 2, MaximumFee: 8}

	for seed := int64(1); seed <= 300; seed++ {
		random := rand.New(rand.NewSource(seed))
		locker, err := NewLocker(cfg)
		if err != nil {
			t.Fatal(err)
		}
		model := newNaiveLocker(cfg)
		time := int64(0)
		phones := []string{"13800001234", "13900009999", "13700004321"}

		for step := 0; step < 90; step++ {
			time += int64(random.Intn(7))
			action := random.Intn(10)
			waybill := fmt.Sprintf("w%d", random.Intn(8))
			phone := phones[random.Intn(len(phones))]
			switch action {
			case 0, 1, 2:
				parcel := Parcel{Waybill: waybill, Phone: phone, Size: Size(random.Intn(3) + 1)}
				got, gotErr := locker.Deposit(time, parcel)
				want, wantCode := model.deposit(time, parcel)
				decisionLog(t, "seed=%d step=%d input=deposit at=%d parcel=%+v output=%+v err=%v expected=%s basis=%s", seed, step, time, parcel, got, gotErr, wantCode, "linear scan model comparison")
				compareError(t, seed, step, "deposit", gotErr, wantCode)
				compareValue(t, seed, step, "deposit", got, want)
			case 3, 4:
				code := fmt.Sprintf("%d", random.Intn(6))
				got, gotErr := locker.Pickup(time, code, phone)
				want, wantCode := model.pickup(time, code, phone)
				decisionLog(t, "seed=%d step=%d input=pickup at=%d code=%s phone=%s output=%+v err=%v expected=%s basis=%s", seed, step, time, code, phone, got, gotErr, wantCode, "priority order and side-effect comparison")
				compareError(t, seed, step, "pickup", gotErr, wantCode)
				compareValue(t, seed, step, "pickup", got, want)
			case 5:
				amount := int64(random.Intn(11))
				got, gotErr := locker.Pay(time, waybill, amount)
				want, wantCode := model.pay(time, waybill, amount)
				decisionLog(t, "seed=%d step=%d input=pay at=%d waybill=%s amount=%d output=%+v err=%v expected=%s basis=%s", seed, step, time, waybill, amount, got, gotErr, wantCode, "current fee schedule comparison")
				compareError(t, seed, step, "pay", gotErr, wantCode)
				compareValue(t, seed, step, "pay", got, want)
			case 6:
				got, gotErr := locker.Recycle(time, waybill)
				want, wantCode := model.recycle(time, waybill)
				decisionLog(t, "seed=%d step=%d input=recycle at=%d waybill=%s output=%+v err=%v expected=%s basis=%s", seed, step, time, waybill, got, gotErr, wantCode, "timeout-only recycle comparison")
				compareError(t, seed, step, "recycle", gotErr, wantCode)
				compareValue(t, seed, step, "recycle", got, want)
			case 7:
				got, gotErr := locker.Unlock(time, waybill)
				want, wantCode := model.unlock(time, waybill)
				decisionLog(t, "seed=%d step=%d input=unlock at=%d waybill=%s output=%+v err=%v expected=%s basis=%s", seed, step, time, waybill, got, gotErr, wantCode, "operator unlock comparison")
				compareError(t, seed, step, "unlock", gotErr, wantCode)
				compareValue(t, seed, step, "unlock", got, want)
			default:
				if random.Intn(4) == 0 {
					time -= int64(random.Intn(8))
					if time < 0 {
						time = 0
					}
				}
				parcel := Parcel{Waybill: "", Phone: "bad", Size: 99}
				if random.Intn(2) == 0 {
					parcel = Parcel{Waybill: waybill, Phone: phone, Size: Size(random.Intn(3) + 1)}
				}
				_, gotErr := locker.Deposit(time, parcel)
				_, wantCode := model.deposit(time, parcel)
				decisionLog(t, "seed=%d step=%d input=invalid-deposit at=%d parcel=%+v err=%v expected=%s basis=%s", seed, step, time, parcel, gotErr, wantCode, "validation precedes clock")
				compareError(t, seed, step, "invalid-deposit", gotErr, wantCode)
			}

			actual := normalizedSnapshot(locker.Snapshot())
			modelSnapshot := Snapshot{LastTime: model.lastTime, Active: make([]ParcelSnapshot, 0, len(model.active))}
			for _, item := range model.active {
				modelSnapshot.Active = append(modelSnapshot.Active, ParcelSnapshot{
					Waybill: item.parcel.Waybill, Phone: item.parcel.Phone, Size: item.parcel.Size,
					CellID: item.cellID, Code: item.code, StoredAt: item.storedAt, PaidFee: item.paidFee,
					WrongHits: item.wrong, Locked: item.locked,
				})
			}
			expected := normalizedSnapshot(modelSnapshot)
			compareValue(t, seed, step, "snapshot", Snapshot{LastTime: locker.Snapshot().LastTime, Active: actual}, Snapshot{LastTime: modelSnapshot.LastTime, Active: expected})
		}
	}
}

func compareError(t *testing.T, seed int64, step int, action string, err error, want string) {
	t.Helper()
	got := ""
	if err != nil {
		got = errorCode(t, err)
	}
	if got != want {
		t.Fatalf("seed=%d step=%d action=%s error=%q want=%q", seed, step, action, got, want)
	}
}

func compareValue(t *testing.T, seed int64, step int, action string, got, want any) {
	t.Helper()
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("seed=%d step=%d action=%s got=%v want=%v", seed, step, action, got, want)
	}
}
