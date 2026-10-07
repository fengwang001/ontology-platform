package ontology

// exportForFuzz is a test-only frozen view used by differential tests.
// It is declared in a _test.go file so it is absent from production builds.
func (g *Graph) ExportForFuzz(caller ID) (objs map[ID]bool, lts map[ID]LinkType, links []LinkInstance, ex, tr map[ID]bool) {
	g.mu.RLock()
	defer g.mu.RUnlock()

	objs = make(map[ID]bool, len(g.objects))
	for k := range g.objects {
		objs[k] = true
	}
	lts = make(map[ID]LinkType, len(g.linkTypes))
	for k, v := range g.linkTypes {
		lts[k] = v
	}
	links = make([]LinkInstance, 0, len(g.links))
	for _, v := range g.links {
		links = append(links, v)
	}
	ex = map[ID]bool{}
	tr = map[ID]bool{}
	if set, ok := g.existence[caller]; ok {
		for k, v := range set {
			ex[k] = v
		}
	}
	if set, ok := g.traversable[caller]; ok {
		for k, v := range set {
			tr[k] = v
		}
	}
	return objs, lts, links, ex, tr
}
