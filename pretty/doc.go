// Package pretty 实现行宽适配排版引擎：把调用方构造的文档树按给定行宽
// 渲染成文本。能放进一行的结构放在一行，放不下的结构在其可断点处换行
// 并缩进。渲染决策在组（Group）上做，详见 DESIGN.md。
package pretty

// 引擎的硬性限制。
const (
	// MinWidth 是允许的最小行宽。
	MinWidth = 1
	// MaxWidth 是允许的最大行宽。
	MaxWidth = 10000
	// MaxDepth 是文档树（含引用展开后）允许的最大嵌套深度。
	MaxDepth = 1000
	// MaxOutputWidth 是渲染结果总宽度（按码点宽度计，换行符计 1）的上限。
	MaxOutputWidth = 10_000_000
)

// doubleWidthBoundary 是双宽码点的下界：码点值不小于它的字符宽度为 2。
const doubleWidthBoundary = 0x2E80

// kind 是文档树节点的种类。
type kind uint8

const (
	kindText     kind = iota // 文本（不得含换行字符）
	kindSpace                // 可断空格：平铺时为一个空格，断开时为换行
	kindBlank                // 可断空串：平铺时无输出，断开时为换行
	kindHardLine             // 强制换行
	kindIndent               // 缩进：内容额外增加 n 列缩进
	kindAlign                // 对齐：内容缩进取进入该节点时的当前列
	kindGroup                // 组：平铺/断开决策点
	kindCond                 // 条件文本：平铺/断开各输出一段文本
	kindSeq                  // 序列：按顺序拼接
	kindRef                  // 对命名片段的引用
)

// node 是文档树节点的内部表示。节点构造后不可变，渲染只读。
type node struct {
	kind     kind
	text     string  // kindText 的文本；kindCond 的平铺文本
	alt      string  // kindCond 的断开文本
	n        int     // kindIndent 的缩进增量
	name     string  // kindRef 的片段名
	child    *node   // 单子节点（kindIndent/kindAlign/kindGroup）
	children []*node // kindSeq 的子节点
}

// Doc 是文档树节点的对外句柄。Doc 值不可变，可安全地被多个
// 渲染并发共享。
type Doc interface{ sealed() }

func (*node) sealed() {}

// 共享的不可变单例节点。
var (
	spaceNode    = &node{kind: kindSpace}
	blankNode    = &node{kind: kindBlank}
	hardLineNode = &node{kind: kindHardLine}
	emptyNode    = &node{kind: kindText}
)

// asNode 把 Doc 还原为内部节点；nil 被归一化为空文本节点。
func asNode(d Doc) *node {
	if d == nil {
		return emptyNode
	}
	n, ok := d.(*node)
	if !ok || n == nil {
		return emptyNode
	}
	return n
}

// Text 构造文本节点。s 不得包含换行字符（'\n' 或 '\r'），否则渲染
// 或登记时报参数非法错误。
func Text(s string) Doc { return &node{kind: kindText, text: s} }

// Space 构造可断空格：平铺时输出一个空格，断开时输出换行。
func Space() Doc { return spaceNode }

// Blank 构造可断空串：平铺时不输出任何内容，断开时输出换行。
func Blank() Doc { return blankNode }

// HardLine 构造强制换行：任何模式下都换行。包含强制换行的组不可能
// 平铺，其所有外层组同样一律断开。
func HardLine() Doc { return hardLineNode }

// Indent 构造缩进节点：对其内容额外增加 n 列缩进。n 必须为非负整数，
// 否则渲染或登记时报参数非法错误。
func Indent(n int, d Doc) Doc { return &node{kind: kindIndent, n: n, child: asNode(d)} }

// Align 构造对齐节点：其内容的缩进取进入该节点时的当前列，其内部的
// 缩进继续在此基础上累加。
func Align(d Doc) Doc { return &node{kind: kindAlign, child: asNode(d)} }

// Group 构造组：渲染到组时做平铺/断开决策。组平铺时其内部所有可断点
// 都不断开；组断开时其直接包含的可断点全部换行，嵌套的组各自重新判定。
func Group(d Doc) Doc { return &node{kind: kindGroup, child: asNode(d)} }

// CondText 构造条件文本：所在最近外层组平铺时输出 flat，断开时输出
// broken；不在任何组内时按断开处理。两段文本都不得含换行字符。
func CondText(flat, broken string) Doc { return &node{kind: kindCond, text: flat, alt: broken} }

// Ref 构造对命名片段的引用。渲染时就地展开，行为与直接写入该子树相同。
func Ref(name string) Doc { return &node{kind: kindRef, name: name} }

// Seq 构造序列：按顺序拼接各子文档。空序列等价于空文本；单元素序列
// 归一化为该元素本身；嵌套的序列在构造时被拍平。
func Seq(docs ...Doc) Doc {
	var flat []*node
	for _, d := range docs {
		n := asNode(d)
		if n.kind == kindSeq {
			flat = append(flat, n.children...)
		} else {
			flat = append(flat, n)
		}
	}
	switch len(flat) {
	case 0:
		return emptyNode
	case 1:
		return flat[0]
	}
	return &node{kind: kindSeq, children: flat}
}

// Width 返回 s 的显示宽度：码点值不小于 U+2E80 的字符宽度为 2，
// 其余为 1。
func Width(s string) int {
	w := 0
	for _, r := range s {
		if r >= doubleWidthBoundary {
			w += 2
		} else {
			w++
		}
	}
	return w
}
