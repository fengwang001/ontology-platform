package ontology

import "sort"

// propagate 在工作副本上按派生 DAG 分层（BFS）重算所有受影响实例。
//
// 精确性约定（对应需求“不多不少、不重复”）：
//   - 种子集合本身经 dedupAffected 去重；每个 (派生节点,实例) 进入 done 后不再处理；
//   - 一层的受影响集合只从该节点声明的链接入边取得，即“当前真实指向来源的下游”；
//   - reindex 内部再以 seen 兜底，保证同实例在单次重算中至多被处理一次。
//
// 任一实例的索引更新触发 failHook 失败，立即返回 KindDownstreamUpdateFailed，
// 调用方丢弃工作副本，实现整个变更单元的回滚。
func (st *Store) propagate(s *storeState, seed []affectedNode, reason string,
) ([]propagationLevel, error) {
	var levels []propagationLevel
	done := map[propKey]map[ObjectID]bool{}
	frontier := dedupAffected(seed)
	for len(frontier) > 0 {
		byNode := map[propKey][]ObjectID{}
		for _, a := range frontier {
			byNode[a.node] = append(byNode[a.node], a.id)
		}
		var next []affectedNode
		nodeNames := make([]propKey, 0, len(byNode))
		for k := range byNode {
			nodeNames = append(nodeNames, k)
		}
		sort.Slice(nodeNames, func(i, j int) bool {
			if nodeNames[i].typ != nodeNames[j].typ {
				return nodeNames[i].typ < nodeNames[j].typ
			}
			return nodeNames[i].prop < nodeNames[j].prop
		})
		for _, node := range nodeNames {
			ids := byNode[node]
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			d := st.schema.state.derived[node]
			touched, reasons, err := reindex(s, st.schema, d, ids, st.failHook)
			if err != nil {
				return nil, err
			}
			if done[node] == nil {
				done[node] = map[ObjectID]bool{}
			}
			for _, id := range touched {
				done[node][id] = true
			}
			levels = append(levels, propagationLevel{
				typ:       node.typ,
				prop:      node.prop,
				instances: touched,
				reason:    reason + "; " + joinReasons(reasons),
			})
			// 沿派生 DAG 向更下游传播：本节点的值变了，经过本节点对应链接
			// 指向这些实例的更下游实例需要重算。注意入边集合来自当前真实链接结构。
			for _, down := range st.schema.state.dependents[node] {
				dd := st.schema.state.derived[down]
				for _, id := range touched {
					for from := range s.incoming[id][dd.Link] {
						if done[down] != nil && done[down][from] {
							continue
						}
						next = append(next, affectedNode{node: down, id: from})
					}
				}
			}
		}
		frontier = dedupAffected(next)
	}
	return levels, nil
}

func joinReasons(perInstance map[ObjectID]string) string {
	keys := make([]ObjectID, 0, len(perInstance))
	for k := range perInstance {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	out := ""
	for _, k := range keys {
		if out != "" {
			out += ", "
		}
		out += string(k) + ":" + perInstance[k]
	}
	return out
}
