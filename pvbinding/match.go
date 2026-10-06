package pvbinding

// cloneStringSet 深拷贝字符串集合。
func cloneStringSet(in map[string]bool) map[string]bool {
	if in == nil {
		return nil
	}
	out := make(map[string]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// cloneStringMap 深拷贝字符串键值映射。
func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// cloneAccessModes 深拷贝访问模式集合。
func cloneAccessModes(in map[AccessMode]bool) map[AccessMode]bool {
	if in == nil {
		return nil
	}
	out := make(map[AccessMode]bool, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// candidateFilter 汇总一次候选判定所需的谓词。
type candidateFilter struct{}

// matches 判断卷是否满足声明的全部候选条件。节点约束不在此处检查。
func (f candidateFilter) matches(v *Volume, c *Claim) bool {
	if v.Phase != VolumeAvailable {
		return false
	}
	if v.Spec.StorageClass != c.Spec.StorageClass {
		return false
	}
	if v.Spec.Capacity < c.Spec.RequestCapacity {
		return false
	}
	for mode := range c.Spec.AccessModes {
		if !v.Spec.AccessModes[mode] {
			return false
		}
	}
	for key, want := range c.Spec.Selector {
		got, ok := v.Spec.Labels[key]
		if !ok || got != want {
			return false
		}
	}
	if v.Spec.ReservedClaim != "" && v.Spec.ReservedClaim != c.Name {
		return false
	}
	if c.Spec.VolumeName != "" && c.Spec.VolumeName != v.Name {
		return false
	}
	return true
}

// nodeAllowed 判断卷在给定节点上是否可用：无节点约束（nil 或空集）表示不限。
func nodeAllowed(v *Volume, node string) bool {
	if len(v.Spec.NodeNames) == 0 {
		return true
	}
	return v.Spec.NodeNames[node]
}
