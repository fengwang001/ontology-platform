package ontology

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"testing"
)

// TestQuerySnapshotIsolation 验证查询进行中发生的权限变更不影响本次结果：
// 查询必须以开始时刻的权限状态完成计算。
func TestQuerySnapshotIsolation(t *testing.T) {
	reg := NewPermissionRegistry(DenyOverrides)
	reg.UpsertGroup("g", 1)
	reg.AssignPrincipal("p", "g")
	reg.SetLinkTypeDeclaration("g", "L", DeclAllow)
	g := NewGraph()
	g.AddObject("S", "TS")
	g.AddObject("T", "TT")
	mustLink(t, g, "S", "T", "L", 1)
	svc := NewService(reg, g)
	started := make(chan struct{})
	resume := make(chan struct{})
	var once sync.Once
	svc.evalHook = func() {
		once.Do(func() {
			close(started)
			<-resume
		})
	}
	type outcome struct {
		res QueryResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := svc.ShortestPath("p", "S", "T")
		done <- outcome{res, err}
	}()
	<-started
	// 查询已经开始（快照已加载），此时变更权限：拒绝链接类型 L。
	reg.SetLinkTypeDeclaration("g", "L", DeclDeny)
	close(resume)
	out := <-done
	if out.err != nil {
		t.Fatal(out.err)
	}
	if out.res.Status != StatusReachable {
		t.Fatalf("in-flight query status = %v, want reachable (snapshot at start)", out.res.Status)
	}
	// 变更后的新查询必须看到新状态。
	svc.evalHook = nil
	res, err := svc.ShortestPath("p", "S", "T")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusUnreachable {
		t.Fatalf("post-change query status = %v, want unreachable", res.Status)
	}
}

// TestMembershipChangeMidQuery 验证查询进行中主体隶属关系变更不影响本次结果。
func TestMembershipChangeMidQuery(t *testing.T) {
	reg := NewPermissionRegistry(DenyOverrides)
	reg.UpsertGroup("g1", 1)
	reg.UpsertGroup("g2", 1)
	reg.AssignPrincipal("p", "g1")
	reg.SetLinkTypeDeclaration("g1", "L", DeclAllow)
	reg.SetLinkTypeDeclaration("g2", "L", DeclDeny)
	g := NewGraph()
	g.AddObject("S", "TS")
	g.AddObject("T", "TT")
	mustLink(t, g, "S", "T", "L", 1)
	svc := NewService(reg, g)
	started := make(chan struct{})
	resume := make(chan struct{})
	var once sync.Once
	svc.evalHook = func() {
		once.Do(func() {
			close(started)
			<-resume
		})
	}
	done := make(chan QueryResult, 1)
	go func() {
		res, err := svc.ShortestPath("p", "S", "T")
		if err != nil {
			t.Error(err)
		}
		done <- res
	}()
	<-started
	// 查询进行中把主体移出放行组、加入拒绝组。
	reg.UnassignPrincipal("p", "g1")
	reg.AssignPrincipal("p", "g2")
	close(resume)
	if res := <-done; res.Status != StatusReachable {
		t.Fatalf("in-flight query status = %v, want reachable (membership snapshot)", res.Status)
	}
	svc.evalHook = nil
	res, err := svc.ShortestPath("p", "S", "T")
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != StatusUnreachable {
		t.Fatalf("post-change query status = %v, want unreachable", res.Status)
	}
}

// loggedOp 记录一次权限变更操作及其生效版本，用于串行化回放验证。
type loggedOp struct {
	version   uint64
	kind      string
	group     GroupID
	priority  int
	target    string
	value     DeclValue
	principal PrincipalID
}

// applyToNaive 将操作回放到朴素模型。
func (op loggedOp) applyToNaive(st *naiveState) {
	switch op.kind {
	case "upsertGroup":
		st.priorities[op.group] = op.priority
	case "setObject":
		m := st.objectDecls[op.group]
		if m == nil {
			m = make(map[ObjectTypeID]DeclValue)
			st.objectDecls[op.group] = m
		}
		m[ObjectTypeID(op.target)] = op.value
	case "clearObject":
		delete(st.objectDecls[op.group], ObjectTypeID(op.target))
	case "setLink":
		m := st.linkDecls[op.group]
		if m == nil {
			m = make(map[LinkTypeID]DeclValue)
			st.linkDecls[op.group] = m
		}
		m[LinkTypeID(op.target)] = op.value
	case "clearLink":
		delete(st.linkDecls[op.group], LinkTypeID(op.target))
	case "assign":
		m := st.members[op.principal]
		if m == nil {
			m = make(map[GroupID]bool)
			st.members[op.principal] = m
		}
		m[op.group] = true
	case "unassign":
		delete(st.members[op.principal], op.group)
	}
}

// TestConcurrentSerializable 并发执行随机权限变更与路径查询，
// 验证每次查询结果等价于“按版本回放到最后一次变更”的朴素模型结果。
func TestConcurrentSerializable(t *testing.T) {
	const (
		numGroups      = 4
		numObjectTypes = 4
		numLinkTypes   = 3
		numObjects     = 7
		numLinks       = 12
		numMutations   = 60
		numQueries     = 40
	)
	rng := rand.New(rand.NewSource(20261007))

	reg := NewPermissionRegistry(DenyOverrides)
	g := NewGraph()
	for i := 0; i < numObjects; i++ {
		g.AddObject(ObjectID(fmt.Sprintf("O%d", i)), ObjectTypeID(fmt.Sprintf("OT%d", i%numObjectTypes)))
	}
	for i := 0; i < numLinks; i++ {
		from := ObjectID(fmt.Sprintf("O%d", rng.Intn(numObjects)))
		to := ObjectID(fmt.Sprintf("O%d", rng.Intn(numObjects)))
		if from == to {
			continue
		}
		mustLink(t, g, from, to, LinkTypeID(fmt.Sprintf("LT%d", rng.Intn(numLinkTypes))), int64(1+rng.Intn(9)))
	}
	svc := NewService(reg, g)

	// 朴素图视图（与 Graph 内容一致）。
	ng := &naiveGraph{types: make(map[ObjectID]ObjectTypeID), adj: make(map[ObjectID][]Link)}
	for i := 0; i < numObjects; i++ {
		id := ObjectID(fmt.Sprintf("O%d", i))
		ng.types[id] = ObjectTypeID(fmt.Sprintf("OT%d", i%numObjectTypes))
		for _, l := range g.outgoing(id) {
			ng.adj[id] = append(ng.adj[id], l)
		}
	}

	principals := []PrincipalID{"p0", "p1", "p2"}

	var mu sync.Mutex
	var ops []loggedOp
	record := func(op loggedOp, version uint64) {
		mu.Lock()
		op.version = version
		ops = append(ops, op)
		mu.Unlock()
	}

	var wg sync.WaitGroup
	// 并发变更。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(seed int64) {
			defer wg.Done()
			r := rand.New(rand.NewSource(seed))
			for i := 0; i < numMutations/4; i++ {
				group := GroupID(fmt.Sprintf("g%d", r.Intn(numGroups)))
				switch r.Intn(7) {
				case 0:
					prio := r.Intn(4)
					v := reg.UpsertGroup(group, prio)
					record(loggedOp{kind: "upsertGroup", group: group, priority: prio}, v)
				case 1, 2:
					target := fmt.Sprintf("OT%d", r.Intn(numObjectTypes))
					val := DeclAllow
					if r.Intn(2) == 0 {
						val = DeclDeny
					}
					v := reg.SetObjectTypeDeclaration(group, ObjectTypeID(target), val)
					record(loggedOp{kind: "setObject", group: group, target: target, value: val}, v)
				case 3, 4:
					target := fmt.Sprintf("LT%d", r.Intn(numLinkTypes))
					val := DeclAllow
					if r.Intn(2) == 0 {
						val = DeclDeny
					}
					v := reg.SetLinkTypeDeclaration(group, LinkTypeID(target), val)
					record(loggedOp{kind: "setLink", group: group, target: target, value: val}, v)
				case 5:
					p := principals[r.Intn(len(principals))]
					v := reg.AssignPrincipal(p, group)
					record(loggedOp{kind: "assign", principal: p, group: group}, v)
				case 6:
					p := principals[r.Intn(len(principals))]
					v := reg.UnassignPrincipal(p, group)
					record(loggedOp{kind: "unassign", principal: p, group: group}, v)
				}
			}
		}(int64(w)*7919 + 13)
	}

	// 并发查询。
	type queryRecord struct {
		principal PrincipalID
		from, to  ObjectID
		res       QueryResult
	}
	results := make([]queryRecord, numQueries)
	for w := 0; w < numQueries; w++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			r := rand.New(rand.NewSource(int64(idx)*104729 + 7))
			p := principals[r.Intn(len(principals))]
			from := ObjectID(fmt.Sprintf("O%d", r.Intn(numObjects)))
			to := ObjectID(fmt.Sprintf("O%d", r.Intn(numObjects)))
			res, err := svc.ShortestPath(p, from, to)
			if err != nil {
				t.Error(err)
				return
			}
			results[idx] = queryRecord{principal: p, from: from, to: to, res: res}
		}(w)
	}
	wg.Wait()

	// 版本号必须连续，证明全部变更被串行化。
	sort.Slice(ops, func(i, j int) bool { return ops[i].version < ops[j].version })
	for i, op := range ops {
		if op.version != uint64(i+1) {
			t.Fatalf("versions not contiguous: ops[%d].version = %d", i, op.version)
		}
	}

	// 逐查询回放验证：结果必须等于“查询版本之前最后一次变更”的状态。
	for _, qr := range results {
		st := newNaiveState(DenyOverrides)
		for _, op := range ops {
			if op.version > qr.res.snapshotVersion {
				break
			}
			op.applyToNaive(st)
		}
		want := st.naiveShortestPath(qr.principal, ng, qr.from, qr.to)
		if qr.res.Status != want.status {
			t.Errorf("query %s %s->%s @v%d: status = %v, naive = %v",
				qr.principal, qr.from, qr.to, qr.res.snapshotVersion, qr.res.Status, want.status)
			continue
		}
		if qr.res.Status == StatusReachable {
			if fmt.Sprint(qr.res.Path) != fmt.Sprint(want.path) || qr.res.Cost != want.cost {
				t.Errorf("query %s %s->%s @v%d: path = %v cost %d, naive = %v cost %d",
					qr.principal, qr.from, qr.to, qr.res.snapshotVersion,
					qr.res.Path, qr.res.Cost, want.path, want.cost)
			}
		}
		// 逐步核对每条候选链接的判定与朴素模型一致。
		for _, td := range qr.res.Trace {
			naive := st.naiveAuthorize(qr.principal, td.Link, ng.types[td.Link.From], ng.types[td.Link.To])
			if td.Decision != naive {
				t.Errorf("query %s link %+v: decision = %v, naive = %v", qr.principal, td.Link, td.Decision, naive)
			}
		}
	}
}
