package ontology

import (
	"math/big"
	"sync"
)

// Reason 标识一次被整体拒绝的操作的具体原因。
type Reason string

const (
	ReasonTNotPositive      Reason = "T_not_positive"
	ReasonWNotPositive      Reason = "W_not_positive"
	ReasonFOutOfRange       Reason = "F_out_of_range"
	ReasonNegativeNow       Reason = "negative_now"
	ReasonNegativeWalPos    Reason = "negative_wal_pos"
	ReasonNegativeDirty     Reason = "negative_dirty"
	ReasonCheckpointActive  Reason = "checkpoint_active"
	ReasonNowRegression     Reason = "now_regression"
	ReasonWalPosRegression  Reason = "wal_pos_regression"
	ReasonNoCheckpoint      Reason = "no_checkpoint"
	ReasonKNotPositive      Reason = "k_not_positive"
	ReasonKExceedsRemaining Reason = "k_exceeds_remaining"
)

// Error 携带可区分的拒绝原因。
type Error struct {
	Op     string
	Reason Reason
}

func (e *Error) Error() string {
	return "ontology: " + e.Op + " rejected: " + string(e.Reason)
}

func reject(op string, reason Reason) error {
	return &Error{Op: op, Reason: reason}
}

// Scheduler 按时间与日志两条进度调度检查点刷脏节奏。
type Scheduler struct {
	mu        sync.Mutex
	t         int64
	w         int64
	f         int64
	active    bool
	startNow  int64
	startWal  int64
	n         int64
	written   int64
	lastNow   int64
	lastWal   int64
	completed int64
}

// scaledQuota 精确计算 min(n, floor(n*progress*1000/(budget*f)))。
// n、progress、budget、f 均非负且 budget、f 为正；n 与 progress 可达 2^40，
// 故全程使用 big.Int，避免中间乘积溢出。
func scaledQuota(n, progress, budget, f int64) int64 {
	num := new(big.Int).Mul(big.NewInt(n), big.NewInt(progress))
	num.Mul(num, big.NewInt(1000))
	den := new(big.Int).Mul(big.NewInt(budget), big.NewInt(f)) // 分母 = budget*f，1000 仅在分子
	// 先在大整数域内做封顶比较，避免 num 超出 int64 时 Int64() 结果未定义。
	capLine := new(big.Int).Mul(big.NewInt(n), den)
	if num.Cmp(capLine) >= 0 {
		return n
	}
	num.Quo(num, den)
	return num.Int64()
}

// NewScheduler 构造调度器。T 为检查点间隔（毫秒），W 为日志预算（字节），
// F 为完成目标（千分数，1..999）。
func NewScheduler(t, w, f int64) (*Scheduler, error) {
	if t <= 0 {
		return nil, reject("NewScheduler", ReasonTNotPositive)
	}
	if w <= 0 {
		return nil, reject("NewScheduler", ReasonWNotPositive)
	}
	if f < 1 || f > 999 {
		return nil, reject("NewScheduler", ReasonFOutOfRange)
	}
	return &Scheduler{t: t, w: w, f: f}, nil
}

// Begin 开始一次检查点。
func (s *Scheduler) Begin(now, walPos, dirty int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 {
		return reject("Begin", ReasonNegativeNow)
	}
	if walPos < 0 {
		return reject("Begin", ReasonNegativeWalPos)
	}
	if dirty < 0 {
		return reject("Begin", ReasonNegativeDirty)
	}
	if s.active {
		return reject("Begin", ReasonCheckpointActive)
	}
	if now < s.lastNow {
		return reject("Begin", ReasonNowRegression)
	}
	if walPos < s.lastWal {
		return reject("Begin", ReasonWalPosRegression)
	}
	s.lastNow = now
	s.lastWal = walPos
	s.active = true
	s.startNow = now
	s.startWal = walPos
	s.n = dirty
	s.written = 0
	if dirty == 0 {
		s.active = false
		s.n = 0
		s.completed++
	}
	return nil
}

// Tick 返回当前累计配额 Q 与还需写出数 need。
func (s *Scheduler) Tick(now, walPos int64) (quota, need int64, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if now < 0 {
		return 0, 0, reject("Tick", ReasonNegativeNow)
	}
	if walPos < 0 {
		return 0, 0, reject("Tick", ReasonNegativeWalPos)
	}
	if !s.active {
		return 0, 0, reject("Tick", ReasonNoCheckpoint)
	}
	if now < s.lastNow {
		return 0, 0, reject("Tick", ReasonNowRegression)
	}
	if walPos < s.lastWal {
		return 0, 0, reject("Tick", ReasonWalPosRegression)
	}
	s.lastNow = now
	s.lastWal = walPos
	e := now - s.startNow
	u := walPos - s.startWal
	q1 := scaledQuota(s.n, e, s.t, s.f)
	q2 := scaledQuota(s.n, u, s.w, s.f)
	if q2 > q1 {
		q1 = q2
	}
	need = q1 - s.written
	if need < 0 {
		need = 0
	}
	return q1, need, nil
}

// Wrote 登记又写出 k 页。
func (s *Scheduler) Wrote(k int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.active {
		return reject("Wrote", ReasonNoCheckpoint)
	}
	if k <= 0 {
		return reject("Wrote", ReasonKNotPositive)
	}
	remaining := s.n - s.written
	if k > remaining {
		return reject("Wrote", ReasonKExceedsRemaining)
	}
	s.written += k
	if s.written == s.n {
		s.active = false
		s.n = 0
		s.written = 0
		s.completed++
	}
	return nil
}

// Status 报告调度器当前状态。
type StatusInfo struct {
	Active    bool
	N         int64
	Written   int64
	Completed int64
}

func (s *Scheduler) Status() StatusInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	return StatusInfo{
		Active:    s.active,
		N:         s.n,
		Written:   s.written,
		Completed: s.completed,
	}
}
