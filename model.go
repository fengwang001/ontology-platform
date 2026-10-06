package ontology

// Signature 是提交或标签携带的签名；本题不做密码学运算。
type Signature struct {
	KeyID    string
	SignedAt int64
}

// Commit 描述一个提交。
type Commit struct {
	ID         string
	Parents    []string
	AuthorTime int64
	Sig        *Signature
}

// Tag 指向一个提交，可携带签名。
type Tag struct {
	ID     string
	Commit string
	Sig    *Signature
}

// Endorsement 是“我信任此密钥”的背书声明。
type Endorsement struct {
	EndorserID string
	SignedAt   int64
}

// Reason 是签名/链路上一个不可信点的精确原因。
type Reason string

const (
	ReasonUnsigned          Reason = "unsigned"
	ReasonFuture            Reason = "future"
	ReasonInvertedTime      Reason = "inverted-time"
	ReasonForged            Reason = "forged"
	ReasonUnknownKey        Reason = "unknown-key"
	ReasonNotYetValid       Reason = "not-yet-valid"
	ReasonExpired           Reason = "expired"
	ReasonRevoked           Reason = "revoked"
	ReasonBrokenEndorsement Reason = "broken-endorsement"
	ReasonAnchorNotOnChain  Reason = "anchor-not-on-chain"
)
