package joinorder

import (
	"math/big"
	"math/bits"
	"sort"
)

type candidate struct {
	text string
	rows *big.Rat
	cost *big.Rat
}

func selectPlan(p *problem) Result {
	dp := make(map[uint]*candidate)
	var partitions int64

	sortedComponents := append([]uint(nil), p.components...)
	sort.Slice(sortedComponents, func(i, j int) bool {
		return sortedComponents[i] < sortedComponents[j]
	})

	componentPlans := make([]*candidate, len(sortedComponents))
	for componentIndex, componentMask := range sortedComponents {
		solveComponent(p, componentMask, dp, &partitions)
		componentPlans[componentIndex] = dp[componentMask]
	}

	best := combineComponents(componentPlans, &partitions)
	return Result{
		Plan: Plan{
			Text: best.text,
			Rows: new(big.Rat).Set(best.rows),
			Cost: new(big.Rat).Set(best.cost),
		},
		PartitionsExamined: partitions,
	}
}

func solveComponent(p *problem, component uint, dp map[uint]*candidate, partitions *int64) {
	masks := subsetsOf(component)
	sort.Slice(masks, func(i, j int) bool {
		leftCount := bits.OnesCount(uint(masks[i]))
		rightCount := bits.OnesCount(uint(masks[j]))
		if leftCount != rightCount {
			return leftCount < rightCount
		}
		return masks[i] < masks[j]
	})

	for _, mask := range masks {
		if mask&(mask-1) == 0 {
			index := bits.TrailingZeros32(uint32(mask))
			dp[mask] = &candidate{
				text: p.names[index],
				rows: new(big.Rat).Set(p.rows[index]),
				cost: new(big.Rat),
			}
			continue
		}
		if !isConnectedInducedSubset(p.adjacency, mask) {
			continue
		}

		lowest := mask & (^mask + 1)
		remaining := mask ^ lowest
		rightMask := remaining
		for rightMask != 0 {
			*partitions++
			leftMask := mask ^ rightMask
			left := dp[leftMask]
			right := dp[rightMask]
			if left != nil && right != nil && hasCrossEdge(p, leftMask, rightMask) {
				joined := joinCandidates(p, left, right, leftMask, rightMask)
				keepBest(dp, mask, joined)
			}
			rightMask = (rightMask - 1) & remaining
		}
	}
}

func combineComponents(components []*candidate, partitions *int64) *candidate {
	componentCount := len(components)
	dp := make(map[uint]*candidate)
	masks := nonemptySubsets(1 << uint(componentCount))
	sort.Slice(masks, func(i, j int) bool {
		leftCount := bits.OnesCount(uint(masks[i]))
		rightCount := bits.OnesCount(uint(masks[j]))
		if leftCount != rightCount {
			return leftCount < rightCount
		}
		return masks[i] < masks[j]
	})

	for _, mask := range masks {
		if mask&(mask-1) == 0 {
			index := bits.TrailingZeros32(uint32(mask))
			dp[mask] = components[index]
			continue
		}

		base := mask & (^mask + 1)
		remaining := mask ^ base
		rightMask := remaining
		for rightMask != 0 {
			*partitions++
			left := dp[mask^rightMask]
			right := dp[rightMask]
			joined := cartesianCandidates(left, right)
			keepBest(dp, mask, joined)
			rightMask = (rightMask - 1) & remaining
		}
	}

	return dp[1<<uint(componentCount)-1]
}

func joinCandidates(p *problem, left, right *candidate, leftMask, rightMask uint) *candidate {
	selectivity := crossSelectivity(p, leftMask, rightMask)
	rows := new(big.Rat).Mul(left.rows, right.rows)
	rows.Mul(rows, selectivity)
	cost := new(big.Rat).Add(left.cost, right.cost)
	cost.Add(cost, rows)
	return newCandidate(left, right, rows, cost)
}

func cartesianCandidates(left, right *candidate) *candidate {
	rows := new(big.Rat).Mul(left.rows, right.rows)
	cost := new(big.Rat).Add(left.cost, right.cost)
	cost.Add(cost, rows)
	return newCandidate(left, right, rows, cost)
}

func newCandidate(left, right *candidate, rows, cost *big.Rat) *candidate {
	first := left.text
	second := right.text
	if first > second {
		first, second = second, first
	}
	return &candidate{
		text: "(" + first + " " + second + ")",
		rows: rows,
		cost: cost,
	}
}

func keepBest(dp map[uint]*candidate, mask uint, next *candidate) {
	current := dp[mask]
	if current == nil || next.cost.Cmp(current.cost) < 0 ||
		(next.cost.Cmp(current.cost) == 0 && next.text < current.text) {
		dp[mask] = next
	}
}

func hasCrossEdge(p *problem, leftMask, rightMask uint) bool {
	for rightIndex := range p.names {
		if rightMask&(1<<uint(rightIndex)) == 0 {
			continue
		}
		for _, leftIndex := range p.adjacency[rightIndex] {
			if leftMask&(1<<uint(leftIndex)) != 0 {
				return true
			}
		}
	}
	return false
}

func crossSelectivity(p *problem, leftMask, rightMask uint) *big.Rat {
	product := new(big.Rat).SetFrac64(1, 1)
	for rightIndex := range p.names {
		if rightMask&(1<<uint(rightIndex)) == 0 {
			continue
		}
		for leftIndex := range p.names {
			if leftMask&(1<<uint(leftIndex)) == 0 {
				continue
			}
			if factor, exists := p.selectivity[edgeKey(leftIndex, rightIndex)]; exists {
				product.Mul(product, factor)
			}
		}
	}
	return product
}

func isConnectedInducedSubset(adjacency [][]int, mask uint) bool {
	start := bits.TrailingZeros32(uint32(mask))
	seen := uint(1 << uint(start))
	queue := []int{start}
	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]
		for _, next := range adjacency[node] {
			nextBit := uint(1 << uint(next))
			if mask&nextBit != 0 && seen&nextBit == 0 {
				seen |= nextBit
				queue = append(queue, next)
			}
		}
	}
	return seen == mask
}

func subsetsOf(full uint) []uint {
	subsets := make([]uint, 0, bits.OnesCount(full))
	current := full
	for {
		if current == 0 {
			return subsets
		}
		subsets = append(subsets, current)
		current = (current - 1) & full
	}
}

func nonemptySubsets(full int) []uint {
	subsets := make([]uint, 0, full-1)
	for mask := uint(1); mask < uint(full); mask++ {
		subsets = append(subsets, mask)
	}
	return subsets
}
