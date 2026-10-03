package rule

import (
	"testing"

	"ontology/store"
)

func rv(ver, c int64, marker bool, r int64) store.Version {
	return store.Version{Ver: ver, C: c, Marker: marker, R: r}
}

func actionsOf(items []Item) []Action {
	out := make([]Action, len(items))
	for i, it := range items {
		out[i] = it.Action
	}
	return out
}

func versOf(items []Item) []int64 {
	out := make([]int64, len(items))
	for i, it := range items {
		out[i] = it.Ver
	}
	return out
}

func eqInt64(a, b []int64) bool {
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

func eqAction(a, b []Action) bool {
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

func TestDue(t *testing.T) {
	cases := []struct {
		name    string
		t       int64
		days    int
		wantDue int64
	}{
		{"zero plus one day", 0, 1, 86400},
		{"at midnight 1 day", 86400, 1, 172800},
		{"100000 plus 1 day", 100000, 1, 259200},
		{"1 sec before midnight", 86399, 1, 172800},
		{"1 sec after midnight", 86401, 1, 259200},
		{"3600 plus 2 days", 3600, 2, 259200},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Due(tc.t, tc.days); got != tc.wantDue {
				t.Fatalf("Due(%d,%d) = %d, want %d", tc.t, tc.days, got, tc.wantDue)
			}
		})
	}
}

func TestNewValidation(t *testing.T) {
	good := Rule{ID: "x", Kind: Expire, Days: 1, Keep: 0}
	cases := []struct {
		name  string
		rules []Rule
	}{
		{"empty id", []Rule{{ID: "", Kind: Expire, Days: 1}}},
		{"dup id", []Rule{good, {ID: "x", Kind: OrphanMarker, Days: 1}}},
		{"days 0", []Rule{{ID: "a", Kind: Expire, Days: 0}}},
		{"days big", []Rule{{ID: "a", Kind: Expire, Days: 3651}}},
		{"keep neg", []Rule{{ID: "a", Kind: NoncurrentExpire, Days: 1, Keep: -1}}},
		{"keep big", []Rule{{ID: "a", Kind: NoncurrentExpire, Days: 1, Keep: 1001}}},
		{"bad kind", []Rule{{ID: "a", Kind: Kind(9), Days: 1}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.rules); err == nil {
				t.Fatal("expected constructor error")
			}
		})
	}
	if _, err := New([]Rule{good}); err != nil {
		t.Fatalf("valid rules rejected: %v", err)
	}
}

// 规格中的例子：R1 NoncurrentExpire days=1 keep=1，R2 Expire days=1。
func TestEvalSpecExample(t *testing.T) {
	eng, err := New([]Rule{
		{ID: "R1", Kind: NoncurrentExpire, Days: 1, Keep: 1},
		{ID: "R2", Kind: Expire, Days: 1},
	})
	if err != nil {
		t.Fatal(err)
	}
	versions := []store.Version{rv(1, 0, false, 0), rv(2, 100000, false, 0), rv(3, 200000, false, 0)}

	t.Run("now=400000 keep=1", func(t *testing.T) {
		items, plan := eng.Eval([]byte("a"), versions, 400000, 4)
		if got := versOf(items); !eqInt64(got, []int64{4, 1, 2}) {
			t.Fatalf("vers %v", got)
		}
		if got := actionsOf(items); !eqAction(got, []Action{AddMarker, Removed, Removed}) {
			t.Fatalf("actions %v", got)
		}
		if !plan.AddMarker || !eqInt64(plan.Remove, []int64{1, 2}) {
			t.Fatalf("plan add=%v rem=%v", plan.AddMarker, plan.Remove)
		}
		if plan.MarkerC != 345600 {
			t.Fatalf("marker c = %d, want 345600", plan.MarkerC)
		}
	})

	t.Run("now=500000 keep=0 cascade to orphan", func(t *testing.T) {
		eng0, _ := New([]Rule{
			{ID: "R1", Kind: NoncurrentExpire, Days: 1, Keep: 0},
			{ID: "R2", Kind: Expire, Days: 1},
			{ID: "R3", Kind: OrphanMarker, Days: 1},
		})
		items, plan := eng0.Eval([]byte("a"), versions, 500000, 4)
		if got := versOf(items); !eqInt64(got, []int64{4, 1, 2, 3, 4}) {
			t.Fatalf("vers %v", got)
		}
		if got := actionsOf(items); !eqAction(got, []Action{AddMarker, Removed, Removed, Removed, Removed}) {
			t.Fatalf("actions %v", got)
		}
		if !plan.AddMarker || !eqInt64(plan.Remove, []int64{1, 2, 3, 4}) {
			t.Fatalf("plan add=%v rem=%v", plan.AddMarker, plan.Remove)
		}
	})

	t.Run("now=400000 keep=0 v3 not due yet", func(t *testing.T) {
		eng0, _ := New([]Rule{
			{ID: "R1", Kind: NoncurrentExpire, Days: 1, Keep: 0},
			{ID: "R2", Kind: Expire, Days: 1},
		})
		items, plan := eng0.Eval([]byte("a"), versions, 400000, 4)
		if got := versOf(items); !eqInt64(got, []int64{4, 1, 2}) {
			t.Fatalf("vers %v", got)
		}
		if !plan.AddMarker || !eqInt64(plan.Remove, []int64{1, 2}) {
			t.Fatalf("plan add=%v rem=%v", plan.AddMarker, plan.Remove)
		}
	})
}

// now 恰等到期则删；now 小 1 不删；起算恰在零点不进位。
func TestEvalDueBoundaryNowEquals(t *testing.T) {
	eng, _ := New([]Rule{{ID: "E", Kind: Expire, Days: 1}})
	versions := []store.Version{rv(1, 0, false, 0)}

	items, plan := eng.Eval([]byte("k"), versions, 86400, 2)
	if len(items) != 1 || items[0].Action != AddMarker {
		t.Fatalf("at due must expire: %+v", items)
	}
	if plan.MarkerC != 86400 {
		t.Fatalf("marker c %d", plan.MarkerC)
	}
	items, _ = eng.Eval([]byte("k"), versions, 86399, 2)
	if len(items) != 0 {
		t.Fatalf("one second before due must not expire: %+v", items)
	}
}

// 当前版本已是删除标记时，Expire 不追加标记。
func TestEvalCurrentMarkerSkipped(t *testing.T) {
	eng, _ := New([]Rule{{ID: "E", Kind: Expire, Days: 1}})
	versions := []store.Version{rv(1, 0, false, 0), rv(2, 0, true, 0)}
	items, _ := eng.Eval([]byte("k"), versions, 1<<40, 3)
	if len(items) != 0 {
		t.Fatalf("marker current must not be expired: %+v", items)
	}
}

// keep 数被锁版本，被锁版本占名额；到期但 r>now 记 Blocked；r==now 视为无锁。
func TestEvalKeepCountsLockedAndBlocked(t *testing.T) {
	versions := []store.Version{
		rv(1, 86400, false, 0),
		rv(2, 172800, false, 1_000_000),
		rv(3, 259200, false, 0),
		rv(4, 345600, false, 0),
		rv(5, 432000, false, 0),
	}
	eng, _ := New([]Rule{{ID: "N", Kind: NoncurrentExpire, Days: 1, Keep: 1}})

	// 非当前 v1,v2,v3,v4，keep=1 只保最新 v4。
	// v1 due=259200 Removed；v2 due=345600 但锁到 1e6 → Blocked；v3 due=432000 Removed。
	items, plan := eng.Eval([]byte("k"), versions, 500000, 6)
	if got := versOf(items); !eqInt64(got, []int64{1, 2, 3}) {
		t.Fatalf("vers %v", got)
	}
	if got := actionsOf(items); !eqAction(got, []Action{Removed, Blocked, Removed}) {
		t.Fatalf("actions %v", got)
	}
	if plan.AddMarker || !eqInt64(plan.Remove, []int64{1, 3}) {
		t.Fatalf("blocked must not be removed: add=%v rem=%v", plan.AddMarker, plan.Remove)
	}

	versions[1].R = 500000 // r == now → 无锁
	items, plan = eng.Eval([]byte("k"), versions, 500000, 6)
	if got := actionsOf(items); !eqAction(got, []Action{Removed, Removed, Removed}) {
		t.Fatalf("r==now must be treated unlocked: %v", got)
	}
	if !eqInt64(plan.Remove, []int64{1, 2, 3}) {
		t.Fatalf("rem %v", plan.Remove)
	}
}

// 阶段级联：Expire 标记使原当前以标记 c 为起算时刻进入阶段二。
func TestEvalExpireMarkerFeedsStage2(t *testing.T) {
	eng, _ := New([]Rule{
		{ID: "E", Kind: Expire, Days: 30},
		{ID: "N", Kind: NoncurrentExpire, Days: 1, Keep: 0},
	})
	versions := []store.Version{
		rv(1, 0, false, 0),
		rv(2, 100, false, 0),
		rv(3, 200000, false, 0),
	}
	// marker c = ceil((200000+30*86400)/86400)*86400 = 33*86400 = 2851200。
	markerC := int64(33 * 86400)

	items, plan := eng.Eval([]byte("k"), versions, 3_000_000, 4)
	if got := versOf(items); !eqInt64(got, []int64{4, 1, 2, 3}) {
		t.Fatalf("vers %v", got)
	}
	if got := actionsOf(items); !eqAction(got, []Action{AddMarker, Removed, Removed, Removed}) {
		t.Fatalf("actions %v", got)
	}
	if !plan.AddMarker || plan.MarkerC != markerC {
		t.Fatalf("marker c=%d want %d", plan.MarkerC, markerC)
	}

	// now 比 v3 新到期（2851200+86400=2937600）早 1 秒：v3 保留。
	items, plan = eng.Eval([]byte("k"), versions, 2_937_599, 4)
	if got := versOf(items); !eqInt64(got, []int64{4, 1, 2}) {
		t.Fatalf("vers %v", got)
	}
	if !eqInt64(plan.Remove, []int64{1, 2}) {
		t.Fatalf("rem %v", plan.Remove)
	}
}

// 同键同 kind 多规则：最长前缀优先；前缀等长取 id 字节序最小。
func TestRuleSelectionLongestPrefixAndTie(t *testing.T) {
	eng, err := New([]Rule{
		{ID: "short", Prefix: []byte("a"), Kind: Expire, Days: 1},
		{ID: "long", Prefix: []byte("ab"), Kind: Expire, Days: 100},
		{ID: "zzz", Prefix: []byte("abc"), Kind: Expire, Days: 2},
		{ID: "aaa", Prefix: []byte("abc"), Kind: Expire, Days: 3},
	})
	if err != nil {
		t.Fatal(err)
	}
	// key "ab..." 匹配 days=100 的 long；key "abc..." 在 aaa 与 zzz 间取 aaa(days=3)。
	versions := []store.Version{rv(1, 0, false, 0)}

	// long: due = 100 天后 = 8640000。now=8640000-1 不删。
	if items, _ := eng.Eval([]byte("abx"), versions, 8_639_999, 2); len(items) != 0 {
		t.Fatalf("long-prefix rule should be selected: %+v", items)
	}
	if items, _ := eng.Eval([]byte("abx"), versions, 8_640_000, 2); len(items) != 1 {
		t.Fatalf("long-prefix rule should expire now")
	}
	// aaa: days=3 → due=259200；若错选 zzz days=2 则 due=172800。
	items, _ := eng.Eval([]byte("abcd"), versions, 200000, 2)
	if len(items) != 0 {
		t.Fatalf("tie should pick id aaa (days=3, not due yet): %+v", items)
	}
	// 不匹配任何前缀：无动作。
	if items, _ := eng.Eval([]byte("zzz"), versions, 1<<40, 2); len(items) != 0 {
		t.Fatalf("unmatched key must have no actions: %+v", items)
	}
}

// OrphanMarker 仅对有匹配规则的键生效；阶段三看到的是阶段一二之后的版本集。
func TestOrphanMarkerOnlyWhenMatched(t *testing.T) {
	with, _ := New([]Rule{{ID: "o", Kind: OrphanMarker, Days: 1}})
	without, _ := New([]Rule{
		{ID: "e", Kind: Expire, Days: 1},
		{ID: "n", Kind: NoncurrentExpire, Days: 1, Keep: 0},
	})
	// 只剩一个删除标记：有孤儿规则则删，无则保留。
	oneMarker := []store.Version{rv(7, 500, true, 0)}
	if items, plan := with.Eval([]byte("k"), oneMarker, 1000, 8); len(items) != 1 ||
		items[0].Action != Removed || !eqInt64(plan.Remove, []int64{7}) {
		t.Fatalf("orphan marker should be removed: %+v %+v", items, plan)
	}
	if items, _ := without.Eval([]byte("k"), oneMarker, 1000, 8); len(items) != 0 {
		t.Fatalf("without matching orphan rule marker must stay: %+v", items)
	}

	// 仅剩一个数据版本不算孤儿。
	oneData := []store.Version{rv(7, 500, false, 0)}
	if items, _ := with.Eval([]byte("k"), oneData, 1000, 8); len(items) != 0 {
		t.Fatalf("single data version is not an orphan marker: %+v", items)
	}
}
