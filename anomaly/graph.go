package anomaly

import "sort"

type graph struct {
	nodes []int
	edges map[[2]int]EdgeType // 同一对事务间的多种边合并为位掩码
}

// g1Witness 记录 G1a/G1b 的首个命中证据（按确定性次序扫描）。
type g1Witness struct {
	reader int
	key    string
	ver    Version
}

func buildGraph(h History, v *validated) (*graph, *g1Witness, *g1Witness) {
	ids := make([]int, 0, len(v.committed))
	for id := range v.committed {
		ids = append(ids, id)
	}
	sort.Ints(ids)

	g := &graph{nodes: ids, edges: map[[2]int]EdgeType{}}
	addEdge := func(from, to int, t EdgeType) {
		if from == to {
			return // 只在两端事务不同的版本间产生边
		}
		g.edges[[2]int{from, to}] |= t
	}

	// 每个键的版本次序位置，仅次序内的版本产生边。
	// 键集合取操作键与次序键的并集（G1a/G1b 的键可能未给次序），
	// 按名字排序，保证内部扫描与输入排列无关。
	keySet := map[string]bool{}
	for k := range h.Order {
		keySet[k] = true
	}
	for _, id := range ids {
		for _, op := range v.txns[id].Ops {
			keySet[op.Key] = true
		}
	}
	keyNames := make([]string, 0, len(keySet))
	for k := range keySet {
		keyNames = append(keyNames, k)
	}
	sort.Strings(keyNames)

	var g1a, g1b *g1Witness
	note := func(w *g1Witness, slot **g1Witness) {
		if *slot == nil {
			*slot = w
		}
	}

	for _, name := range keyNames {
		order := h.Order[name] // 次序缺省时为 nil：无次序内边，但仍检查 G1a/G1b
		pos := make(map[Version]int, len(order))
		for i, ver := range order {
			pos[ver] = i
		}

		// 写写边：相邻版本的写入者 Ti -> Tj（初始版本无写入者，索引 0 不产生来源）。
		for i := 1; i+1 < len(order); i++ {
			addEdge(order[i].Txn, order[i+1].Txn, WW)
		}

		// 写读边与读写边来自已提交事务的读操作。
		for _, reader := range ids {
			t := v.txns[reader]
			for _, op := range t.Ops {
				if op.Key != name || op.Read == nil {
					continue
				}
				r := *op.Read

				// G1a：已提交事务读到已中止事务写的版本。
				if r.Txn != 0 {
					if wt, ok := v.txns[r.Txn]; ok && wt.Status == Aborted && g1a == nil {
						g1a = &g1Witness{reader: reader, key: name, ver: r}
					}
				}

				idx, inOrder := pos[r]
				if !inOrder {
					// 次序外版本不产生边；若为别的已提交事务的非最后写则为 G1b。
					if r.Txn != 0 && r.Txn != reader && v.committed[r.Txn] {
						k := v.keys.get(name)
						if last, ok := v.lastWrite[[2]int{r.Txn, k}]; ok && r.Seq != last {
							note(&g1Witness{reader: reader, key: name, ver: r}, &g1b)
						}
					}
					continue
				}

				// 写读边：Tj 读了 Ti 写的版本 => Ti -> Tj。
				if r.Txn != 0 && v.committed[r.Txn] {
					addEdge(r.Txn, reader, WR)
				}

				// 读写边：Tj 读的版本紧邻 Ti 写的版本之前 => Tj -> Ti。
				if idx+1 < len(order) {
					next := order[idx+1]
					if next.Txn != 0 && v.committed[next.Txn] {
						addEdge(reader, next.Txn, RW)
					}
				}
			}
		}
	}

	return g, g1a, g1b
}
