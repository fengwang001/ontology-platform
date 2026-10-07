package ontology

// IncompatKind 以位掩码暴露不兼容的全部类别。
type IncompatKind uint32

const (
	KindConstraintValue  IncompatKind = 1 << iota // 既有取值不满足新约束
	KindIrreversibleType                          // 类型转换不可逆
	KindMissingRequired                           // 缺失取值且不允许缺失
	KindReferenceDrift                            // 外部引用方语义漂移
)

// Basis 记录单条检查依据（检查了什么、在哪些实例上、结论如何）。
type Basis struct {
	Check     string
	Detail    string
	Instances []InstanceID
	Passed    bool
}

// ItemVerdict 是单个字段变更的逐项判定结果。
type ItemVerdict struct {
	Field      string
	Category   ChangeCategory
	Compatible bool
	Reasons    IncompatKind
	Bases      []Basis
}

// BatchReport 是一次打包提交的汇总判定。
type BatchReport struct {
	ObjectType string
	Compatible bool
	Reasons    IncompatKind
	Items      []ItemVerdict
}

type Checker struct {
	objectType string
	store      *InstanceStore
	refs       *ReferenceRegistry
	auditor    AuditSink
}

func NewChecker(objectType string, store *InstanceStore, refs *ReferenceRegistry, auditor AuditSink) *Checker {
	if refs == nil {
		refs = NewReferenceRegistry()
	}
	return &Checker{objectType: objectType, store: store, refs: refs, auditor: auditor}
}

// CheckBatch 逐项独立判定后汇总；只读，不产生任何状态变化。
// 同一变更项可同时携带多类不兼容原因（位掩码全部保留）；
// 整批只要有一项不兼容即 Compatible==false，并汇总全部类别。
func (c *Checker) CheckBatch(changes []FieldChange) BatchReport {
	report := BatchReport{ObjectType: c.objectType, Compatible: true}
	for _, fc := range changes {
		verdict := c.checkOne(fc)
		report.Items = append(report.Items, verdict)
		if !verdict.Compatible {
			report.Compatible = false
			report.Reasons |= verdict.Reasons
		}
	}
	return report
}

func (c *Checker) checkOne(fc FieldChange) ItemVerdict {
	v := ItemVerdict{
		Field:      fc.Name,
		Category:   Classify(fc),
		Compatible: true,
	}
	// 绑定到当前已提交定义后若新旧完全相同（典型原因：调用方持过期
	// Old 快照把字段改回旧上界），这不是一次有效演进，拒绝该提交，
	// 防止并发提交把放宽链“倒退”。
	if fc.Old != nil && fc.New != nil && sameDef(fc.Old, fc.New) {
		v.Compatible = false
		v.Reasons |= KindConstraintValue
		v.Bases = append(v.Bases, Basis{
			Check:  "effective-change",
			Detail: "bound change is identical to the committed definition (stale snapshot?)",
			Passed: false,
		})
		return c.finish(fc, v)
	}

	switch v.Category {
	case CatNone:
		if fc.Old == nil && fc.New == nil {
			v.Compatible = false
			v.Reasons |= KindConstraintValue
			v.Bases = append(v.Bases, Basis{
				Check:  "change-target-exists",
				Detail: "change references a field that does not exist in the committed schema",
				Passed: false,
			})
			break
		}
		v.Bases = append(v.Bases, Basis{
			Check:  "metadata-only",
			Detail: "the admissible value set is unchanged; only metadata differs",
			Passed: true,
		})

	case CatAddWithDefault:
		if !fc.New.Type.Validate(fc.New.Default) || !fc.New.Constraint.Satisfies(fc.New.Default) {
			v.Compatible = false
			// 默认值自身非法：对既有实例等同于无法满足新约束。
			v.Reasons |= KindConstraintValue
			v.Bases = append(v.Bases, Basis{
				Check:  "default-validity",
				Detail: "declared default is not a legal value of the new field",
				Passed: false,
			})
		} else {
			v.Bases = append(v.Bases, Basis{
				Check:  "default-validity",
				Detail: "default value is a legal value; existing instances read it implicitly",
				Passed: true,
			})
		}

	case CatAddWithoutDefault:
		switch {
		case fc.New.AllowMissing:
			v.Bases = append(v.Bases, Basis{
				Check:  "missing-policy",
				Detail: "field is declared AllowMissing; existing instances stay absent",
				Passed: true,
			})
		case fc.Backfill != nil:
			var bad []InstanceID
			c.store.Scan(func(inst Instance) bool {
				val, ok := fc.Backfill.Fill(inst.ID, inst.Fields)
				if !ok || !val.Present() || !fc.New.Type.Validate(val) ||
					!fc.New.Constraint.Satisfies(val) {
					bad = append(bad, inst.ID)
				}
				return true
			})
			if len(bad) > 0 {
				v.Compatible = false
				v.Reasons |= KindMissingRequired
				v.Bases = append(v.Bases, Basis{
					Check:     "backfill-covers-all",
					Detail:    "backfill rule fails to produce a legal value for some live instances",
					Instances: bad,
					Passed:    false,
				})
			} else {
				v.Bases = append(v.Bases, Basis{
					Check:  "backfill-covers-all",
					Detail: "backfill rule produces a legal value for every live instance",
					Passed: true,
				})
			}
		default:
			v.Compatible = false
			v.Reasons |= KindMissingRequired
			v.Bases = append(v.Bases, Basis{
				Check:  "missing-policy",
				Detail: "field is required (AllowMissing=false) with no default and no backfill rule",
				Passed: false,
			})
		}

	case CatTighten, CatMixed:
		var violating []InstanceID
		c.store.Scan(func(inst Instance) bool {
			oldVal, had := inst.Fields[fc.Name]
			if had && oldVal.Present() && !fc.New.Constraint.Satisfies(oldVal) {
				violating = append(violating, inst.ID)
			}
			return true
		})
		if len(violating) > 0 {
			v.Compatible = false
			v.Reasons |= KindConstraintValue
			v.Bases = append(v.Bases, Basis{
				Check:     "all-live-values-satisfy",
				Detail:    "at least one live instance value violates the tightened constraint",
				Instances: violating,
				Passed:    false,
			})
		} else {
			v.Bases = append(v.Bases, Basis{
				Check:  "all-live-values-satisfy",
				Detail: "every live instance value satisfies the new constraint",
				Passed: true,
			})
		}

	case CatLoosen:
		v.Bases = append(v.Bases, Basis{
			Check:  "loosen-no-scan",
			Detail: "relaxing a constraint is always compatible; no instance scan required",
			Passed: true,
		})

	case CatTypeChange:
		var irreversible []InstanceID
		var violating []InstanceID
		c.store.Scan(func(inst Instance) bool {
			oldVal, had := inst.Fields[fc.Name]
			if !had || !oldVal.Present() {
				return true
			}
			if !fc.New.Type.CanReinterpret(fc.Old.Type, oldVal) {
				irreversible = append(irreversible, inst.ID)
				return true
			}
			// 能无损重解释后，还必须满足新字段声明的取值约束。
			nv, ok := ReinterpretValue(fc.New.Type, fc.Old.Type, oldVal)
			if !ok || !fc.New.Constraint.Satisfies(nv) {
				violating = append(violating, inst.ID)
			}
			return true
		})
		if len(irreversible) > 0 {
			v.Compatible = false
			v.Reasons |= KindIrreversibleType
			v.Bases = append(v.Bases, Basis{
				Check:     "all-live-values-lossless",
				Detail:    "at least one live value cannot be losslessly reinterpreted under the new type",
				Instances: irreversible,
				Passed:    false,
			})
		} else {
			v.Bases = append(v.Bases, Basis{
				Check:  "all-live-values-lossless",
				Detail: "every live value has an exact lossless reinterpretation under the new type",
				Passed: true,
			})
		}
		if len(violating) > 0 {
			v.Compatible = false
			v.Reasons |= KindConstraintValue
			v.Bases = append(v.Bases, Basis{
				Check:     "reinterpreted-values-satisfy",
				Detail:    "losslessly reinterpreted value still violates the new field constraint",
				Instances: violating,
				Passed:    false,
			})
		} else {
			v.Bases = append(v.Bases, Basis{
				Check:  "reinterpreted-values-satisfy",
				Detail: "every reinterpreted value satisfies the new field constraint",
				Passed: true,
			})
		}

	case CatRemove:
		v.Bases = append(v.Bases, Basis{
			Check: "remove-no-value-scan",
			Detail: "old stored values are retained but unreadable through the new schema; " +
				"compatibility is decided solely by external references",
			Passed: true,
		})

	}

	return c.finish(fc, v)
}

// finish 追加引用方语义漂移检查并写审计记录。引用漂移与取值类检查
// 互相独立，因此可与其他不兼容原因在同一变更项上同时出现。
func (c *Checker) finish(fc FieldChange, v ItemVerdict) ItemVerdict {
	if refBasis, drift := c.checkReferences(fc); drift {
		v.Compatible = false
		v.Reasons |= KindReferenceDrift
		v.Bases = append(v.Bases, refBasis...)
	} else if len(refBasis) > 0 {
		v.Bases = append(v.Bases, refBasis...)
	}
	if c.auditor != nil {
		c.auditor.Record(AuditRecord{
			ObjectType: c.objectType,
			Field:      v.Field,
			Category:   v.Category.String(),
			Change:     describeChange(fc),
			Compatible: v.Compatible,
			Reasons:    v.Reasons.Strings(),
			Bases:      v.Bases,
		})
	}
	return v
}

// sameDef 精确比较两个字段定义是否完全一致（含默认值/可缺失性等元数据）。
func sameDef(a, b *FieldDef) bool {
	if a == nil || b == nil {
		return a == b
	}
	typeOK := (a.Type == nil && b.Type == nil) ||
		(a.Type != nil && b.Type != nil && a.Type.Name() == b.Type.Name())
	conOK := (a.Constraint == nil && b.Constraint == nil) ||
		(a.Constraint != nil && b.Constraint != nil && a.Constraint.Equivalent(b.Constraint))
	defOK := a.HasDefault == b.HasDefault &&
		(!a.HasDefault || valuesEqual(a.Default, b.Default))
	return typeOK && conOK && defOK && a.AllowMissing == b.AllowMissing
}

func valuesEqual(x, y Value) bool {
	if x.Present() != y.Present() {
		return false
	}
	if !x.Present() {
		return true
	}
	return x.Raw() == y.Raw()
}

// checkReferences 对每个引用方逐存活实例比较“旧语义判定结果”与
// “新语义判定结果”。任一实例上结果不同即语义漂移。
// 访问实例数同样以该字段存活实例总数为上界。
func (c *Checker) checkReferences(fc FieldChange) ([]Basis, bool) {
	refs := c.refs.On(c.objectType, fc.Name)
	if len(refs) == 0 {
		return []Basis{{
			Check:  "reference-drift",
			Detail: "no link type or action references this field",
			Passed: true,
		}}, false
	}
	anyDrift := false
	var bases []Basis
	for _, ref := range refs {
		var drifted []InstanceID
		newJudgment := ref.Predicate
		if ar, ok := ref.(adaptableReference); ok && ar.NewPredicate() != nil {
			newJudgment = ar.NewPredicate()
		}
		c.store.Scan(func(inst Instance) bool {
			oldView := projectView(fc, inst, true)
			newView := projectView(fc, inst, false)
			if ref.Predicate(oldView, fc.Old) != newJudgment(newView, fc.New) {
				drifted = append(drifted, inst.ID)
			}
			return true
		})
		passed := len(drifted) == 0
		bases = append(bases, Basis{
			Check:     "reference-drift:" + ref.Kind() + ":" + ref.ID(),
			Detail:    "compared old vs new predicate outcome over live instances",
			Instances: drifted,
			Passed:    passed,
		})
		if !passed {
			anyDrift = true
		}
	}
	return bases, anyDrift
}

// projectView 构造引用方在旧/新字段语义下看到的取值视图。
// oldSide==true 使用变更前定义，否则使用变更后定义（含默认值/回填投影）。
func projectView(fc FieldChange, inst Instance, oldSide bool) EffectiveView {
	view := EffectiveView{}
	for k, val := range inst.Fields {
		view[k] = val
	}
	if fc.Name == "" {
		return view
	}
	view[fc.Name] = effectiveValue(fc, inst.ID, inst.Fields, oldSide)
	return view
}

func effectiveValue(fc FieldChange, id InstanceID, existing map[string]Value, oldSide bool) Value {
	if oldSide {
		if fc.Old != nil {
			if v, ok := existing[fc.Name]; ok {
				return v
			}
			if fc.Old.HasDefault {
				return fc.Old.Default
			}
		}
		return Missing()
	}
	switch {
	case fc.New == nil: // 字段被删除：新语义下该字段取值缺失
		return Missing()
	case fc.Old == nil: // 新增字段
		if v, ok := existing[fc.Name]; ok {
			return v
		}
		if fc.New.HasDefault {
			return fc.New.Default
		}
		if fc.Backfill != nil {
			if v, ok := fc.Backfill.Fill(id, existing); ok {
				return v
			}
		}
		return Missing()
	default: // 修改既有字段
		v, ok := existing[fc.Name]
		if !ok {
			if fc.New.HasDefault {
				return fc.New.Default
			}
			return Missing()
		}
		if Classify(fc) == CatTypeChange {
			if nv, ok := ReinterpretValue(fc.New.Type, fc.Old.Type, v); ok {
				return nv
			}
		}
		return v
	}
}

func describeChange(fc FieldChange) string {
	old, neu := "<absent>", "<absent>"
	if fc.Old != nil {
		old = describeDef(fc.Old)
	}
	if fc.New != nil {
		neu = describeDef(fc.New)
	}
	return fc.Name + ": " + old + " -> " + neu
}

func describeDef(d *FieldDef) string {
	typeName := "<nil-type>"
	if d.Type != nil {
		typeName = d.Type.Name()
	}
	return typeName + "{allowMissing=" + boolStr(d.AllowMissing) +
		",hasDefault=" + boolStr(d.HasDefault) + "}"
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func (k IncompatKind) Strings() []string {
	var out []string
	for _, bit := range []struct {
		k IncompatKind
		s string
	}{
		{KindConstraintValue, "constraint-value-violated"},
		{KindIrreversibleType, "irreversible-type-change"},
		{KindMissingRequired, "missing-required"},
		{KindReferenceDrift, "reference-semantic-drift"},
	} {
		if k&bit.k != 0 {
			out = append(out, bit.s)
		}
	}
	return out
}
