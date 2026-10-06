package taxipool

type driverState int

const (
	stateIdle       driverState = iota // 已知但不在队列/放行中
	stateQueued                        // 在某候机楼队列中
	stateDispatched                    // 已放行、等待到达或爽约处理
	stateArrived                       // 已到达上客点，可登记行程
)

func (s driverState) String() string {
	switch s {
	case stateQueued:
		return "queued"
	case stateDispatched:
		return "dispatched"
	case stateArrived:
		return "arrived"
	default:
		return "idle"
	}
}

// Voucher 为短途返回优先凭证。Strikes 记录因优先名额已满被迫普通入队的次数。
type Voucher struct {
	IssuedAt  int64
	ExpiresAt int64
	Strikes   int
}

// Trip 为最近一次载客行程，用于再次入池时判定是否发放凭证。
type Trip struct {
	Distance   int64
	DepartedAt int64
}

type Driver struct {
	id              string
	state           driverState
	terminal        string   // stateQueued 时所在候机楼
	key             entryKey // stateQueued 时的队列次序键
	deadline        int64    // stateDispatched 时的到达时限
	noShows         int
	bannedUntil     int64
	voucher         *Voucher
	pendingTrip     *Trip
	voucherDay      int64
	voucherDayCount int
}
