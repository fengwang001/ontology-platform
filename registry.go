package ontology

import (
	"errors"
	"fmt"
)

// snapshot 是注册表在某一时刻的不可变快照。
// 注册/放弃采用 copy-on-write 生成新快照，在途调用持有的旧快照不受影响。
type snapshot struct {
	ver   int64
	table map[string]map[string]*installNode // action -> type -> node
}

// Registry 管理动作声明与不可变注册表快照。
type Registry struct {
	actions map[string]*Action
}

func newRegistry() *Registry {
	return &Registry{actions: map[string]*Action{}}
}

func newSnapshot() *snapshot {
	return &snapshot{ver: 0, table: map[string]map[string]*installNode{}}
}

func (r *Registry) declare(a Action) error {
	if a.Name == "" {
		return errors.New("declare: action name is empty")
	}
	if _, dup := r.actions[a.Name]; dup {
		return fmt.Errorf("declare: action %q already declared", a.Name)
	}
	r.actions[a.Name] = &a
	return nil
}

func (r *Registry) action(name string) *Action {
	return r.actions[name]
}

func cloneSnapshot(snap *snapshot, seq int64) *snapshot {
	if snap == nil {
		snap = newSnapshot()
	}
	table := make(map[string]map[string]*installNode, len(snap.table))
	for action, byType := range snap.table {
		cp := make(map[string]*installNode, len(byType))
		for typeName, node := range byType {
			cp[typeName] = node
		}
		table[action] = cp
	}
	return &snapshot{ver: seq, table: table}
}

// install 在给定快照上生成“注册/替换执行逻辑”的新快照。
// 旧节点挂在新节点的 prev 上：在途调用持有旧指针不受影响，审计也可回溯。
func install(snap *snapshot, action, typeName string, logic Logic, relax RelaxScope, seq int64) *snapshot {
	next := cloneSnapshot(snap, seq)
	byType := next.table[action]
	if byType == nil {
		byType = map[string]*installNode{}
		next.table[action] = byType
	}
	byType[typeName] = &installNode{
		logic: &logic,
		state: stateActive,
		relax: relax,
		seq:   seq,
		prev:  byType[typeName],
	}
	return next
}

// waive 在给定快照上生成“显式放弃直接处理”的新快照。
// 放弃本身也是一种可被替换的注册状态，同样保留历史指针。
func waive(snap *snapshot, action, typeName string, seq int64) *snapshot {
	next := cloneSnapshot(snap, seq)
	byType := next.table[action]
	if byType == nil {
		byType = map[string]*installNode{}
		next.table[action] = byType
	}
	byType[typeName] = &installNode{
		state: stateWaived,
		seq:   seq,
		prev:  byType[typeName],
	}
	return next
}

// resolveOnChain 在不可变快照上沿继承来源链向上查找第一个生效注册。
//
// 返回值区分三种结果：
//   - basis == HitDirect / HitInherited：命中某层的生效逻辑；
//   - basis == HitNone 且 sawWaive == false：具体类型及全部来源均未注册；
//   - basis == HitNone 且 sawWaive == true：查找过程中遇到过显式放弃，
//     但继承来源上同样没有可用逻辑。
//
// 只遍历 chain 自身（长度为具体类型到其祖先的实际深度），不扫描注册表中
// 其他类型，因此成本与系统中其他注册类型的数量无关。
func resolveOnChain(snap *snapshot, action string, chain []*ObjectType) (node *installNode, hitType string, basis HitBasis, path []string, steps int, sawWaive bool) {
	byType := snap.table[action]
	for i, t := range chain {
		path = append(path, t.Name)
		steps++
		var n *installNode
		if byType != nil {
			n = byType[t.Name]
		}
		if n == nil || n.state == stateAbsent {
			continue
		}
		if n.state == stateWaived {
			sawWaive = true // 显式放弃：可区分于“从未注册”，继续向上
			continue
		}
		if i == 0 {
			basis = HitDirect
		} else {
			basis = HitInherited
		}
		return n, t.Name, basis, path, steps, sawWaive
	}
	return nil, "", HitNone, path, steps, sawWaive
}
