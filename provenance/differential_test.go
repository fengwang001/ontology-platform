package provenance

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// op 是喂给两套实现的同一条写操作（预先记录，便于出错时复现种子）。
type op struct {
	kind           int // 0=object 1=link
	id             string
	writeAt        Time
	start, end     Time
	exists         bool
	source, target string
}

func applyOps(t *testing.T, s *Store, n *NaiveStore, ops []op) {
	t.Helper()
	for _, o := range ops {
		var err1, err2 error
		if o.kind == 0 {
			err1 = s.WriteObject(ObjectID(o.id), o.writeAt, Interval{o.start, o.end}, o.exists)
			err2 = n.WriteObject(ObjectID(o.id), o.writeAt, Interval{o.start, o.end}, o.exists)
		} else {
			err1 = s.WriteLink(LinkID(o.id), o.writeAt, Interval{o.start, o.end},
				ObjectID(o.source), ObjectID(o.target), o.exists)
			err2 = n.WriteLink(LinkID(o.id), o.writeAt, Interval{o.start, o.end},
				ObjectID(o.source), ObjectID(o.target), o.exists)
		}
		if (err1 == nil) != (err2 == nil) {
			t.Fatalf("write rejection mismatch: store=%v naive=%v op=%+v", err1, err2, o)
		}
	}
}

func generateOps(rng *rand.Rand, count int) ([]op, []ObjectID, []LinkID) {
	var ops []op
	objs := []ObjectID{"o0", "o1", "o2", "o3", "o4"}
	for _, id := range objs {
		ops = append(ops, op{kind: 0, id: string(id), writeAt: 1, start: 0, end: 100, exists: true})
	}
	// 每个对象 3 条可能链接，随机化邻接。
	var links []LinkID
	nextWrite := Time(2)
	for i := 0; i < 15; i++ {
		a := objs[rng.Intn(len(objs))]
		b := objs[rng.Intn(len(objs))]
		if a == b {
			continue
		}
		id := LinkID(fmt.Sprintf("e%d", i))
		links = append(links, id)
		ops = append(ops, op{kind: 1, id: string(id), writeAt: nextWrite,
			start: 0, end: 100, exists: true, source: string(a), target: string(b)})
		nextWrite++
	}
	// 随机修正：收窄/延长/整体替换/消亡，写入时间严格递增。
	for i := 0; i < count; i++ {
		nextWrite += Time(rng.Intn(3) + 1)
		if rng.Intn(2) == 0 {
			id := objs[rng.Intn(len(objs))]
			st := Time(rng.Intn(120))
			en := st + Time(rng.Intn(120))
			ops = append(ops, op{kind: 0, id: string(id), writeAt: nextWrite,
				start: st, end: en, exists: rng.Intn(6) != 0})
		} else if len(links) > 0 {
			id := links[rng.Intn(len(links))]
			var src, tgt string
			for _, o := range ops {
				if o.kind == 1 && LinkID(o.id) == id {
					src, tgt = o.source, o.target
					break
				}
			}
			st := Time(rng.Intn(120))
			en := st + Time(rng.Intn(120))
			ops = append(ops, op{kind: 1, id: string(id), writeAt: nextWrite,
				start: st, end: en, exists: rng.Intn(6) != 0, source: src, target: tgt})
		}
	}
	return ops, objs, links
}

// TestDifferentialVsNaive 在大量随机操作序列上对照两套独立实现。
func TestDifferentialVsNaive(t *testing.T) {
	const iterations = 40
	for seed := int64(1); seed <= iterations; seed++ {
		rng := rand.New(rand.NewSource(seed))
		ops, objs, _ := generateOps(rng, 80)
		s, n := NewStore(), NewNaiveStore()
		applyOps(t, s, n, ops)

		maxWrite := Time(0)
		for _, o := range ops {
			if o.writeAt > maxWrite {
				maxWrite = o.writeAt
			}
		}

		// 在多种 validAt / asOf / depth 组合上逐条比对（含错误与拒绝分类）。
		for trial := 0; trial < 120; trial++ {
			q := Query{
				Source:   objs[rng.Intn(len(objs))],
				ValidAt:  Time(rng.Intn(130)) - 5,
				AsOf:     Time(rng.Intn(int(maxWrite) + 4)),
				MaxDepth: 1 + rng.Intn(4),
			}
			if rng.Intn(15) == 0 {
				q.AsOf = IllegalTime // 故意触发参数错误路径
			}
			r1, e1 := s.Traverse(q)
			r2, e2 := n.Traverse(q)
			if (e1 == nil) != (e2 == nil) || (e1 != nil && e1.Error() != e2.Error()) {
				t.Fatalf("seed=%d trial=%d error mismatch: %v vs %v q=%+v",
					seed, trial, e1, e2, q)
			}
			if e1 != nil {
				continue
			}
			if !equalResults(r1, r2) {
				t.Fatalf("seed=%d trial=%d result mismatch\nstore=%+v\nnaive=%+v\nq=%+v",
					seed, trial, summarize(r1), summarize(r2), q)
			}
		}
	}
}

func summarize(r *Result) string {
	return fmt.Sprintf("sourceReason=%s paths=%d rejected=%v seen=%d",
		r.SourceReason, len(r.Paths), r.Rejected, r.CandidatesSeen)
}

// equalResults 比较可观察语义：源原因、路径（节点+每跳三方记录标识）、
// 拒绝分类（不比较 CandidatesSeen，因为两套实现的扫描策略本就不同）。
func equalResults(a, b *Result) bool {
	if a.SourceReason != b.SourceReason {
		return false
	}
	if len(a.Paths) != len(b.Paths) {
		return false
	}
	for i := range a.Paths {
		pa, pb := a.Paths[i], b.Paths[i]
		if !reflect.DeepEqual(pa.Nodes, pb.Nodes) {
			return false
		}
		for j := range pa.Hops {
			ha, hb := pa.Hops[j], pb.Hops[j]
			if ha.Link.ID != hb.Link.ID || ha.Link.WriteAt != hb.Link.WriteAt ||
				ha.Target.ID != hb.Target.ID || ha.Target.WriteAt != hb.Target.WriteAt {
				return false
			}
		}
	}
	if len(a.Rejected) != len(b.Rejected) {
		return false
	}
	for i := range a.Rejected {
		ra, rb := a.Rejected[i], b.Rejected[i]
		if ra.From != rb.From || ra.Link != rb.Link || ra.To != rb.To ||
			ra.Failed != rb.Failed || ra.Reason != rb.Reason {
			return false
		}
	}
	return true
}
