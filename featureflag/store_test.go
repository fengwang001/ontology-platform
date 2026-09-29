package featureflag

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func testLogger(w io.Writer) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelDebug}))
}

// 发布与求值并发：求值过程中绝不允许新旧版本混用——
// 一次求值及其递归前置链上每个决策必须来自同一个版本。
func TestConcurrentPublishAndEval(t *testing.T) {
	store := NewStore(testLogger(io.Discard))
	if _, err := store.Publish(chainFlags()); err != nil {
		t.Fatalf("initial publish: %v", err)
	}

	var stop atomic.Bool
	var wg sync.WaitGroup

	// 发布者：在 c 开/关两个规则集之间切换发布固定批次，
	// 与读者的求值窗口充分重叠。
	wg.Add(1)
	go func() {
		defer wg.Done()
		on := chainFlags()
		off := chainFlags()
		off["c"].Enabled = false
		for i := 0; i < 500; i++ {
			if i%2 == 0 {
				store.Publish(on)
			} else {
				store.Publish(off)
			}
		}
		stop.Store(true)
	}()

	// 求值者：直接使用内部 evaluator，检查递归链版本一致。
	const readers = 8
	for r := 0; r < readers; r++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; !stop.Load() || i < 100; i++ {
				snap := store.current.Load()
				ev := newEvaluator(snap, fmt.Sprintf("u-%d-%d", seed, i), nil)
				res, err := ev.eval(context.Background(), "a")
				if err != nil {
					t.Errorf("eval: %v", err)
					return
				}
				for name, d := range ev.memo {
					if d.Version != snap.version {
						t.Errorf("mixed versions in one eval: top=%d %s=%d",
							snap.version, name, d.Version)
						return
					}
				}
				// 同一快照下，c 关闭则 a 必为前置失败 off；
				// c 开启则链路全通为 on。版本与结论必须自洽。
				if ev.memo["c"].Reason == ReasonDisabled && res.Variant != "off" {
					t.Errorf("inconsistent: c disabled but a=%s", res.Variant)
				}
				if ev.memo["c"].Variant == "on" && res.Variant != "on" {
					t.Errorf("inconsistent: c on but a=%s", res.Variant)
				}
			}
		}(r)
	}

	wg.Wait()

	// 发布返回后开始的求值必须看到新版本（线性可见性）。
	finalOff := chainFlags()
	finalOff["c"].Enabled = false
	wantV, err := store.Publish(finalOff)
	if err != nil {
		t.Fatalf("final publish: %v", err)
	}
	res, err := store.Eval(context.Background(), "a", "visibility", nil)
	if err != nil {
		t.Fatalf("final eval: %v", err)
	}
	if res.Version != wantV {
		t.Fatalf("eval after publish saw version %d, want %d", res.Version, wantV)
	}
	if res.Variant != "off" {
		t.Fatalf("eval after publish saw stale rules, variant=%s", res.Variant)
	}
}

// 日志必须打印输入、输出与判定依据（含递归前置链与桶号）。
func TestLogging(t *testing.T) {
	var buf bytes.Buffer
	store := NewStore(testLogger(&buf))
	if _, err := store.Publish(chainFlags()); err != nil {
		t.Fatalf("publish: %v", err)
	}
	buf.Reset()

	res, err := store.Eval(context.Background(), "a", "user-42",
		map[string]string{"tier": "vip"})
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	log := buf.String()
	for _, want := range []string{
		"featureflag eval",
		`input.flag=a`,
		`input.user_id=user-42`,
		`input.attrs=map[tier:vip]`,
		`output.variant=` + res.Variant,
		`output.reason=` + string(res.Reason),
		"decisions.b.", // 链中包含 b/c 的判定依据
		"decisions.c.",
		fmt.Sprintf("version=%d", res.Version),
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q\n--- log ---\n%s", want, log)
		}
	}

	// 发布被拒时也应有日志。
	buf.Reset()
	bad := chainFlags()
	bad["c"].Prerequisites = []Prerequisite{{Flag: "a", RequiredVariant: "on"}}
	if _, err := store.Publish(bad); err == nil {
		t.Fatal("expected rejection")
	}
	if !strings.Contains(buf.String(), "publish rejected") {
		t.Fatalf("rejection not logged:\n%s", buf.String())
	}
}

func chainFlags() RuleSet {
	alwaysOn := Rollout{Weights: []Weight{{"on", 10000}}}
	return RuleSet{
		"c": {Enabled: true, Variants: []string{"off", "on"}, OffVariant: "off", DefaultRollout: alwaysOn},
		"b": {
			Enabled: true, Variants: []string{"off", "on"}, OffVariant: "off",
			Prerequisites:  []Prerequisite{{Flag: "c", RequiredVariant: "on"}},
			DefaultRollout: alwaysOn,
		},
		"a": {
			Enabled: true, Variants: []string{"off", "on"}, OffVariant: "off",
			Prerequisites:  []Prerequisite{{Flag: "b", RequiredVariant: "on"}},
			DefaultRollout: alwaysOn,
		},
	}
}

// 成环（含自环）必须拒绝。
func TestPublishCycle(t *testing.T) {
	store := NewStore(testLogger(io.Discard))
	cyclic := chainFlags()
	cyclic["c"].Prerequisites = []Prerequisite{{Flag: "a", RequiredVariant: "on"}}
	if err := publishErr(store, cyclic); !errors.Is(err, ErrPrerequisiteCycle) {
		t.Fatalf("want cycle error, got %v", err)
	}

	self := chainFlags()
	self["b"].Prerequisites = []Prerequisite{{Flag: "b", RequiredVariant: "on"}}
	if err := publishErr(store, self); !errors.Is(err, ErrPrerequisiteCycle) {
		t.Fatalf("want self-cycle error, got %v", err)
	}
}

// 多因同时成立时按「变体 -> 负权重 -> 权重和 -> 未知前置」顺序只报第一个。
func TestPublishErrorPriority(t *testing.T) {
	store := NewStore(testLogger(io.Discard))
	multi := func() RuleSet {
		return RuleSet{
			"f": {
				Enabled: true, Variants: []string{"off", "a", "b"}, OffVariant: "off",
				Targeting:      []Rule{{Name: "bad", Variant: "ghost"}},
				DefaultRollout: Rollout{Weights: []Weight{{"a", -5}, {"b", 10005}}},
				Prerequisites:  []Prerequisite{{Flag: "missing", RequiredVariant: "on"}},
			},
		}
	}

	if err := publishErr(store, multi()); !errors.Is(err, ErrUnknownVariant) {
		t.Fatalf("priority: want unknown variant, got %v", err)
	}

	r1 := multi()
	r1["f"].Targeting[0].Variant = "a"
	if err := publishErr(store, r1); !errors.Is(err, ErrNegativeWeight) {
		t.Fatalf("priority: want negative weight, got %v", err)
	}

	r2 := multi()
	r2["f"].Targeting[0].Variant = "a"
	r2["f"].DefaultRollout.Weights = []Weight{{"a", 9000}, {"b", 2000}}
	if err := publishErr(store, r2); !errors.Is(err, ErrBadWeightSum) {
		t.Fatalf("priority: want bad sum, got %v", err)
	}

	r3 := multi()
	r3["f"].Targeting[0].Variant = "a"
	r3["f"].DefaultRollout.Weights = []Weight{{"a", 8000}, {"b", 2000}}
	if err := publishErr(store, r3); !errors.Is(err, ErrUnknownPrerequisite) {
		t.Fatalf("priority: want unknown prerequisite, got %v", err)
	}
}

// 被拒绝的发布不得改变当前生效版本；未知开关求值单独报错。
func TestRejectedPublishKeepsVersion(t *testing.T) {
	store := NewStore(testLogger(io.Discard))
	v, err := store.Publish(chainFlags())
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	cyclic := chainFlags()
	cyclic["c"].Prerequisites = []Prerequisite{{Flag: "a", RequiredVariant: "on"}}
	if _, err := store.Publish(cyclic); err == nil {
		t.Fatal("cyclic publish must be rejected")
	}
	if store.Version() != v {
		t.Fatalf("rejected publish changed version %d -> %d", v, store.Version())
	}
	if _, err := store.Eval(context.Background(), "a", "u", nil); err != nil {
		t.Fatalf("old version must still serve: %v", err)
	}

	if _, err := store.Eval(context.Background(), "nope", "u", nil); !errors.Is(err, ErrUnknownFlag) {
		t.Fatalf("unknown flag should error distinctly, got %v", err)
	}

	// 发布快照不受调用方后续修改影响（深拷贝隔离）。
	good := chainFlags()
	if _, err := store.Publish(good); err != nil {
		t.Fatalf("publish good: %v", err)
	}
	good["a"].Enabled = false
	delete(good, "b")
	res, err := store.Eval(context.Background(), "a", "u", nil)
	if err != nil {
		t.Fatalf("eval after caller mutation: %v", err)
	}
	if res.Variant != "on" {
		t.Fatalf("snapshot must be isolated from caller mutation, got %+v", res)
	}
}

// 三层前置链 a -> b -> c；中间层 b 关闭时：b 未启用直接返回关闭变体，
// a 因前置不满足也返回关闭变体（未启用开关不再下钻其前置 c），
// 且整次递归链上每个决策版本号一致。
func TestThreeLevelPrerequisiteChainMiddleOff(t *testing.T) {
	store := NewStore(testLogger(io.Discard))
	alwaysOn := Rollout{Weights: []Weight{{"on", 10000}}}
	rules := RuleSet{
		"c": {Enabled: true, Variants: []string{"off", "on"}, OffVariant: "off", DefaultRollout: alwaysOn},
		"b": {
			Enabled: false, Variants: []string{"off", "on"}, OffVariant: "off",
			Prerequisites:  []Prerequisite{{Flag: "c", RequiredVariant: "on"}},
			DefaultRollout: alwaysOn,
		},
		"a": {
			Enabled: true, Variants: []string{"off", "on"}, OffVariant: "off",
			Prerequisites:  []Prerequisite{{Flag: "b", RequiredVariant: "on"}},
			DefaultRollout: alwaysOn,
		},
	}
	v, err := store.Publish(rules)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	ev := newEvaluator(store.current.Load(), "u1", nil)
	res, err := ev.eval(context.Background(), "a")
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if res.Variant != "off" || res.Reason != ReasonPrerequisiteFailed ||
		res.MatchedRule != "prerequisite:b" {
		t.Fatalf("unexpected top result: %+v", res)
	}
	if got := ev.memo["b"]; got.Variant != "off" || got.Reason != ReasonDisabled {
		t.Fatalf("b should be disabled off, got %+v", got)
	}
	if _, evaluated := ev.memo["c"]; evaluated {
		t.Fatal("disabled flag must short-circuit and not evaluate its prerequisite c")
	}
	for name, r := range ev.memo {
		if r.Version != v {
			t.Fatalf("decision %s version %d, want %d", name, r.Version, v)
		}
	}

	rules["b"].Enabled = true
	if _, err := store.Publish(rules); err != nil {
		t.Fatalf("republish: %v", err)
	}
	res, err = store.Eval(context.Background(), "a", "u1", nil)
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if res.Variant != "on" || res.Reason != ReasonDefaultRollout {
		t.Fatalf("chain should pass after b enabled, got %+v", res)
	}
}

// 缺少所引用属性视为条件不满足：跳过该规则，继续后续规则/默认放量。
func TestMissingAttributeDoesNotMatch(t *testing.T) {
	store := NewStore(testLogger(io.Discard))
	rules := RuleSet{
		"f": {
			Enabled: true, Variants: []string{"off", "vip", "normal"}, OffVariant: "off",
			Targeting: []Rule{{
				Name:    "vip-rule",
				When:    []Condition{{Attribute: "tier", Op: OpEqual, Value: "vip"}},
				Variant: "vip",
			}},
			DefaultRollout: Rollout{Weights: []Weight{{"normal", 10000}}},
		},
	}
	if _, err := store.Publish(rules); err != nil {
		t.Fatalf("publish: %v", err)
	}

	res, err := store.Eval(context.Background(), "f", "u", map[string]string{})
	if err != nil {
		t.Fatalf("eval: %v", err)
	}
	if res.Variant != "normal" || res.Reason != ReasonDefaultRollout {
		t.Fatalf("missing attr should skip rule, got %+v", res)
	}

	res, _ = store.Eval(context.Background(), "f", "u", map[string]string{"tier": "free"})
	if res.Variant != "normal" {
		t.Fatalf("non-matching attr should skip rule, got %+v", res)
	}

	res, _ = store.Eval(context.Background(), "f", "u", map[string]string{"tier": "vip"})
	if res.Variant != "vip" || res.Reason != ReasonTargeting || res.MatchedRule != "vip-rule" {
		t.Fatalf("matching attr should hit rule, got %+v", res)
	}

	rules["f"].Targeting = []Rule{{
		Name: "r1",
		When: []Condition{
			{Attribute: "tier", Op: OpIn, Values: []string{"a", "b"}},
			{Attribute: "city", Op: OpEqual, Value: "sh"},
		},
		Variant: "vip",
	}}
	if _, err := store.Publish(rules); err != nil {
		t.Fatalf("publish: %v", err)
	}
	res, _ = store.Eval(context.Background(), "f", "u", map[string]string{"tier": "a"})
	if res.Variant != "normal" {
		t.Fatalf("one of two conditions missing should not match, got %+v", res)
	}
	res, _ = store.Eval(context.Background(), "f", "u", map[string]string{"tier": "b", "city": "sh"})
	if res.Variant != "vip" {
		t.Fatalf("both conditions should match, got %+v", res)
	}
}

func rolloutFlag() *SwitchDef {
	return &SwitchDef{
		Enabled:        true,
		Variants:       []string{"off", "a", "b"},
		OffVariant:     "off",
		DefaultRollout: Rollout{Weights: []Weight{{"a", 5000}, {"b", 5000}}},
	}
}

func publishErr(store *Store, rules RuleSet) error {
	_, err := store.Publish(rules)
	return err
}

// 权重从后一个变体挪给前一个变体（a 5000 -> 7000，b 5000 -> 3000），
// 原先落在前一个变体 a 的一万名用户必须全部不变。
func TestWeightMoveNoDrift(t *testing.T) {
	store := NewStore(testLogger(io.Discard))

	v1, err := store.Publish(RuleSet{"feat": rolloutFlag()})
	if err != nil {
		t.Fatalf("publish v1: %v", err)
	}

	const n = 10000
	before := make([]string, n)
	buckets := make([]int, n)
	for i := 0; i < n; i++ {
		uid := fmt.Sprintf("user-%05d", i)
		res, err := store.Eval(context.Background(), "feat", uid, nil)
		if err != nil {
			t.Fatalf("eval before: %v", err)
		}
		before[i] = res.Variant
		buckets[i] = res.Bucket
		if res.Version != v1 || res.Reason != ReasonDefaultRollout {
			t.Fatalf("unexpected result: %+v", res)
		}
	}

	moved := rolloutFlag()
	moved.DefaultRollout = Rollout{Weights: []Weight{{"a", 7000}, {"b", 3000}}}
	v2, err := store.Publish(RuleSet{"feat": moved})
	if err != nil {
		t.Fatalf("publish v2: %v", err)
	}
	if v2 != v1+1 {
		t.Fatalf("version = %d, want %d", v2, v1+1)
	}

	var changed int
	for i := 0; i < n; i++ {
		uid := fmt.Sprintf("user-%05d", i)
		res, err := store.Eval(context.Background(), "feat", uid, nil)
		if err != nil {
			t.Fatalf("eval after: %v", err)
		}
		if res.Version != v2 {
			t.Fatalf("version = %d, want %d", res.Version, v2)
		}
		if before[i] == "a" && res.Variant != "a" {
			t.Fatalf("user %s bucket %d drifted a -> %s", uid, buckets[i], res.Variant)
		}
		if res.Variant != before[i] {
			changed++
			if before[i] != "b" || res.Variant != "a" {
				t.Fatalf("unexpected move %s -> %s for %s bucket %d",
					before[i], res.Variant, uid, buckets[i])
			}
		}
	}
	if changed == 0 {
		t.Fatal("weight change had no effect, test is not meaningful")
	}

	// 同一版本与输入反复求值结果完全相同。
	for i := 0; i < 50; i++ {
		uid := fmt.Sprintf("user-%05d", i*97%n)
		r1, _ := store.Eval(context.Background(), "feat", uid, nil)
		r2, _ := store.Eval(context.Background(), "feat", uid, nil)
		if *r1 != *r2 {
			t.Fatalf("determinism violated for %s: %+v vs %+v", uid, r1, r2)
		}
	}
}
