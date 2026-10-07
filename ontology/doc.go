// Package ontology implements the permission propagation gateway for the
// ontology platform.
//
// 主体（subject）对对象类型（object type）的显式授权可以沿声明了传播能力
// 的有向链接类型（link type）向外扩散。每条链接类型有自身的最大传播深度：
// 深度为 0 表示不参与传播。目标对象类型可以声明覆盖（override）规则：
// 只替换上游传播结果（Replace）或替换并阻断继续传播（Block）。
//
// 详细语义、取舍与本地验证方法见 design.md。
package ontology

// MaxPropagationDepth is the platform-wide upper bound accepted by the
// gateway for a link type's declared propagation depth. A depth outside
// [0, MaxPropagationDepth] is a configuration error.
const MaxPropagationDepth = 64
