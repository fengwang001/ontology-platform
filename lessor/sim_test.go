package lessor

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"
)

// modelLease is the naive simulator's lease record.
type modelLease struct {
	g, sv, x int64
	keys     []string
}

// naiveModel re-implements the specification with plain maps and linear scans,
// sharing no heap structure with the real lessor.
type naiveModel struct {
	cfg    Config
	leader bool
	clock  int64
	leases map[int64]*modelLease
	owner  map[string]int64
}

func newNaiveModel(cfg Config) *naiveModel {
	return &naiveModel{
		cfg:    cfg,
		leases: make(map[int64]*modelLease),
		owner:  make(map[string]int64),
	}
}

func (m *naiveModel) validTime(now int64) bool {
	return now >= 0 && now <= 1_000_000_000_000_000 && now >= m.clock
}

type modelResult struct {
	err  error
	num  int64
	revs []Revocation
	keys []string
}

type opKind int

const (
	opGrant opKind = iota
	opRenew
	opAttach
	opTick
	opRevoke
	opCheckpoint
	opDemote
	opPromote
	opTTL
)

func (k opKind) name() string {
	switch k {
	case opGrant:
		return "Grant"
	case opRenew:
		return "Renew"
	case opAttach:
		return "Attach"
	case opTick:
		return "Tick"
	case opRevoke:
		return "Revoke"
	case opCheckpoint:
		return "Checkpoint"
	case opDemote:
		return "Demote"
	case opPromote:
		return "Promote"
	case opTTL:
		return "TTL"
	}
	return "?"
}

type simOp struct {
	kind opKind
	id   int64
	ttl  int64
	now  int64
	key  string
}

func (o simOp) String() string {
	switch o.kind {
	case opAttach:
		return fmt.Sprintf("%s(%q,%d,%d)", o.kind.name(), o.key, o.id, o.now)
	case opGrant:
		return fmt.Sprintf("%s(%d,%d,%d)", o.kind.name(), o.id, o.ttl, o.now)
	case opTick, opCheckpoint, opDemote, opPromote:
		return fmt.Sprintf("%s(%d)", o.kind.name(), o.now)
	default:
		return fmt.Sprintf("%s(%d,%d)", o.kind.name(), o.id, o.now)
	}
}

func removeKey(keys []string, key string) []string {
	pos := sort.SearchStrings(keys, key)
	if pos < len(keys) && keys[pos] == key {
		return append(keys[:pos], keys[pos+1:]...)
	}
	return keys
}

func insertKey(keys []string, key string) []string {
	pos := sort.SearchStrings(keys, key)
	keys = append(keys, "")
	copy(keys[pos+1:], keys[pos:])
	keys[pos] = key
	return keys
}

func (m *naiveModel) apply(o simOp) modelResult {
	switch o.kind {
	case opGrant:
		if o.id < 1 {
			return modelResult{err: ErrInvalidID}
		}
		if o.ttl < 1 || o.ttl > m.cfg.MaxTTL {
			return modelResult{err: ErrInvalidTTL}
		}
		if !m.validTime(o.now) {
			return modelResult{err: ErrInvalidTime}
		}
		if !m.leader {
			return modelResult{err: ErrNotLeader}
		}
		if _, ok := m.leases[o.id]; ok {
			return modelResult{err: ErrLeaseExists}
		}
		g := o.ttl
		if g < m.cfg.MinTTL {
			g = m.cfg.MinTTL
		}
		m.leases[o.id] = &modelLease{g: g, x: o.now + g}
		m.clock = o.now
		return modelResult{}

	case opRenew:
		if o.id < 1 {
			return modelResult{err: ErrInvalidID}
		}
		if !m.validTime(o.now) {
			return modelResult{err: ErrInvalidTime}
		}
		if !m.leader {
			return modelResult{err: ErrNotLeader}
		}
		ls, ok := m.leases[o.id]
		if !ok {
			return modelResult{err: ErrNoLease}
		}
		if ls.x <= o.now {
			return modelResult{err: ErrExpired}
		}
		ls.x = o.now + ls.g
		ls.sv = 0
		m.clock = o.now
		return modelResult{num: ls.g}

	case opAttach:
		if o.id < 1 {
			return modelResult{err: ErrInvalidID}
		}
		if o.key == "" {
			return modelResult{err: ErrEmptyKey}
		}
		if !m.validTime(o.now) {
			return modelResult{err: ErrInvalidTime}
		}
		if !m.leader {
			return modelResult{err: ErrNotLeader}
		}
		target, ok := m.leases[o.id]
		if !ok {
			return modelResult{err: ErrNoLease}
		}
		if target.x <= o.now {
			return modelResult{err: ErrExpired}
		}
		if owner, has := m.owner[o.key]; has {
			if owner == o.id {
				m.clock = o.now
				return modelResult{}
			}
			if len(target.keys) >= m.cfg.Kmax {
				return modelResult{err: ErrLeaseFull}
			}
			target2 := m.leases[owner]
			target2.keys = removeKey(target2.keys, o.key)
		} else if len(target.keys) >= m.cfg.Kmax {
			return modelResult{err: ErrLeaseFull}
		}
		target.keys = insertKey(target.keys, o.key)
		m.owner[o.key] = o.id
		m.clock = o.now
		return modelResult{}

	case opTick:
		if !m.validTime(o.now) {
			return modelResult{err: ErrInvalidTime}
		}
		if !m.leader {
			return modelResult{err: ErrNotLeader}
		}
		type cand struct {
			x, id int64
		}
		var cands []cand
		for id, ls := range m.leases {
			if ls.x <= o.now {
				cands = append(cands, cand{ls.x, id})
			}
		}
		sort.Slice(cands, func(i, j int) bool {
			return cands[i].x < cands[j].x || (cands[i].x == cands[j].x && cands[i].id < cands[j].id)
		})
		if int64(len(cands)) > m.cfg.R {
			cands = cands[:m.cfg.R]
		}
		out := make([]Revocation, 0, len(cands))
		for _, c := range cands {
			ls := m.leases[c.id]
			keys := append([]string(nil), ls.keys...)
			for _, k := range keys {
				delete(m.owner, k)
			}
			delete(m.leases, c.id)
			out = append(out, Revocation{ID: c.id, Keys: keys})
		}
		m.clock = o.now
		return modelResult{revs: out}

	case opRevoke:
		if o.id < 1 {
			return modelResult{err: ErrInvalidID}
		}
		if !m.validTime(o.now) {
			return modelResult{err: ErrInvalidTime}
		}
		if !m.leader {
			return modelResult{err: ErrNotLeader}
		}
		ls, ok := m.leases[o.id]
		if !ok {
			return modelResult{err: ErrNoLease}
		}
		keys := append([]string(nil), ls.keys...)
		for _, k := range keys {
			delete(m.owner, k)
		}
		delete(m.leases, o.id)
		m.clock = o.now
		return modelResult{keys: keys}

	case opCheckpoint:
		if !m.validTime(o.now) {
			return modelResult{err: ErrInvalidTime}
		}
		if !m.leader {
			return modelResult{err: ErrNotLeader}
		}
		for _, ls := range m.leases {
			if ls.x > o.now {
				ls.sv = ls.x - o.now
			}
		}
		m.clock = o.now
		return modelResult{}

	case opDemote:
		if !m.validTime(o.now) {
			return modelResult{err: ErrInvalidTime}
		}
		if !m.leader {
			return modelResult{err: ErrWrongRole}
		}
		m.leader = false
		m.clock = o.now
		return modelResult{}

	case opPromote:
		if !m.validTime(o.now) {
			return modelResult{err: ErrInvalidTime}
		}
		if m.leader {
			return modelResult{err: ErrWrongRole}
		}
		m.leader = true
		for _, ls := range m.leases {
			rem := ls.g
			if ls.sv > 0 {
				rem = ls.sv
			}
			ls.x = o.now + m.cfg.E + rem
		}
		m.clock = o.now
		return modelResult{}

	case opTTL:
		if o.id < 1 {
			return modelResult{err: ErrInvalidID}
		}
		if !m.validTime(o.now) {
			return modelResult{err: ErrInvalidTime}
		}
		ls, ok := m.leases[o.id]
		if !ok {
			return modelResult{err: ErrNoLease}
		}
		if m.leader {
			if ls.x > o.now {
				return modelResult{num: ls.x - o.now}
			}
			return modelResult{num: 0}
		}
		if ls.sv > 0 {
			return modelResult{num: ls.sv}
		}
		return modelResult{num: ls.g}
	}
	return modelResult{}
}

func formatModelResult(r modelResult) string {
	switch {
	case r.err != nil:
		return "ERR " + r.err.Error()
	case r.revs != nil:
		parts := make([]string, 0, len(r.revs))
		for _, rev := range r.revs {
			parts = append(parts, fmt.Sprintf("(%d,%v)", rev.ID, rev.Keys))
		}
		return "REV [" + strings.Join(parts, ",") + "]"
	case r.keys != nil:
		return fmt.Sprintf("KEYS %v", r.keys)
	default:
		return fmt.Sprintf("OK %d", r.num)
	}
}

// genOps builds one random operation sequence. Arguments are sometimes
// deliberately illegal to exercise rejection paths; time mostly advances with
// occasional backwards jolts.
func genOps(r *rand.Rand, cfg Config, n int) []simOp {
	ops := make([]simOp, 0, n)
	var now int64
	idPool := []int64{1, 2, 3, 4, 5, 6, 7, 8}
	keyPool := []string{"", "a", "b", "c", "d", "e", "f"}
	for i := 0; i < n; i++ {
		if r.Intn(6) == 0 {
			now += int64(r.Intn(40))
		}
		opNow := now
		if now > 2 && r.Intn(8) == 0 {
			opNow = now - int64(1+r.Intn(int(now)))
		}
		if opNow < 0 {
			opNow = 0
		}
		id := idPool[r.Intn(len(idPool))]
		if r.Intn(15) == 0 {
			id = 0
		}
		switch r.Intn(10) {
		case 0:
			ttl := int64(1 + r.Intn(int(cfg.MaxTTL)+5))
			if r.Intn(12) == 0 {
				ttl = 0
			}
			ops = append(ops, simOp{kind: opGrant, id: id, ttl: ttl, now: opNow})
		case 1:
			ops = append(ops, simOp{kind: opRenew, id: id, now: opNow})
		case 2:
			key := keyPool[r.Intn(len(keyPool))]
			ops = append(ops, simOp{kind: opAttach, id: id, key: key, now: opNow})
		case 3:
			ops = append(ops, simOp{kind: opTick, now: opNow})
		case 4:
			ops = append(ops, simOp{kind: opRevoke, id: id, now: opNow})
		case 5:
			ops = append(ops, simOp{kind: opCheckpoint, now: opNow})
		case 6:
			ops = append(ops, simOp{kind: opDemote, now: opNow})
		case 7:
			ops = append(ops, simOp{kind: opPromote, now: opNow})
		default:
			ops = append(ops, simOp{kind: opTTL, id: id, now: opNow})
		}
	}
	return ops
}

func runOnLessor(l *Lessor, o simOp) modelResult {
	switch o.kind {
	case opGrant:
		return modelResult{err: l.Grant(o.id, o.ttl, o.now)}
	case opRenew:
		v, err := l.Renew(o.id, o.now)
		return modelResult{err: err, num: v}
	case opAttach:
		return modelResult{err: l.Attach(o.key, o.id, o.now)}
	case opTick:
		revs, err := l.Tick(o.now)
		return modelResult{err: err, revs: revs}
	case opRevoke:
		keys, err := l.Revoke(o.id, o.now)
		return modelResult{err: err, keys: keys}
	case opCheckpoint:
		return modelResult{err: l.Checkpoint(o.now)}
	case opDemote:
		return modelResult{err: l.Demote(o.now)}
	case opPromote:
		return modelResult{err: l.Promote(o.now)}
	case opTTL:
		v, err := l.TTL(o.id, o.now)
		return modelResult{err: err, num: v}
	}
	return modelResult{}
}

// snapshot captures every externally meaningful field for equality checks.
type snapshot struct {
	leader bool
	clock  int64
	leases map[int64]struct {
		g, sv, x int64
		keys     []string
	}
	owner map[string]int64
}

func takeSnapshot(l *Lessor) snapshot {
	s := snapshot{
		leader: l.leader,
		clock:  l.clock,
		leases: make(map[int64]struct {
			g, sv, x int64
			keys     []string
		}),
		owner: make(map[string]int64),
	}
	for id, ls := range l.leases {
		s.leases[id] = struct {
			g, sv, x int64
			keys     []string
		}{ls.g, ls.sv, ls.x, append([]string(nil), ls.keys...)}
	}
	for k, v := range l.keyOwner {
		s.owner[k] = v
	}
	return s
}

func modelSnapshot(m *naiveModel) snapshot {
	s := snapshot{
		leader: m.leader,
		clock:  m.clock,
		leases: make(map[int64]struct {
			g, sv, x int64
			keys     []string
		}),
		owner: make(map[string]int64),
	}
	for id, ls := range m.leases {
		s.leases[id] = struct {
			g, sv, x int64
			keys     []string
		}{ls.g, ls.sv, ls.x, append([]string(nil), ls.keys...)}
	}
	for k, v := range m.owner {
		s.owner[k] = v
	}
	return s
}

func snapshotEqual(a, b snapshot) (bool, string) {
	if a.leader != b.leader {
		return false, fmt.Sprintf("leader %v != %v", a.leader, b.leader)
	}
	if a.clock != b.clock {
		return false, fmt.Sprintf("clock %d != %d", a.clock, b.clock)
	}
	if len(a.leases) != len(b.leases) {
		return false, fmt.Sprintf("lease count %d != %d", len(a.leases), len(b.leases))
	}
	for id, la := range a.leases {
		lb, ok := b.leases[id]
		if !ok {
			return false, fmt.Sprintf("lease %d missing in model", id)
		}
		if la.g != lb.g || la.sv != lb.sv || la.x != lb.x {
			return false, fmt.Sprintf("lease %d fields (g,sv,x)=(%d,%d,%d) != (%d,%d,%d)",
				id, la.g, la.sv, la.x, lb.g, lb.sv, lb.x)
		}
		if fmt.Sprint(la.keys) != fmt.Sprint(lb.keys) {
			return false, fmt.Sprintf("lease %d keys %v != %v", id, la.keys, lb.keys)
		}
	}
	if fmt.Sprint(a.owner) != fmt.Sprint(b.owner) {
		return false, fmt.Sprintf("owner map %v != %v", a.owner, b.owner)
	}
	return true, ""
}

func resultsEqual(a, b modelResult) (bool, string) {
	if fmt.Sprint(a.err) != fmt.Sprint(b.err) {
		return false, fmt.Sprintf("error %q != %q", a.err, b.err)
	}
	if a.num != b.num {
		return false, fmt.Sprintf("num %d != %d", a.num, b.num)
	}
	if len(a.revs) != len(b.revs) {
		return false, fmt.Sprintf("rev count %d != %d", len(a.revs), len(b.revs))
	}
	for i := range a.revs {
		if a.revs[i].ID != b.revs[i].ID || fmt.Sprint(a.revs[i].Keys) != fmt.Sprint(b.revs[i].Keys) {
			return false, fmt.Sprintf("rev %d %v != %v", i, a.revs[i], b.revs[i])
		}
	}
	if fmt.Sprint(a.keys) != fmt.Sprint(b.keys) {
		return false, fmt.Sprintf("keys %v != %v", a.keys, b.keys)
	}
	return true, ""
}

// TestRandomAgainstNaiveModel replays 2000 random sequences against both the
// real lessor and the naive simulator, and replays each winning trace on a
// second fresh lessor to prove identical revocation sequences and expiries.
func TestRandomAgainstNaiveModel(t *testing.T) {
	const sequences = 2000
	const opsPerSeq = 120
	cfg := Config{MinTTL: 5, MaxTTL: 60, E: 3, R: 3, Kmax: 4}
	var log strings.Builder

	for seq := 0; seq < sequences; seq++ {
		r := rand.New(rand.NewSource(int64(seq + 1)))
		ops := genOps(r, cfg, opsPerSeq)
		fmt.Fprintf(&log, "--- sequence %d seed=%d ops=%d ---\\n", seq, seq+1, len(ops))

		l, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		m := newNaiveModel(cfg)

		var firstTrace []string
		for step, op := range ops {
			got := runOnLessor(l, op)
			want := m.apply(op)
			fmt.Fprintf(&log, "step %d IN  %s\\n", step, op)
			fmt.Fprintf(&log, "step %d OUT impl=%s model=%s\\n", step, formatModelResult(got), formatModelResult(want))
			if ok, why := resultsEqual(got, want); !ok {
				t.Fatalf("seq %d step %d %s: result mismatch: %s\\n%s", seq, step, op, why, log.String())
			}
			if ok, why := snapshotEqual(takeSnapshot(l), modelSnapshot(m)); !ok {
				t.Fatalf("seq %d step %d %s: state mismatch: %s\\n%s", seq, step, op, why, log.String())
			}
			firstTrace = append(firstTrace, formatModelResult(got))
		}

		// Deterministic replay: a fresh lessor fed the same accepted inputs
		// must emit the exact same outputs.
		l2, err := New(cfg)
		if err != nil {
			t.Fatal(err)
		}
		for step, op := range ops {
			got := runOnLessor(l2, op)
			fmt.Fprintf(&log, "replay step %d IN  %s\\n", step, op)
			fmt.Fprintf(&log, "replay step %d OUT %s\\n", step, formatModelResult(got))
			if formatModelResult(got) != firstTrace[step] {
				t.Fatalf("seq %d step %d replay differs: %s vs %s\\n%s",
					seq, step, formatModelResult(got), firstTrace[step], log.String())
			}
		}
		if ok, why := snapshotEqual(takeSnapshot(l), takeSnapshot(l2)); !ok {
			t.Fatalf("seq %d end-state replay mismatch: %s\\n%s", seq, why, log.String())
		}
	}

	t.Logf("compared %d random sequences (%d ops each); inputs, outputs and\\n%s",
		sequences, opsPerSeq, "")
	t.Logf("judgement basis: per-op return equality plus full-state snapshot\\n%s", "")
	t.Logf("sample trace:\\n%s", head(&log, 40))
}

func head(b *strings.Builder, n int) string {
	lines := strings.Split(b.String(), "\\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\\n")
}
