package compensate

import (
	"fmt"
	"sort"
)

// BranchSpec 声明一条副作用分支及其依赖（Deps 中每条分支须先完全生效）。
type BranchSpec struct {
	Name   string
	Deps   []string
	DepSet map[string]bool // 声明阶段由 Deps 归一化得到，执行期只读
	Ops    []WriteOp
}

// ActionSpec 声明一次动作的全部分支。
type ActionSpec struct {
	ID       string
	Branches []BranchSpec
}

// Action 是通过声明校验（含环检测）后的可执行动作句柄。
type Action struct {
	spec   ActionSpec
	decl   *declaredSpec
	engine *Engine
	rt     *runtime
}

// declaredSpec 是声明阶段计算出的不可变结果。
// 执行期可变状态在每次 Execute 时独立创建，因此同一声明可被多次执行，动作间互不影响。
type declaredSpec struct {
	order      []string            // 依赖边“依赖->本分支”的一个合法拓扑顺序（参考实现使用）
	dependents map[string][]string // branch -> 直接依赖它的分支（补偿调度的下行方向）
	byName     map[string]*BranchSpec
}

// ID 返回动作标识。
func (a *Action) ID() string {
	if a == nil {
		return ""
	}
	return a.spec.ID
}

// Declare 在引擎上声明动作；在任何分支生效前完成环检测与其它静态校验。
// 被拒绝时返回错误且不触碰对象图，不产生任何可观察改动。
func (e *Engine) Declare(spec ActionSpec) (*Action, error) {
	decl, err := validate(spec)
	if err != nil {
		return nil, err
	}
	return &Action{spec: spec, decl: decl, engine: e}, nil
}

// validate 是纯计算，发生在任何分支生效之前。
func validate(spec ActionSpec) (*declaredSpec, error) {
	byName := make(map[string]*BranchSpec, len(spec.Branches))
	for i := range spec.Branches {
		b := &spec.Branches[i]
		if b.Name == "" {
			return nil, fmt.Errorf("declare %q: branch name must not be empty", spec.ID)
		}
		if _, dup := byName[b.Name]; dup {
			return nil, fmt.Errorf("declare %q: duplicate branch name %q", spec.ID, b.Name)
		}
		byName[b.Name] = b
	}

	indegree := make(map[string]int, len(spec.Branches))
	dependents := make(map[string][]string, len(spec.Branches))
	for i := range spec.Branches {
		b := &spec.Branches[i]
		b.DepSet = make(map[string]bool, len(b.Deps))
		for _, dep := range b.Deps {
			if _, ok := byName[dep]; !ok {
				return nil, fmt.Errorf("declare %q: branch %q depends on unknown branch %q", spec.ID, b.Name, dep)
			}
			if dep == b.Name {
				return nil, cycleError(spec.ID, []string{b.Name})
			}
			if b.DepSet[dep] {
				return nil, fmt.Errorf("declare %q: branch %q lists duplicate dependency %q", spec.ID, b.Name, dep)
			}
			b.DepSet[dep] = true
			indegree[b.Name]++
			dependents[dep] = append(dependents[dep], b.Name)
		}
	}

	// Kahn 拓扑排序检测环：边 dep -> branch。无法被排序的分支恰好位于环上。
	queue := make([]string, 0, len(spec.Branches))
	for _, b := range spec.Branches {
		if indegree[b.Name] == 0 {
			queue = append(queue, b.Name)
		}
	}
	order := make([]string, 0, len(spec.Branches))
	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		order = append(order, cur)
		for _, down := range dependents[cur] {
			indegree[down]--
			if indegree[down] == 0 {
				queue = append(queue, down)
			}
		}
	}
	if len(order) != len(spec.Branches) {
		onCycle := make([]string, 0)
		for _, b := range spec.Branches {
			if indegree[b.Name] > 0 {
				onCycle = append(onCycle, b.Name)
			}
		}
		return nil, cycleError(spec.ID, onCycle)
	}

	if err := validateWriteSetDisjoint(spec, byName); err != nil {
		return nil, err
	}

	return &declaredSpec{order: order, dependents: dependents, byName: byName}, nil
}

// validateWriteSetDisjoint 保证无依赖关系的两条分支不会写同一对象图槽位。
// 无依赖的分支允许任意交织生效与并发补偿；写入集不相交时其提交/逆序操作天然可交换，
// 于是无论相对快慢，最终对象图都等价于某个合法拓扑顺序串行补偿的结果。
// 存在（传递）依赖的分支有确定先后次序，允许写同一槽位。
func validateWriteSetDisjoint(spec ActionSpec, byName map[string]*BranchSpec) error {
	owner := map[string]string{}
	for i := range spec.Branches {
		b := &spec.Branches[i]
		seen := map[string]bool{}
		for _, op := range b.Ops {
			slot := slotKey(op.ObjectID, op.Property)
			if seen[slot] {
				continue
			}
			seen[slot] = true
			if other, clash := owner[slot]; clash && !related(b, other, byName) {
				return fmt.Errorf("declare %q: independent branches %q and %q both write slot(%s,%s); concurrent interleaving would not be serializable",
					spec.ID, other, b.Name, op.ObjectID, op.Property)
			}
			owner[slot] = b.Name
		}
	}
	return nil
}

// related 判断 x 与 yName 之间是否存在任一方向的传递依赖关系。
func related(x *BranchSpec, yName string, byName map[string]*BranchSpec) bool {
	if ancestors(x.Name, byName)[yName] {
		return true
	}
	return ancestors(yName, byName)[x.Name]
}

// ancestors 返回某分支的全部（传递）上游分支名。
func ancestors(name string, byName map[string]*BranchSpec) map[string]bool {
	out := map[string]bool{}
	var dfs func(string)
	dfs = func(cur string) {
		b := byName[cur]
		if b == nil {
			return
		}
		for d := range b.DepSet {
			if !out[d] {
				out[d] = true
				dfs(d)
			}
		}
	}
	dfs(name)
	return out
}

func cycleError(actionID string, onCycle []string) error {
	return ErrorRecord{Kind: KindDependencyCycle, ActionID: actionID, Branch: joinNames(onCycle),
		OpIndex: -1, Message: "dependency cycle detected among: " + joinNames(onCycle)}
}

func joinNames(ns []string) string {
	out := ""
	for i, n := range ns {
		if i > 0 {
			out += ","
		}
		out += n
	}
	return out
}

// Engine 承载对象图、锁管理器与审计日志。
type Engine struct {
	graph  *Graph
	logger Logger
}

func NewEngine(graph *Graph, logger Logger) *Engine {
	return &Engine{graph: graph, logger: logger}
}

// slotPair 标识一个对象图写入槽位。
type slotPair struct{ objectID, property string }

// allWriteSlots 返回动作声明中去重后的全部写入槽位，按全局顺序排序。
// 用于动作级严格 2PL：统一顺序加锁可杜绝跨动作加锁顺序环。
func (a *Action) allWriteSlots() []slotPair {
	uniq := map[slotPair]bool{}
	for _, b := range a.spec.Branches {
		for _, op := range b.Ops {
			uniq[slotPair{op.ObjectID, op.Property}] = true
		}
	}
	out := make([]slotPair, 0, len(uniq))
	for s := range uniq {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].objectID != out[j].objectID {
			return out[i].objectID < out[j].objectID
		}
		return out[i].property < out[j].property
	})
	return out
}
