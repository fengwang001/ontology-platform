// Package allocator 实现按检查点纪元回收的数据源拆分分配器。
//
// 拆分按「偏好读取器 = 编号 mod R」分配；读取器失败时，按其最近已完成
// 检查点 done 决定名下拆分的去留：分配纪元 at 晚于 done 的归还（可能被
// 隔离），完成纪元 ft 晚于 done 的撤销完成，其余保留。所有操作在内部
// 互斥锁下串行化，ReaderFailed 是原子步骤。
package allocator

import (
	"errors"
	"fmt"
	"sort"
	"sync"
)

// maxSplitID 是拆分编号的上界（含）。
const maxSplitID = int64(1_000_000_000)

// 可被 errors.Is 区分的拒绝原因。
var (
	ErrInvalidArgument      = errors.New("allocator: invalid argument")
	ErrSealed               = errors.New("allocator: source is sealed")
	ErrDuplicateSplit       = errors.New("allocator: duplicate split id")
	ErrReaderFailed         = errors.New("allocator: reader has failed")
	ErrReaderNotFailed      = errors.New("allocator: reader has not failed")
	ErrNotAssignedToReader  = errors.New("allocator: split not assigned to this reader")
	ErrCheckpointOutOfOrder = errors.New("allocator: checkpoint out of order")
	ErrCompleteAhead        = errors.New("allocator: completion ahead of triggered checkpoint")
	ErrCompleteStale        = errors.New("allocator: completion not newer than done")
)

// State 是拆分的生命周期状态。
type State int

const (
	StateUnassigned State = iota
	StateAssigned
	StateFinished
	StateQuarantined
)

func (s State) String() string {
	switch s {
	case StateUnassigned:
		return "UNASSIGNED"
	case StateAssigned:
		return "ASSIGNED"
	case StateFinished:
		return "FINISHED"
	case StateQuarantined:
		return "QUARANTINED"
	}
	return "UNKNOWN"
}

// ReplyKind 是 RequestSplit 的三种结果之一。
type ReplyKind int

const (
	// ReplyAssigned 表示分配到了一个拆分（Split 字段有效）。
	ReplyAssigned ReplyKind = iota
	// ReplyWait 表示暂无可分配拆分，但未来可能出现。
	ReplyWait
	// ReplyNoMore 表示已封口且不存在任何 UNASSIGNED 或 ASSIGNED 拆分。
	ReplyNoMore
)

func (k ReplyKind) String() string {
	switch k {
	case ReplyAssigned:
		return "ASSIGNED"
	case ReplyWait:
		return "WAIT"
	case ReplyNoMore:
		return "NO_MORE"
	}
	return "UNKNOWN"
}

// Reply 是 RequestSplit 的返回值。
type Reply struct {
	Kind  ReplyKind
	Split int64
}

// Failure 是 ReaderFailed 的返回清单，三个清单互不相交且均按编号升序。
type Failure struct {
	Returned    []int64 // 归还为 UNASSIGNED 的拆分
	Quarantined []int64 // 归还次数达到上限被隔离的拆分
	Revoked     []int64 // 完成状态被撤销、回到 ASSIGNED 的拆分
}

// SplitInfo 是 State 查询的结果。Owner 在无属主时为 -1。
type SplitInfo struct {
	State State
	Owner int
	Ret   int
}

// Counts 是各状态的拆分数。
type Counts struct {
	Unassigned  int
	Assigned    int
	Finished    int
	Quarantined int
}

// split 是拆分的内部记录。
type split struct {
	id        int64
	pref      int // 偏好读取器 = id mod R，注册后不变
	state     State
	owner     int // 无属主时为 -1
	at        int64
	ft        int64
	ret       int
	heapIndex int // 在 pools[pref] 中的下标，不在堆中时为 -1
}

// Allocator 是拆分分配器。所有方法可并发调用，效果等价于某个串行顺序。
type Allocator struct {
	mu     sync.Mutex
	r      int
	a      int
	splits map[int64]*split
	pools  []splitHeap          // 按偏好读取器分桶的 UNASSIGNED 最小堆
	held   []map[int64]struct{} // 每个读取器名下 ASSIGNED/FINISHED 的拆分
	alive  []bool               // 读取器是否存活
	lt     int64                // 最近触发的检查点号
	done   int64                // 最近完成的检查点号
	sealed bool
	counts [4]int // 按 State 索引的计数

	probes int64 // RequestSplit 累计查看的候选堆顶数
	owned  int64 // ReaderFailed 累计逐个处理的拆分数
}

// New 构造分配器。readers 必须在 [1, 64]，quarantineAfter 必须在 [1, 100]，
// 否则返回 ErrInvalidArgument。
func New(readers, quarantineAfter int) (*Allocator, error) {
	if readers < 1 || readers > 64 || quarantineAfter < 1 || quarantineAfter > 100 {
		return nil, fmt.Errorf("%w: readers=%d quarantineAfter=%d", ErrInvalidArgument, readers, quarantineAfter)
	}
	a := &Allocator{
		r:      readers,
		a:      quarantineAfter,
		splits: make(map[int64]*split),
		pools:  make([]splitHeap, readers),
		held:   make([]map[int64]struct{}, readers),
		alive:  make([]bool, readers),
	}
	for i := range a.held {
		a.held[i] = make(map[int64]struct{})
		a.alive[i] = true
	}
	return a, nil
}

// epoch 返回当前纪元 E = lt + 1。调用方须持有锁。
func (a *Allocator) epoch() int64 { return a.lt + 1 }

// AddSplits 登记一批新拆分。列表长度须在 [1, 1000]，编号须在 [0, 1e9]；
// 封口后拒绝；列表内重复或与任何曾登记编号重复则整个列表不生效。
func (a *Allocator) AddSplits(ids []int64) error {
	if len(ids) < 1 || len(ids) > 1000 {
		return fmt.Errorf("%w: batch size %d", ErrInvalidArgument, len(ids))
	}
	for _, id := range ids {
		if id < 0 || id > maxSplitID {
			return fmt.Errorf("%w: split id %d", ErrInvalidArgument, id)
		}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.sealed {
		return ErrSealed
	}
	batch := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if _, dup := batch[id]; dup {
			return fmt.Errorf("%w: split %d twice in batch", ErrDuplicateSplit, id)
		}
		if _, exists := a.splits[id]; exists {
			return fmt.Errorf("%w: split %d already registered", ErrDuplicateSplit, id)
		}
		batch[id] = struct{}{}
	}
	for _, id := range ids {
		sp := &split{id: id, pref: int(id % int64(a.r)), state: StateUnassigned, owner: -1, heapIndex: -1}
		a.splits[id] = sp
		a.pools[sp.pref].push(sp)
		a.counts[StateUnassigned]++
	}
	return nil
}

// Seal 封口，幂等。封口后 AddSplits 被拒绝。
func (a *Allocator) Seal() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.sealed = true
}

// Checkpoint 触发检查点 cp，要求 cp 恰等于 lt+1。
func (a *Allocator) Checkpoint(cp int64) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if cp != a.lt+1 {
		return fmt.Errorf("%w: got %d, want %d", ErrCheckpointOutOfOrder, cp, a.lt+1)
	}
	a.lt = cp
	return nil
}

// Complete 完成检查点 cp，要求 cp >= 1、cp <= lt 且 cp > done。
func (a *Allocator) Complete(cp int64) error {
	if cp < 1 {
		return fmt.Errorf("%w: cp %d < 1", ErrInvalidArgument, cp)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if cp > a.lt {
		return fmt.Errorf("%w: cp %d > lt %d", ErrCompleteAhead, cp, a.lt)
	}
	if cp <= a.done {
		return fmt.Errorf("%w: cp %d <= done %d", ErrCompleteStale, cp, a.done)
	}
	a.done = cp
	return nil
}

// RequestSplit 为读取器 r 请求一个拆分。
func (a *Allocator) RequestSplit(r int) (Reply, error) {
	if r < 0 || r >= a.r {
		return Reply{}, fmt.Errorf("%w: reader %d", ErrInvalidArgument, r)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.alive[r] {
		return Reply{}, fmt.Errorf("%w: reader %d", ErrReaderFailed, r)
	}
	var pick *split
	a.probes++ // 查看本读取器偏好堆顶
	if top := a.pools[r].peek(); top != nil {
		pick = top
	} else {
		// 偷取：只看偏好读取器当前已失败的堆。
		for q := 0; q < a.r; q++ {
			if a.alive[q] {
				continue
			}
			a.probes++ // 查看一个已失败读取器的偏好堆顶
			if top := a.pools[q].peek(); top != nil && (pick == nil || top.id < pick.id) {
				pick = top
			}
		}
	}
	if pick != nil {
		return a.assignLocked(r, pick), nil
	}
	if a.sealed && a.counts[StateUnassigned] == 0 && a.counts[StateAssigned] == 0 {
		return Reply{Kind: ReplyNoMore}, nil
	}
	return Reply{Kind: ReplyWait}, nil
}

// assignLocked 把 sp 分配给读取器 r。调用方须持有锁。
func (a *Allocator) assignLocked(r int, sp *split) Reply {
	a.pools[sp.pref].remove(sp)
	sp.state = StateAssigned
	sp.owner = r
	sp.at = a.epoch()
	a.counts[StateUnassigned]--
	a.counts[StateAssigned]++
	a.held[r][sp.id] = struct{}{}
	return Reply{Kind: ReplyAssigned, Split: sp.id}
}

// Finished 把拆分 s 标记为完成，要求 s 为 ASSIGNED 且属主为 r。
func (a *Allocator) Finished(r int, s int64) error {
	if r < 0 || r >= a.r || s < 0 || s > maxSplitID {
		return fmt.Errorf("%w: reader %d split %d", ErrInvalidArgument, r, s)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.alive[r] {
		return fmt.Errorf("%w: reader %d", ErrReaderFailed, r)
	}
	sp, ok := a.splits[s]
	if !ok || sp.state != StateAssigned || sp.owner != r {
		return fmt.Errorf("%w: reader %d split %d", ErrNotAssignedToReader, r, s)
	}
	sp.state = StateFinished
	sp.ft = a.epoch()
	a.counts[StateAssigned]--
	a.counts[StateFinished]++
	return nil
}

// ReaderFailed 令读取器 r 失败，并按编号升序原子地处理其名下
// ASSIGNED/FINISHED 拆分：at > done 的归还（ret 达到上限则隔离），
// 否则 ft > done 的 FINISHED 撤销完成，其余保留。
func (a *Allocator) ReaderFailed(r int) (Failure, error) {
	res := Failure{Returned: []int64{}, Quarantined: []int64{}, Revoked: []int64{}}
	if r < 0 || r >= a.r {
		return res, fmt.Errorf("%w: reader %d", ErrInvalidArgument, r)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.alive[r] {
		return res, fmt.Errorf("%w: reader %d", ErrReaderFailed, r)
	}
	a.alive[r] = false
	ids := make([]int64, 0, len(a.held[r]))
	for id := range a.held[r] {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	for _, id := range ids {
		a.owned++
		sp := a.splits[id]
		if sp.at > a.done {
			// 分配发生在最近已完成检查点之后：恢复出的状态里没有它，归还。
			delete(a.held[r], id)
			a.counts[sp.state]--
			sp.owner = -1
			sp.ret++
			if sp.ret >= a.a {
				sp.state = StateQuarantined
				a.counts[StateQuarantined]++
				res.Quarantined = append(res.Quarantined, id)
			} else {
				sp.state = StateUnassigned
				a.counts[StateUnassigned]++
				a.pools[sp.pref].push(sp)
				res.Returned = append(res.Returned, id)
			}
		} else if sp.state == StateFinished && sp.ft > a.done {
			// 完成发生在最近已完成检查点之后：恢复后需重新读取，撤销完成。
			sp.state = StateAssigned
			a.counts[StateFinished]--
			a.counts[StateAssigned]++
			res.Revoked = append(res.Revoked, id)
		}
		// 其余（at <= done 且未完成，或完成已持久化）保留不变。
	}
	return res, nil
}

// ReaderRestarted 令已失败的读取器 r 重新存活。
func (a *Allocator) ReaderRestarted(r int) error {
	if r < 0 || r >= a.r {
		return fmt.Errorf("%w: reader %d", ErrInvalidArgument, r)
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.alive[r] {
		return fmt.Errorf("%w: reader %d", ErrReaderNotFailed, r)
	}
	a.alive[r] = true
	return nil
}

// State 查询拆分 s 的状态、属主与被归还次数；s 从未登记时 ok 为 false。
func (a *Allocator) State(s int64) (info SplitInfo, ok bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	sp, exists := a.splits[s]
	if !exists {
		return SplitInfo{}, false
	}
	return SplitInfo{State: sp.state, Owner: sp.owner, Ret: sp.ret}, true
}

// Quarantined 按编号升序返回所有被隔离的拆分。
func (a *Allocator) Quarantined() []int64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]int64, 0, a.counts[StateQuarantined])
	for id, sp := range a.splits {
		if sp.state == StateQuarantined {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Counts 返回各状态的拆分数。
func (a *Allocator) Counts() Counts {
	a.mu.Lock()
	defer a.mu.Unlock()
	return Counts{
		Unassigned:  a.counts[StateUnassigned],
		Assigned:    a.counts[StateAssigned],
		Finished:    a.counts[StateFinished],
		Quarantined: a.counts[StateQuarantined],
	}
}
