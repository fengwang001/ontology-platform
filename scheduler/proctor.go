package scheduler

import "sort"

// proctor.go 负责监考人力占用账目与确定性自动选取。
//
// 规则：
//   - 每个考场在每个被占用时段需要 1 名监考；
//   - 监考人员同一时段只能出现在一个考场；
//   - 每人每日监考时段总数不得超过上限；
//   - 选人必须确定且与调用历史无关：相同状态 + 相同输入给出相同人选。
//
// 选取问题按日建模为「岗位（时段+考场） -> （监考,时段） -> 监考」网络，
// 每名监考当日容量 = 每日上限 - 当日已占用时段数；同时段冲突由
// 「岗位 -> (监考,时段)」边的存在与否表达。用 Dinic 最大流精确判定
// 可行性（不依赖启发式，不会把可行方案误判为缺人）；岗位按时段、
// 考场标识排序，监考按标识排序，邻接边只追加且顺序固定，故同一
// 可行输入恒产生同一人选，可精确复现。

// proctorBook 维护监考人员在各时段的占用与每日监考时段计数。
type proctorBook struct {
	maxPerDay  int
	slotsPerD  int
	order      []string
	atSlot     map[int]map[string]string // slot -> room -> proctor（已提交占用）
	dayCount   map[string]map[int]int    // proctor -> day -> 已监考时段数
	slotToRoom map[int]map[string]string // 供查询：slot -> room(单条记录即可)
}

func newProctorBook(proctors []Proctor, maxPerDay, slotsPerDay int) *proctorBook {
	b := &proctorBook{
		maxPerDay: maxPerDay,
		slotsPerD: slotsPerDay,
		atSlot:    map[int]map[string]string{},
		dayCount:  map[string]map[int]int{},
	}
	seen := map[string]struct{}{}
	for _, p := range proctors {
		if _, ok := seen[p.ID]; ok {
			continue
		}
		seen[p.ID] = struct{}{}
		b.order = append(b.order, p.ID)
	}
	sort.Strings(b.order)
	return b
}

// proctorJob 是一个待填充的监考岗位：某日某时段某考场。
type proctorJob struct {
	day    int
	slot   int
	roomID string
}

// dayProblem 为某一日的选人问题输入：
//   - baseCount：该日保留下来的已提交岗位中每位监考的时段数；
//   - baseBusy：该日保留下来的已提交岗位中每个时段已被哪位监考占用；
//   - jobs：本事务新增（需要选人）的岗位，允许跨多门考试。
type dayProblem struct {
	day       int
	baseCount map[string]int
	baseBusy  map[int]map[string]bool
	jobs      []proctorJob
}

// solveDay 用最大流精确求解一日选人问题。
// 成功返回 slot -> room -> proctorID；不可行返回 nil。
func (b *proctorBook) solveDay(prob dayProblem) map[int]map[string]string {
	jobs := append([]proctorJob(nil), prob.jobs...)
	sort.Slice(jobs, func(i, j int) bool {
		if jobs[i].slot != jobs[j].slot {
			return jobs[i].slot < jobs[j].slot
		}
		return jobs[i].roomID < jobs[j].roomID
	})

	slotSet := map[int]struct{}{}
	for _, jb := range jobs {
		slotSet[jb.slot] = struct{}{}
	}
	slots := make([]int, 0, len(slotSet))
	for s := range slotSet {
		slots = append(slots, s)
	}
	sort.Ints(slots)

	proctors := make([]string, 0, len(b.order))
	residual := map[string]int{}
	for _, p := range b.order {
		if r := b.maxPerDay - prob.baseCount[p]; r > 0 {
			proctors = append(proctors, p)
			residual[p] = r
		}
	}

	// 节点：source | jobs | proctorSlots | proctors | sink
	nJob := len(jobs)
	nPS := len(proctors) * len(slots)
	slotIndex := map[int]int{}
	for i, s := range slots {
		slotIndex[s] = i
	}
	source := 0
	jobBase := 1
	psBase := jobBase + nJob
	pBase := psBase + nPS
	sink := pBase + len(proctors)

	d := newDinic(sink + 1)
	for pi, p := range proctors {
		pNode := pBase + pi
		d.addEdge(pNode, sink, residual[p])
		for si := range slots {
			d.addEdge(psBase+pi*len(slots)+si, pNode, 1)
		}
	}
	for ji, jb := range jobs {
		jNode := jobBase + ji
		d.addEdge(source, jNode, 1)
		for pi, p := range proctors {
			if prob.baseBusy[jb.slot][p] {
				continue
			}
			d.addEdge(jNode, psBase+pi*len(slots)+slotIndex[jb.slot], 1)
		}
	}

	if d.maxFlow(source, sink) != nJob {
		return nil
	}

	out := map[int]map[string]string{}
	for ji, jb := range jobs {
		for _, e := range d.g[jobBase+ji] {
			if e.to >= psBase && e.to < pBase && e.flowed() {
				pi := (e.to - psBase) / len(slots)
				if out[jb.slot] == nil {
					out[jb.slot] = map[string]string{}
				}
				out[jb.slot][jb.roomID] = proctors[pi]
				break
			}
		}
	}
	return out
}

// commit 将一门考试的监考结果写入账目。
func (b *proctorBook) commit(examID string, p placed) {
	for slot, rooms := range p.proctors {
		day := slot / b.slotsPerD
		if b.atSlot[slot] == nil {
			b.atSlot[slot] = map[string]string{}
		}
		for room, proctor := range rooms {
			b.atSlot[slot][room] = proctor
			if b.dayCount[proctor] == nil {
				b.dayCount[proctor] = map[int]int{}
			}
			b.dayCount[proctor][day]++
		}
	}
}

// release 释放一门考试的全部监考占用。
func (b *proctorBook) release(p placed) {
	for slot, rooms := range p.proctors {
		day := slot / b.slotsPerD
		for room, proctor := range rooms {
			if cur, ok := b.atSlot[slot]; ok && cur[room] == proctor {
				delete(cur, room)
			}
			if b.dayCount[proctor] != nil {
				b.dayCount[proctor][day]--
			}
		}
	}
}

// slotsOfProctor 返回某监考人员被占用的时段列表（升序、去重）。
func (b *proctorBook) slotsOfProctor(proctorID string) []Occupy {
	slotSet := map[int]struct{}{}
	for slot, rooms := range b.atSlot {
		for _, p := range rooms {
			if p == proctorID {
				slotSet[slot] = struct{}{}
			}
		}
	}
	out := make([]Occupy, 0, len(slotSet))
	for slot := range slotSet {
		out = append(out, Occupy{Slot: slot})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Slot < out[j].Slot })
	return out
}

// dEdge 是 Dinic 的一条有向边；cap 为剩余容量。
type dEdge struct {
	to, cap, rev int
}

// flowed 判断该正向边是否承载了 1 单位流（初始容量 1，剩余 0）。
func (e dEdge) flowed() bool { return e.cap == 0 }

type dinic struct {
	g     [][]dEdge
	level []int
	it    []int
}

func newDinic(n int) *dinic {
	return &dinic{g: make([][]dEdge, n), level: make([]int, n), it: make([]int, n)}
}

func (d *dinic) addEdge(from, to, cap int) {
	d.g[from] = append(d.g[from], dEdge{to: to, cap: cap, rev: len(d.g[to])})
	d.g[to] = append(d.g[to], dEdge{to: from, cap: 0, rev: len(d.g[from]) - 1})
}

func (d *dinic) maxFlow(s, t int) int {
	flow := 0
	for d.bfs(s, t) {
		for i := range d.it {
			d.it[i] = 0
		}
		for {
			f := d.dfs(s, t, 1<<30)
			if f == 0 {
				break
			}
			flow += f
		}
	}
	return flow
}

func (d *dinic) bfs(s, t int) bool {
	for i := range d.level {
		d.level[i] = -1
	}
	d.level[s] = 0
	q := []int{s}
	for len(q) > 0 {
		v := q[0]
		q = q[1:]
		for _, e := range d.g[v] {
			if e.cap > 0 && d.level[e.to] < 0 {
				d.level[e.to] = d.level[v] + 1
				q = append(q, e.to)
			}
		}
	}
	return d.level[t] >= 0
}

func (d *dinic) dfs(v, t, pushed int) int {
	if v == t {
		return pushed
	}
	for ; d.it[v] < len(d.g[v]); d.it[v]++ {
		e := &d.g[v][d.it[v]]
		if e.cap > 0 && d.level[e.to] == d.level[v]+1 {
			if f := d.dfs(e.to, t, minInt(pushed, e.cap)); f > 0 {
				e.cap -= f
				d.g[e.to][e.rev].cap += f
				return f
			}
		}
	}
	return 0
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
