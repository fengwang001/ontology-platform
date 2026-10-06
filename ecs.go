package ontology

import (
	"errors"
	"time"
)

// ErrInvalidArgument 表示调用方传入了非法参数（地址族/前缀长度/名字/TTL 等）。
// ErrUpstreamFailure 表示已向上游发起解析，但上游报告失败。
// 二者为固定优先级：参数校验先于任何上游交互发生。
var (
	ErrInvalidArgument = errors.New("ontology: invalid argument")
	ErrUpstreamFailure = errors.New("ontology: upstream failure")
)

// AddrFamily 标识客户端地址所属的协议族。
type AddrFamily uint8

const (
	FamilyUnspecified AddrFamily = 0
	FamilyV4          AddrFamily = 4 // 地址长度固定 4 字节，前缀长度上限 32
	FamilyV6          AddrFamily = 6 // 地址长度固定 16 字节，前缀长度上限 128
)

// Kind 描述一条解析结果的类别。
type Kind uint8

const (
	KindUnknown Kind = 0
	KindRecords Kind = 1 // 正常记录集
	KindNoName  Kind = 2 // 名字不存在（NXDOMAIN）
	KindNoType  Kind = 3 // 名字存在但无此记录类型（NODATA）
)

// IsNegative 判断是否为否定结果。
func (k Kind) IsNegative() bool { return k == KindNoName || k == KindNoType }

// Addr 是定长的客户端地址：其长度必须与所属地址族一致（4 或 16 字节）。
// Addr 一旦构造即视为不可变；返回给调用方的底层数组同样不得改写。
type Addr []byte

// Query 是调用方提交的一次解析请求。
type Query struct {
	Name      string     // 不区分大小写；比较前去掉恰好一个末尾点
	Rrtype    uint16     // DNS 记录类型
	Client    Addr       // 客户端地址，长度须与其族一致
	SrcPrefix int        // 发往上游时声明的源前缀长度；0 表示不暴露地址
	Family    AddrFamily // 客户端地址族
}

// Answer 是上游对一次查询给出的应答。
type Answer struct {
	Kind        Kind
	Records     []byte // 仅 Kind == KindRecords 时有意义
	TTL         int    // 秒，闭区间 [0, 604800]；0 表示不缓存
	ScopePrefix int    // 应答声明的适用范围前缀长度；0 表示对所有客户端适用
}

// Result 是缓存/递归解析返回给调用方的结果。
type Result struct {
	Kind    Kind
	Records []byte
}

// Clock 与 Upstream 由调用方注入，便于以确定方式控制时间与模拟上游。
type Clock interface {
	Now() time.Time
}

// Upstream 模拟递归解析器的上游。返回错误时视为上游失败（不写缓存）。
type Upstream interface {
	Resolve(q Query) (Answer, error)
}

// ClockFunc / UpstreamFunc 提供函数式适配器。
type ClockFunc func() time.Time

func (f ClockFunc) Now() time.Time { return f() }

type UpstreamFunc func(q Query) (Answer, error)

func (f UpstreamFunc) Resolve(q Query) (Answer, error) { return f(q) }
