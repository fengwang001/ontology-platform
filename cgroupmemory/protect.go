package cgroupmemory

import (
	"math/big"
	"sort"
)

type effectiveValues struct {
	low int64
	min int64
}

func (c *Calculator) effectiveMap() map[*Group]effectiveValues {
	values := make(map[*Group]effectiveValues)
	usages := make(map[*Group]*big.Int)
	c.collectUsage(c.root, usages)
	c.calculateEffective(c.root, usages, values, nil, nil)
	return values
}

func (c *Calculator) collectUsage(group *Group, usages map[*Group]*big.Int) *big.Int {
	if len(group.children) == 0 {
		usages[group] = big.NewInt(group.usage)
		return usages[group]
	}

	total := new(big.Int)
	for _, child := range c.childrenInPathOrder(group) {
		total.Add(total, c.collectUsage(child, usages))
	}
	usages[group] = total
	return total
}

func (c *Calculator) calculateEffective(
	group *Group,
	usages map[*Group]*big.Int,
	values map[*Group]effectiveValues,
	parentEffectiveLow *big.Int,
	parentEffectiveMin *big.Int,
) {
	if group != c.root {
		lowLimit := big.NewInt(group.low)
		minLimit := big.NewInt(group.min)
		usage := usages[group]

		lowCandidate := minBig(usage, lowLimit)
		minCandidate := minBig(usage, minLimit)

		var effectiveLow *big.Int
		var effectiveMin *big.Int
		if group.parent == c.root {
			effectiveLow = lowCandidate
			effectiveMin = minCandidate
		} else {
			effectiveLow = shareProtection(parentEffectiveLow, lowCandidate, c.childLowSum(group.parent, usages))
			effectiveMin = shareProtection(parentEffectiveMin, minCandidate, c.childMinSum(group.parent, usages))
		}

		if effectiveMin.Cmp(effectiveLow) > 0 {
			effectiveMin = new(big.Int).Set(effectiveLow)
		}

		values[group] = effectiveValues{
			low: effectiveLow.Int64(),
			min: effectiveMin.Int64(),
		}
		parentEffectiveLow = effectiveLow
		parentEffectiveMin = effectiveMin
	}

	for _, child := range c.childrenInPathOrder(group) {
		c.calculateEffective(child, usages, values, parentEffectiveLow, parentEffectiveMin)
	}
}

func (c *Calculator) childLowSum(parent *Group, usages map[*Group]*big.Int) *big.Int {
	total := new(big.Int)
	for _, child := range c.childrenInPathOrder(parent) {
		total.Add(total, minBig(usages[child], big.NewInt(child.low)))
	}
	return total
}

func (c *Calculator) childMinSum(parent *Group, usages map[*Group]*big.Int) *big.Int {
	total := new(big.Int)
	for _, child := range c.childrenInPathOrder(parent) {
		total.Add(total, minBig(usages[child], big.NewInt(child.min)))
	}
	return total
}

func shareProtection(parentEffective *big.Int, candidate *big.Int, siblingSum *big.Int) *big.Int {
	if siblingSum.Sign() == 0 || siblingSum.Cmp(parentEffective) <= 0 {
		return new(big.Int).Set(candidate)
	}

	product := new(big.Int).Mul(parentEffective, candidate)
	return product.Quo(product, siblingSum)
}

func minBig(left *big.Int, right *big.Int) *big.Int {
	if left.Cmp(right) <= 0 {
		return new(big.Int).Set(left)
	}
	return new(big.Int).Set(right)
}

func (c *Calculator) childrenInPathOrder(parent *Group) []*Group {
	children := make([]*Group, 0, len(parent.children))
	for _, child := range parent.children {
		children = append(children, child)
	}

	sort.Slice(children, func(i, j int) bool { return children[i].path < children[j].path })
	return children
}
