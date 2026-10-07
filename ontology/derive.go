package ontology

import "sort"

// IndexState 表示某个下游实例在某个派生属性上的索引状态。
type IndexState struct {
	Status IndexStatus
	Value  PropertyValue
}

type IndexStatus int

const (
	StateIndexable IndexStatus = iota
	StateMissingSource
	StateNotUnique
	StateNoValue
)

type nodeKey struct {
	id   ObjectID
	prop PropertyName
}

// evaluate 依据当前（事务工作副本中的）真实链接结构，求实例 id 在派生声明 d 上的索引状态。
//
// 状态判定规则（全部基于此刻的真实状态，绝不选取“任意一个”目标）：
//   - 链接目标数为 0：StateMissingSource（不可索引：来源缺失）
//   - 链接目标数 > 1：StateNotUnique（不可索引：声明要求唯一但不唯一）
//   - 恰好 1 个目标、目标实例不存在：StateMissingSource
//   - 恰好 1 个目标、目标属性无值：StateNoValue（不可索引：源属性未设置）
//   - 恰好 1 个目标且取到值：StateIndexable，Value 为该值；若来源属性本身也是派生属性，
//     递归采用来源实例在该派生属性上已经求出的当前状态（多级传递）。
func evaluate(s *storeState, schema *Schema, id ObjectID, d DerivedIndex) IndexState {
	return evalLoop(s, schema, id, d, map[nodeKey]bool{})
}

func evalLoop(s *storeState, schema *Schema, id ObjectID, d DerivedIndex,
	onPath map[nodeKey]bool) IndexState {
	ins, ok := s.inst[id]
	if !ok {
		return IndexState{Status: StateMissingSource}
	}
	targets := ins.out[d.Link]
	if len(targets) == 0 {
		return IndexState{Status: StateMissingSource}
	}
	if len(targets) > 1 {
		return IndexState{Status: StateNotUnique}
	}
	var targetID ObjectID
	for to := range targets {
		targetID = to
	}
	target, ok := s.inst[targetID]
	if !ok {
		return IndexState{Status: StateMissingSource}
	}
	if srcDerived, isDerived := schema.derivedIndex(target.typ, d.DstProp); isDerived {
		key := nodeKey{targetID, d.DstProp}
		// 多级传递：来源属性本身也是派生值。模式注册阶段已保证派生 DAG 无环，
		// onPath 是双保险：一旦出现环路，立即按不可索引处理而非死循环。
		if onPath[key] {
			return IndexState{Status: StateMissingSource}
		}
		onPath[key] = true
		st := evalLoop(s, schema, targetID, srcDerived, onPath)
		delete(onPath, key)
		return st
	}
	v, has := target.props[d.DstProp]
	if !has || !v.Has {
		return IndexState{Status: StateNoValue}
	}
	return IndexState{Status: StateIndexable, Value: v}
}

// reason 给出状态对应的人类可读判定依据，写入变更日志。
func (st IndexState) reason() string {
	switch st.Status {
	case StateIndexable:
		return "unique link -> source value present"
	case StateMissingSource:
		return "no link target (or target missing): unindexable"
	case StateNotUnique:
		return "multiple link targets while uniqueness required: unindexable"
	case StateNoValue:
		return "unique target but source property unset: unindexable"
	default:
		return "unknown"
	}
}

// propagationLevel 描述多级传播中的一层：某 (类型,属性) 节点上受影响的实例集合。
type propagationLevel struct {
	typ       ObjectTypeName
	prop      PropertyName
	instances []ObjectID
	reason    string
}

// reindex 在事务工作副本上重算指定实例集合在某个派生节点上的索引状态，
// 更新索引桶与状态表，并返回实际被触及的实例（去重后）。
// 同一实例在一次调用中至多被处理一次。
func reindex(s *storeState, schema *Schema, d DerivedIndex, ids []ObjectID,
	hook func() error) (touched []ObjectID, perInstance map[ObjectID]string, err error) {
	key := propKey{d.SrcType, d.PropName}
	buckets := s.index[key]
	if buckets == nil {
		buckets = map[string]map[ObjectID]struct{}{}
		s.index[key] = buckets
	}
	seen := map[ObjectID]bool{}
	perInstance = map[ObjectID]string{}
	for _, id := range ids {
		if seen[id] {
			continue // 精确性保证：同一下游实例不重复处理。
		}
		seen[id] = true
		ins, ok := s.inst[id]
		if !ok || ins.typ != d.SrcType {
			continue
		}
		if hook != nil {
			if herr := hook(); herr != nil {
				return nil, nil, wrapError(KindDownstreamUpdateFailed, herr,
					"index update for downstream %s failed", id)
			}
		}
		// 从旧桶摘除。
		for val, members := range buckets {
			if _, member := members[id]; member {
				delete(members, id)
				if len(members) == 0 {
					delete(buckets, val)
				}
			}
		}
		state := evaluate(s, schema, id, d)
		if s.states[id] == nil {
			s.states[id] = map[PropertyName]IndexState{}
		}
		s.states[id][d.PropName] = state
		if state.Status == StateIndexable {
			if buckets[state.Value.Val] == nil {
				buckets[state.Value.Val] = map[ObjectID]struct{}{}
			}
			buckets[state.Value.Val][id] = struct{}{}
		}
		touched = append(touched, id)
		perInstance[id] = state.reason()
	}
	sort.Slice(touched, func(i, j int) bool { return touched[i] < touched[j] })
	return touched, perInstance, nil
}
