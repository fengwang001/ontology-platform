// Package dynamicforest maintains the unique minimum spanning forest of a
// dynamic undirected weighted multigraph.
//
// Edges are totally ordered by the lexicographic key (weight, edge ID).
// Running Kruskal in that order therefore selects a unique forest. A
// non-forest edge belongs to a graph cycle and can enter the forest only when
// it is smaller than the largest edge on its forest path. Increasing the
// weight of a forest edge temporarily removes that edge and selects the
// smallest surviving edge crossing the resulting cut; that candidate may be a
// parallel edge or the changed edge itself.
//
// Every accepted AddEdge, SetWeight, or RemoveEdge increments the version.
// Entered and Left contain only edges whose forest membership changes. A
// deleted forest edge is reported in Left. Empty, non-nil slices indicate that
// the forest did not change. TreeSince records the version at which an edge
// most recently entered the forest. Weight changes while it remains in the
// forest, including self-replacement after an increase, do not reset that
// version. The value is zero for edges currently outside the forest.
//
// A mutex serializes mutating operations and provides linearizable reads.
// Forest operations use a link-cut tree for connectivity and path maxima.
// When a forest edge is removed or made heavier, the service computes the
// smaller post-cut tree, traverses only that tree to classify nodes, and
// scans graph adjacency there. The non-exported scanned counter measures this
// work; for a side containing s vertices it is at most 2s+2.
package dynamicforest
