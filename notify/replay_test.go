package notify_test

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"testing"

	"ontology/alert"
	"ontology/labrule"
	"ontology/notify"
)

// simEvent 是朴素模拟中的事件，字段与 alert.Event 一一对应。
type simEvent struct {
	id         int
	patient    string
	code       string
	sev        int
	rep        int64
	deadline   int64
	status     alert.Status
	readings   []alert.Reading
	tech       string
	receiver   string
	mismatch   int
	late       bool
	closedLate bool
}

// sim 是按规则逐条写成的朴素模拟：线性扫描定位事件，全量排序落地逾期。
type sim struct {
	T       [4]int64
	book    *labrule.Book
	wards   map[string]string
	grants  map[string]map[string]notify.Role
	events  map[int]*simEvent
	nextID  int
	maxNow  int64
	started bool
	overdue []int
}

func newSim(T [4]int64) *sim {
	return &sim{
		T:      T,
		book:   labrule.NewBook(),
		wards:  map[string]string{},
		grants: map[string]map[string]notify.Role{},
		events: map[int]*simEvent{},
		nextID: 1,
	}
}

func (s *sim) commit(now int64) {
	var due []*simEvent
	for _, ev := range s.events {
		if ev.status != alert.Closed && !ev.late && now > ev.deadline {
			due = append(due, ev)
		}
	}
	sort.Slice(due, func(i, j int) bool {
		if due[i].deadline != due[j].deadline {
			return due[i].deadline < due[j].deadline
		}
		return due[i].id < due[j].id
	})
	for _, ev := range due {
		ev.late = true
		s.overdue = append(s.overdue, ev.id)
	}
	s.maxNow = now
	s.started = true
}

func (s *sim) checkClock(now int64) error {
	if s.started && now < s.maxNow {
		return alert.ErrClockBack
	}
	return nil
}

func (s *sim) AddTest(code string, low, high, step int64) (error, string) {
	if err := s.book.Add(code, low, high, step); err != nil {
		return alert.ErrInvalidParam, "拒绝: 参数非法或重复注册"
	}
	return nil, "接受: 注册项目"
}

func (s *sim) SetWard(patient, ward string) (error, string) {
	if patient == "" || ward == "" {
		return alert.ErrInvalidParam, "拒绝: 参数非法"
	}
	s.wards[patient] = ward
	return nil, "接受: 设置病区"
}

func (s *sim) Grant(user, ward string, role notify.Role) (error, string) {
	if user == "" || ward == "" || (role != notify.Nurse && role != notify.Doctor) {
		return alert.ErrInvalidParam, "拒绝: 参数非法"
	}
	if s.grants[ward] == nil {
		s.grants[ward] = map[string]notify.Role{}
	}
	s.grants[ward][user] = role
	return nil, "接受: 授权"
}

func (s *sim) qualified(user, ward string, roles ...notify.Role) bool {
	r, ok := s.grants[ward][user]
	if !ok {
		return false
	}
	for _, want := range roles {
		if r == want {
			return true
		}
	}
	return false
}

func (s *sim) Result(now int64, patient, code string, v int64) (int, bool, error, string) {
	if patient == "" || code == "" || now < 0 || now > alert.MaxClock || v < -alert.MaxValue || v > alert.MaxValue {
		return 0, false, alert.ErrInvalidParam, "拒绝: 参数非法"
	}
	if err := s.checkClock(now); err != nil {
		return 0, false, err, "拒绝: 时钟回退"
	}
	if !s.book.Has(code) {
		return 0, false, alert.ErrNotFound, "拒绝: 项目不存在"
	}
	if _, ok := s.wards[patient]; !ok {
		return 0, false, alert.ErrNotFound, "拒绝: 患者不存在"
	}
	s.commit(now)
	sev, crit := s.book.Severity(code, v)
	if !crit {
		return 0, false, nil, "接受: 正常结果, 不影响任何事件"
	}
	for _, ev := range s.events {
		if ev.patient != patient || ev.code != code || ev.status == alert.Closed {
			continue
		}
		ev.readings = append(ev.readings, alert.Reading{Now: now, V: v})
		if sev > ev.sev {
			old := ev.deadline
			ev.sev, ev.rep = sev, v
			ev.status = alert.PendingNotify
			ev.tech, ev.receiver, ev.mismatch = "", "", 0
			if d := now + s.T[sev]; d < ev.deadline {
				ev.deadline = d
			}
			return ev.id, true, nil, fmt.Sprintf("接受: 升级事件 %d 到 sev=%d, deadline %d->%d, 退回待通知", ev.id, sev, old, ev.deadline)
		}
		return ev.id, true, nil, fmt.Sprintf("接受: 并入事件 %d, 同档不升级, rep 不变", ev.id)
	}
	ev := &simEvent{
		id:       s.nextID,
		patient:  patient,
		code:     code,
		sev:      sev,
		rep:      v,
		deadline: now + s.T[sev],
		status:   alert.PendingNotify,
		readings: []alert.Reading{{Now: now, V: v}},
	}
	s.nextID++
	s.events[ev.id] = ev
	return ev.id, true, nil, fmt.Sprintf("接受: 新建事件 %d sev=%d deadline=%d", ev.id, sev, ev.deadline)
}

func (s *sim) Notify(now int64, eventID int, tech, receiver string) (error, string) {
	if tech == "" || receiver == "" || now < 0 || now > alert.MaxClock {
		return alert.ErrInvalidParam, "拒绝: 参数非法"
	}
	if err := s.checkClock(now); err != nil {
		return err, "拒绝: 时钟回退"
	}
	ev := s.events[eventID]
	if ev == nil {
		return alert.ErrNotFound, "拒绝: 事件不存在"
	}
	if !s.qualified(receiver, s.wards[ev.patient], notify.Nurse, notify.Doctor) {
		return alert.ErrNoQual, "拒绝: 接收人无资格"
	}
	if ev.status != alert.PendingNotify {
		return alert.ErrBadState, "拒绝: 状态不符(非待通知)"
	}
	s.commit(now)
	ev.tech, ev.receiver = tech, receiver
	ev.status = alert.PendingReadBack
	return nil, fmt.Sprintf("接受: 通知事件 %d, 接收人 %s", eventID, receiver)
}

func (s *sim) ReadBack(now int64, eventID int, receiver string, v int64) (bool, error, string) {
	if receiver == "" || now < 0 || now > alert.MaxClock || v < -alert.MaxValue || v > alert.MaxValue {
		return false, alert.ErrInvalidParam, "拒绝: 参数非法"
	}
	if err := s.checkClock(now); err != nil {
		return false, err, "拒绝: 时钟回退"
	}
	ev := s.events[eventID]
	if ev == nil {
		return false, alert.ErrNotFound, "拒绝: 事件不存在"
	}
	if ev.receiver != "" && receiver != ev.receiver {
		return false, alert.ErrNoQual, "拒绝: 非本次通知接收人"
	}
	if ev.status != alert.PendingReadBack {
		return false, alert.ErrBadState, "拒绝: 状态不符(非待回读)"
	}
	s.commit(now)
	if v == ev.rep {
		ev.status = alert.PendingAct
		return false, nil, fmt.Sprintf("接受: 回读一致, 事件 %d 转待处置", eventID)
	}
	ev.mismatch++
	if ev.mismatch >= 2 {
		ev.status = alert.PendingNotify
		ev.mismatch = 0
		ev.tech, ev.receiver = "", ""
		return true, nil, fmt.Sprintf("接受: 回读不符达 2 次, 事件 %d 退回待通知", eventID)
	}
	return true, nil, fmt.Sprintf("接受: 回读不符(第 1 次), 事件 %d", eventID)
}

func (s *sim) Act(now int64, eventID int, doctor string) (bool, error, string) {
	if doctor == "" || now < 0 || now > alert.MaxClock {
		return false, alert.ErrInvalidParam, "拒绝: 参数非法"
	}
	if err := s.checkClock(now); err != nil {
		return false, err, "拒绝: 时钟回退"
	}
	ev := s.events[eventID]
	if ev == nil {
		return false, alert.ErrNotFound, "拒绝: 事件不存在"
	}
	if !s.qualified(doctor, s.wards[ev.patient], notify.Doctor) {
		return false, alert.ErrNoQual, "拒绝: 非本病区医生"
	}
	if ev.status != alert.PendingAct {
		return false, alert.ErrBadState, "拒绝: 状态不符(非待处置)"
	}
	s.commit(now)
	ev.status = alert.Closed
	ev.closedLate = ev.late
	return ev.closedLate, nil, fmt.Sprintf("接受: 事件 %d 闭环, late=%v", eventID, ev.closedLate)
}

func (s *sim) Overdue() []int {
	out := make([]int, len(s.overdue))
	copy(out, s.overdue)
	return out
}

// executeSeq 用固定种子生成一条随机操作序列，逐步同时作用于
// 真实管理器与朴素模拟，逐步比对输出，日志打印输入、输出与判定依据。
func executeSeq(t *testing.T, seed int64) ([]alert.Event, []int) {
	t.Helper()
	rng := rand.New(rand.NewSource(seed))
	m, err := notify.New(60, 30, 10)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	s := newSim([4]int64{0, 60, 30, 10})

	codes := []string{"K", "GLU", "XX"}
	patients := []string{"p1", "p2", "p3", "p4", ""}
	wards := []string{"w1", "w2", ""}
	users := []string{"n1", "d1", "u2", ""}
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }

	var clock int64
	maxSeenID := 1
	ops := 20 + rng.Intn(15)
	for i := 0; i < ops; i++ {
		now := clock + rng.Int63n(4)
		if rng.Intn(100) < 15 {
			now = clock - rng.Int63n(3) - 1 // 制造时钟回退/非法时刻
		}
		kind := rng.Intn(100)
		var desc, outcome, reason string
		switch {
		case kind < 35:
			patient, code := pick(patients), pick(codes)
			v := rng.Int63n(141) - 40
			if rng.Intn(100) < 5 {
				v = 2_000_000_000 // 越界值
			}
			desc = fmt.Sprintf("Result(now=%d,%q,%q,%d)", now, patient, code, v)
			id, crit, err := m.Result(now, patient, code, v)
			sid, scrit, serr, r := s.Result(now, patient, code, v)
			reason = r
			outcome = fmt.Sprintf("id=%d crit=%v err=%v", id, crit, err)
			if id != sid || crit != scrit || err != serr {
				t.Fatalf("seq=%d op=%02d %s\n管理器: id=%d crit=%v err=%v\n模拟器: id=%d crit=%v err=%v",
					seed, i, desc, id, crit, err, sid, scrit, serr)
			}
			if err == nil {
				clock = now
				if id > maxSeenID {
					maxSeenID = id
				}
			}
		case kind < 50:
			eventID := 1 + rng.Intn(maxSeenID+1)
			tech, receiver := "tech", pick(users)
			if rng.Intn(100) < 5 {
				tech = ""
			}
			desc = fmt.Sprintf("Notify(now=%d,ev=%d,%q,%q)", now, eventID, tech, receiver)
			err := m.Notify(now, eventID, tech, receiver)
			serr, r := s.Notify(now, eventID, tech, receiver)
			reason = r
			outcome = fmt.Sprintf("err=%v", err)
			if err != serr {
				t.Fatalf("seq=%d op=%02d %s\n管理器: %v\n模拟器: %v", seed, i, desc, err, serr)
			}
			if err == nil {
				clock = now
			}
		case kind < 65:
			eventID := 1 + rng.Intn(maxSeenID+1)
			receiver := pick(users)
			v := rng.Int63n(141) - 40
			desc = fmt.Sprintf("ReadBack(now=%d,ev=%d,%q,%d)", now, eventID, receiver, v)
			mm, err := m.ReadBack(now, eventID, receiver, v)
			smm, serr, r := s.ReadBack(now, eventID, receiver, v)
			reason = r
			outcome = fmt.Sprintf("mismatch=%v err=%v", mm, err)
			if mm != smm || err != serr {
				t.Fatalf("seq=%d op=%02d %s\n管理器: mm=%v err=%v\n模拟器: mm=%v err=%v",
					seed, i, desc, mm, err, smm, serr)
			}
			if err == nil {
				clock = now
			}
		case kind < 75:
			eventID := 1 + rng.Intn(maxSeenID+1)
			doctor := pick(users)
			desc = fmt.Sprintf("Act(now=%d,ev=%d,%q)", now, eventID, doctor)
			late, err := m.Act(now, eventID, doctor)
			slate, serr, r := s.Act(now, eventID, doctor)
			reason = r
			outcome = fmt.Sprintf("late=%v err=%v", late, err)
			if late != slate || err != serr {
				t.Fatalf("seq=%d op=%02d %s\n管理器: late=%v err=%v\n模拟器: late=%v err=%v",
					seed, i, desc, late, err, slate, serr)
			}
			if err == nil {
				clock = now
			}
		case kind < 80:
			patient, ward := pick(patients), pick(wards)
			desc = fmt.Sprintf("SetWard(%q,%q)", patient, ward)
			err := m.SetWard(patient, ward)
			serr, r := s.SetWard(patient, ward)
			reason = r
			outcome = fmt.Sprintf("err=%v", err)
			if err != serr {
				t.Fatalf("seq=%d op=%02d %s\n管理器: %v\n模拟器: %v", seed, i, desc, err, serr)
			}
		case kind < 85:
			user, ward := pick(users), pick(wards)
			role := notify.Role(rng.Intn(4))
			desc = fmt.Sprintf("Grant(%q,%q,%d)", user, ward, int(role))
			err := m.Grant(user, ward, role)
			serr, r := s.Grant(user, ward, role)
			reason = r
			outcome = fmt.Sprintf("err=%v", err)
			if err != serr {
				t.Fatalf("seq=%d op=%02d %s\n管理器: %v\n模拟器: %v", seed, i, desc, err, serr)
			}
		case kind < 90:
			code := pick(codes)
			low := rng.Int63n(60) - 30
			high := low + rng.Int63n(80)
			step := 1 + rng.Int63n(10)
			desc = fmt.Sprintf("AddTest(%q,%d,%d,%d)", code, low, high, step)
			err := m.AddTest(code, low, high, step)
			serr, r := s.AddTest(code, low, high, step)
			reason = r
			outcome = fmt.Sprintf("err=%v", err)
			if err != serr {
				t.Fatalf("seq=%d op=%02d %s\n管理器: %v\n模拟器: %v", seed, i, desc, err, serr)
			}
		default:
			desc = "Overdue()"
			got, want := m.Overdue(), s.Overdue()
			reason = "比对逾期清单"
			outcome = fmt.Sprintf("%v", got)
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("seq=%d op=%02d %s\n管理器: %v\n模拟器: %v", seed, i, desc, got, want)
			}
		}
		t.Logf("seq=%d op=%02d %s => %s | 依据: %s", seed, i, desc, outcome, reason)
	}
	// 序列结束：比对逾期清单与全部事件状态。
	if got, want := m.Overdue(), s.Overdue(); !reflect.DeepEqual(got, want) {
		t.Fatalf("seq=%d 最终逾期清单\n管理器: %v\n模拟器: %v", seed, got, want)
	}
	snap := m.Snapshot()
	if len(snap) != len(s.events) {
		t.Fatalf("seq=%d 事件数: 管理器 %d, 模拟器 %d", seed, len(snap), len(s.events))
	}
	for _, ev := range snap {
		se := s.events[ev.ID]
		if ev.Patient != se.patient || ev.Code != se.code || ev.Sev != se.sev ||
			ev.Rep != se.rep || ev.Deadline != se.deadline || ev.Status != se.status ||
			ev.Tech != se.tech || ev.Receiver != se.receiver || ev.Mismatch != se.mismatch ||
			ev.Late != se.late || ev.ClosedLate != se.closedLate ||
			!reflect.DeepEqual(ev.Readings, se.readings) {
			t.Fatalf("seq=%d 事件 %d 状态不一致\n管理器: %+v\n模拟器: %+v", seed, ev.ID, ev, se)
		}
	}
	return snap, m.Overdue()
}

func TestReplayAgainstNaive(t *testing.T) {
	for seed := int64(0); seed < 1500; seed++ {
		executeSeq(t, seed)
	}
}

func TestReplayDeterministic(t *testing.T) {
	for seed := int64(0); seed < 50; seed++ {
		snap1, od1 := executeSeq(t, seed)
		snap2, od2 := executeSeq(t, seed)
		if !reflect.DeepEqual(snap1, snap2) || !reflect.DeepEqual(od1, od2) {
			t.Fatalf("seq=%d 重放结果不一致", seed)
		}
	}
}
