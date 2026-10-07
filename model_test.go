package ontology

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// 本文件包含一个按规则独立写成的朴素模型：不用堆与二分，
// 有效截止靠全量扫描授权、取档靠线性扫描、结算靠重放版本日志。
// 随机操作序列同时作用于引擎与模型，逐步比对输入、输出与判定依据。

type nGrant struct {
	id       int64
	kind     TargetKind
	target   string
	duration int64
	revoked  bool
}

type nVersion struct {
	time    int64
	onTime  bool
	invalid bool
	snaps   map[string]memberSnap
}

type nGroup struct {
	members map[string]bool
	former  map[string]int
}

type nAssignment struct {
	cfg         AssignmentConfig
	subjects    map[string]bool
	versions    map[string][]nVersion
	designated  map[string]int
	grants      []nGrant
	nextGrant   int64
	groups      map[string]*nGroup
	memberGroup map[string]string
	settled     bool
	result      []MemberSettlement
}

type naiveEngine struct {
	clock       int64
	assignments map[string]*nAssignment
	note        string // 最近一步的判定依据（供日志）
}

func newNaiveEngine() *naiveEngine {
	return &naiveEngine{assignments: map[string]*nAssignment{}}
}

// nFindTier 线性扫描取首个覆盖档位。
func nFindTier(tiers []Tier, late int64) (Tier, bool) {
	for _, tier := range tiers {
		if tier.MaxLate >= late {
			return tier, true
		}
	}
	return Tier{}, false
}

// maxExtWhere 全量扫描授权，取生效中延期的最大时长。
func (a *nAssignment) maxExtWhere(match func(nGrant) bool) int64 {
	var best int64
	for _, g := range a.grants {
		if g.revoked || !match(g) {
			continue
		}
		if g.duration > best {
			best = g.duration
		}
	}
	return best
}

func (a *nAssignment) personalMax(member string) int64 {
	return a.maxExtWhere(func(g nGrant) bool { return g.kind == PersonalTarget && g.target == member })
}

func (a *nAssignment) groupMax(group string) int64 {
	return a.maxExtWhere(func(g nGrant) bool { return g.kind == GroupTarget && g.target == group })
}

func (n *naiveEngine) lookup(op, aid string) (*nAssignment, error) {
	a, ok := n.assignments[aid]
	if !ok {
		return nil, newErr(op, ErrNotFound, "assignment %q not found", aid)
	}
	return a, nil
}

func (n *naiveEngine) CreateAssignment(cfg AssignmentConfig, now int64) error {
	const op = "CreateAssignment"
	if err := validateConfig(op, cfg); err != nil {
		return err
	}
	if now < 0 {
		return newErr(op, ErrInvalidArgument, "negative time %d", now)
	}
	if now < n.clock {
		return newErr(op, ErrClockRegression, "now=%d before clock %d", now, n.clock)
	}
	if _, ok := n.assignments[cfg.ID]; ok {
		return newErr(op, ErrStateNotAllowed, "assignment %q already exists", cfg.ID)
	}
	n.assignments[cfg.ID] = &nAssignment{
		cfg:         cfg,
		subjects:    map[string]bool{},
		versions:    map[string][]nVersion{},
		designated:  map[string]int{},
		groups:      map[string]*nGroup{},
		memberGroup: map[string]string{},
	}
	n.clock = now
	return nil
}

func (n *naiveEngine) Submit(aid, subject, member string, now int64) (int, error) {
	const op = "Submit"
	if aid == "" || subject == "" || member == "" {
		return 0, newErr(op, ErrInvalidArgument, "empty id")
	}
	if now < 0 {
		return 0, newErr(op, ErrInvalidArgument, "negative time %d", now)
	}
	if now < n.clock {
		return 0, newErr(op, ErrClockRegression, "now=%d before clock %d", now, n.clock)
	}
	a, err := n.lookup(op, aid)
	if err != nil {
		return 0, err
	}
	if a.cfg.GroupWork {
		if _, ok := a.groups[subject]; !ok {
			return 0, newErr(op, ErrNotFound, "group %q not found", subject)
		}
	}
	if a.settled {
		return 0, newErr(op, ErrAlreadySettled, "assignment %q settled", aid)
	}
	if now > a.cfg.HardClose {
		return 0, newErr(op, ErrClosed, "now=%d past hard close %d", now, a.cfg.HardClose)
	}
	var members []string
	if a.cfg.GroupWork {
		g := a.groups[subject]
		if !g.members[member] {
			return 0, newErr(op, ErrStateNotAllowed, "member %q not in group %q", member, subject)
		}
		for m := range g.members {
			members = append(members, m)
		}
	} else {
		if subject != member {
			return 0, newErr(op, ErrStateNotAllowed, "cannot submit for %q as %q", subject, member)
		}
		members = []string{member}
	}
	var subjEff int64
	if a.cfg.GroupWork {
		subjEff = a.cfg.Deadline + a.groupMax(subject)
	} else {
		subjEff = a.cfg.Deadline + a.personalMax(subject)
	}
	late := now - subjEff
	v := nVersion{time: now, onTime: late <= 0, snaps: map[string]memberSnap{}}
	if late > 0 {
		_, ok := nFindTier(a.cfg.Tiers, late)
		v.invalid = !ok
	}
	for _, m := range members {
		eff := a.cfg.Deadline + a.personalMax(m)
		if a.cfg.GroupWork {
			if gm := a.groupMax(subject); gm > eff-a.cfg.Deadline {
				eff = a.cfg.Deadline + gm
			}
		}
		snap := memberSnap{effDeadline: eff, late: now - eff}
		if snap.late > 0 {
			if tier, ok := nFindTier(a.cfg.Tiers, snap.late); ok {
				snap.penalty = tier.Penalty
			} else {
				snap.invalid = true
			}
		}
		v.snaps[m] = snap
	}
	a.versions[subject] = append(a.versions[subject], v)
	a.subjects[subject] = true
	n.clock = now
	n.note = fmt.Sprintf("subjectEff=%d late=%d onTime=%v invalid=%v", subjEff, late, v.onTime, v.invalid)
	return len(a.versions[subject]), nil
}

func (n *naiveEngine) GrantExtension(aid string, kind TargetKind, target string, duration int64, now int64) (int64, error) {
	const op = "GrantExtension"
	if aid == "" || target == "" {
		return 0, newErr(op, ErrInvalidArgument, "empty id")
	}
	if kind != PersonalTarget && kind != GroupTarget {
		return 0, newErr(op, ErrInvalidArgument, "unknown target kind %d", kind)
	}
	if duration < 0 {
		return 0, newErr(op, ErrInvalidArgument, "negative duration %d", duration)
	}
	if now < 0 {
		return 0, newErr(op, ErrInvalidArgument, "negative time %d", now)
	}
	if now < n.clock {
		return 0, newErr(op, ErrClockRegression, "now=%d before clock %d", now, n.clock)
	}
	a, err := n.lookup(op, aid)
	if err != nil {
		return 0, err
	}
	if kind == GroupTarget {
		if _, ok := a.groups[target]; !ok {
			return 0, newErr(op, ErrNotFound, "group %q not found", target)
		}
	}
	if a.settled {
		return 0, newErr(op, ErrAlreadySettled, "assignment %q settled", aid)
	}
	if a.cfg.Deadline+duration > a.cfg.HardClose {
		return 0, newErr(op, ErrExtensionExceedsClose,
			"deadline %d + duration %d exceeds hard close %d", a.cfg.Deadline, duration, a.cfg.HardClose)
	}
	a.nextGrant++
	a.grants = append(a.grants, nGrant{id: a.nextGrant, kind: kind, target: target, duration: duration})
	n.clock = now
	n.note = fmt.Sprintf("grant#%d kind=%d target=%s duration=%d", a.nextGrant, kind, target, duration)
	return a.nextGrant, nil
}

func (n *naiveEngine) RevokeExtension(aid string, grantID int64, now int64) error {
	const op = "RevokeExtension"
	if aid == "" || grantID <= 0 {
		return newErr(op, ErrInvalidArgument, "empty assignment or non-positive grant id")
	}
	if now < 0 {
		return newErr(op, ErrInvalidArgument, "negative time %d", now)
	}
	if now < n.clock {
		return newErr(op, ErrClockRegression, "now=%d before clock %d", now, n.clock)
	}
	a, err := n.lookup(op, aid)
	if err != nil {
		return err
	}
	idx := -1
	for i := range a.grants {
		if a.grants[i].id == grantID && !a.grants[i].revoked {
			idx = i
			break
		}
	}
	if idx < 0 {
		return newErr(op, ErrNotFound, "grant %d not found", grantID)
	}
	if a.settled {
		return newErr(op, ErrAlreadySettled, "assignment %q settled", aid)
	}
	a.grants[idx].revoked = true
	n.clock = now
	n.note = fmt.Sprintf("revoked grant#%d", grantID)
	return nil
}

func (n *naiveEngine) JoinGroup(aid, groupID, member string, now int64) error {
	const op = "JoinGroup"
	if aid == "" || groupID == "" || member == "" {
		return newErr(op, ErrInvalidArgument, "empty id")
	}
	if now < 0 {
		return newErr(op, ErrInvalidArgument, "negative time %d", now)
	}
	if now < n.clock {
		return newErr(op, ErrClockRegression, "now=%d before clock %d", now, n.clock)
	}
	a, err := n.lookup(op, aid)
	if err != nil {
		return err
	}
	if a.settled {
		return newErr(op, ErrAlreadySettled, "assignment %q settled", aid)
	}
	if now > a.cfg.HardClose {
		return newErr(op, ErrClosed, "now=%d past hard close %d", now, a.cfg.HardClose)
	}
	if !a.cfg.GroupWork {
		return newErr(op, ErrStateNotAllowed, "assignment %q is not group work", aid)
	}
	if _, busy := a.memberGroup[member]; busy {
		return newErr(op, ErrStateNotAllowed, "member %q already in a group", member)
	}
	g, ok := a.groups[groupID]
	if ok {
		if _, left := g.former[member]; left {
			return newErr(op, ErrStateNotAllowed, "member %q already left group %q", member, groupID)
		}
		if len(a.versions[groupID]) > 0 {
			return newErr(op, ErrStateNotAllowed, "group %q already submitted", groupID)
		}
	} else {
		g = &nGroup{members: map[string]bool{}, former: map[string]int{}}
		a.groups[groupID] = g
		a.subjects[groupID] = true
	}
	g.members[member] = true
	a.memberGroup[member] = groupID
	n.clock = now
	n.note = fmt.Sprintf("member %s joined %s", member, groupID)
	return nil
}

func (n *naiveEngine) LeaveGroup(aid, groupID, member string, now int64) error {
	const op = "LeaveGroup"
	if aid == "" || groupID == "" || member == "" {
		return newErr(op, ErrInvalidArgument, "empty id")
	}
	if now < 0 {
		return newErr(op, ErrInvalidArgument, "negative time %d", now)
	}
	if now < n.clock {
		return newErr(op, ErrClockRegression, "now=%d before clock %d", now, n.clock)
	}
	a, err := n.lookup(op, aid)
	if err != nil {
		return err
	}
	g, ok := a.groups[groupID]
	if !ok {
		return newErr(op, ErrNotFound, "group %q not found", groupID)
	}
	if a.settled {
		return newErr(op, ErrAlreadySettled, "assignment %q settled", aid)
	}
	if now > a.cfg.HardClose {
		return newErr(op, ErrClosed, "now=%d past hard close %d", now, a.cfg.HardClose)
	}
	if !g.members[member] {
		return newErr(op, ErrStateNotAllowed, "member %q not in group %q", member, groupID)
	}
	delete(g.members, member)
	delete(a.memberGroup, member)
	g.former[member] = len(a.versions[groupID])
	n.clock = now
	n.note = fmt.Sprintf("member %s left %s at version %d", member, groupID, g.former[member])
	return nil
}

func (n *naiveEngine) DesignateVersion(aid, subject, member string, versionNum int, now int64) error {
	const op = "DesignateVersion"
	if aid == "" || subject == "" || member == "" || versionNum <= 0 {
		return newErr(op, ErrInvalidArgument, "empty id or non-positive version")
	}
	if now < 0 {
		return newErr(op, ErrInvalidArgument, "negative time %d", now)
	}
	if now < n.clock {
		return newErr(op, ErrClockRegression, "now=%d before clock %d", now, n.clock)
	}
	a, err := n.lookup(op, aid)
	if err != nil {
		return err
	}
	if !a.subjects[subject] {
		return newErr(op, ErrNotFound, "subject %q not found", subject)
	}
	vs := a.versions[subject]
	if versionNum > len(vs) {
		return newErr(op, ErrNotFound, "version %d not found", versionNum)
	}
	if a.settled {
		return newErr(op, ErrAlreadySettled, "assignment %q settled", aid)
	}
	if now > a.cfg.HardClose {
		return newErr(op, ErrClosed, "now=%d past hard close %d", now, a.cfg.HardClose)
	}
	if a.cfg.GroupWork {
		if !a.groups[subject].members[member] {
			return newErr(op, ErrStateNotAllowed, "member %q not in group %q", member, subject)
		}
	} else if subject != member {
		return newErr(op, ErrStateNotAllowed, "cannot designate for %q as %q", subject, member)
	}
	if vs[versionNum-1].invalid {
		return newErr(op, ErrVersionInvalid, "version %d is invalid", versionNum)
	}
	a.designated[subject] = versionNum
	n.clock = now
	n.note = fmt.Sprintf("subject %s designated v%d", subject, versionNum)
	return nil
}

func (n *naiveEngine) Settle(aid string, now int64) ([]MemberSettlement, error) {
	const op = "Settle"
	if aid == "" {
		return nil, newErr(op, ErrInvalidArgument, "empty assignment id")
	}
	if now < 0 {
		return nil, newErr(op, ErrInvalidArgument, "negative time %d", now)
	}
	if now < n.clock {
		return nil, newErr(op, ErrClockRegression, "now=%d before clock %d", now, n.clock)
	}
	a, err := n.lookup(op, aid)
	if err != nil {
		return nil, err
	}
	if a.settled {
		return nil, newErr(op, ErrAlreadySettled, "assignment %q settled", aid)
	}
	if now <= a.cfg.HardClose {
		return nil, newErr(op, ErrStateNotAllowed, "now=%d not past hard close %d", now, a.cfg.HardClose)
	}
	a.result = n.computeSettlement(a)
	a.settled = true
	n.clock = now
	n.note = fmt.Sprintf("settled %d members", len(a.result))
	return append([]MemberSettlement(nil), a.result...), nil
}

func (n *naiveEngine) computeSettlement(a *nAssignment) []MemberSettlement {
	var out []MemberSettlement
	emit := func(subject, member string, vnum int) {
		ms := MemberSettlement{AssignmentID: a.cfg.ID, SubjectID: subject, MemberID: member}
		vs := a.versions[subject]
		if vnum >= 1 && vnum <= len(vs) {
			if snap, ok := vs[vnum-1].snaps[member]; ok {
				ms.Version = vnum
				ms.Late = snap.late
				ms.Penalty = snap.penalty
				ms.Invalid = snap.invalid
			}
		}
		if ms.Version == 0 {
			ms.Invalid = true
		}
		out = append(out, ms)
	}
	for subject := range a.subjects {
		g := a.designated[subject]
		if g == 0 {
			vs := a.versions[subject]
			for i := len(vs); i >= 1; i-- {
				if a.cfg.AllowLateOverride {
					if !vs[i-1].invalid {
						g = i
						break
					}
				} else if vs[i-1].onTime {
					g = i
					break
				}
			}
		}
		if a.cfg.GroupWork {
			grp := a.groups[subject]
			for m := range grp.members {
				emit(subject, m, g)
			}
			for m, lv := range grp.former {
				emit(subject, m, lv)
			}
		} else {
			emit(subject, subject, g)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SubjectID != out[j].SubjectID {
			return out[i].SubjectID < out[j].SubjectID
		}
		return out[i].MemberID < out[j].MemberID
	})
	return out
}

func (n *naiveEngine) EffectiveDeadline(aid, member string) (int64, error) {
	const op = "EffectiveDeadline"
	if aid == "" || member == "" {
		return 0, newErr(op, ErrInvalidArgument, "empty id")
	}
	a, err := n.lookup(op, aid)
	if err != nil {
		return 0, err
	}
	best := a.personalMax(member)
	if gid, ok := a.memberGroup[member]; ok {
		if gm := a.groupMax(gid); gm > best {
			best = gm
		}
	}
	return a.cfg.Deadline + best, nil
}

// system 抽象引擎与朴素模型的共同接口。
type system interface {
	CreateAssignment(cfg AssignmentConfig, now int64) error
	Submit(aid, subject, member string, now int64) (int, error)
	GrantExtension(aid string, kind TargetKind, target string, duration int64, now int64) (int64, error)
	RevokeExtension(aid string, grantID int64, now int64) error
	JoinGroup(aid, group, member string, now int64) error
	LeaveGroup(aid, group, member string, now int64) error
	DesignateVersion(aid, subject, member string, versionNum int, now int64) error
	Settle(aid string, now int64) ([]MemberSettlement, error)
	EffectiveDeadline(aid, member string) (int64, error)
}

var (
	_ system = (*Engine)(nil)
	_ system = (*naiveEngine)(nil)
)

func kindOf(err error) ErrKind {
	if err == nil {
		return -1
	}
	k, _ := ErrKindOf(err)
	return k
}

func errString(err error) string {
	if err == nil {
		return "ok"
	}
	k, _ := ErrKindOf(err)
	return k.String()
}

func equalSettlements(a, b []MemberSettlement) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// 随机生成的操作序列同时作用于引擎与朴素模型，逐步比对，
// 日志打印每步输入、输出与判定依据。
func TestRandomizedAgainstNaiveModel(t *testing.T) {
	for seed := int64(1); seed <= 6; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runDifferential(t, seed)
		})
	}
}

func runDifferential(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	eng := NewEngine()
	model := newNaiveEngine()
	configs := []AssignmentConfig{
		{ID: "ind", Deadline: 20, HardClose: 40,
			Tiers: []Tier{{MaxLate: 4, Penalty: 0.1}, {MaxLate: 8, Penalty: 0.25}, {MaxLate: 15, Penalty: 0.5}}},
		{ID: "ovr", Deadline: 20, HardClose: 40, AllowLateOverride: true,
			Tiers: []Tier{{MaxLate: 4, Penalty: 0.1}, {MaxLate: 8, Penalty: 0.25}, {MaxLate: 15, Penalty: 0.5}}},
		{ID: "grp", Deadline: 15, HardClose: 30, GroupWork: true, AllowLateOverride: true,
			Tiers: []Tier{{MaxLate: 3, Penalty: 0.1}, {MaxLate: 6, Penalty: 0.3}}},
	}
	for _, cfg := range configs {
		if err := eng.CreateAssignment(cfg, 0); err != nil {
			t.Fatalf("engine create: %v", err)
		}
		if err := model.CreateAssignment(cfg, 0); err != nil {
			t.Fatalf("model create: %v", err)
		}
	}
	members := []string{"m1", "m2", "m3", "m4"}
	groups := []string{"g1", "g2", "gX"} // gX 不存在
	assignmentIDs := []string{"ind", "ovr", "grp"}
	pick := func(xs []string) string { return xs[rng.Intn(len(xs))] }

	var maxGrant int64
	var lastNow int64
	type subjectKey struct{ aid, subject string }
	submitCount := map[subjectKey]int{}
	maxVersion := map[subjectKey]int{}

	mismatch := func(step int, what string) {
		t.Fatalf("step %d: %s mismatch between engine and naive model", step, what)
	}
	compareErr := func(step int, e1, e2 error) {
		if kindOf(e1) != kindOf(e2) {
			t.Fatalf("step %d: engine err %v, model err %v", step, e1, e2)
		}
	}

	const steps = 400
	for i := 0; i < steps; i++ {
		delta := []int64{0, 0, 1, 1, 2, 4, -1, -3}[rng.Intn(8)]
		now := lastNow + delta
		if now < 0 {
			now = 0
		}
		lastNow = now
		aid := pick(assignmentIDs)
		member := pick(members)
		subject := member
		if aid == "grp" {
			subject = pick(groups)
		}
		model.note = ""

		switch op := rng.Intn(100); {
		case op < 28: // 提交
			v1, e1 := eng.Submit(aid, subject, member, now)
			v2, e2 := model.Submit(aid, subject, member, now)
			t.Logf("step %03d submit aid=%s subject=%s member=%s now=%d -> engine(v=%d,%s) model(v=%d,%s) basis[%s]",
				i, aid, subject, member, now, v1, errString(e1), v2, errString(e2), model.note)
			compareErr(i, e1, e2)
			if e1 == nil && v1 != v2 {
				mismatch(i, "version number")
			}
			if e1 == nil {
				k := subjectKey{aid, subject}
				submitCount[k]++
				maxVersion[k] = v1
			}
		case op < 43: // 授予延期
			kind := PersonalTarget
			target := member
			if rng.Intn(2) == 0 {
				kind = GroupTarget
				target = pick(groups)
			}
			duration := rng.Int63n(30)
			id1, e1 := eng.GrantExtension(aid, kind, target, duration, now)
			id2, e2 := model.GrantExtension(aid, kind, target, duration, now)
			t.Logf("step %03d grant aid=%s kind=%d target=%s dur=%d now=%d -> engine(id=%d,%s) model(id=%d,%s) basis[%s]",
				i, aid, kind, target, duration, now, id1, errString(e1), id2, errString(e2), model.note)
			compareErr(i, e1, e2)
			if e1 == nil && id1 != id2 {
				mismatch(i, "grant id")
			}
			if e1 == nil {
				maxGrant = id1
			}
		case op < 51: // 撤销延期
			gid := int64(1 + rng.Intn(int(maxGrant+2)))
			e1 := eng.RevokeExtension(aid, gid, now)
			e2 := model.RevokeExtension(aid, gid, now)
			t.Logf("step %03d revoke aid=%s grant=%d now=%d -> engine(%s) model(%s) basis[%s]",
				i, aid, gid, now, errString(e1), errString(e2), model.note)
			compareErr(i, e1, e2)
		case op < 59: // 加入小组
			g := pick(groups)
			e1 := eng.JoinGroup(aid, g, member, now)
			e2 := model.JoinGroup(aid, g, member, now)
			t.Logf("step %03d join aid=%s group=%s member=%s now=%d -> engine(%s) model(%s) basis[%s]",
				i, aid, g, member, now, errString(e1), errString(e2), model.note)
			compareErr(i, e1, e2)
		case op < 65: // 退出小组
			g := pick(groups)
			e1 := eng.LeaveGroup(aid, g, member, now)
			e2 := model.LeaveGroup(aid, g, member, now)
			t.Logf("step %03d leave aid=%s group=%s member=%s now=%d -> engine(%s) model(%s) basis[%s]",
				i, aid, g, member, now, errString(e1), errString(e2), model.note)
			compareErr(i, e1, e2)
		case op < 75: // 显式指定评分版本
			ver := rng.Intn(4)
			e1 := eng.DesignateVersion(aid, subject, member, ver, now)
			e2 := model.DesignateVersion(aid, subject, member, ver, now)
			t.Logf("step %03d designate aid=%s subject=%s member=%s v=%d now=%d -> engine(%s) model(%s) basis[%s]",
				i, aid, subject, member, ver, now, errString(e1), errString(e2), model.note)
			compareErr(i, e1, e2)
		case op < 80: // 结算
			r1, e1 := eng.Settle(aid, now)
			r2, e2 := model.Settle(aid, now)
			t.Logf("step %03d settle aid=%s now=%d -> engine(%d rows,%s) model(%d rows,%s) basis[%s]",
				i, aid, now, len(r1), errString(e1), len(r2), errString(e2), model.note)
			compareErr(i, e1, e2)
			if e1 == nil && !equalSettlements(r1, r2) {
				t.Fatalf("step %d: settlement mismatch\nengine: %v\nmodel:  %v", i, r1, r2)
			}
		default: // 查询有效截止
			d1, e1 := eng.EffectiveDeadline(aid, member)
			d2, e2 := model.EffectiveDeadline(aid, member)
			t.Logf("step %03d effdeadline aid=%s member=%s -> engine(%d,%s) model(%d,%s)",
				i, aid, member, d1, errString(e1), d2, errString(e2))
			compareErr(i, e1, e2)
			if e1 == nil && d1 != d2 {
				mismatch(i, "effective deadline")
			}
		}
	}
	// 每个主体的版本号连续无洞：成功提交次数必等于最大版本号。
	for k, cnt := range submitCount {
		if maxVersion[k] != cnt {
			t.Fatalf("subject %+v: max version %d != successful submits %d (hole detected)", k, maxVersion[k], cnt)
		}
	}
	t.Logf("seed=%d done: %d subjects submitted, maxGrant=%d", seed, len(submitCount), maxGrant)
}
