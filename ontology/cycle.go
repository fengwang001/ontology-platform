package ontology

// findPropagationCycle returns a non-empty description when the directed graph
// made exclusively of propagation-enabled links (depth > 0) contains a cycle.
// Links with depth 0 never carry propagation, so they cannot participate in a
// propagation cycle and are deliberately excluded.
func findPropagationCycle(links map[string]LinkType) string {
	adj := map[string][]string{}
	for _, link := range s_links(links) {
		if link.propagates() {
			adj[link.From] = append(adj[link.From], link.To)
		}
	}
	const (
		unseen = 0
		active = 1
		done   = 2
	)
	color := map[string]int{}
	var stack []string
	var dfs func(node string) string
	dfs = func(node string) string {
		color[node] = active
		stack = append(stack, node)
		for _, next := range adj[node] {
			switch color[next] {
			case active:
				return describeCycle(stack, next)
			case unseen:
				if found := dfs(next); found != "" {
					return found
				}
			}
		}
		color[node] = done
		stack = stack[:len(stack)-1]
		return ""
	}
	for node := range adj {
		if color[node] == unseen {
			if found := dfs(node); found != "" {
				return found
			}
		}
	}
	return ""
}

func describeCycle(stack []string, repeated string) string {
	start := 0
	for i, node := range stack {
		if node == repeated {
			start = i
			break
		}
	}
	cycle := append(append([]string{}, stack[start:]...), repeated)
	msg := "cycle through object types:"
	for _, node := range cycle {
		msg += " " + node
	}
	return msg
}

// s_links deterministically iterates links. Kept tiny on purpose so cycle
// detection is reproducible for tests and logs.
func s_links(links map[string]LinkType) []LinkType {
	out := make([]LinkType, 0, len(links))
	for _, link := range links {
		out = append(out, link)
	}
	// Order by name for deterministic traversal.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j-1].Name > out[j].Name; j-- {
			out[j-1], out[j] = out[j], out[j-1]
		}
	}
	return out
}
