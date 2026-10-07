package ontology

// reachedNode 是传播遍历的内部中间结果。
type reachedNode struct {
	id         InstanceID
	depth      int
	via        LinkTypeID
	operations []Operation
	stop       bool // 经 NoPropagate 规则到达：施加影响但不继续扩展
	parent     int  // BFS 首次到达父节点下标；根为 -1
	invisible  bool // 主体不可见：跳过点（仅在 Skip 模式出现）
}

// visibilityFunc 报告主体在给定传播深度是否能感知某实例。
type visibilityFunc func(inst Instance, depth int) bool

// traverse 按动作声明从直接目标做有限深度、环上只访问一次、且只沿
// "主体当前可见"实例向外扩展的传播。
//
// 语义：
//   - 深度 0 为直接目标（roots），其可见性由调用方单独检查（优先级 3）。
//   - 沿某条 CascadeRule 走一步到达深度 d+1；统一受动作级 MaxDepth 约束。
//   - 某实例首次到达即固定深度与父子关系；之后经更长路径或环回边再次
//     到达只合并操作、不重新扩展，因此环上每实例只检查/记录一次且必终止。
//   - 首次到达为"不可见"的实例记为跳过点：它本身被记录，但不从它继续
//     扩展，于是其"仅能经由它到达"的后继自然被剪除；若该实例同时可经
//     一条全部可见的更短/独立路径到达，则它在那条路径上已被访问为可见，
//     不会被误判为不可见。
//   - NoPropagate 规则到达的节点仍被记录并施加影响，但 stop=true，
//     不再向外扩展；它是传播终点，不参与越界判定。
//   - 若存在只能在 >MaxDepth 深度首次到达、且经由可传播边的实例，
//     置 exceeded=true。
func traverse(st Store, action ActionDeclaration, roots []InstanceID, visible visibilityFunc,
) (nodes []reachedNode, exceeded bool) {
	byID := map[InstanceID]int{}
	queue := make([]reachedNode, 0, len(roots))
	for _, r := range roots {
		if _, dup := byID[r]; dup {
			continue
		}
		byID[r] = len(queue)
		queue = append(queue, reachedNode{id: r, depth: 0, parent: -1})
	}

	// 深度越界是纯图结构事实，必须先于可见性判定（错误优先级第 2 类）。
	// 用独立的可见性无关 BFS 计算每个实例的最短传播深度；忽略 NoPropagate
	// 终点之外的扩展，并在发现 >MaxDepth 的可达实例时立即置标志。
	depthOf := map[InstanceID]int{}
	depthStop := map[InstanceID]bool{}
	var depthQ []InstanceID
	for _, r := range roots {
		if _, ok := depthOf[r]; !ok {
			depthOf[r] = 0
			depthQ = append(depthQ, r)
		}
	}
	rules := action.Cascades
	for dh := 0; dh < len(depthQ); dh++ {
		cur := depthQ[dh]
		if depthStop[cur] {
			continue
		}
		for _, rule := range rules {
			for _, nb := range st.Neighbors(cur, rule) {
				nd := depthOf[cur] + 1
				if _, seen := depthOf[nb]; seen {
					continue
				}
				depthOf[nb] = nd
				if rule.NoPropagate {
					depthStop[nb] = true
				}
				if nd > action.MaxDepth && !rule.NoPropagate {
					exceeded = true
				}
				depthQ = append(depthQ, nb)
			}
		}
	}

	for head := 0; head < len(queue); head++ {
		cur := queue[head]
		if cur.stop || cur.invisible {
			continue
		}
		for _, rule := range action.Cascades {
			for _, nb := range st.Neighbors(cur.id, rule) {
				nextDepth := cur.depth + 1
				if idx, ok := byID[nb]; ok {
					if queue[idx].depth > 0 {
						queue[idx].operations = addOp(queue[idx].operations, rule.Effect)
					}
					continue
				}
				if nextDepth > action.MaxDepth && !rule.NoPropagate {
					continue // 越界已由深度预扫描记录，此处不作为触及节点入队
				}
				nbInst, exists := st.Get(nb)
				invisible := !exists || (visible != nil && !visible(nbInst, nextDepth))
				queue = append(queue, reachedNode{
					id:         nb,
					depth:      nextDepth,
					via:        rule.LinkType,
					operations: []Operation{rule.Effect},
					stop:       rule.NoPropagate,
					parent:     head,
					invisible:  invisible,
				})
				byID[nb] = len(queue) - 1
			}
		}
	}
	return queue, exceeded
}

// mergeDecisions 按声明的合并规则归约多层级结论；只依赖结论多重集，
// 与层级访问顺序无关。
func mergeDecisions(policy MergePolicy, ds []LayerDecision) bool {
	voted := false
	for _, d := range ds {
		if d.Abstain {
			continue
		}
		voted = true
		if policy == MergeAny && d.Allowed {
			return true
		}
		if policy == MergeAll && !d.Allowed {
			return false
		}
	}
	if !voted {
		return false
	}
	return policy == MergeAll
}

func addOp(ops []Operation, op Operation) []Operation {
	for _, o := range ops {
		if o == op {
			return ops
		}
	}
	return append(ops, op)
}
