package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sync"
	"testing"
)

// errClass 将错误归类为可比较的类别。
func errClass(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, ErrPermissionDenied):
		return "permission-denied"
	case errors.Is(err, ErrObjectDeleted):
		return "object-deleted"
	case errors.Is(err, ErrObjectNotDeleted):
		return "object-not-deleted"
	case errors.Is(err, ErrStaleDeletion):
		return "stale-deletion"
	case errors.Is(err, ErrLinkRestore):
		return "link-restore"
	case errors.Is(err, ErrNotFound):
		return "not-found"
	case errors.Is(err, ErrAlreadyExists):
		return "already-exists"
	default:
		return "unknown"
	}
}

// pair 同时驱动协调器与朴素模型，逐步比对判定结果与可观察状态。
type pair struct {
	t     *testing.T
	c     *Coordinator
	n     *naiveModel
	objs  []ObjectID
	links []LinkID
	step  int
}

func newPair(t *testing.T) *pair {
	t.Helper()
	p := &pair{t: t, c: New(), n: newNaive()}
	for _, lt := range []LinkType{
		{Name: "dep", Policy: CascadeInvalidate},
		{Name: "tag", Policy: Independent},
	} {
		p.c.RegisterLinkType(lt)
		p.n.registerType(lt)
	}
	for _, g := range []Grant{
		{ID: "g-del", Actor: "alice", Action: ActionDelete},
		{ID: "g-rev", Actor: "alice", Action: ActionRevive},
		{ID: "g-del-bob", Actor: "bob", Action: ActionDelete},
		{ID: "g-rev-bob", Actor: "bob", Action: ActionRevive},
	} {
		p.c.AddGrant(g)
		p.n.addGrant(g)
	}
	return p
}

// check 比对一次判定的输出，并全量比对两个实现的可观察状态；
// 日志打印每次判定的输入、输出与依据。
func (p *pair) check(input string, errC, errN error, basis string) {
	p.t.Helper()
	p.step++
	classC, classN := errClass(errC), errClass(errN)
	p.t.Logf("step=%d input=%s output=%s basis=%s", p.step, input, classC, basis)
	if classC != classN {
		p.t.Fatalf("step %d %s: coordinator=%s naive=%s", p.step, input, classC, classN)
	}
	for _, id := range p.objs {
		hC, errC := p.c.History(id)
		hN, errN := p.n.history(id)
		if (errC == nil) != (errN == nil) {
			p.t.Fatalf("step %d history %s: err mismatch %v vs %v", p.step, id, errC, errN)
		}
		if errC == nil && !reflect.DeepEqual(hC, hN) {
			p.t.Fatalf("step %d history %s diverged:\ncoordinator=%+v\nnaive=%+v", p.step, id, hC, hN)
		}
	}
	for _, id := range p.links {
		lC, _ := p.c.GetLink(id)
		lN, _ := p.n.getLink(id)
		if lC != lN {
			p.t.Fatalf("step %d link %s diverged: %+v vs %+v", p.step, id, lC, lN)
		}
	}
}

func (p *pair) basisOf(id ObjectID) string {
	h, err := p.c.History(id)
	if err != nil {
		return fmt.Sprintf("obj=%s missing", id)
	}
	last := h.Intervals[len(h.Intervals)-1]
	return fmt.Sprintf("obj=%s alive=%v intervals=%d lastClosedBy=%s",
		id, h.Alive, len(h.Intervals), last.ClosedBy)
}

// deletionIDs 返回对象全部删除事件标识（按发生顺序）。
func deletionIDs(h ObjectHistory) []EventID {
	var out []EventID
	for _, iv := range h.Intervals {
		if iv.ClosedBy != "" {
			out = append(out, iv.ClosedBy)
		}
	}
	return out
}

// TestRandomSequenceAgainstNaiveModel 随机生成操作序列，
// 与独立实现的朴素生命周期模型逐步对照。
func TestRandomSequenceAgainstNaiveModel(t *testing.T) {
	for seed := int64(0); seed < 40; seed++ {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runRandomSequence(t, rand.New(rand.NewSource(seed)), 120)
		})
	}
}

func runRandomSequence(t *testing.T, rng *rand.Rand, steps int) {
	t.Helper()
	p := newPair(t)
	objIDs := []ObjectID{"o0", "o1", "o2", "o3"}
	grantIDs := []GrantID{"g-del", "g-rev", "g-del-bob", "g-rev-bob", "g-missing"}
	linkCounter := 0

	randObj := func() ObjectID { return objIDs[rng.Intn(len(objIDs))] }

	for i := 0; i < steps; i++ {
		switch rng.Intn(10) {
		case 0: // 创建
			id := randObj()
			errC := p.c.CreateObject("alice", id)
			errN := p.n.createObject("alice", id)
			if errC == nil {
				p.objs = append(p.objs, id)
			}
			p.check(fmt.Sprintf("create %s", id), errC, errN, "create")
		case 1, 2: // 删除（含无效权限条目）
			id := randObj()
			gid := grantIDs[rng.Intn(len(grantIDs))]
			errC := p.c.DeleteObject("alice", id, gid)
			errN := p.n.deleteObject("alice", id, gid)
			p.check(fmt.Sprintf("delete %s grant=%s", id, gid), errC, errN, p.basisOf(id))
		case 3, 4: // 复活（正确指向 / 指向更早删除 / 垃圾指针 / 显式链接列表）
			id := randObj()
			req := ReviveRequest{Actor: "alice", Object: id, GrantID: "g-rev"}
			h, err := p.c.History(id)
			dels := []EventID{"evt-424242"}
			if err == nil {
				dels = append(dels, deletionIDs(h)...)
			}
			req.ResumesDeletion = dels[rng.Intn(len(dels))]
			req.RestoreLinks = rng.Intn(2) == 0
			if req.RestoreLinks && len(p.links) > 0 && rng.Intn(2) == 0 {
				// 随机挑选若干已有链接作为显式恢复列表，
				// 其中可能包含不满足条件的链接。
				for _, lid := range p.links {
					if rng.Intn(3) == 0 {
						req.LinkIDs = append(req.LinkIDs, lid)
					}
				}
			}
			errC := p.c.ReviveObject(req)
			errN := p.n.reviveObject(req)
			p.check(fmt.Sprintf("revive %s resumes=%s restore=%v links=%v",
				id, req.ResumesDeletion, req.RestoreLinks, req.LinkIDs),
				errC, errN, p.basisOf(id))
		case 5: // 读
			id := randObj()
			errC := p.c.ReadObject("alice", id)
			errN := p.n.accessObject("alice", id, EventRead, "")
			p.check(fmt.Sprintf("read %s", id), errC, errN, p.basisOf(id))
		case 6: // 写
			id := randObj()
			errC := p.c.WriteObject("alice", id, "payload")
			errN := p.n.accessObject("alice", id, EventWrite, "payload")
			p.check(fmt.Sprintf("write %s", id), errC, errN, p.basisOf(id))
		case 7, 8: // 建链
			from, to := randObj(), randObj()
			typeName := []string{"dep", "tag", "missing-type"}[rng.Intn(3)]
			lid := LinkID(fmt.Sprintf("l%d", linkCounter))
			linkCounter++
			errC := p.c.LinkObjects("alice", lid, typeName, from, to)
			errN := p.n.linkObjects("alice", lid, typeName, from, to)
			if errC == nil {
				p.links = append(p.links, lid)
			}
			p.check(fmt.Sprintf("link %s %s %s->%s", lid, typeName, from, to),
				errC, errN, "link")
		case 9: // 吊销权限条目
			gid := grantIDs[rng.Intn(len(grantIDs))]
			p.c.RevokeGrant(gid)
			p.n.revokeGrant(gid)
			p.check(fmt.Sprintf("revoke-grant %s", gid), nil, nil, "revoke")
		}
	}
}

// TestConcurrentLinearizable 并发执行删除/复活/读写/审计查询，
// 任一时刻的历史都必须满足溯源链结构不变量，
// 最终可观察结果等价于某个全局串行顺序。
func TestConcurrentLinearizable(t *testing.T) {
	c := New()
	c.RegisterLinkType(LinkType{Name: "dep", Policy: CascadeInvalidate})
	c.RegisterLinkType(LinkType{Name: "tag", Policy: Independent})
	c.AddGrant(Grant{ID: "g-del", Actor: "alice", Action: ActionDelete})
	c.AddGrant(Grant{ID: "g-rev", Actor: "alice", Action: ActionRevive})
	objs := []ObjectID{"o0", "o1", "o2", "o3"}
	for _, id := range objs {
		if err := c.CreateObject("alice", id); err != nil {
			t.Fatal(err)
		}
	}
	for i, id := range objs {
		lid := LinkID(fmt.Sprintf("l%d", i))
		if err := c.LinkObjects("alice", lid, "dep", id, objs[(i+1)%len(objs)]); err != nil {
			t.Fatal(err)
		}
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 64)
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			rng := rand.New(rand.NewSource(seed))
			for i := 0; i < 300; i++ {
				id := objs[rng.Intn(len(objs))]
				switch rng.Intn(6) {
				case 0:
					_ = c.DeleteObject("alice", id, "g-del")
				case 1:
					h, err := c.History(id)
					if err != nil || h.Alive {
						continue
					}
					dels := deletionIDs(h)
					resumes := dels[len(dels)-1]
					if rng.Intn(4) == 0 && len(dels) > 1 {
						resumes = dels[0] // 故意指向更早的删除事件
					}
					_ = c.ReviveObject(ReviveRequest{
						Actor: "alice", Object: id, GrantID: "g-rev",
						ResumesDeletion: resumes, RestoreLinks: rng.Intn(2) == 0,
					})
				case 2:
					_ = c.ReadObject("alice", id)
				case 3:
					_ = c.WriteObject("alice", id, "x")
				case 4:
					if h, err := c.History(id); err == nil {
						// 任一时刻的历史都必须结构完整。
						if _, verr := validateChain(h); verr != nil {
							errCh <- fmt.Errorf("obj=%s: %w", id, verr)
							return
						}
					}
				case 5:
					lid := LinkID(fmt.Sprintf("lx-%d-%d", seed, i))
					_ = c.LinkObjects("alice", lid, "tag", id, objs[rng.Intn(len(objs))])
				}
			}
		}(int64(w))
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent history violated chain invariants: %v", err)
	}

	seen := map[LogicalTime]bool{}
	for _, id := range objs {
		h, err := c.History(id)
		if err != nil {
			t.Fatal(err)
		}
		checkChain(t, h)
		for _, iv := range h.Intervals {
			for _, e := range iv.Events {
				if seen[e.Time] {
					t.Fatalf("duplicate logical time %d across events", e.Time)
				}
				seen[e.Time] = true
			}
		}
	}
	t.Logf("final: %d distinct event times across %d objects", len(seen), len(objs))
}
