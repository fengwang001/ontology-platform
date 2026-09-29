package featureflag

import (
	"bytes"
	"log/slog"
	"strings"
	"sync"
	"testing"
)

// 成环拒绝，以及多因同时成立时按优先级只报第一个。
func TestPublishValidationPriority(t *testing.T) {
	s := NewStore()

	// 单纯成环 a<->b。
	_, err := s.Publish(SpecSet{
		"a": {Enabled: true, OffVariant: "off", Variants: []string{"off", "on"},
			Prerequisites: []Prerequisite{{Flag: "b", Variant: "off"}}},
		"b": {Enabled: true, OffVariant: "off", Variants: []string{"off", "on"},
			Prerequisites: []Prerequisite{{Flag: "a", Variant: "off"}}},
	})
	if code := errCode(err); code != ErrPrerequisiteCycle {
		t.Fatalf("cycle err code=%v err=%v", code, err)
	}

	// 多因同时成立：未知变体 + 负权重 + 未知前置，必须报第一个（未知变体）。
	_, err = s.Publish(SpecSet{
		"a": {
			Enabled:    true,
			OffVariant: "off",
			Variants:   []string{"off", "on"},
			Rules: []Rule{{
				Name:    "bad",
				Rollout: []RolloutWeight{{Variant: "ghost", Weight: -1}},
			}},
			Prerequisites: []Prerequisite{{Flag: "missing", Variant: "off"}},
		},
	})
	if code := errCode(err); code != ErrVariantNotFound {
		t.Fatalf("priority 1: code=%v err=%v", code, err)
	}

	// 修掉变体问题后，应报权重问题。
	_, err = s.Publish(SpecSet{
		"a": {
			Enabled:    true,
			OffVariant: "off",
			Variants:   []string{"off", "on"},
			Rules: []Rule{{
				Name:    "bad",
				Variant: "on",
				Rollout: []RolloutWeight{{Variant: "on", Weight: -1}},
			}},
			Prerequisites: []Prerequisite{{Flag: "missing", Variant: "off"}},
		},
	})
	if code := errCode(err); code != ErrInvalidWeights {
		t.Fatalf("priority 2: code=%v err=%v", code, err)
	}

	// 修掉权重后，应报前置不存在。
	_, err = s.Publish(SpecSet{
		"a": {
			Enabled:    true,
			OffVariant: "off",
			Variants:   []string{"off", "on"},
			Rules: []Rule{{
				Name:    "bad",
				Variant: "on",
				Rollout: []RolloutWeight{{Variant: "on", Weight: 10000}},
			}},
			Prerequisites: []Prerequisite{{Flag: "missing", Variant: "off"}},
		},
	})
	if code := errCode(err); code != ErrPrerequisiteNotFound {
		t.Fatalf("priority 3: code=%v err=%v", code, err)
	}

	// 修掉未知前置后，环 a<->b 浮现。
	_, err = s.Publish(SpecSet{
		"a": {
			Enabled:        true,
			OffVariant:     "off",
			Variants:       []string{"off", "on"},
			DefaultRollout: rw([2]any{"on", 10000}),
			Prerequisites:  []Prerequisite{{Flag: "b", Variant: "off"}}},
		"b": {Enabled: true, OffVariant: "off", Variants: []string{"off", "on"},
			Prerequisites: []Prerequisite{{Flag: "a", Variant: "off"}}},
	})
	if code := errCode(err); code != ErrPrerequisiteCycle {
		t.Fatalf("priority 4: code=%v err=%v", code, err)
	}

	// 权重之和不为 10000。
	_, err = s.Publish(SpecSet{
		"a": {
			Enabled:        true,
			OffVariant:     "off",
			Variants:       []string{"off", "on"},
			DefaultRollout: rw([2]any{"on", 9999}),
		},
	})
	if code := errCode(err); code != ErrInvalidWeights {
		t.Fatalf("sum: code=%v err=%v", code, err)
	}

	// 前置要求的变体在被引用开关上不存在，属于「变体引用不存在」。
	_, err = s.Publish(SpecSet{
		"a": {Enabled: true, OffVariant: "off", Variants: []string{"off", "on"},
			Prerequisites: []Prerequisite{{Flag: "b", Variant: "b_on"}}},
		"b": {Enabled: true, OffVariant: "b_off", Variants: []string{"b_off"}},
	})
	if code := errCode(err); code != ErrVariantNotFound {
		t.Fatalf("prereq variant: code=%v err=%v", code, err)
	}
}

// 被拒绝的发布不得改变当前生效版本。
func TestRejectedPublishKeepsVersion(t *testing.T) {
	s := NewStore()
	v1 := mustPublish(t, s, SpecSet{
		"exp": {
			Enabled:        true,
			OffVariant:     "off",
			Variants:       []string{"off", "a", "b"},
			DefaultRollout: rw([2]any{"a", 5000}, [2]any{"b", 5000}),
		},
	})
	if v1 != 1 {
		t.Fatalf("first version=%d, want 1", v1)
	}

	v, err := s.Publish(SpecSet{
		"exp": {
			Enabled:        true,
			OffVariant:     "off",
			Variants:       []string{"off", "a", "b"},
			DefaultRollout: rw([2]any{"a", 4000}, [2]any{"b", 4000}),
		},
	})
	if errCode(err) != ErrInvalidWeights {
		t.Fatalf("want invalid weights, got %v", err)
	}
	if v != 1 || s.Version() != 1 {
		t.Fatalf("version after rejected publish: ret=%d store=%d, want 1/1", v, s.Version())
	}

	// 当前版本仍可正常求值。
	res, err := s.Evaluate("exp", EvalInput{UserID: "u"})
	if err != nil || res.Version != 1 {
		t.Fatalf("eval after rejection: %+v err=%v", res, err)
	}

	// 再次合法发布，版本号只增加一次。
	if v := mustPublish(t, s, SpecSet{
		"exp": {
			Enabled:        true,
			OffVariant:     "off",
			Variants:       []string{"off", "a", "b"},
			DefaultRollout: rw([2]any{"a", 10000}),
		},
	}); v != 2 {
		t.Fatalf("version after next valid publish=%d, want 2", v)
	}
}

// 同一版本与输入反复求值结果完全相同。
func TestDeterministicSameVersion(t *testing.T) {
	s := NewStore()
	mustPublish(t, s, SpecSet{
		"exp": {
			Enabled:    true,
			OffVariant: "off",
			Variants:   []string{"off", "a", "b"},
			Rules: []Rule{{
				Name:       "vip",
				Conditions: []Condition{{Attribute: "tier", Operator: OpIn, Values: []any{"gold", "plat"}}},
				Rollout:    rw([2]any{"a", 3000}, [2]any{"b", 7000}),
			}},
			DefaultRollout: rw([2]any{"a", 5000}, [2]any{"b", 5000}),
		},
	})

	first, err := s.Evaluate("exp", EvalInput{UserID: "dup-user", Attributes: map[string]any{"tier": "gold"}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		res, err := s.Evaluate("exp", EvalInput{UserID: "dup-user", Attributes: map[string]any{"tier": "gold"}})
		if err != nil || res != first {
			t.Fatalf("iteration %d: %+v err=%v, first=%+v", i, res, err, first)
		}
	}
}

// 并发发布与求值：每次求值连同前置开关必须只落在同一版本，
// 不出现「主开关 vN + 前置 vM」的新旧混用；发布返回后的求值必须看到新版本。
func TestConcurrentPublishAndEvaluate(t *testing.T) {
	s := NewStore()
	mustPublish(t, s, chainedSpec(true))

	var wg sync.WaitGroup
	stop := make(chan struct{})

	// 求值方：不断对三层链求值。发布序列版本 n 满足：奇数版本全链开启，
	// 偶数版本中间层 b 关闭。若一次求值（含递归前置）混用了新旧版本，
	// 结果的 (version, variant, reason) 就会打破这个奇偶对应关系。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			res, err := s.Evaluate("a", EvalInput{UserID: "u"})
			if err != nil {
				t.Errorf("concurrent eval: %v", err)
				return
			}
			switch res.Version % 2 {
			case 1:
				if res.Variant != "a_on" || res.Reason != "rule:always:variant" {
					t.Errorf("version %d should be all-on, got %q/%q",
						res.Version, res.Variant, res.Reason)
					return
				}
			case 0:
				if res.Variant != "a_off" || res.Reason != "prerequisite_failed:b" {
					t.Errorf("version %d should have middle disabled, got %q/%q",
						res.Version, res.Variant, res.Reason)
					return
				}
			}
		}
	}()

	// 发布方：交替发布「全链开启」与「中间层关闭」两个版本。
	// v1 已在上方发布（全链开启）；此后奇数版本开启、偶数版本关闭。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for v := 2; v <= 201; v++ {
			_, err := s.Publish(chainedSpec(v%2 == 1))
			if err != nil {
				t.Errorf("concurrent publish: %v", err)
				return
			}
		}
		close(stop)
	}()

	wg.Wait()

	// 发布全部返回后，新求值必须看到最后一版（i=199 为奇数，b 关闭）。
	last := s.Version()
	res, err := s.Evaluate("a", EvalInput{UserID: "u"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Version != last || last != 201 || res.Variant != "a_on" || res.Reason != "rule:always:variant" {
		t.Fatalf("after publish: %+v last=%d, want version 201 a_on/rule:always:variant", res, last)
	}
}

func chainedSpec(bEnabled bool) SpecSet {
	return SpecSet{
		"c": {
			Enabled: true, OffVariant: "c_off", Variants: []string{"c_off", "c_on"},
			Rules: []Rule{{Name: "always", Variant: "c_on"}},
		},
		"b": {
			Enabled: bEnabled, OffVariant: "b_off", Variants: []string{"b_off", "b_on"},
			Prerequisites: []Prerequisite{{Flag: "c", Variant: "c_on"}},
			Rules:         []Rule{{Name: "always", Variant: "b_on"}},
		},
		"a": {
			Enabled: true, OffVariant: "a_off", Variants: []string{"a_off", "a_on"},
			Prerequisites: []Prerequisite{{Flag: "b", Variant: "b_on"}},
			Rules:         []Rule{{Name: "always", Variant: "a_on"}},
		},
	}
}

// 日志需打印输入、输出与判定依据。
func TestEvaluationLogging(t *testing.T) {
	var buf bytes.Buffer
	old := Logger
	Logger = slog.New(slog.NewTextHandler(&buf, nil))
	t.Cleanup(func() { Logger = old })

	s := NewStore()
	mustPublish(t, s, SpecSet{
		"exp": {
			Enabled:    true,
			OffVariant: "off",
			Variants:   []string{"off", "on"},
			Rules: []Rule{{
				Name:       "r",
				Conditions: []Condition{{Attribute: "tier", Operator: OpEqual, Values: []any{"gold"}}},
				Variant:    "on",
			}},
		},
	})

	_, err := s.Evaluate("exp", EvalInput{UserID: "user-42", Attributes: map[string]any{"tier": "gold"}})
	if err != nil {
		t.Fatal(err)
	}
	log := buf.String()
	for _, want := range []string{
		`user_id=user-42`,
		`flag=exp`,
		`variant=on`,
		`reason=rule:r:variant`,
		"matched rule",
		"attributes=",
	} {
		if !strings.Contains(log, want) {
			t.Fatalf("log missing %q:\n%s", want, log)
		}
	}
}
