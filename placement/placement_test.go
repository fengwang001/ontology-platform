package placement

import (
	"reflect"
	"testing"
)

func mustAdd(t *testing.T, f *Filter, name, zone string) {
	t.Helper()
	if err := f.AddNode(Node{Name: name, Zone: zone}); err != nil {
		t.Fatalf("AddNode(%s): %v", name, err)
	}
}

func mustPlace(t *testing.T, f *Filter, x Pod, n string) {
	t.Helper()
	if err := f.Place(x, n); err != nil {
		t.Fatalf("Place(%s on %s): %v", x.ID, n, err)
	}
}

func feasibleIDs(t *testing.T, f *Filter, x Pod) []string {
	t.Helper()
	got, err := f.Feasible(x)
	if err != nil {
		t.Fatalf("Feasible(%s): %v", x.ID, err)
	}
	return got
}

func dbPod(id string, min int, topo TopologyKey) Pod {
	return Pod{
		ID:     id,
		Labels: map[string]string{"app": "web"},
		Affinity: []AffinityTerm{{
			Selector:    Selector{"app": "db"},
			Topology:    topo,
			MinRequired: min,
		}},
	}
}

func reasonOf(err error) Reason {
	if err == nil {
		return ""
	}
	return err.(*PlacementError).Reason
}

func asPErr(t *testing.T, err error) *PlacementError {
	t.Helper()
	pe, ok := err.(*PlacementError)
	if !ok {
		t.Fatalf("expected *PlacementError, got %T: %v", err, err)
	}
	return pe
}

// 题目示例：zone 级亲和 m=2，两个 db 在 zone a 的两个节点；只剩一个时不豁免。
func TestExampleZoneAffinity(t *testing.T) {
	f := NewFilter(10)
	mustAdd(t, f, "n1", "a")
	mustAdd(t, f, "n2", "a")
	mustAdd(t, f, "n3", "b")
	mustPlace(t, f, Pod{ID: "db1", Labels: map[string]string{"app": "db"}}, "n1")
	mustPlace(t, f, Pod{ID: "db2", Labels: map[string]string{"app": "db"}}, "n2")

	if got := feasibleIDs(t, f, dbPod("w1", 2, TopologyZone)); !reflect.DeepEqual(got, []string{"n1", "n2"}) {
		t.Fatalf("got %v want [n1 n2]", got)
	}

	if err := f.Remove("db2"); err != nil {
		t.Fatal(err)
	}
	if got := feasibleIDs(t, f, dbPod("w1", 2, TopologyZone)); len(got) != 0 {
		t.Fatalf("got %v want []", got)
	}
}

// 首个成员豁免；全集群已有匹配（数量不足/在别的域）时不豁免；Remove 后恢复。
func TestFirstMemberExemption(t *testing.T) {
	f := NewFilter(10)
	mustAdd(t, f, "n1", "a")
	mustAdd(t, f, "n2", "b")

	first := Pod{ID: "db0", Labels: map[string]string{"app": "db"},
		Affinity: []AffinityTerm{{Selector: Selector{"app": "db"}, Topology: TopologyZone, MinRequired: 2}}}
	if got := feasibleIDs(t, f, first); !reflect.DeepEqual(got, []string{"n1", "n2"}) {
		t.Fatalf("exemption feasible = %v", got)
	}
	mustPlace(t, f, first, "n1")

	second := Pod{ID: "db1", Labels: map[string]string{"app": "db"},
		Affinity: []AffinityTerm{{Selector: Selector{"app": "db"}, Topology: TopologyZone, MinRequired: 2}}}
	if got := feasibleIDs(t, f, second); len(got) != 0 {
		t.Fatalf("want no feasible node, got %v", got)
	}

	if err := f.Remove("db0"); err != nil {
		t.Fatal(err)
	}
	second.ID = "db2"
	if got := feasibleIDs(t, f, second); !reflect.DeepEqual(got, []string{"n1", "n2"}) {
		t.Fatalf("exemption after remove = %v", got)
	}
}

// 预留对豁免/计数即时可见，Cancel 全部匹配者后豁免恢复。
func TestExemptionAfterCancel(t *testing.T) {
	f := NewFilter(10)
	mustAdd(t, f, "n1", "a")
	mustAdd(t, f, "n2", "b")
	r := Pod{ID: "db9", Labels: map[string]string{"app": "db"}}
	if err := f.Reserve(r, "n1"); err != nil {
		t.Fatal(err)
	}
	x := Pod{ID: "w", Labels: map[string]string{"app": "db"},
		Affinity: []AffinityTerm{{Selector: Selector{"app": "db"}, Topology: TopologyZone, MinRequired: 1}}}
	if got := feasibleIDs(t, f, x); !reflect.DeepEqual(got, []string{"n1"}) {
		t.Fatalf("reserved visible: %v", got)
	}
	if err := f.Cancel("db9"); err != nil {
		t.Fatal(err)
	}
	if got := feasibleIDs(t, f, x); !reflect.DeepEqual(got, []string{"n1", "n2"}) {
		t.Fatalf("after cancel: %v", got)
	}
}

// 多条亲和逐项独立豁免；m=1 与 m>1 的同域取等边界。
func TestMultipleAffinityAndEquality(t *testing.T) {
	f := NewFilter(10)
	mustAdd(t, f, "n1", "a")
	mustAdd(t, f, "n2", "a")
	mustAdd(t, f, "n3", "b")
	mustPlace(t, f, Pod{ID: "cache1", Labels: map[string]string{"app": "cache"}}, "n1")

	// 项0 需要 db：全集群无匹配且自身是 db，逐项独立豁免；
	// 项1 需要同 node 1 个 cache：仅 n1。
	x := Pod{ID: "x", Labels: map[string]string{"app": "db"}, Affinity: []AffinityTerm{
		{Selector: Selector{"app": "db"}, Topology: TopologyZone, MinRequired: 2},
		{Selector: Selector{"app": "cache"}, Topology: TopologyNode, MinRequired: 1},
	}}
	if got := feasibleIDs(t, f, x); !reflect.DeepEqual(got, []string{"n1"}) {
		t.Fatalf("independent exemption: %v", got)
	}

	eq1 := Pod{ID: "e1", Affinity: []AffinityTerm{
		{Selector: Selector{"app": "cache"}, Topology: TopologyNode, MinRequired: 1}}}
	if got := feasibleIDs(t, f, eq1); !reflect.DeepEqual(got, []string{"n1"}) {
		t.Fatalf("m=1 equality: %v", got)
	}
	eq2 := Pod{ID: "e2", Affinity: []AffinityTerm{
		{Selector: Selector{"app": "cache"}, Topology: TopologyZone, MinRequired: 2}}}
	if got := feasibleIDs(t, f, eq2); len(got) != 0 {
		t.Fatalf("m=2 one short, not exempt (cluster has match): %v", got)
	}
	// 同域恰好 2 个即取等满足。
	mustPlace(t, f, Pod{ID: "cache2", Labels: map[string]string{"app": "cache"}}, "n2")
	if got := feasibleIDs(t, f, eq2); !reflect.DeepEqual(got, []string{"n1", "n2"}) {
		t.Fatalf("m=2 equality zone: %v", got)
	}
}

// 预留即时可见；Q 恰满与超出一个；Place 不占额度。
func TestReservationVisibilityAndQuota(t *testing.T) {
	f := NewFilter(2)
	mustAdd(t, f, "n1", "a")
	mustAdd(t, f, "n2", "b")

	if err := f.Reserve(Pod{ID: "b1", AntiAffinity: []AntiAffinityTerm{
		{Selector: Selector{"app": "x"}, Topology: TopologyNode}}}, "n1"); err != nil {
		t.Fatal(err)
	}
	victim := Pod{ID: "v1", Labels: map[string]string{"app": "x"}}
	if got := feasibleIDs(t, f, victim); !reflect.DeepEqual(got, []string{"n2"}) {
		t.Fatalf("reservation anti-affinity: %v", got)
	}

	if err := f.Reserve(Pod{ID: "r2"}, "n1"); err != nil {
		t.Fatal(err)
	}
	if pe := asPErr(t, f.Reserve(Pod{ID: "r3"}, "n1")); pe.Reason != ReasonReservationFull {
		t.Fatalf("want reservation_full")
	}

	// Place 不占预留额度：预留已满仍可 Place。
	if err := f.Place(Pod{ID: "p1"}, "n2"); err != nil {
		t.Fatalf("place while reservations full: %v", err)
	}
	// 已提交的 p1 同样参与排斥。
	if got := feasibleIDs(t, f, victim); !reflect.DeepEqual(got, []string{"n2"}) {
		t.Fatalf("p1 has no anti-affinity; n2 stays feasible: %v", got)
	}

	if err := f.Commit("r2"); err != nil {
		t.Fatal(err)
	}
	if err := f.Reserve(Pod{ID: "r4"}, "n2"); err != nil {
		t.Fatalf("reserve after commit frees quota: %v", err)
	}
}

// node/zone 同域判定与空选择器。
func TestTopologyAndEmptySelector(t *testing.T) {
	f := NewFilter(10)
	mustAdd(t, f, "n1", "a")
	mustAdd(t, f, "n2", "a")
	mustAdd(t, f, "n3", "b")
	mustPlace(t, f, Pod{ID: "z1"}, "n1")

	nodeAnti := Pod{ID: "na", AntiAffinity: []AntiAffinityTerm{
		{Selector: Selector{}, Topology: TopologyNode}}}
	if got := feasibleIDs(t, f, nodeAnti); !reflect.DeepEqual(got, []string{"n2", "n3"}) {
		t.Fatalf("node topology: %v", got)
	}
	zoneAnti := Pod{ID: "za", AntiAffinity: []AntiAffinityTerm{
		{Selector: Selector{}, Topology: TopologyZone}}}
	if got := feasibleIDs(t, f, zoneAnti); !reflect.DeepEqual(got, []string{"n3"}) {
		t.Fatalf("zone topology: %v", got)
	}
	zoneAff := Pod{ID: "zf", Affinity: []AffinityTerm{
		{Selector: Selector{}, Topology: TopologyZone, MinRequired: 1}}}
	if got := feasibleIDs(t, f, zoneAff); !reflect.DeepEqual(got, []string{"n1", "n2"}) {
		t.Fatalf("zone affinity empty selector: %v", got)
	}
}

// 对称排斥：已有 Pod 的反亲和排斥新 Pod；亲和不对称；自身不自我排斥。
func TestSymmetricRejection(t *testing.T) {
	f := NewFilter(10)
	mustAdd(t, f, "n1", "a")
	mustAdd(t, f, "n2", "a")
	mustPlace(t, f, Pod{ID: "q", AntiAffinity: []AntiAffinityTerm{
		{Selector: Selector{"app": "x"}, Topology: TopologyZone}}}, "n1")
	x := Pod{ID: "x", Labels: map[string]string{"app": "x"}}
	if got := feasibleIDs(t, f, x); len(got) != 0 {
		t.Fatalf("symmetric reject whole zone: %v", got)
	}
	pe := asPErr(t, f.Place(x, "n1"))
	if pe.Reason != ReasonRejectedByExistingPod || pe.BlockerID != "q" {
		t.Fatalf("want rejected by q, got %+v", pe)
	}

	// 亲和不对称：他者的亲和项不影响新 Pod。
	f2 := NewFilter(10)
	mustAdd(t, f2, "n1", "a")
	if err := f2.Place(Pod{ID: "a", Affinity: []AffinityTerm{
		{Selector: Selector{"app": "db"}, Topology: TopologyNode, MinRequired: 1}}}, "n1"); reasonOf(err) != ReasonAffinityNotSatisfied {
		t.Fatalf("a should fail its own affinity, got %v", err)
	}
	if err := f2.Place(Pod{ID: "free"}, "n1"); err != nil {
		t.Fatalf("other pod affinity is not symmetric: %v", err)
	}

	// 自身匹配自身选择器：第一个可放置，Relabel 保持匹配也不自我冲突。
	f3 := NewFilter(10)
	mustAdd(t, f3, "n1", "a")
	self := Pod{ID: "self", Labels: map[string]string{"app": "s"},
		AntiAffinity: []AntiAffinityTerm{{Selector: Selector{"app": "s"}, Topology: TopologyNode}}}
	if err := f3.Place(self, "n1"); err != nil {
		t.Fatalf("self selector must not block first placement: %v", err)
	}
	if err := f3.Relabel("self", map[string]string{"app": "s", "v": "2"}); err != nil {
		t.Fatalf("relabel keeping own selector must not self-conflict: %v", err)
	}
}

// Relabel：引入被排斥标签则拒绝（携带最小排斥者），原标签不变；
// 换到别的域后允许；Relabel 后豁免状态随之变化。
func TestRelabel(t *testing.T) {
	f := NewFilter(10)
	mustAdd(t, f, "n1", "a")
	mustAdd(t, f, "n2", "a")
	mustAdd(t, f, "n3", "b")
	mustPlace(t, f, Pod{ID: "aaa", AntiAffinity: []AntiAffinityTerm{
		{Selector: Selector{"app": "x"}, Topology: TopologyZone}}}, "n1")
	mustPlace(t, f, Pod{ID: "zzz", AntiAffinity: []AntiAffinityTerm{
		{Selector: Selector{"app": "x"}, Topology: TopologyZone}}}, "n2")
	mustPlace(t, f, Pod{ID: "target"}, "n2")

	pe := asPErr(t, f.Relabel("target", map[string]string{"app": "x"}))
	if pe.Reason != ReasonRejectedByExistingPod || pe.BlockerID != "aaa" {
		t.Fatalf("want rejected by aaa, got %+v", pe)
	}
	// 被拒绝不改状态：换成不冲突标签应成功。
	if err := f.Relabel("target", map[string]string{"app": "other"}); err != nil {
		t.Fatalf("state unchanged after rejected relabel: %v", err)
	}

	// 新的 app=x Pod 只能落在别的域 zone b（n3）。
	if got := feasibleIDs(t, f, Pod{ID: "moved", Labels: map[string]string{"app": "x"}}); !reflect.DeepEqual(got, []string{"n3"}) {
		t.Fatalf("x only feasible in zone b: %v", got)
	}

	// Relabel 后豁免状态变化。
	f2 := NewFilter(10)
	mustAdd(t, f2, "n1", "a")
	mustPlace(t, f2, Pod{ID: "t"}, "n1")
	needDB := Pod{ID: "nd", Labels: map[string]string{"app": "db"}, Affinity: []AffinityTerm{
		{Selector: Selector{"app": "db"}, Topology: TopologyZone, MinRequired: 2}}}
	if got := feasibleIDs(t, f2, needDB); !reflect.DeepEqual(got, []string{"n1"}) {
		t.Fatalf("exempt before relabel: %v", got)
	}
	if err := f2.Relabel("t", map[string]string{"app": "db"}); err != nil {
		t.Fatal(err)
	}
	if got := feasibleIDs(t, f2, needDB); len(got) != 0 {
		t.Fatalf("no longer exempt once a db exists: %v", got)
	}
	if err := f2.Relabel("t", map[string]string{"app": "other"}); err != nil {
		t.Fatal(err)
	}
	if got := feasibleIDs(t, f2, needDB); !reflect.DeepEqual(got, []string{"n1"}) {
		t.Fatalf("exempt again: %v", got)
	}
}

// 拒绝原因先后与携带信息；被拒绝操作不改状态。
func TestRejectionOrder(t *testing.T) {
	f := NewFilter(1)
	mustAdd(t, f, "n1", "a")

	// 参数非法最先于 Pod 已存在。
	mustPlace(t, f, Pod{ID: "dup"}, "n1")
	if pe := asPErr(t, f.Place(Pod{ID: "dup", Labels: map[string]string{"": "v"}}, "n1")); pe.Reason != ReasonInvalidArgument {
		t.Fatalf("invalid before exists")
	}
	if pe := asPErr(t, f.Place(Pod{ID: "dup"}, "n1")); pe.Reason != ReasonPodExists {
		t.Fatalf("exists before node")
	}
	if pe := asPErr(t, f.Place(Pod{ID: "new"}, "ghost")); pe.Reason != ReasonNodeNotFound {
		t.Fatalf("node not found")
	}

	// 亲和先于反亲和：同节点两者都不满足时报亲和（带第一项下标）。
	mustPlace(t, f, Pod{ID: "blocker", Labels: map[string]string{"app": "x"},
		AntiAffinity: []AntiAffinityTerm{{Selector: Selector{"app": "x"}, Topology: TopologyNode}}}, "n1")
	bad := Pod{ID: "bad", Labels: map[string]string{"app": "x"},
		Affinity:     []AffinityTerm{{Selector: Selector{"app": "db"}, Topology: TopologyNode, MinRequired: 1}},
		AntiAffinity: []AntiAffinityTerm{{Selector: Selector{"app": "x"}, Topology: TopologyNode}}}
	pe := asPErr(t, f.Place(bad, "n1"))
	if pe.Reason != ReasonAffinityNotSatisfied || pe.TermIndex != 0 {
		t.Fatalf("affinity first with index, got %+v", pe)
	}

	// 去掉亲和后报第一条反亲和冲突，携带最小阻挡者。
	bad.Affinity = nil
	pe = asPErr(t, f.Place(bad, "n1"))
	if pe.Reason != ReasonAntiAffinityConflict || pe.TermIndex != 0 || pe.BlockerID != "blocker" {
		t.Fatalf("anti-affinity with blocker, got %+v", pe)
	}

	// 第一条不冲突、第二条冲突时 index=1。
	f2 := NewFilter(10)
	mustAdd(t, f2, "n1", "a")
	mustPlace(t, f2, Pod{ID: "y", Labels: map[string]string{"app": "y"}}, "n1")
	two := Pod{ID: "two", AntiAffinity: []AntiAffinityTerm{
		{Selector: Selector{"app": "nope"}, Topology: TopologyNode},
		{Selector: Selector{"app": "y"}, Topology: TopologyNode}}}
	pe = asPErr(t, f2.Place(two, "n1"))
	if pe.Reason != ReasonAntiAffinityConflict || pe.TermIndex != 1 || pe.BlockerID != "y" {
		t.Fatalf("first conflicting term index, got %+v", pe)
	}

	// 预留已满最后判。
	f3 := NewFilter(1)
	mustAdd(t, f3, "n1", "a")
	if err := f3.Reserve(Pod{ID: "q"}, "n1"); err != nil {
		t.Fatal(err)
	}
	if pe := asPErr(t, f3.Reserve(Pod{ID: "q2"}, "n1")); pe.Reason != ReasonReservationFull {
		t.Fatalf("quota checked last, got %+v", pe)
	}
}

// 非法参数的各种形态。
func TestInvalidArguments(t *testing.T) {
	f := NewFilter(10)
	cases := []Pod{
		{ID: "", Labels: map[string]string{"a": "b"}},
		{ID: "p", Labels: map[string]string{"": "b"}},
		{ID: "p", Affinity: []AffinityTerm{{Selector: Selector{"": "v"}, Topology: TopologyNode, MinRequired: 1}}},
		{ID: "p", Affinity: []AffinityTerm{{Selector: Selector{}, Topology: "bad", MinRequired: 1}}},
		{ID: "p", Affinity: []AffinityTerm{{Selector: Selector{}, Topology: TopologyNode, MinRequired: 0}}},
		{ID: "p", Affinity: []AffinityTerm{{Selector: Selector{}, Topology: TopologyNode, MinRequired: 101}}},
		{ID: "p", AntiAffinity: []AntiAffinityTerm{{Selector: Selector{"": "v"}, Topology: TopologyNode}}},
		{ID: "p", AntiAffinity: []AntiAffinityTerm{{Selector: Selector{}, Topology: "nope"}}},
	}
	for i, p := range cases {
		if err := f.Place(p, "n1"); reasonOf(err) != ReasonInvalidArgument {
			t.Fatalf("case %d Place: %v", i, err)
		}
		if _, err := f.Feasible(p); reasonOf(err) != ReasonInvalidArgument {
			t.Fatalf("case %d Feasible: %v", i, err)
		}
	}
	// Relabel：参数非法 -> Pod 不存在。
	if err := f.Relabel("ghost", map[string]string{"": "v"}); reasonOf(err) != ReasonInvalidArgument {
		t.Fatalf("relabel invalid first: %v", err)
	}
	if err := f.Relabel("ghost", map[string]string{"a": "b"}); reasonOf(err) != ReasonPodNotFound {
		t.Fatalf("relabel not found: %v", err)
	}
}

// 节点与 Pod 生命周期的各拒绝原因可区分；被拒绝不改状态。
func TestLifecycleErrors(t *testing.T) {
	f := NewFilter(2)
	if err := f.AddNode(Node{Name: "", Zone: "z"}); reasonOf(err) != ReasonInvalidArgument {
		t.Fatalf("addnode empty name: %v", err)
	}
	if err := f.AddNode(Node{Name: "n1", Zone: ""}); reasonOf(err) != ReasonInvalidArgument {
		t.Fatalf("addnode empty zone: %v", err)
	}
	mustAdd(t, f, "n1", "a")
	if err := f.AddNode(Node{Name: "n1", Zone: "b"}); reasonOf(err) != ReasonNodeExists {
		t.Fatalf("addnode duplicate: %v", err)
	}
	if err := f.RemoveNode("ghost"); reasonOf(err) != ReasonNodeNotFound {
		t.Fatalf("removenode missing: %v", err)
	}

	// Commit/Cancel 不存在；预留中的 Pod 不能 Remove；提交后的 Pod 不能再 Commit/Cancel。
	if err := f.Commit("ghost"); reasonOf(err) != ReasonPodNotFound {
		t.Fatalf("commit missing: %v", err)
	}
	if err := f.Cancel("ghost"); reasonOf(err) != ReasonPodNotFound {
		t.Fatalf("cancel missing: %v", err)
	}
	if err := f.Remove("ghost"); reasonOf(err) != ReasonPodNotFound {
		t.Fatalf("remove missing: %v", err)
	}
	if err := f.Reserve(Pod{ID: "r"}, "n1"); err != nil {
		t.Fatal(err)
	}
	if err := f.RemoveNode("n1"); reasonOf(err) != ReasonNodeInUse {
		t.Fatalf("removenode in use (reserved): %v", err)
	}
	if err := f.Remove("r"); reasonOf(err) != ReasonStillReserved {
		t.Fatalf("remove reserved: %v", err)
	}
	if err := f.Commit("r"); err != nil {
		t.Fatal(err)
	}
	if err := f.Commit("r"); reasonOf(err) != ReasonNotReserved {
		t.Fatalf("commit committed: %v", err)
	}
	if err := f.Cancel("r"); reasonOf(err) != ReasonNotReserved {
		t.Fatalf("cancel committed: %v", err)
	}
	if err := f.RemoveNode("n1"); reasonOf(err) != ReasonNodeInUse {
		t.Fatalf("removenode in use (committed): %v", err)
	}
	if err := f.Remove("r"); err != nil {
		t.Fatal(err)
	}
	if err := f.RemoveNode("n1"); err != nil {
		t.Fatalf("removenode now empty: %v", err)
	}
}

// 深拷贝：调用方在操作后修改入参不影响内部状态。
func TestInputDefensiveCopy(t *testing.T) {
	f := NewFilter(10)
	mustAdd(t, f, "n1", "a")
	labels := map[string]string{"app": "x"}
	x := Pod{ID: "x", Labels: labels, AntiAffinity: []AntiAffinityTerm{
		{Selector: Selector{"app": "x"}, Topology: TopologyNode}}}
	if err := f.Place(x, "n1"); err != nil {
		t.Fatal(err)
	}
	labels["app"] = "mutated"
	x.AntiAffinity[0].Selector["app"] = "mutated"
	// 内部仍持有 app=x：同节点再来一个 app=x 必须被 x 对称排斥。
	if pe := asPErr(t, f.Place(Pod{ID: "y", Labels: map[string]string{"app": "x"}}, "n1")); pe.Reason != ReasonRejectedByExistingPod {
		t.Fatalf("internal state leaked: %+v", pe)
	}
}
