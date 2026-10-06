package smartlocker

import (
	"fmt"
	"strings"
	"sync"
)

type storedParcel struct {
	parcel   Parcel
	cellID   string
	code     string
	storedAt int64
	paidFee  int64
	wrong    int
	locked   bool
}

type Locker struct {
	mu       sync.Mutex
	cfg      Config
	cells    *cellAllocator
	codes    *codePool
	active   map[string]*storedParcel
	byCode   map[string]*storedParcel
	lastTime int64
	deposits int64
}

func NewLocker(cfg Config) (*Locker, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	if cfg.WrongCodeLimit == 0 {
		cfg.WrongCodeLimit = 3
	}
	return &Locker{
		cfg:    cfg,
		cells:  newCellAllocator(cfg.Cells),
		codes:  newCodePool(),
		active: make(map[string]*storedParcel),
		byCode: make(map[string]*storedParcel),
	}, nil
}

func (locker *Locker) Deposit(at int64, parcel Parcel) (DepositResult, error) {
	locker.mu.Lock()
	defer locker.mu.Unlock()

	if err := validateParcel(parcel); err != nil {
		locker.log("deposit reject", "at", at, "parcel", parcel, "reason", err)
		return DepositResult{}, err
	}
	if err := locker.checkClock(at); err != nil {
		locker.log("deposit reject", "at", at, "parcel", parcel, "reason", err)
		return DepositResult{}, err
	}
	if _, exists := locker.active[parcel.Waybill]; exists {
		locker.log("deposit reject", "at", at, "waybill", parcel.Waybill, "reason", ErrDuplicateWaybill)
		return DepositResult{}, ErrDuplicateWaybill
	}
	if !locker.cells.hasCompatible(parcel.Size) {
		locker.log("deposit reject", "at", at, "parcel", parcel, "reason", ErrNoCompatibleCell)
		return DepositResult{}, ErrNoCompatibleCell
	}
	cellID, ok := locker.cells.allocate(parcel.Size)
	if !ok {
		locker.log("deposit reject", "at", at, "parcel", parcel, "reason", ErrAllFitCellsOccupied)
		return DepositResult{}, ErrAllFitCellsOccupied
	}
	code := locker.codes.allocate(at)
	item := &storedParcel{
		parcel:   parcel,
		cellID:   cellID,
		code:     code,
		storedAt: at,
	}
	locker.active[parcel.Waybill] = item
	locker.byCode[code] = item
	locker.lastTime = at
	locker.deposits++
	locker.log("deposit accept", "at", at, "parcel", parcel, "cell", cellID, "code", code, "basis", "smallest compatible free cell")
	return DepositResult{Code: code, CellID: cellID}, nil
}

func (locker *Locker) Pickup(at int64, code, phone string) (PickupResult, error) {
	locker.mu.Lock()
	defer locker.mu.Unlock()

	if at < 0 || code == "" || !isMainlandMobile(phone) {
		locker.log("pickup reject", "at", at, "code", code, "phone", phone, "reason", ErrInvalidArgument)
		return PickupResult{}, ErrInvalidArgument
	}
	if err := locker.checkClock(at); err != nil {
		locker.log("pickup reject", "at", at, "code", code, "reason", err)
		return PickupResult{}, err
	}
	item := locker.byCode[code]
	if item == nil {
		locker.log("pickup reject", "at", at, "code", code, "reason", ErrCodeNotFound)
		return PickupResult{}, ErrCodeNotFound
	}
	if item.locked {
		locker.log("pickup reject", "at", at, "code", code, "waybill", item.parcel.Waybill, "reason", ErrParcelLocked)
		return PickupResult{}, ErrParcelLocked
	}
	if phoneSuffix(item.parcel.Phone) != phoneSuffix(phone) {
		item.wrong++
		basis := fmt.Sprintf("wrong phone attempt %d", item.wrong)
		if item.wrong >= locker.cfg.WrongCodeLimit {
			item.locked = true
			basis += "; parcel locked"
		}
		locker.log("pickup reject phone mismatch", "at", at, "code", code, "waybill", item.parcel.Waybill, "basis", basis, "clockAdvanced", false)
		return PickupResult{}, ErrPhoneMismatch
	}
	if locker.timedOut(item, at) {
		locker.log("pickup reject", "at", at, "code", code, "waybill", item.parcel.Waybill, "reason", ErrParcelTimedOut)
		return PickupResult{}, ErrParcelTimedOut
	}
	due := locker.due(item, at)
	if due > 0 {
		locker.log("pickup reject", "at", at, "code", code, "waybill", item.parcel.Waybill, "due", due, "reason", ErrFeeDue)
		return PickupResult{}, ErrFeeDue
	}

	paid := item.paidFee
	waybill := item.parcel.Waybill
	cellID := item.cellID
	locker.removeParcel(item, at)
	locker.lastTime = at
	locker.log("pickup accept", "at", at, "code", code, "waybill", waybill, "cell", cellID, "paidFee", paid, "basis", "identity verified and fee settled")
	return PickupResult{Waybill: waybill, CellID: cellID, FeePaid: paid}, nil
}

func (locker *Locker) Pay(at int64, waybill string, amount int64) (PaymentResult, error) {
	locker.mu.Lock()
	defer locker.mu.Unlock()

	if at < 0 || waybill == "" || amount < 0 {
		locker.log("pay reject", "at", at, "waybill", waybill, "amount", amount, "reason", ErrInvalidArgument)
		return PaymentResult{}, ErrInvalidArgument
	}
	if err := locker.checkClock(at); err != nil {
		locker.log("pay reject", "at", at, "waybill", waybill, "reason", err)
		return PaymentResult{}, err
	}
	item := locker.active[waybill]
	if item == nil {
		err := fmt.Errorf("%w: 运单不存在", ErrInvalidArgument)
		locker.log("pay reject", "at", at, "waybill", waybill, "reason", err)
		return PaymentResult{}, err
	}
	due := locker.due(item, at)
	if amount != due {
		locker.log("pay reject", "at", at, "waybill", waybill, "amount", amount, "due", due, "reason", ErrWrongPaymentAmount)
		return PaymentResult{}, ErrWrongPaymentAmount
	}
	item.paidFee += amount
	locker.lastTime = at
	locker.log("pay accept", "at", at, "waybill", waybill, "amount", amount, "totalPaid", item.paidFee)
	return PaymentResult{Paid: item.paidFee, Due: due}, nil
}

func (locker *Locker) Recycle(at int64, waybill string) (RecycleResult, error) {
	locker.mu.Lock()
	defer locker.mu.Unlock()

	if at < 0 || waybill == "" {
		locker.log("recycle reject", "at", at, "waybill", waybill, "reason", ErrInvalidArgument)
		return RecycleResult{}, ErrInvalidArgument
	}
	if err := locker.checkClock(at); err != nil {
		locker.log("recycle reject", "at", at, "waybill", waybill, "reason", err)
		return RecycleResult{}, err
	}
	item := locker.active[waybill]
	if item == nil {
		err := fmt.Errorf("%w: 运单不存在", ErrInvalidArgument)
		locker.log("recycle reject", "at", at, "waybill", waybill, "reason", err)
		return RecycleResult{}, err
	}
	if !locker.timedOut(item, at) {
		locker.log("recycle reject", "at", at, "waybill", waybill, "reason", ErrParcelNotTimedOut)
		return RecycleResult{}, ErrParcelNotTimedOut
	}
	cellID := item.cellID
	locker.removeParcel(item, at)
	locker.lastTime = at
	locker.log("recycle accept", "at", at, "waybill", waybill, "cell", cellID, "basis", "storage limit reached")
	return RecycleResult{Waybill: waybill, CellID: cellID}, nil
}

func (locker *Locker) Unlock(at int64, waybill string) (UnlockResult, error) {
	locker.mu.Lock()
	defer locker.mu.Unlock()

	if at < 0 || waybill == "" {
		locker.log("unlock reject", "at", at, "waybill", waybill, "reason", ErrInvalidArgument)
		return UnlockResult{}, ErrInvalidArgument
	}
	if err := locker.checkClock(at); err != nil {
		locker.log("unlock reject", "at", at, "waybill", waybill, "reason", err)
		return UnlockResult{}, err
	}
	item := locker.active[waybill]
	if item == nil {
		err := fmt.Errorf("%w: 运单不存在", ErrInvalidArgument)
		locker.log("unlock reject", "at", at, "waybill", waybill, "reason", err)
		return UnlockResult{}, err
	}
	item.locked = false
	item.wrong = 0
	locker.lastTime = at
	locker.log("unlock accept", "at", at, "waybill", waybill, "basis", "wrong attempts reset")
	return UnlockResult{Waybill: waybill}, nil
}

func (locker *Locker) Snapshot() Snapshot {
	locker.mu.Lock()
	defer locker.mu.Unlock()

	result := Snapshot{LastTime: locker.lastTime, Active: make([]ParcelSnapshot, 0, len(locker.active))}
	for _, item := range locker.active {
		result.Active = append(result.Active, ParcelSnapshot{
			Waybill:   item.parcel.Waybill,
			Phone:     item.parcel.Phone,
			Size:      item.parcel.Size,
			CellID:    item.cellID,
			Code:      item.code,
			StoredAt:  item.storedAt,
			PaidFee:   item.paidFee,
			WrongHits: item.wrong,
			Locked:    item.locked,
		})
	}
	return result
}

func (locker *Locker) AllocationStats() AllocationStats {
	locker.mu.Lock()
	defer locker.mu.Unlock()
	return AllocationStats{
		Deposits:          locker.deposits,
		LastFindProbes:    locker.cells.lastProbes,
		TotalFindProbes:   locker.cells.totalProbes,
		MaximumFindProbes: locker.cells.maxProbes,
	}
}

func (locker *Locker) removeParcel(item *storedParcel, at int64) {
	locker.cells.release(item.cellID)
	locker.codes.release(item.code, at, locker.cfg.CodeCooldown)
	delete(locker.byCode, item.code)
	delete(locker.active, item.parcel.Waybill)
}

func (locker *Locker) checkClock(at int64) error {
	if at < locker.lastTime {
		return ErrClockRewound
	}
	return nil
}

func (locker *Locker) timedOut(item *storedParcel, at int64) bool {
	return at-item.storedAt >= locker.cfg.StorageLimit
}

func (locker *Locker) due(item *storedParcel, at int64) int64 {
	return feeDue(locker.cfg.FeePolicy, item.storedAt, at, item.paidFee)
}

func (locker *Locker) log(message string, fields ...any) {
	if locker.cfg.Logger == nil {
		return
	}
	var builder strings.Builder
	builder.WriteString(message)
	for index := 0; index+1 < len(fields); index += 2 {
		fmt.Fprintf(&builder, " %v=%v", fields[index], fields[index+1])
	}
	locker.cfg.Logger.Printf("%s", builder.String())
}

func validateConfig(cfg Config) error {
	if len(cfg.Cells) == 0 {
		return fmt.Errorf("%w: 至少需要一个格口", ErrInvalidArgument)
	}
	for id, size := range cfg.Cells {
		if id == "" || size < Small || size > Large {
			return fmt.Errorf("%w: 格口配置非法", ErrInvalidArgument)
		}
	}
	if cfg.FeePolicy.Period <= 0 || cfg.FeePolicy.FreeDuration < 0 || cfg.FeePolicy.PeriodFee < 0 || cfg.FeePolicy.MaximumFee < 0 {
		return fmt.Errorf("%w: 计费配置非法", ErrInvalidArgument)
	}
	if cfg.StorageLimit < 0 || cfg.CodeCooldown < 0 {
		return fmt.Errorf("%w: 时长配置非法", ErrInvalidArgument)
	}
	return nil
}

func validateParcel(parcel Parcel) error {
	if parcel.Waybill == "" || parcel.Size < Small || parcel.Size > Large || !isMainlandMobile(parcel.Phone) {
		return ErrInvalidArgument
	}
	return nil
}

func isMainlandMobile(phone string) bool {
	if len(phone) != 11 || phone[0] != '1' {
		return false
	}
	for index := 1; index < len(phone); index++ {
		if !isDigit(phone[index]) {
			return false
		}
	}
	return true
}

func phoneSuffix(phone string) string {
	if len(phone) < 4 {
		return phone
	}
	return phone[len(phone)-4:]
}
