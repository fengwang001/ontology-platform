package ontology

// 强度偏序（a >= b 表示 a 不弱于 b）：
// IS < IX, IS < S, IX < SIX, S < SIX, SIX < X；IX 与 S 不可比。
var ge = map[Mode]map[Mode]bool{
	IS:  {IS: true},
	IX:  {IX: true, IS: true},
	S:   {S: true, IS: true},
	SIX: {SIX: true, IX: true, S: true, IS: true},
	X:   {X: true, SIX: true, IX: true, S: true, IS: true},
}

// joinTable[a][b] 为偏序下不小于 a、b 的最小模式（最小上界）。
// 不存在公共上界的模式对不会在本问题中出现（五种模式有公共顶 X），
// join(IX, S) = SIX 是关键的不可比对。
var joinTable = map[Mode]map[Mode]Mode{
	IS:  {IS: IS, IX: IX, S: S, SIX: SIX, X: X},
	IX:  {IS: IX, IX: IX, S: SIX, SIX: SIX, X: X},
	S:   {IS: S, IX: SIX, S: S, SIX: SIX, X: X},
	SIX: {IS: SIX, IX: SIX, S: SIX, SIX: SIX, X: X},
	X:   {IS: X, IX: X, S: X, SIX: X, X: X},
}

// 相容矩阵：两个不同事务在同一节点上可同时持有的模式对。
var compatible = map[Mode]map[Mode]bool{
	IS:  {IS: true, IX: true, S: true, SIX: true, X: false},
	IX:  {IS: true, IX: true, S: false, SIX: false, X: false},
	S:   {IS: true, IX: false, S: true, SIX: false, X: false},
	SIX: {IS: true, IX: false, S: false, SIX: false, X: false},
	X:   {IS: false, IX: false, S: false, SIX: false, X: false},
}

var validMode = map[Mode]bool{IS: true, IX: true, S: true, SIX: true, X: true}

// atLeast 报告 a 在强度偏序下是否不小于 b。
func atLeast(a, b Mode) bool { return ge[a][b] }

// join 返回两个模式在强度偏序下的最小上界。
func join(a, b Mode) Mode { return joinTable[a][b] }

// canCoexist 报告两个模式在同一节点上是否相容。
func canCoexist(a, b Mode) bool { return compatible[a][b] }

// requiredIntent 返回某模式在真祖先上所需的祖先意向。
// IS、S 只需祖先持 IS；IX、SIX、X 需要祖先持 IX。
func requiredIntent(m Mode) Mode {
	switch m {
	case IX, SIX, X:
		return IX
	default:
		return IS
	}
}
