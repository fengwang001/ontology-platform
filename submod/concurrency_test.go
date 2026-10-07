package submod

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"sync"
	"testing"
)

// opRec 记录一次已执行的变更操作，用于按序号回放。
type opRec struct {
	kind  string
	args  []any
	rev   uint64
	apply func(n *naive) (uint64, error)
}

// TestConcurrentSerialEquivalence 并发执行混合操作，把成功的操作按返回
// 序号在朴素模型上回放，验证终态一致（即存在等价的串行顺序）。
func TestConcurrentSerialEquivalence(t *testing.T) {
	a := mustRepo(t, "A",
		cm("a0", nil, nil), cm("a1", []string{"a0"}, nil), cm("a2", []string{"a1"}, nil))
	b := mustRepo(t, "B",
		cm("b0", nil, nil), cm("b1", []string{"b0"}, nil))
	if err := a.SetBranch("main", "a2"); err != nil {
		t.Fatal(err)
	}
	if err := b.SetBranch("main", "b1"); err != nil {
		t.Fatal(err)
	}
	super := mustRepo(t, "S", cm("s0", nil, Table{
		"m1": {Repo: "A", Commit: "a1", Track: "main"},
		"m2": {Repo: "B", Commit: "b0"},
	}))
	if err := super.SetBranch("main", "s0"); err != nil {
		t.Fatal(err)
	}
	repos := map[string]*Repo{"S": super, "A": a, "B": b}
	c := mustCoord(t, cloneRepos(repos), "S")
	mustSet(t, func() (uint64, error) { return c.SetCheckout("m1", "a1") })
	mustSet(t, func() (uint64, error) { return c.SetCheckout("m2", "b0") })

	var mu sync.Mutex
	var successes []opRec
	record := func(op opRec, rev uint64, err error) {
		if err != nil {
			return
		}
		op.rev = rev
		mu.Lock()
		successes = append(successes, op)
		mu.Unlock()
	}

	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				switch (g + j) % 7 {
				case 0:
					rev, err := c.UpdateToPins()
					record(opRec{kind: "update", apply: func(n *naive) (uint64, error) { return n.update() }}, rev, err)
				case 1:
					rev, err := c.AdvanceTracking()
					record(opRec{kind: "advance", apply: func(n *naive) (uint64, error) { return n.advance() }}, rev, err)
				case 2:
					p := fmt.Sprintf("g%d/m%d", g, j)
					rev, err := c.AddMount(p, "A", "a1", "")
					record(opRec{kind: "add", apply: func(n *naive) (uint64, error) {
						return n.add(p, "A", "a1", "")
					}}, rev, err)
				case 3:
					p := fmt.Sprintf("g%d/m%d", g, j-2)
					rev, err := c.RemoveMount(p, true)
					record(opRec{kind: "remove", apply: func(n *naive) (uint64, error) {
						return n.remove(p, true)
					}}, rev, err)
				case 4:
					rev, err := c.SetCheckout("m1", "a0")
					record(opRec{kind: "setco", apply: func(n *naive) (uint64, error) {
						return n.setCheckout("m1", "a0")
					}}, rev, err)
				case 5:
					rev, err := c.SetDirty("m2", j%2 == 0)
					record(opRec{kind: "setdirty", apply: func(n *naive) (uint64, error) {
						return n.setDirty("m2", j%2 == 0)
					}}, rev, err)
				case 6:
					if _, err := c.StatusAll(); err != nil {
						t.Errorf("StatusAll: %v", err)
					}
				}
			}
		}(g)
	}
	wg.Wait()

	// 序号即串行顺序：按序号回放成功操作。
	sort.Slice(successes, func(i, j int) bool { return successes[i].rev < successes[j].rev })
	for i, op := range successes {
		if i > 0 && successes[i-1].rev == op.rev {
			t.Fatalf("序号重复: %d", op.rev)
		}
	}
	n := newNaive(cloneRepos(repos), "S", "main")
	if _, err := n.setCheckout("m1", "a1"); err != nil {
		t.Fatal(err)
	}
	if _, err := n.setCheckout("m2", "b0"); err != nil {
		t.Fatal(err)
	}
	for _, op := range successes {
		if _, err := op.apply(n); err != nil {
			t.Fatalf("回放 %s(rev=%d) 在朴素模型上失败: %v", op.kind, op.rev, err)
		}
	}
	gotStatus, err := c.StatusAll()
	if err != nil {
		t.Fatalf("StatusAll: %v", err)
	}
	wantStatus, err := n.statusAll()
	if err != nil {
		t.Fatalf("naive statusAll: %v", err)
	}
	t.Logf("输入=8协程x25混合操作 实际输出=成功操作%d个 终态%v 判定依据=按序号回放后终态与朴素模型一致",
		len(successes), gotStatus)
	if !reflect.DeepEqual(gotStatus, wantStatus) {
		t.Fatalf("终态不一致: 协调器=%v 朴素=%v", gotStatus, wantStatus)
	}
	if c.Revision() != n.revision {
		t.Fatalf("序号不一致: 协调器=%d 朴素=%d", c.Revision(), n.revision)
	}
	if !reflect.DeepEqual(c.SuperTable(), map[string]Record(n.table())) {
		t.Fatalf("超级表不一致: 协调器=%v 朴素=%v", c.SuperTable(), n.table())
	}
	// 同一挂载点的并发更新不得撕裂：最终对齐后检出必须等于固定点。
	if _, err := c.UpdateToPins(); err == nil {
		co, _, _ := c.CheckoutState("m1")
		if pin := c.SuperTable()["m1"].Commit; co != pin {
			t.Fatalf("检出 %s 与固定点 %s 出现未请求的组合", co, pin)
		}
	}
}

// TestRandomizedAgainstNaiveModel 随机挂载树 + 随机操作序列，
// 与独立朴素模型逐步比对。
func TestRandomizedAgainstNaiveModel(t *testing.T) {
	for _, seed := range []int64{7, 42, 2026} {
		t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
			runRandom(t, seed)
		})
	}
}

func runRandom(t *testing.T, seed int64) {
	rng := rand.New(rand.NewSource(seed))
	repoIDs := []string{"S", "R0", "R1", "R2", "R3"}
	pathPool := []string{"m", "n", "o", "p", "q", "x/y"}
	repos := map[string]*Repo{}
	commitIDs := map[string][]string{}
	for _, id := range repoIDs {
		r := NewRepo(id)
		n := 4 + rng.Intn(4)
		for i := 0; i < n; i++ {
			cid := fmt.Sprintf("%sc%d", id, i)
			var parents []string
			for j := 0; j < i; j++ {
				if rng.Intn(2) == 0 {
					parents = append(parents, fmt.Sprintf("%sc%d", id, j))
				}
			}
			var tab Table
			if rng.Intn(10) < 4 {
				tab = Table{}
				perm := rng.Perm(len(pathPool))[:1+rng.Intn(3)]
				for _, pi := range perm {
					target := repoIDs[rng.Intn(len(repoIDs))]
					rec := Record{Repo: target}
					if ids := commitIDs[target]; ids != nil && rng.Intn(10) < 8 {
						rec.Commit = ids[rng.Intn(len(ids))]
					} else {
						rec.Commit = "zz" // 悬空
					}
					switch rng.Intn(4) {
					case 1:
						rec.Track = "main"
					case 2:
						rec.Track = "ghost"
					}
					tab[pathPool[pi]] = rec
				}
			}
			if err := r.AddCommit(cid, parents, tab); err != nil {
				t.Fatal(err)
			}
			commitIDs[id] = append(commitIDs[id], cid)
		}
		if err := r.SetBranch("main", fmt.Sprintf("%sc%d", id, rng.Intn(n))); err != nil {
			t.Fatal(err)
		}
		if rng.Intn(2) == 0 {
			if err := r.SetBranch("dev", fmt.Sprintf("%sc%d", id, rng.Intn(n))); err != nil {
				t.Fatal(err)
			}
		}
		repos[id] = r
	}

	c := mustCoord(t, cloneRepos(repos), "S")
	n := newNaive(cloneRepos(repos), "S", "main")
	t.Logf("输入=种子%d 仓库%d个 判定依据=每步与朴素模型输出一致", seed, len(repos))

	randPath := func() string {
		p := pathPool[rng.Intn(len(pathPool))]
		if rng.Intn(3) == 0 {
			p += "/" + pathPool[rng.Intn(len(pathPool))]
		}
		return p
	}
	errCode := func(err error) ErrCode {
		if err == nil {
			return 0
		}
		return err.(*Error).Code
	}
	checkStatus := func(step int) {
		gs, ge := c.StatusAll()
		ns, ne := n.statusAll()
		if errCode(ge) != errCode(ne) || (ge == nil && !reflect.DeepEqual(gs, ns)) {
			t.Fatalf("步骤%d 状态不一致: 协调器=%v,%v 朴素=%v,%v", step, gs, ge, ns, ne)
		}
	}
	cmp := func(step int, kind string, cre uint64, cerr error, nre uint64, nerr error) {
		t.Logf("步骤=%d 操作=%s 实际输出=序号%d/错误%v 判定依据=与朴素模型(序号%d/错误%v)一致",
			step, kind, cre, errCode(cerr), nre, errCode(nerr))
		if cre != nre || errCode(cerr) != errCode(nerr) {
			t.Fatalf("步骤%d %s 不一致: 协调器=(%d,%v) 朴素=(%d,%v)",
				step, kind, cre, cerr, nre, nerr)
		}
		checkStatus(step)
	}

	for step := 0; step < 300; step++ {
		switch rng.Intn(9) {
		case 0:
			checkStatus(step)
		case 1:
			p := randPath()
			gs, ge := c.StatusAt(p)
			ns, ne := n.statusAt(p)
			if errCode(ge) != errCode(ne) || (ge == nil && gs != ns) {
				t.Fatalf("步骤%d StatusAt(%s): 协调器=%v,%v 朴素=%v,%v", step, p, gs, ge, ns, ne)
			}
		case 2:
			cre, cerr := c.UpdateToPins()
			nre, nerr := n.update()
			cmp(step, "update", cre, cerr, nre, nerr)
		case 3:
			cre, cerr := c.AdvanceTracking()
			nre, nerr := n.advance()
			cmp(step, "advance", cre, cerr, nre, nerr)
		case 4:
			p, rp := randPath(), "ghost"
			if i := rng.Intn(len(repoIDs) + 1); i < len(repoIDs) {
				rp = repoIDs[i]
			}
			com := "zz"
			if ids := commitIDs[rp]; ids != nil && rng.Intn(2) == 0 {
				com = ids[rng.Intn(len(ids))]
			}
			tr := []string{"", "main", "dev", "ghost"}[rng.Intn(4)]
			cre, cerr := c.AddMount(p, rp, com, tr)
			nre, nerr := n.add(p, rp, com, tr)
			cmp(step, "add", cre, cerr, nre, nerr)
		case 5:
			p, f := randPath(), rng.Intn(2) == 0
			cre, cerr := c.RemoveMount(p, f)
			nre, nerr := n.remove(p, f)
			cmp(step, "remove", cre, cerr, nre, nerr)
		case 6:
			p, com := randPath(), fmt.Sprintf("w%d", rng.Intn(3))
			cre, cerr := c.SetCheckout(p, com)
			nre, nerr := n.setCheckout(p, com)
			cmp(step, "setCheckout", cre, cerr, nre, nerr)
		case 7:
			p, d := randPath(), rng.Intn(2) == 0
			cre, cerr := c.SetDirty(p, d)
			nre, nerr := n.setDirty(p, d)
			cmp(step, "setDirty", cre, cerr, nre, nerr)
		case 8:
			p := randPath()
			cre, cerr := c.ClearCheckout(p)
			nre, nerr := n.clearCheckout(p)
			cmp(step, "clearCheckout", cre, cerr, nre, nerr)
		}
	}
	t.Logf("实际输出=300步后序号=%d 判定依据=协调器与朴素模型序号一致", c.Revision())
	if c.Revision() != n.revision {
		t.Fatalf("最终序号不一致: %d vs %d", c.Revision(), n.revision)
	}
}
