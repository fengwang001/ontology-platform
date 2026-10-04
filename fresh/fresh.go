package fresh

import (
	"fmt"

	"ontology/dag"
)

// Violation 标识一个当前违约的 (数据集, 期号)。
type Violation struct {
	Dataset string
	K       int64
}

type dstate struct {
	next   int64           // 最小未落地期号
	lands  map[int64]int64 // 已落地期 -> 落地时刻
	alert  map[int64]bool  // 已告警（归因冻结）期号
	cursor int64           // 已落地区间中待考察的最早期
	// tail 是未落地尾部中尚未考察过的最早期（只增不减）。
	// 时钟连续跨过多个周期截止时，[next, kmax] 整个区间同时违约，
	// 这些都是新违约，故考察数恰好等于新增告警数，符合复杂度上界。
	tail int64
}

// Tracker 在 DAG 之上维护单调时钟与每个数据集的落地记录，并增量判定违约。
// 检测从每数据集自己的未结案最早期开始，不从第 0 期重扫。
type Tracker struct {
	*dag.Graph
	now      int64
	st       map[string]*dstate
	examined int64
}

// New 构造周期长度为 T 秒的跟踪器。
func New(T int64) (*Tracker, error) {
	g, err := dag.NewGraph(T)
	if err != nil {
		return nil, err
	}
	return &Tracker{Graph: g, st: make(map[string]*dstate)}, nil
}

// Now 返回当前时钟。
func (tr *Tracker) Now() int64 {
	tr.Lock()
	defer tr.Unlock()
	return tr.now
}

func (tr *Tracker) stateFor(name string) *dstate {
	s, ok := tr.st[name]
	if !ok {
		s = &dstate{lands: make(map[int64]int64), alert: make(map[int64]bool)}
		tr.st[name] = s
	}
	return s
}

func (tr *Tracker) deadlineOf(name string, k int64) int64 {
	d, _ := tr.DatasetLocked(name)
	return k*tr.T + d.Off
}

// Land 记录 name 第 k 期在 now 落地。
// 拒绝次序：参数非法 > 时钟回退 > 数据集不存在 > ErrAlready/ErrOutOfOrder >
// ErrTooEarly > ErrUpstreamMissing；被拒绝不改任何状态（含时钟）。
func (tr *Tracker) Land(name string, k, now int64) error {
	if name == "" || k < 0 || now < 0 || now > 1_000_000_000_000 {
		return dag.ErrInvalid
	}
	tr.Lock()
	defer tr.Unlock()

	if now < tr.now {
		return dag.ErrClockRewind
	}
	d, ok := tr.DatasetLocked(name)
	if !ok {
		return dag.ErrNoDataset
	}
	s := tr.stateFor(name)
	if k < s.next {
		return dag.ErrAlready
	}
	if k > s.next {
		return dag.ErrOutOfOrder
	}
	if k > 0 && now < k*tr.T {
		return dag.ErrTooEarly
	}
	// 各父第 k 期必须已落地；Parents 按名字节序排序，缺失即取最小者。
	for _, p := range d.Parents {
		ps, ok := tr.st[p]
		if !ok || ps.next <= k {
			// 父尚无任何状态，或父的最小未落地期仍 <= k：第 k 期都未落地。
			return fmt.Errorf("%w: %s", dag.ErrUpstreamMissing, p)
		}
	}

	tr.now = now
	tr.FreezeLocked()
	s.lands[k] = now
	s.next = k + 1
	return nil
}

// Landed 返回 (name,k) 的落地时刻；未落地第二返回值为 false。
func (tr *Tracker) Landed(name string, k int64) (int64, bool) {
	tr.Lock()
	defer tr.Unlock()
	return tr.landedLocked(name, k)
}

// LandedLocked 是 Landed 的不加锁版本，调用方须持有 Graph 锁。
func (tr *Tracker) LandedLocked(name string, k int64) (int64, bool) {
	return tr.landedLocked(name, k)
}

func (tr *Tracker) landedLocked(name string, k int64) (int64, bool) {
	s, ok := tr.st[name]
	if !ok {
		return 0, false
	}
	t, landed := s.lands[k]
	return t, landed
}

// Deadline 返回 deadline(name,k)=k*T+off(name)。
func (tr *Tracker) Deadline(name string, k int64) int64 {
	tr.Lock()
	defer tr.Unlock()
	return tr.deadlineOf(name, k)
}

// DeadlineLocked 是 Deadline 的不加锁版本。
func (tr *Tracker) DeadlineLocked(name string, k int64) int64 {
	return tr.deadlineOf(name, k)
}

// MarkAlerted 在归因冻结违约 (name,k) 时调用。
func (tr *Tracker) MarkAlerted(name string, k int64) {
	tr.stateFor(name).alert[k] = true
}

// MarkAlertedLocked 是 MarkAlerted 的不加锁版本。
func (tr *Tracker) MarkAlertedLocked(name string, k int64) {
	tr.stateFor(name).alert[k] = true
}

// Alerted 报告 (name,k) 是否已经告警。
func (tr *Tracker) Alerted(name string, k int64) bool {
	tr.Lock()
	defer tr.Unlock()
	s, ok := tr.st[name]
	return ok && s.alert[k]
}

// Examined 返回 Evaluate 累计考察的 (d,k) 对数。
func (tr *Tracker) Examined() int64 {
	tr.Lock()
	defer tr.Unlock()
	return tr.examined
}

// Violating 判断 (name,k) 在时刻 t 是否违约（调用方持锁，不计数）。
func (tr *Tracker) Violating(name string, k, t int64) bool {
	tr.Lock()
	defer tr.Unlock()
	return tr.violatingLocked(name, k, t)
}

// ViolatingLocked 是 Violating 的不加锁版本。
func (tr *Tracker) ViolatingLocked(name string, k, t int64) bool {
	return tr.violatingLocked(name, k, t)
}

func (tr *Tracker) violatingLocked(name string, k, t int64) bool {
	s := tr.stateFor(name)
	if landedAt, landed := s.lands[k]; landed {
		return landedAt > tr.deadlineOf(name, k)
	}
	return t > tr.deadlineOf(name, k)
}

// Detect 推进时钟并增量找出当前违约且未告警的 (d,k)（顺序按数据集登记序）。
// 拒绝次序：参数非法 > 时钟回退；被拒绝不改状态。
func (tr *Tracker) Detect(now int64) ([]Violation, error) {
	tr.Lock()
	defer tr.Unlock()
	return tr.detectLocked(now)
}

// DetectLocked 是 Detect 的不加锁版本，供 blame 在同一把锁内完成
// “检测→归因→冻结”，避免跨结构操作出现可观察交错。
func (tr *Tracker) DetectLocked(now int64) ([]Violation, error) {
	return tr.detectLocked(now)
}

func (tr *Tracker) detectLocked(now int64) ([]Violation, error) {
	if now < 0 || now > 1_000_000_000_000 {
		return nil, dag.ErrInvalid
	}
	if now < tr.now {
		return nil, dag.ErrClockRewind
	}
	tr.now = now
	tr.FreezeLocked()

	var out []Violation
	for _, name := range tr.NamesLocked() {
		s := tr.stateFor(name)
		// 已落地区间：从 cursor 扫到 next-1，逐个结案。
		for k := s.cursor; k < s.next; k++ {
			tr.examined++
			if s.lands[k] > tr.deadlineOf(name, k) && !s.alert[k] {
				out = append(out, Violation{Dataset: name, K: k})
			}
		}
		s.cursor = s.next
		if s.tail < s.next {
			s.tail = s.next
		}
		// 未落地尾部：从 tail 起逐期考察，直到截止 >= now。
		// 这些期都未落地，凡 deadline < now 即违约，且都为首次出现
		//（tail 之前或已准时结案、或已告警冻结），故考察数恰为新增违约数。
		for k := s.tail; now > tr.deadlineOf(name, k); k++ {
			tr.examined++
			if !s.alert[k] {
				out = append(out, Violation{Dataset: name, K: k})
			}
			s.tail = k + 1
		}
	}
	return out, nil
}
