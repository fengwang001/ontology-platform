package push

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"ontology/group"
	"ontology/policy"
)

// ---------- 快照与断言辅助 ----------

type pdSnap struct {
	id  int
	cfg map[string]string
}

type devSnap struct {
	ack map[string]string
	pd  *pdSnap
	err int
}

type snapshot struct {
	next  int
	tasks map[int]Task
	devs  map[string]devSnap
}

func cloneCfg(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	c := make(map[string]string, len(m))
	for k, v := range m {
		c[k] = v
	}
	return c
}

func takeSnapshot(s *Service) snapshot {
	sn := snapshot{next: s.nextID, tasks: map[int]Task{}, devs: map[string]devSnap{}}
	for id, t := range s.tasks {
		sn.tasks[id] = Task{PushID: t.PushID, Device: t.Device, Config: cloneCfg(t.Config), Outcome: t.Outcome}
	}
	for dev, ds := range s.devs {
		d := devSnap{ack: cloneCfg(ds.ack), err: ds.failures}
		if ds.pd != nil {
			d.pd = &pdSnap{id: ds.pd.pushID, cfg: cloneCfg(ds.pd.config)}
		}
		sn.devs[dev] = d
	}
	return sn
}

func (s snapshot) String() string {
	var b strings.Builder
	fmt.Fprintf(&b, "next=%d\n", s.next)
	ids := make([]int, 0, len(s.tasks))
	for id := range s.tasks {
		ids = append(ids, id)
	}
	sort.Ints(ids)
	for _, id := range ids {
		t := s.tasks[id]
		fmt.Fprintf(&b, "  task %d dev=%s cfg=%v outcome=%s\n", id, t.Device, t.Config, outcomeName(t.Outcome))
	}
	devs := make([]string, 0, len(s.devs))
	for d := range s.devs {
		devs = append(devs, d)
	}
	sort.Strings(devs)
	for _, d := range devs {
		ds := s.devs[d]
		pd := "-"
		if ds.pd != nil {
			pd = fmt.Sprintf("#%d=%v", ds.pd.id, ds.pd.cfg)
		}
		fmt.Fprintf(&b, "  dev %s A=%v Pd=%s nacks=%d\n", d, ds.ack, pd, ds.err)
	}
	return b.String()
}

func outcomeName(o Outcome) string {
	switch o {
	case Acked:
		return "Acked"
	case Cancelled:
		return "Cancelled"
	case Superseded:
		return "Superseded"
	default:
		return "Pending"
	}
}

func pdDiff(a, b *pdSnap) bool {
	if a == nil || b == nil {
		return a != b
	}
	return a.id != b.id || !mapEqual(a.cfg, b.cfg)
}

func mapEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if bv, ok := b[k]; !ok || bv != v {
			return false
		}
	}
	return true
}

func diffSnap(a, b snapshot) string {
	var diffs []string
	if a.next != b.next {
		diffs = append(diffs, fmt.Sprintf("nextID %d != %d", a.next, b.next))
	}
	for id, ta := range a.tasks {
		tb, ok := b.tasks[id]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("task %d missing on naive", id))
			continue
		}
		if ta.Device != tb.Device || ta.Outcome != tb.Outcome || !mapEqual(ta.Config, tb.Config) {
			diffs = append(diffs, fmt.Sprintf("task %d: %+v != %+v", id, ta, tb))
		}
	}
	for id := range b.tasks {
		if _, ok := a.tasks[id]; !ok {
			diffs = append(diffs, fmt.Sprintf("task %d missing on service", id))
		}
	}
	for dev, da := range a.devs {
		db, ok := b.devs[dev]
		if !ok {
			diffs = append(diffs, fmt.Sprintf("dev %s missing on naive", dev))
			continue
		}
		if !mapEqual(da.ack, db.ack) || da.err != db.err {
			diffs = append(diffs, fmt.Sprintf("dev %s A/nack %+v != %+v", dev, da, db))
		}
		if pdDiff(da.pd, db.pd) {
			diffs = append(diffs, fmt.Sprintf("dev %s Pd %+v != %+v", dev, da.pd, db.pd))
		}
	}
	for dev := range b.devs {
		if _, ok := a.devs[dev]; !ok {
			diffs = append(diffs, fmt.Sprintf("dev %s missing on service", dev))
		}
	}
	return strings.Join(diffs, "\n")
}

func describeApplyErr(err error) string {
	var oe *OpError
	if errors.As(err, &oe) {
		return fmt.Sprintf("OpError{idx=%d %v}", oe.Index, oe.Err)
	}
	if err == nil {
		return "nil"
	}
	return err.Error()
}

func describeOps(ops []Op) string {
	var parts []string
	for _, op := range ops {
		switch op.Kind {
		case AddDeviceK:
			parts = append(parts, "AddDevice("+op.Device+")")
		case RemoveDeviceK:
			parts = append(parts, "RemoveDevice("+op.Device+")")
		case AddGroupK:
			parts = append(parts, fmt.Sprintf("AddGroup(%s,%d)", op.Group, op.Priority))
		case RemoveGroupK:
			parts = append(parts, "RemoveGroup("+op.Group+")")
		case AddMemberK:
			parts = append(parts, "AddMember("+op.Group+","+op.Device+")")
		case RemoveMemberK:
			parts = append(parts, "RemoveMember("+op.Group+","+op.Device+")")
		case SetPolicyK:
			var kv []string
			ks := make([]string, 0, len(op.Policy))
			for k := range op.Policy {
				ks = append(ks, k)
			}
			sort.Strings(ks)
			for _, k := range ks {
				v := op.Policy[k]
				if s, ok := v.Get(); ok {
					kv = append(kv, k+"="+strconv.Quote(s))
				} else {
					kv = append(kv, k+"=Unset")
				}
			}
			parts = append(parts, "SetPolicy("+op.Group+",{"+strings.Join(kv, ",")+"})")
		case SetPriorityK:
			parts = append(parts, fmt.Sprintf("SetPriority(%s,%d)", op.Group, op.Priority))
		}
	}
	return "[" + strings.Join(parts, "; ") + "]"
}

// ---------- 朴素全量重算模拟器 ----------

type simDev struct {
	ack  map[string]string
	pd   *pending
	nack int
}

type sim struct {
	gmax   int
	pr     map[string]int
	member map[string]map[string]struct{}
	of     map[string]map[string]struct{}
	devSet map[string]struct{}
	pol    map[string]map[string]policy.Value
	devs   map[string]*simDev
	next   int
	tasks  map[int]*Task
}

func newSim(gmax int) *sim {
	return &sim{
		gmax:   gmax,
		pr:     map[string]int{group.Star: -1},
		member: map[string]map[string]struct{}{group.Star: {}},
		of:     map[string]map[string]struct{}{},
		devSet: map[string]struct{}{},
		pol:    map[string]map[string]policy.Value{group.Star: {}},
		devs:   map[string]*simDev{},
		next:   1,
		tasks:  map[int]*Task{},
	}
}

func cloneSet(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for k := range in {
		out[k] = struct{}{}
	}
	return out
}

// deepClone 供失败批次整体回滚。
func (m *sim) deepClone() *sim {
	c := newSim(m.gmax)
	c.next = m.next
	for g, pr := range m.pr {
		c.pr[g] = pr
	}
	for g, set := range m.member {
		c.member[g] = cloneSet(set)
	}
	for d, set := range m.of {
		c.of[d] = cloneSet(set)
	}
	for d := range m.devSet {
		c.devSet[d] = struct{}{}
	}
	for g, p := range m.pol {
		cp := make(map[string]policy.Value, len(p))
		for k, v := range p {
			cp[k] = v
		}
		c.pol[g] = cp
	}
	for d, ds := range m.devs {
		cd := &simDev{ack: cloneCfg(ds.ack), nack: ds.nack}
		if ds.pd != nil {
			cd.pd = &pending{pushID: ds.pd.pushID, config: cloneCfg(ds.pd.config)}
		}
		c.devs[d] = cd
	}
	for id, t := range m.tasks {
		c.tasks[id] = &Task{PushID: t.PushID, Device: t.Device, Config: cloneCfg(t.Config), Outcome: t.Outcome}
	}
	return c
}

func (m *sim) snapshot() snapshot {
	sn := snapshot{next: m.next, tasks: map[int]Task{}, devs: map[string]devSnap{}}
	for id, t := range m.tasks {
		sn.tasks[id] = Task{PushID: t.PushID, Device: t.Device, Config: cloneCfg(t.Config), Outcome: t.Outcome}
	}
	for d, ds := range m.devs {
		v := devSnap{ack: cloneCfg(ds.ack), err: ds.nack}
		if ds.pd != nil {
			v.pd = &pdSnap{id: ds.pd.pushID, cfg: cloneCfg(ds.pd.config)}
		}
		sn.devs[d] = v
	}
	return sn
}

func (m *sim) effective(dev string) map[string]string {
	groups := []string{group.Star}
	for g := range m.of[dev] {
		groups = append(groups, g)
	}
	keys := map[string]struct{}{}
	for _, g := range groups {
		for k := range m.pol[g] {
			keys[k] = struct{}{}
		}
	}
	out := map[string]string{}
	for k := range keys {
		var winVal policy.Value
		winPr, winName, found := 0, "", false
		for _, g := range groups {
			v, ok := m.pol[g][k]
			if !ok {
				continue
			}
			pr := m.pr[g]
			if !found || pr > winPr || pr == winPr && g < winName {
				found, winVal, winPr, winName = true, v, pr, g
			}
		}
		if found {
			if s, ok := winVal.Get(); ok {
				out[k] = s
			}
		}
	}
	return out
}

// alignAll 是朴素做法：每次变更后对全部设备重算 E 并对齐。
func (m *sim) alignAll() {
	devs := make([]string, 0, len(m.devSet))
	for d := range m.devSet {
		devs = append(devs, d)
	}
	sort.Strings(devs)
	for _, d := range devs {
		ds := m.devs[d]
		// 批内刚加后删的设备已从 devSet 移除。
		if ds == nil {
			continue
		}
		e := m.effective(d)
		if mapEqual(e, ds.ack) {
			if ds.pd != nil {
				m.tasks[ds.pd.pushID].Outcome = Cancelled
				ds.pd = nil
			}
			continue
		}
		if ds.pd != nil && mapEqual(e, ds.pd.config) {
			continue
		}
		if ds.pd != nil {
			m.tasks[ds.pd.pushID].Outcome = Superseded
		}
		id := m.next
		m.next++
		cfg := cloneCfg(e)
		ds.pd = &pending{pushID: id, config: cfg}
		m.tasks[id] = &Task{PushID: id, Device: d, Config: cfg, Outcome: Pending}
	}
}

// runOne 在可变状态上执行单个操作，返回其影响设备。
func (m *sim) runOne(op Op) (map[string]struct{}, error) {
	aff := map[string]struct{}{}
	switch op.Kind {
	case AddDeviceK:
		if !group.ValidName(op.Device) {
			return nil, ErrInvalid
		}
		if _, ok := m.devSet[op.Device]; ok {
			return nil, ErrExists
		}
		m.devSet[op.Device] = struct{}{}
		m.of[op.Device] = map[string]struct{}{}
		m.devs[op.Device] = &simDev{ack: map[string]string{}}
		aff[op.Device] = struct{}{}
	case RemoveDeviceK:
		if !group.ValidName(op.Device) {
			return nil, ErrInvalid
		}
		if _, ok := m.devSet[op.Device]; !ok {
			return nil, ErrNotFound
		}
		aff[op.Device] = struct{}{}
		if ds := m.devs[op.Device]; ds != nil && ds.pd != nil {
			m.tasks[ds.pd.pushID].Outcome = Cancelled
		}
		for g := range m.of[op.Device] {
			delete(m.member[g], op.Device)
		}
		delete(m.of, op.Device)
		delete(m.devSet, op.Device)
		delete(m.devs, op.Device)
	case AddGroupK:
		if !group.ValidName(op.Group) || op.Priority < 0 || op.Priority > 1000 {
			return nil, ErrInvalid
		}
		if _, ok := m.pr[op.Group]; ok {
			return nil, ErrExists
		}
		m.pr[op.Group] = op.Priority
		m.member[op.Group] = map[string]struct{}{}
		m.pol[op.Group] = map[string]policy.Value{}
	case RemoveGroupK:
		if op.Group == group.Star || !group.ValidName(op.Group) {
			return nil, ErrInvalid
		}
		set, ok := m.member[op.Group]
		if !ok {
			return nil, ErrNotFound
		}
		if len(set) > 0 {
			return nil, ErrNotEmpty
		}
		delete(m.pr, op.Group)
		delete(m.member, op.Group)
		delete(m.pol, op.Group)
	case AddMemberK:
		if op.Group == group.Star || !group.ValidName(op.Group) || !group.ValidName(op.Device) {
			return nil, ErrInvalid
		}
		gset, gok := m.member[op.Group]
		dset, dok := m.of[op.Device]
		if !gok || !dok {
			return nil, ErrNotFound
		}
		if _, ok := gset[op.Device]; ok {
			return nil, ErrExists
		}
		if len(dset) >= m.gmax {
			return nil, ErrTooManyGroups
		}
		gset[op.Device] = struct{}{}
		dset[op.Group] = struct{}{}
		aff[op.Device] = struct{}{}
	case RemoveMemberK:
		if op.Group == group.Star || !group.ValidName(op.Group) || !group.ValidName(op.Device) {
			return nil, ErrInvalid
		}
		gset, gok := m.member[op.Group]
		dset, dok := m.of[op.Device]
		if !gok || !dok {
			return nil, ErrNotFound
		}
		if _, ok := gset[op.Device]; !ok {
			return nil, ErrNotFound
		}
		delete(gset, op.Device)
		delete(dset, op.Group)
		aff[op.Device] = struct{}{}
	case SetPolicyK:
		if op.Group != group.Star && !group.ValidName(op.Group) {
			return nil, ErrInvalid
		}
		p, ok := m.pol[op.Group]
		if !ok {
			return nil, ErrNotFound
		}
		if len(op.Policy) > policy.MaxEntries {
			return nil, ErrInvalid
		}
		for k := range op.Policy {
			if len(k) < 1 || len(k) > 64 {
				return nil, ErrInvalid
			}
		}
		np := make(map[string]policy.Value, len(op.Policy))
		for k, v := range op.Policy {
			np[k] = v
		}
		m.pol[op.Group] = np
		_ = p
		for d := range m.member[op.Group] {
			aff[d] = struct{}{}
		}
		if op.Group == group.Star {
			for d := range m.devSet {
				aff[d] = struct{}{}
			}
		}
	case SetPriorityK:
		if op.Group == group.Star || !group.ValidName(op.Group) ||
			op.Priority < 0 || op.Priority > 1000 {
			return nil, ErrInvalid
		}
		if _, ok := m.pr[op.Group]; !ok {
			return nil, ErrNotFound
		}
		m.pr[op.Group] = op.Priority
		for d := range m.member[op.Group] {
			aff[d] = struct{}{}
		}
	default:
		return nil, ErrInvalid
	}
	return aff, nil
}

func (m *sim) apply(ops []Op) error {
	if len(ops) < 1 || len(ops) > 256 {
		return &OpError{Index: 0, Err: ErrInvalid}
	}
	backup := m.deepClone()
	for i, op := range ops {
		if _, err := m.runOne(op); err != nil {
			*m = *backup
			return &OpError{Index: i, Err: err}
		}
	}
	m.alignAll()
	return nil
}

func (m *sim) ack(dev string, id int) error {
	ds, ok := m.devs[dev]
	if !ok || ds.pd == nil || ds.pd.pushID != id {
		return ErrStale
	}
	ds.ack = cloneCfg(ds.pd.config)
	m.tasks[id].Outcome = Acked
	ds.pd = nil
	return nil
}

func (m *sim) nack(dev string, id int) error {
	ds, ok := m.devs[dev]
	if !ok || ds.pd == nil || ds.pd.pushID != id {
		return ErrStale
	}
	ds.nack++
	return nil
}

// ---------- 表驱动用例 ----------

func TestWorkedExample(t *testing.T) {
	s, err := New(16)
	if err != nil {
		t.Fatal(err)
	}
	apply := func(ops ...Op) {
		t.Helper()
		if err := s.Apply(ops); err != nil {
			t.Fatalf("apply %v: %v", ops, err)
		}
	}
	apply(AddGroup("g1", 10), AddGroup("g2", 10), AddGroup("g3", 20))
	apply(SetPolicy(group.Star, map[string]policy.Value{
		"interval": policy.String("300"), "log": policy.String("warn"),
	}))
	apply(
		SetPolicy("g1", map[string]policy.Value{"interval": policy.String("30"), "mode": policy.String("eco")}),
		SetPolicy("g2", map[string]policy.Value{"interval": policy.String("60")}),
		SetPolicy("g3", map[string]policy.Value{"mode": policy.Unset()}),
	)

	apply(AddDevice("d"))
	if want := map[string]string{"interval": "300", "log": "warn"}; !mapEqual(want, s.tasks[1].Config) {
		t.Fatalf("task1 cfg=%v want %v", s.tasks[1].Config, want)
	}

	apply(AddMember("g1", "d"), AddMember("g2", "d"))
	if s.tasks[1].Outcome != Superseded {
		t.Errorf("task1 outcome=%s want Superseded", outcomeName(s.tasks[1].Outcome))
	}
	if want := map[string]string{"interval": "30", "mode": "eco", "log": "warn"}; !mapEqual(want, s.tasks[2].Config) {
		t.Errorf("task2 cfg=%v want %v", s.tasks[2].Config, want)
	}

	if err := s.Ack("d", 2); err != nil {
		t.Fatal(err)
	}
	apply(AddMember("g3", "d"))
	if want := map[string]string{"interval": "30", "log": "warn"}; !mapEqual(want, s.tasks[3].Config) {
		t.Errorf("task3 cfg=%v want %v (Unset shadows mode)", s.tasks[3].Config, want)
	}

	apply(RemoveMember("g3", "d"))
	if s.tasks[3].Outcome != Cancelled || s.devs["d"].pd != nil {
		t.Errorf("task3=%s Pd=%v, want Cancelled and Pd nil", outcomeName(s.tasks[3].Outcome), s.devs["d"].pd)
	}
	if err := s.Ack("d", 3); !errors.Is(err, ErrStale) {
		t.Errorf("Ack cancelled err=%v want ErrStale", err)
	}

	before := s.nextID
	apply(AddMember("g3", "d"), RemoveMember("g3", "d"))
	if s.nextID != before {
		t.Errorf("no-op net batch consumed id: %d -> %d", before, s.nextID)
	}

	apply(SetPriority("g2", 11))
	if want := map[string]string{"interval": "60", "mode": "eco", "log": "warn"}; !mapEqual(want, s.tasks[4].Config) {
		t.Errorf("task4 cfg=%v want %v", s.tasks[4].Config, want)
	}
	apply(SetPriority("g2", 9))
	if s.tasks[4].Outcome != Cancelled {
		t.Errorf("task4=%s want Cancelled", outcomeName(s.tasks[4].Outcome))
	}
	apply(SetPriority("g2", 11))
	if _, ok := s.tasks[5]; !ok {
		t.Fatal("new change after cancellation must allocate task 5, not reuse 4")
	}
}

func TestGmaxBatchOrder(t *testing.T) {
	build := func() *Service {
		s, _ := New(2)
		if err := s.Apply([]Op{
			AddGroup("g1", 1), AddGroup("g2", 1), AddGroup("g3", 1),
			AddDevice("d"),
			AddMember("g1", "d"), AddMember("g2", "d"),
		}); err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := build()
	err := s.Apply([]Op{AddMember("g3", "d"), RemoveMember("g1", "d")})
	var oe *OpError
	if !errors.As(err, &oe) || oe.Index != 0 || !errors.Is(err, ErrTooManyGroups) {
		t.Fatalf("want idx0 ErrTooManyGroups, got %s", describeApplyErr(err))
	}
	if s.groups.MembershipCount("d") != 2 {
		t.Error("rejected batch must leave membership unchanged")
	}

	s2 := build()
	if err := s2.Apply([]Op{RemoveMember("g1", "d"), AddMember("g3", "d")}); err != nil {
		t.Fatalf("reversed order must pass: %s", describeApplyErr(err))
	}
	if got := s2.groups.GroupsOf("d"); len(got) != 3 {
		t.Errorf("groups after batch=%v want g2,g3,*", got)
	}
}

func TestFailedBatchNoChange(t *testing.T) {
	s, _ := New(4)
	if err := s.Apply([]Op{
		AddGroup("g", 5),
		AddDevice("d"),
		SetPolicy(group.Star, map[string]policy.Value{"k": policy.String("v")}),
	}); err != nil {
		t.Fatal(err)
	}
	snap := takeSnapshot(s)
	err := s.Apply([]Op{AddGroup("h", 5), AddDevice("x"), AddMember(group.Star, "d")})
	var oe *OpError
	if !errors.As(err, &oe) || oe.Index != 2 || !errors.Is(err, ErrInvalid) {
		t.Fatalf("want idx2 ErrInvalid got %s", describeApplyErr(err))
	}
	if d := diffSnap(snap, takeSnapshot(s)); d != "" {
		t.Fatalf("rejected batch changed state:\n%s", d)
	}
	if s.groups.HasGroup("h") || s.groups.HasDevice("x") {
		t.Fatal("candidate objects leaked into committed state")
	}
}

func TestRejectionPrecedence(t *testing.T) {
	s, _ := New(4)
	run := func(name string, ops []Op, idx int, want error) {
		t.Helper()
		err := s.Apply(ops)
		var oe *OpError
		if !errors.As(err, &oe) || oe.Index != idx || !errors.Is(err, want) {
			t.Errorf("%s: got %s want idx=%d %v", name, describeApplyErr(err), idx, want)
		}
	}
	run("empty batch", nil, 0, ErrInvalid)
	big := make([]Op, 257)
	for i := range big {
		big[i] = AddDevice("d" + strconv.Itoa(i))
	}
	run("oversized batch", big, 0, ErrInvalid)
	run("remove star", []Op{RemoveGroup(group.Star)}, 0, ErrInvalid)
	run("setpriority star", []Op{SetPriority(group.Star, 5)}, 0, ErrInvalid)
	run("addmember star", []Op{AddMember(group.Star, "d")}, 0, ErrInvalid)
	run("removemember star", []Op{RemoveMember(group.Star, "d")}, 0, ErrInvalid)
	run("bad pr outranks missing", []Op{SetPriority("g", 1001)}, 0, ErrInvalid)
	run("missing group", []Op{RemoveGroup("g")}, 0, ErrNotFound)
	run("bad policy key", []Op{SetPolicy("g", map[string]policy.Value{"": policy.String("x")})}, 0, ErrInvalid)
	run("policy too many keys", []Op{SetPolicy(group.Star, tooManyKeys())}, 0, ErrInvalid)

	if err := s.Apply([]Op{AddGroup("g", 1), AddDevice("d"), AddMember("g", "d")}); err != nil {
		t.Fatal(err)
	}
	run("nonempty before notfound-misc", []Op{RemoveGroup("g")}, 0, ErrNotEmpty)
	run("dup device", []Op{AddDevice("d")}, 0, ErrExists)
	run("dup group", []Op{AddGroup("g", 1)}, 0, ErrExists)
}

func tooManyKeys() map[string]policy.Value {
	m := map[string]policy.Value{}
	for i := 0; i < policy.MaxEntries+1; i++ {
		m["k"+strconv.Itoa(i)] = policy.String("v")
	}
	return m
}

func TestEmptyStringVsUnsetInTasks(t *testing.T) {
	s, _ := New(4)
	if err := s.Apply([]Op{
		AddGroup("g", 10),
		SetPolicy(group.Star, map[string]policy.Value{"a": policy.String("x")}),
		SetPolicy("g", map[string]policy.Value{"a": policy.String("")}),
		AddDevice("d"),
		AddMember("g", "d"),
	}); err != nil {
		t.Fatal(err)
	}
	pd := s.devs["d"].pd
	if pd == nil || !mapEqual(pd.config, map[string]string{"a": ""}) {
		t.Fatalf("empty string must be pushed: %+v", pd)
	}
	if err := s.Ack("d", pd.pushID); err != nil {
		t.Fatal(err)
	}
	// Unset 遮蔽空串：E 为空 map，与 A 不同 → 新任务。
	if err := s.Apply([]Op{SetPolicy("g", map[string]policy.Value{"a": policy.Unset()})}); err != nil {
		t.Fatal(err)
	}
	pd = s.devs["d"].pd
	if pd == nil || len(pd.config) != 0 {
		t.Fatalf("Unset must yield pending empty config: %+v", pd)
	}
	if err := s.Ack("d", pd.pushID); err != nil {
		t.Fatal(err)
	}
	// 再回到空串：与已确认的空 map 不同，再次下发。
	if err := s.Apply([]Op{SetPolicy("g", map[string]policy.Value{"a": policy.String("")})}); err != nil {
		t.Fatal(err)
	}
	if pd := s.devs["d"].pd; pd == nil || !mapEqual(pd.config, map[string]string{"a": ""}) {
		t.Fatalf("expect a='' task again: %+v", pd)
	}
}

func TestNackAndStale(t *testing.T) {
	s, _ := New(4)
	if err := s.Apply([]Op{
		SetPolicy(group.Star, map[string]policy.Value{"k": policy.String("v")}),
		AddDevice("d"),
	}); err != nil {
		t.Fatal(err)
	}
	id := s.devs["d"].pd.pushID
	for i := 0; i < 3; i++ {
		if err := s.Nack("d", id); err != nil {
			t.Fatal(err)
		}
	}
	if s.devs["d"].failures != 3 || s.devs["d"].pd.pushID != id {
		t.Fatal("Nack must count and keep Pd")
	}
	if err := s.Nack("d", id+100); !errors.Is(err, ErrStale) {
		t.Errorf("wrong id=%v", err)
	}
	if err := s.Nack("ghost", id); !errors.Is(err, ErrStale) {
		t.Errorf("missing device=%v", err)
	}
	if err := s.Ack("d", id+100); !errors.Is(err, ErrStale) {
		t.Errorf("wrong ack id=%v", err)
	}
	if err := s.Ack("d", id); err != nil {
		t.Fatal(err)
	}
	if err := s.Ack("d", id); !errors.Is(err, ErrStale) {
		t.Errorf("double ack=%v", err)
	}
}

func TestAddDeviceAndRemoveDevice(t *testing.T) {
	s, _ := New(4)
	if err := s.Apply([]Op{AddDevice("d")}); err != nil {
		t.Fatal(err)
	}
	if s.devs["d"].pd != nil || s.nextID != 1 {
		t.Fatal("empty star policy creates no task")
	}
	if err := s.Apply([]Op{SetPolicy(group.Star, map[string]policy.Value{"k": policy.String("v")})}); err != nil {
		t.Fatal(err)
	}
	if s.devs["d"].pd == nil || s.nextID != 2 {
		t.Fatalf("star policy change must push to all devices, next=%d", s.nextID)
	}
	id := s.devs["d"].pd.pushID
	if err := s.Apply([]Op{RemoveDevice("d")}); err != nil {
		t.Fatal(err)
	}
	if s.tasks[id].Outcome != Cancelled {
		t.Errorf("removed device task=%s want Cancelled", outcomeName(s.tasks[id].Outcome))
	}
	if _, ok := s.devs["d"]; ok {
		t.Error("device state must be removed")
	}
	if err := s.Ack("d", id); !errors.Is(err, ErrStale) {
		t.Errorf("ack removed dev=%v", err)
	}
}

func TestSameConfigKeepsPushId(t *testing.T) {
	s, _ := New(4)
	if err := s.Apply([]Op{
		AddGroup("g1", 10), AddGroup("g2", 20),
		SetPolicy("g1", map[string]policy.Value{"k": policy.String("v")}),
		SetPolicy("g2", map[string]policy.Value{"other": policy.String("z")}),
		AddDevice("d"), AddMember("g1", "d"), AddMember("g2", "d"),
	}); err != nil {
		t.Fatal(err)
	}
	id := s.devs["d"].pd.pushID
	// 改动一个不影响该设备 E 的键所在分组的成员集合以外……这里用 SetPolicy
	// 重设为相同内容：E 不变，Pd 保留不换号。
	if err := s.Apply([]Op{SetPolicy("g2", map[string]policy.Value{"other": policy.String("z")})}); err != nil {
		t.Fatal(err)
	}
	if s.devs["d"].pd == nil || s.devs["d"].pd.pushID != id {
		t.Fatalf("equal E must keep Pd and pushId, next=%d", s.nextID)
	}
}

func TestBatchIntermediateNoIdHole(t *testing.T) {
	s, _ := New(4)
	if err := s.Apply([]Op{
		AddGroup("g", 10),
		SetPolicy(group.Star, map[string]policy.Value{"k": policy.String("star")}),
		SetPolicy("g", map[string]policy.Value{"k": policy.Unset()}),
		AddDevice("d"),
	}); err != nil {
		t.Fatal(err)
	}
	// 单批：先加入（中间态 E 为空，朴素上可能撤任务——但 d 本就无 Pd），
	// 再改 * 策略；只按批后对齐一次。
	if err := s.Apply([]Op{
		AddMember("g", "d"),
		SetPolicy(group.Star, map[string]policy.Value{"k": policy.String("star2")}),
	}); err != nil {
		t.Fatal(err)
	}
	// g(pr 10) 的 Unset 遮蔽 *(pr -1) 的 k：批后 E 为空。
	// 注意任务的产生发生在第一批 AddDevice 时（E={k:star}），第二批只对齐一次将其撤销；
	// 中间态（已入组、* 尚未改名）若逐操作对齐会先撤销再产生，可能留下无结局编号。
	if s.devs["d"].pd != nil {
		t.Fatal("post-batch E empty (g's Unset shadows *), Pd must be cancelled")
	}
	if s.tasks[1].Outcome != Cancelled {
		t.Errorf("task1=%s want Cancelled by single post-batch alignment",
			outcomeName(s.tasks[1].Outcome))
	}
	// 新设备按字节序发号：多设备同批对齐。
	s2, _ := New(4)
	if err := s2.Apply([]Op{
		SetPolicy(group.Star, map[string]policy.Value{"k": policy.String("v")}),
		AddDevice("b"), AddDevice("a"), AddDevice("c"),
	}); err != nil {
		t.Fatal(err)
	}
	if s2.tasks[1].Device != "a" || s2.tasks[2].Device != "b" || s2.tasks[3].Device != "c" {
		t.Errorf("pushIds assigned in device byte order: 1=%s 2=%s 3=%s",
			s2.tasks[1].Device, s2.tasks[2].Device, s2.tasks[3].Device)
	}
}

func TestRecomputedCounter(t *testing.T) {
	s, _ := New(16)
	if err := s.Apply([]Op{
		AddGroup("g", 5),
		AddDevice("d1"), AddDevice("d2"), AddDevice("d3"), AddDevice("d4"), AddDevice("d5"),
		AddMember("g", "d1"), AddMember("g", "d2"), AddMember("g", "d3"),
		AddMember("g", "d4"), AddMember("g", "d5"),
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Apply([]Op{SetPolicy("g", map[string]policy.Value{"k": policy.String("v")})}); err != nil {
		t.Fatal(err)
	}
	if got := s.Recomputed(); got != 5 {
		t.Errorf("SetPolicy recomputed=%d want 5", got)
	}
	if err := s.Apply([]Op{SetPriority("g", 6)}); err != nil {
		t.Fatal(err)
	}
	if got := s.Recomputed(); got != 5 {
		t.Errorf("SetPriority recomputed=%d want 5", got)
	}
	if err := s.Apply([]Op{AddMember("g", "d1")}); err != nil {
		// d1 已在组中 -> ErrExists，recomputed 不更新。
	}
	if got := s.Recomputed(); got != 5 {
		t.Errorf("failed batch must leave recomputed untouched, got %d", got)
	}
	if err := s.Apply([]Op{RemoveMember("g", "d1")}); err != nil {
		t.Fatal(err)
	}
	if got := s.Recomputed(); got != 1 {
		t.Errorf("member op recomputed=%d want 1", got)
	}
	// * 策略变更重算全部设备（5 台）。
	if err := s.Apply([]Op{SetPolicy(group.Star, map[string]policy.Value{"k": policy.String("w")})}); err != nil {
		t.Fatal(err)
	}
	if got := s.Recomputed(); got != 5 {
		t.Errorf("star SetPolicy recomputed=%d want 5", got)
	}
	// 纯结构性变更（AddGroup/RemoveGroup）重算 0 台。
	if err := s.Apply([]Op{AddGroup("empty", 1)}); err != nil {
		t.Fatal(err)
	}
	if got := s.Recomputed(); got != 0 {
		t.Errorf("AddGroup recomputed=%d want 0", got)
	}
}

// ---------- 1500 组随机操作序列差分测试 ----------

func genActions(rng *rand.Rand, steps int) []action {
	// 小名字空间制造冲突：存在/重复/超限/非空等拒绝路径。
	devs := []string{"d0", "d1", "d2", "d3"}
	groups := []string{"g0", "g1", "g2"}
	keys := []string{"interval", "mode", "log", "empty"}
	vals := []func() policy.Value{
		func() policy.Value { return policy.String("v" + strconv.Itoa(rng.Intn(4))) },
		func() policy.Value { return policy.String("") },
		func() policy.Value { return policy.Unset() },
	}
	var acts []action
	for i := 0; i < steps; i++ {
		switch rng.Intn(13) {
		case 0, 1:
			acts = append(acts, action{kind: "apply", op: AddDevice(devs[rng.Intn(len(devs))])})
		case 2:
			acts = append(acts, action{kind: "apply", op: RemoveDevice(devs[rng.Intn(len(devs))])})
		case 3:
			acts = append(acts, action{kind: "apply", op: AddGroup(groups[rng.Intn(len(groups))], rng.Intn(30)-1)})
		case 4:
			acts = append(acts, action{kind: "apply", op: RemoveGroup(groups[rng.Intn(len(groups))])})
		case 5, 6:
			acts = append(acts, action{kind: "apply", op: AddMember(groups[rng.Intn(len(groups))], devs[rng.Intn(len(devs))])})
		case 7:
			acts = append(acts, action{kind: "apply", op: RemoveMember(groups[rng.Intn(len(groups))], devs[rng.Intn(len(devs))])})
		case 8, 9:
			g := groups[rng.Intn(len(groups))]
			if rng.Intn(4) == 0 {
				g = group.Star
			}
			p := map[string]policy.Value{}
			for _, k := range keys {
				if rng.Intn(2) == 0 {
					p[k] = vals[rng.Intn(len(vals))]()
				}
			}
			acts = append(acts, action{kind: "apply", op: SetPolicy(g, p)})
		case 10:
			acts = append(acts, action{kind: "apply", op: SetPriority(groups[rng.Intn(len(groups))], rng.Intn(1001))})
		case 11:
			acts = append(acts, action{kind: "ack", dev: devs[rng.Intn(len(devs))], id: 1 + rng.Intn(8)})
		case 12:
			acts = append(acts, action{kind: "nack", dev: devs[rng.Intn(len(devs))], id: 1 + rng.Intn(8)})
		}
	}
	return acts
}

// batchize 以 1..4 个连续 apply 动作聚成变更集，模拟混合单操作与批量。
func batchize(acts []action, rng *rand.Rand) []action {
	var out []action
	for i := 0; i < len(acts); {
		a := acts[i]
		if a.kind != "apply" {
			out = append(out, a)
			i++
			continue
		}
		n := 1 + rng.Intn(4)
		var ops []Op
		for j := 0; j < n && i+j < len(acts) && acts[i+j].kind == "apply"; j++ {
			ops = append(ops, acts[i+j].op)
		}
		kind := "apply"
		if len(ops) > 1 {
			kind = "batch"
		}
		out = append(out, action{kind: kind, op: ops[0], ops: ops})
		i += len(ops)
	}
	return out
}

type action struct {
	kind string
	op   Op
	ops  []Op
	id   int
	dev  string
}

func TestRandomDifferential(t *testing.T) {
	if os.Getenv("RAND_VERBOSE") == "" {
		t.Log("set RAND_VERBOSE=1 to print every input/output/decision")
	}
	const rounds = 1500
	rng := rand.New(rand.NewSource(20261004))
	for r := 0; r < rounds; r++ {
		gmax := 1 + rng.Intn(4)
		svc, err := New(gmax)
		if err != nil {
			t.Fatal(err)
		}
		m := newSim(gmax)
		acts := genActions(rand.New(rand.NewSource(int64(r+1))), 40)
		acts = batchize(acts, rand.New(rand.NewSource(int64(r+100000))))
		var logb strings.Builder
		fmt.Fprintf(&logb, "round %d gmax=%d\n", r, gmax)
		for ai, a := range acts {
			var svcErr, simErr error
			switch a.kind {
			case "apply", "batch":
				ops := a.ops
				if ops == nil {
					ops = []Op{a.op}
				}
				svcErr = svc.Apply(ops)
				simErr = m.apply(ops)
				fmt.Fprintf(&logb, "[%d] Apply(%d ops) %s -> svc=%s sim=%s\n", ai, len(ops),
					describeOps(ops),
					describeApplyErr(svcErr), describeApplyErr(simErr))
			case "ack", "nack":
				dev := a.dev
				if a.kind == "ack" {
					svcErr = svc.Ack(dev, a.id)
					simErr = m.ack(dev, a.id)
				} else {
					svcErr = svc.Nack(dev, a.id)
					simErr = m.nack(dev, a.id)
				}
				fmt.Fprintf(&logb, "[%d] %s(%s,%d) -> svc=%v sim=%v\n", ai, a.kind, dev, a.id, svcErr, simErr)
			}
			if errors.Is(svcErr, ErrInvalid) != errors.Is(simErr, ErrInvalid) ||
				errors.Is(svcErr, ErrNotFound) != errors.Is(simErr, ErrNotFound) ||
				errors.Is(svcErr, ErrExists) != errors.Is(simErr, ErrExists) ||
				errors.Is(svcErr, ErrNotEmpty) != errors.Is(simErr, ErrNotEmpty) ||
				errors.Is(svcErr, ErrTooManyGroups) != errors.Is(simErr, ErrTooManyGroups) ||
				errors.Is(svcErr, ErrStale) != errors.Is(simErr, ErrStale) {
				t.Fatalf("round %d action %d error mismatch: svc=%v sim=%v\n%s", r, ai, svcErr, simErr, logb.String())
			}
			var soe1, soe2 *OpError
			e1, e2 := errors.As(svcErr, &soe1), errors.As(simErr, &soe2)
			if e1 != e2 || e1 && (soe1.Index != soe2.Index) {
				t.Fatalf("round %d action %d index mismatch: %s vs %s\n%s",
					r, ai, describeApplyErr(svcErr), describeApplyErr(simErr), logb.String())
			}
			if d := diffSnap(takeSnapshot(svc), m.snapshot()); d != "" {
				star := map[string]string{}
				for k, v := range m.pol[group.Star] {
					if s, ok := v.Get(); ok {
						star[k] = s
					} else {
						star[k] = "<Unset>"
					}
				}
				t.Fatalf("round %d action %d state mismatch:\n%s\nlog:\n%s--- service ---\n%s--- naive ---\n%s",
					r, ai, d, logb.String()+"\nnaive star policy: "+fmt.Sprint(star)+
						" devSet="+fmt.Sprint(m.devSet)+"\n", takeSnapshot(svc), m.snapshot())
			}
		}
		if os.Getenv("RAND_VERBOSE") != "" {
			t.Logf("round %d accepted log:\n%sfinal:\n%s", r, logb.String(), takeSnapshot(svc))
		}
	}
}

// TestRecomputedScaleContrast 证明 recomputed 只取决于分组成员数而非设备总数：
// 100 与 10000 台设备、目标组成员同为 5 时，SetPolicy/SetPriority 重算数都恰为 5；
// 朴素全量重算则分别为 100/10000。
func TestRecomputedScaleContrast(t *testing.T) {
	for _, n := range []int{100, 10000} {
		s, _ := New(16)
		if err := s.Apply([]Op{AddGroup("g", 5)}); err != nil {
			t.Fatal(err)
		}
		all := make([]Op, 0, n+5)
		for i := 0; i < n; i++ {
			all = append(all, AddDevice(fmt.Sprintf("dev%05d", i)))
		}
		for i := 0; i < 5; i++ {
			all = append(all, AddMember("g", fmt.Sprintf("dev%05d", i)))
		}
		for start := 0; start < len(all); start += 256 {
			end := start + 256
			if end > len(all) {
				end = len(all)
			}
			if err := s.Apply(all[start:end]); err != nil {
				t.Fatal(err)
			}
		}
		if err := s.Apply([]Op{SetPolicy("g", map[string]policy.Value{"k": policy.String("v")})}); err != nil {
			t.Fatal(err)
		}
		if got := s.Recomputed(); got != 5 {
			t.Errorf("n=%d SetPolicy recomputed=%d want 5", n, got)
		}
		if err := s.Apply([]Op{SetPriority("g", 6)}); err != nil {
			t.Fatal(err)
		}
		if got := s.Recomputed(); got != 5 {
			t.Errorf("n=%d SetPriority recomputed=%d want 5", n, got)
		}
		// 朴素对照：全量重算台数等于设备总数。
		t.Logf("n=%d: targeted recomputed=5 vs naive full-rescan=%d (member count identical)", n, n)
	}
}

func TestConcurrentEquivalence(t *testing.T) {
	s, _ := New(16)
	if err := s.Apply([]Op{
		AddGroup("g", 5),
		SetPolicy(group.Star, map[string]policy.Value{"k": policy.String("v")}),
		AddDevice("d1"), AddDevice("d2"), AddDevice("d3"), AddDevice("d4"),
	}); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(int64(seed)))
			for i := 0; i < 200; i++ {
				switch rng.Intn(4) {
				case 0:
					_ = s.Apply([]Op{SetPolicy(group.Star, map[string]policy.Value{
						"k": policy.String(strconv.Itoa(rng.Intn(6))),
					})})
				case 1:
					dev := fmt.Sprintf("d%d", 1+rng.Intn(4))
					_ = s.Apply([]Op{AddMember("g", dev)})
				case 2:
					dev := fmt.Sprintf("d%d", 1+rng.Intn(4))
					if id, ok := s.PendingPushID(dev); ok {
						_ = s.Ack(dev, id)
					}
				case 3:
					_ = s.Apply([]Op{SetPriority("g", rng.Intn(1001))})
				}
			}
		}(w)
	}
	wg.Wait()
	// 不变量：pushId 连续无洞；每个 Pd 内容恒等于当前 E；有 Pd 当且仅当 E≠A。
	for i := 1; i < s.nextID; i++ {
		if _, ok := s.tasks[i]; !ok {
			t.Fatalf("pushId hole at %d", i)
		}
	}
	for dev, ds := range s.devs {
		e := policy.Effective(s.groups, s.pol, dev)
		eq := mapEqual(e, ds.ack)
		if eq && ds.pd != nil {
			t.Errorf("dev %s: Pd exists while E==A", dev)
		}
		if !eq && (ds.pd == nil || !mapEqual(e, ds.pd.config)) {
			t.Errorf("dev %s: Pd missing or differs from E", dev)
		}
	}
}
