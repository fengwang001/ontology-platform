package retention_test

// naiveOracle 是一个“朴素实现”参照模型：
//   - 保留全部状态转换历史（append-only，从不删除/折叠）；
//   - 每次判定前都从头重放全部历史，逐条重建每个对象的当前状态；
//   - 显式模拟宽限到期：某对象 GRACE 且 deadline <= t 时，
//     下一个“涉及该对象”的事件（含被拒绝的请求与查询）先把它归档。
//
// 它与生产实现不共享任何代码，仅依据规格独立编码，
// 用于在大量随机操作序列上逐条比对结果。

type nState int

const (
	nAlive nState = iota
	nGrace
	nFrozen
	nArchived
	nMissing
)

type nEvent struct {
	t      int64
	kind   string // create edge delete undelete freeze archive query edgevis advance
	id     string
	target string
	param  int64
	role   int // 0 user, 1 admin
}

type naiveOracle struct {
	now    int64
	events []nEvent // 完整历史，永不压缩
}

type nSnapshot struct {
	state          nState
	graceDeadline  int64
	freezeDeadline int64
}

// rebuild 从全部历史重放，返回某一时刻（事件应用前）所有对象的状态。
func (n *naiveOracle) rebuild(upto int) map[string]nSnapshot {
	st := map[string]nSnapshot{}
	touch := func(id string, t int64) {
		if cur, ok := st[id]; ok && cur.state == nGrace && t >= cur.graceDeadline {
			cur.state = nArchived
			cur.graceDeadline = 0
			st[id] = cur
		}
	}
	for i := 0; i < upto; i++ {
		e := n.events[i]
		switch e.kind {
		case "advance":
			for id := range st {
				touch(id, e.t)
			}
		case "create":
			// create 不触及已存在对象的到期判定（按不存在/已存在校验）
			if _, ok := st[e.id]; !ok {
				st[e.id] = nSnapshot{state: nAlive}
			}
		case "edge":
			touch(e.id, e.t) // 源对象
		case "query", "edgevis", "delete", "undelete", "freeze", "archive":
			touch(e.id, e.t)
		}
		// 重放事件本身记录的转换结果
		switch e.kind {
		case "delete":
			if cur, ok := st[e.id]; ok && cur.state == nAlive && e.param > e.t {
				cur.state = nGrace
				cur.graceDeadline = e.param
				st[e.id] = cur
			}
		case "undelete":
			if cur, ok := st[e.id]; ok && cur.state == nGrace {
				cur.state = nAlive
				cur.graceDeadline = 0
				st[e.id] = cur
			}
		case "freeze":
			if cur, ok := st[e.id]; ok && cur.state == nAlive && e.param > 0 {
				cur.state = nFrozen
				cur.freezeDeadline = e.t + e.param
				st[e.id] = cur
			}
		case "archive":
			if cur, ok := st[e.id]; ok && cur.state == nFrozen && e.t >= cur.freezeDeadline {
				cur.state = nArchived
				cur.freezeDeadline = 0
				st[e.id] = cur
			}
		}
	}
	return st
}

func (n *naiveOracle) stateAt(id string, upto int) nSnapshot {
	cur, ok := n.rebuild(upto)[id]
	if !ok {
		return nSnapshot{state: nMissing}
	}
	return cur
}

// expectErr 按固定次序独立给出请求应报的错误码（0 表示成功）。
// code: 1 notfound 2 illegal 3 invalid 4 frozen-not-expired
func (n *naiveOracle) expectErr(e nEvent) int {
	cur := n.stateAt(e.id, len(n.events)) // 应用本事件前
	switch e.kind {
	case "create":
		if cur.state != nMissing {
			return 2 // 重复创建：方向不允许
		}
		return 0
	case "edge":
		if cur.state == nMissing {
			return 1 // 源不存在
		}
		if n.stateAt(e.target, len(n.events)).state == nMissing {
			return 1 // 目标不存在
		}
		return 0
	default:
		if cur.state == nMissing {
			return 1
		}
	case "delete":
		if cur.state != nAlive {
			return 2
		}
		if e.param <= e.t {
			return 3
		}
	case "freeze":
		if cur.state != nAlive {
			return 2
		}
		if e.param <= 0 {
			return 3
		}
	case "undelete":
		if cur.state == nFrozen && e.t < cur.freezeDeadline {
			return 4
		}
		if cur.state != nGrace {
			return 2
		}
	case "archive":
		if cur.state == nFrozen && e.t < cur.freezeDeadline {
			return 4
		}
		if cur.state != nFrozen {
			return 2
		}
	}
	return 0
}

// visible 给出对象对某身份的可见性。
// 返回收 (exists, visible, state, redactedAttrs, grace, freeze)。
func (n *naiveOracle) visible(id string, admin bool, upto int) (bool, bool, nState, int64, int64) {
	cur := n.stateAt(id, upto)
	if cur.state == nMissing {
		return false, false, nMissing, 0, 0
	}
	if !admin {
		return true, cur.state == nAlive, cur.state, 0, 0
	}
	return true, true, cur.state, cur.graceDeadline, cur.freezeDeadline
}
