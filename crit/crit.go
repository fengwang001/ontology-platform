// Package crit reconstructs the critical path of an operator latency DAG
// and identifies the bottleneck operator on it.
package crit

import "ontology/dag"

// CriticalPath returns the longest source→sink path (source first) and its
// total latency. It walks predecessors from the sink back to the source.
func CriticalPath(g *dag.Graph) (path []string, total int64, err error) {
	source, sink, err := g.Endpoints()
	if err != nil {
		return nil, 0, err
	}
	dist, pred := g.Longest()
	for n := sink; ; n = pred[n] {
		path = append(path, n)
		if n == source {
			break
		}
	}
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path, dist[sink], nil
}

// Bottleneck returns the max-latency operator on the critical path; ties
// break to the lexicographically smallest name.
func Bottleneck(g *dag.Graph) (name string, lat int64, err error) {
	path, _, err := CriticalPath(g)
	if err != nil {
		return "", 0, err
	}
	name, lat = path[0], g.Lat(path[0])
	for _, n := range path[1:] {
		if l := g.Lat(n); l > lat || (l == lat && n < name) {
			name, lat = n, l
		}
	}
	return name, lat, nil
}
