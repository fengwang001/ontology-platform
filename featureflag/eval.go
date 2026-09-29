package featureflag

import "context"

// evaluator 绑定单个不可变快照；一次顶层求值及其全部递归前置
// 都通过同一个 evaluator 完成，因此只看同一版规则集。
type evaluator struct {
	snap   *snapshot
	userID string
	attrs  map[string]string

	// memo 缓存本次顶层求值中每个开关的结果，
	// 既避免重复计算，也保证菱形依赖下结果一致。
	memo map[string]*EvalResult
}

func newEvaluator(snap *snapshot, userID string, attrs map[string]string) *evaluator {
	return &evaluator{snap: snap, userID: userID, attrs: attrs, memo: make(map[string]*EvalResult)}
}

func (e *evaluator) eval(ctx context.Context, flag string) (*EvalResult, error) {
	if r, ok := e.memo[flag]; ok {
		return r, nil
	}
	sw, ok := e.snap.rules[flag]
	if !ok {
		return nil, unknownFlagError(flag)
	}

	res := &EvalResult{Flag: flag, Version: e.snap.version, Bucket: -1}

	// 1) 未启用：直接返回关闭变体。
	if !sw.Enabled {
		res.Variant = sw.OffVariant
		res.Reason = ReasonDisabled
		e.memo[flag] = res
		return res, nil
	}

	// 2) 前置开关按序求值；任一结果不等于要求变体即返回关闭变体。
	for _, p := range sw.Prerequisites {
		pre, err := e.eval(ctx, p.Flag)
		if err != nil {
			return nil, err
		}
		if pre.Variant != p.RequiredVariant {
			res.Variant = sw.OffVariant
			res.Reason = ReasonPrerequisiteFailed
			res.MatchedRule = "prerequisite:" + p.Flag
			e.memo[flag] = res
			return res, nil
		}
	}

	// 3) 取首个条件全部满足的定向规则；缺少所引用属性视为不满足。
	for i := range sw.Targeting {
		rule := &sw.Targeting[i]
		if !e.matchAll(rule.When) {
			continue
		}
		res.MatchedRule = rule.Name
		if rule.Variant != "" {
			res.Variant = rule.Variant
			res.Reason = ReasonTargeting
		} else {
			res.Reason = ReasonTargetingRollout
			res.Bucket = bucket(flag, e.userID)
			res.Variant = pickVariant(rule.Rollout.Weights, res.Bucket)
		}
		e.memo[flag] = res
		return res, nil
	}

	// 4) 默认放量。
	res.Reason = ReasonDefaultRollout
	res.Bucket = bucket(flag, e.userID)
	res.Variant = pickVariant(sw.DefaultRollout.Weights, res.Bucket)
	e.memo[flag] = res
	return res, nil
}

func (e *evaluator) matchAll(conds []Condition) bool {
	for _, c := range conds {
		raw, ok := e.attrs[c.Attribute]
		if !ok {
			// 缺少所引用属性：条件不满足。
			return false
		}
		switch c.Op {
		case OpEqual:
			if raw != c.Value {
				return false
			}
		case OpIn:
			found := false
			for _, v := range c.Values {
				if raw == v {
					found = true
					break
				}
			}
			if !found {
				return false
			}
		default:
			return false
		}
	}
	return true
}

type unknownFlagErr struct{ flag string }

func (e *unknownFlagErr) Error() string { return ErrUnknownFlag.Error() + ": " + e.flag }
func (e *unknownFlagErr) Unwrap() error { return ErrUnknownFlag }

func unknownFlagError(flag string) error { return &unknownFlagErr{flag: flag} }
