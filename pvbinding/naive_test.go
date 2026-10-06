package pvbinding

import "sort"

// naiveModel 是与 Controller 完全独立的朴素参考实现：
//   - 立即绑定：对全部卷线性扫描；
//   - 联合绑定：枚举全部指派，暴力选最优；
//   - 不共享任何索引或候选代码路径，仅复用类型定义。
type naiveModel struct {
	vols   map[string]VolumeSpec
	phase  map[string]VolumePhase
	vclaim map[string]string
	claims map[string]ClaimSpec
	cbound map[string]bool
	cvol   map[string]string
}

func newNaive() *naiveModel {
	return &naiveModel{
		vols:   map[string]VolumeSpec{},
		phase:  map[string]VolumePhase{},
		vclaim: map[string]string{},
		claims: map[string]ClaimSpec{},
		cbound: map[string]bool{},
		cvol:   map[string]string{},
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

func (m *naiveModel) candidate(vn string, vs VolumeSpec, cn string, cs ClaimSpec) bool {
	if m.phase[vn] != VolumeAvailable {
		return false
	}
	if vs.StorageClass != cs.StorageClass || vs.Capacity < cs.RequestCapacity {
		return false
	}
	for mode := range cs.AccessModes {
		if !vs.AccessModes[mode] {
			return false
		}
	}
	for k, val := range cs.Selector {
		if vs.Labels[k] != val {
			return false
		}
	}
	if vs.ReservedClaim != "" && vs.ReservedClaim != cn {
		return false
	}
	if cs.VolumeName != "" && cs.VolumeName != vn {
		return false
	}
	return true
}

func (m *naiveModel) bind(cn string, cs ClaimSpec, vn string) {
	m.phase[vn] = VolumeBound
	m.vclaim[vn] = cn
	m.cbound[cn] = true
	m.cvol[cn] = vn
}

func (m *naiveModel) reevaluate() {
	var names []string
	for n, cs := range m.claims {
		if !m.cbound[n] && cs.BindMode == BindImmediate {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	for _, cn := range names {
		if m.cbound[cn] {
			continue
		}
		cs := m.claims[cn]
		var best string
		for vn, vs := range m.vols {
			if !m.candidate(vn, vs, cn, cs) {
				continue
			}
			if best == "" {
				best = vn
				continue
			}
			bs := m.vols[best]
			if vs.Capacity < bs.Capacity || (vs.Capacity == bs.Capacity && vn < best) {
				best = vn
			}
		}
		if best != "" {
			m.bind(cn, cs, best)
		}
	}
}

func (m *naiveModel) addVolume(name string, s VolumeSpec) bool {
	if _, ok := m.vols[name]; ok {
		return false
	}
	m.vols[name] = s
	m.phase[name] = VolumeAvailable
	m.vclaim[name] = s.ReservedClaim
	m.reevaluate()
	return true
}

func (m *naiveModel) addClaim(name string, s ClaimSpec) bool {
	if _, ok := m.claims[name]; ok {
		return false
	}
	m.claims[name] = s
	m.cbound[name] = false
	if s.BindMode == BindImmediate {
		m.reevaluate()
	}
	return true
}

func (m *naiveModel) updateVolume(name string, s VolumeSpec) (found, ok bool) {
	if _, exists := m.vols[name]; !exists {
		return false, false
	}
	if m.phase[name] != VolumeAvailable {
		return true, false
	}
	m.vols[name] = s
	m.vclaim[name] = s.ReservedClaim
	m.reevaluate()
	return true, true
}

func (m *naiveModel) deleteClaim(name string) bool {
	if _, ok := m.claims[name]; !ok {
		return false
	}
	if m.cbound[name] {
		vn := m.cvol[name]
		vs := m.vols[vn]
		if vs.Reclaim == ReclaimDelete {
			delete(m.vols, vn)
			delete(m.phase, vn)
			delete(m.vclaim, vn)
		} else {
			m.phase[vn] = VolumeReleased
			m.vclaim[vn] = ""
			vs.ReservedClaim = ""
			m.vols[vn] = vs
		}
	}
	delete(m.claims, name)
	delete(m.cbound, name)
	delete(m.cvol, name)
	m.reevaluate()
	return true
}

func (m *naiveModel) resetVolume(name string) (found, ok bool) {
	if _, exists := m.vols[name]; !exists {
		return false, false
	}
	if m.phase[name] != VolumeReleased {
		return true, false
	}
	m.phase[name] = VolumeAvailable
	m.vclaim[name] = ""
	m.reevaluate()
	return true, true
}

func (m *naiveModel) expand(name string, newCap int64) (found, allowed bool) {
	cs, ok := m.claims[name]
	if !ok {
		return false, false
	}
	if !m.cbound[name] {
		return true, false
	}
	if newCap <= 0 || newCap < cs.RequestCapacity {
		return true, false
	}
	if newCap > m.vols[m.cvol[name]].Capacity {
		return true, false
	}
	cs.RequestCapacity = newCap
	m.claims[name] = cs
	return true, true
}

// naiveJoint 暴力枚举全部不重复卷指派，取总浪费最小、并列时卷名序列字典序最小者。
func (m *naiveModel) joint(node string, input []string) (map[string]string, bool) {
	names := append([]string(nil), input...)
	sort.Strings(names)
	cand := make([][]string, len(names))
	for i, cn := range names {
		cs := m.claims[cn]
		var list []string
		for vn, vs := range m.vols {
			if !m.candidate(vn, vs, cn, cs) {
				continue
			}
			if len(vs.NodeNames) > 0 && !vs.NodeNames[node] {
				continue
			}
			list = append(list, vn)
		}
		sort.Strings(list)
		if len(list) == 0 {
			return nil, false
		}
		cand[i] = list
	}

	pick := make([]string, len(names))
	used := map[string]bool{}
	var best []string
	var bestWaste int64
	var dfs func(i int, waste int64)
	dfs = func(i int, waste int64) {
		if i == len(names) {
			if best != nil {
				if waste > bestWaste {
					return
				}
				if waste == bestWaste {
					for j := range pick {
						if pick[j] != best[j] {
							if pick[j] > best[j] {
								return
							}
							break
						}
					}
				}
			}
			bestWaste = waste
			best = append([]string(nil), pick...)
			return
		}
		for _, vn := range cand[i] {
			if used[vn] {
				continue
			}
			used[vn] = true
			pick[i] = vn
			dfs(i+1, waste+m.vols[vn].Capacity-m.claims[names[i]].RequestCapacity)
			delete(used, vn)
		}
	}
	dfs(0, 0)
	if best == nil {
		return nil, false
	}
	out := map[string]string{}
	for i, cn := range names {
		out[cn] = best[i]
		m.bind(cn, m.claims[cn], best[i])
	}
	return out, true
}

func itoa(x int64) string {
	if x == 0 {
		return "0"
	}
	var b [24]byte
	i := len(b)
	for x > 0 {
		i--
		b[i] = byte('0' + x%10)
		x /= 10
	}
	return string(b[i:])
}

func (m *naiveModel) fingerprint() string {
	var out []byte
	vn := make([]string, 0, len(m.vols))
	for n := range m.vols {
		vn = append(vn, n)
	}
	sort.Strings(vn)
	for _, n := range vn {
		v := m.vols[n]
		out = append(out, ("V:" + n + "|" + string(m.phase[n]) + "|" +
			itoa(v.Capacity) + "|" + v.StorageClass + "|" + m.vclaim[n] + "\n")...)
	}
	cn := make([]string, 0, len(m.claims))
	for n := range m.claims {
		cn = append(cn, n)
	}
	sort.Strings(cn)
	for _, n := range cn {
		c := m.claims[n]
		state, vol := "P", ""
		if m.cbound[n] {
			state, vol = "B", m.cvol[n]
		}
		out = append(out, ("C:" + n + "|" + state + "|" +
			itoa(c.RequestCapacity) + "|" + vol + "\n")...)
	}
	return string(out)
}

func controllerFingerprint(s Snapshot) string {
	var out []byte
	vn := make([]string, 0, len(s.Volumes))
	for n := range s.Volumes {
		vn = append(vn, n)
	}
	sort.Strings(vn)
	for _, n := range vn {
		v := s.Volumes[n]
		out = append(out, ("V:" + n + "|" + string(v.Phase) + "|" +
			itoa(v.Spec.Capacity) + "|" + v.Spec.StorageClass + "|" + v.ClaimName + "\n")...)
	}
	cn := make([]string, 0, len(s.Claims))
	for n := range s.Claims {
		cn = append(cn, n)
	}
	sort.Strings(cn)
	for _, n := range cn {
		c := s.Claims[n]
		state, vol := "P", ""
		if c.Bound {
			state, vol = "B", c.VolumeName
		}
		out = append(out, ("C:" + n + "|" + state + "|" +
			itoa(c.Spec.RequestCapacity) + "|" + vol + "\n")...)
	}
	return string(out)
}
