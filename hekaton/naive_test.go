package hekaton

// 本文件用最直白的方式把规格逐条重写成一个朴素参考模型 naive，
// 再与 Engine 在随机调用序列上逐步对照（见 diff_test.go）。

import (
	"fmt"
	"sort"
)

const (
	nActive    = 0
	nPrepared  = 1
	nCommitted = 2
	nAborted   = 3
)

type nVer struct {
	val     int
	creator int
	ender   int
}

type nTxn struct {
	state    int
	rt, et   int
	finished bool
	deps     map[int]bool
	dependTs map[int]bool
}

type naive struct {
	k      int
	clock  int
	nextID int
	txns   map[int]*nTxn
	vers   map[int][]*nVer // key -> 自旧到新
	reason string          // 上一操作的判定依据（用于日志）
}

func newNaive(k int) *naive {
	m := &naive{k: k, nextID: 1, txns: map[int]*nTxn{}, vers: map[int][]*nVer{}}
	for i := 0; i < k; i++ {
		m.vers[i] = []*nVer{{val: 0, creator: 0, ender: 0}}
	}
	return m
}

func (m *naive) cstate(id int) (int, int) {
	if id == 0 {
		return nCommitted, 0
	}
	tr := m.txns[id]
	return tr.state, tr.et
}

// refResult 是操作结果的统一编码；rejected 表示调用被拒绝（错误）。
type refResult struct {
	rejected bool
	val      int
	list     []int
}

func (m *naive) txnID(want *nTxn) int {
	for id, tr := range m.txns {
		if tr == want {
			return id
		}
	}
	return -1
}

func (m *naive) begin() refResult {
	m.reason = fmt.Sprintf("时钟 %d→%d, 新事务 RT=%d", m.clock, m.clock+1, m.clock+1)
	m.clock++
	id := m.nextID
	m.nextID++
	m.txns[id] = &nTxn{
		state: nActive, rt: m.clock,
		deps: map[int]bool{}, dependTs: map[int]bool{},
	}
	return refResult{val: id}
}

func (m *naive) startVis(v *nVer, rid int, r *nTxn) bool {
	if v.creator == rid {
		return true
	}
	s, et := m.cstate(v.creator)
	if s == nActive || s == nAborted {
		return false
	}
	return et < r.rt
}

func (m *naive) endVis(v *nVer, rid int, r *nTxn) bool {
	if v.ender == 0 {
		return true
	}
	if v.ender == rid {
		return false
	}
	switch m.txns[v.ender].state {
	case nActive, nAborted:
		return true
	default:
		return r.rt < m.txns[v.ender].et
	}
}

func (m *naive) addDep(who, whom int) {
	if who == whom {
		return
	}
	m.txns[who].deps[whom] = true
	m.txns[whom].dependTs[who] = true
}

func (m *naive) read(t, key int) refResult {
	m.reason = ""
	r, ok := m.txns[t]
	if !ok {
		m.reason = "拒绝: 事务号不存在"
		return refResult{rejected: true}
	}
	if r.state != nActive {
		m.reason = "拒绝: Read 要求活跃态"
		return refResult{rejected: true}
	}
	if key < 0 || key >= m.k {
		m.reason = "拒绝: 键越界"
		return refResult{rejected: true}
	}
	for i := len(m.vers[key]) - 1; i >= 0; i-- {
		v := m.vers[key][i]
		if v.creator != 0 && m.txns[v.creator].state == nAborted {
			m.reason += fmt.Sprintf("跳过垃圾版本(c%d); ", v.creator)
			continue
		}
		if !m.startVis(v, t, r) {
			m.reason += fmt.Sprintf("版本(c%d)起点不可见; ", v.creator)
			continue
		}
		if !m.endVis(v, t, r) {
			m.reason += fmt.Sprintf("版本(c%d,e%d)终点不可见; ", v.creator, v.ender)
			continue
		}
		dep := "无依赖"
		if v.creator != 0 && v.creator != t &&
			m.txns[v.creator].state == nPrepared {
			m.addDep(t, v.creator)
			dep = fmt.Sprintf("对预备创建者 c%d 产生依赖", v.creator)
		}
		m.reason += fmt.Sprintf("选中版本(c%d,val%d): %s", v.creator, v.val, dep)
		return refResult{val: v.val}
	}
	return refResult{val: 0}
}

func (m *naive) write(t, key, x int) refResult {
	m.reason = ""
	tr, ok := m.txns[t]
	if !ok {
		m.reason = "拒绝: 事务号不存在"
		return refResult{rejected: true}
	}
	if tr.state != nActive {
		m.reason = "拒绝: Write 要求活跃态"
		return refResult{rejected: true}
	}
	if key < 0 || key >= m.k {
		m.reason = "拒绝: 键越界"
		return refResult{rejected: true}
	}
	vs := m.vers[key]
	var v *nVer
	for i := len(vs) - 1; i >= 0; i-- {
		if vs[i].creator != 0 && m.txns[vs[i].creator].state == nAborted {
			continue
		}
		v = vs[i]
		break
	}
	if v.creator == t {
		v.val = x
		m.reason = "最新版本由自己创建: 原地改值"
		return refResult{}
	}
	enderOK := v.ender == 0 || m.txns[v.ender].state == nAborted
	cs, cet := m.cstate(v.creator)
	creatorOK := cs != nActive && cet < tr.rt
	if !enderOK || !creatorOK {
		m.reason = fmt.Sprintf("写冲突: enderOK=%v creatorOK=%v(c状态%d,ET%d,RT%d) → 中止并级联",
			enderOK, creatorOK, cs, cet, tr.rt)
		return refResult{list: m.abort(t)}
	}
	m.vers[key] = append(vs, &nVer{val: x, creator: t, ender: 0})
	v.ender = t
	if cs == nPrepared {
		m.addDep(t, v.creator)
		m.reason = fmt.Sprintf("新建版本: 结束 c%d 的版本并对其产生依赖", v.creator)
	} else {
		m.reason = fmt.Sprintf("新建版本: 结束 c%d(ET%d<RT%d)", v.creator, cet, tr.rt)
	}
	return refResult{}
}

func (m *naive) precommit(t int) refResult {
	tr, ok := m.txns[t]
	if !ok || tr.state != nActive {
		m.reason = "拒绝: 事务号不存在或非活跃"
		return refResult{rejected: true}
	}
	m.clock++
	tr.state = nPrepared
	tr.et = m.clock
	m.reason = fmt.Sprintf("转预备态 ET=%d, 当前依赖数=%d", tr.et, len(tr.deps))
	return refResult{val: tr.et}
}

func (m *naive) commitOne(start int) []int {
	order := []int{}
	ready := map[int]bool{start: true}
	for len(ready) > 0 {
		ids := sortedBools(ready)
		id := ids[0]
		delete(ready, id)
		cur := m.txns[id]
		if cur.state != nPrepared || !cur.finished || len(cur.deps) > 0 {
			continue
		}
		cur.state = nCommitted
		order = append(order, id)
		for _, d := range sortedBools(cur.dependTs) {
			dep := m.txns[d]
			delete(dep.deps, id)
			delete(cur.dependTs, d)
			if dep.state == nPrepared && dep.finished && len(dep.deps) == 0 {
				ready[d] = true
			}
		}
		cur.deps = map[int]bool{}
		cur.dependTs = map[int]bool{}
	}
	return order
}

func (m *naive) finish(t int) refResult {
	tr, ok := m.txns[t]
	if !ok || tr.state != nPrepared || tr.finished {
		m.reason = "拒绝: 非预备或已 Finish"
		return refResult{rejected: true}
	}
	tr.finished = true
	if len(tr.deps) == 0 {
		o := m.commitOne(t)
		m.reason = fmt.Sprintf("依赖为空, 提交次序=%v", o)
		return refResult{list: o}
	}
	m.reason = fmt.Sprintf("仍有依赖 %v, 等待", sortedBools(tr.deps))
	return refResult{}
}

func (m *naive) abort(t int) []int {
	start := m.txns[t]
	victims := map[int]*nTxn{t: start}
	front := []*nTxn{start}
	for len(front) > 0 {
		cur := front[len(front)-1]
		front = front[:len(front)-1]
		cid := m.txnID(cur)
		for _, d := range sortedBools(cur.dependTs) {
			if _, seen := victims[d]; seen {
				continue
			}
			if m.txns[d].state == nCommitted {
				continue
			}
			victims[d] = m.txns[d]
			front = append(front, m.txns[d])
		}
		_ = cid
	}
	ids := make([]int, 0, len(victims))
	for id := range victims {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		tr := victims[id]
		for d := range tr.deps {
			delete(m.txns[d].dependTs, id)
		}
		tr.deps = map[int]bool{}
		tr.dependTs = map[int]bool{}
		tr.state = nAborted
	}
	return ids
}

func (m *naive) abortOp(t int) refResult {
	tr, ok := m.txns[t]
	if !ok || (tr.state != nActive && tr.state != nPrepared) {
		m.reason = "拒绝: 非活跃/预备态"
		return refResult{rejected: true}
	}
	l := m.abort(t)
	m.reason = fmt.Sprintf("中止并级联(含间接/活跃依赖者), 列表=%v", l)
	return refResult{list: l}
}

func sortedBools(s map[int]bool) []int {
	r := make([]int, 0, len(s))
	for k := range s {
		r = append(r, k)
	}
	sort.Ints(r)
	return r
}
