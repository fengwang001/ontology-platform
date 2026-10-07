package aggview

import (
	"fmt"
	"math/big"
	"sort"
)

// Logger 记录每次变更的输入、受影响分组与判定依据。
type Logger interface {
	Log(ev Event)
}

// Event 是一次变更的审计日志条目。
type Event struct {
	Op       string
	Input    string
	Reason   string
	Affected []GroupContribution
	Err      string
}

// GroupContribution 描述某个分组聚合结果的增量调整。
type GroupContribution struct {
	View   ID
	Group  ID
	Delta  *big.Rat
	DCount int
}

// Touched 标识一份被触及的分组聚合条目（视图 × 分组）。
type Touched struct {
	View  ID
	Group ID
}

// Change 描述一次提交后的原子变更，供日志与常数份更新证明使用。
type Change struct {
	Op      string
	Input   string
	Reason  string
	touched []Touched
}

// TouchedGroups 返回本次变更触及的分组聚合条目份数（按 视图×分组 去重）。
// 单次归属改变在单条链接层面恒为常数：新增 1 份、移除 1 份、替换至多 2 份。
func (c *Change) TouchedGroups() int { return len(c.Touched()) }

// Touched 返回去重后的触及条目（测试做常数份证明时使用）。
func (c *Change) Touched() []Touched {
	seen := map[Touched]bool{}
	var out []Touched
	for _, t := range c.touched {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].View != out[j].View {
			return out[i].View < out[j].View
		}
		return out[i].Group < out[j].Group
	})
	return out
}

// Engine 在 Store 之上增量维护一组聚合视图。
// 所有写操作经由同一把互斥锁串行化，因此属性写入与归属改变即便“同时发生”，
// 在引擎内部也必然按某个全序逐一应用，天然可串行化。
type Engine struct {
	store  *Store
	log    Logger
	mu     lock
	views  map[ID]*view
	byLink map[ID][]*view

	// fail 非 nil 时，每个处理单元在校验通过、全部聚合更新完成后调用；
	// 返回错误则整个处理单元回滚。用于验证处理单元失败的原子回滚。
	fail func(op string) error
}

func NewEngine(st *Store, log Logger) *Engine {
	return &Engine{
		store:  st,
		log:    log,
		views:  map[ID]*view{},
		byLink: map[ID][]*view{},
	}
}

// SetFailHook 注入处理单元失败钩子（测试使用）。
func (e *Engine) SetFailHook(f func(op string) error) { e.fail = f }

// RegisterView 注册聚合视图；要求注册时相关实例尚未创建，
// 即视图先于数据声明（见设计说明中放弃的“注册时回溯”方案）。
func (e *Engine) RegisterView(d ViewDef) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := validateDef(d); err != nil {
		return err
	}
	if _, exists := e.views[d.Name]; exists {
		return fmt.Errorf("aggview: duplicate view name %q", d.Name)
	}
	vw := newView(d)
	e.views[d.Name] = vw
	for _, src := range d.Sources {
		e.byLink[src.LinkType] = append(e.byLink[src.LinkType], vw)
	}
	return nil
}

// txn 在单一互斥锁内运行一个处理单元：校验/更新失败则整体回滚。
func (e *Engine) txn(op, input string, mutate func(m *mutator) error) (*Change, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	t := newTxn(e.store)
	chg := &Change{Op: op, Input: input}
	m := &mutator{txn: t, eng: e, chg: chg}
	if err := mutate(m); err != nil {
		t.rollback()
		e.emit(Event{Op: op, Input: input, Reason: m.reason, Err: err.Error()})
		return nil, err
	}
	if e.fail != nil {
		if err := e.fail(op); err != nil {
			t.rollback()
			wrapped := &Error{kind: KindUpdateFailed,
				msg: fmt.Sprintf("%s: %v", errUpdateFailed.msg, err)}
			e.emit(Event{Op: op, Input: input, Reason: m.reason, Err: wrapped.Error()})
			return nil, wrapped
		}
	}
	chg.Reason = m.reason
	chg.touched = append(chg.touched, m.touched...)
	e.emit(Event{
		Op:       op,
		Input:    input,
		Reason:   m.reason,
		Affected: append([]GroupContribution(nil), m.affected...),
	})
	return chg, nil
}

func (e *Engine) emit(ev Event) {
	if e.log != nil {
		e.log.Log(ev)
	}
}

func (m *mutator) apply(vw *view, group, objType ID, c *big.Rat, dCount int) {
	vw.addTo(group, objType, c, dCount)
	m.txn.push(func() { vw.addTo(group, objType, new(big.Rat).Neg(c), -dCount) })
	m.affected = append(m.affected, GroupContribution{
		View: vw.def.Name, Group: group,
		Delta: new(big.Rat).Set(c), DCount: dCount,
	})
	m.touched = append(m.touched, Touched{View: vw.def.Name, Group: group})
}

func (m *mutator) bumpVersion(vw *view, member, objType ID) {
	vw.bumpVersion(member, objType)
	m.txn.push(func() { vw.versions[member][objType]-- })
}

func (m *mutator) deleteBucket(vw *view, group ID) {
	saved := vw.agg[group]
	delete(vw.agg, group)
	m.txn.push(func() {
		if saved != nil {
			vw.agg[group] = saved
		}
	})
}

func countDelta(present bool, n int) int {
	if present {
		return n
	}
	return 0
}

// reshare 处理 EvenShare 策略下“留下”分组在归属数变化时的份额微调。
// Full 策略或属性不存在时无需微调。
func (m *mutator) reshare(vw *view, sd *sourceDef, stay []ID, kOld, kNew int, val Value) {
	if !val.Present || sd.src.Policy == PolicyFull || kOld == 0 || kNew == 0 {
		return
	}
	old := sd.contribution(val, kOld)
	now := sd.contribution(val, kNew)
	delta := new(big.Rat).Sub(now, old)
	for _, g := range stay {
		m.apply(vw, g, sd.src.ObjectType, delta, 0)
	}
}

func formatValue(v Value) string {
	if !v.Present {
		return "<absent>"
	}
	return v.Rat.RatString()
}

func without(groups []ID, x ID) []ID {
	var out []ID
	for _, g := range groups {
		if g != x {
			out = append(out, g)
		}
	}
	return out
}

func contains(groups []ID, x ID) bool {
	for _, g := range groups {
		if g == x {
			return true
		}
	}
	return false
}

func max1(n int) int {
	if n < 1 {
		return 1
	}
	return n
}
