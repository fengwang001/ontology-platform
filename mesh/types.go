// Package mesh implements service-mesh traffic routing: atomic config
// publication with optimistic versioning, ordered-rule first-hit matching,
// weighted bucket distribution, per-field policy inheritance and
// publish-time rejection of shadowed rules.
package mesh

// Kind classifies every error returned by the mesh. Error priority, from
// highest to lowest, is exactly the declaration order below.
type Kind int

const (
	KindInvalidArgument Kind = iota // 参数非法（结构、取值范围、调用方式）
	KindVersionConflict             // 版本冲突
	KindWeightInvalid               // 校验失败：分权重类
	KindPolicyInvalid               // 校验失败：策略类
	KindShadowedRule                // 校验失败：遮蔽类
	KindNoRoute                     // 无路由
	KindNoEndpoint                  // 无可用端点
)

func (k Kind) String() string {
	switch k {
	case KindInvalidArgument:
		return "InvalidArgument"
	case KindVersionConflict:
		return "VersionConflict"
	case KindWeightInvalid:
		return "WeightInvalid"
	case KindPolicyInvalid:
		return "PolicyInvalid"
	case KindShadowedRule:
		return "ShadowedRule"
	case KindNoRoute:
		return "NoRoute"
	case KindNoEndpoint:
		return "NoEndpoint"
	default:
		return "Unknown"
	}
}

// Error is the single error type used by this package. Kind drives error
// priority; RuleIndex is the zero-based rule index that produced a
// validation error (FallbackRuleIndex denotes the fallback target list).
type Error struct {
	Kind      Kind
	RuleIndex int
	Message   string
}

// FallbackRuleIndex marks a validation problem located on the fallback
// target list rather than on a rule.
const FallbackRuleIndex = -1

func (e *Error) Error() string {
	if e.RuleIndex == FallbackRuleIndex {
		return e.Kind.String() + ": " + e.Message
	}
	return e.Kind.String() + " (rule[" + itoa(e.RuleIndex) + "]): " + e.Message
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// PathKind selects exact or segment-boundary-prefix path matching.
type PathKind int

const (
	PathExact PathKind = iota
	PathPrefix
)

// PathCondition matches a request path. The query string is stripped before
// matching. Prefix matching honors segment boundaries only.
type PathCondition struct {
	Kind  PathKind
	Value string
}

// HeaderOp selects the three header condition shapes.
type HeaderOp int

const (
	HeaderExact  HeaderOp = iota // value is case-sensitively equal to Value
	HeaderPrefix                 // some value begins with Value
	HeaderExists                 // the header merely has to be present
)

// HeaderCondition is one header condition inside a matcher. Header names are
// case-insensitive; values are case-sensitive.
type HeaderCondition struct {
	Name  string
	Op    HeaderOp
	Value string // ignored for HeaderExists
}

// Matcher is one alternative of a rule. Path is optional: an absent path
// condition matches every path. Header conditions are AND-ed; matchers of a
// rule are OR-ed.
type Matcher struct {
	Path    *PathCondition
	Headers []HeaderCondition
}

// Target names a subset with an integer weight. Weights of one target list
// must sum to exactly 100.
type Target struct {
	Subset string
	Weight int
}

// Policy carries the three independently optional fields. A nil pointer
// means the field is unset. Durations are milliseconds.
type Policy struct {
	Timeout        *int
	Retries        *int
	PerAttemptTime *int
}

// Rule is one ordered routing rule.
type Rule struct {
	Matchers []Matcher
	Targets  []Target
	// Policy optionally overrides individual fields of the service default.
	Policy *Policy
}

// Config is one whole service routing configuration. Publication replaces a
// previous Config atomically.
type Config struct {
	Rules    []Rule
	Fallback []Target // optional
	Default  Policy   // service-level default policy
}

// Request is a routing request. Headers maps a lower-cased header name to
// every value carried under that name (multi-valued headers). Callers may
// pass mixed-case names; the mesh lower-cases them on lookup.
type Request struct {
	Path    string
	Headers map[string][]string
	// Bucket is caller-supplied in [0, 9999].
	Bucket int
}

// EffectivePolicy reports the resolved policy; nil fields remain unset.
type EffectivePolicy struct {
	Timeout        *int
	Retries        *int
	PerAttemptTime *int
}

// RouteResult identifies what a request was routed to.
type RouteResult struct {
	Subset       string
	RuleIndex    int // index of the winning rule; FallbackRuleIndex for fallback
	TargetIdx    int // index of the target inside the winning target list
	FromFallback bool
	Policy       EffectivePolicy
}

// Endpoint is one endpoint with a readiness flag.
type Endpoint struct {
	Addr  string
	Ready bool
}

// SubsetDef is the registration payload for one subset.
type SubsetDef struct {
	Name      string
	Endpoints []Endpoint
}
