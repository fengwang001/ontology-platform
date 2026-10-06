package layerconfig

import "sort"

// allSchemas returns every registered schema in deterministic key order.
func allSchemas(r *registry) []Schema {
	r.mu.RLock()
	defer r.mu.RUnlock()
	keys := make([]string, 0, len(r.schemas))
	for k := range r.schemas {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Schema, 0, len(keys))
	for _, k := range keys {
		out = append(out, r.schemas[k])
	}
	return out
}

// requiredSchemas returns registered required schemas in deterministic order.
func requiredSchemas(r *registry) []Schema {
	all := allSchemas(r)
	out := all[:0:0]
	for _, sc := range all {
		if sc.Required {
			out = append(out, sc)
		}
	}
	return out
}

// identity is one existing (scope,layer) pair used in required validation.
type identity struct {
	scope Scope
	layer Layer
}

func sortIdents(in []identity) {
	sort.Slice(in, func(i, j int) bool {
		a, b := in[i], in[j]
		if a.scope.Env != b.scope.Env {
			return a.scope.Env < b.scope.Env
		}
		if a.scope.Region != b.scope.Region {
			return a.scope.Region < b.scope.Region
		}
		return a.scope.Instance < b.scope.Instance
	})
}
