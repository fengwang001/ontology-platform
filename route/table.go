package route

import "errors"

var (
	ErrInvalidPrefix  = errors.New("invalid route prefix")
	ErrEmptyBackend   = errors.New("empty backend")
	ErrDuplicateRoute = errors.New("duplicate route prefix")
)

type Route struct {
	Prefix  string
	Scope   string
	Backend string
}

type Match struct {
	Route Route
	Rest  string
}

type tableNode struct {
	children map[string]*tableNode
	route    *Route
}

type Table struct {
	root *tableNode
}

func NewTable(routes []Route) (*Table, error) {
	table := &Table{root: &tableNode{children: make(map[string]*tableNode)}}
	seen := make(map[string]bool)
	for _, item := range routes {
		normalized, err := Normalize(item.Prefix)
		if err != nil || normalized != item.Prefix {
			return nil, ErrInvalidPrefix
		}
		if item.Backend == "" {
			return nil, ErrEmptyBackend
		}
		if seen[item.Prefix] {
			return nil, ErrDuplicateRoute
		}
		seen[item.Prefix] = true

		node := table.root
		if item.Prefix != "/" {
			for _, segment := range splitSegments(item.Prefix) {
				if node.children[segment] == nil {
					node.children[segment] = &tableNode{children: make(map[string]*tableNode)}
				}
				node = node.children[segment]
			}
		}
		route := item
		node.route = &route
	}
	return table, nil
}

func (t *Table) Lookup(path string) (Match, bool) {
	match, _, ok := t.lookupWithCounter(path)
	return match, ok
}

func (t *Table) lookupWithExamined(path string) (Match, int, bool) {
	return t.lookupWithCounter(path)
}

func (t *Table) lookupWithCounter(path string) (Match, int, bool) {
	normalized, err := Normalize(path)
	if err != nil {
		return Match{}, 0, false
	}
	path = normalized

	node := t.root
	examined := 1
	var best *tableNode
	if node.route != nil {
		best = node
	}

	segments := splitSegments(path)
	for _, segment := range segments {
		next := node.children[segment]
		if next == nil {
			break
		}
		node = next
		examined++
		if node.route != nil {
			best = node
		}
	}

	if best == nil {
		return Match{}, examined, false
	}
	prefix := best.route.Prefix
	rest := ""
	if prefix != "/" {
		if len(path) > len(prefix) {
			rest = path[len(prefix):]
		}
	} else if path != "/" {
		rest = path
	}
	return Match{Route: *best.route, Rest: rest}, examined, true
}

func splitSegments(path string) []string {
	if path == "/" {
		return nil
	}
	var segments []string
	start := 1
	for i := 1; i <= len(path); i++ {
		if i == len(path) || path[i] == '/' {
			segments = append(segments, path[start:i])
			start = i + 1
		}
	}
	return segments
}
