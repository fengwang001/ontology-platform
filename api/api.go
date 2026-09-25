// Package api 对外提供请求生命周期登记、统计告警查询与内置自检；依赖 api -> mon -> sla。
package api

import (
	"errors"
	"fmt"
	"sync"

	"ontology/mon"
	"ontology/sla"
)

var (
	ErrInvalidParam   = errors.New("api: invalid parameters: t<0, w<1, k<1 or k>w")
	ErrDuplicateBegin = errors.New("api: duplicate begin: id already in flight")
	ErrUnknown        = errors.New("api: end for unknown id (never begun or already ended)")
)

type Monitor struct {
	mu       sync.Mutex
	inflight map[string]int64 // id -> beginTs，仅在途请求存在
	mon      *mon.Monitor
}

func New(t int64, w, k int) (*Monitor, error) {
	mm, err := mon.New(t, w, k)
	if err != nil {
		return nil, ErrInvalidParam
	}
	return &Monitor{inflight: map[string]int64{}, mon: mm}, nil
}
func (m *Monitor) Begin(id string, ts int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.inflight[id]; ok {
		return ErrDuplicateBegin
	}
	m.inflight[id] = ts
	m.mon.Begin()
	return nil
}
func (m *Monitor) End(id string, ts int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	begin, ok := m.inflight[id]
	if !ok {
		return ErrUnknown
	}
	latency := sla.Latency(begin, ts)
	if latency < 0 {
		return sla.ErrNegative
	}
	m.mon.Complete(latency)
	delete(m.inflight, id)
	return nil
}
func (m *Monitor) Count() int64        { return m.mon.Count() }
func (m *Monitor) Violations() int64   { return m.mon.Violations() }
func (m *Monitor) InFlight() int64     { return m.mon.InFlight() }
func (m *Monitor) MinLatency() int64   { return m.mon.MinLatency() }
func (m *Monitor) MaxLatency() int64   { return m.mon.MaxLatency() }
func (m *Monitor) AvgLatency() float64 { return m.mon.AvgLatency() }
func (m *Monitor) Breached() bool      { return m.mon.Breached() }

type snapshot struct {
	count, violations, inFlight, minLatency, maxLatency int64
	avgLatency                                          float64
	breached                                            bool
}

func (m *Monitor) snap() snapshot {
	return snapshot{m.Count(), m.Violations(), m.InFlight(), m.MinLatency(),
		m.MaxLatency(), m.AvgLatency(), m.Breached()}
}

// SelfCheck 在独立临时监控器上按 NOTES.md 七步序列核验四条不变量。
func (*Monitor) SelfCheck() error {
	m, err := New(10, 4, 3)
	if err != nil {
		return err
	}
	ids := []string{"A", "B", "C", "D", "E", "F", "G"}
	begs := []int64{0, 20, 40, 50, 70, 90, 100}
	ends := []int64{10, 35, 45, 61, 82, 94, 120}
	violS := []bool{false, true, false, true, true, false, true}
	brS := []bool{false, false, false, false, true, false, true}
	// 独立增量维护的期望聚合（与“对全部已完成批量重算”同一定义）。
	var cnt, vio, sum, mn, mx int64
	for i := range ids {
		if e := m.Begin(ids[i], begs[i]); e != nil {
			return e
		}
		if i == 0 { // 不变量2：Begin 后 End 前，在途不计
			if g := m.snap(); g.count != 0 || g.inFlight != 1 {
				return fmt.Errorf("不变量2 在途不计: %+v", g)
			}
		}
		if e := m.End(ids[i], ends[i]); e != nil {
			return e
		}
		lat := ends[i] - begs[i]
		sum, cnt = sum+lat, int64(i+1)
		if violS[i] {
			vio++
		}
		if cnt == 1 || lat < mn {
			mn = lat
		}
		if lat > mx {
			mx = lat
		}
		g := m.snap() // 不变量1：逐项等于批量重算；不变量3：窗口告警
		if g.count != cnt || g.violations != vio || g.minLatency != mn ||
			g.maxLatency != mx || g.avgLatency != float64(sum)/float64(cnt) ||
			g.breached != brS[i] {
			return fmt.Errorf("步%s 聚合/告警不符: %+v", ids[i], g)
		}
	}
	before := m.snap() // 不变量4：失败不留痕
	if e := m.Begin("X", 1); e != nil {
		return e
	}
	if e := m.Begin("N", 100); e != nil {
		return e
	}
	rejs := []func() error{
		func() error { return m.Begin("X", 1) },
		func() error { return m.End("ghost", 2) },
		func() error { return m.End("N", 99) },
	}
	tgts := []error{ErrDuplicateBegin, ErrUnknown, sla.ErrNegative}
	for j := range rejs {
		if e := rejs[j](); !errors.Is(e, tgts[j]) {
			return fmt.Errorf("不变量4 期望 %v 得到 %v", tgts[j], e)
		}
	}
	after := m.snap() // X、N 合法在途但无完成：在途多 2，其余逐项不变
	if after.inFlight != before.inFlight+2 {
		return fmt.Errorf("不变量4 在途数: before=%d after=%d", before.inFlight, after.inFlight)
	}
	after.inFlight = before.inFlight
	if after != before {
		return fmt.Errorf("不变量4 失败留痕: before=%+v after=%+v", before, after)
	}
	return nil
}
