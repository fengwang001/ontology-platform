package engine

// probeOrProbe 返回 OrJoin 判定累计考察的"其它令牌所在节点"次数（非导出计数器）。
// 该次数只与当前有令牌的节点数有关，与图节点总数无关。
func (in *Instance) probeOrProbe() int {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.orProbe
}

// probeReset 清零探针，便于单步核对。
func (in *Instance) probeReset() {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.orProbe = 0
}
