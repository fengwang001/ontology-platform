package reflog

import (
	"fmt"
	"math/rand"
	"sort"
	"testing"
)

// naiveSystem is an independent, deliberately inefficient reference
// implementation of the same semantics. It keeps one plain slice of
// records per reference, recomputes tiers from scratch on every
// expiry and GC, and scans everything. The randomized test below
// compares System against it operation by operation.
type naiveSystem struct {
	cfg     Config
	now     int64
	commits map[CommitID]*Commit
	objects map[ObjectID]*Object
	heads   map[string]CommitID
	logs    map[string][]Record
	seqs    map[string]uint64
}

func newNaiveSystem(cfg Config) *naiveSystem {
	return &naiveSystem{
		cfg:     cfg,
		commits: make(map[CommitID]*Commit),
		objects: make(map[ObjectID]*Object),
		heads:   make(map[string]CommitID),
		logs:    make(map[string][]Record),
		seqs:    make(map[string]uint64),
	}
}

func (m *naiveSystem) clock(now int64) error {
	if now < m.now {
		return ErrClockRegression
	}
	return nil
}

func (m *naiveSystem) writeCommit(id CommitID, parents []CommitID, createdAt int64, contents []ObjectID, size int64, now int64) error {
	if id == "" || createdAt < 0 || size < 0 || now < 0 {
		return ErrInvalidParam
	}
	if err := m.clock(now); err != nil {
		return err
	}
	if _, ok := m.commits[id]; !ok {
		m.commits[id] = &Commit{ID: id, Parents: parents, CreatedAt: createdAt, Contents: contents, Size: size, FirstWrittenAt: now}
	}
	m.now = now
	return nil
}

func (m *naiveSystem) writeObject(id ObjectID, size int64, now int64) error {
	if id == "" || size < 0 || now < 0 {
		return ErrInvalidParam
	}
	if err := m.clock(now); err != nil {
		return err
	}
	if _, ok := m.objects[id]; !ok {
		m.objects[id] = &Object{ID: id, Size: size, FirstWrittenAt: now}
	}
	m.now = now
	return nil
}

// ancestor reports whether old is an ancestor of (or equal to) head,
// by brute-force traversal over the commits currently present.
func (m *naiveSystem) ancestor(old, head CommitID) bool {
	seen := make(map[CommitID]bool)
	stack := []CommitID{head}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if id == old {
			return true
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		if c, ok := m.commits[id]; ok {
			stack = append(stack, c.Parents...)
		}
	}
	return false
}

// retentionOf recomputes the tier of r from scratch.
func (m *naiveSystem) retentionOf(name string, r Record) int64 {
	head, ok := m.heads[name]
	if ok && r.Old != "" && m.ancestor(r.Old, head) {
		return m.cfg.ReachableRetention
	}
	return m.cfg.UnreachableRetention
}

func (m *naiveSystem) expired(name string, r Record, now int64) bool {
	return now-r.Time >= m.retentionOf(name, r)
}

func (m *naiveSystem) appendRec(name string, old, new CommitID, now int64, who string) {
	rec := Record{Seq: m.seqs[name], Old: old, New: new, Time: now, Who: who}
	m.seqs[name]++
	m.logs[name] = append(m.logs[name], rec)
}

func (m *naiveSystem) createRef(name string, commit CommitID, now int64, who string) error {
	if name == "" || now < 0 {
		return ErrInvalidParam
	}
	if err := m.clock(now); err != nil {
		return err
	}
	if _, ok := m.commits[commit]; !ok {
		return ErrCommitNotFound
	}
	old := CommitID("")
	if head, ok := m.heads[name]; ok {
		old = head
	}
	m.appendRec(name, old, commit, now, who)
	m.heads[name] = commit
	m.now = now
	return nil
}

func (m *naiveSystem) updateRef(name string, commit CommitID, now int64, who string) error {
	if name == "" || now < 0 {
		return ErrInvalidParam
	}
	if err := m.clock(now); err != nil {
		return err
	}
	head, ok := m.heads[name]
	if !ok {
		return ErrRefNotFound
	}
	if _, ok := m.commits[commit]; !ok {
		return ErrCommitNotFound
	}
	m.appendRec(name, head, commit, now, who)
	m.heads[name] = commit
	m.now = now
	return nil
}

func (m *naiveSystem) deleteRef(name string, now int64, who string) error {
	if name == "" || now < 0 {
		return ErrInvalidParam
	}
	if err := m.clock(now); err != nil {
		return err
	}
	head, ok := m.heads[name]
	if !ok {
		return ErrRefNotFound
	}
	m.appendRec(name, head, "", now, who)
	delete(m.heads, name)
	m.now = now
	return nil
}

func (m *naiveSystem) readLog(name string, index int) (Record, error) {
	if name == "" || index < 1 {
		return Record{}, ErrInvalidParam
	}
	recs, ok := m.logs[name]
	if !ok {
		return Record{}, ErrRefNotFound
	}
	if index > len(recs) {
		return Record{}, ErrRecordNotFound
	}
	return recs[len(recs)-index], nil
}

func (m *naiveSystem) expireLogs(now int64) (int, error) {
	if now < 0 {
		return 0, ErrInvalidParam
	}
	if err := m.clock(now); err != nil {
		return 0, err
	}
	total := 0
	for name, recs := range m.logs {
		kept := recs[:0]
		for _, r := range recs {
			if m.expired(name, r, now) {
				total++
			} else {
				kept = append(kept, r)
			}
		}
		m.logs[name] = kept
	}
	m.now = now
	return total, nil
}

func (m *naiveSystem) gc(now int64) (GCStats, error) {
	if now < 0 {
		return GCStats{}, ErrInvalidParam
	}
	if err := m.clock(now); err != nil {
		return GCStats{}, err
	}
	stats := m.gcBody(now)
	m.now = now
	return stats, nil
}

func (m *naiveSystem) expireAndGC(now int64) (int, GCStats, error) {
	if now < 0 {
		return 0, GCStats{}, ErrInvalidParam
	}
	if err := m.clock(now); err != nil {
		return 0, GCStats{}, err
	}
	expired, _ := m.expireLogs(now)
	stats, _ := m.gc(now)
	return expired, stats, nil
}

func (m *naiveSystem) gcBody(now int64) GCStats {
	roots := make(map[CommitID]bool)
	for _, head := range m.heads {
		roots[head] = true
	}
	for name, recs := range m.logs {
		for _, r := range recs {
			if m.expired(name, r, now) {
				continue
			}
			if r.Old != "" {
				roots[r.Old] = true
			}
			if r.New != "" {
				roots[r.New] = true
			}
		}
	}
	live := make(map[CommitID]bool)
	stack := make([]CommitID, 0, len(roots))
	for id := range roots {
		stack = append(stack, id)
	}
	for len(stack) > 0 {
		id := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if live[id] {
			continue
		}
		c, ok := m.commits[id]
		if !ok {
			continue
		}
		live[id] = true
		stack = append(stack, c.Parents...)
	}
	liveObjects := make(map[ObjectID]bool)
	for id := range live {
		for _, oid := range m.commits[id].Contents {
			liveObjects[oid] = true
		}
	}
	var stats GCStats
	for id, c := range m.commits {
		if live[id] || now-c.FirstWrittenAt < m.cfg.FreshnessGrace {
			continue
		}
		delete(m.commits, id)
		stats.Commits++
		stats.Bytes += c.Size
	}
	for id, o := range m.objects {
		if liveObjects[id] || now-o.FirstWrittenAt < m.cfg.FreshnessGrace {
			continue
		}
		delete(m.objects, id)
		stats.Objects++
		stats.Bytes += o.Size
	}
	return stats
}

func sortedCommitIDs(s *System) []string {
	out := make([]string, 0, len(s.store.commits))
	for id := range s.store.commits {
		out = append(out, string(id))
	}
	sort.Strings(out)
	return out
}

func sortedObjectIDs(s *System) []string {
	out := make([]string, 0, len(s.store.objects))
	for id := range s.store.objects {
		out = append(out, string(id))
	}
	sort.Strings(out)
	return out
}

func sortedNaiveCommits(m *naiveSystem) []string {
	out := make([]string, 0, len(m.commits))
	for id := range m.commits {
		out = append(out, string(id))
	}
	sort.Strings(out)
	return out
}

func sortedNaiveObjects(m *naiveSystem) []string {
	out := make([]string, 0, len(m.objects))
	for id := range m.objects {
		out = append(out, string(id))
	}
	sort.Strings(out)
	return out
}

func deepCompare(t *testing.T, s *System, m *naiveSystem, names []string) {
	t.Helper()
	if got, want := fmt.Sprint(sortedCommitIDs(s)), fmt.Sprint(sortedNaiveCommits(m)); got != want {
		t.Fatalf("commits diverge:\nsys:   %s\nnaive: %s", got, want)
	}
	if got, want := fmt.Sprint(sortedObjectIDs(s)), fmt.Sprint(sortedNaiveObjects(m)); got != want {
		t.Fatalf("objects diverge:\nsys:   %s\nnaive: %s", got, want)
	}
	for _, name := range names {
		headS, okS := s.Head(name)
		headM, okM := m.heads[name]
		if okS != okM || headS != headM {
			t.Fatalf("head(%s) diverge: sys=%v/%v naive=%v/%v", name, headS, okS, headM, okM)
		}
		var sysRecs []*Record
		if l, ok := s.logs[name]; ok {
			sysRecs = l.merged()
		}
		naiveRecs := m.logs[name]
		if len(sysRecs) != len(naiveRecs) {
			t.Fatalf("log(%s) length diverge: sys=%d naive=%d", name, len(sysRecs), len(naiveRecs))
		}
		for i := range sysRecs {
			if *sysRecs[i] != naiveRecs[i] {
				t.Fatalf("log(%s)[%d] diverge: sys=%+v naive=%+v", name, i, *sysRecs[i], naiveRecs[i])
			}
		}
	}
}

// TestRandomModelComparison drives System and the naive model with
// the same random operation sequences and compares every result.
func TestRandomModelComparison(t *testing.T) {
	for seed := int64(1); seed <= 8; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runRandomComparison(t, seed)
		})
	}
}

func runRandomComparison(t *testing.T, seed int64) {
	cfg := Config{ReachableRetention: 8, UnreachableRetention: 3, FreshnessGrace: 2}
	s := mustSystem(t, cfg)
	m := newNaiveSystem(cfg)
	rng := rand.New(rand.NewSource(seed))
	names := []string{"a", "b", "c", "d"}

	var commits []CommitID
	var objects []ObjectID
	now := int64(0)
	const ops = 3000
	opCounts := map[string]int{}

	t.Logf("输入: seed=%d 配置=%+v 操作数=%d", seed, cfg, ops)

	pickCommit := func() CommitID {
		if len(commits) == 0 {
			return "none"
		}
		return commits[rng.Intn(len(commits))]
	}
	pickObject := func() ObjectID {
		if len(objects) == 0 {
			return "none"
		}
		return objects[rng.Intn(len(objects))]
	}
	pickName := func() string { return names[rng.Intn(len(names))] }

	step := func(label string, sysErr, naiveErr error) {
		opCounts[label]++
		if sysErr != naiveErr {
			t.Fatalf("op %s: err diverge: sys=%v naive=%v", label, sysErr, naiveErr)
		}
	}

	for i := 0; i < ops; i++ {
		now += int64(rng.Intn(3))
		if rng.Intn(100) < 4 {
			now -= int64(rng.Intn(8)) // clock regression attempt
		}
		switch rng.Intn(9) {
		case 0:
			id := CommitID(fmt.Sprintf("c%d", len(commits)))
			var parents []CommitID
			for k := 0; k < rng.Intn(3); k++ {
				parents = append(parents, pickCommit())
			}
			var contents []ObjectID
			for k := 0; k < rng.Intn(3); k++ {
				contents = append(contents, pickObject())
			}
			size := int64(rng.Intn(50))
			step("writeCommit",
				s.WriteCommit(id, parents, now, contents, size, now),
				m.writeCommit(id, parents, now, contents, size, now))
			commits = append(commits, id)
		case 1:
			id := ObjectID(fmt.Sprintf("o%d", len(objects)))
			size := int64(rng.Intn(50))
			step("writeObject",
				s.WriteObject(id, size, now),
				m.writeObject(id, size, now))
			objects = append(objects, id)
		case 2:
			name, c := pickName(), pickCommit()
			step("createRef",
				s.CreateRef(name, c, now, "rnd"),
				m.createRef(name, c, now, "rnd"))
		case 3:
			name, c := pickName(), pickCommit()
			step("updateRef",
				s.UpdateRef(name, c, now, "rnd"),
				m.updateRef(name, c, now, "rnd"))
		case 4:
			name := pickName()
			step("deleteRef",
				s.DeleteRef(name, now, "rnd"),
				m.deleteRef(name, now, "rnd"))
		case 5:
			name, idx := pickName(), rng.Intn(6)+1
			rs, es := s.ReadLog(name, idx)
			rm, em := m.readLog(name, idx)
			opCounts["readLog"]++
			if es != em || rs != rm {
				t.Fatalf("readLog(%s,%d): sys=%+v/%v naive=%+v/%v", name, idx, rs, es, rm, em)
			}
		case 6:
			ns, es := s.ExpireLogs(now)
			nm, em := m.expireLogs(now)
			opCounts["expire"]++
			if es != em || ns != nm {
				t.Fatalf("expire: sys=%d/%v naive=%d/%v", ns, es, nm, em)
			}
		case 7:
			sts, es := s.GC(now)
			stm, em := m.gc(now)
			opCounts["gc"]++
			if es != em || sts != stm {
				t.Fatalf("gc: sys=%+v/%v naive=%+v/%v", sts, es, stm, em)
			}
		case 8:
			xs, sts, es := s.ExpireAndGC(now)
			xm, stm, em := m.expireAndGC(now)
			opCounts["expireAndGC"]++
			if es != em || xs != xm || sts != stm {
				t.Fatalf("expireAndGC: sys=%d/%+v/%v naive=%d/%+v/%v", xs, sts, es, xm, stm, em)
			}
		}
		if i%200 == 199 {
			deepCompare(t, s, m, names)
		}
	}
	deepCompare(t, s, m, names)
	t.Logf("实际输出: 操作分布=%v 终态提交=%v 对象=%v", opCounts, sortedCommitIDs(s), sortedObjectIDs(s))
	t.Logf("判定依据: 3000个随机操作的每次返回值/错误与每200步的全量状态均与朴素模型一致")
}
