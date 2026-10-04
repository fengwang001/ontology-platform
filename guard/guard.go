package guard

// Guard 按 sn 记录错误计数 e、历史锁定次数 k 与锁定截止 lockUntil。
// Guard 自身不加锁：编排层在 roster 的全局锁内调用，保证并发等价于某串行顺序。
type Guard struct {
	m  int
	lk int64
	g  map[string]*entry
}

type entry struct {
	e         int
	k         int
	lockUntil int64
}

// New 创建 Guard，m 为错误尝试阈值，lk 为锁定基数（秒）。
func New(m int, lk int64) *Guard {
	return &Guard{m: m, lk: lk, g: make(map[string]*entry)}
}

// Locked 报告 now 时刻 sn 是否仍处于锁定期。now==lockUntil 即已解锁（false）。
func (g *Guard) Locked(sn []byte, now int64) bool {
	e := g.g[string(sn)]
	return e != nil && now < e.lockUntil
}

// NoteConflict 记录一次错误尝试。
// 返回 locked=true 表示本次（第 M 次）触发锁定：k 加 1、e 清零、
// lockUntil=now+Lk*2^(min(k,7)-1)（k 取加 1 之后的值）。
// 第 M 次的那次调用仍由调用方向用户返回 ErrConflict。
func (g *Guard) NoteConflict(sn []byte, now int64) (locked bool, lockUntil int64, k int) {
	e := g.g[string(sn)]
	if e == nil {
		e = &entry{}
		g.g[string(sn)] = e
	}
	e.e++
	if e.e < g.m {
		return false, e.lockUntil, e.k
	}
	e.k++
	e.e = 0
	e.lockUntil = now + g.lk<<uint(min(e.k, 7)-1)
	return true, e.lockUntil, e.k
}

// Reset 清零错误计数并解除锁定，但保留历史锁定次数 k（Reset 语义）。
func (g *Guard) Reset(sn []byte) {
	e := g.g[string(sn)]
	if e == nil {
		return
	}
	e.e = 0
	e.lockUntil = 0
}

// Snapshot 是某 sn 的 guard 状态只读视图。
type Snapshot struct {
	E         int
	K         int
	LockUntil int64
}

// Get 返回 guard 状态快照（不存在返回零值与 false）。
func (g *Guard) Get(sn []byte) (Snapshot, bool) {
	e := g.g[string(sn)]
	if e == nil {
		return Snapshot{}, false
	}
	return Snapshot{E: e.e, K: e.k, LockUntil: e.lockUntil}, true
}
