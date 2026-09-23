package graph

import (
	"errors"
	"sort"
)

type unknownDep struct {
	from string
	to   string
}

func (e unknownDep) Error() string {
	return "graph: edge " + e.from + "->" + e.to + " references an unknown task"
}

func (e unknownDep) Is(target error) bool {
	return target == ErrUnknownDep
}

func unknownDepError(from, to string) error {
	return errors.Join(ErrUnknownDep, unknownDep{from: from, to: to})
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
