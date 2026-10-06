// Package querier 实现查询器选举与通用查询发送计划。
//
// 选举规则：本机自时刻 0 起为查询器；收到 srcIP 更小的他机通用查询则让位，
// 他机查询器存续期 OQPI 到期（恰等即恢复）后本机恢复并自该时刻重置相位。
// 通用查询计划用区间列表惰性表示，不设后台定时器。
package querier

import "sort"

// Querier 跟踪选举状态、路由器端口与通用查询计划。
// 它不是并发安全的，调用方必须串行化访问。
type Querier struct {
	ownIP uint32
	qi    int64
	oqpi  int64

	yielded bool
	oq      int64 // 他机查询器存续截止时刻，恰等即恢复

	intervals [][2]int64 // 通用查询区间 [start, end]，end<0 表示开放
	routers   map[int]int64
}

// New 构造查询器控制器，qi 为查询间隔，oqpi 为他机查询器存续期。
func New(ownIP uint32, qi, oqpi int64) *Querier {
	return &Querier{
		ownIP:     ownIP,
		qi:        qi,
		oqpi:      oqpi,
		intervals: [][2]int64{{0, -1}},
		routers:   make(map[int]int64),
	}
}

// Advance 落地不晚于 now 的选举迁移：他机查询器到期则本机恢复，
// 并自 oq 起开启新的通用查询区间（相位重置）。仅在操作被接受后调用。
func (q *Querier) Advance(now int64) {
	if q.yielded && now >= q.oq {
		q.yielded = false
		q.intervals = append(q.intervals, [2]int64{q.oq, -1})
	}
}

// IsQuerierAt 报告 now 时刻本机是否为查询器，是不改状态的纯函数。
func (q *Querier) IsQuerierAt(now int64) bool {
	return !q.yielded || now >= q.oq
}

// Query 处理收到的他机通用查询，返回本机是否因此让位（含已让位时刷新）。
// 无论 srcIP 大小，该端口都成为（或刷新）路由器端口。
func (q *Querier) Query(port int, srcIP uint32, now int64) bool {
	q.routers[port] = now + q.oqpi
	if srcIP >= q.ownIP {
		return false
	}
	if !q.yielded {
		// 让位封闭当前区间；恰在让位时刻的通用查询照发（区间端点含 now）。
		q.intervals[len(q.intervals)-1][1] = now
	}
	q.yielded = true
	q.oq = now + q.oqpi
	return true
}

// RouterPorts 返回 now 时刻未到期的路由器端口（升序）与触达的记录数。
func (q *Querier) RouterPorts(now int64) ([]int, int) {
	touched := len(q.routers)
	ports := make([]int, 0, len(q.routers))
	for p, exp := range q.routers {
		if exp > now {
			ports = append(ports, p)
		} else {
			delete(q.routers, p)
		}
	}
	sort.Ints(ports)
	return ports, touched
}

// DrainGeneral 返回 (after, now] 内的全部通用查询时刻，升序。
func (q *Querier) DrainGeneral(after, now int64) []int64 {
	var out []int64
	for _, iv := range q.intervals {
		start, end := iv[0], iv[1]
		lo := start
		if after >= start {
			lo = start + ((after-start)/q.qi+1)*q.qi
		}
		for t := lo; t <= now && (end < 0 || t <= end); t += q.qi {
			out = append(out, t)
		}
	}
	return out
}
