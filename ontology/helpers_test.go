package ontology_test

func reverseCopy(in []string) []string {
	out := make([]string, len(in))
	for i := range in {
		out[i] = in[len(in)-1-i]
	}
	return out
}

// verifyCycleArcs 校验证据确实是由给定弧构成的简单有向环（首尾闭合）。
func verifyCycleArcs(t interface{ Helper() }, cycle []string, out map[string]map[string]bool) bool {
	t.Helper()
	if len(cycle) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, id := range cycle {
		if seen[id] {
			return false
		}
		seen[id] = true
	}
	for i := range cycle {
		from := cycle[i]
		to := cycle[(i+1)%len(cycle)]
		if !out[from][to] {
			return false
		}
	}
	return true
}
