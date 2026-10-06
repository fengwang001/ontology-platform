package ontology

import "sort"

func readyDependent(work map[string]bool, records map[string]*typeRecord) string {
	ready := make([]string, 0, len(work))
	for name := range work {
		blocked := false
		for dep := range records[name].directDeps {
			if work[dep] {
				blocked = true
				break
			}
		}
		if !blocked {
			ready = append(ready, name)
		}
	}
	if len(ready) > 0 {
		sort.Strings(ready)
		return ready[0]
	}
	keys := make([]string, 0, len(work))
	for name := range work {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	return keys[0]
}
