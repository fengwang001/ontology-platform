package ontology

import (
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"
)

// randomInstance 是一次随机生成的查询实例。
type randomInstance struct {
	seed      int64
	policy    ConflictPolicy
	principal PrincipalID
	from, to  ObjectID
	service   *Service
	naive     *naiveState
	graph     *naiveGraph
	groups    []GroupID
}

// genRandomInstance 生成随机权限组、声明、隶属关系与链接图。
func genRandomInstance(t *testing.T, seed int64, policy ConflictPolicy) *randomInstance {
	t.Helper()
	r := rand.New(rand.NewSource(seed))

	reg := NewPermissionRegistry(policy)
	st := newNaiveState(policy)

	numGroups := 1 + r.Intn(5)
	groups := make([]GroupID, numGroups)
	for i := range groups {
		groups[i] = GroupID(fmt.Sprintf("g%d", i))
		prio := r.Intn(4) // 小范围优先级，制造同优先级冲突
		reg.UpsertGroup(groups[i], prio)
		st.priorities[groups[i]] = prio
	}

	numObjTypes := 3
	numLinkTypes := 3
	// 随机对象类型层声明。
	for _, g := range groups {
		for i := 0; i < numObjTypes; i++ {
			if r.Intn(2) == 0 {
				continue
			}
			ot := ObjectTypeID(fmt.Sprintf("OT%d", i))
			v := DeclAllow
			if r.Intn(2) == 0 {
				v = DeclDeny
			}
			reg.SetObjectTypeDeclaration(g, ot, v)
			if st.objectDecls[g] == nil {
				st.objectDecls[g] = make(map[ObjectTypeID]DeclValue)
			}
			st.objectDecls[g][ot] = v
		}
		// 随机链接类型层声明（含未声明）。
		for i := 0; i < numLinkTypes; i++ {
			if r.Intn(2) == 0 {
				continue
			}
			lt := LinkTypeID(fmt.Sprintf("LT%d", i))
			v := DeclAllow
			if r.Intn(2) == 0 {
				v = DeclDeny
			}
			reg.SetLinkTypeDeclaration(g, lt, v)
			if st.linkDecls[g] == nil {
				st.linkDecls[g] = make(map[LinkTypeID]DeclValue)
			}
			st.linkDecls[g][lt] = v
		}
	}

	// 主体随机构成权限组集合。
	principal := PrincipalID("p")
	for _, g := range groups {
		if r.Intn(2) == 0 {
			continue
		}
		reg.AssignPrincipal(principal, g)
		if st.members[principal] == nil {
			st.members[principal] = make(map[GroupID]bool)
		}
		st.members[principal][g] = true
	}

	// 随机图。
	numObjects := 4 + r.Intn(4)
	graph := NewGraph()
	ng := &naiveGraph{types: make(map[ObjectID]ObjectTypeID), adj: make(map[ObjectID][]Link)}
	for i := 0; i < numObjects; i++ {
		id := ObjectID(fmt.Sprintf("O%d", i))
		ot := ObjectTypeID(fmt.Sprintf("OT%d", r.Intn(numObjTypes)))
		graph.AddObject(id, ot)
		ng.types[id] = ot
	}
	numLinks := 4 + r.Intn(9)
	for i := 0; i < numLinks; i++ {
		from := ObjectID(fmt.Sprintf("O%d", r.Intn(numObjects)))
		to := ObjectID(fmt.Sprintf("O%d", r.Intn(numObjects)))
		if from == to {
			continue
		}
		link := Link{
			From: from,
			To:   to,
			Type: LinkTypeID(fmt.Sprintf("LT%d", r.Intn(numLinkTypes))),
			Cost: int64(1 + r.Intn(9)),
		}
		if err := graph.AddLink(link); err != nil {
			t.Fatal(err)
		}
		ng.adj[from] = append(ng.adj[from], link)
	}

	from := ObjectID(fmt.Sprintf("O%d", r.Intn(numObjects)))
	to := ObjectID(fmt.Sprintf("O%d", r.Intn(numObjects)))
	return &randomInstance{
		seed:      seed,
		policy:    policy,
		principal: principal,
		from:      from,
		to:        to,
		service:   NewService(reg, graph),
		naive:     st,
		graph:     ng,
		groups:    groups,
	}
}

// queryLogRecord 是一次查询的完整记录：输入、输出与每步判定依据。
type queryLogRecord struct {
	Seed      int64          `json:"seed"`
	Policy    string         `json:"policy"`
	Principal PrincipalID    `json:"principal"`
	Groups    []GroupID      `json:"groups"`
	From      ObjectID       `json:"from"`
	To        ObjectID       `json:"to"`
	Status    string         `json:"status"`
	Path      []ObjectID     `json:"path,omitempty"`
	Cost      int64          `json:"cost"`
	Naive     string         `json:"naiveStatus"`
	Steps     []stepLogEntry `json:"steps"`
}

// stepLogEntry 是一条候选链接的判定记录。
type stepLogEntry struct {
	Link     Link            `json:"link"`
	Decision string          `json:"decision"`
	Bases    []DecisionBasis `json:"bases"`
	Naive    string          `json:"naiveDecision"`
}

// queryLogger 把查询记录追加写入 JSONL 文件。
type queryLogger struct {
	f *os.File
}

func newQueryLogger(t *testing.T) *queryLogger {
	t.Helper()
	dir := os.Getenv("ONTOLOGY_QUERYLOG_DIR")
	if dir == "" {
		dir = t.TempDir()
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create query log dir: %v", err)
	}
	path := filepath.Join(dir, "querylog.jsonl")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open query log: %v", err)
	}
	t.Cleanup(func() { f.Close() })
	t.Logf("查询判定依据日志: %s", path)
	return &queryLogger{f: f}
}

func (l *queryLogger) log(t *testing.T, rec queryLogRecord) {
	t.Helper()
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal query log: %v", err)
	}
	if _, err := l.f.Write(append(data, '\n')); err != nil {
		t.Fatalf("write query log: %v", err)
	}
}

// checkTraceAgainstNaive 核对主实现每一步判定与朴素模型一致，
// 并核对判定依据（层、合并结果）与朴素分层求值一致。
func checkTraceAgainstNaive(t *testing.T, inst *randomInstance, res QueryResult) {
	t.Helper()
	for _, td := range res.Trace {
		link := td.Link
		fromType := inst.graph.types[link.From]
		toType := inst.graph.types[link.To]
		want := inst.naive.naiveAuthorize(inst.principal, link, fromType, toType)
		if td.Decision != want {
			t.Fatalf("seed %d link %+v: decision = %v, naive = %v",
				inst.seed, link, td.Decision, want)
		}
		// 核对判定依据结构：链接层声明存在时应只有链接层依据。
		linkDeclared, linkD := inst.naive.naiveEvalLayer(inst.principal, true, string(link.Type))
		if linkDeclared {
			if len(td.Bases) != 1 || td.Bases[0].Layer != LayerLink {
				t.Fatalf("seed %d link %+v: bases = %+v, want single link-layer basis",
					inst.seed, link, td.Bases)
			}
			if td.Bases[0].Merged != linkD {
				t.Fatalf("seed %d link %+v: link basis merged = %v, naive = %v",
					inst.seed, link, td.Bases[0].Merged, linkD)
			}
			continue
		}
		fromDeclared, _ := inst.naive.naiveEvalLayer(inst.principal, false, string(fromType))
		toDeclared, _ := inst.naive.naiveEvalLayer(inst.principal, false, string(toType))
		if !fromDeclared && !toDeclared {
			if len(td.Bases) != 1 || td.Bases[0].Layer != LayerDefault {
				t.Fatalf("seed %d link %+v: bases = %+v, want default-deny basis",
					inst.seed, link, td.Bases)
			}
			if td.Decision != DecisionDeny {
				t.Fatalf("seed %d link %+v: decision = %v, want default deny",
					inst.seed, link, td.Decision)
			}
			continue
		}
		for _, b := range td.Bases {
			if b.Layer != LayerObject {
				t.Fatalf("seed %d link %+v: unexpected basis layer %v", inst.seed, link, b.Layer)
			}
			declared, d := inst.naive.naiveEvalLayer(inst.principal, false, string(b.ObjectType))
			if !declared || b.Merged != d {
				t.Fatalf("seed %d link %+v: object basis %s merged = %v, naive = %v (declared=%v)",
					inst.seed, link, b.ObjectType, b.Merged, d, declared)
			}
		}
	}
}

// TestRandomizedCrossCheck 在随机实例上对照主实现与朴素模型的最终结果，
// 并记录每次查询的输入、输出与每步判定依据。
func TestRandomizedCrossCheck(t *testing.T) {
	logger := newQueryLogger(t)
	const numSeeds = 300
	for seed := int64(1); seed <= numSeeds; seed++ {
		inst := genRandomInstance(t, seed, DenyOverrides)
		res, err := inst.service.ShortestPath(inst.principal, inst.from, inst.to)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		want := inst.naive.naiveShortestPath(inst.principal, inst.graph, inst.from, inst.to)

		rec := queryLogRecord{
			Seed:      seed,
			Policy:    "deny-overrides",
			Principal: inst.principal,
			Groups:    inst.naive.sortedGroups(inst.principal),
			From:      inst.from,
			To:        inst.to,
			Status:    res.Status.String(),
			Path:      res.Path,
			Cost:      res.Cost,
			Naive:     want.status.String(),
		}
		for _, td := range res.Trace {
			naiveD := inst.naive.naiveAuthorize(inst.principal, td.Link,
				inst.graph.types[td.Link.From], inst.graph.types[td.Link.To])
			rec.Steps = append(rec.Steps, stepLogEntry{
				Link:     td.Link,
				Decision: td.Decision.String(),
				Bases:    td.Bases,
				Naive:    naiveD.String(),
			})
		}
		logger.log(t, rec)

		if res.Status != want.status {
			t.Fatalf("seed %d: status = %v, naive = %v", seed, res.Status, want.status)
		}
		if res.Status == StatusReachable {
			if fmt.Sprint(res.Path) != fmt.Sprint(want.path) {
				t.Fatalf("seed %d: path = %v, naive = %v", seed, res.Path, want.path)
			}
			if res.Cost != want.cost {
				t.Fatalf("seed %d: cost = %d, naive = %d", seed, res.Cost, want.cost)
			}
		}
		checkTraceAgainstNaive(t, inst, res)
	}
}

// TestRandomizedStrictConflict 在严格策略随机实例上核对：
// 每步判定与朴素模型一致；主实现报歧义当且仅当搜索过程中遇到
// 朴素模型同样判定为歧义的链接。
func TestRandomizedStrictConflict(t *testing.T) {
	logger := newQueryLogger(t)
	const numSeeds = 200
	sawAmbiguous := false
	for seed := int64(1000); seed < 1000+numSeeds; seed++ {
		inst := genRandomInstance(t, seed, StrictConflict)
		res, err := inst.service.ShortestPath(inst.principal, inst.from, inst.to)
		if err != nil {
			t.Fatalf("seed %d: %v", seed, err)
		}
		checkTraceAgainstNaive(t, inst, res)

		rec := queryLogRecord{
			Seed:      seed,
			Policy:    "strict-conflict",
			Principal: inst.principal,
			Groups:    inst.naive.sortedGroups(inst.principal),
			From:      inst.from,
			To:        inst.to,
			Status:    res.Status.String(),
			Path:      res.Path,
			Cost:      res.Cost,
		}
		traceHasAmbiguous := false
		for _, td := range res.Trace {
			if td.Decision == DecisionAmbiguous {
				traceHasAmbiguous = true
			}
			naiveD := inst.naive.naiveAuthorize(inst.principal, td.Link,
				inst.graph.types[td.Link.From], inst.graph.types[td.Link.To])
			rec.Steps = append(rec.Steps, stepLogEntry{
				Link:     td.Link,
				Decision: td.Decision.String(),
				Bases:    td.Bases,
				Naive:    naiveD.String(),
			})
		}
		logger.log(t, rec)

		if res.Status == StatusAmbiguous {
			sawAmbiguous = true
			if !traceHasAmbiguous {
				t.Fatalf("seed %d: status ambiguous but no ambiguous trace entry", seed)
			}
		} else if traceHasAmbiguous {
			t.Fatalf("seed %d: status %v but trace contains ambiguous entry", seed, res.Status)
		}
	}
	if !sawAmbiguous {
		t.Log("注意：本轮随机实例未产生歧义结果（同优先级矛盾未出现在搜索路径上）")
	}
}
