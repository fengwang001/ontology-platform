package snooping

import (
	"errors"
	"fmt"
	"math/rand"
	"slices"
	"sort"
	"testing"
)

// naive 是逐秒推进的朴素模拟：每秒全表扫描落地到期、逐秒检查查询
// 计划，与主实现的惰性求值相互独立，用于差分对照。
type naive struct {
	p            int
	ownIP        uint32
	qi, lmqi     int64
	gmi, oqpi    int64
	rb           int64
	fastLeave    []bool
	floodUnknown bool
	gmax, lp     int

	cur     int64 // 已推进到的时刻
	maxNow  int64
	querier bool
	anchor  int64
	oq      int64

	members map[naiveKey]int64
	routers map[int]int64
	specs   []*naiveSpec
	sent    []QueryEvent
	drained int
}

type naiveKey struct {
	group uint32
	port  int
}

type naiveSpec struct {
	time      int64
	group     uint32
	port      int
	cancelled bool
	emitted   bool
}

func newNaive(p int, ownIP uint32, qi, qri int64, rb int, lmqi int64, fl []bool, flood bool, gmax, lp int) *naive {
	return &naive{
		p: p, ownIP: ownIP, qi: qi, lmqi: lmqi,
		gmi: int64(rb)*qi + qri, oqpi: int64(rb)*qi + qri/2,
		rb: int64(rb), fastLeave: fl, floodUnknown: flood,
		gmax: gmax, lp: lp,
		cur: -1, querier: true, anchor: 0,
		members: make(map[naiveKey]int64),
		routers: make(map[int]int64),
	}
}

func isMulticastGroup(g uint32) bool { return g&0xF0000000 == 0xE0000000 }

func isLinkLocalGroup(g uint32) bool { return g >= 0xE0000000 && g <= 0xE00000FF }

func (n *naive) emitDue() {
	for _, s := range n.specs {
		if !s.cancelled && !s.emitted && s.time <= n.cur {
			s.emitted = true
			n.sent = append(n.sent, QueryEvent{Time: s.time, Kind: Specific, Group: s.group, Port: s.port})
		}
	}
}

// advanceTo 逐秒推进到 now：先落地到期，再恢复查询器，再发通用查询，
// 最后发特定组查询（同刻通用先于特定）。
func (n *naive) advanceTo(now int64) {
	for t := n.cur + 1; t <= now; t++ {
		n.cur = t
		for k, exp := range n.members {
			if exp <= t {
				delete(n.members, k)
			}
		}
		for p, exp := range n.routers {
			if exp <= t {
				delete(n.routers, p)
			}
		}
		if !n.querier && t >= n.oq {
			n.querier = true
			n.anchor = t
		}
		if n.querier && (t-n.anchor)%n.qi == 0 {
			n.sent = append(n.sent, QueryEvent{Time: t, Kind: General})
		}
		n.emitDue()
	}
}

func (n *naive) checkClock(now int64) error {
	if now < 0 || now > 1_000_000_000_000 {
		return ErrParam
	}
	if now < n.maxNow {
		return ErrClock
	}
	return nil
}

func (n *naive) portLiveCount(port int, now int64) int {
	c := 0
	for k, exp := range n.members {
		if k.port == port && exp > now {
			c++
		}
	}
	return c
}

func (n *naive) groupLive(group uint32, now int64) bool {
	for k, exp := range n.members {
		if k.group == group && exp > now {
			return true
		}
	}
	return false
}

func (n *naive) liveGroupCount(now int64) int {
	seen := map[uint32]bool{}
	for k, exp := range n.members {
		if exp > now {
			seen[k.group] = true
		}
	}
	return len(seen)
}

func (n *naive) cancelSpec(key naiveKey, now int64) {
	for _, s := range n.specs {
		if s.group == key.group && s.port == key.port && s.time > now {
			s.cancelled = true
		}
	}
}

func (n *naive) routerPorts(now int64) []int {
	var out []int
	for p, exp := range n.routers {
		if exp > now {
			out = append(out, p)
		}
	}
	sort.Ints(out)
	return out
}

func (n *naive) Report(port int, group uint32, now int64) ([]int, error) {
	if port < 1 || port > n.p || !isMulticastGroup(group) {
		return nil, ErrParam
	}
	if err := n.checkClock(now); err != nil {
		return nil, err
	}
	if isLinkLocalGroup(group) {
		return nil, ErrLinkLocal
	}
	key := naiveKey{group, port}
	exp, ok := n.members[key]
	switch {
	case ok && exp > now:
		n.members[key] = now + n.gmi
		n.cancelSpec(key, now)
	case n.portLiveCount(port, now) >= n.lp:
		return nil, ErrPortLimit
	case !n.groupLive(group, now) && n.liveGroupCount(now) >= n.gmax:
		return nil, ErrGroupLimit
	default:
		n.members[key] = now + n.gmi
		n.cancelSpec(key, now)
	}
	n.advanceTo(now)
	n.maxNow = now
	return excludePort(n.routerPorts(now), port), nil
}

func (n *naive) Leave(port int, group uint32, now int64) error {
	if port < 1 || port > n.p || !isMulticastGroup(group) {
		return ErrParam
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	if isLinkLocalGroup(group) {
		return ErrLinkLocal
	}
	key := naiveKey{group, port}
	exp, ok := n.members[key]
	if !ok || exp <= now {
		return ErrNotMember
	}
	n.advanceTo(now)
	switch {
	case n.fastLeave[port-1]:
		delete(n.members, key)
		n.cancelSpec(key, now)
	case n.querier:
		pending := false
		for _, s := range n.specs {
			if !s.cancelled && s.group == group && s.port == port && s.time >= now {
				pending = true
				break
			}
		}
		if !pending {
			newExp := now + n.rb*n.lmqi
			if exp < newExp {
				newExp = exp
			}
			n.members[key] = newExp
			for i := int64(0); i < n.rb; i++ {
				t := now + i*n.lmqi
				if t >= newExp {
					break
				}
				n.specs = append(n.specs, &naiveSpec{time: t, group: group, port: port})
			}
			n.emitDue()
		}
	}
	n.maxNow = now
	return nil
}

func (n *naive) Query(port int, srcIP uint32, now int64) error {
	if port < 1 || port > n.p || srcIP == 0 || srcIP == n.ownIP {
		return ErrParam
	}
	if err := n.checkClock(now); err != nil {
		return err
	}
	n.advanceTo(now)
	n.routers[port] = now + n.oqpi
	if srcIP < n.ownIP {
		n.querier = false
		n.oq = now + n.oqpi
		for _, s := range n.specs {
			if s.time > now {
				s.cancelled = true
			}
		}
	}
	n.maxNow = now
	return nil
}

func (n *naive) Drain(now int64) ([]QueryEvent, error) {
	if err := n.checkClock(now); err != nil {
		return nil, err
	}
	n.advanceTo(now)
	out := make([]QueryEvent, 0, len(n.sent)-n.drained)
	out = append(out, n.sent[n.drained:]...)
	n.drained = len(n.sent)
	sortEvents(out)
	n.maxNow = now
	return out, nil
}

func (n *naive) Forward(group uint32, inPort int, now int64) ([]int, error) {
	if inPort < 1 || inPort > n.p || !isMulticastGroup(group) {
		return nil, ErrParam
	}
	if err := n.checkClock(now); err != nil {
		return nil, err
	}
	n.advanceTo(now)
	n.maxNow = now
	if isLinkLocalGroup(group) {
		return n.flood(inPort), nil
	}
	var members []int
	for k, exp := range n.members {
		if k.group == group && exp > now {
			members = append(members, k.port)
		}
	}
	sort.Ints(members)
	routers := n.routerPorts(now)
	switch {
	case len(members) > 0:
		return unionExcludeSorted(members, routers, inPort), nil
	case n.floodUnknown:
		return n.flood(inPort), nil
	default:
		return excludePort(routers, inPort), nil
	}
}

func (n *naive) flood(inPort int) []int {
	out := make([]int, 0, n.p-1)
	for p := 1; p <= n.p; p++ {
		if p != inPort {
			out = append(out, p)
		}
	}
	return out
}

func unionExcludeSorted(a, b []int, port int) []int {
	out := make([]int, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) || j < len(b) {
		var v int
		switch {
		case j >= len(b) || (i < len(a) && a[i] < b[j]):
			v = a[i]
			i++
		case i >= len(a) || b[j] < a[i]:
			v = b[j]
			j++
		default:
			v = a[i]
			i++
			j++
		}
		if v != port && (len(out) == 0 || out[len(out)-1] != v) {
			out = append(out, v)
		}
	}
	return out
}

func sortEvents(ev []QueryEvent) {
	slices.SortFunc(ev, func(a, b QueryEvent) int {
		if a.Time != b.Time {
			return cmpInt64(a.Time, b.Time)
		}
		if a.Kind != b.Kind {
			return cmpInt(int(a.Kind), int(b.Kind))
		}
		if a.Group != b.Group {
			return cmpUint32(a.Group, b.Group)
		}
		return cmpInt(a.Port, b.Port)
	})
}

func cmpInt(a, b int) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func cmpInt64(a, b int64) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func cmpUint32(a, b uint32) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}

func sameErr(a, b error) bool {
	if (a == nil) != (b == nil) {
		return false
	}
	if a == nil {
		return true
	}
	for _, s := range []error{ErrParam, ErrClock, ErrLinkLocal, ErrNotMember, ErrPortLimit, ErrGroupLimit} {
		if errors.Is(a, s) || errors.Is(b, s) {
			return errors.Is(a, s) && errors.Is(b, s)
		}
	}
	return false
}

func errKind(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrParam):
		return "参数非法"
	case errors.Is(err, ErrClock):
		return "时钟回退"
	case errors.Is(err, ErrLinkLocal):
		return "本地链路组"
	case errors.Is(err, ErrNotMember):
		return "非成员"
	case errors.Is(err, ErrPortLimit):
		return "端口超限"
	case errors.Is(err, ErrGroupLimit):
		return "组超限"
	}
	return "未知错误"
}

// TestDifferentialAgainstNaive 生成 1500 组随机操作序列，把主实现与逐秒
// 推进的朴素模拟逐步对照，日志打印输入、输出与判定依据。
func TestDifferentialAgainstNaive(t *testing.T) {
	const sequences = 1500
	for seq := 0; seq < sequences; seq++ {
		rng := rand.New(rand.NewSource(int64(seq)*7919 + 1))
		p := 1 + rng.Intn(6)
		qi := int64(1 + rng.Intn(10))
		qri := int64(rng.Intn(int(qi)))
		rb := 1 + rng.Intn(7)
		lmqi := int64(1 + rng.Intn(10))
		ownIP := uint32(1000)
		fl := make([]bool, p)
		for i := range fl {
			fl[i] = rng.Intn(5) == 0
		}
		flood := rng.Intn(2) == 0
		gmax := 1 + rng.Intn(8)
		lp := 1 + rng.Intn(5)
		main, err := New(p, ownIP, qi, qri, rb, lmqi, fl, flood, gmax, lp)
		if err != nil {
			t.Fatalf("seq=%d New: %v", seq, err)
		}
		nav := newNaive(p, ownIP, qi, qri, rb, lmqi, fl, flood, gmax, lp)
		groups := []uint32{g1, g2, g3, g1 + 3, g1 + 4, ll, 0xF0000001}
		now := int64(0)
		for i := 0; i < 25; i++ {
			switch r := rng.Intn(100); {
			case r < 90:
				now += int64(rng.Intn(int(3*qi + 2)))
			case r < 95:
				// 同一时刻连续操作
			default:
				now -= int64(1 + rng.Intn(3)) // 时钟回退或非法时刻
			}
			port := 1 + rng.Intn(p)
			if rng.Intn(20) == 0 {
				port = rng.Intn(p + 2) // 可能越界
			}
			group := groups[rng.Intn(len(groups))]
			var (
				gotPorts, wantPorts []int
				gotEv, wantEv       []QueryEvent
				gotErr, wantErr     error
				desc                string
			)
			switch kind := rng.Intn(100); {
			case kind < 30:
				desc = fmt.Sprintf("Report(port=%d, group=%#x, now=%d)", port, group, now)
				gotPorts, gotErr = main.Report(port, group, now)
				wantPorts, wantErr = nav.Report(port, group, now)
			case kind < 50:
				desc = fmt.Sprintf("Leave(port=%d, group=%#x, now=%d)", port, group, now)
				gotErr = main.Leave(port, group, now)
				wantErr = nav.Leave(port, group, now)
			case kind < 65:
				srcIP := ownIP + uint32(rng.Intn(5)) - 2
				if rng.Intn(10) == 0 {
					srcIP = 0
				}
				desc = fmt.Sprintf("Query(port=%d, srcIP=%d, now=%d)", port, srcIP, now)
				gotErr = main.Query(port, srcIP, now)
				wantErr = nav.Query(port, srcIP, now)
			case kind < 80:
				desc = fmt.Sprintf("Drain(now=%d)", now)
				gotEv, gotErr = main.Drain(now)
				wantEv, wantErr = nav.Drain(now)
			default:
				desc = fmt.Sprintf("Forward(group=%#x, inPort=%d, now=%d)", group, port, now)
				gotPorts, gotErr = main.Forward(group, port, now)
				wantPorts, wantErr = nav.Forward(group, port, now)
			}
			if !sameErr(gotErr, wantErr) || !slices.Equal(gotPorts, wantPorts) || !slices.Equal(gotEv, wantEv) {
				t.Fatalf("seq=%d op#%d %s\n主实现: ports=%v events=%v err=%v\n朴素模拟: ports=%v events=%v err=%v",
					seq, i, desc, gotPorts, gotEv, gotErr, wantPorts, wantEv, wantErr)
			}
			t.Logf("seq=%d op#%d 输入=%s 输出=ports=%v events=%v 判定=%s（依据：主实现与逐秒朴素模拟结果一致）",
				seq, i, desc, gotPorts, gotEv, errKind(gotErr))
		}
	}
}
