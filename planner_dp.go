package ontology

import "math/big"

type candidatePlan struct {
	text string
	rows *big.Rat
	cost *big.Rat
}

func chooseNormalizedPlan(query normalizedQuery) (Plan, int64) {
	tableCount := len(query.names)
	totalMasks := 1 << tableCount

	adjacency := make([]uint16, tableCount)
	for left := range adjacency {
		for right := left + 1; right < tableCount; right++ {
			if query.selectivities[left][right] != nil {
				adjacency[left] |= 1 << right
				adjacency[right] |= 1 << left
			}
		}
	}

	componentMask := make([]uint16, totalMasks)
	for mask := 1; mask < totalMasks; mask++ {
		anchor := uint16(mask & -mask)
		seen := anchor
		frontier := anchor
		for frontier != 0 {
			bit := uint16(frontier & -frontier)
			frontier ^= bit
			next := adjacency[bitIndex(bit)] & uint16(mask) &^ seen
			seen |= next
			frontier |= next
		}
		componentMask[mask] = seen
	}

	best := make([]*candidatePlan, totalMasks)
	for i, name := range query.names {
		best[1<<i] = &candidatePlan{
			text: name,
			rows: new(big.Rat).SetInt64(query.rowCounts[i]),
			cost: new(big.Rat),
		}
	}

	var partitionsExamined int64
	for mask := 1; mask < totalMasks; mask++ {
		if mask&(mask-1) == 0 {
			continue
		}

		partitions := enumeratePartitions(uint16(mask), componentMask[mask], componentMask)
		for _, rightMask := range partitions {
			leftMask := uint16(mask) ^ rightMask
			partitionsExamined++

			left := best[leftMask]
			right := best[rightMask]
			if left == nil || right == nil {
				continue
			}

			rows := joinRows(query, leftMask, rightMask, *left, *right)
			cost := new(big.Rat).Add(left.cost, right.cost)
			cost.Add(cost, rows)
			text := canonicalJoin(left.text, right.text)
			candidate := &candidatePlan{text: text, rows: rows, cost: cost}

			if best[mask] == nil || better(candidate, best[mask]) {
				best[mask] = candidate
			}
		}
	}

	complete := best[totalMasks-1]
	return Plan{
		Text:       complete.text,
		OutputRows: new(big.Rat).Set(complete.rows),
		Cost:       new(big.Rat).Set(complete.cost),
	}, partitionsExamined
}

func enumeratePartitions(mask uint16, component uint16, componentMask []uint16) []uint16 {
	anchor := uint16(mask & -mask)
	if component == mask {
		remaining := mask ^ anchor
		partitions := make([]uint16, 0)
		for right := remaining; right != 0; right = (right - 1) & remaining {
			left := mask ^ right
			if componentMask[right] == right && componentMask[left] == left {
				partitions = append(partitions, right)
			}
		}
		return partitions
	}

	var components []uint16
	remaining := mask
	for remaining != 0 {
		nextComponent := componentMask[remaining]
		components = append(components, nextComponent)
		remaining ^= nextComponent
	}

	otherCount := len(components) - 1
	partitions := make([]uint16, 0, (1<<otherCount)-1)
	for combination := 1; combination < 1<<otherCount; combination++ {
		right := uint16(0)
		for index, component := range components[1:] {
			if combination&(1<<index) != 0 {
				right |= component
			}
		}
		partitions = append(partitions, right)
	}
	return partitions
}

func joinRows(query normalizedQuery, leftMask uint16, rightMask uint16, left candidatePlan, right candidatePlan) *big.Rat {
	rows := new(big.Rat).Mul(left.rows, right.rows)
	for leftBits := leftMask; leftBits != 0; leftBits &= leftBits - 1 {
		leftIndex := bitIndex(uint16(leftBits & -leftBits))
		for rightBits := rightMask; rightBits != 0; rightBits &= rightBits - 1 {
			rightIndex := bitIndex(uint16(rightBits & -rightBits))
			selectivity := query.selectivities[leftIndex][rightIndex]
			if leftIndex > rightIndex {
				selectivity = query.selectivities[rightIndex][leftIndex]
			}
			if selectivity != nil {
				rows.Mul(rows, selectivity)
			}
		}
	}
	return rows
}

func canonicalJoin(left string, right string) string {
	if right < left {
		left, right = right, left
	}
	return "(" + left + " " + right + ")"
}

func better(candidate *candidatePlan, current *candidatePlan) bool {
	comparison := candidate.cost.Cmp(current.cost)
	return comparison < 0 || comparison == 0 && candidate.text < current.text
}

func bitIndex(bit uint16) int {
	for i := 0; i < 16; i++ {
		if bit == 1<<i {
			return i
		}
	}
	return -1
}
