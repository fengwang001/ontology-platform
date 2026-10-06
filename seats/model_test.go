package seats

// 本文件包含一个按题目规则独立写成的朴素对照模型：不用任何增量计数与队
// 列，每次查询都全量扫描所有条目，按定义直接计算可用数。它显然正确但
// O(条目数)，用于与生产实现逐步比对随机操作序列的结果。

import (
	"fmt"
	"math/rand"
	"testing"
)

type nEntry struct {
	id     string
	status Status
	legs   []Leg
	pax    int
	expiry int64
}

type nSeg struct {
	physical, overbook int
	auth               [NumClasses]int
}

type naive struct {
	holdDur int64
	now     int64
	nextID  int
	segs    map[string]*nSeg
	entries map[string]*nEntry
}

func newNaive(holdDur int64) *naive {
	return &naive{holdDur: holdDur, segs: map[string]*nSeg{}, entries: map[string]*nEntry{}}
}

// occupied 按定义扫描全部条目，统计 segID 上 class 及更低等级在时刻 at 的
// 占用：已确认出票 + 在 at 时刻尚未到期的预占。
func (n *naive) occupied(segID string, class int, at int64) int {
	occ := 0
	for _, e := range n.entries {
		active := e.status == StatusTicketed || (e.status == StatusHold && e.expiry > at)
		if !active {
			continue
		}
		for _, l := range e.legs {
			if l.Segment == segID && l.Class >= class {
				occ += e.pax
			}
		}
	}
	return occ
}

func (n *naive) availAt(segID string, class int, at int64) int {
	return n.segs[segID].auth[class] - n.occupied(segID, class, at)
}

func (n *naive) fitsAt(segID string, class, pax int, at int64) bool {
	for c := 0; c <= class; c++ {
		if n.availAt(segID, c, at) < pax {
			return false
		}
	}
	return true
}

func (n *naive) addSegment(id string, physical, overbook int, auth [NumClasses]int, now int64) error {
	if id == "" || physical <= 0 || overbook < 0 || now < 0 {
		return errInvalid("bad segment params")
	}
	for _, a := range auth {
		if a < 0 {
			return errInvalid("negative auth")
		}
	}
	if auth[0] < auth[1] || auth[1] < auth[2] || auth[0] > physical+overbook {
		return errInvalid("auth not nested or over cap")
	}
	if _, dup := n.segs[id]; dup {
		return errInvalid("duplicate segment")
	}
	if now < n.now {
		return &Error{Kind: KindClockRollback}
	}
	n.segs[id] = &nSeg{physical: physical, overbook: overbook, auth: auth}
	n.now = now
	return nil
}

func (n *naive) checkLegs(legs []Leg, pax int) *Error {
	if len(legs) < 1 || len(legs) > 4 || pax < 1 || pax > 9 {
		return errInvalid("bad itinerary or pax")
	}
	seen := map[string]bool{}
	for _, l := range legs {
		if l.Segment == "" || l.Class < 0 || l.Class >= NumClasses || seen[l.Segment] {
			return errInvalid("bad leg")
		}
		seen[l.Segment] = true
	}
	return nil
}

func (n *naive) hold(legs []Leg, pax int, now int64) (string, int64, error) {
	if now < 0 {
		return "", 0, errInvalid("negative time")
	}
	if err := n.checkLegs(legs, pax); err != nil {
		return "", 0, err
	}
	if now < n.now {
		return "", 0, &Error{Kind: KindClockRollback}
	}
	for _, l := range legs {
		if _, ok := n.segs[l.Segment]; !ok {
			return "", 0, &Error{Kind: KindNotFound, Segment: l.Segment}
		}
	}
	for _, l := range legs {
		if !n.fitsAt(l.Segment, l.Class, pax, now) {
			return "", 0, &Error{Kind: KindInsufficient, Segment: l.Segment}
		}
	}
	id := fmt.Sprintf("E%06d", n.nextID)
	n.nextID++
	expiry := now + n.holdDur
	n.entries[id] = &nEntry{id: id, status: StatusHold, legs: append([]Leg(nil), legs...), pax: pax, expiry: expiry}
	n.now = now
	return id, expiry, nil
}

func (n *naive) find(id string, now int64) (*nEntry, *Error) {
	if id == "" || now < 0 {
		return nil, errInvalid("bad id or time")
	}
	if now < n.now {
		return nil, &Error{Kind: KindClockRollback}
	}
	e, ok := n.entries[id]
	if !ok {
		return nil, errNotFound("entry " + id)
	}
	if e.status == StatusHold && now >= e.expiry {
		return nil, &Error{Kind: KindHoldExpired}
	}
	return e, nil
}

func (n *naive) ticket(id string, now int64) error {
	e, err := n.find(id, now)
	if err != nil {
		return err
	}
	if e.status != StatusHold {
		return &Error{Kind: KindStateConflict, Status: e.status}
	}
	e.status = StatusTicketed
	n.now = now
	return nil
}

func (n *naive) cancel(id string, now int64) error {
	e, err := n.find(id, now)
	if err != nil {
		return err
	}
	if e.status == StatusCancelled {
		return &Error{Kind: KindStateConflict, Status: StatusCancelled}
	}
	e.status = StatusCancelled
	n.now = now
	return nil
}

func (n *naive) adjustAuth(segID string, class, newAuth int, now int64) error {
	if segID == "" || class < 0 || class >= NumClasses || newAuth < 0 || now < 0 {
		return errInvalid("bad adjust params")
	}
	if seg, ok := n.segs[segID]; ok {
		cand := seg.auth
		cand[class] = newAuth
		if cand[0] < cand[1] || cand[1] < cand[2] || cand[0] > seg.physical+seg.overbook {
			return errInvalid("auth not nested or over cap")
		}
	}
	if now < n.now {
		return &Error{Kind: KindClockRollback}
	}
	seg, ok := n.segs[segID]
	if !ok {
		return errNotFound("segment " + segID)
	}
	seg.auth[class] = newAuth
	n.now = now
	return nil
}

func (n *naive) availability(segID string, class int) (int, error) {
	if class < 0 || class >= NumClasses {
		return 0, errInvalid("bad class")
	}
	if _, ok := n.segs[segID]; !ok {
		return 0, errNotFound("segment " + segID)
	}
	return n.availAt(segID, class, n.now), nil
}

func (n *naive) canHold(legs []Leg, pax int) (bool, string, error) {
	if err := n.checkLegs(legs, pax); err != nil {
		return false, "", err
	}
	for _, l := range legs {
		if _, ok := n.segs[l.Segment]; !ok {
			return false, "", &Error{Kind: KindNotFound, Segment: l.Segment}
		}
	}
	for _, l := range legs {
		if !n.fitsAt(l.Segment, l.Class, pax, n.now) {
			return false, l.Segment, nil
		}
	}
	return true, "", nil
}

// 随机操作序列比对：生产实现与朴素模型执行同一操作流，逐步比较输入、输
// 出与判定依据（拒绝类别 / 失败航段 / 冲突状态），并校验不变式。
func TestRandomAgainstNaiveModel(t *testing.T) {
	for _, seed := range []int64{20241001, 7, 999983} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runRandomSequence(t, seed, 2000)
		})
	}
}

func errDesc(err error) string {
	if err == nil {
		return "ok"
	}
	if e, ok := err.(*Error); ok {
		d := e.Kind.String()
		if e.Segment != "" {
			d += "@" + e.Segment
		}
		if e.Kind == KindStateConflict {
			d += "(" + e.Status.String() + ")"
		}
		return d
	}
	return "unexpected:" + err.Error()
}

func sameErr(a, b error) bool {
	ea, oka := aToErr(a)
	eb, okb := aToErr(b)
	if !oka || !okb {
		return oka == okb
	}
	return ea.Kind == eb.Kind && ea.Segment == eb.Segment && ea.Status == eb.Status
}

func aToErr(err error) (*Error, bool) {
	if err == nil {
		return nil, false
	}
	e, ok := err.(*Error)
	if !ok {
		return nil, false
	}
	return e, true
}

func runRandomSequence(t *testing.T, seed int64, steps int) {
	rng := rand.New(rand.NewSource(seed))
	sys, err := NewSystem(5)
	if err != nil {
		t.Fatalf("NewSystem: %v", err)
	}
	nai := newNaive(5)
	segIDs := []string{"A", "B", "C", "D"}
	auths := [][NumClasses]int{{6, 4, 2}, {4, 4, 3}, {9, 5, 5}, {3, 2, 1}}
	phys := []int{5, 4, 8, 3}
	over := []int{1, 0, 2, 0}
	for i, id := range segIDs {
		if err := sys.AddSegment(id, phys[i], over[i], auths[i], 0); err != nil {
			t.Fatalf("sys AddSegment: %v", err)
		}
		if err := nai.addSegment(id, phys[i], over[i], auths[i], 0); err != nil {
			t.Fatalf("naive AddSegment: %v", err)
		}
	}

	var idPool []string
	var now int64
	randLegs := func() []Leg {
		k := 1 + rng.Intn(4)
		perm := rng.Perm(len(segIDs))
		legs := make([]Leg, 0, k)
		for i := 0; i < k; i++ {
			legs = append(legs, Leg{Segment: segIDs[perm[i]], Class: rng.Intn(NumClasses)})
		}
		return legs
	}
	randTime := func() int64 {
		if now > 0 && rng.Intn(100) < 8 { // 8% 概率时钟回退
			return now - 1 - int64(rng.Intn(2))
		}
		now += int64(rng.Intn(4))
		return now
	}

	for step := 0; step < steps; step++ {
		op := rng.Intn(100)
		var input, outSys, outNai string
		switch {
		case op < 35: // 预占
			legs := randLegs()
			pax := 1 + rng.Intn(9)
			if rng.Intn(100) < 4 { // 4% 概率非法人数
				pax = []int{0, 10}[rng.Intn(2)]
			}
			tm := randTime()
			input = fmt.Sprintf("HOLD %v pax=%d now=%d", legs, pax, tm)
			idS, expS, errS := sys.Hold(legs, pax, tm)
			idN, expN, errN := nai.hold(legs, pax, tm)
			outSys = fmt.Sprintf("id=%s expiry=%d %s", idS, expS, errDesc(errS))
			outNai = fmt.Sprintf("id=%s expiry=%d %s", idN, expN, errDesc(errN))
			if !sameErr(errS, errN) || idS != idN || expS != expN {
				t.Fatalf("step %d %s:\n sys=%s\n nai=%s", step, input, outSys, outNai)
			}
			if errS == nil {
				idPool = append(idPool, idS)
			}
		case op < 50: // 出票
			id := pickID(rng, idPool)
			tm := randTime()
			input = fmt.Sprintf("TICKET %s now=%d", id, tm)
			errS, errN := sys.Ticket(id, tm), nai.ticket(id, tm)
			outSys, outNai = errDesc(errS), errDesc(errN)
			if !sameErr(errS, errN) {
				t.Fatalf("step %d %s:\n sys=%s\n nai=%s", step, input, outSys, outNai)
			}
		case op < 65: // 取消
			id := pickID(rng, idPool)
			tm := randTime()
			input = fmt.Sprintf("CANCEL %s now=%d", id, tm)
			errS, errN := sys.Cancel(id, tm), nai.cancel(id, tm)
			outSys, outNai = errDesc(errS), errDesc(errN)
			if !sameErr(errS, errN) {
				t.Fatalf("step %d %s:\n sys=%s\n nai=%s", step, input, outSys, outNai)
			}
		case op < 80: // 授权量调整
			seg := segIDs[rng.Intn(len(segIDs))]
			if rng.Intn(100) < 5 {
				seg = "NOPE"
			}
			class := rng.Intn(NumClasses+1) - 1 // -1..3，含越界
			val := rng.Intn(12) - 1             // -1..10，含非法
			tm := randTime()
			input = fmt.Sprintf("ADJUST %s class=%d val=%d now=%d", seg, class, val, tm)
			errS, errN := sys.AdjustAuth(seg, class, val, tm), nai.adjustAuth(seg, class, val, tm)
			outSys, outNai = errDesc(errS), errDesc(errN)
			if !sameErr(errS, errN) {
				t.Fatalf("step %d %s:\n sys=%s\n nai=%s", step, input, outSys, outNai)
			}
		case op < 90: // 可用数查询
			seg := segIDs[rng.Intn(len(segIDs))]
			class := rng.Intn(NumClasses+1) - 1
			input = fmt.Sprintf("AVAIL %s class=%d", seg, class)
			aS, errS := sys.Availability(seg, class)
			aN, errN := nai.availability(seg, class)
			outSys = fmt.Sprintf("%d %s", aS, errDesc(errS))
			outNai = fmt.Sprintf("%d %s", aN, errDesc(errN))
			if !sameErr(errS, errN) || (errS == nil && aS != aN) {
				t.Fatalf("step %d %s:\n sys=%s\n nai=%s", step, input, outSys, outNai)
			}
		default: // 行程可售判定
			legs := randLegs()
			pax := 1 + rng.Intn(9)
			input = fmt.Sprintf("CANHOLD %v pax=%d", legs, pax)
			okS, segS, errS := sys.CanHold(legs, pax)
			okN, segN, errN := nai.canHold(legs, pax)
			outSys = fmt.Sprintf("%v@%s %s", okS, segS, errDesc(errS))
			outNai = fmt.Sprintf("%v@%s %s", okN, segN, errDesc(errN))
			if !sameErr(errS, errN) || okS != okN || segS != segN {
				t.Fatalf("step %d %s:\n sys=%s\n nai=%s", step, input, outSys, outNai)
			}
		}
		// 日志打印每步的输入、输出与判定依据（两者一致即判定通过）。
		t.Logf("step %04d | %-40s | sys=%s | naive=%s | match", step, input, outSys, outNai)

		// 不变式：每个航段已确认+未到期预占之和不超过最高等级授权量。
		for i, id := range segIDs {
			a, err := sys.Availability(id, 0)
			if err != nil {
				t.Fatalf("step %d availability: %v", step, err)
			}
			if a < 0 {
				t.Fatalf("step %d: segment %s total occupancy exceeds top authorization", step, id)
			}
			_ = i
		}
	}
}

func pickID(rng *rand.Rand, pool []string) string {
	switch r := rng.Intn(100); {
	case r < 70 && len(pool) > 0:
		return pool[rng.Intn(len(pool))]
	case r < 85:
		return "E999999" // 从未存在
	case r < 95:
		return fmt.Sprintf("E%06d", rng.Intn(len(pool)+3)) // 可能存在
	default:
		return "" // 参数非法
	}
}
