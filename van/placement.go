package van

// constraint 分区对单件候选货物的首个不满足约束。
type constraint int

const (
	constraintNone constraint = iota
	constraintOrder
	constraintIsolation
	constraintOverWeight
	constraintOverVolume
)

// firstViolation 返回该分区对候选货物首个不满足的约束；
// 检查顺序即严重度由高到低：顺序、隔离、载重、容积。
// 判定只读取分区级聚合（重量和、体积和、类别集合、停靠点最小/最大值），
// 不枚举任何在车货物记录，因此开销与车上货物总数无关。
func (z *zone) firstViolation(c Cargo) constraint {
	for _, present := range []Category{General, Food, Flammable, Oxidizer} {
		if z.has[present] && !compatible(c.Kind, present) {
			return constraintIsolation
		}
	}
	if z.weight+c.Weight > z.weightLimit {
		return constraintOverWeight
	}
	if z.volume+c.Volume > z.volumeLimit {
		return constraintOverVolume
	}
	return constraintNone
}

// compatible 判断两类货物能否同区：
// 易燃与氧化互斥；食品不得与易燃、氧化同区；其余组合均允许。
func compatible(a, b Category) bool {
	if a == b {
		return true
	}
	if (a == Flammable && b == Oxidizer) || (a == Oxidizer && b == Flammable) {
		return false
	}
	if (a == Food && (b == Flammable || b == Oxidizer)) ||
		(b == Food && (a == Flammable || a == Oxidizer)) {
		return false
	}
	return true
}
