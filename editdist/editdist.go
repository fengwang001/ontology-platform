// Package editdist 提供带阈值 k 的 Levenshtein 编辑距离匹配。
//
// 语义按 Unicode 码点（rune）而非字节：一个码点的增/删/改计距离 1。
// 非法 UTF-8 输入返回可判定的 *UTF8Error（含侧别与字节偏移）。
//
// 提前终止：算法只在 |i-j| <= k 的对角线带内做 DP，长度差大于 k
// 时零单元直接判定，某一行带内全部超过 k 时立即返回，绝不先算
// 完整距离再比较。Result.Stats.CellsFilled 可读出本次实际填充的
// DP 单元数，上界为 (2k+1)*(max(lenA,lenB)+1)。
//
// 内存：滚动数组长度为 min(2k+1, min(lenA,lenB)+1)，即 O(min(len))，
// 可由 Result.Stats.MaxWorkLen 读出。
//
// 对称性：Distance(a,b,k) 与 Distance(b,a,k) 的距离与填充单元数
// 完全一致（实现先把两侧规范化为短边在后，两边网格互为转置）。
//
// 大小写折叠：Options.CaseFold 开启后按 Unicode 简单折叠（simple
// case folding）逐码点比较，边界到单码点等价类为止（如 K/k/K、
// ß/ẞ）；不做全折叠（full folding），故 "Straße" 与 "STRASSE"
// 这类需要 ß→ss 展开的情形不匹配。
package editdist

// Options 控制比较行为。
type Options struct {
	// CaseFold 为 true 时按 Unicode 简单大小写折叠比较。
	CaseFold bool
}

// Stats 记录一次比较的运行时统计。
type Stats struct {
	// CellsFilled 是本次比较实际填充的 DP 单元数。
	CellsFilled int
	// MaxWorkLen 是本次比较分配的最大工作数组长度（滚动数组宽度）。
	MaxWorkLen int
}

// Result 是一次带阈值比较的结果。
type Result struct {
	// Distance 是精确编辑距离；Exceeded 为 true 时为 -1（未知，只知道 > k）。
	Distance int
	// Exceeded 为 true 表示距离超过 k（已提前终止）。
	Exceeded bool
	Stats    Stats
}

// Distance 以默认选项判断 a 与 b 的编辑距离是否不超过 k。
func Distance(a, b string, k int) (Result, error) {
	return Within(a, b, k, Options{})
}

// Within 判断 a 与 b 的编辑距离是否不超过 k。
// 不超过时 Result.Distance 为精确距离（与 Full 完全一致）；
// 超过时提前终止，Result.Exceeded 为 true。
// k 为负返回 ErrNegativeK；非法 UTF-8 返回 *UTF8Error。
func Within(a, b string, k int, opts Options) (Result, error) {
	if k < 0 {
		return Result{}, ErrNegativeK
	}
	ra, rb, err := decodeBoth(a, b)
	if err != nil {
		return Result{}, err
	}
	if opts.CaseFold {
		foldRunes(ra)
		foldRunes(rb)
	}
	var st Stats
	d, exc := banded(ra, rb, k, &st)
	return Result{Distance: d, Exceeded: exc, Stats: st}, nil
}

// Full 不设上限计算 a 与 b 的精确编辑距离，用作带阈值结果的对照。
// 内部复用同一带状 DP（k 取 max(lenA,lenB)，带覆盖整个网格），
// 内存仍为 O(min(lenA, lenB))。
func Full(a, b string, opts Options) (Result, error) {
	ra, rb, err := decodeBoth(a, b)
	if err != nil {
		return Result{}, err
	}
	if opts.CaseFold {
		foldRunes(ra)
		foldRunes(rb)
	}
	k := max(len(ra), len(rb))
	var st Stats
	d, _ := banded(ra, rb, k, &st)
	return Result{Distance: d, Exceeded: false, Stats: st}, nil
}

func decodeBoth(a, b string) ([]rune, []rune, error) {
	ra, err := decodeRunes(a, "a")
	if err != nil {
		return nil, nil, err
	}
	rb, err := decodeRunes(b, "b")
	if err != nil {
		return nil, nil, err
	}
	return ra, rb, nil
}
