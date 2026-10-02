package ontology

const (
	minPosition            = 0
	maxPositionCount       = 64
	maxEdgeCount           = 512
	maxDelay               = 1_000_000
	infinity         int64 = 1 << 62
)

// NewTracker validates the graph and computes its non-empty path summary.
func NewTracker(n int, edges []Edge, sources []int) (*Tracker, error) {
	if n < 1 || n > maxPositionCount || len(edges) > maxEdgeCount {
		return nil, ErrInvalidConfig
	}

	dist := make([][]int64, n)
	for row := range dist {
		dist[row] = make([]int64, n)
		for col := range dist[row] {
			dist[row][col] = infinity
		}
	}

	for _, edge := range edges {
		if edge.From < 0 || edge.From >= n || edge.To < 0 || edge.To >= n ||
			edge.Delay < 0 || edge.Delay > maxDelay {
			return nil, ErrInvalidConfig
		}
		if edge.Delay < dist[edge.From][edge.To] {
			dist[edge.From][edge.To] = edge.Delay
		}
	}

	for middle := 0; middle < n; middle++ {
		for from := 0; from < n; from++ {
			if dist[from][middle] == infinity {
				continue
			}
			for to := 0; to < n; to++ {
				if dist[middle][to] == infinity {
					continue
				}
				candidate := dist[from][middle] + dist[middle][to]
				if candidate < dist[from][to] {
					dist[from][to] = candidate
				}
			}
		}
	}

	for position := 0; position < n; position++ {
		if dist[position][position] == 0 {
			return nil, ErrInvalidConfig
		}
	}

	if len(sources) == 0 {
		return nil, ErrInvalidConfig
	}
	isSource := make([]bool, n)
	for _, source := range sources {
		if source < 0 || source >= n || isSource[source] {
			return nil, ErrInvalidConfig
		}
		isSource[source] = true
	}

	for position := 0; position < n; position++ {
		dist[position][position] = 0
	}

	tracker := &Tracker{
		n:        n,
		dist:     dist,
		source:   isSource,
		counts:   make(map[entryKey]int64),
		active:   make([]activeSet, n),
		frontier: make([]int64, n),
	}
	for position := range tracker.frontier {
		tracker.frontier[position] = infinity
	}
	return tracker, nil
}
