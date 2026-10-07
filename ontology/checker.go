package ontology

import (
	"fmt"
	"strings"
)

// IncompatCategory 是不兼容原因的类别，按位组合。
// 一次打包提交可同时触发多类，全部类别都会被记录与返回。
type IncompatCategory uint32

const (
	// InstanceConstraintViolation 既有存活实例取值不满足收紧后的约束。
	InstanceConstraintViolation IncompatCategory = 1 << iota
	// IrreversibleTypeConversion 旧类型取值无法无损重解释为新类型。
	IrreversibleTypeConversion
	// MissingValueNotAllowed 新增字段不带默认且不允许缺失，又无回填规则。
	MissingValueNotAllowed
	// ExternalSemanticDrift 外部引用方（链接/动作）依赖的字段语义发生漂移。
	ExternalSemanticDrift
)

func (c IncompatCategory) Has(o IncompatCategory) bool { return c&o != 0 }

func (c IncompatCategory) String() string {
	var parts []string
	if c.Has(InstanceConstraintViolation) {
		parts = append(parts, "instance-constraint-violation")
	}
	if c.Has(IrreversibleTypeConversion) {
		parts = append(parts, "irreversible-type-conversion")
	}
	if c.Has(MissingValueNotAllowed) {
		parts = append(parts, "missing-value-not-allowed")
	}
	if c.Has(ExternalSemanticDrift) {
		parts = append(parts, "external-semantic-drift")
	}
	if len(parts) == 0 {
		return "none"
	}
	return strings.Join(parts, "|")
}

// Decision 是一次变更判定的完整记录：变更内容、检查依据与结论，供事后核对。
type Decision struct {
	Seq        int64 // 在引擎全序日志中的序号
	Change     FieldChange
	Kind       ChangeKind
	Compatible bool
	// Categories 命中的全部不兼容类别（位掩码），兼容时为 0。
	Categories IncompatCategory
	// CheckedInstances 本次判定实际检查的存活实例数量，
	// 保证不超过该字段所属对象类型当前存活实例总数。
	CheckedInstances   int
	ViolatingInstances []string // 取值不满足新约束/无法无损转换的实例 ID
	DriftedRefs        []string // 发生语义漂移的引用方 ID
	Reason             string   // 人类可读的检查依据
}

// Checker 对单次字段变更做兼容性判定。判定为纯函数，不修改任何状态。
type Checker struct {
	Store *InstanceStore
	Refs  *RefRegistry
}

// Check 判定一次变更是否兼容。live 总数取自存活索引，与历史总量无关。
func (c *Checker) Check(ch FieldChange) (Decision, error) {
	kind, err := Classify(ch)
	if err == ErrNoChange {
		// 仅默认值/可空标志变化：对既有实例取值无影响，
		// 但引用方可能依赖默认取值，仍需做漂移检测。
		d := Decision{Change: ch, Compatible: true,
			Reason: "定义微调（默认值/可空标志）：对既有实例取值无影响"}
		drifted := c.Refs.driftedAgainst(ch.ObjectType, ch.Field, SignatureOf(ch.New))
		if len(drifted) > 0 {
			d.Compatible = false
			d.Categories |= ExternalSemanticDrift
			d.DriftedRefs = drifted
			d.Reason += "；外部引用方依赖的字段语义发生漂移"
		}
		return d, nil
	}
	if err != nil {
		return Decision{}, err
	}
	d := Decision{Change: ch, Kind: kind, Compatible: true}

	switch kind {
	case AddFieldWithDefault:
		d.Reason = "新增带默认取值的字段：既有实例缺失该字段时由读路径回填默认值，无需检查实例"

	case AddFieldWithoutDefault:
		if ch.New.Nullable {
			d.Reason = "新增字段允许缺失：既有实例天然处于取值缺失状态，兼容"
		} else if ch.Backfill != nil {
			d.Reason = "新增字段不允许缺失，但变更携带为全部既有存活实例回填取值的规则，兼容"
		} else {
			d.Compatible = false
			d.Categories |= MissingValueNotAllowed
			d.Reason = "新增字段不带默认取值且不允许缺失，又未携带回填规则，不兼容"
		}

	case TightenConstraint:
		var bad []string
		checked := 0
		c.Store.EachLive(ch.ObjectType, func(in *Instance) bool {
			checked++
			if v, ok := in.Values[ch.Field]; ok {
				if !ch.New.Constraint.SatisfiedBy(v) {
					bad = append(bad, in.ID)
				}
			}
			return true
		})
		d.CheckedInstances = checked
		if len(bad) > 0 {
			d.Compatible = false
			d.Categories |= InstanceConstraintViolation
			d.ViolatingInstances = bad
			d.Reason = fmt.Sprintf("收紧约束：%d 个存活实例中有 %d 个既有取值不满足新约束", checked, len(bad))
		} else {
			d.Reason = fmt.Sprintf("收紧约束：全部 %d 个存活实例的既有取值均满足新约束", checked)
		}

	case LoosenConstraint:
		d.Reason = "放宽约束：既有取值在新约束下必然合法，无需检查实例"

	case ChangeType:
		var bad []string
		checked := 0
		c.Store.EachLive(ch.ObjectType, func(in *Instance) bool {
			checked++
			if v, ok := in.Values[ch.Field]; ok {
				nv, ok := ch.Old.Type.ReinterpretTo(ch.New.Type, v)
				if !ok {
					bad = append(bad, in.ID)
					return true
				}
				// 重解释后的取值还必须满足新类型下的约束。
				if !ch.New.Constraint.SatisfiedBy(nv) {
					bad = append(bad, in.ID)
				}
			}
			return true
		})
		d.CheckedInstances = checked
		if len(bad) > 0 {
			d.Compatible = false
			d.Categories |= IrreversibleTypeConversion
			d.ViolatingInstances = bad
			d.Reason = fmt.Sprintf("类型变更：%d 个存活实例中有 %d 个取值无法无损重解释为 %s", checked, len(bad), ch.New.Type.Kind())
		} else {
			d.Reason = fmt.Sprintf("类型变更：全部 %d 个存活实例的取值均可无损重解释为 %s", checked, ch.New.Type.Kind())
		}

	case RemoveField:
		d.Reason = "删除字段：既有实例上的残留取值随定义一并移除，无需检查实例"
	}

	// 无论变更本身对实例取值是否兼容，都必须检查外部引用方的语义依赖。
	drifted := c.Refs.driftedAgainst(ch.ObjectType, ch.Field, SignatureOf(ch.New))
	if len(drifted) > 0 {
		d.Compatible = false
		d.Categories |= ExternalSemanticDrift
		d.DriftedRefs = drifted
		d.Reason += fmt.Sprintf("；%d 个外部引用方依赖的字段语义发生漂移", len(drifted))
	}
	return d, nil
}
