package slotcoord

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"
)

// 朴素对照模型: 按题目规则独立写成的低性能实现, 用全量扫描代替索引,
// 用于与优化实现(System)在随机操作序列上逐步比对。
type naiveSeries struct {
	id         int
	airline    string
	holder     string
	weekday    int
	hour       int
	startWeek  int
	endWeek    int
	returned   map[int]bool
	beforeDl   map[int]bool
	registered map[int]bool
	executed   map[int]bool
	exempt     map[int]bool
}

type naiveSystem struct {
	cfg      Config
	historic map[Eligibility]bool
	last     int64
	hasLast  bool
	requests []Request
	series   []*naiveSeries
	nextID   int
	wait     []Request // 等候名单(全局, 按提交次序)
	closed   bool
	settled  bool
	elig     []Eligibility
}

func newNaive(cfg Config, historic []Eligibility) *naiveSystem {
	n := &naiveSystem{cfg: cfg, historic: map[Eligibility]bool{}}
	for _, e := range historic {
		n.historic[e] = true
	}
	return n
}

func (n *naiveSystem) clockErr(now int64) error {
	if n.hasLast && now < n.last {
		return ErrClockRegression
	}
	return nil
}

func (n *naiveSystem) accept(now int64) { n.last, n.hasLast = now, true }

func (n *naiveSystem) find(id int) *naiveSeries {
	for _, s := range n.series {
		if s.id == id {
			return s
		}
	}
	return nil
}

// used 朴素统计单元格已分配数: 扫描全部系列。
func (n *naiveSystem) used(week, weekday, hour int) int {
	cnt := 0
	for _, s := range n.series {
		if s.weekday == weekday && s.hour == hour &&
			week >= s.startWeek && week <= s.endWeek && !s.returned[week] {
			cnt++
		}
	}
	return cnt
}

func (n *naiveSystem) remaining(week, weekday, hour int) int {
	return n.cfg.Capacity - n.used(week, weekday, hour)
}

func (n *naiveSystem) held(airline string) int {
	cnt := 0
	for _, s := range n.series {
		if s.holder == airline {
			cnt++
		}
	}
	return cnt
}

func (n *naiveSystem) isHistoric(r Request) bool {
	for e := range n.historic {
		if e.matches(r) {
			return true
		}
	}
	return false
}

func (n *naiveSystem) alloc(req Request) {
	n.series = append(n.series, &naiveSeries{
		id: n.nextID, airline: req.Airline, holder: req.Airline,
		weekday: req.Weekday, hour: req.Hour,
		startWeek: req.StartWeek, endWeek: req.EndWeek,
		returned: map[int]bool{}, beforeDl: map[int]bool{},
		registered: map[int]bool{}, executed: map[int]bool{}, exempt: map[int]bool{},
	})
	n.nextID++
}

func (n *naiveSystem) submit(now int64, req Request) (int, error) {
	paramErr := req.validate(n.cfg)
	var phaseErr error
	switch {
	case n.settled:
		phaseErr = ErrSeasonSettled
	case n.closed || now > n.cfg.ApplicationDeadline:
		phaseErr = ErrApplicationClosed
	}
	if err := firstError(paramErr, n.clockErr(now), phaseErr); err != nil {
		return 0, err
	}
	n.accept(now)
	req.ID = len(n.requests)
	req.SubmittedAt = now
	n.requests = append(n.requests, req)
	return req.ID, nil
}

func (n *naiveSystem) close(now int64) error {
	var paramErr, phaseErr error
	switch {
	case n.settled:
		phaseErr = ErrSeasonSettled
	case n.closed:
		paramErr = ErrParam
	case now < n.cfg.ApplicationDeadline:
		paramErr = ErrParam
	}
	// 历史申请合计需求校验(参数非法)。
	histErr := error(nil)
	if !n.closed && !n.settled {
		demand := map[Cell]int{}
		for _, r := range n.requests {
			if !n.isHistoric(r) {
				continue
			}
			for _, c := range r.cells() {
				demand[c]++
				if demand[c] > n.cfg.Capacity {
					histErr = ErrParam
				}
			}
		}
	}
	if err := firstError(paramErr, histErr, n.clockErr(now), phaseErr); err != nil {
		return err
	}
	n.accept(now)
	n.closed = true

	allocated := make([]bool, len(n.requests))
	// 第一轮: 历史优先权。
	for i, r := range n.requests {
		if n.isHistoric(r) {
			n.alloc(r)
			allocated[i] = true
		}
	}
	// 保留额快照。
	reserve := map[Cell]int{}
	for i, r := range n.requests {
		if allocated[i] {
			continue
		}
		for _, c := range r.cells() {
			if _, ok := reserve[c]; !ok {
				reserve[c] = n.remaining(c.Week, c.Weekday, c.Hour) / 2
			}
		}
	}
	// 第二轮: 新进入者。
	for i, r := range n.requests {
		if allocated[i] || n.held(r.Airline) >= n.cfg.NewcomerThreshold {
			continue
		}
		ok := true
		for _, c := range r.cells() {
			if reserve[c] <= 0 {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		for _, c := range r.cells() {
			reserve[c]--
		}
		n.alloc(r)
		allocated[i] = true
	}
	// 第三轮: 其余申请。
	for i, r := range n.requests {
		if allocated[i] {
			continue
		}
		ok := true
		for _, c := range r.cells() {
			if n.remaining(c.Week, c.Weekday, c.Hour)-reserve[c] <= 0 {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		n.alloc(r)
		allocated[i] = true
	}
	// 等候名单。
	for i, r := range n.requests {
		if !allocated[i] {
			n.wait = append(n.wait, r)
		}
	}
	return nil
}

func (n *naiveSystem) returnWeeks(now int64, airline string, seriesID int, weeks []int) error {
	ser := n.find(seriesID)
	found := ser != nil && ser.holder == airline
	var paramErr error
	if len(weeks) == 0 {
		paramErr = ErrParam
	}
	seen := map[int]bool{}
	for _, w := range weeks {
		if w < 0 || w >= n.cfg.Weeks || seen[w] {
			paramErr = ErrParam
		}
		seen[w] = true
	}
	if found {
		for _, w := range weeks {
			if w < ser.startWeek || w > ser.endWeek {
				paramErr = ErrParam
				continue
			}
			if ser.returned[w] || ser.registered[w] || ser.exempt[w] {
				paramErr = ErrParam
			}
		}
	}
	var notFoundErr error
	if !found {
		notFoundErr = ErrNotFound
	}
	var phaseErr error
	if n.settled {
		phaseErr = ErrSeasonSettled
	}
	if err := firstError(paramErr, n.clockErr(now), notFoundErr, phaseErr); err != nil {
		return err
	}
	n.accept(now)
	sorted := append([]int(nil), weeks...)
	sort.Ints(sorted)
	for _, w := range sorted {
		ser.returned[w] = true
		ser.beforeDl[w] = now <= n.cfg.ReturnDeadline
		// 朴素重扫整个等候名单: 覆盖该周该时段且全部单元格可分配者被满足。
		for i := 0; i < len(n.wait); {
			req := n.wait[i]
			covers := req.Weekday == ser.weekday && req.Hour == ser.hour &&
				w >= req.StartWeek && w <= req.EndWeek
			ok := covers
			if ok {
				for _, c := range req.cells() {
					if n.remaining(c.Week, c.Weekday, c.Hour) <= 0 {
						ok = false
						break
					}
				}
			}
			if ok {
				n.alloc(req)
				n.wait = append(n.wait[:i], n.wait[i+1:]...)
			} else {
				i++
			}
		}
	}
	return nil
}

func (n *naiveSystem) swap(now int64, aID, bID int) error {
	a, b := n.find(aID), n.find(bID)
	var paramErr error
	if aID == bID {
		paramErr = ErrParam
	}
	if a != nil && b != nil {
		if a.holder == b.holder {
			paramErr = ErrParam
		}
		if a.startWeek != b.startWeek || a.endWeek != b.endWeek {
			paramErr = ErrParam
		}
	}
	var notFoundErr error
	if a == nil || b == nil {
		notFoundErr = ErrNotFound
	}
	var phaseErr error
	if n.settled {
		phaseErr = ErrSeasonSettled
	} else if a != nil && b != nil {
		for w := a.startWeek; w <= a.endWeek; w++ {
			if a.executed[w] {
				phaseErr = ErrWeekExecuted
			}
		}
		for w := b.startWeek; w <= b.endWeek; w++ {
			if b.executed[w] {
				phaseErr = ErrWeekExecuted
			}
		}
	}
	if err := firstError(paramErr, n.clockErr(now), notFoundErr, phaseErr); err != nil {
		return err
	}
	n.accept(now)
	a.holder, b.holder = b.holder, a.holder
	return nil
}

func (n *naiveSystem) register(now int64, airline string, seriesID, week int, executed bool) error {
	ser := n.find(seriesID)
	found := ser != nil && ser.holder == airline
	weekValid := week >= 0 && week < n.cfg.Weeks
	var paramErr error
	switch {
	case !weekValid:
		paramErr = ErrParam
	case found && (week < ser.startWeek || week > ser.endWeek):
		paramErr = ErrParam
	case found && ser.returned[week]:
		paramErr = ErrParam
	case now < n.cfg.weekEnd(week):
		paramErr = ErrParam
	}
	var notFoundErr error
	if !found {
		notFoundErr = ErrNotFound
	}
	var phaseErr error
	if n.settled {
		phaseErr = ErrSeasonSettled
	}
	var lateErr error
	if weekValid && now > n.cfg.weekEnd(week)+n.cfg.RegistrationWindow {
		lateErr = ErrRegistrationLate
	}
	var dupErr error
	if found && weekValid && week >= ser.startWeek && week <= ser.endWeek && ser.registered[week] {
		dupErr = ErrDuplicateRegistration
	}
	if err := firstError(paramErr, n.clockErr(now), notFoundErr, phaseErr, lateErr, dupErr); err != nil {
		return err
	}
	n.accept(now)
	ser.registered[week] = true
	ser.executed[week] = executed
	return nil
}

func (n *naiveSystem) exempt(now int64, airline string, seriesID, week int) error {
	ser := n.find(seriesID)
	found := ser != nil && ser.holder == airline
	var paramErr error
	switch {
	case week < 0 || week >= n.cfg.Weeks:
		paramErr = ErrParam
	case found && (week < ser.startWeek || week > ser.endWeek):
		paramErr = ErrParam
	case found && ser.returned[week]:
		paramErr = ErrParam
	case found && ser.exempt[week]:
		paramErr = ErrParam
	}
	var notFoundErr error
	if !found {
		notFoundErr = ErrNotFound
	}
	var phaseErr error
	if n.settled {
		phaseErr = ErrSeasonSettled
	}
	if err := firstError(paramErr, n.clockErr(now), notFoundErr, phaseErr); err != nil {
		return err
	}
	n.accept(now)
	ser.exempt[week] = true
	return nil
}

func (n *naiveSystem) settle(now int64) error {
	var paramErr, phaseErr error
	switch {
	case n.settled:
		phaseErr = ErrSeasonSettled
	case !n.closed:
		paramErr = ErrParam
	case now < n.cfg.seasonEnd():
		paramErr = ErrParam
	}
	if err := firstError(paramErr, n.clockErr(now), phaseErr); err != nil {
		return err
	}
	n.accept(now)
	n.settled = true
	var list []Eligibility
	for _, ser := range n.series { // 按 ID 顺序
		executed, planned := 0, 0
		for w := ser.startWeek; w <= ser.endWeek; w++ {
			if ser.exempt[w] {
				continue
			}
			if ser.returned[w] && ser.beforeDl[w] {
				continue
			}
			planned++
			if ser.executed[w] {
				executed++
			}
		}
		if historicEligible(executed, planned, n.cfg.HistoricThresholdPct) {
			list = append(list, Eligibility{
				Airline: ser.holder, Weekday: ser.weekday, Hour: ser.hour,
				StartWeek: ser.startWeek, EndWeek: ser.endWeek,
			})
		}
	}
	sort.Slice(list, func(i, j int) bool {
		a, b := list[i], list[j]
		if a.Airline != b.Airline {
			return a.Airline < b.Airline
		}
		if a.Weekday != b.Weekday {
			return a.Weekday < b.Weekday
		}
		if a.Hour != b.Hour {
			return a.Hour < b.Hour
		}
		return a.StartWeek < b.StartWeek
	})
	n.elig = list
	return nil
}

func (n *naiveSystem) startNextSeason(now int64, cfg Config) error {
	var paramErr error
	if !n.settled {
		paramErr = ErrParam
	}
	if err := cfg.validate(); err != nil {
		paramErr = err
	}
	if err := firstError(paramErr, n.clockErr(now)); err != nil {
		return err
	}
	n.accept(now)
	n.cfg = cfg
	n.historic = map[Eligibility]bool{}
	for _, e := range n.elig {
		n.historic[e] = true
	}
	n.requests = nil
	n.series = nil
	n.nextID = 0
	n.wait = nil
	n.closed = false
	n.settled = false
	n.elig = nil
	return nil
}

func (n *naiveSystem) snapshot() Snapshot {
	snap := Snapshot{Closed: n.closed, Settled: n.settled}
	for _, s := range n.series {
		view := SeriesView{
			ID: s.id, Airline: s.airline, Holder: s.holder,
			Weekday: s.weekday, Hour: s.hour, StartWeek: s.startWeek, EndWeek: s.endWeek,
		}
		for w := s.startWeek; w <= s.endWeek; w++ {
			view.Weeks = append(view.Weeks, WeekView{
				Week: w, Returned: s.returned[w], BeforeDeadline: s.beforeDl[w],
				Registered: s.registered[w], Executed: s.executed[w], Exempt: s.exempt[w],
			})
		}
		snap.Series = append(snap.Series, view)
	}
	bySlot := map[Slot][]int{}
	var slots []Slot
	for _, req := range n.wait {
		slot := Slot{req.Weekday, req.Hour}
		if _, ok := bySlot[slot]; !ok {
			slots = append(slots, slot)
		}
		bySlot[slot] = append(bySlot[slot], req.ID)
	}
	sort.Slice(slots, func(i, j int) bool {
		if slots[i].Weekday != slots[j].Weekday {
			return slots[i].Weekday < slots[j].Weekday
		}
		return slots[i].Hour < slots[j].Hour
	})
	for _, slot := range slots {
		snap.Waitlists = append(snap.Waitlists, WaitlistView{Slot: slot, Requests: bySlot[slot]})
	}
	snap.Eligibility = append([]Eligibility(nil), n.elig...)
	return snap
}

// 随机操作序列上的逐步对照: 每个操作同时作用于优化实现与朴素模型,
// 比较错误结果与完整状态快照, 并打印每步的输入、输出与判定依据。
func TestDifferentialRandom(t *testing.T) {
	for seed := int64(1); seed <= 60; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			rng := rand.New(rand.NewSource(seed))
			runOneSeason(t, rng, seed)
		})
	}
}

func randomConfig(rng *rand.Rand) Config {
	weeks := 3 + rng.Intn(3)
	return Config{
		Weeks:                weeks,
		MinSeriesWeeks:       1,
		Capacity:             1 + rng.Intn(3),
		HistoricThresholdPct: 50 + rng.Intn(50),
		NewcomerThreshold:    1 + rng.Intn(3),
		SeasonStart:          0,
		WeekLength:           100,
		ApplicationDeadline:  50,
		ReturnDeadline:       100 + rng.Int63n(int64(weeks-1)*100),
		RegistrationWindow:   40 + rng.Int63n(30),
	}
}

var airlines = []string{"A", "B", "C", "D"}

func randomRequest(rng *rand.Rand, cfg Config, historic []Eligibility) Request {
	// 三分之一的概率构造一个命中历史资格的申请。
	if len(historic) > 0 && rng.Intn(3) == 0 {
		e := historic[rng.Intn(len(historic))]
		return Request{Airline: e.Airline, Weekday: e.Weekday, Hour: e.Hour, StartWeek: e.StartWeek, EndWeek: e.EndWeek}
	}
	start := rng.Intn(cfg.Weeks)
	end := start + rng.Intn(cfg.Weeks-start)
	if rng.Intn(10) == 0 {
		end = cfg.Weeks + rng.Intn(3) // 偶发非法参数
	}
	return Request{
		Airline:   airlines[rng.Intn(len(airlines))],
		Weekday:   rng.Intn(3),
		Hour:      rng.Intn(3),
		StartWeek: start,
		EndWeek:   end,
	}
}

func runOneSeason(t *testing.T, rng *rand.Rand, seed int64) {
	cfg := randomConfig(rng)
	var historic []Eligibility
	for i := 0; i < rng.Intn(5); i++ {
		start := rng.Intn(cfg.Weeks)
		historic = append(historic, Eligibility{
			Airline:   airlines[rng.Intn(len(airlines))],
			Weekday:   rng.Intn(3),
			Hour:      rng.Intn(3),
			StartWeek: start,
			EndWeek:   start + rng.Intn(cfg.Weeks-start),
		})
	}

	sys, err := NewSystem(cfg, historic)
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	sys.SetLogger(func(format string, args ...any) {
		t.Logf("  判定依据: "+format, args...)
	})
	naive := newNaive(cfg, historic)

	lastAccepted := int64(-1 << 60)
	step := 0
	run := func(desc string, now int64, applySys func(*System) error, applyNaive func(*naiveSystem) error) {
		errSys := applySys(sys)
		errNaive := applyNaive(naive)
		t.Logf("step %d: 输入=%s now=%d | 输出 system=%v naive=%v", step, desc, now, errSys, errNaive)
		if (errSys == nil) != (errNaive == nil) {
			t.Fatalf("step %d 结果分歧: system=%v naive=%v", step, errSys, errNaive)
		}
		if errSys != nil && errSys != errNaive {
			t.Fatalf("step %d 错误类别分歧: system=%v naive=%v", step, errSys, errNaive)
		}
		if errSys == nil {
			lastAccepted = now
		}
		snapSys, snapNaive := sys.Snapshot(), naive.snapshot()
		if !reflect.DeepEqual(snapSys, snapNaive) {
			t.Fatalf("step %d 状态分歧:\nsystem=%+v\nnaive=%+v", step, snapSys, snapNaive)
		}
		step++
	}
	// now 生成器: 通常推进, 偶尔回退以测试时钟回退。
	nextNow := func() int64 {
		if rng.Intn(12) == 0 && lastAccepted > -1<<50 {
			return lastAccepted - 1 - rng.Int63n(5)
		}
		base := lastAccepted
		if base < 0 {
			base = 0
		}
		return base + rng.Int63n(40)
	}

	// 阶段 1: 申请。
	for i, n := 0, 4+rng.Intn(10); i < n; i++ {
		req := randomRequest(rng, cfg, historic)
		now := nextNow()
		run(fmt.Sprintf("提交申请 %+v", req), now,
			func(s *System) error { _, err := s.SubmitRequest(now, req); return err },
			func(v *naiveSystem) error { _, err := v.submit(now, req); return err })
	}
	// 阶段 2: 截止结算(含一次可能的提前截止尝试)。
	if lastAccepted < cfg.ApplicationDeadline && rng.Intn(2) == 0 {
		run("提前截止", lastAccepted,
			func(s *System) error { return s.CloseApplications(lastAccepted) },
			func(v *naiveSystem) error { return v.close(lastAccepted) })
	}
	closeNow := lastAccepted
	if closeNow < cfg.ApplicationDeadline {
		closeNow = cfg.ApplicationDeadline
	}
	run("截止结算", closeNow,
		func(s *System) error { return s.CloseApplications(closeNow) },
		func(v *naiveSystem) error { return v.close(closeNow) })

	// 阶段 3: 航季内随机操作。
	for i, n := 0, 15+rng.Intn(25); i < n; i++ {
		now := nextNow()
		snap := sys.Snapshot()
		switch rng.Intn(6) {
		case 0, 1: // 返还
			id, weeks := -1, []int{rng.Intn(cfg.Weeks + 1)}
			airline := "ZZ"
			if len(snap.Series) > 0 && rng.Intn(4) > 0 {
				sv := snap.Series[rng.Intn(len(snap.Series))]
				id, airline = sv.ID, sv.Holder
				weeks = []int{sv.StartWeek + rng.Intn(sv.EndWeek-sv.StartWeek+1)}
				if rng.Intn(3) == 0 && sv.EndWeek > sv.StartWeek {
					weeks = append(weeks, sv.StartWeek+rng.Intn(sv.EndWeek-sv.StartWeek+1))
				}
			} else {
				id = rng.Intn(8)
				airline = airlines[rng.Intn(len(airlines))]
			}
			run(fmt.Sprintf("返还 系列=%d 公司=%s 周=%v", id, airline, weeks), now,
				func(s *System) error { return s.ReturnWeeks(now, airline, id, weeks) },
				func(v *naiveSystem) error { return v.returnWeeks(now, airline, id, weeks) })
		case 2, 3: // 登记执行
			id, week := rng.Intn(8), rng.Intn(cfg.Weeks+1)
			airline := airlines[rng.Intn(len(airlines))]
			executed := rng.Intn(2) == 0
			if len(snap.Series) > 0 && rng.Intn(4) > 0 {
				sv := snap.Series[rng.Intn(len(snap.Series))]
				id, airline = sv.ID, sv.Holder
				week = sv.StartWeek + rng.Intn(sv.EndWeek-sv.StartWeek+1)
				// 尽量把时刻对准登记窗口, 提高成功登记的覆盖。
				wEnd := cfg.weekEnd(week)
				switch {
				case lastAccepted < wEnd:
					now = wEnd + rng.Int63n(cfg.RegistrationWindow+1)
				case lastAccepted <= wEnd+cfg.RegistrationWindow:
					now = lastAccepted
				}
			}
			run(fmt.Sprintf("登记 系列=%d 公司=%s 周=%d 执行=%v", id, airline, week, executed), now,
				func(s *System) error { return s.RegisterExecution(now, airline, id, week, executed) },
				func(v *naiveSystem) error { return v.register(now, airline, id, week, executed) })
		case 4: // 交换 / 豁免
			if rng.Intn(2) == 0 {
				a, b := rng.Intn(8), rng.Intn(8)
				if len(snap.Series) >= 2 && rng.Intn(3) > 0 {
					i1, i2 := rng.Intn(len(snap.Series)), rng.Intn(len(snap.Series))
					a, b = snap.Series[i1].ID, snap.Series[i2].ID
				}
				// 一半概率刻意挑选同周数范围、不同持有者的两个系列。
				if rng.Intn(2) == 0 {
					byRange := map[[2]int][]SeriesView{}
					for _, sv := range snap.Series {
						k := [2]int{sv.StartWeek, sv.EndWeek}
						byRange[k] = append(byRange[k], sv)
					}
					for _, group := range byRange {
						if len(group) < 2 {
							continue
						}
						for _, x := range group {
							for _, y := range group {
								if x.Holder != y.Holder {
									a, b = x.ID, y.ID
								}
							}
						}
					}
				}
				run(fmt.Sprintf("交换 系列=%d,%d", a, b), now,
					func(s *System) error { return s.SwapSeries(now, a, b) },
					func(v *naiveSystem) error { return v.swap(now, a, b) })
			} else {
				id, week := rng.Intn(8), rng.Intn(cfg.Weeks)
				airline := airlines[rng.Intn(len(airlines))]
				if len(snap.Series) > 0 && rng.Intn(3) > 0 {
					sv := snap.Series[rng.Intn(len(snap.Series))]
					id, airline = sv.ID, sv.Holder
					week = sv.StartWeek + rng.Intn(sv.EndWeek-sv.StartWeek+1)
				}
				run(fmt.Sprintf("豁免 系列=%d 公司=%s 周=%d", id, airline, week), now,
					func(s *System) error { return s.RegisterExemption(now, airline, id, week) },
					func(v *naiveSystem) error { return v.exempt(now, airline, id, week) })
			}
		case 5: // 提前结算 / 截止后提交
			if rng.Intn(2) == 0 {
				run("提前结算", now,
					func(s *System) error { return s.SettleSeason(now) },
					func(v *naiveSystem) error { return v.settle(now) })
			} else {
				req := randomRequest(rng, cfg, historic)
				run(fmt.Sprintf("截止后提交 %+v", req), now,
					func(s *System) error { _, err := s.SubmitRequest(now, req); return err },
					func(v *naiveSystem) error { _, err := v.submit(now, req); return err })
			}
		}
	}

	// 阶段 4: 结算并滚入下一航季, 验证历史资格传递。
	settleNow := lastAccepted
	if settleNow < cfg.seasonEnd() {
		settleNow = cfg.seasonEnd()
	}
	run("航季结算", settleNow,
		func(s *System) error { return s.SettleSeason(settleNow) },
		func(v *naiveSystem) error { return v.settle(settleNow) })

	next := cfg
	next.SeasonStart += 10000
	next.ApplicationDeadline += 10000
	next.ReturnDeadline += 10000
	run("开启下一航季", settleNow+10,
		func(s *System) error { return s.StartNextSeason(settleNow+10, next) },
		func(v *naiveSystem) error { return v.startNextSeason(settleNow+10, next) })
	// 下一航季提交少量申请并截止, 验证历史优先权生效一致。
	elig := sys.Eligibility()
	for i, n := 0, 3+rng.Intn(5); i < n; i++ {
		req := randomRequest(rng, next, elig)
		now := nextNow()
		run(fmt.Sprintf("次季提交 %+v", req), now,
			func(s *System) error { _, err := s.SubmitRequest(now, req); return err },
			func(v *naiveSystem) error { _, err := v.submit(now, req); return err })
	}
	close2 := lastAccepted
	if close2 < next.ApplicationDeadline {
		close2 = next.ApplicationDeadline
	}
	run("次季截止结算", close2,
		func(s *System) error { return s.CloseApplications(close2) },
		func(v *naiveSystem) error { return v.close(close2) })
	t.Logf("seed=%d 完成 %d 步, 最终快照一致", seed, step)
}

// 可复现性: 相同申请与操作序列重放得到逐系列相同的分配、
// 相同的等候名单与相同的历史资格清单。
func TestReplayDeterminism(t *testing.T) {
	seed := int64(2024)
	run := func() Snapshot {
		rng := rand.New(rand.NewSource(seed))
		cfg := randomConfig(rng)
		historic := []Eligibility{{Airline: "A", Weekday: 0, Hour: 0, StartWeek: 0, EndWeek: 1}}
		s, err := NewSystem(cfg, historic)
		if err != nil {
			t.Fatalf("NewSystem: %v", err)
		}
		var now int64
		advance := func() int64 { now += 1 + rng.Int63n(20); return now }
		for i := 0; i < 12; i++ {
			req := randomRequest(rng, cfg, historic)
			_, _ = s.SubmitRequest(advance(), req)
		}
		if err := s.CloseApplications(cfg.ApplicationDeadline); err != nil {
			t.Fatalf("CloseApplications: %v", err)
		}
		now = cfg.ApplicationDeadline
		for i := 0; i < 30; i++ {
			tNow := advance()
			snap := s.Snapshot()
			if len(snap.Series) == 0 {
				continue
			}
			sv := snap.Series[rng.Intn(len(snap.Series))]
			switch rng.Intn(3) {
			case 0:
				_ = s.ReturnWeeks(tNow, sv.Holder, sv.ID, []int{sv.StartWeek})
			case 1:
				_ = s.RegisterExecution(tNow, sv.Holder, sv.ID, sv.StartWeek, rng.Intn(2) == 0)
			case 2:
				_ = s.RegisterExemption(tNow, sv.Holder, sv.ID, sv.StartWeek)
			}
		}
		if err := s.SettleSeason(cfg.seasonEnd() + 1000); err != nil {
			t.Fatalf("SettleSeason: %v", err)
		}
		return s.Snapshot()
	}
	first, second := run(), run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("重放结果不一致:\n第一次=%+v\n第二次=%+v", first, second)
	}
}
