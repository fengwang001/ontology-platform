package bcontext

// allowlistContains 判定嵌入允许列表是否允许 target 来源使用该能力。
// declared 为真时 origins 是显式声明（"*" 表示全部来源）；
// declared 为假时按能力默认列表 def 取值。
func allowlistContains(origins []string, declared bool, def DefaultAllowlist, selfOrigin, target string) bool {
	if !declared {
		switch def {
		case DefaultAll:
			return true
		default:
			return selfOrigin == target
		}
	}
	for _, origin := range origins {
		if origin == "*" || origin == target {
			return true
		}
	}
	return false
}

// featureEnabled 沿父链自顶向下单趟求值能力可用性，开销仅与到顶层的深度有关。
// 逐级三条件：父文档该能力可用（由累计结果 parentEnabled 携带，无重复遍历）、
// 父对该子的嵌入允许列表包含子来源、子自身声明未排除自身来源；
// 需隔离能力在任一未隔离文档上一律不可用。
func (k *Kernel) featureEnabled(c *contextNode, feature string) (bool, string) {
	feat, known := k.features[feature]
	if !known {
		return false, "feature unknown"
	}
	chain := make([]*contextNode, 0, 4)
	for node := c; node != nil; node = node.parent {
		chain = append(chain, node)
	}
	parentEnabled := true
	for i := len(chain) - 1; i >= 0; i-- {
		node := chain[i]
		doc := node.doc
		if feat.RequiresIsolation && !node.isolated {
			return false, "feature requires isolation but document is not isolated"
		}
		if !parentEnabled {
			return false, "ancestor document does not have the feature enabled"
		}
		if !doc.declaredSelfAllowed(feature, feat.Default) {
			return false, "document permissions policy excludes its own origin"
		}
		if node.parent != nil {
			origins, declared := node.allowAtLoad[feature]
			if !allowlistContains(origins, declared, feat.Default, node.parent.doc.Origin, doc.Origin) {
				return false, "embedding allowlist does not include this origin"
			}
		}
		parentEnabled = true
	}
	return true, "isolated(if required), ancestor-enabled, self-declared and embedding allowlist all satisfied"
}
