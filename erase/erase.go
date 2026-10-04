// Package erase 在 store 与 refs 之上执行数据主体擦除：
// 计算删除集 D、摘除共有归属、匿名化被保全种子、断开指向 D 的引用，
// 并维护时钟、法律保全与墓碑。Executor 的所有方法可并发调用，
// 结果等价于某个串行顺序。
package erase

import (
	"errors"
	"fmt"
	"slices"
	"sync"

	"ontology/refs"
	"ontology/store"
)

var (
	ErrInvalidArgument = store.ErrInvalidArgument
	ErrClockRegression = store.ErrClockRegression
	ErrExists          = store.ErrExists
	ErrNotFound        = store.ErrNotFound
	ErrTooManyOutgoing = store.ErrTooManyOutgoing
	ErrErased          = errors.New("subject is tombstoned")
	ErrAlreadyErased   = errors.New("subject already erased")
	ErrRestricted      = errors.New("restricted reference blocks erase")
)

// RestrictedError 携带阻止擦除的 Restrict 引用信息：
// Record 为 D 内被这样引用的最小记录 id，Referrer 为其 Restrict 引用者中的最小 id。
type RestrictedError struct {
	Record   int64
	Referrer int64
}

func (e *RestrictedError) Error() string {
	return fmt.Sprintf("%v: record %d restrict-referenced by %d", ErrRestricted, e.Record, e.Referrer)
}

func (e *RestrictedError) Is(target error) bool { return target == ErrRestricted }

// Report 是一次成功擦除（或计划）的处置报告，各字段均已排序。
type Report struct {
	Deleted    []int64
	Detached   []int64
	Anonymized []int64
	Unlinked   []refs.Edge
}

type Executor struct {
	mu         sync.Mutex
	store      *store.Store
	graph      *refs.Graph
	holds      map[int64]int64
	tombstones map[string]struct{}
	clock      int64
	visited    int
}

func NewExecutor(st *store.Store, g *refs.Graph) *Executor {
	return &Executor{
		store:      st,
		graph:      g,
		holds:      make(map[int64]int64),
		tombstones: make(map[string]struct{}),
	}
}

func (e *Executor) checkClock(now int64) error {
	if now < e.clock {
		return ErrClockRegression
	}
	return nil
}

func (e *Executor) tombstoned(subject string) bool {
	_, ok := e.tombstones[subject]
	return ok
}

func (e *Executor) held(id, now int64) bool {
	until, ok := e.holds[id]
	return ok && now < until
}

// Put 登记记录。拒绝次序：参数非法 > 时钟回退 > 已存在 > ErrErased。
func (e *Executor) Put(id int64, owners []string, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !store.ValidID(id) || store.ValidateOwners(owners) != nil || !store.ValidNow(now) {
		return ErrInvalidArgument
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	if e.store.Exists(id) {
		return ErrExists
	}
	for _, o := range owners {
		if e.tombstoned(o) {
			return ErrErased
		}
	}
	if err := e.store.Add(id, owners); err != nil {
		return err
	}
	e.clock = now
	return nil
}

// AddRef 登记引用边。拒绝次序：参数非法 > 时钟回退 > 记录不存在 > 已存在 > 出边超限。
func (e *Executor) AddRef(child, parent int64, p refs.Policy, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !store.ValidID(child) || !store.ValidID(parent) || child == parent || !p.Valid() || !store.ValidNow(now) {
		return ErrInvalidArgument
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	if !e.store.Exists(child) || !e.store.Exists(parent) {
		return ErrNotFound
	}
	if e.graph.Has(child, parent) {
		return ErrExists
	}
	if err := e.graph.Add(child, parent, p); err != nil {
		return err
	}
	e.clock = now
	return nil
}

// RemoveRef 删除引用边。拒绝次序：参数非法 > 时钟回退 > 记录或边不存在。
func (e *Executor) RemoveRef(child, parent int64, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !store.ValidID(child) || !store.ValidID(parent) || child == parent || !store.ValidNow(now) {
		return ErrInvalidArgument
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	if !e.store.Exists(child) || !e.store.Exists(parent) || !e.graph.Has(child, parent) {
		return ErrNotFound
	}
	if err := e.graph.Remove(child, parent); err != nil {
		return err
	}
	e.clock = now
	return nil
}

// Hold 给记录加法律保全，until 须严格大于 now，重复调用覆盖。恰等到期即失效。
func (e *Executor) Hold(id, until, now int64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !store.ValidID(id) || !store.ValidNow(now) || until <= now {
		return ErrInvalidArgument
	}
	if err := e.checkClock(now); err != nil {
		return err
	}
	if !e.store.Exists(id) {
		return ErrNotFound
	}
	e.holds[id] = until
	e.clock = now
	return nil
}

// Erase 计算并应用主体 s 的擦除；被 Restrict 阻止时不改任何状态。
func (e *Executor) Erase(subject string, now int64) (Report, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	rep, err := e.prepare(subject, now)
	if err != nil {
		return Report{}, err
	}
	e.apply(subject, now, rep)
	return rep, nil
}

// Plan 只读地返回与此时执行 Erase 完全相同的报告或错误，不改任何状态。
func (e *Executor) Plan(subject string, now int64) (Report, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.prepare(subject, now)
}

// prepare 校验并计算计划。拒绝次序：参数非法 > 时钟回退 > ErrAlreadyErased > ErrRestricted。
func (e *Executor) prepare(subject string, now int64) (Report, error) {
	if subject == "" || !store.ValidNow(now) {
		return Report{}, ErrInvalidArgument
	}
	if err := e.checkClock(now); err != nil {
		return Report{}, err
	}
	if e.tombstoned(subject) {
		return Report{}, ErrAlreadyErased
	}
	rep, rerr := e.compute(subject, now)
	if rerr != nil {
		return Report{}, rerr
	}
	return rep, nil
}

// compute 只读计算擦除报告。visited 记账：每个种子计 1，沿 D 中记录
// 的每条入边考察引用者计 1。Unlinked 与 Restrict 候选先收集，待 D 定型后
// 过滤掉引用者最终进入 D 的边（那些边随记录一并消失）。
func (e *Executor) compute(subject string, now int64) (Report, *RestrictedError) {
	e.visited = 0
	rep := Report{
		Deleted:    []int64{},
		Detached:   []int64{},
		Anonymized: []int64{},
		Unlinked:   []refs.Edge{},
	}
	seeds := e.store.OwnedBy(subject)
	e.visited += len(seeds)
	inD := make(map[int64]bool)
	var queue []int64
	for _, id := range seeds {
		if len(e.store.Owners(id)) == 1 {
			if e.held(id, now) {
				rep.Anonymized = append(rep.Anonymized, id)
			} else {
				inD[id] = true
				queue = append(queue, id)
			}
		} else {
			rep.Detached = append(rep.Detached, id)
		}
	}
	var restrict []refs.Edge
	for len(queue) > 0 {
		d := queue[0]
		queue = queue[1:]
		for _, edge := range e.graph.In(d) {
			e.visited++
			referrer := edge.Child
			if inD[referrer] {
				continue
			}
			pol, _ := e.graph.Policy(referrer, d)
			if pol == refs.Cascade && len(e.store.Owners(referrer)) == 0 && !e.held(referrer, now) {
				inD[referrer] = true
				queue = append(queue, referrer)
				continue
			}
			if pol == refs.Restrict {
				restrict = append(restrict, edge)
			} else {
				rep.Unlinked = append(rep.Unlinked, edge)
			}
		}
	}
	kept := rep.Unlinked[:0]
	for _, edge := range rep.Unlinked {
		if !inD[edge.Child] {
			kept = append(kept, edge)
		}
	}
	rep.Unlinked = kept
	var hit *RestrictedError
	for _, edge := range restrict {
		if inD[edge.Child] {
			continue
		}
		if hit == nil || edge.Parent < hit.Record ||
			(edge.Parent == hit.Record && edge.Child < hit.Referrer) {
			hit = &RestrictedError{Record: edge.Parent, Referrer: edge.Child}
		}
	}
	if hit != nil {
		return Report{}, hit
	}
	for id := range inD {
		rep.Deleted = append(rep.Deleted, id)
	}
	slices.Sort(rep.Deleted)
	slices.SortFunc(rep.Unlinked, func(a, b refs.Edge) int {
		if a.Child != b.Child {
			return compare(a.Child, b.Child)
		}
		return compare(a.Parent, b.Parent)
	})
	return rep, nil
}

// apply 应用一次成功的擦除：断边、摘除、匿名化、删除、写墓碑、推进时钟。
func (e *Executor) apply(subject string, now int64, rep Report) {
	for _, edge := range rep.Unlinked {
		_ = e.graph.Remove(edge.Child, edge.Parent)
	}
	for _, id := range rep.Detached {
		e.store.RemoveOwner(id, subject)
	}
	for _, id := range rep.Anonymized {
		e.store.RemoveOwner(id, subject)
	}
	for _, id := range rep.Deleted {
		e.graph.RemoveRecord(id)
		e.store.Delete(id)
		delete(e.holds, id)
	}
	e.tombstones[subject] = struct{}{}
	e.clock = now
}

func compare(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}
