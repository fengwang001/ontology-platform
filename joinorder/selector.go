package joinorder

import (
	"math/big"
	"sort"
)

type problem struct {
	names       []string
	rows        []*big.Rat
	selectivity map[uint64]*big.Rat
	adjacency   [][]int
	components  []uint
}

func (s *Selector) SelectPlan(tables []Table, predicates []Predicate) (Result, error) {
	p, err := buildProblem(tables, predicates)
	if err != nil {
		return Result{}, err
	}
	return selectPlan(p), nil
}

func buildProblem(tables []Table, predicates []Predicate) (*problem, error) {
	if len(tables) == 0 {
		return nil, ValidationError{Reason: ReasonEmptyTableSet, Detail: "at least one table is required"}
	}
	if len(tables) > 12 {
		return nil, ValidationError{Reason: ReasonTooManyTables, Detail: "more than 12 tables are not supported"}
	}

	sortedTables := append([]Table(nil), tables...)
	sort.Slice(sortedTables, func(i, j int) bool {
		return sortedTables[i].Name < sortedTables[j].Name
	})

	indexByName := make(map[string]int, len(sortedTables))
	names := make([]string, len(sortedTables))
	rows := make([]*big.Rat, len(sortedTables))
	for i, table := range sortedTables {
		if table.Name == "" {
			return nil, ValidationError{Reason: ReasonEmptyTableName, Detail: "table name must not be empty"}
		}
		if _, exists := indexByName[table.Name]; exists {
			return nil, ValidationError{Reason: ReasonDuplicateTableName, Detail: "duplicate table name: " + table.Name}
		}
		indexByName[table.Name] = i
		names[i] = table.Name
		if table.Rows < 0 {
			return nil, ValidationError{Reason: ReasonNegativeRowCount, Detail: "negative row count for table: " + table.Name}
		}
		rows[i] = new(big.Rat).SetFrac64(table.Rows, 1)
	}

	sortedPredicates := append([]Predicate(nil), predicates...)
	sort.Slice(sortedPredicates, func(i, j int) bool {
		left := sortedPredicates[i]
		right := sortedPredicates[j]
		if left.LeftTable != right.LeftTable {
			return left.LeftTable < right.LeftTable
		}
		if left.RightTable != right.RightTable {
			return left.RightTable < right.RightTable
		}
		if left.Numerator != right.Numerator {
			return left.Numerator < right.Numerator
		}
		return left.Denominator < right.Denominator
	})

	selectivity := make(map[uint64]*big.Rat)
	adjacency := make([][]int, len(names))
	for _, predicate := range sortedPredicates {
		leftIndex, leftOK := indexByName[predicate.LeftTable]
		rightIndex, rightOK := indexByName[predicate.RightTable]
		if !leftOK || !rightOK {
			return nil, ValidationError{Reason: ReasonUnknownTable, Detail: "predicate references an unknown table"}
		}
		if leftIndex == rightIndex {
			return nil, ValidationError{Reason: ReasonSameTablePredicate, Detail: "predicate endpoints must be different tables: " + predicate.LeftTable}
		}
		if predicate.Numerator <= 0 || predicate.Denominator <= 0 || predicate.Numerator > predicate.Denominator {
			return nil, ValidationError{Reason: ReasonInvalidSelectivity, Detail: "selectivity must satisfy 0 < numerator <= denominator"}
		}

		if leftIndex > rightIndex {
			leftIndex, rightIndex = rightIndex, leftIndex
		}
		key := edgeKey(leftIndex, rightIndex)
		factor := new(big.Rat).SetFrac64(predicate.Numerator, predicate.Denominator)
		if current, exists := selectivity[key]; exists {
			selectivity[key] = new(big.Rat).Mul(current, factor)
		} else {
			selectivity[key] = factor
		}
		adjacency[leftIndex] = append(adjacency[leftIndex], rightIndex)
		adjacency[rightIndex] = append(adjacency[rightIndex], leftIndex)
	}

	for i := range adjacency {
		sort.Ints(adjacency[i])
	}

	components := connectedComponents(adjacency)
	return &problem{
		names:       names,
		rows:        rows,
		selectivity: selectivity,
		adjacency:   adjacency,
		components:  components,
	}, nil
}

func connectedComponents(adjacency [][]int) []uint {
	visited := make([]bool, len(adjacency))
	components := make([]uint, 0)
	for start := 0; start < len(adjacency); start++ {
		if visited[start] {
			continue
		}
		mask := uint(1 << start)
		visited[start] = true
		queue := []int{start}
		for len(queue) > 0 {
			node := queue[0]
			queue = queue[1:]
			for _, next := range adjacency[node] {
				if !visited[next] {
					visited[next] = true
					mask |= 1 << uint(next)
					queue = append(queue, next)
				}
			}
		}
		components = append(components, mask)
	}
	return components
}

func edgeKey(left, right int) uint64 {
	return uint64(left)<<32 | uint64(right)
}
