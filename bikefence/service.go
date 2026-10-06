package bikefence

import (
	"fmt"
	"io"
	"sync"
	"time"
)

// ErrorKind 精确定义每一类业务错误，调用方可据此区分。
type ErrorKind int

const (
	ErrInvalidArgument   ErrorKind = iota // 参数非法
	ErrClockBackwards                     // 时钟回退
	ErrFenceNotFound                      // 围栏不存在
	ErrFenceConstraint                    // 围栏约束不合法
	ErrBikeNotFound                       // 车辆不存在
	ErrBikeNotRiding                      // 车辆不在骑行中
	ErrNoParkingReturn                    // 禁停区还车
	ErrFenceFull                          // 围栏已满
	ErrTaskNotFound                       // 任务不存在
	ErrTaskClaimed                        // 任务已被认领
	ErrTaskNotInProgress                  // 任务不在处理中
	ErrIllegalDropoff                     // 落点不合法
)

// Error 携带错误类别，便于精确分支处理。
type Error struct {
	Kind ErrorKind
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func errf(k ErrorKind, format string, args ...any) error {
	return &Error{Kind: k, Msg: fmt.Sprintf(format, args...)}
}

type TaskKind int

const (
	TaskOutside  TaskKind = iota // 区外还车：搬回默认运营区
	TaskEvacuate                 // 围栏达阈值：疏散到同运营区内最空闲围栏
)

type TaskStatus int

const (
	TaskPending TaskStatus = iota
	TaskInProgress
	TaskDone
)

// ClaimRecord 保留每一次认领记录，超时释放也不删除。
type ClaimRecord struct {
	WorkerID  string
	ClaimedAt int64
}

// Task 是一条可复现的调度任务。
type Task struct {
	ID          string
	Kind        TaskKind
	BikeID      string
	FromFenceID string
	ToFenceID   string
	Status      TaskStatus
	ClaimedBy   string
	ClaimedAt   int64
	CreatedAt   int64
	CompletedAt int64
	Claims      []ClaimRecord
}

// Bike 是车辆状态。
type Bike struct {
	ID      string
	Riding  bool
	FenceID string
}

// Config 是服务配置。
type Config struct {
	Timezone           *time.Location
	EvacNumerator      int
	EvacDenominator    int
	ClaimTimeout       time.Duration
	DefaultOperatingID string
	OutsideFee         int64
	RewardAmount       int64
	Log                io.Writer
}

// Receipt 是一次还车的可复现结果。
type Receipt struct {
	BikeID        string
	UserID        string
	At            int64
	Point         Point
	FenceID       string
	Outside       bool
	Accepted      bool
	Fee           int64
	Reward        int64
	RewardGranted bool
	TaskID        string
	Reason        string
}

// LocateResult 是归属查询结果。
type LocateResult struct {
	FenceID      string
	Outside      bool
	Reason       string
	NodesVisited int
}

type serviceCore struct {
	cfg        Config
	fences     map[string]*Fence
	order      []string
	bikes      map[string]*Bike
	tasks      map[string]*Task
	taskOrder  []string
	counts     map[string]int
	parent     map[string]string
	children   map[string][]string
	activeEvac map[string]string
	rewardDays map[string]bool
	lastTime   int64
	seq        int
	tree       *RTree
}

// Service 并发安全；单把互斥锁使全部操作等价于某个串行顺序。
type Service struct {
	mu sync.Mutex
	c  serviceCore
}

// New 创建服务；配置非法时返回参数错误。
func New(cfg Config) (*Service, error) {
	if cfg.Timezone == nil {
		cfg.Timezone = time.UTC
	}
	if cfg.EvacNumerator <= 0 || cfg.EvacDenominator <= 0 ||
		cfg.EvacNumerator > cfg.EvacDenominator || cfg.ClaimTimeout <= 0 ||
		cfg.DefaultOperatingID == "" {
		return nil, errf(ErrInvalidArgument, "invalid config")
	}
	s := &Service{}
	s.c = serviceCore{
		cfg:        cfg,
		fences:     map[string]*Fence{},
		bikes:      map[string]*Bike{},
		tasks:      map[string]*Task{},
		counts:     map[string]int{},
		parent:     map[string]string{},
		children:   map[string][]string{},
		activeEvac: map[string]string{},
		rewardDays: map[string]bool{},
		tree:       BuildRTree(nil),
	}
	return s, nil
}

func (c *serviceCore) logf(format string, args ...any) {
	if c.cfg.Log != nil {
		fmt.Fprintf(c.cfg.Log, format+"\n", args...)
	}
}

func (c *serviceCore) checkClock(ts int64) error {
	if ts < c.lastTime {
		return errf(ErrClockBackwards, "clock backwards: %d < %d", ts, c.lastTime)
	}
	return nil
}

// sweepExpired 在锁内、时钟校验后调用：认领超时任务回到待认领，认领记录保留。
func (c *serviceCore) sweepExpired(now int64) {
	limit := int64(c.cfg.ClaimTimeout / time.Millisecond)
	for _, id := range c.taskOrder {
		t := c.tasks[id]
		if t.Status == TaskInProgress && t.ClaimedAt > 0 && now-t.ClaimedAt >= limit {
			t.Status = TaskPending
			t.ClaimedBy = ""
			t.ClaimedAt = 0
			c.logf("[sweep] task %s claim timed out at %d, back to pending (claims kept: %d)",
				t.ID, now, len(t.Claims))
		}
	}
}

func validID(id string) bool { return id != "" }

func validPolygon(vs []Point) bool {
	if len(vs) < 3 {
		return false
	}
	n := len(vs)
	for i := 0; i < n; i++ {
		if vs[i] == vs[(i+1)%n] {
			return false
		}
	}
	return true
}

func (c *serviceCore) rebuildIndex() {
	fs := make([]*Fence, 0, len(c.order))
	for _, id := range c.order {
		fs = append(fs, c.fences[id])
	}
	c.tree = BuildRTree(fs)
}

func selfIntersects(vs []Point) bool {
	n := len(vs)
	for i := 0; i < n; i++ {
		a, b := vs[i], vs[(i+1)%n]
		for j := i + 1; j < n; j++ {
			if j == i || (j+1)%n == i {
				continue
			}
			d, e := vs[j], vs[(j+1)%n]
			if d == a || d == b || e == a || e == b {
				continue
			}
			if segmentsIntersect(a, b, d, e) {
				return true
			}
		}
	}
	return false
}

// RegisterFence 登记围栏并校验全部空间约束；成功后重建空间索引。
func (s *Service) RegisterFence(id string, kind FenceKind, vertices []Point, capacity int, at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := &s.c
	if !validID(id) || capacity <= 0 || !validPolygon(vertices) ||
		kind < KindOperating || kind > KindReward {
		return errf(ErrInvalidArgument, "bad fence parameters")
	}
	if _, dup := c.fences[id]; dup {
		return errf(ErrInvalidArgument, "duplicate fence id: %s", id)
	}
	if err := c.checkClock(at); err != nil {
		return err
	}

	nf := &Fence{ID: id, Kind: kind, Vertices: append([]Point(nil), vertices...), Capacity: capacity}
	nf.bbox = bboxOf(nf.Vertices)

	if selfIntersects(nf.Vertices) {
		return errf(ErrFenceConstraint, "fence %s is self-intersecting", id)
	}

	containedIn := ""
	for _, eid := range c.order {
		ex := c.fences[eid]
		hit := polygonsIntersect(nf.Vertices, ex.Vertices)
		switch {
		case kind == KindOperating:
			if hit {
				return errf(ErrFenceConstraint, "operating fence %s intersects %s", id, eid)
			}
		case ex.Kind == KindOperating:
			if hit {
				if !polygonContains(ex.Vertices, nf.Vertices) {
					return errf(ErrFenceConstraint, "fence %s not wholly inside operating %s", id, eid)
				}
				if containedIn != "" {
					return errf(ErrFenceConstraint, "fence %s inside multiple operating areas", id)
				}
				containedIn = eid
			}
		default:
			if hit {
				return errf(ErrFenceConstraint, "fence %s intersects fence %s", id, eid)
			}
		}
	}
	if kind != KindOperating && containedIn == "" {
		return errf(ErrFenceConstraint, "fence %s is not inside any operating area", id)
	}

	c.fences[id] = nf
	c.order = append(c.order, id)
	c.counts[id] = 0
	if kind != KindOperating {
		c.parent[id] = containedIn
		c.children[containedIn] = append(c.children[containedIn], id)
	}
	c.rebuildIndex()
	c.lastTime = at
	c.logf("[register] fence=%s kind=%s cap=%d at=%d -> accepted", id, kind, capacity, at)
	return nil
}

// RegisterBike 登记一辆初始处于骑行中的车辆。
func (s *Service) RegisterBike(id string, at int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := &s.c
	if !validID(id) {
		return errf(ErrInvalidArgument, "bad bike id")
	}
	if _, dup := c.bikes[id]; dup {
		return errf(ErrInvalidArgument, "duplicate bike id: %s", id)
	}
	if err := c.checkClock(at); err != nil {
		return err
	}
	c.bikes[id] = &Bike{ID: id, Riding: true}
	c.lastTime = at
	c.logf("[register-bike] bike=%s at=%d -> accepted (riding)", id, at)
	return nil
}

// classify 按 奖励区 > 禁停区 > 运营区 判定点归属。
func (c *serviceCore) classify(p Point) (*Fence, string, int) {
	cands, visited := c.tree.PointQueryStats(p)
	var reward, noPark, op *Fence
	for _, f := range cands {
		if !pointInPolygon(f.Vertices, p) {
			continue
		}
		switch f.Kind {
		case KindReward:
			reward = f
		case KindNoParking:
			noPark = f
		case KindOperating:
			op = f
		}
	}
	switch {
	case reward != nil:
		return reward, fmt.Sprintf("point (%d,%d) inside reward %s", p.X, p.Y, reward.ID), visited
	case noPark != nil:
		return noPark, fmt.Sprintf("point (%d,%d) inside no-parking %s", p.X, p.Y, noPark.ID), visited
	case op != nil:
		return op, fmt.Sprintf("point (%d,%d) inside operating %s", p.X, p.Y, op.ID), visited
	default:
		return nil, fmt.Sprintf("point (%d,%d) outside all fences; rtree nodes=%d", p.X, p.Y, visited), visited
	}
}

// Locate 查询点归属，返回判定依据与 R 树访问节点数。
func (s *Service) Locate(p Point, at int64) (LocateResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := &s.c
	if err := c.checkClock(at); err != nil {
		return LocateResult{}, err
	}
	f, reason, visited := c.classify(p)
	res := LocateResult{Reason: reason, NodesVisited: visited}
	if f == nil {
		res.Outside = true
	} else {
		res.FenceID = f.ID
	}
	c.logf("[locate] p=(%d,%d) at=%d -> fence=%q outside=%v nodes=%d",
		p.X, p.Y, at, res.FenceID, res.Outside, visited)
	return res, nil
}

// FenceCount 以 O(1)（不随车辆总数增长）返回围栏内当前车辆数。
func (s *Service) FenceCount(fenceID string, at int64) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := &s.c
	if !validID(fenceID) {
		return 0, errf(ErrInvalidArgument, "bad fence id")
	}
	if err := c.checkClock(at); err != nil {
		return 0, err
	}
	f, ok := c.fences[fenceID]
	if !ok {
		return 0, errf(ErrFenceNotFound, "fence not found: %s", fenceID)
	}
	return c.counts[f.ID], nil
}
