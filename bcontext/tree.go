package bcontext

import "errors"

// contextNode 为浏览上下文树节点。树关系（parent/children）在上下文创建时
// 固定；当前文档随导航替换，detached 标记父导航导致的整棵子树失效。
type contextNode struct {
	id       int64
	parent   *contextNode
	children []*contextNode

	// top 为该上下文创建时的顶层上下文；嵌入框架父链恒定，顶层不变。
	top *contextNode

	doc      *Document
	isolated bool
	detached bool

	// allow 为该框架创建时固定的嵌入属性允许列表：能力名 -> 允许来源。
	// 仅非顶层上下文使用；出现键即显式声明，未出现走能力默认列表。
	allow map[string][]string

	// allowAtLoad 为当前文档装载时刻 allow 的快照；允许列表修改只影响后续装载。
	allowAtLoad map[string][]string

	// openedFrom 非空时，本上下文为弹窗，指向开启者所在上下文。
	openedFrom *contextNode
}

// alive 返回节点当前是否装载着文档（未因祖先导航而被替换）。
func (c *contextNode) alive() bool {
	return c != nil && !c.detached && c.doc != nil
}

// openerEdge 描述一组开启者/被开启者关系的引用可达性。
// 组关系与树关系相互独立；断开为单向且永久。
type openerEdge struct {
	opener *contextNode
	openee *contextNode
	// openerToOpenee / openeeToOpener 分别表示两个方向的引用是否保留。
	openerToOpenee bool
	openeeToOpener bool
}

// 错误类别六者可区分；拒绝次序见 Kernel 各方法的校验顺序。
var (
	// ErrInvalidArgument 参数非法：未知策略值、空来源、能力名为空。
	ErrInvalidArgument = errors.New("invalid argument")
	// ErrContextNotExist 上下文不存在。
	ErrContextNotExist = errors.New("browsing context does not exist")
	// ErrDocumentNotExist 文档不存在：上下文存在但其整棵子树已被父导航替换。
	ErrDocumentNotExist = errors.New("document does not exist")
	// ErrEmbedderMismatch 嵌入策略不符：跨源子文档的嵌入者策略不满足父文档要求。
	ErrEmbedderMismatch = errors.New("embedder policy mismatch")
	// ErrOpenerGroupBroken 开启者组已断开：对已断开的组做跨引用。
	ErrOpenerGroupBroken = errors.New("opener group broken")
	// ErrFeatureUnknown 能力未知。
	ErrFeatureUnknown = errors.New("unknown feature")
)
