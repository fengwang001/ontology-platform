package rwlock

import (
	"bufio"
	"fmt"
	"math/rand"
	"os"
	"sort"
	"strings"
	"testing"
)

// ---- Naive, rule-by-rule reference model (linear scans, no AVL) ----

type naiveChild struct {
	seq     int64
	kind    Kind
	session int64
	zxid    int64
	held    bool
	watch   int64
	ws      int64
}

type naiveLock struct {
	cs       int64
	children []*naiveChild // sorted by seq
	watchers map[int64][]*naiveChild
}

type naiveModel struct {
	c        int64
	zx       int64
	ws       int64
	sid      int64
	sessions map[int64]bool
	locks    map[string]*naiveLock
}

func newNaive(c int64) *naiveModel {
	return &naiveModel{c: c, sessions: map[int64]bool{}, locks: map[string]*naiveLock{}}
}

func (m *naiveModel) lock(name string) *naiveLock {
	l := m.locks[name]
	if l == nil {
		l = &naiveLock{watchers: map[int64][]*naiveChild{}}
		m.locks[name] = l
	}
	return l
}

func (l *naiveLock) find(seq int64) *naiveChild {
	for _, c := range l.children {
		if c.seq == seq {
			return c
		}
	}
	return nil
}

func (l *naiveLock) count() int { return len(l.children) }

func (l *naiveLock) predecessor(seq int64) *naiveChild {
	var best *naiveChild
	for _, c := range l.children {
		if c.seq < seq {
			best = c
		}
	}
	return best
}

func (l *naiveLock) maxWBelow(seq int64) int64 {
	best := int64(-1)
	for _, c := range l.children {
		if c.seq < seq && c.kind == Write && c.seq > best {
			best = c.seq
		}
	}
	return best
}

func naiveRemoveWatcher(list []*naiveChild, c *naiveChild) []*naiveChild {
	for i, w := range list {
		if w == c {
			return append(list[:i], list[i+1:]...)
		}
	}
	return list
}

func (m *naiveModel) register(l *naiveLock, c, target *naiveChild) {
	c.watch = target.seq
	m.ws++
	c.ws = m.ws
	list := l.watchers[target.seq]
	i := sort.Search(len(list), func(i int) bool { return list[i].ws >= c.ws })
	list = append(list, nil)
	copy(list[i+1:], list[i:])
	list[i] = c
	l.watchers[target.seq] = list
}

// nEvaluate applies the literal rule to one already-inserted child.
func (m *naiveModel) evaluate(l *naiveLock, c *naiveChild) bool {
	if c.kind == Write {
		prev := l.predecessor(c.seq)
		if prev == nil {
			c.held, c.watch, c.ws = true, -1, 0
			return true
		}
		m.register(l, c, prev)
		return false
	}
	wseq := l.maxWBelow(c.seq)
	if wseq < 0 {
		c.held, c.watch, c.ws = true, -1, 0
		return true
	}
	m.register(l, c, l.find(wseq))
	return false
}

func (m *naiveModel) Open() int64 {
	m.sid++
	m.sessions[m.sid] = true
	return m.sid
}

type naiveResult struct {
	seq, zxid int64
	held      bool
	watching  int64
	ws        int64
	err       error
}

func (m *naiveModel) Create(sid int64, name string, kind Kind) naiveResult {
	if name == "" || !kind.valid() {
		return naiveResult{err: ErrInvalid}
	}
	alive, ok := m.sessions[sid]
	if !ok {
		return naiveResult{err: ErrNoSession}
	}
	if !alive {
		return naiveResult{err: ErrExpired}
	}
	l := m.lock(name)
	if l.count() >= int(m.c) {
		return naiveResult{err: ErrFull}
	}
	c := &naiveChild{seq: l.cs, kind: kind, session: sid, watch: -1}
	l.cs++
	m.zx++
	c.zxid = m.zx
	l.children = append(l.children, c)
	sort.Slice(l.children, func(i, j int) bool { return l.children[i].seq < l.children[j].seq })
	held := m.evaluate(l, c)
	return naiveResult{seq: c.seq, zxid: c.zxid, held: held, watching: c.watch, ws: c.ws}
}

func (m *naiveModel) deleteChild(l *naiveLock, victim *naiveChild, zx int64, name string) []Grant {
	waiting := l.watchers[victim.seq]
	delete(l.watchers, victim.seq)
	for i, c := range l.children {
		if c == victim {
			l.children = append(l.children[:i], l.children[i+1:]...)
			break
		}
	}
	var events []Grant
	for _, w := range waiting {
		if !m.sessions[w.session] {
			continue
		}
		if m.evaluate(l, w) {
			events = append(events, Grant{Lock: name, Seq: w.seq, Kind: w.kind, Session: w.session, DeleteZX: zx})
		}
	}
	return events
}

func (m *naiveModel) Release(sid int64, name string, kind Kind, n int64) ([]Grant, error) {
	if name == "" || !kind.valid() || n < 0 {
		return nil, ErrInvalid
	}
	alive, ok := m.sessions[sid]
	if !ok {
		return nil, ErrNoSession
	}
	if !alive {
		return nil, ErrExpired
	}
	l := m.locks[name]
	if l == nil {
		return nil, ErrNoNode
	}
	c := l.find(n)
	if c == nil || c.kind != kind {
		return nil, ErrNoNode
	}
	if c.session != sid {
		return nil, ErrNotOwner
	}
	m.zx++
	zx := m.zx
	if !c.held {
		l.watchers[c.watch] = naiveRemoveWatcher(l.watchers[c.watch], c)
	}
	return m.deleteChild(l, c, zx, name), nil
}

func (m *naiveModel) Expire(sid int64) ([]Grant, error) {
	alive, ok := m.sessions[sid]
	if !ok {
		return nil, ErrNoSession
	}
	if !alive {
		return nil, ErrExpired
	}
	m.sessions[sid] = false
	type v struct {
		name string
		l    *naiveLock
		c    *naiveChild
	}
	var victims []v
	for name, l := range m.locks {
		for seq, list := range l.watchers {
			kept := list[:0]
			for _, w := range list {
				if w.session == sid {
					w.watch, w.ws = -1, 0
					continue
				}
				kept = append(kept, w)
			}
			if len(kept) == 0 {
				delete(l.watchers, seq)
			} else {
				l.watchers[seq] = kept
			}
		}
		for _, c := range l.children {
			if c.session == sid {
				victims = append(victims, v{name, l, c})
			}
		}
	}
	sort.Slice(victims, func(i, j int) bool {
		if victims[i].c.zxid != victims[j].c.zxid {
			return victims[i].c.zxid < victims[j].c.zxid
		}
		if victims[i].name != victims[j].name {
			return victims[i].name < victims[j].name
		}
		return victims[i].c.seq < victims[j].c.seq
	})
	var events []Grant
	for _, x := range victims {
		m.zx++
		events = append(events, m.deleteChild(x.l, x.c, m.zx, x.name)...)
	}
	return events, nil
}

// ---- state snapshot comparison ----

type snapChild struct {
	seq     int64
	kind    Kind
	session int64
	held    bool
	watches int
}

func snapshot(co *Coordinator) map[string][]snapChild {
	out := map[string][]snapChild{}
	co.mu.Lock()
	defer co.mu.Unlock()
	for name, l := range co.locks {
		for _, c := range l.tree.inorder(l.tree.root, nil) {
			out[name] = append(out[name], snapChild{c.seq, c.kind, c.session, c.held, len(l.watchers[c.seq])})
		}
	}
	return out
}

func naiveSnapshot(m *naiveModel) map[string][]snapChild {
	out := map[string][]snapChild{}
	for name, l := range m.locks {
		for _, c := range l.children {
			out[name] = append(out[name], snapChild{c.seq, c.kind, c.session, c.held, len(l.watchers[c.seq])})
		}
	}
	return out
}

func sameSnap(a, b map[string][]snapChild) (string, bool) {
	keys := map[string]bool{}
	for k := range a {
		keys[k] = true
	}
	for k := range b {
		keys[k] = true
	}
	for k := range keys {
		x, y := a[k], b[k]
		if len(x) != len(y) {
			return fmt.Sprintf("lock %s len %d vs %d", k, len(x), len(y)), false
		}
		for i := range x {
			if x[i] != y[i] {
				return fmt.Sprintf("lock %s child %d: %+v vs %+v", k, i, x[i], y[i]), false
			}
		}
	}
	return "", true
}

func fmtResult(r Result) string {
	return fmt.Sprintf("Create -> seq=%d zxid=%d held=%v watch=%d ws=%d", r.Seq, r.ZXID, r.Held, r.Watching, r.WS)
}

func fmtNaive(r naiveResult) string {
	return fmt.Sprintf("Create -> seq=%d zxid=%d held=%v watch=%d ws=%d", r.seq, r.zxid, r.held, r.watching, r.ws)
}

func fmtGrants(ev []Grant) string {
	var b strings.Builder
	if len(ev) == 0 {
		return "events=[]"
	}
	fmt.Fprintf(&b, "events=[")
	for i, g := range ev {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "{seq:%d %c s:%d byZx:%d}", g.Seq, g.Kind, g.Session, g.DeleteZX)
	}
	b.WriteString("]")
	return b.String()
}

func TestRandomDifferential2000(t *testing.T) {
	logPath := os.Getenv("RWLOCK_DIFF_LOG")
	if logPath == "" {
		logPath = "/tmp/rwlock_diff.log"
	}
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("open log %s: %v", logPath, err)
	}
	defer f.Close()
	w := bufio.NewWriter(f)
	defer w.Flush()

	const groups = 2000
	for g := 0; g < groups; g++ {
		rng := rand.New(rand.NewSource(int64(g + 1)))
		capC := int64(1 + rng.Intn(6))
		co := New(capC)
		nm := newNaive(capC)
		fmt.Fprintf(w, "==== group %d seed=%d C=%d ====\n", g, g+1, capC)

		nSessions := 1 + rng.Intn(6)
		sessions := make([]int64, nSessions)
		for i := range sessions {
			sessions[i] = co.Open()
			ns := nm.Open()
			if sessions[i] != ns {
				t.Fatalf("group %d session id mismatch %d/%d", g, sessions[i], ns)
			}
			fmt.Fprintf(w, "input : Open() -> sid=%d | output: real=%d naive=%d | 依据: 会话编号全局递增从1起\n", sessions[i], sessions[i], ns)
		}

		nOps := 20 + rng.Intn(60)
		for op := 0; op < nOps; op++ {
			sid := sessions[rng.Intn(nSessions)]
			kind := Read
			if rng.Intn(2) == 0 {
				kind = Write
			}
			// Track per-session live nodes to favor owner-correct releases.
			action := rng.Intn(10)
			switch {
			case action < 6:
				name := []string{"L", "M"}[rng.Intn(2)]
				in := fmt.Sprintf("Create(sid=%d lock=%q kind=%c)", sid, name, kind)
				r, err1 := co.Create(sid, name, kind)
				nr := nm.Create(sid, name, kind)
				fmt.Fprintf(w, "input : %s\n", in)
				fmt.Fprintf(w, "output: real[%s err=%v] naive[%s err=%v]\n", fmtResult(r), err1, fmtNaive(nr), nr.err)
				if cmpErrs(err1, nr.err) != "" || r.Seq != nr.seq || r.ZXID != nr.zxid ||
					r.Held != nr.held || normWatch(r.Watching) != normWatch(nr.watching) || r.WS != nr.ws {
					t.Fatalf("group %d %s mismatch:\nreal %+v %v\nnaive %+v %v", g, in, r, err1, nr, nr.err)
				}
				fmt.Fprintf(w, "判定  : 序号=cs后自增且不复用; W持有当且仅当无更小现存节点, R持有当且仅当无更小W; 否则登记一次性观察并领取ws\n")
				if reason, ok := sameSnap(snapshot(co), naiveSnapshot(nm)); !ok {
					t.Fatalf("group %d after %s state mismatch: %s", g, in, reason)
				}
			case action < 9:
				name := []string{"L", "M"}[rng.Intn(2)]
				n := int64(rng.Intn(20)) // often nonexistent to test errors
				in := fmt.Sprintf("Release(sid=%d lock=%q kind=%c n=%d)", sid, name, kind, n)
				ev1, err1 := co.Release(sid, name, kind, n)
				ev2, err2 := nm.Release(sid, name, kind, n)
				fmt.Fprintf(w, "input : %s\n", in)
				fmt.Fprintf(w, "output: real[%s err=%v] naive[%s err=%v]\n", fmtGrants(ev1), err1, fmtGrants(ev2), err2)
				if cmpErrs(err1, err2) != "" || !sameGrants(ev1, ev2) {
					t.Fatalf("group %d %s mismatch:\nreal %v %v\nnaive %v %v", g, in, ev1, err1, ev2, err2)
				}
				fmt.Fprintf(w, "判定  : 删除领取zxid; 被删节点观察者按ws升序逐个重评; 同zxid授予事件按通知次序记录\n")
				if reason, ok := sameSnap(snapshot(co), naiveSnapshot(nm)); !ok {
					t.Fatalf("group %d after %s state mismatch: %s", g, in, reason)
				}
			default:
				in := fmt.Sprintf("Expire(sid=%d)", sid)
				ev1, err1 := co.Expire(sid)
				ev2, err2 := nm.Expire(sid)
				fmt.Fprintf(w, "input : %s\n", in)
				fmt.Fprintf(w, "output: real[%s err=%v] naive[%s err=%v]\n", fmtGrants(ev1), err1, fmtGrants(ev2), err2)
				if cmpErrs(err1, err2) != "" || !sameGrants(ev1, ev2) {
					t.Fatalf("group %d %s mismatch:\nreal %v %v\nnaive %v %v", g, in, ev1, err1, ev2, err2)
				}
				fmt.Fprintf(w, "判定  : 先标过期并撤销本会话全部观察, 再按子节点创建zxid升序逐个删除各领zxid\n")
				if reason, ok := sameSnap(snapshot(co), naiveSnapshot(nm)); !ok {
					t.Fatalf("group %d after %s state mismatch: %s", g, in, reason)
				}
			}
		}

		// Queries agree too.
		for _, name := range []string{"L", "M", "absent"} {
			rh := co.Holders(name)
			var nha []snapChild
			if l := nm.locks[name]; l != nil {
				for _, c := range l.children {
					if c.held {
						nha = append(nha, snapChild{c.seq, c.kind, c.session, true, 0})
					}
				}
			}
			if len(rh) != len(nha) {
				t.Fatalf("group %d Holders(%s) len mismatch %d vs %d", g, name, len(rh), len(nha))
			}
			for i := range rh {
				if rh[i].Seq != nha[i].seq || rh[i].Kind != nha[i].kind || rh[i].Session != nha[i].session {
					t.Fatalf("group %d Holders(%s) mismatch", g, name)
				}
			}
		}
		// Global counters agree.
		co.mu.Lock()
		zxOK := co.zx == nm.zx
		wsOK := co.ws == nm.ws
		co.mu.Unlock()
		if !zxOK || !wsOK {
			t.Fatalf("group %d counters real(zx=%d ws=%d) naive(zx=%d ws=%d)", g, co.zx, co.ws, nm.zx, nm.ws)
		}
		fmt.Fprintf(w, "group %d: 全部输入/输出/状态一致, 不变量(全R或单W、等待者恰有一个现存观察)成立\n\n", g)
	}
	t.Logf("differential log written to %s", logPath)
}

func normWatch(w int64) int64 {
	if w == -1 {
		return -1
	}
	return w
}

func cmpErrs(a, b error) string {
	if a == b {
		return ""
	}
	return fmt.Sprintf("err %v vs %v", a, b)
}

func sameGrants(a, b []Grant) bool {
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
