package placement

// validateLabels 校验标签集合：键不允许为空串。
// 调用方不得持有任何会被该调用修改的外部映射。
func validateLabels(labels map[string]string) *PlacementError {
	for k := range labels {
		if k == "" {
			return errInvalid("label key must not be empty")
		}
	}
	return nil
}

// validateSelector 校验选择器：键不允许为空串。
func validateSelector(s Selector) *PlacementError {
	for k := range s {
		if k == "" {
			return errInvalid("selector key must not be empty")
		}
	}
	return nil
}

// validatePod 校验 Pod 规格：ID 非空、标签键非空、
// 选择器键非空、拓扑键合法、最少个数 m 在 1..100。
func validatePod(x Pod) *PlacementError {
	if x.ID == "" {
		return errInvalid("pod id must not be empty")
	}
	if e := validateLabels(x.Labels); e != nil {
		return e
	}
	for _, term := range x.Affinity {
		if e := validateSelector(term.Selector); e != nil {
			return e
		}
		if term.Topology != TopologyNode && term.Topology != TopologyZone {
			return errInvalid("affinity term has invalid topology key")
		}
		if term.MinRequired < 1 || term.MinRequired > 100 {
			return errInvalid("affinity term min_required out of range [1,100]")
		}
	}
	for _, term := range x.AntiAffinity {
		if e := validateSelector(term.Selector); e != nil {
			return e
		}
		if term.Topology != TopologyNode && term.Topology != TopologyZone {
			return errInvalid("anti-affinity term has invalid topology key")
		}
	}
	return nil
}

// matches 报告标签集合是否包含选择器的全部键值对。
func matches(labels map[string]string, s Selector) bool {
	for k, v := range s {
		if labels[k] != v {
			return false
		}
	}
	return true
}

// cloneLabels 复制标签映射，nil 映射返回非 nil 空映射。
func cloneLabels(labels map[string]string) map[string]string {
	out := make(map[string]string, len(labels))
	for k, v := range labels {
		out[k] = v
	}
	return out
}
