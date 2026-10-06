// Package bus 实现一条公交线路的车辆排班、串车识别与干预（扣车 / 跳站 / 备车插入），
// 并在同一服务内强制司机连续在岗工时约束。
//
// 所有时间均为 int64 秒；车次号为正整数，车次号先后即计划发车次序。
// 服务内部用锁串行化所有写操作，并发调用等价于某个确定的串行顺序。
package bus

import (
	"errors"
	"math/big"
	"sort"
	"sync"
)

// 可区分的错误类别。
var (
	ErrInvalidArgument = errors.New("bus: 参数非法")
	ErrClockRollback   = errors.New("bus: 时钟回退")
	ErrTripNotFound    = errors.New("bus: 车次不存在")
	ErrStationNotFound = errors.New("bus: 站点不存在")
	ErrOutOfOrder      = errors.New("bus: 上报乱序")
	ErrDuplicateReport = errors.New("bus: 重复上报")
	ErrNotBunched      = errors.New("bus: 干预对象不是串车")
	ErrDriverNotFound  = errors.New("bus: 司机不存在")
)

// Verdict 是车辆到达控制站时的判定结论。
type Verdict string

const (
	VerdictNone   Verdict = ""       // 非控制站或尚未判定
	VerdictNormal Verdict = "normal" // 非串车、也无大间隔
	VerdictBunch  Verdict = "bunched"
	VerdictGap    Verdict = "big-gap"
)

// Action 是某站最终生效的干预类别。
type Action string

const (
	ActionNone   Action = "none"
	ActionHold   Action = "hold"
	ActionSkip   Action = "skip"
	ActionInsert Action = "insert"
)

// Reason 是干预被降级 / 未完全生效的可查询原因。
type Reason string

const (
	ReasonNone          Reason = ""
	ReasonHoldCap       Reason = "hold-capped"
	ReasonDutyLimit     Reason = "duty-limit"
	ReasonLateReject    Reason = "late-departure"
	ReasonAlightRequest Reason = "alight-request"
	ReasonSkipUsed      Reason = "skip-already-used"
	ReasonNoNextControl Reason = "no-next-control"
	ReasonPoolEmpty     Reason = "pool-empty"
)

// Config 是线路与方案参数，所有时长均为正整数秒。
type Config struct {
	StationCount       int
	ControlStations    []int
	TargetHeadway      int64
	Dwells             []int64
	TravelTimes        []int64
	HoldCap            int64
	DepartureTolerance int64
	MaxDutySeconds     int64
}

// frac 是不可约分的正有理数次序键，用 big.Int 防止深度插入时溢出。
// 已发车车次的相对次序永不变更；新车次取相邻两键的中位分数 (a+c)/(b+d)。
type frac struct {
	n *big.Int
	d *big.Int
}

func fracInt(x int64) frac {
	return frac{n: big.NewInt(x), d: big.NewInt(1)}
}

func mediant(a, b frac) frac {
	return frac{n: new(big.Int).Add(a.n, b.n), d: new(big.Int).Add(a.d, b.d)}
}

func cmpFrac(a, b frac) int {
	return new(big.Int).Mul(a.n, b.d).Cmp(new(big.Int).Mul(b.n, a.d))
}

type record struct {
	arrival   int64
	departure int64
	finalized bool
	reported  bool
	autoBorn  bool
	skipped   bool

	bunched bool
	gap     bool
	predID  int64

	action  Action
	reason  Reason
	holdSec int64
	insTrip int64
}

type trip struct {
	id     int64
	driver int64
	key    frac

	base  []int64 // 各站计划到站（未受扣车影响），插入点之前为 -1
	holds map[int]int64

	insertedAt int
	skipFrom   int
	skipTo     int
	skipUsed   bool

	dutyStart  int64
	lastReport int
	lastFinal  int // 离站时刻已最终确定的最大站下标，-1 表示尚无
}

// Service 是排班与干预服务。
type Service struct {
	mu      sync.RWMutex
	cfg     Config
	control []bool

	drivers    map[int64]struct{}
	trips      map[int64]*trip
	pool       []int64
	poolDriver map[int64]int64

	orderKeys  []frac
	orderTrips []*trip

	recs map[[2]int64]*record
	reqs map[[2]int64]struct{}

	clock    int64
	lastShed int64
}

// New 构造服务并校验方案参数。
func New(cfg Config) (*Service, error) {
	n := cfg.StationCount
	if n < 2 || cfg.TargetHeadway <= 0 || cfg.HoldCap <= 0 ||
		cfg.DepartureTolerance <= 0 || cfg.MaxDutySeconds <= 0 {
		return nil, ErrInvalidArgument
	}
	if len(cfg.Dwells) != n || len(cfg.TravelTimes) != n-1 {
		return nil, ErrInvalidArgument
	}
	for _, d := range cfg.Dwells {
		if d <= 0 {
			return nil, ErrInvalidArgument
		}
	}
	for _, d := range cfg.TravelTimes {
		if d <= 0 {
			return nil, ErrInvalidArgument
		}
	}
	ctl := make([]bool, n)
	prev := -1
	for _, st := range cfg.ControlStations {
		if st < 0 || st >= n || st <= prev || ctl[st] {
			return nil, ErrInvalidArgument
		}
		ctl[st] = true
		prev = st
	}
	return &Service{
		cfg:        cfg,
		control:    ctl,
		drivers:    map[int64]struct{}{},
		trips:      map[int64]*trip{},
		poolDriver: map[int64]int64{},
		recs:       map[[2]int64]*record{},
		reqs:       map[[2]int64]struct{}{},
		clock:      0,
		lastShed:   0,
	}, nil
}

// Clock 返回最近一次被接受操作的时刻。
func (s *Service) Clock() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.clock
}

// RegisterDriver 登记一名可绑定车辆的司机。
func (s *Service) RegisterDriver(id int64) error {
	if id <= 0 {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drivers[id] = struct{}{}
	return nil
}

// AddBackup 把一辆带司机的备车放入备车池（此刻尚不在运行序列中）。
func (s *Service) AddBackup(tripID, driverID, at int64) error {
	if tripID <= 0 || driverID <= 0 || at < 0 {
		return ErrInvalidArgument
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if at < s.clock {
		return ErrClockRollback
	}
	if _, ok := s.trips[tripID]; ok {
		return ErrInvalidArgument
	}
	if _, dup := s.poolDriver[tripID]; dup {
		return ErrInvalidArgument
	}
	if _, ok := s.drivers[driverID]; !ok {
		return ErrDriverNotFound
	}
	s.pool = append(s.pool, tripID)
	sort.Slice(s.pool, func(i, j int) bool { return s.pool[i] < s.pool[j] })
	s.poolDriver[tripID] = driverID
	s.clock = at
	return nil
}

// StopInfo 是某车次在某站的完整可复现信息。
type StopInfo struct {
	TripID    int64
	Station   int
	Arrival   int64
	Departure int64
	Reported  bool
	Finalized bool
	Skipped   bool
	Verdict   Verdict
	Action    Action
	Reason    Reason
	HoldSec   int64
	Inserted  int64
	PrevTrip  int64
}

// 具体操作 ScheduleTrip / ReportArrival / RegisterAlight / Intervene / Query
// 分别实现于 service.go 与 intervene.go；朴素重放模型见 model.go。
