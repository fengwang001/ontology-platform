// Package deadlock 实现带备选请求的多实例资源分配图死锁检测与最小代价消解器。
//
// 分配规则：先到先授予；请求携带 1..3 个备选向量，按列出顺序取第一个
// 不超过当前可用量的备选授予；全部放不下才阻塞。释放后做授予不动点：
// 反复在阻塞进程中选阻塞序号最小且存在可满足备选者授予，直到没有可授予者。
package deadlock

import (
	"fmt"
	"sync"
)

// ErrCode 区分被拒绝操作的原因。
type ErrCode int

const (
	// ErrInvalidParam 参数非法（构造参数越界、备选个数/向量长度/负数/全零等）。
	ErrInvalidParam ErrCode = iota
	// ErrNoSuchProcess 进程不存在（编号越界或已被永久中止）。
	ErrNoSuchProcess
	// ErrProcessBlocked 进程已阻塞（对阻塞进程再 Request 或 Release）。
	ErrProcessBlocked
	// ErrUnsatisfiable 请求永不可满足（某备选加已持有量超过总量）。
	ErrUnsatisfiable
	// ErrOverRelease 归还超过持有。
	ErrOverRelease
)

func (c ErrCode) String() string {
	switch c {
	case ErrInvalidParam:
		return "invalid parameter"
	case ErrNoSuchProcess:
		return "no such process"
	case ErrProcessBlocked:
		return "process blocked"
	case ErrUnsatisfiable:
		return "request can never be satisfied"
	case ErrOverRelease:
		return "release exceeds allocation"
	}
	return "unknown error"
}

// Error 是可区分原因的操作错误。
type Error struct {
	Code ErrCode
	Msg  string
}

func (e *Error) Error() string { return e.Msg }

func newError(code ErrCode, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf("%s: %s", code, fmt.Sprintf(format, args...))}
}

// Grant 记录一次授予：进程 PID 获得其备选下标 Alt。
type Grant struct {
	PID int
	Alt int
}

// RequestResult 是 Request 的返回值：Granted 表示是否授予，
// 授予时 Alt 为被选中的备选下标，阻塞时 Alt 为 -1。
type RequestResult struct {
	Granted bool
	Alt     int
}

// ResolveStep 是 Resolve 的一步消解记录。
type ResolveStep struct {
	Victim     int     // 牺牲者进程编号
	Cost       int64   // 选择时计算的代价 Σ alloc[r]*c[r]*(1+rb)
	Grants     []Grant // 该步回滚后授予不动点产生的授予列表
	Terminated bool    // 回滚后 rb 达到 L 被永久中止
}

// Manager 是资源分配与死锁消解器。所有方法可并发调用，
// 结果等价于某个串行顺序；Resolve 整体是一个原子步骤。
type Manager struct {
	mu sync.Mutex

	R, P, L int
	T       []int64   // 各类资源总量
	c       []int64   // 各类资源代价权重
	avail   []int64   // 当前可用量
	alloc   [][]int64 // 每进程持有量
	rb      []int     // 每进程回滚次数
	alive   []bool    // 是否存活（未被永久中止）
	blocked []bool    // 是否阻塞
	pending [][][]int64
	bseq    []int64 // 阻塞序号，仅阻塞时有效
	bseqCtr int64   // 全局阻塞序号计数器

	checks int64 // 非导出计数器：最近一次 Detect 检查阻塞进程的次数
}

// New 构造管理器。参数越界返回 ErrInvalidParam。
func New(R int, T, c []int64, P, L int) (*Manager, error) {
	if R < 1 || R > 8 {
		return nil, newError(ErrInvalidParam, "R=%d out of range [1,8]", R)
	}
	if len(T) != R || len(c) != R {
		return nil, newError(ErrInvalidParam, "len(T)=%d len(c)=%d, want %d", len(T), len(c), R)
	}
	for r := 0; r < R; r++ {
		if T[r] < 1 || T[r] > 1_000_000 {
			return nil, newError(ErrInvalidParam, "T[%d]=%d out of range [1,1e6]", r, T[r])
		}
		if c[r] < 1 || c[r] > 1000 {
			return nil, newError(ErrInvalidParam, "c[%d]=%d out of range [1,1000]", r, c[r])
		}
	}
	if P < 1 || P > 64 {
		return nil, newError(ErrInvalidParam, "P=%d out of range [1,64]", P)
	}
	if L < 1 || L > 10 {
		return nil, newError(ErrInvalidParam, "L=%d out of range [1,10]", L)
	}
	m := &Manager{
		R: R, P: P, L: L,
		T:       append([]int64(nil), T...),
		c:       append([]int64(nil), c...),
		avail:   append([]int64(nil), T...),
		alloc:   make([][]int64, P),
		rb:      make([]int, P),
		alive:   make([]bool, P),
		blocked: make([]bool, P),
		pending: make([][][]int64, P),
		bseq:    make([]int64, P),
	}
	for p := 0; p < P; p++ {
		m.alloc[p] = make([]int64, R)
		m.alive[p] = true
	}
	return m, nil
}

// Request 为进程 p 请求一组备选向量（任选其一）。
// 校验顺序：参数非法 → 进程不存在 → 进程已阻塞 → 请求永不可满足，
// 只报第一个原因；被拒绝时不改变任何状态与 bseq 计数器。
// “请求永不可满足”指全部备选都不可满足（某备选存在 r 使
// alloc[p][r]+alt[r] > T[r] 时该备选不可满足）；只要还有一个备选
// 原则上可满足，请求就被接受（授予或阻塞）。
func (m *Manager) Request(p int, alts [][]int64) (RequestResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.validateAlts(alts); err != nil {
		return RequestResult{}, err
	}
	if err := m.checkAlive(p); err != nil {
		return RequestResult{}, err
	}
	if m.blocked[p] {
		return RequestResult{}, newError(ErrProcessBlocked, "process %d is blocked", p)
	}
	satisfiable := false
	for _, alt := range alts {
		ok := true
		for r := 0; r < m.R; r++ {
			if m.alloc[p][r]+alt[r] > m.T[r] {
				ok = false
				break
			}
		}
		if ok {
			satisfiable = true
			break
		}
	}
	if !satisfiable {
		return RequestResult{}, newError(ErrUnsatisfiable,
			"no alternative of process %d can ever fit within totals", p)
	}
	if idx := firstFitting(alts, m.avail); idx >= 0 {
		m.grant(p, alts[idx])
		return RequestResult{Granted: true, Alt: idx}, nil
	}
	m.bseqCtr++
	m.bseq[p] = m.bseqCtr
	m.pending[p] = copyAlts(alts)
	m.blocked[p] = true
	return RequestResult{Granted: false, Alt: -1}, nil
}

// Release 让进程 p 归还 vec，随后做授予不动点，返回按授予先后排列的授予列表。
// 校验顺序：参数非法 → 进程不存在 → 进程已阻塞 → 归还超过持有。
func (m *Manager) Release(p int, vec []int64) ([]Grant, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.validateVec(vec); err != nil {
		return nil, err
	}
	if err := m.checkAlive(p); err != nil {
		return nil, err
	}
	if m.blocked[p] {
		return nil, newError(ErrProcessBlocked, "process %d is blocked", p)
	}
	for r := 0; r < m.R; r++ {
		if vec[r] > m.alloc[p][r] {
			return nil, newError(ErrOverRelease,
				"process %d releases %d of resource %d but holds %d", p, vec[r], r, m.alloc[p][r])
		}
	}
	for r := 0; r < m.R; r++ {
		m.avail[r] += vec[r]
		m.alloc[p][r] -= vec[r]
	}
	return m.grantFixpoint(), nil
}

// Detect 做图归约，返回死锁进程编号升序列表，不改变任何状态。
//
// Work 初值为 avail 加上全部未阻塞进程的持有量（未阻塞进程视为总会
// 结束并释放）；反复按进程编号升序检查不在 Finish 的阻塞进程，只要它
// 存在某个备选不大于 Work 就加入 Finish 并把它的持有量并入 Work（取得
// 备选后结束，备选与持有一并归还，净增为持有量），直到不再有变化。
// 不在 Finish 的阻塞进程即死锁集合。checks 计数不超过 b(b+1)/2。
func (m *Manager) Detect() []int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.detect()
}

func (m *Manager) detect() []int {
	m.checks = 0
	work := append([]int64(nil), m.avail...)
	for p := 0; p < m.P; p++ {
		if m.alive[p] && !m.blocked[p] {
			for r := 0; r < m.R; r++ {
				work[r] += m.alloc[p][r]
			}
		}
	}
	finished := make([]bool, m.P)
	for {
		progress := false
		for p := 0; p < m.P; p++ {
			if !m.alive[p] || !m.blocked[p] || finished[p] {
				continue
			}
			m.checks++
			if firstFitting(m.pending[p], work) >= 0 {
				finished[p] = true
				for r := 0; r < m.R; r++ {
					work[r] += m.alloc[p][r]
				}
				progress = true
			}
		}
		if !progress {
			break
		}
	}
	var dead []int
	for p := 0; p < m.P; p++ {
		if m.alive[p] && m.blocked[p] && !finished[p] {
			dead = append(dead, p)
		}
	}
	return dead
}

// Resolve 原子地逐个回滚牺牲者直至死锁解除，返回每步记录。
//
// 每步：计算死锁集合，为空则结束；否则在集合中持有量总和大于 0 的
// 进程里选代价 Σ alloc[r]*c[r]*(1+rb) 最小者（并列取小编号）为牺牲者，
// 回滚它（持有量并入 avail、阻塞请求取消、rb 加一；达到 L 则永久中止），
// 随后做一次授予不动点。整个过程持有锁，观察者看不到中间状态。
func (m *Manager) Resolve() []ResolveStep {
	m.mu.Lock()
	defer m.mu.Unlock()
	var steps []ResolveStep
	for {
		dead := m.detect()
		if len(dead) == 0 {
			return steps
		}
		victim := -1
		var bestCost int64
		for _, p := range dead {
			var total, cost int64
			for r := 0; r < m.R; r++ {
				total += m.alloc[p][r]
				cost += m.alloc[p][r] * m.c[r]
			}
			if total == 0 {
				continue
			}
			cost *= int64(1 + m.rb[p])
			if victim < 0 || cost < bestCost || (cost == bestCost && p < victim) {
				victim, bestCost = p, cost
			}
		}
		if victim < 0 {
			// 不可达：死锁集合中必有持有资源的进程（否则其备选
			// 必然不大于 Work）。防御性退出以避免死循环。
			return steps
		}
		for r := 0; r < m.R; r++ {
			m.avail[r] += m.alloc[victim][r]
			m.alloc[victim][r] = 0
		}
		m.blocked[victim] = false
		m.pending[victim] = nil
		m.rb[victim]++
		terminated := m.rb[victim] == m.L
		if terminated {
			m.alive[victim] = false
		}
		grants := m.grantFixpoint()
		steps = append(steps, ResolveStep{
			Victim:     victim,
			Cost:       bestCost,
			Grants:     grants,
			Terminated: terminated,
		})
	}
}

// Checks 返回最近一次 Detect 检查阻塞进程的次数（用于验证归约复杂度上界）。
func (m *Manager) Checks() int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.checks
}

// validateAlts 校验备选组：1..3 个备选，每个长度 R、非负、不全为 0。
func (m *Manager) validateAlts(alts [][]int64) error {
	if len(alts) < 1 || len(alts) > 3 {
		return newError(ErrInvalidParam, "need 1..3 alternatives, got %d", len(alts))
	}
	for _, alt := range alts {
		if err := m.validateVec(alt); err != nil {
			return err
		}
	}
	return nil
}

// validateVec 校验单个向量：长度 R、非负、不全为 0。
func (m *Manager) validateVec(vec []int64) error {
	if len(vec) != m.R {
		return newError(ErrInvalidParam, "vector length %d, want %d", len(vec), m.R)
	}
	allZero := true
	for r, v := range vec {
		if v < 0 {
			return newError(ErrInvalidParam, "negative amount %d at resource %d", v, r)
		}
		if v != 0 {
			allZero = false
		}
	}
	if allZero {
		return newError(ErrInvalidParam, "all-zero vector")
	}
	return nil
}

// checkAlive 校验进程编号合法且未被永久中止。
func (m *Manager) checkAlive(p int) error {
	if p < 0 || p >= m.P {
		return newError(ErrNoSuchProcess, "process id %d out of range [0,%d)", p, m.P)
	}
	if !m.alive[p] {
		return newError(ErrNoSuchProcess, "process %d has been terminated", p)
	}
	return nil
}

// fits 判断 vec 是否不超过 avail。
func fits(vec, avail []int64) bool {
	for r := range vec {
		if vec[r] > avail[r] {
			return false
		}
	}
	return true
}

// firstFitting 返回第一个不超过 avail 的备选下标，没有则返回 -1。
func firstFitting(alts [][]int64, avail []int64) int {
	for i, alt := range alts {
		if fits(alt, avail) {
			return i
		}
	}
	return -1
}

// grant 把备选 alt 授予进程 p：avail 减去、alloc 加上。
func (m *Manager) grant(p int, alt []int64) {
	for r := 0; r < m.R; r++ {
		m.avail[r] -= alt[r]
		m.alloc[p][r] += alt[r]
	}
}

// unblock 解除进程 p 的阻塞状态并授予备选 idx。
func (m *Manager) unblock(p, idx int) {
	m.grant(p, m.pending[p][idx])
	m.blocked[p] = false
	m.pending[p] = nil
}

// grantFixpoint 授予不动点：反复在全部阻塞进程中选 bseq 最小且存在
// 可满足备选的进程，授予其第一个满足的备选，直到没有这样的进程。
// 返回按授予先后排列的授予列表。
func (m *Manager) grantFixpoint() []Grant {
	var grants []Grant
	for {
		best := -1
		bestIdx := -1
		for p := 0; p < m.P; p++ {
			if !m.alive[p] || !m.blocked[p] {
				continue
			}
			idx := firstFitting(m.pending[p], m.avail)
			if idx < 0 {
				continue
			}
			if best < 0 || m.bseq[p] < m.bseq[best] {
				best, bestIdx = p, idx
			}
		}
		if best < 0 {
			return grants
		}
		m.unblock(best, bestIdx)
		grants = append(grants, Grant{PID: best, Alt: bestIdx})
	}
}

func copyAlts(alts [][]int64) [][]int64 {
	out := make([][]int64, len(alts))
	for i, alt := range alts {
		out[i] = append([]int64(nil), alt...)
	}
	return out
}
