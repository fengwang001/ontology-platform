package ontology

import (
	"errors"
	"sort"
	"sync"
)

// TxnState 是事务在名册中的生命周期状态。
type TxnState int

const (
	OPEN TxnState = iota
	PREPARED
	COMMITTED
	ABORTED
)

// TxnName 是事务名 (s, k)：子任务号 s 与检查点号 k。
type TxnName struct {
	S int
	K int64
}

// TxnInfo 是 Txn 查询的返回值。
type TxnInfo struct {
	State TxnState
	Epoch int
	N     int64
}

// Stats 是 Stats 查询的返回值。
type Stats struct {
	Committed  int64
	Aborted    int64
	Open       int64
	Prepared   int64
	ProbeCount int64
	Visited    int64
}

// RestoreResult 是 Restore 的返回值。
type RestoreResult struct {
	Committed []TxnName
	Aborted   []TxnName
	Probes    int64
}

var (
	ErrInvalidArgument = errors.New("invalid argument")
	ErrStaleBarrier    = errors.New("checkpoint out of order")
	ErrStaleNotice     = errors.New("stale completion notice")
	ErrFutureNotice    = errors.New("completion notice ahead of barrier")
	ErrRestoreFallback = errors.New("restore checkpoint before last notice")
	ErrRestoreAhead    = errors.New("restore checkpoint after last barrier")
	ErrNoSuchTxn       = errors.New("no such transaction")
)

type txnEntry struct {
	state TxnState
	epoch int
	n     int64
	life  int64
}

type commitItem struct {
	k     int64
	s     int
	entry *txnEntry
}

// Registry 是带事务命名、通知提交与悬挂事务清扫的两阶段提交 sink 事务名册。
type Registry struct {
	mu sync.Mutex

	p    int
	m    int
	lb   int64
	ln   int64
	life int64

	txns map[TxnName]*txnEntry
	open map[int]TxnName

	ready  []commitItem
	cursor int

	committedN int64
	abortedN   int64
	probes     int64
	visited    int64
}

// NewRegistry 创建一个并行度为 P、连续缺失容忍为 M 的事务名册。
func NewRegistry(P, M int) (*Registry, error) {
	if P < 1 || P > 64 || M < 1 || M > 1000 {
		return nil, ErrInvalidArgument
	}
	return &Registry{
		p:    P,
		m:    M,
		txns: make(map[TxnName]*txnEntry),
		open: make(map[int]TxnName),
	}, nil
}

// Write 在子任务 s 当前打开事务名下累加 n 条记录，必要时先中止遗留事务。
func (r *Registry) Write(s int, n int64) error {
	if s < 0 || s >= r.p || n < 1 || n > 1_000_000 {
		return ErrInvalidArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	name := TxnName{S: s, K: r.lb + 1}
	old, exists := r.txns[name]

	if exists && old.state == OPEN && old.life == r.life {
		old.n += n
		return nil
	}

	epoch := 0
	if exists {
		// 名下有旧 life 的 OPEN/PREPARED 遗留事务，或终态事务：同一名字下
		// 至多保留一个非终态事务，重开前先中止旧事务。
		epoch = old.epoch + 1
		if old.state == OPEN || old.state == PREPARED {
			old.state = ABORTED
			r.abortedN += old.n
			r.removeOpen(s, name)
		}
	}

	entry := &txnEntry{state: OPEN, epoch: epoch, n: n, life: r.life}
	r.txns[name] = entry
	r.open[s] = name
	return nil
}

// Barrier 把检查点 cp 下当前 life 的 OPEN 事务预提交。
func (r *Registry) Barrier(cp int64) ([]TxnName, error) {
	if cp < 1 {
		return nil, ErrInvalidArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if cp != r.lb+1 {
		return nil, ErrStaleBarrier
	}

	prepared := make([]TxnName, 0)
	// 按 s 升序处理；没有写入的子任务名下没有事务，不产生条目。
	for s := 0; s < r.p; s++ {
		name := TxnName{S: s, K: cp}
		entry, ok := r.txns[name]
		if !ok || entry.state != OPEN || entry.life != r.life {
			continue
		}
		entry.state = PREPARED
		r.removeOpen(s, name)
		r.ready = append(r.ready, commitItem{k: cp, s: s, entry: entry})
		prepared = append(prepared, name)
	}
	r.lb = cp
	return prepared, nil
}

// Complete 处理完成通知：提交所有 k 不大于 c 的已预提交事务。
func (r *Registry) Complete(c int64) ([]TxnName, error) {
	if c < 1 {
		return nil, ErrInvalidArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if c <= r.ln {
		return nil, ErrStaleNotice
	}
	if c > r.lb {
		return nil, ErrFutureNotice
	}

	committed := r.commitUpTo(c)
	r.ln = c
	return committed, nil
}

// Restore 从检查点 c 恢复并改并行度：提交、清扫、换代。
func (r *Registry) Restore(c int64, newP int) (RestoreResult, error) {
	if c < 0 || newP < 1 || newP > 64 {
		return RestoreResult{}, ErrInvalidArgument
	}
	r.mu.Lock()
	defer r.mu.Unlock()

	if c < r.ln {
		return RestoreResult{}, ErrRestoreFallback
	}
	if c > r.lb {
		return RestoreResult{}, ErrRestoreAhead
	}

	result := RestoreResult{Committed: r.commitUpTo(c)}

	// 清扫：s 取并集，缩容扫到旧并行度，扩容扫到新并行度。
	maxP := r.p
	if newP > maxP {
		maxP = newP
	}
	for s := 0; s < maxP; s++ {
		missing := 0
		for k := c + 1; missing < r.m; k++ {
			name := TxnName{S: s, K: k}
			r.probes++
			result.Probes++
			entry, ok := r.txns[name]
			if ok && (entry.state == OPEN || entry.state == PREPARED) {
				entry.state = ABORTED
				r.abortedN += entry.n
				r.removeOpen(s, name)
				result.Aborted = append(result.Aborted, name)
				missing = 0
			} else {
				missing++
			}
		}
	}

	// 换代：ready 中剩余的只可能是当前 life 且 k > c 的 PREPARED 事务，
	// 它们已变成旧 life 遗留事务，此后不再被提交。
	r.p = newP
	r.lb = c
	r.ln = c
	r.life++
	r.ready = r.ready[:0]
	r.cursor = 0

	// open 里的非终态名字在清扫中已全部被中止（其 k 必然大于 c，属于
	// (c+1 .. c+M) 探测窗口）。
	r.open = make(map[int]TxnName)

	return result, nil
}

// Txn 查询名字 (s, k) 下事务的状态、世代与记录数。
func (r *Registry) Txn(s int, k int64) (TxnInfo, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.txns[TxnName{S: s, K: k}]
	if !ok {
		return TxnInfo{}, ErrNoSuchTxn
	}
	return TxnInfo{State: entry.state, Epoch: entry.epoch, N: entry.n}, nil
}

// Pending 返回全部 OPEN 或 PREPARED 事务名，按 (s, k) 升序。
func (r *Registry) Pending() []TxnName {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := make([]TxnName, 0)
	for name, entry := range r.txns {
		if entry.state == OPEN || entry.state == PREPARED {
			names = append(names, name)
		}
	}
	sortNames(names)
	return names
}

// Stats 返回记录守恒各分量、探测累计数与 visited。
func (r *Registry) Stats() Stats {
	r.mu.Lock()
	defer r.mu.Unlock()
	var openN, preparedN int64
	for _, entry := range r.txns {
		switch entry.state {
		case OPEN:
			openN += entry.n
		case PREPARED:
			preparedN += entry.n
		}
	}
	return Stats{
		Committed:  r.committedN,
		Aborted:    r.abortedN,
		Open:       openN,
		Prepared:   preparedN,
		ProbeCount: r.probes,
		Visited:    r.visited,
	}
}

// commitUpTo 提交当前 life、k 不大于 c 的 PREPARED 事务，按 (k, s) 升序。
// ready 按 Barrier 推进顺序追加：k 递增，同一 k 内 s 递增，故本身即有序；
// 已提交前缀通过 cursor 丢弃，扫描代价只取决于本次提交数量。
func (r *Registry) commitUpTo(c int64) []TxnName {
	committed := make([]TxnName, 0)
	i := r.cursor
	for i < len(r.ready) {
		item := r.ready[i]
		if item.k > c {
			break
		}
		r.visited++ // 被提交的待提交条目
		item.entry.state = COMMITTED
		r.committedN += item.entry.n
		committed = append(committed, TxnName{S: item.s, K: item.k})
		i++
	}
	// 再计一次"越过本次提交边界"的访问（首个 k>c 的待提交条目）；
	// 没有边界条目（全部提交）时不多计。故增量 = 提交数 或 提交数+1。
	if i < len(r.ready) {
		r.visited++
	}
	r.cursor = i
	return committed
}

func (r *Registry) removeOpen(s int, name TxnName) {
	if cur, ok := r.open[s]; ok && cur == name {
		delete(r.open, s)
	}
}

func sortNames(names []TxnName) {
	sort.Slice(names, func(i, j int) bool {
		if names[i].S != names[j].S {
			return names[i].S < names[j].S
		}
		return names[i].K < names[j].K
	})
}
