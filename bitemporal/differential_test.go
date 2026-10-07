package bitemporal

import (
	"fmt"
	"math/rand/v2"
	"testing"
)

type linkSpec struct {
	id, src, dst string
}

type simulator struct {
	store *Store
	naive *NaiveStore
	rng   *rand.Rand
	clock int
	nodes []string
	links []linkSpec
	seq   int
}

func newSimulator(seed uint64) *simulator {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	s := &simulator{
		store: NewStore(),
		naive: NewNaiveStore(),
		rng:   rng,
		clock: 1,
	}
	const nodeCount = 10
	for i := 0; i < nodeCount; i++ {
		id := fmt.Sprintf("N%d", i)
		s.nodes = append(s.nodes, id)
		s.writeObject(id, s.randomInterval())
	}
	return s
}

func (s *simulator) randomInterval() Interval {
	base := newQClock()
	a := s.rng.IntN(40)
	b := a + 1 + s.rng.IntN(20)
	return base.iv(a, b)
}

func (s *simulator) tick() int {
	h := s.clock
	s.clock += 1 + s.rng.IntN(3)
	return h
}

func (s *simulator) writeObject(id string, iv Interval) {
	s.seq++
	rec := ObjectRecord{
		ID:        id,
		VersionID: fmt.Sprintf("%s@%d", id, s.seq),
		Valid:     iv,
		WrittenAt: newQClock().at(s.tick()),
	}
	if err := s.store.AppendObject(rec); err != nil {
		panic(err)
	}
	if err := s.naive.AppendObject(rec); err != nil {
		panic(err)
	}
}

func (s *simulator) writeLink(spec linkSpec, iv Interval) {
	s.seq++
	rec := LinkRecord{
		ID:        spec.id,
		VersionID: fmt.Sprintf("%s@%d", spec.id, s.seq),
		SourceID:  spec.src,
		TargetID:  spec.dst,
		Valid:     iv,
		WrittenAt: newQClock().at(s.tick()),
	}
	if err := s.store.AppendLink(rec); err != nil {
		panic(err)
	}
	if err := s.naive.AppendLink(rec); err != nil {
		panic(err)
	}
}

func (s *simulator) step() Query {
	switch s.rng.IntN(4) {
	case 0:
		s.writeObject(s.nodes[s.rng.IntN(len(s.nodes))], s.randomInterval())
	case 1:
		if len(s.links) > 0 && s.rng.IntN(2) == 0 {
			spec := s.links[s.rng.IntN(len(s.links))]
			s.writeLink(spec, s.randomInterval())
		} else {
			spec := linkSpec{
				id:  fmt.Sprintf("L%d", len(s.links)),
				src: s.nodes[s.rng.IntN(len(s.nodes))],
				dst: s.nodes[s.rng.IntN(len(s.nodes))],
			}
			s.links = append(s.links, spec)
			s.writeLink(spec, s.randomInterval())
		}
	default:
		// 不做写入，仅增加查询权重。
	}
	base := newQClock()
	return Query{
		SourceID: s.nodes[s.rng.IntN(len(s.nodes))],
		ValidAt:  base.at(s.rng.IntN(45) - 2),
		AsOf:     base.at(s.rng.IntN(s.clock + 5)),
		MaxDepth: 1 + s.rng.IntN(3),
	}
}

func hopEqual(a, b HopEvidence) bool { return a == b }

func pathEqual(a, b Path) bool {
	if len(a.Nodes) != len(b.Nodes) || len(a.Links) != len(b.Links) || len(a.Evidence) != len(b.Evidence) {
		return false
	}
	for i := range a.Nodes {
		if a.Nodes[i] != b.Nodes[i] {
			return false
		}
	}
	for i := range a.Links {
		if a.Links[i] != b.Links[i] || !hopEqual(a.Evidence[i], b.Evidence[i]) {
			return false
		}
	}
	return true
}

func blockedEqual(a, b BlockedEdge) bool {
	if len(a.Prefix) != len(b.Prefix) || a.LinkID != b.LinkID || a.TargetID != b.TargetID {
		return false
	}
	for i := range a.Prefix {
		if a.Prefix[i] != b.Prefix[i] {
			return false
		}
	}
	return a.SourceStatus == b.SourceStatus && a.LinkStatus == b.LinkStatus &&
		a.TargetStatus == b.TargetStatus && a.Combined == b.Combined
}

func resultEqual(a, b *Result) bool {
	if len(a.Paths) != len(b.Paths) || len(a.Blocked) != len(b.Blocked) {
		return false
	}
	for i := range a.Paths {
		if !pathEqual(a.Paths[i], b.Paths[i]) {
			return false
		}
	}
	for i := range a.Blocked {
		if !blockedEqual(a.Blocked[i], b.Blocked[i]) {
			return false
		}
	}
	return true
}

// 在大量随机「写入修正 + AsOf 查询」序列上，索引实现与全量扫描的朴素实现
// 必须逐条返回相同的错误类别、路径集合（含三方证据版本）与阻断边类别。
func TestRandomDifferentialAgainstNaive(t *testing.T) {
	const seeds = 24
	const ops = 800
	for seed := uint64(1); seed <= seeds; seed++ {
		sim := newSimulator(seed)
		engine := NewEngine(sim.store, nil)
		for op := 0; op < ops; op++ {
			q := sim.step()
			r1, err1 := engine.AsOf(q)
			r2, err2 := sim.naive.AsOf(q)
			k1, k2 := ErrorKind(0), ErrorKind(0)
			if qe, ok := err1.(*QueryError); ok {
				k1 = qe.Kind
			}
			if qe, ok := err2.(*QueryError); ok {
				k2 = qe.Kind
			}
			if k1 != k2 {
				t.Fatalf("seed=%d op=%d q=%+v 错误类别不一致: %d vs %d", seed, op, q, k1, k2)
			}
			if k1 == 0 && !resultEqual(r1, r2) {
				t.Fatalf("seed=%d op=%d q=%+v 结果不一致\nindexed=%+v\nnaive=%+v",
					seed, op, q, dump(r1), dump(r2))
			}
		}
	}
}

func dump(r *Result) string {
	if r == nil {
		return "<nil>"
	}
	return fmt.Sprintf("paths=%v blocked=%+v", r.Paths, r.Blocked)
}
