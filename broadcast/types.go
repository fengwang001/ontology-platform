// Package broadcast 实现广播状态（broadcast state）模式下的规则版本化处理。
//
// 规则发布只推进全局版本；投递让实例按版本顺序逐个生效；数据按 key 稳定
// 路由到单一实例，并以到达时的全局版本打标签：实例已生效版本等于标签时
// 立即处理，否则进入该实例的缓冲区，待对应版本生效后按到达顺序刷出。
package broadcast

import (
	"errors"
)

// Op 表示一次规则变更的操作种类。
type Op uint8

const (
	// OpUpsert 新增或覆盖一条规则。
	OpUpsert Op = iota
	// OpDelete 按规则标识删除一条规则；删除一条当前不存在的规则也是合法的。
	OpDelete
)

func (o Op) String() string {
	switch o {
	case OpUpsert:
		return "upsert"
	case OpDelete:
		return "delete"
	default:
		return "unknown"
	}
}

// Change 是一次规则发布中的单条变更。
type Change struct {
	Op   Op
	Rule Rule
}

// Rule 是一条阈值规则。数据值 value 满足 value >= Threshold 时命中。
type Rule struct {
	ID        string
	Threshold int
}

// Hit 是一条数据对一条规则的命中记录。同一数据对多条规则命中时，
// 按规则标识排序各输出一条。
type Hit struct {
	Instance int
	Key      int
	Value    int
	Version  int // 数据处理时所依据的规则版本（即数据到达时的标签）
	RuleID   string
	Seq      int64 // 数据发送的全局顺序号，用于说明输出顺序的来源
}

// opKind 区分一次处理流程的触发来源，仅用于日志。
type opKind uint8

const (
	opPublish opKind = iota
	opDeliver
	opSend
	opQuery
)

func (k opKind) String() string {
	switch k {
	case opPublish:
		return "publish"
	case opDeliver:
		return "deliver"
	case opSend:
		return "send"
	case opQuery:
		return "query"
	default:
		return "unknown"
	}
}

// 非法输入的可区分错误原因。调用方可用 errors.Is 精确判别类别。
var (
	// ErrInvalidRule 规则非法：空标识、未知操作类型等。
	ErrInvalidRule = errors.New("broadcast: invalid rule change")
	// ErrInvalidPublish 发布非法：变更批次为空。
	ErrInvalidPublish = errors.New("broadcast: invalid publish: empty change set")
	// ErrInvalidInstance 投递或查询了不存在的实例（实例数非正亦归此类别）。
	ErrInvalidInstance = errors.New("broadcast: invalid instance id")
	// ErrNoVersionToApply 投递越界：实例已生效全部已发布版本，没有可投递的版本。
	ErrNoVersionToApply = errors.New("broadcast: deliver out of range: no version to apply")
	// ErrNegativeKey 数据键为负。
	ErrNegativeKey = errors.New("broadcast: negative data key")
	// ErrBufferFull 目标实例的缓冲区已满。
	ErrBufferFull = errors.New("broadcast: instance buffer full")
)
