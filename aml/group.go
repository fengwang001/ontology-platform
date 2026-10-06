package aml

// deposit 为一笔存款的内部记录。
type deposit struct {
	txnID    string
	account  string
	amount   int64
	date     int64 // 即该存款操作的 now
	covered  bool  // 是否已被某份结构化报告覆盖（冲正不恢复）
	reversed bool  // 是否已冲正
	inWindow bool  // 是否当前计入所属客户组的窗口统计
}

// group 为一个客户组（并查集元素），仅根节点的字段有效。
type group struct {
	members   map[string]struct{} // 组内全部账户
	window    []*deposit          // 窗口内小额存款，按 date 非递减排序；惰性剔除过期/冲正项
	count     int                 // 窗口内未冲正小额存款笔数
	sum       int64               // 窗口内未冲正小额存款金额合计
	uncovered int                 // 窗口内未被任何结构化报告覆盖的笔数
}

func newGroup(account string) *group {
	return &group{members: map[string]struct{}{account: {}}}
}

// find 返回账户所在组的根，带路径压缩。
func (e *Engine) find(acc string) string {
	root := acc
	for e.parent[root] != root {
		root = e.parent[root]
	}
	for e.parent[acc] != root {
		e.parent[acc], acc = root, e.parent[acc]
	}
	return root
}

// findRoot 返回账户所在组的根，不做路径压缩，供只读查询使用。
func (e *Engine) findRoot(acc string) string {
	for e.parent[acc] != acc {
		acc = e.parent[acc]
	}
	return acc
}

// expired 判定存款在时刻 now 是否已滑出窗口。
func (e *Engine) expired(d *deposit, now int64) bool {
	return now-d.date >= e.cfg.D
}

// evict 从组窗口头部剔除已过期条目（窗口按日期非递减排序）。
// 每个条目至多被剔除一次，均摊 O(1)。
func (e *Engine) evict(g *group, now int64) {
	i := 0
	for i < len(g.window) {
		d := g.window[i]
		e.visits++
		if !e.expired(d, now) {
			break
		}
		if d.inWindow {
			d.inWindow = false
			g.count--
			g.sum -= d.amount
			if !d.covered {
				g.uncovered--
			}
		}
		i++
	}
	g.window = g.window[i:]
}

// merge 把 rb 组合并进 ra 组（调用方保证按大小选择方向），返回存活根。
// 只访问两组窗口内（含惰性待剔除）的条目，不触碰窗口之外的历史存款。
func (e *Engine) merge(ra, rb string, now int64) string {
	if e.size[ra] < e.size[rb] {
		ra, rb = rb, ra
	}
	ga, gb := e.groups[ra], e.groups[rb]
	e.evict(ga, now)

	merged := make([]*deposit, 0, len(ga.window)+len(gb.window))
	i, j := 0, 0
	for i < len(ga.window) || j < len(gb.window) {
		var d *deposit
		fromB := false
		if j >= len(gb.window) || (i < len(ga.window) && ga.window[i].date <= gb.window[j].date) {
			d = ga.window[i]
			i++
		} else {
			d = gb.window[j]
			j++
			fromB = true
		}
		e.visits++
		if !d.inWindow {
			continue // 已冲正，惰性剔除
		}
		if e.expired(d, now) {
			d.inWindow = false
			continue
		}
		if fromB {
			ga.count++
			ga.sum += d.amount
			if !d.covered {
				ga.uncovered++
			}
		}
		merged = append(merged, d)
	}
	ga.window = merged

	for acc := range gb.members {
		ga.members[acc] = struct{}{}
	}
	e.parent[rb] = ra
	e.size[ra] += e.size[rb]
	delete(e.size, rb)
	delete(e.groups, rb)
	return ra
}

// evaluate 对组做一次结构化评估：成立且仍有未覆盖存款时发出一份报告，
// 并把集合中全部存款标记为已覆盖；否则返回 nil。
func (e *Engine) evaluate(root string, now int64, trigger string) *Report {
	g := e.groups[root]
	e.evict(g, now)
	if g.count < e.cfg.K || g.sum < e.cfg.High || g.uncovered == 0 {
		return nil
	}
	txnIDs := make([]string, 0, g.count)
	var total int64
	kept := g.window[:0]
	for _, d := range g.window {
		e.visits++
		if !d.inWindow {
			continue // 已冲正，顺手压实
		}
		d.covered = true
		txnIDs = append(txnIDs, d.txnID)
		total += d.amount
		kept = append(kept, d)
	}
	g.window = kept
	g.uncovered = 0
	return e.emit(KindStructuring, g, txnIDs, total, trigger, now)
}
