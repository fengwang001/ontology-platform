package resolver

// Name 是上游可解析的名字。
type Name = string

// Address 是解析得到的地址。
type Address = string

// AnswerKind 区分上游应答的三类。
type AnswerKind int

const (
	// AnswerAlias 表示别名记录，指向另一个名字。
	AnswerAlias AnswerKind = iota
	// AnswerAddress 表示地址记录，是链的终点。
	AnswerAddress
	// AnswerNXDOMAIN 表示不存在应答（否定终点）。
	AnswerNXDOMAIN
)

// Answer 是上游对单个名字的应答。
//
//   - Kind 为 AnswerAlias 时使用 Target 与 TTL；
//   - Kind 为 AnswerAddress 时使用 Addresses 与 TTL；
//   - Kind 为 AnswerNXDOMAIN 时使用 SOATTL 与 MinimumTTL，否定存活取二者最小值。
type Answer struct {
	Kind       AnswerKind
	Target     Name
	Addresses  []Address
	TTL        int64
	SOATTL     int64
	MinimumTTL int64
}

// AliasAnswer 构造一条别名应答。
func AliasAnswer(target Name, ttl int64) Answer {
	return Answer{Kind: AnswerAlias, Target: target, TTL: ttl}
}

// AddressAnswer 构造一条地址应答。
func AddressAnswer(addresses []Address, ttl int64) Answer {
	return Answer{Kind: AnswerAddress, Addresses: append([]Address(nil), addresses...), TTL: ttl}
}

// NXDOMAINAnswer 构造一条不存在应答，soaTTL 为授权记录存活，minimumTTL 为最小值。
func NXDOMAINAnswer(soaTTL, minimumTTL int64) Answer {
	return Answer{Kind: AnswerNXDOMAIN, SOATTL: soaTTL, MinimumTTL: minimumTTL}
}

// Upstream 是注入的上游查询函数。返回错误表示上游失败。
type Upstream func(name Name) (Answer, error)

// entryKind 区分缓存条目类型。
type entryKind int

const (
	entryAlias entryKind = iota
	entryAddress
	entryNegative
)

// entry 是缓存中的一条记录。缓存按名字至多保存一条（同名替换）。
type entry struct {
	kind      entryKind
	target    Name
	addresses []Address
	expireAt  int64
}

func (e *entry) alive(now int64) bool { return now < e.expireAt }

func (e *entry) remaining(now int64) int64 { return e.expireAt - now }

// AliasLink 是解析结果链上的一环别名。
type AliasLink struct {
	Name   Name
	Target Name
}

// Result 是一次成功解析的结果。Found 为 false 时表示不存在（否定终点）。
type Result struct {
	Name      Name
	Found     bool
	Chain     []AliasLink
	Addresses []Address
	TTL       int64
}

// MaxAliasRecords 限制一次解析可经过的别名记录数量：超过 8 条即为链过长。
const MaxAliasRecords = 8
