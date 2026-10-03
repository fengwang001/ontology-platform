package scan

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"

	"ontology/rule"
	"ontology/store"
)

// --- 测试辅助 ---

func mustSet(t *testing.T, rules ...rule.Rule) *rule.Set {
	t.Helper()
	rs, err := rule.NewSet(rules...)
	if err != nil {
		t.Fatalf("NewSet: %v", err)
	}
	return rs
}

func mustLoad(t *testing.T, st *store.Store, key string, vers ...store.Version) {
	t.Helper()
	for _, v := range vers {
		if err := st.Load(key, v); err != nil {
			t.Fatalf("Load(%q, %+v): %v", key, v, err)
		}
	}
}

// drain 反复调用 Scan 直到空游标，返回拼接后的清单与每次调用结果。
func drain(t *testing.T, sc *Scanner, now, budget int64) (Result, []Result) {
	t.Helper()
	var all Result
	var calls []Result
	for {
		res, err := sc.Scan(now, budget)
		if err != nil {
			t.Fatalf("Scan(now=%d, budget=%d): %v", now, budget, err)
		}
		calls = append(calls, res)
		all.Entries = append(all.Entries, res.Entries...)
		if res.Cursor == "" {
			return all, calls
		}
	}
}

// --- 题面示例：R1=(NoncurrentExpire,"",days=1,keep=1), R2=(Expire,"",days=1) ---

func TestSpecExample(t *testing.T) {
	st := store.New()
	mustLoad(t, st, "a",
		store.Version{Ver: 1, C: 0},
		store.Version{Ver: 2, C: 100000},
		store.Version{Ver: 3, C: 200000})
	rs := mustSet(t,
		rule.Rule{ID: "R1", Kind: rule.NoncurrentExpire, Days: 1, Keep: 1},
		rule.Rule{ID: "R2", Kind: rule.Expire, Days: 1})
	res, err := New(st, rs).Scan(400000, MaxBudget)
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{Key: "a", Ver: 4, Action: AddMarker}, // 标记 c = 到期时刻 345600
		{Key: "a", Ver: 1, Action: Removed},
		{Key: "a", Ver: 2, Action: Removed},
	}
	if !reflect.DeepEqual(res.Entries, want) {
		t.Fatalf("entries = %v, want %v", res.Entries, want)
	}
	wantState := map[string][]store.Version{
		"a": {{Ver: 3, C: 200000}, {Ver: 4, C: 345600, Marker: true}},
	}
	if got := st.Snapshot(); !reflect.DeepEqual(got, wantState) {
		t.Fatalf("state = %v, want %v", got, wantState)
	}
	if res.Cursor != "" || res.Examined() != 3 {
		t.Fatalf("cursor=%q examined=%d, want \"\" and 3", res.Cursor, res.Examined())
	}
}

// 题面示例 keep=0：v3 到期时刻 432000。
// now=400000 时 v3 不删；now=500000 时 v3 删除，阶段三同次清掉孤儿标记。
func TestSpecExampleKeepZero(t *testing.T) {
	load := func() *store.Store {
		st := store.New()
		mustLoad(t, st, "a",
			store.Version{Ver: 1, C: 0},
			store.Version{Ver: 2, C: 100000},
			store.Version{Ver: 3, C: 200000})
		return st
	}
	rules := func() *rule.Set {
		return mustSet(t,
			rule.Rule{ID: "R1", Kind: rule.NoncurrentExpire, Days: 1, Keep: 0},
			rule.Rule{ID: "R2", Kind: rule.Expire, Days: 1},
			rule.Rule{ID: "R3", Kind: rule.OrphanMarker, Days: 1})
	}

	st := load()
	res, err := New(st, rules()).Scan(400000, MaxBudget)
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{Key: "a", Ver: 4, Action: AddMarker},
		{Key: "a", Ver: 1, Action: Removed},
		{Key: "a", Ver: 2, Action: Removed},
	}
	if !reflect.DeepEqual(res.Entries, want) {
		t.Fatalf("now=400000 entries = %v, want %v", res.Entries, want)
	}
	if got := st.Snapshot(); len(got["a"]) != 2 {
		t.Fatalf("now=400000 state = %v, want v3+v4", got)
	}

	st = load()
	res, err = New(st, rules()).Scan(500000, MaxBudget)
	if err != nil {
		t.Fatal(err)
	}
	want = []Entry{
		{Key: "a", Ver: 4, Action: AddMarker},
		{Key: "a", Ver: 1, Action: Removed},
		{Key: "a", Ver: 2, Action: Removed},
		{Key: "a", Ver: 3, Action: Removed},
		{Key: "a", Ver: 4, Action: Removed}, // 孤儿标记同次清理
	}
	if !reflect.DeepEqual(res.Entries, want) {
		t.Fatalf("now=500000 entries = %v, want %v", res.Entries, want)
	}
	if got := st.Snapshot(); len(got) != 0 {
		t.Fatalf("now=500000 state = %v, want empty", got)
	}
}

// --- 到期边界：恰等于 now 到期、小 1 不到期、恰在零点不进位 ---

func TestExpireBoundary(t *testing.T) {
	cases := []struct {
		name string
		c    int64
		days int
		now  int64
		fire bool
	}{
		{"due equals now", 0, 1, 86400, true},
		{"one before due", 0, 1, 86399, false},
		{"midnight start no carry", 86400, 1, 172800, true}, // 恰在零点起算不进位
		{"midnight start minus 1", 86400, 1, 172799, false},
		{"rounds up to next midnight", 1, 1, 172800, true}, // 86401 -> 172800
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := store.New()
			mustLoad(t, st, "k", store.Version{Ver: 1, C: tc.c})
			rs := mustSet(t, rule.Rule{ID: "e", Kind: rule.Expire, Days: tc.days})
			res, err := New(st, rs).Scan(tc.now, MaxBudget)
			if err != nil {
				t.Fatal(err)
			}
			fired := len(res.Entries) == 1 && res.Entries[0].Action == AddMarker
			if fired != tc.fire {
				t.Fatalf("c=%d days=%d now=%d fired=%v, want %v (entries=%v)",
					tc.c, tc.days, tc.now, fired, tc.fire, res.Entries)
			}
			if fired {
				wantC := dueAt(tc.c, tc.days)
				if got := st.Snapshot()["k"][1]; got.C != wantC || !got.Marker {
					t.Fatalf("marker = %+v, want c=%d", got, wantC)
				}
			}
		})
	}
}

// --- keep 名额计入被锁版本；r > now 才阻塞（恰等于 now 视为无锁） ---

func TestKeepCountsLockedAndBlocked(t *testing.T) {
	// v2 被锁且占最新 1 个 keep 名额 -> v1 不在名额内且已到期 -> 删除。
	// 若被锁不占名额，v1 会被保留。
	st := store.New()
	mustLoad(t, st, "k",
		store.Version{Ver: 1, C: 0},
		store.Version{Ver: 2, C: 100000, R: 1000000},
		store.Version{Ver: 3, C: 200000})
	rs := mustSet(t, rule.Rule{ID: "n", Kind: rule.NoncurrentExpire, Days: 1, Keep: 1})
	res, err := New(st, rs).Scan(300000, MaxBudget)
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{{Key: "k", Ver: 1, Action: Removed}}
	if !reflect.DeepEqual(res.Entries, want) {
		t.Fatalf("entries = %v, want %v", res.Entries, want)
	}
	if got := st.Snapshot()["k"]; len(got) != 2 {
		t.Fatalf("state = %v, want v2+v3", got)
	}
}

func TestBlockedAndLockBoundary(t *testing.T) {
	cases := []struct {
		name string
		r    int64
		now  int64
		want Action
	}{
		{"locked beyond now", 300001, 300000, Blocked},
		{"lock equals now means unlocked", 300000, 300000, Removed},
		{"no lock", 0, 300000, Removed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := store.New()
			mustLoad(t, st, "k",
				store.Version{Ver: 1, C: 0, R: tc.r},
				store.Version{Ver: 2, C: 100000})
			rs := mustSet(t, rule.Rule{ID: "n", Kind: rule.NoncurrentExpire, Days: 1, Keep: 0})
			res, err := New(st, rs).Scan(tc.now, MaxBudget)
			if err != nil {
				t.Fatal(err)
			}
			want := []Entry{{Key: "k", Ver: 1, Action: tc.want}}
			if !reflect.DeepEqual(res.Entries, want) {
				t.Fatalf("entries = %v, want %v", res.Entries, want)
			}
			_, stillThere := st.Snapshot()["k"]
			if tc.want == Removed && len(st.Snapshot()["k"]) != 1 {
				t.Fatalf("v1 must be removed: %v", st.Snapshot())
			}
			if tc.want == Blocked && (!stillThere || len(st.Snapshot()["k"]) != 2) {
				t.Fatalf("v1 must be kept: %v", st.Snapshot())
			}
		})
	}
}

// --- 同键同 kind 多规则：最长前缀优先，并列取 id 最小 ---

func TestRulePrecedenceInScan(t *testing.T) {
	rules := func() *rule.Set {
		return mustSet(t,
			rule.Rule{ID: "b", Prefix: "", Kind: rule.Expire, Days: 5},
			rule.Rule{ID: "a", Prefix: "", Kind: rule.Expire, Days: 1},
			rule.Rule{ID: "c", Prefix: "app", Kind: rule.Expire, Days: 10})
	}
	// "apple" 命中前缀最长的 c（days=10，到期 864000），now=100000 不触发。
	st := store.New()
	mustLoad(t, st, "apple", store.Version{Ver: 1, C: 0})
	res, err := New(st, rules()).Scan(100000, MaxBudget)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 0 {
		t.Fatalf("apple must use longest-prefix rule c: %v", res.Entries)
	}
	// "banana" 并列空前缀取 id 最小的 a（days=1，到期 86400），触发。
	st = store.New()
	mustLoad(t, st, "banana", store.Version{Ver: 1, C: 0})
	res, err = New(st, rules()).Scan(100000, MaxBudget)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 || res.Entries[0].Action != AddMarker {
		t.Fatalf("banana must use tie-broken rule a: %v", res.Entries)
	}
}

// --- 阶段间级联：Expire 标记使原当前成为非当前（起算=标记的 c） ---

func TestCascadeMarkerCStartsNoncurrent(t *testing.T) {
	// v1 c=0 当前，Expire days=1 -> 标记 v2 c=86400。
	// v1 自 86400 起算，NoncurrentExpire days=1 -> 到期 172800。
	// now=172799：v1 不到期保留；now=172800：v1 删除，随后孤儿 v2 同次清理。
	rules := func() *rule.Set {
		return mustSet(t,
			rule.Rule{ID: "e", Kind: rule.Expire, Days: 1},
			rule.Rule{ID: "n", Kind: rule.NoncurrentExpire, Days: 1, Keep: 0},
			rule.Rule{ID: "o", Kind: rule.OrphanMarker, Days: 1})
	}
	load := func() *store.Store {
		st := store.New()
		mustLoad(t, st, "k", store.Version{Ver: 1, C: 0})
		return st
	}

	st := load()
	res, err := New(st, rules()).Scan(172799, MaxBudget)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Entries) != 1 || res.Entries[0].Action != AddMarker {
		t.Fatalf("now=172799 entries = %v, want only AddMarker", res.Entries)
	}

	st = load()
	res, err = New(st, rules()).Scan(172800, MaxBudget)
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		{Key: "k", Ver: 2, Action: AddMarker},
		{Key: "k", Ver: 1, Action: Removed},
		{Key: "k", Ver: 2, Action: Removed},
	}
	if !reflect.DeepEqual(res.Entries, want) {
		t.Fatalf("now=172800 entries = %v, want %v", res.Entries, want)
	}
	if got := st.Snapshot(); len(got) != 0 {
		t.Fatalf("state = %v, want empty", got)
	}
}

// --- budget=1 逐步推进、首键超限仍处理、examined 语义 ---

func TestBudgetOneProgress(t *testing.T) {
	st := store.New()
	// 三个键，代价分别为 2、1、3。
	mustLoad(t, st, "a", store.Version{Ver: 1, C: 0}, store.Version{Ver: 2, C: 10})
	mustLoad(t, st, "b", store.Version{Ver: 3, C: 0})
	mustLoad(t, st, "c",
		store.Version{Ver: 4, C: 0}, store.Version{Ver: 5, C: 10}, store.Version{Ver: 6, C: 20})
	rs := mustSet(t, rule.Rule{ID: "e", Kind: rule.Expire, Days: 1})
	sc := New(st, rs)
	_, calls := drain(t, sc, 200000, 1)
	if len(calls) != 3 {
		t.Fatalf("budget=1 must process exactly one key per call, got %d calls", len(calls))
	}
	wantCost := []int64{2, 1, 3}
	for i, c := range calls {
		if c.Examined() != wantCost[i] {
			t.Fatalf("call %d examined=%d, want %d", i, c.Examined(), wantCost[i])
		}
	}
	// 全部当前版本到期 -> 每键一个 AddMarker。
	var all []Entry
	for _, c := range calls {
		all = append(all, c.Entries...)
	}
	if len(all) != 3 {
		t.Fatalf("entries = %v, want 3 AddMarker", all)
	}
	for _, e := range all {
		if e.Action != AddMarker {
			t.Fatalf("entries = %v, want all AddMarker", all)
		}
	}
}

func TestFirstKeyExceedsBudget(t *testing.T) {
	st := store.New()
	mustLoad(t, st, "big",
		store.Version{Ver: 1, C: 0}, store.Version{Ver: 2, C: 10},
		store.Version{Ver: 3, C: 20}, store.Version{Ver: 4, C: 30},
		store.Version{Ver: 5, C: 40})
	mustLoad(t, st, "small", store.Version{Ver: 6, C: 0})
	rs := mustSet(t, rule.Rule{ID: "e", Kind: rule.Expire, Days: 1})
	sc := New(st, rs)
	res, err := sc.Scan(200000, 2)
	if err != nil {
		t.Fatal(err)
	}
	// 首键代价 5 > budget 2 仍处理，examined=5；下一键留待下次。
	if res.Examined() != 5 || res.Cursor != "small" {
		t.Fatalf("examined=%d cursor=%q, want 5 and \"small\"", res.Examined(), res.Cursor)
	}
	if len(res.Entries) != 1 || res.Entries[0].Key != "big" {
		t.Fatalf("entries = %v", res.Entries)
	}
	res, err = sc.Scan(200000, 2)
	if err != nil {
		t.Fatal(err)
	}
	if res.Cursor != "" || res.Examined() != 1 {
		t.Fatalf("second call: cursor=%q examined=%d", res.Cursor, res.Examined())
	}
}

func TestInvalidBudget(t *testing.T) {
	sc := New(store.New(), mustSet(t, rule.Rule{ID: "e", Kind: rule.Expire, Days: 1}))
	for _, b := range []int64{0, -1, MaxBudget + 1} {
		if _, err := sc.Scan(0, b); !errors.Is(err, ErrInvalidBudget) {
			t.Fatalf("budget=%d: got %v", b, err)
		}
	}
}

// --- Remove 注入失败：整键回滚，游标指向该键，撤销注入后可重处理 ---

func TestRemoveFailureRollback(t *testing.T) {
	st := store.New()
	mustLoad(t, st, "a", store.Version{Ver: 1, C: 0})
	mustLoad(t, st, "b",
		store.Version{Ver: 2, C: 0}, store.Version{Ver: 3, C: 100000})
	rs := mustSet(t,
		rule.Rule{ID: "e", Kind: rule.Expire, Days: 1},
		rule.Rule{ID: "n", Kind: rule.NoncurrentExpire, Days: 1, Keep: 0})
	sc := New(st, rs)
	// 键 b：阶段一追加标记 v4(c=172800->186400? 见下)，阶段二删除 v2、v3 时注入失败。
	st.SetRemoveHook(func(key string, ver uint64) bool { return key == "b" && ver == 3 })
	before := st.Snapshot()
	res, err := sc.Scan(500000, MaxBudget)
	if !errors.Is(err, ErrRemoveFailed) {
		t.Fatalf("got %v, want ErrRemoveFailed", err)
	}
	if res.Cursor != "b" {
		t.Fatalf("cursor=%q, want \"b\"", res.Cursor)
	}
	// 键 a 的效果与清单保留（标记 v4 + 阶段二删除 v1）；键 b 整键撤销。
	wantA := []Entry{
		{Key: "a", Ver: 4, Action: AddMarker},
		{Key: "a", Ver: 1, Action: Removed},
	}
	if !reflect.DeepEqual(res.Entries, wantA) {
		t.Fatalf("entries = %v", res.Entries)
	}
	if got := st.Snapshot()["b"]; !reflect.DeepEqual(got, before["b"]) {
		t.Fatalf("key b must be rolled back: got %v want %v", got, before["b"])
	}
	if res.Examined() != 1+2 {
		t.Fatalf("examined=%d, want 3 (含回滚键)", res.Examined())
	}
	// 撤销注入后重处理：游标仍指向 b，本次成功；标记取新全局序号 6（5 已回滚消耗）。
	st.SetRemoveHook(nil)
	res, err = sc.Scan(500000, MaxBudget)
	if err != nil {
		t.Fatal(err)
	}
	if res.Cursor != "" {
		t.Fatalf("cursor=%q, want \"\"", res.Cursor)
	}
	want := []Entry{
		{Key: "b", Ver: 6, Action: AddMarker},
		{Key: "b", Ver: 2, Action: Removed},
		{Key: "b", Ver: 3, Action: Removed},
	}
	if !reflect.DeepEqual(res.Entries, want) {
		t.Fatalf("entries = %v, want %v", res.Entries, want)
	}
	if got := st.Snapshot()["b"]; len(got) != 1 || !got[0].Marker {
		t.Fatalf("key b final = %v, want single marker", got)
	}
}

// --- 朴素逐步模拟（独立实现，作为随机对照的基准） ---

func simCeilDay(x int64) int64 { return (x + 86399) / 86400 * 86400 }

func simMatch(rules []rule.Rule, key string, kind rule.Kind) (rule.Rule, bool) {
	var best rule.Rule
	found := false
	for _, r := range rules {
		if r.Kind != kind || !strings.HasPrefix(key, r.Prefix) {
			continue
		}
		if !found || len(r.Prefix) > len(best.Prefix) ||
			(len(r.Prefix) == len(best.Prefix) && r.ID < best.ID) {
			best, found = r, true
		}
	}
	return best, found
}

// simulate 按题面规则逐键、逐阶段、逐版本地朴素模拟一次完整扫描，
// 返回清单与最终状态。maxVer 为已用全局序号高水位。
func simulate(state map[string][]store.Version, rules []rule.Rule, now int64, maxVer uint64) ([]Entry, map[string][]store.Version) {
	state = copyState(state)
	var entries []Entry
	for _, key := range sortedKeys(state) {
		vers := state[key]
		// 阶段一：Expire
		if r, ok := simMatch(rules, key, rule.Expire); ok {
			cur := vers[len(vers)-1]
			if !cur.Marker && now >= simCeilDay(cur.C+int64(r.Days)*86400) {
				maxVer++
				due := simCeilDay(cur.C + int64(r.Days)*86400)
				vers = append(vers, store.Version{Ver: maxVer, C: due, Marker: true})
				entries = append(entries, Entry{Key: key, Ver: maxVer, Action: AddMarker})
			}
		}
		// 阶段二：NoncurrentExpire（判定基于本阶段开始时的版本集）
		if r, ok := simMatch(rules, key, rule.NoncurrentExpire); ok {
			protected := map[uint64]bool{}
			cnt := 0
			for i := len(vers) - 2; i >= 0 && cnt < r.Keep; i-- {
				if !vers[i].Marker {
					protected[vers[i].Ver] = true
					cnt++
				}
			}
			var kept []store.Version
			for i, v := range vers {
				isCurrent := i == len(vers)-1
				if isCurrent || v.Marker || protected[v.Ver] {
					kept = append(kept, v)
					continue
				}
				became := vers[i+1].C
				if now < simCeilDay(became+int64(r.Days)*86400) {
					kept = append(kept, v)
					continue
				}
				if v.R > now {
					entries = append(entries, Entry{Key: key, Ver: v.Ver, Action: Blocked})
					kept = append(kept, v)
				} else {
					entries = append(entries, Entry{Key: key, Ver: v.Ver, Action: Removed})
				}
			}
			vers = kept
		}
		// 阶段三：OrphanMarker
		if _, ok := simMatch(rules, key, rule.OrphanMarker); ok {
			if len(vers) == 1 && vers[0].Marker {
				entries = append(entries, Entry{Key: key, Ver: vers[0].Ver, Action: Removed})
				vers = nil
			}
		}
		if len(vers) == 0 {
			delete(state, key)
		} else {
			state[key] = vers
		}
	}
	return entries, state
}

func copyState(m map[string][]store.Version) map[string][]store.Version {
	out := make(map[string][]store.Version, len(m))
	for k, v := range m {
		out[k] = append([]store.Version(nil), v...)
	}
	return out
}

func sortedKeys(m map[string][]store.Version) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// --- 1500 组随机键与规则：引擎 vs 朴素模拟，且与扫描切分无关 ---

type randomCase struct {
	state  map[string][]store.Version
	rules  []rule.Rule
	now    int64
	maxVer uint64
}

func genCase(rng *rand.Rand) randomCase {
	keyPool := []string{"a", "ab", "abc", "b", "ba", "c", "d/e", "d/f"}
	nKeys := 1 + rng.Intn(5)
	perm := rng.Perm(len(keyPool))[:nKeys]
	keys := make([]string, 0, nKeys)
	for _, i := range perm {
		keys = append(keys, keyPool[i])
	}
	sort.Strings(keys)

	state := map[string][]store.Version{}
	var maxVer uint64
	for _, k := range keys {
		n := 1 + rng.Intn(5)
		var c int64
		for i := 0; i < n; i++ {
			c += int64(rng.Intn(80000))
			maxVer++
			v := store.Version{Ver: maxVer, C: c}
			if rng.Intn(4) == 0 {
				v.Marker = true
			} else if rng.Intn(3) == 0 {
				v.R = int64(rng.Intn(900000))
			}
			state[k] = append(state[k], v)
		}
	}

	prefixes := []string{"", "a", "ab", "b", "d/"}
	nRules := 1 + rng.Intn(5)
	var rules []rule.Rule
	for i := 0; i < nRules; i++ {
		rules = append(rules, rule.Rule{
			ID:     fmt.Sprintf("r%d", i),
			Prefix: prefixes[rng.Intn(len(prefixes))],
			Kind:   rule.Kind(rng.Intn(3)),
			Days:   1 + rng.Intn(4),
			Keep:   rng.Intn(3),
		})
	}
	return randomCase{
		state:  state,
		rules:  rules,
		now:    int64(rng.Intn(900000)),
		maxVer: maxVer,
	}
}

func (tc randomCase) describe() string {
	var b strings.Builder
	fmt.Fprintf(&b, "now=%d rules=%v", tc.now, tc.rules)
	for _, k := range sortedKeys(tc.state) {
		fmt.Fprintf(&b, "\n  key %q:", k)
		for _, v := range tc.state[k] {
			fmt.Fprintf(&b, " %+v", v)
		}
	}
	return b.String()
}

func buildStore(t *testing.T, state map[string][]store.Version) *store.Store {
	t.Helper()
	st := store.New()
	for _, k := range sortedKeys(state) {
		for _, v := range state[k] {
			if err := st.Load(k, v); err != nil {
				t.Fatalf("Load(%q, %+v): %v", k, v, err)
			}
		}
	}
	return st
}

func mustRules(t *testing.T, rules []rule.Rule) *rule.Set {
	t.Helper()
	rs, err := rule.NewSet(rules...)
	if err != nil {
		t.Fatalf("NewSet(%v): %v", rules, err)
	}
	return rs
}

func TestRandomAgainstSimulation(t *testing.T) {
	rng := rand.New(rand.NewSource(20261004))
	for tc := 0; tc < 1500; tc++ {
		c := genCase(rng)
		wantEntries, wantState := simulate(c.state, c.rules, c.now, c.maxVer)
		costs := map[string]int64{}
		var totalCost int64
		for k, vs := range c.state {
			costs[k] = int64(len(vs))
			totalCost += int64(len(vs))
		}
		t.Logf("case %d input:%s", tc, c.describe())
		t.Logf("case %d want entries=%v final=%v (依据: 逐键三阶段朴素模拟)", tc, wantEntries, wantState)

		// 1) 一次 budget 无穷大的 Scan
		st := buildStore(t, c.state)
		rs := mustRules(t, c.rules)
		res, err := New(st, rs).Scan(c.now, MaxBudget)
		if err != nil {
			t.Fatalf("case %d big scan: %v\ninput:%s", tc, err, c.describe())
		}
		if res.Cursor != "" || res.Examined() != totalCost {
			t.Fatalf("case %d big scan cursor=%q examined=%d, want \"\" and %d",
				tc, res.Cursor, res.Examined(), totalCost)
		}
		if !reflect.DeepEqual(res.Entries, wantEntries) {
			t.Fatalf("case %d big scan entries =\n%v\nwant\n%v\ninput:%s",
				tc, res.Entries, wantEntries, c.describe())
		}
		if got := st.Snapshot(); !reflect.DeepEqual(got, wantState) {
			t.Fatalf("case %d big scan state =\n%v\nwant\n%v\ninput:%s",
				tc, got, wantState, c.describe())
		}
		// 完成后重扫：状态不变（被锁版本可能再次记 Blocked，但不得改动状态）。
		again, err := New(st, rs).Scan(c.now, MaxBudget)
		if err != nil {
			t.Fatalf("case %d rescan: %v", tc, err)
		}
		if got := st.Snapshot(); !reflect.DeepEqual(got, wantState) {
			t.Fatalf("case %d rescan changed state: %v -> %v", tc, wantState, got)
		}
		_ = again

		// 2) budget=1 反复调用直到空游标
		st1 := buildStore(t, c.state)
		sc1 := New(st1, rs)
		var got1 []Entry
		keys := sortedKeys(c.state)
		ptr := 0
		for {
			r1, err := sc1.Scan(c.now, 1)
			if err != nil {
				t.Fatalf("case %d budget=1: %v", tc, err)
			}
			if ptr >= len(keys) {
				t.Fatalf("case %d budget=1: extra call", tc)
			}
			if r1.Examined() != costs[keys[ptr]] {
				t.Fatalf("case %d budget=1 examined=%d, want cost of %q = %d",
					tc, r1.Examined(), keys[ptr], costs[keys[ptr]])
			}
			ptr++
			got1 = append(got1, r1.Entries...)
			if r1.Cursor == "" {
				break
			}
		}
		if ptr != len(keys) {
			t.Fatalf("case %d budget=1 processed %d keys, want %d", tc, ptr, len(keys))
		}
		if !reflect.DeepEqual(got1, wantEntries) {
			t.Fatalf("case %d budget=1 entries =\n%v\nwant\n%v\ninput:%s",
				tc, got1, wantEntries, c.describe())
		}
		if got := st1.Snapshot(); !reflect.DeepEqual(got, wantState) {
			t.Fatalf("case %d budget=1 state =\n%v\nwant\n%v", tc, got, wantState)
		}

		// 3) 随机 budget 序列
		st2 := buildStore(t, c.state)
		sc2 := New(st2, rs)
		var got2 []Entry
		ptr = 0
		for {
			budget := int64(1 + rng.Intn(20))
			r2, err := sc2.Scan(c.now, budget)
			if err != nil {
				t.Fatalf("case %d budget=%d: %v", tc, budget, err)
			}
			start := ptr
			if r2.Cursor == "" {
				ptr = len(keys)
			} else {
				idx := sort.SearchStrings(keys, r2.Cursor)
				if idx >= len(keys) || keys[idx] != r2.Cursor {
					t.Fatalf("case %d cursor %q not a key", tc, r2.Cursor)
				}
				ptr = idx
			}
			var wantExamined int64
			for _, k := range keys[start:ptr] {
				wantExamined += costs[k]
			}
			if r2.Examined() != wantExamined {
				t.Fatalf("case %d budget=%d examined=%d, want %d",
					tc, budget, r2.Examined(), wantExamined)
			}
			if start < ptr {
				if over := costs[keys[start]] - budget; over > 0 {
					if r2.Examined() > budget+over {
						t.Fatalf("case %d examined=%d exceeds budget+overflow", tc, r2.Examined())
					}
				} else if r2.Examined() > budget {
					t.Fatalf("case %d examined=%d exceeds budget=%d", tc, r2.Examined(), budget)
				}
			}
			got2 = append(got2, r2.Entries...)
			if r2.Cursor == "" {
				break
			}
		}
		if !reflect.DeepEqual(got2, wantEntries) {
			t.Fatalf("case %d random-budget entries =\n%v\nwant\n%v\ninput:%s",
				tc, got2, wantEntries, c.describe())
		}
		if got := st2.Snapshot(); !reflect.DeepEqual(got, wantState) {
			t.Fatalf("case %d random-budget state =\n%v\nwant\n%v", tc, got, wantState)
		}
		t.Logf("case %d ok: entries=%d finalKeys=%d", tc, len(wantEntries), len(wantState))
	}
}

// --- 并发冒烟：所有操作可并发调用（配合 -race） ---

func TestConcurrentSmoke(t *testing.T) {
	st := store.New()
	for i := 0; i < 8; i++ {
		key := fmt.Sprintf("k%d", i)
		if err := st.Load(key, store.Version{Ver: uint64(i*2 + 1), C: int64(i * 100)}); err != nil {
			t.Fatal(err)
		}
		if err := st.Load(key, store.Version{Ver: uint64(i*2 + 2), C: int64(i*100 + 50)}); err != nil {
			t.Fatal(err)
		}
	}
	rs := mustSet(t,
		rule.Rule{ID: "e", Kind: rule.Expire, Days: 1},
		rule.Rule{ID: "n", Kind: rule.NoncurrentExpire, Days: 1, Keep: 1},
		rule.Rule{ID: "o", Kind: rule.OrphanMarker, Days: 1})
	sc := New(st, rs)
	var wg sync.WaitGroup
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				if _, err := sc.Scan(int64(i*10000), 3); err != nil {
					t.Errorf("Scan: %v", err)
				}
			}
		}()
	}
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 30; i++ {
				_ = st.Load(fmt.Sprintf("g%d/%02d", g, i),
					store.Version{Ver: uint64(1000 + g*1000 + i), C: int64(i)})
				_ = st.Remove("k0", uint64(100+i))
			}
		}(g)
	}
	wg.Wait()
}
