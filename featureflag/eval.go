package featureflag

import (
	"fmt"
	"log/slog"
	"reflect"
)

// EvalError 表示求值时的错误，例如引用了未知开关。
type EvalError struct {
	Flag string
	Msg  string
}

func (e *EvalError) Error() string {
	return fmt.Sprintf("featureflag: evaluate flag %q: %s", e.Flag, e.Msg)
}

// Result 是一次求值的结果。同一版本与同一输入反复求值必然得到相同结果。
type Result struct {
	Variant string
	Version int
	// Reason 是机器可读的命中原因：off / prerequisite_failed /
	// rule:<name>:variant / rule:<name>:rollout / default:rollout / default:off。
	Reason string
}

// EvalInput 是求值输入。Attributes 中缺失的属性视为「属性不存在」。
type EvalInput struct {
	UserID     string
	Attributes map[string]any
}

// Logger 用于记录每次求值的输入、输出与判定依据。
var Logger *slog.Logger = slog.Default()

// Evaluate 按当前生效版本为用户求某个开关的变体。
func (s *Store) Evaluate(flagKey string, in EvalInput) (Result, error) {
	// 一次求值（连同递归的前置开关）只持有同一版快照。
	snap := s.snapshot.Load()
	return evaluateOn(snap, flagKey, in, map[string]bool{})
}

func evaluateOn(snap *snapshot, flagKey string, in EvalInput, stack map[string]bool) (Result, error) {
	flag, ok := snap.flags[flagKey]
	if !ok {
		return Result{}, &EvalError{Flag: flagKey, Msg: "unknown flag"}
	}

	out := Result{Version: snap.version}

	if !flag.enabled {
		out.Variant = flag.offVariant
		out.Reason = "off"
		logEval(snap.version, flagKey, in, out, "flag disabled, returning off variant")
		return out, nil
	}

	for _, pre := range flag.prerequisites {
		if stack[pre.Flag] {
			// 已发布规则集经过成环校验，正常不会走到这里。
			return Result{}, &EvalError{Flag: flagKey, Msg: "prerequisite cycle in published ruleset"}
		}
		stack[pre.Flag] = true
		res, err := evaluateOn(snap, pre.Flag, in, stack)
		delete(stack, pre.Flag)
		if err != nil {
			return Result{}, err
		}
		if res.Variant != pre.Variant {
			out.Variant = flag.offVariant
			out.Reason = "prerequisite_failed:" + pre.Flag
			logEval(snap.version, flagKey, in, out,
				"prerequisite flag %q resolved %q, required %q", pre.Flag, res.Variant, pre.Variant)
			return out, nil
		}
	}

	for ri := range flag.rules {
		rule := &flag.rules[ri]
		if !allConditionsMatch(rule.when, in.Attributes) {
			continue
		}
		if len(rule.rollout) == 0 {
			out.Variant = rule.variant
			out.Reason = "rule:" + rule.name + ":variant"
			logEval(snap.version, flagKey, in, out, "matched rule %q, fixed variant", rule.name)
			return out, nil
		}
		b := bucket(flagKey, in.UserID)
		v, idx := pickRollout(rule.rollout, b)
		out.Variant = v
		out.Reason = "rule:" + rule.name + ":rollout"
		logEval(snap.version, flagKey, in, out,
			"matched rule %q, bucket=%d variant_index=%d", rule.name, b, idx)
		return out, nil
	}

	if len(flag.defaultRoll) > 0 {
		b := bucket(flagKey, in.UserID)
		v, idx := pickRollout(flag.defaultRoll, b)
		out.Variant = v
		out.Reason = "default:rollout"
		logEval(snap.version, flagKey, in, out,
			"no rule matched, default rollout bucket=%d variant_index=%d", b, idx)
		return out, nil
	}

	out.Variant = flag.offVariant
	out.Reason = "default:off"
	logEval(snap.version, flagKey, in, out, "no rule matched and no default rollout, returning off variant")
	return out, nil
}

// allConditionsMatch 要求所有条件满足；缺少所引用属性视为不满足。
func allConditionsMatch(conds []Condition, attrs map[string]any) bool {
	for _, c := range conds {
		val, present := attrs[c.Attribute]
		if !present {
			return false
		}
		if !conditionHolds(c, val) {
			return false
		}
	}
	return true
}

func conditionHolds(c Condition, val any) bool {
	switch c.Operator {
	case OpEqual:
		return len(c.Values) == 1 && reflect.DeepEqual(val, c.Values[0])
	case OpNotEqual:
		return len(c.Values) == 1 && !reflect.DeepEqual(val, c.Values[0])
	case OpIn:
		for _, want := range c.Values {
			if reflect.DeepEqual(val, want) {
				return true
			}
		}
		return false
	default:
		return false
	}
}

// pickRollout 按累计区间选桶：区间为 [prevEnd, end)。
// 把权重从后一个变体挪给前一个变体时只扩大前一个区间的右边界，
// 原区间内用户保持原变体，不发生漂移。
func pickRollout(roll []compiledRollout, b int) (string, int) {
	for i, r := range roll {
		if b < r.end {
			return r.variant, i
		}
	}
	last := roll[len(roll)-1]
	return last.variant, len(roll) - 1
}

func logEval(version int, flagKey string, in EvalInput, out Result, format string, args ...any) {
	if Logger == nil {
		return
	}
	Logger.Info("featureflag evaluate",
		slog.Int("version", version),
		slog.String("flag", flagKey),
		slog.String("user_id", in.UserID),
		slog.Any("attributes", in.Attributes),
		slog.String("variant", out.Variant),
		slog.String("reason", out.Reason),
		slog.String("basis", fmt.Sprintf(format, args...)),
	)
}
