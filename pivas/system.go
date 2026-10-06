package pivas

import "sync"

// Storage 表示配置完成后的存放方式。
type Storage int

const (
	Room Storage = iota // 室温
	Cold                // 冷藏（必须冷藏运送）
)

// Bench 是一台洁净台。
type Bench struct {
	ID       string // 非空
	Capacity int    // 单批容量（正整数）
	ClearGap int    // 相邻批次清场间隔秒（正整数）
}

// OrderInput 是一张待受理的配置医嘱。
type OrderInput struct {
	ID      string   // 非空，且不得与既有医嘱重复
	Drugs   []string // 1..6 种药品
	Solvent string   // 所选溶媒类别
	DueAt   int      // 要求送达时刻
	Urgent  bool     // 紧急标志
}

// Order 是已受理医嘱及其受理时目录快照相关的派生数据。
type Order struct {
	ID         string
	Drugs      []string
	Solvent    string
	DueAt      int
	Urgent     bool
	roomStable int
	coldStable int
	cover      bool // 整袋是否须避光外袋
	storage    Storage
	batchID    string
	acceptedAt int
	catVersion int64 // 受理时目录版本
}

// batch 是洁净台上的一批配置任务。
type batch struct {
	id      string
	benchID string
	solvent string
	cover   bool
	start   int
	dur     int
	orders  []*Order // 按加入顺序
}

// benchState 是洁净台的运行态。
type benchState struct {
	bench Bench
	// batches 为该台按 start 升序的全部批次（含已开始的历史批次）。
	batches []*batch
	// head 指向第一个可能尚未开始的批次；其之前的均为历史批次，
	// 受理时整体跳过，使受理开销只与当前未开始批次数相关。
	head int
}

// System 是排程与稳定期判定系统。所有方法并发安全，
// 其语义等价于按调用完成顺序串行执行。
type System struct {
	mu sync.Mutex

	now      int
	catalog  *catalog
	benches  map[string]*benchState
	benchIDs []string // 登记顺序，稳定遍历

	orders map[string]*Order

	// batchByID 提供批次 ID 到批次的 O(1) 反查，避免查询/取消时扫描历史批次。
	batchByID map[string]*batch
	// batchBench 提供批次到所属台编号的 O(1) 反查。
	batchBench map[string]string

	// durations[n] 为医嘱数量为 n（1..cap）时的配置时长，
	// 登记时要求随 n 严格递增。
	durations map[int]int

	// 运送时长按存放方式配置。
	transport map[Storage]int

	nextBatchSeq int
}

// New 创建空系统。
func New() *System {
	return &System{
		catalog:    newCatalog(),
		benches:    make(map[string]*benchState),
		orders:     make(map[string]*Order),
		batchByID:  make(map[string]*batch),
		batchBench: make(map[string]string),
		durations:  make(map[int]int),
		transport:  make(map[Storage]int),
	}
}

// checkClock 校验时钟单调。调用方须持锁。
func (s *System) checkClock(now int) error {
	if now < s.now {
		return errf(ErrClockRollback, "now=%d < last accepted now=%d", now, s.now)
	}
	return nil
}

// OrderInfo 是对外的查询结果。
type OrderInfo struct {
	OrderID    string
	Storage    Storage
	BenchID    string
	BatchID    string
	BatchStart int
	ReadyAt    int // 配置完成时刻
	ExpireAt   int // 有效期截止（恰到期视为已失效）
	DeliverAt  int // 送达时刻
	OnTime     bool
	Covered    bool
}
