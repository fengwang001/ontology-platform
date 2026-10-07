package ontology

// RelaxScope 是热替换时声明的“放宽范围”元信息。
//
// 只要新逻辑相对旧逻辑存在“原本拒绝、现在放行”的变化，注册方就必须提供
// 一条 RelaxScope；Covers 声明该放宽覆盖哪些输入。系统不阻止注册，但审计
// 可以事后核对实际行为是否与声明一致。
type RelaxScope interface {
	Description() string
	Covers(in Input) bool
}

// AuditFinding 是一条事后审计发现。
type AuditFinding struct {
	Action     string
	Type       string
	OldLogicID string
	NewLogicID string
	Input      Input
	Kind       string
	Detail     string
}

const (
	// FindingRelaxationNotDeclared：新逻辑放行了旧逻辑拒绝的输入，
	// 但本次替换根本没有提供放宽声明。
	FindingRelaxationNotDeclared = "relaxation-not-declared"
	// FindingRelaxationOutOfScope：提供了放宽声明，但该放行输入不在
	// 声明覆盖范围内——实际行为超出了声明的放宽范围。
	FindingRelaxationOutOfScope = "relaxation-out-of-scope"
	// FindingOtherDivergence：不属于“拒绝变放行”的其他行为差异
	//（例如放行变拒绝、或同被放行但输出/后置结果不同），同样记录。
	FindingOtherDivergence = "other-divergence"
)

// logicOutcome 是一套逻辑在给定输入上的归类结果，用于新旧对比。
type logicOutcome struct {
	rejected bool // Pre 或 Execute 或 Post 返回错误
	stage    string
	message  string
	out      Output
}

func evaluateLogic(l *Logic, obj *Object, in Input) logicOutcome {
	if l.Pre != nil {
		if err := l.Pre(obj, in); err != nil {
			return logicOutcome{rejected: true, stage: "pre", message: err.Error()}
		}
	}
	out, err := l.Execute(obj, in)
	if err != nil {
		return logicOutcome{rejected: true, stage: "execute", message: err.Error()}
	}
	if l.Post != nil {
		if err := l.Post(obj, in, out); err != nil {
			return logicOutcome{rejected: true, stage: "post", message: err.Error()}
		}
	}
	return logicOutcome{rejected: false, out: out}
}

// Audit 对比某槽位最近一次注册与其直接前一版在探测输入上的行为差异。
//
// 审计是纯事后机制：注册永远不被阻止，差异以 Finding 形式暴露。
//   - 旧拒绝 -> 新放行，且无 RelaxScope：FindingRelaxationNotDeclared；
//   - 旧拒绝 -> 新放行，有 RelaxScope 但 Covers=false：FindingRelaxationOutOfScope；
//   - 其他差异：FindingOtherDivergence。
//
// 只读旧/新两个 installNode，与运行中的在途调用互不干扰。
func Audit(snap *snapshot, action, typeName string, probes []Input, obj *Object) []AuditFinding {
	var findings []AuditFinding
	if snap == nil {
		return findings
	}
	byType := snap.table[action]
	if byType == nil {
		return findings
	}
	node := byType[typeName]
	if node == nil || node.prev == nil || node.state != stateActive {
		return findings
	}
	newLogic := node.logic
	oldNode := node.prev
	if oldNode.state != stateActive || oldNode.logic == nil {
		return findings
	}
	oldLogic := oldNode.logic

	for _, in := range probes {
		oldRes := evaluateLogic(oldLogic, obj, in)
		newRes := evaluateLogic(newLogic, obj, in)

		if oldRes.rejected && !newRes.rejected {
			switch {
			case node.relax == nil:
				findings = append(findings, AuditFinding{
					Action: action, Type: typeName,
					OldLogicID: oldLogic.ID, NewLogicID: newLogic.ID, Input: in,
					Kind:   FindingRelaxationNotDeclared,
					Detail: "input rejected by old logic but accepted by new logic without a declared RelaxScope",
				})
			case !node.relax.Covers(in):
				findings = append(findings, AuditFinding{
					Action: action, Type: typeName,
					OldLogicID: oldLogic.ID, NewLogicID: newLogic.ID, Input: in,
					Kind: FindingRelaxationOutOfScope,
					Detail: "relaxation declared (\"" + node.relax.Description() +
						"\") but this accepted input is not covered by it",
				})
			}
			continue
		}

		// 需求要求审计“拒绝/放行结果”的不一致；两版都放行属于契约一致，
		// 不因实现自身的版本标识（如输出里的逻辑 ID）不同而误报。
		if oldRes.rejected != newRes.rejected ||
			(oldRes.rejected && oldRes.stage != newRes.stage) {
			kind := FindingOtherDivergence
			detail := "new logic behaves differently from old logic outside any declared relaxation"
			if !oldRes.rejected && newRes.rejected {
				detail = "input accepted by old logic but rejected by new logic (unannounced tightening)"
			}
			findings = append(findings, AuditFinding{
				Action: action, Type: typeName,
				OldLogicID: oldLogic.ID, NewLogicID: newLogic.ID, Input: in,
				Kind: kind, Detail: detail,
			})
		}
	}
	return findings
}

// AuditType 是供调用方在 Dispatcher 上直接使用的加锁审计入口：
// 对指定 (动作, 类型) 槽位的最近一次替换做事后审计。
func (d *Dispatcher) AuditType(action, typeName string, probes []Input, obj *Object) []AuditFinding {
	d.mu.Lock()
	snap := d.snap
	d.mu.Unlock()
	return Audit(snap, action, typeName, probes, obj)
}

// RelaxDeclaredAt 返回指定槽位当前注册所声明放宽范围（nil 表示无声明），
// 便于外部核对“放宽是否被声明”这一元信息本身。
func (d *Dispatcher) RelaxDeclaredAt(action, typeName string) RelaxScope {
	d.mu.Lock()
	defer d.mu.Unlock()
	if byType := d.snap.table[action]; byType != nil {
		if node := byType[typeName]; node != nil && node.state == stateActive {
			return node.relax
		}
	}
	return nil
}
