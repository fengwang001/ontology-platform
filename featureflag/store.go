package featureflag

import (
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
)

// Store 支持规则集发布与求值的并发访问。
// 一次求值只看到某一版不可变快照；被拒绝的发布不会改变当前版本。
type Store struct {
	snapshot atomic.Pointer[snapshot]
	pubMu    struct{ sync.Mutex }
}

type snapshot struct {
	version int
	flags   map[string]compiledFlag
}

type compiledRollout struct {
	variant string
	end     int // 区间为 (prevEnd, end]
}

type compiledRule struct {
	name    string
	when    []Condition
	variant string
	rollout []compiledRollout
}

type compiledFlag struct {
	enabled       bool
	offVariant    string
	prerequisites []Prerequisite
	rules         []compiledRule
	defaultRoll   []compiledRollout
}

// NewStore 创建一个空 Store，初始版本号为 0（无任何开关）。
func NewStore() *Store {
	s := &Store{}
	s.snapshot.Store(&snapshot{version: 0, flags: map[string]compiledFlag{}})
	return s
}

// Version 返回当前生效规则集的版本号。
func (s *Store) Version() int {
	return s.snapshot.Load().version
}

// Publish 校验并整体替换规则集。校验失败时当前生效版本保持不变。
func (s *Store) Publish(spec SpecSet) (int, error) {
	compiled, err := compile(spec)
	if err != nil {
		// 校验失败：不触碰当前快照，当前生效版本保持不变。
		return s.Version(), err
	}
	s.pubMu.Lock()
	next := &snapshot{version: s.snapshot.Load().version + 1, flags: compiled}
	s.snapshot.Store(next)
	version := next.version
	s.pubMu.Unlock()
	return version, nil
}

// validate 按规定优先级检查整个规则集，多因同时成立时只返回第一个：
// 1) 变体引用不存在（含可解析前置所要求的变体）
// 2) 权重为负或之和不为 10000
// 3) 前置开关不存在
// 4) 前置依赖成环
// 开关按键排序，保证多因并存时返回的错误确定。
func validate(spec SpecSet) error {
	keys := make([]string, 0, len(spec))
	for k := range spec {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	known := make(map[string]map[string]bool, len(spec))
	for _, k := range keys {
		set := make(map[string]bool, len(spec[k].Variants))
		for _, v := range spec[k].Variants {
			set[v] = true
		}
		known[k] = set
	}

	// 第一轮：变体引用与权重。引用了不存在的前置开关时，其要求的变体
	// 留待第三轮（前置不存在）报出。
	for _, k := range keys {
		flag := spec[k]
		variants := known[k]
		if !variants[flag.OffVariant] {
			return pubErr(ErrVariantNotFound, k, "off variant %q is not declared", flag.OffVariant)
		}
		for ri, rule := range flag.Rules {
			if len(rule.Rollout) == 0 {
				if !variants[rule.Variant] {
					return pubErr(ErrVariantNotFound, k,
						"rule %d (%q) refers to undeclared variant %q", ri, rule.Name, rule.Variant)
				}
				continue
			}
			for wi, w := range rule.Rollout {
				if !variants[w.Variant] {
					return pubErr(ErrVariantNotFound, k,
						"rule %d (%q) rollout entry %d refers to undeclared variant %q",
						ri, rule.Name, wi, w.Variant)
				}
			}
			if err := checkWeights(k, "rule "+strconv.Quote(rule.Name), rule.Rollout); err != nil {
				return err
			}
		}
		if flag.DefaultRollout != nil {
			for wi, w := range flag.DefaultRollout {
				if !variants[w.Variant] {
					return pubErr(ErrVariantNotFound, k,
						"default rollout entry %d refers to undeclared variant %q", wi, w.Variant)
				}
			}
			if err := checkWeights(k, "default rollout", flag.DefaultRollout); err != nil {
				return err
			}
		}
		for _, pre := range flag.Prerequisites {
			if _, ok := spec[pre.Flag]; ok {
				if !known[pre.Flag][pre.Variant] {
					return pubErr(ErrVariantNotFound, k,
						"prerequisite requires variant %q that is not declared by flag %q",
						pre.Variant, pre.Flag)
				}
			}
		}
	}

	// 第二轮（优先级 3）：前置开关不存在。
	for _, k := range keys {
		for _, pre := range spec[k].Prerequisites {
			if _, ok := spec[pre.Flag]; !ok {
				return pubErr(ErrPrerequisiteNotFound, k,
					"prerequisite flag %q does not exist in this ruleset", pre.Flag)
			}
		}
	}

	// 第三轮（优先级 4）：前置依赖成环。
	if err := detectCycle(spec, keys); err != nil {
		return err
	}
	return nil
}

func checkWeights(flag, where string, weights []RolloutWeight) error {
	sum := 0
	for _, w := range weights {
		if w.Weight < 0 {
			return pubErr(ErrInvalidWeights, flag,
				"%s has negative weight %d for variant %q", where, w.Weight, w.Variant)
		}
		sum += w.Weight
	}
	if sum != 10000 {
		return pubErr(ErrInvalidWeights, flag,
			"%s weights sum to %d, want 10000", where, sum)
	}
	return nil
}

// detectCycle 以 DFS 三色标记检测前置图中的环，按键序保证结果确定。
func detectCycle(spec SpecSet, keys []string) error {
	const white, gray, black = 0, 1, 2
	color := make(map[string]int, len(spec))
	var stack []string

	var visit func(string) error
	visit = func(k string) error {
		color[k] = gray
		stack = append(stack, k)
		pres := append([]Prerequisite(nil), spec[k].Prerequisites...)
		sort.Slice(pres, func(i, j int) bool { return pres[i].Flag < pres[j].Flag })
		for _, pre := range pres {
			switch color[pre.Flag] {
			case white:
				if err := visit(pre.Flag); err != nil {
					return err
				}
			case gray:
				cycle := []string{pre.Flag}
				for i := len(stack) - 1; i >= 0; i-- {
					cycle = append(cycle, stack[i])
					if stack[i] == pre.Flag {
						break
					}
				}
				return pubErr(ErrPrerequisiteCycle, k,
					"prerequisite cycle detected: %v", cycle)
			}
		}
		stack = stack[:len(stack)-1]
		color[k] = black
		return nil
	}

	for _, k := range keys {
		if color[k] == white {
			if err := visit(k); err != nil {
				return err
			}
		}
	}
	return nil
}

func compile(spec SpecSet) (map[string]compiledFlag, error) {
	if err := validate(spec); err != nil {
		return nil, err
	}
	keys := make([]string, 0, len(spec))
	for k := range spec {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make(map[string]compiledFlag, len(spec))
	for _, k := range keys {
		flag := spec[k]
		cf := compiledFlag{
			enabled:       flag.Enabled,
			offVariant:    flag.OffVariant,
			prerequisites: append([]Prerequisite(nil), flag.Prerequisites...),
		}
		for _, rule := range flag.Rules {
			cr := compiledRule{
				name:    rule.Name,
				when:    append([]Condition(nil), rule.Conditions...),
				variant: rule.Variant,
				rollout: compileRollout(rule.Rollout),
			}
			cf.rules = append(cf.rules, cr)
		}
		cf.defaultRoll = compileRollout(flag.DefaultRollout)
		out[k] = cf
	}
	return out, nil
}

// compileRollout 把权重按声明顺序编译成累计右边界区间。
// 第 i 个变体覆盖桶 [prevEnd, end)；把权重从后一个变体挪给前一个变体时，
// 前一个变体的左边界不变，因此原本落入它的用户不会漂移。
func compileRollout(weights []RolloutWeight) []compiledRollout {
	if len(weights) == 0 {
		return nil
	}
	compiled := make([]compiledRollout, len(weights))
	end := 0
	for i, w := range weights {
		end += w.Weight
		compiled[i] = compiledRollout{variant: w.Variant, end: end}
	}
	return compiled
}
