package settlement

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// 本文件用“独立按题意直接写成的朴素模型”（naive）与引擎做随机对照：
// 每步操作同时喂给两边，比较错误类别、版本号与最终结算结果；
// -v 时打印每步输入、输出与判定依据。

type nExt struct {
	id       int
	target   string
	grantAt  int
	revokeAt int
	amount   int
}

func (n *naive) settle(now int) (map[string]*MemberResult, *ClassifiedError) {
	if now < n.cfg.HardClose {
		return nil, ErrInvalidParam
	}
	if err := n.rollback(now); err != nil {
		return nil, err
	}
	if n.settled {
		return nil, ErrSettled
	}
	n.tick = now
	n.settled = true
	out := map[string]*MemberResult{}
	for p := range n.people {
		var vs []*nVer
		var desig int
		if n.cfg.IsGroup {
			gid, in := n.pgroup[p]
			if !in {
				out[p] = &MemberResult{PersonID: p, NoValidVersion: true}
				continue
			}
			vs = n.gver[gid]
			if n.leftAt[p] != 0 {
				vs = vs[:n.leftNo[p]]
			}
			for len(vs) > 0 {
				if _, ok := vs[0].memberDeadline[p]; ok {
					break
				}
				vs = vs[1:]
			}
			desig = n.gdesig[gid]
			if desig > 0 {
				idx := 0
				for i, v := range vs {
					if v.no == desig {
						idx = i + 1
						break
					}
				}
				desig = idx
			}
		} else {
			vs = n.sver[p]
			desig = n.sdesig[p]
		}
		mdl := func(v *nVer) int {
			if n.cfg.IsGroup {
				if d, ok := v.memberDeadline[p]; ok {
					return d
				}
				return v.subjDeadline
			}
			return v.subjDeadline
		}
		valid := func(v *nVer) bool {
			late := v.at - mdl(v)
			if late <= 0 {
				return true
			}
			_, too := n.cfg.penaltyFor(late)
			return !too
		}
		var pick *nVer
		if desig > 0 && desig <= len(vs) && valid(vs[desig-1]) {
			pick = vs[desig-1]
		}
		if pick == nil {
			if n.cfg.AllowLateOverride {
				for i := len(vs) - 1; i >= 0; i-- {
					if valid(vs[i]) {
						pick = vs[i]
						break
					}
				}
			} else {
				for i := len(vs) - 1; i >= 0; i-- {
					if vs[i].at <= mdl(vs[i]) {
						pick = vs[i]
						break
					}
				}
			}
		}
		r := &MemberResult{PersonID: p}
		if pick == nil {
			r.NoValidVersion = true
			out[p] = r
			continue
		}
		dl := mdl(pick)
		late := pick.at - dl
		r.VersionNo = pick.no
		r.SubmittedAt = pick.at
		if late <= 0 {
			r.OnTime = true
		} else {
			r.LateDuration = late
			bps, too := n.cfg.penaltyFor(late)
			r.PenaltyBPS = bps
			r.Invalid = too
		}
		out[p] = r
	}
	return out, nil
}

// ---- 随机操作序列生成与双跑比较 ----

type op struct {
	kind string
	now  int
	args []string
	n    int
}

func TestDifferentialRandom(t *testing.T) {
	const trials = 1500
	for seed := int64(0); seed < trials; seed++ {
		rng := rand.New(rand.NewSource(seed))
		isGroup := rng.Intn(2) == 0
		override := rng.Intn(2) == 0
		runOneTrial(t, rng, seed, isGroup, override)
	}
}

func runOneTrial(t *testing.T, rng *rand.Rand, seed int64, isGroup, override bool) {
	t.Helper()
	const deadline, hardClose = 12, 28
	persons := []string{"p0", "p1", "p2", "p3"}
	cfg := Config{
		Deadline: deadline, HardClose: hardClose, IsGroup: isGroup,
		Tiers: []Tier{
			{MaxLate: 2, PenaltyBPS: 1000},
			{MaxLate: 6, PenaltyBPS: 3000},
			{MaxLate: 10, PenaltyBPS: 6000},
		},
		AllowLateOverride: override,
	}
	e := NewEngine()
	aid := fmt.Sprintf("a%d", seed)
	if err := e.CreateAssignment(aid, cfg, 0); err != nil {
		t.Fatal(err)
	}
	for _, p := range persons {
		if err := e.AddPerson(aid, p, 0); err != nil {
			t.Fatal(err)
		}
	}
	n := newNaive(cfg, persons)

	// 已授予的延期（目标 -> 未撤销 extID），随机撤销用。
	aliveExt := map[string][]int{}
	groupID := "g0"
	if isGroup {
		initMembers := persons[:2+rng.Intn(2)] // 2~3 个初始成员
		if err := e.CreateGroup(aid, groupID, 1, initMembers); err != nil {
			t.Fatal(err)
		}
		if ce := n.createGroup(groupID, 1, initMembers); ce != nil {
			t.Fatal(ce)
		}
	}

	now := 1
	var log []string
	addLog := func(format string, a ...any) { log = append(log, fmt.Sprintf(format, a...)) }

	steps := 60
	for step := 0; step < steps; step++ {
		// 随机序列使用单调不降的时刻；时钟回退拒绝由边界测试专门覆盖
		// （TestClockRollback / TestPriorityClockOverNotFound）。
		now += rng.Intn(3)

		person := persons[rng.Intn(len(persons))]
		var kind string
		if isGroup {
			kind = []string{
				"submit", "submit", "grantP", "grantG", "revoke",
				"join", "leave", "designate", "noop",
			}[rng.Intn(9)]
		} else {
			kind = []string{
				"submit", "submit", "grantP", "revoke",
				"designate", "noop",
			}[rng.Intn(6)]
		}

		var eErr, nErr error
		var eNo, nNo int

		switch kind {
		case "submit":
			subj := person
			if isGroup {
				subj = groupID
			}
			var ev *Version
			ev, eErr = e.Submit(aid, subj, person, now)
			if ev != nil {
				eNo = ev.No
			}
			var nv *nVer
			nv, nErr2 := n.submit(subj, person, now)
			nErr = nErr2
			if nv != nil {
				nNo = nv.no
			}
			addLog("step%d t=%d submit(subj=%s by=%s) -> engErr=%v(no=%d) naiveErr=%v(no=%d)",
				step, now, subj, person, cls(eErr), eNo, cls(nErr), nNo)
		case "grantP":
			amount := 1 + rng.Intn(hardClose-deadline+4) // 含会触发封顶的值
			id, e1 := e.GrantExtension(aid, person, false, now, amount)
			nid, n1 := n.grant(person, false, now, amount, id)
			eErr, nErr = e1, n1
			if e1 == nil {
				aliveExt[person] = append(aliveExt[person], id)
			}
			addLog("step%d t=%d grantPersonal(target=%s amount=%d) -> eng=%v(id=%d) naive=%v(id=%d)",
				step, now, person, amount, cls(eErr), id, cls(nErr), nid)
		case "grantG":
			amount := 1 + rng.Intn(hardClose-deadline+4)
			id, e1 := e.GrantExtension(aid, groupID, true, now, amount)
			_, n1 := n.grant(groupID, true, now, amount, id)
			eErr, nErr = e1, n1
			if e1 == nil {
				aliveExt[groupID] = append(aliveExt[groupID], id)
			}
			addLog("step%d t=%d grantGroup(amount=%d) -> eng=%v naive=%v",
				step, now, amount, cls(eErr), cls(nErr))
		case "revoke":
			target := person
			isGrp := false
			if isGroup && rng.Intn(2) == 0 {
				target, isGrp = groupID, true
			}
			list := aliveExt[target]
			var extID int
			if len(list) > 0 {
				i := rng.Intn(len(list))
				extID = list[i]
			} else {
				extID = 1 + rng.Intn(5)
			}
			eErr = e.RevokeExtension(aid, target, isGrp, now, extID)
			nErr2 := n.revoke(target, isGrp, now, extID)
			nErr = nErr2
			if eErr == nil {
				l2 := list[:0]
				for _, x := range list {
					if x != extID {
						l2 = append(l2, x)
					}
				}
				aliveExt[target] = l2
			}
			addLog("step%d t=%d revoke(target=%s group=%v ext=%d) -> eng=%v naive=%v",
				step, now, target, isGrp, extID, cls(eErr), cls(nErr))
		case "join":
			eErr = e.Join(aid, groupID, person, now)
			nErr2 := n.join(groupID, person, now)
			nErr = nErr2
			addLog("step%d t=%d join(%s) -> eng=%v naive=%v", step, now, person, cls(eErr), cls(nErr))
		case "leave":
			_, eErr = e.Leave(aid, groupID, person, now)
			nErr2 := n.leave(groupID, person, now)
			nErr = nErr2
			addLog("step%d t=%d leave(%s) -> eng=%v naive=%v", step, now, person, cls(eErr), cls(nErr))
		case "designate":
			no := 1 + rng.Intn(4)
			subj := person
			if isGroup {
				subj = groupID
			}
			eErr = e.Designate(aid, subj, person, now, no)
			nErr2 := n.designate(subj, person, now, no)
			nErr = nErr2
			addLog("step%d t=%d designate(subj=%s by=%s no=%d) -> eng=%v naive=%v",
				step, now, subj, person, no, cls(eErr), cls(nErr))
		case "noop":
			addLog("step%d t=%d noop", step, now)
		}

		if cls(eErr) != cls(nErr) || eNo != nNo {
			t.Fatalf("seed=%d step=%d kind=%s divergence: eng=%v(no=%d) naive=%v(no=%d)\nLOG:\n%s",
				seed, step, kind, cls(eErr), eNo, cls(nErr), nNo, joinLines(log))
		}
	}

	// 在 HardClose 之后结算（偶尔尝试提前结算，触发非法参数）。
	settleAt := hardClose
	if rng.Intn(3) == 0 {
		settleAt = hardClose - 1
	}
	eRes, eErr := e.Settle(aid, settleAt)
	nRes, nErr := n.settle(settleAt)
	addLog("settle t=%d -> eng=%v naive=%v", settleAt, cls(eErr), cls(nErr))
	if cls(eErr) != cls(nErr) {
		t.Fatalf("seed=%d settle divergence: eng=%v naive=%v\nLOG:\n%s",
			seed, cls(eErr), cls(nErr), joinLines(log))
	}
	if eErr == nil {
		if len(eRes) != len(nRes) {
			t.Fatalf("seed=%d result size %d vs %d", seed, len(eRes), len(nRes))
		}
		for p, er := range eRes {
			nr := nRes[p]
			if nr == nil || *er != *nr {
				t.Fatalf("seed=%d member=%s divergence eng=%+v naive=%+v\nLOG:\n%s",
					seed, p, er, nr, joinLines(log))
			}
		}
		// 结算后冻结：再结算必须双方都报已结算。
		if _, err := e.Settle(aid, settleAt+1); cls(err) != PrioritySettled {
			t.Fatalf("seed=%d re-settle eng=%v", seed, cls(err))
		}
		if _, err := n.settle(settleAt + 1); cls(err) != PrioritySettled {
			t.Fatalf("seed=%d re-settle naive=%v", seed, cls(err))
		}
		t.Logf("seed=%d group=%v override=%v:\n%s", seed, isGroup, override, joinLines(log))
	}
}

func joinLines(xs []string) string {
	out := ""
	for _, x := range xs {
		out += "  " + x + "\n"
	}
	return out
}

func (n *naive) grant(target string, isGroup bool, now, amount, assignedID int) (int, *ClassifiedError) {
	if target == "" || amount <= 0 {
		return 0, ErrInvalidParam
	}
	if err := n.rollback(now); err != nil {
		return 0, err
	}
	if n.settled {
		return 0, ErrSettled
	}
	if isGroup {
		if !n.cfg.IsGroup {
			return 0, ErrInvalidParam
		}
		if _, ok := n.groups[target]; !ok {
			return 0, ErrNotFound
		}
	} else if !n.people[target] {
		return 0, ErrNotFound
	}
	if now > n.cfg.HardClose {
		return 0, ErrAfterHardClose
	}
	if amount > n.aliveExtra(target) && n.cfg.Deadline+amount > n.cfg.HardClose {
		return 0, ErrExtensionPastClose
	}
	n.tick = now
	id := n.nextExt
	n.nextExt++
	if n.idMap[target] == nil {
		n.idMap[target] = map[int]int{}
	}
	n.idMap[target][assignedID] = id
	n.exts = append(n.exts, &nExt{id: id, target: target, grantAt: now, amount: amount})
	return id, nil
}

func (n *naive) revoke(target string, isGroup bool, now, extID int) *ClassifiedError {
	if target == "" || extID <= 0 {
		return ErrInvalidParam
	}
	if err := n.rollback(now); err != nil {
		return err
	}
	if n.settled {
		return ErrSettled
	}
	if isGroup {
		if !n.cfg.IsGroup {
			return ErrInvalidParam
		}
		if _, ok := n.groups[target]; !ok {
			return ErrNotFound
		}
	} else if !n.people[target] {
		return ErrNotFound
	}
	if now > n.cfg.HardClose {
		return ErrAfterHardClose
	}
	var found *nExt
	for _, x := range n.exts {
		if x.id == extID && x.target == target {
			found = x
			break
		}
	}
	if found == nil || found.revokeAt != 0 {
		return ErrNotFound
	}
	n.tick = now
	found.revokeAt = now
	return nil
}

func (n *naive) submit(subj, person string, now int) (*nVer, *ClassifiedError) {
	if subj == "" || person == "" {
		return nil, ErrInvalidParam
	}
	if n.settled {
		return nil, ErrSettled
	}
	if !n.people[person] {
		return nil, ErrNotFound
	}
	var old []*nVer
	deadline := n.cfg.Deadline
	md := map[string]int{}
	if n.cfg.IsGroup {
		gm, ok := n.groups[subj]
		if !ok {
			return nil, ErrNotFound
		}
		if err := n.rollback(now); err != nil {
			return nil, err
		}
		if now > n.cfg.HardClose {
			return nil, ErrAfterHardClose
		}
		if n.pgroup[person] != subj || !gm[person] {
			return nil, ErrStateNotAllowed
		}
		old = n.gver[subj]
		ge := n.extra(subj, now)
		deadline = n.cfg.Deadline + ge
		for m := range gm {
			ex := ge
			if pe := n.extra(m, now); pe > ex {
				ex = pe
			}
			md[m] = n.cfg.Deadline + ex
		}
	} else {
		if subj != person {
			return nil, ErrNotFound
		}
		if err := n.rollback(now); err != nil {
			return nil, err
		}
		if now > n.cfg.HardClose {
			return nil, ErrAfterHardClose
		}
		old = n.sver[person]
		deadline = n.cfg.Deadline + n.extra(person, now)
	}
	n.tick = now
	v := &nVer{no: len(old) + 1, at: now, subjDeadline: deadline, memberDeadline: md}
	if n.cfg.IsGroup {
		n.gver[subj] = append(n.gver[subj], v)
	} else {
		n.sver[person] = append(n.sver[person], v)
	}
	return v, nil
}

func (n *naive) designate(subj, person string, now, no int) *ClassifiedError {
	if subj == "" || person == "" || no <= 0 {
		return ErrInvalidParam
	}
	if err := n.rollback(now); err != nil {
		return err
	}
	if n.settled {
		return ErrSettled
	}
	if !n.people[person] {
		return ErrNotFound
	}
	if n.cfg.IsGroup {
		if _, ok := n.groups[subj]; !ok {
			return ErrNotFound
		}
	} else if subj != person {
		return ErrNotFound
	}
	// 关闭检查先于状态检查，与固定优先级一致。
	if now > n.cfg.HardClose {
		return ErrAfterHardClose
	}
	var vs []*nVer
	if n.cfg.IsGroup {
		gm := n.groups[subj]
		if n.pgroup[person] != subj || !gm[person] {
			return ErrStateNotAllowed
		}
		vs = n.gver[subj]
	} else {
		vs = n.sver[person]
	}
	if no > len(vs) {
		return ErrNotFound
	}
	v := vs[no-1]
	if late := v.at - v.subjDeadline; late > 0 {
		if _, tooLate := n.cfg.penaltyFor(late); tooLate {
			return ErrInvalidVersion
		}
	}
	n.tick = now
	if n.cfg.IsGroup {
		n.gdesig[subj] = no
	} else {
		n.sdesig[person] = no
	}
	return nil
}

type nVer struct {
	no             int
	at             int
	subjDeadline   int
	memberDeadline map[string]int
}

type naive struct {
	cfg     Config
	people  map[string]bool
	groups  map[string]map[string]bool
	leftAt  map[string]int
	leftNo  map[string]int
	pgroup  map[string]string
	exts    []*nExt
	nextExt int
	gver    map[string][]*nVer
	sver    map[string][]*nVer
	gdesig  map[string]int
	sdesig  map[string]int
	idMap   map[string]map[int]int
	settled bool
	tick    int
}

func newNaive(cfg Config, persons []string) *naive {
	n := &naive{
		cfg: cfg, people: map[string]bool{}, groups: map[string]map[string]bool{},
		leftAt: map[string]int{}, leftNo: map[string]int{}, pgroup: map[string]string{},
		nextExt: 1, gver: map[string][]*nVer{}, sver: map[string][]*nVer{},
		gdesig: map[string]int{}, sdesig: map[string]int{}, idMap: map[string]map[int]int{},
	}
	for _, p := range persons {
		n.people[p] = true
	}
	return n
}

func cls(err error) Priority {
	if err == nil {
		return 0
	}
	v := reflect.ValueOf(err)
	if v.Kind() == reflect.Ptr && v.IsNil() {
		return 0
	}
	if ce, ok := err.(*ClassifiedError); ok {
		return ce.Priority()
	}
	return -1
}

// extra：时刻 t、目标 target 可见延期最大值的朴素线性扫描。
func (n *naive) extra(target string, t int) int {
	best := 0
	for _, x := range n.exts {
		if x.target != target {
			continue
		}
		if x.grantAt < t && (x.revokeAt == 0 || t <= x.revokeAt) && x.amount > best {
			best = x.amount
		}
	}
	return best
}

func (n *naive) aliveExtra(target string) int {
	best := 0
	for _, x := range n.exts {
		if x.target == target && x.revokeAt == 0 && x.amount > best {
			best = x.amount
		}
	}
	return best
}

func (n *naive) rollback(now int) *ClassifiedError {
	if now < n.tick {
		return ErrClockRollback
	}
	return nil
}

func (n *naive) createGroup(gid string, now int, members []string) *ClassifiedError {
	if gid == "" || len(members) == 0 || hasEmpty(members) || hasDup(members) {
		return ErrInvalidParam
	}
	if err := n.rollback(now); err != nil {
		return err
	}
	if !n.cfg.IsGroup {
		return ErrInvalidParam
	}
	if n.settled {
		return ErrSettled
	}
	if now > n.cfg.HardClose {
		return ErrAfterHardClose
	}
	if _, ok := n.groups[gid]; ok {
		return ErrInvalidParam
	}
	for _, p := range members {
		if !n.people[p] {
			return ErrNotFound
		}
		if _, in := n.pgroup[p]; in || n.leftAt[p] != 0 {
			return ErrStateNotAllowed
		}
	}
	n.tick = now
	n.groups[gid] = map[string]bool{}
	for _, p := range members {
		n.groups[gid][p] = true
		n.pgroup[p] = gid
	}
	return nil
}

func (n *naive) join(gid, p string, now int) *ClassifiedError {
	if gid == "" || p == "" {
		return ErrInvalidParam
	}
	if err := n.rollback(now); err != nil {
		return err
	}
	if !n.cfg.IsGroup {
		return ErrInvalidParam
	}
	if n.settled {
		return ErrSettled
	}
	if _, ok := n.groups[gid]; !ok {
		return ErrNotFound
	}
	if !n.people[p] {
		return ErrNotFound
	}
	if now > n.cfg.HardClose {
		return ErrAfterHardClose
	}
	if _, in := n.pgroup[p]; in || n.leftAt[p] != 0 {
		return ErrStateNotAllowed
	}
	if len(n.gver[gid]) > 0 {
		return ErrStateNotAllowed
	}
	n.tick = now
	n.groups[gid][p] = true
	n.pgroup[p] = gid
	return nil
}

func (n *naive) leave(gid, p string, now int) *ClassifiedError {
	if gid == "" || p == "" {
		return ErrInvalidParam
	}
	if err := n.rollback(now); err != nil {
		return err
	}
	if !n.cfg.IsGroup {
		return ErrInvalidParam
	}
	if n.settled {
		return ErrSettled
	}
	if _, ok := n.groups[gid]; !ok {
		return ErrNotFound
	}
	if !n.people[p] {
		return ErrNotFound
	}
	if now > n.cfg.HardClose {
		return ErrAfterHardClose
	}
	if n.pgroup[p] != gid || !n.groups[gid][p] {
		return ErrStateNotAllowed
	}
	n.tick = now
	latest := 0
	if vs := n.gver[gid]; len(vs) > 0 {
		latest = vs[len(vs)-1].no
	}
	delete(n.groups[gid], p)
	n.leftAt[p] = now
	n.leftNo[p] = latest
	return nil
}

// 并发提交同一主体：被接受的版本号必须恰好是 1..N 的一个排列，
// 即结果等价于某个串行顺序，且每个主体版本号连续无洞。
func TestConcurrentSubmitsSerialize(t *testing.T) {
	aid := "conc"
	e2 := NewEngine()
	cfg := Config{
		Deadline: 0, HardClose: 100,
		Tiers:   []Tier{{MaxLate: 50, PenaltyBPS: 1000}, {MaxLate: 90, PenaltyBPS: 3000}},
		IsGroup: true, AllowLateOverride: true,
	}
	if err := e2.CreateAssignment(aid+"g", cfg, 0); err != nil {
		t.Fatal(err)
	}
	const n = 8
	for i := 0; i < n; i++ {
		if err := e2.AddPerson(aid+"g", memberName(i), 0); err != nil {
			t.Fatal(err)
		}
	}
	var members []string
	for i := 0; i < n; i++ {
		members = append(members, memberName(i))
	}
	if err := e2.CreateGroup(aid+"g", "grp", 1, members); err != nil {
		t.Fatal(err)
	}

	const per = 25
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := map[int]bool{}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for k := 0; k < per; k++ {
				v, err := e2.Submit(aid+"g", "grp", memberName(i), 2+k)
				if err == nil {
					mu.Lock()
					accepted[v.No] = true
					mu.Unlock()
				} else if !errors.Is(err, ErrClockRollback) {
					t.Errorf("unexpected err: %v", err)
				}
			}
		}(i)
	}
	wg.Wait()
	if len(accepted) == 0 {
		t.Fatal("no accepted submissions")
	}
	for no := 1; no <= len(accepted); no++ {
		if !accepted[no] {
			t.Fatalf("version %d missing; sequence not serial-equivalent", no)
		}
	}
	if accepted[len(accepted)+1] {
		t.Fatal("unexpected gap beyond accepted range")
	}
}

func memberName(i int) string {
	return "m" + string(rune('a'+i))
}
