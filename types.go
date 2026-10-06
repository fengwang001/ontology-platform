package smartlocker

type Size uint8

const (
	Small Size = iota + 1
	Medium
	Large
)

type Parcel struct {
	Waybill string
	Phone   string
	Size    Size
}

type FeePolicy struct {
	FreeDuration int64
	Period       int64
	PeriodFee    int64
	MaximumFee   int64
}

type Config struct {
	Cells          map[string]Size
	FeePolicy      FeePolicy
	StorageLimit   int64
	CodeCooldown   int64
	WrongCodeLimit int
	Logger         Logger
}

type Logger interface {
	Printf(format string, args ...any)
}

type OperationError struct {
	Code   string
	Reason string
}

func (e *OperationError) Error() string {
	return e.Reason
}

func newOperationError(code, reason string) *OperationError {
	return &OperationError{Code: code, Reason: reason}
}

var (
	ErrInvalidArgument     = newOperationError("invalid_argument", "参数非法")
	ErrClockRewound        = newOperationError("clock_rewound", "时钟回退")
	ErrDuplicateWaybill    = newOperationError("duplicate_waybill", "重复投递")
	ErrNoCompatibleCell    = newOperationError("no_compatible_cell", "柜内没有规格足够的格口")
	ErrAllFitCellsOccupied = newOperationError("all_fit_cells_occupied", "可容纳格口暂时全部占用")
	ErrCodeNotFound        = newOperationError("code_not_found", "取件码不存在或已失效")
	ErrParcelLocked        = newOperationError("parcel_locked", "快件已被锁定")
	ErrPhoneMismatch       = newOperationError("phone_mismatch", "手机号后四位不符")
	ErrParcelTimedOut      = newOperationError("parcel_timed_out", "快件已超时")
	ErrFeeDue              = newOperationError("fee_due", "有待缴滞留费")
	ErrWrongPaymentAmount  = newOperationError("wrong_payment_amount", "缴费金额不等于当前应缴金额")
	ErrParcelNotTimedOut   = newOperationError("parcel_not_timed_out", "快件尚未超时")
)

type DepositResult struct {
	Code   string
	CellID string
}

type PickupResult struct {
	Waybill string
	CellID  string
	FeePaid int64
}

type PaymentResult struct {
	Paid int64
	Due  int64
}

type RecycleResult struct {
	Waybill string
	CellID  string
}

type UnlockResult struct {
	Waybill string
}

type AllocationStats struct {
	Deposits          int64
	LastFindProbes    int64
	TotalFindProbes   int64
	MaximumFindProbes int64
}

type Snapshot struct {
	LastTime int64
	Active   []ParcelSnapshot
}

type ParcelSnapshot struct {
	Waybill   string
	Phone     string
	Size      Size
	CellID    string
	Code      string
	StoredAt  int64
	PaidFee   int64
	WrongHits int
	Locked    bool
}
