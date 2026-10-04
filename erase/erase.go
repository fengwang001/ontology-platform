// Package erase 在 store 与 refs 之上实现数据主体擦除的级联执行器。
package erase

import (
	"errors"
	"sort"
	"sync"

	"ontology/refs"
	"ontology/store"
)

const (
	maxID     int64 = 1_000_000_000
	maxNow    int64 = 1_000_000_000_000
	maxOwners       = 4
)

// 门面错误：参数、时钟、存在性等哨兵直接复用 store/refs。
var (
	ErrInvalid       = store.ErrInvalid
	ErrClockRollback = store.ErrClockRollback
	ErrExists        = store.ErrExists
	ErrNotFound      = store.ErrNotFound
	ErrTooManyRefs   = refs.ErrTooManyRefs

	// ErrAlreadyErased 表示主体已在墓碑中却再次 Erase。
	ErrAlreadyErased = errors.New("erase: subject already erased")
	// ErrErased 表示 Put 的 owners 含墓碑主体。
	ErrErased = errors.New("erase: owner subject has been erased")
)

// ErrRestricted 是 Restrict 阻止错误的哨兵；具体信息见 RestrictedError。
var ErrRestricted = &RestrictedError{}

// RestrictedError 携带 D 内被 Restrict 引用的最小记录 id 及其最小引用者 id。
type RestrictedError struct {
	Parent int64
	Child  int64
}

func (e *RestrictedError) Error() string { return "erase: restricted reference" }

// Is 使任意 *RestrictedError 都可被 errors.Is(err, ErrRestricted) 命中。
func (e *RestrictedError) Is(target error) bool {
	_, ok := target.(*RestrictedError)
	return ok
}

// Ref 是报告中的一条被断开引用。
type Ref struct {
	Child  int64
	Parent int64
}

// Report 是一次擦除的确定性报告。
type Report struct {
	Deleted    []int64
	Detached   []int64
	Anonymized []int64
	Unlinked   []Ref
}

// Executor 是级联执行器；单锁串行化全部操作。
type Executor struct {
	mu         sync.Mutex
	store      *store.Store
	graph      *refs.Graph
	tombstones map[string]struct{}
	now        int64
	visited    int64
}

// New 创建空执行器（时钟初始为 0）。
func New() *Executor {
	return &Executor{
		store:      store.New(),
		graph:      refs.New(),
		tombstones: map[string]struct{}{},
	}
}

// Visited 返回最近一次 Erase/Plan 考察的记录次数。
func (ex *Executor) Visited() int64 {
	ex.mu.Lock()
	defer ex.mu.Unlock()
	return ex.visited
}

// validOwners 校验：0..4 个互不相同的非空主体名。
func validOwners(owners []string) bool {
	if len(owners) > maxOwners {
		return false
	}
	seen := make(map[string]struct{}, len(owners))
	for _, s := range owners {
		if s == "" {
			return false
		}
		if _, dup := seen[s]; dup {
			return false
		}
		seen[s] = struct{}{}
	}
	return true
}

func validID(id int64) bool   { return 1 <= id && id <= maxID }
func validNow(now int64) bool { return 0 <= now && now <= maxNow }

func validPolicy(p refs.Policy) bool {
	return p == refs.Cascade || p == refs.SetNull || p == refs.Restrict
}

// sortedOwners 返回 owners 的升序副本（相等判断与稳定视图用）。
func sortedOwners(in []string) []string {
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// Put 登记记录。拒绝次序：参数非法 > 时钟回退 > 已存在 > ErrErased。
func (ex *Executor) Put(id int64, owners []string, now int64) error {
	if !validID(id) || !validOwners(owners) || !validNow(now) {
		return ErrInvalid
	}
	ex.mu.Lock()
	defer ex.mu.Unlock()
	if now < ex.now {
		return ErrClockRollback
	}
	if _, ok := ex.store.Get(id); ok {
		return ErrExists
	}
	for _, s := range owners {
		if _, erased := ex.tombstones[s]; erased {
			return ErrErased
		}
	}
	if err := ex.store.Put(id, owners); err != nil {
		return err
	}
	ex.now = now
	return nil
}

// AddRef 登记引用边。拒绝次序：参数非法 > 时钟回退 > 记录不存在 > 已存在 > 出边超限。
func (ex *Executor) AddRef(child, parent int64, p refs.Policy, now int64) error {
	if !validID(child) || !validID(parent) || child == parent || !validPolicy(p) || !validNow(now) {
		return ErrInvalid
	}
	ex.mu.Lock()
	defer ex.mu.Unlock()
	if now < ex.now {
		return ErrClockRollback
	}
	if _, ok := ex.store.Get(child); !ok {
		return ErrNotFound
	}
	if _, ok := ex.store.Get(parent); !ok {
		return ErrNotFound
	}
	if _, exists := ex.graph.Has(child, parent); exists {
		return ErrExists
	}
	if err := ex.graph.Add(refs.Edge{Child: child, Parent: parent, Policy: p}); err != nil {
		return err
	}
	ex.now = now
	return nil
}

// RemoveRef 删除引用边。拒绝次序：参数非法 > 时钟回退 > 记录或边不存在。
func (ex *Executor) RemoveRef(child, parent int64, now int64) error {
	if !validID(child) || !validID(parent) || child == parent || !validNow(now) {
		return ErrInvalid
	}
	ex.mu.Lock()
	defer ex.mu.Unlock()
	if now < ex.now {
		return ErrClockRollback
	}
	if err := ex.graph.Remove(child, parent); err != nil {
		return err
	}
	ex.now = now
	return nil
}

// Hold 给记录加法律保全。拒绝次序：参数非法 > 时钟回退 > 记录不存在。
func (ex *Executor) Hold(id int64, until, now int64) error {
	if !validID(id) || !validNow(now) || until <= now {
		return ErrInvalid
	}
	ex.mu.Lock()
	defer ex.mu.Unlock()
	if now < ex.now {
		return ErrClockRollback
	}
	if _, ok := ex.store.Get(id); !ok {
		return ErrNotFound
	}
	ex.store.Hold(id, until)
	ex.now = now
	return nil
}

// plan 是 Erase 与 Plan 共用的纯计算路径。返回报告（已排序）或错误；
// 不触碰任何状态（含时钟），由调用方决定是否提交。
func (ex *Executor) plan(s string, now int64) (*Report, error) {
	seeds := ex.store.Seeds(s)
	ex.visited = 0

	inD := map[int64]bool{}
	var D []int64
	var queue []int64
	var detached, anonymized []int64

	// 初始分类：每个种子取出计 1。
	for _, id := range seeds {
		ex.visited++
		r, _ := ex.store.Get(id)
		owners := sortedOwners(r.Owners)
		switch {
		case len(owners) == 1 && owners[0] == s && !ex.store.Held(id, now):
			inD[id] = true
			D = append(D, id)
			queue = append(queue, id)
		case len(owners) == 1 && owners[0] == s:
			anonymized = append(anonymized, id)
		default:
			detached = append(detached, id)
		}
	}

	// 级联封闭：沿入边考察引用者，每条入边计 1。
	for len(queue) > 0 {
		parent := queue[0]
		queue = queue[1:]
		for _, e := range ex.graph.InEdges(parent) {
			ex.visited++
			child := e.Child
			if inD[child] {
				continue
			}
			if e.Policy != refs.Cascade {
				continue
			}
			cr, ok := ex.store.Get(child)
			if !ok || cr.Anon || len(cr.Owners) != 0 || ex.store.Held(child, now) {
				continue
			}
			inD[child] = true
			D = append(D, child)
			queue = append(queue, child)
		}
	}

	// 收集 D 外 -> D 内的跨边：D 按 id 升序、每个 parent 的入边按 child 升序，
	// 因此第一条 Restrict 跨边即 (最小 parent, 最小 Restrict child)。
	sort.Slice(D, func(i, j int) bool { return D[i] < D[j] })
	var unlinked []Ref
	for _, parent := range D {
		for _, e := range ex.graph.InEdges(parent) {
			if inD[e.Child] {
				continue
			}
			if e.Policy == refs.Restrict {
				return nil, &RestrictedError{Parent: parent, Child: e.Child}
			}
			unlinked = append(unlinked, Ref{Child: e.Child, Parent: parent})
		}
	}
	// 报告口径为 (child,parent) 升序（Restrict 判定仍取 parent 优先的最小对）。
	sort.Slice(unlinked, func(i, j int) bool {
		if unlinked[i].Child != unlinked[j].Child {
			return unlinked[i].Child < unlinked[j].Child
		}
		return unlinked[i].Parent < unlinked[j].Parent
	})

	sort.Slice(detached, func(i, j int) bool { return detached[i] < detached[j] })
	sort.Slice(anonymized, func(i, j int) bool { return anonymized[i] < anonymized[j] })
	return &Report{
		Deleted:    D,
		Detached:   detached,
		Anonymized: anonymized,
		Unlinked:   unlinked,
	}, nil
}

// commit 按计划落地：先断开跨边与摘除/匿名，再删除 D 顶点（其关联边随顶点消失），
// 最后写墓碑并推进时钟。
func (ex *Executor) commit(s string, rep *Report, now int64) {
	for _, ref := range rep.Unlinked {
		_ = ex.graph.Remove(ref.Child, ref.Parent)
	}
	for _, id := range rep.Detached {
		ex.store.RemoveOwner(id, s)
	}
	for _, id := range rep.Anonymized {
		ex.store.Anonymize(id)
	}
	for _, id := range rep.Deleted {
		ex.graph.DeleteVertex(id)
		ex.store.Delete(id)
	}
	ex.tombstones[s] = struct{}{}
	ex.now = now
}

// Erase 执行一次主体擦除。拒绝次序：参数非法 > 时钟回退 > ErrAlreadyErased > ErrRestricted。
func (ex *Executor) Erase(s string, now int64) (*Report, error) {
	if s == "" || !validNow(now) {
		return nil, ErrInvalid
	}
	ex.mu.Lock()
	defer ex.mu.Unlock()
	if now < ex.now {
		return nil, ErrClockRollback
	}
	if _, ok := ex.tombstones[s]; ok {
		return nil, ErrAlreadyErased
	}
	rep, err := ex.plan(s, now)
	if err != nil {
		return nil, err
	}
	ex.commit(s, rep, now)
	return rep, nil
}

// Plan 只读返回与此刻执行 Erase 完全相同的结果，不推进时钟、不写墓碑。
func (ex *Executor) Plan(s string, now int64) (*Report, error) {
	if s == "" || !validNow(now) {
		return nil, ErrInvalid
	}
	ex.mu.Lock()
	defer ex.mu.Unlock()
	if now < ex.now {
		return nil, ErrClockRollback
	}
	if _, ok := ex.tombstones[s]; ok {
		return nil, ErrAlreadyErased
	}
	return ex.plan(s, now)
}
