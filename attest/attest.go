// Package attest 保存绑定摘要的证明与签名者撤销状态。
package attest

// Attestation 是一条绑定摘要的证明。
type Attestation struct {
	Digest string
	Type   string
	Signer string
	Exp    int64 // 严格大于判定时刻 t 才有效
}

// Store 按摘要分桶保存证明与已撤销签名者集合。本包不加锁。
type Store struct {
	byDigest map[string][]Attestation
	revoked  map[string]bool

	// scanned 自证一次闸门检视条数：仅遍历该摘要自己的证明桶。
	scanned int
}

// NewStore 构建空证明库。
func NewStore() *Store {
	return &Store{
		byDigest: make(map[string][]Attestation),
		revoked:  make(map[string]bool),
	}
}

// Add 追加一条证明（允许多条/重复，逐条在闸门处即时判效）。
func (s *Store) Add(a Attestation) {
	s.byDigest[a.Digest] = append(s.byDigest[a.Digest], a)
}

// Revoke 撤销签名者；其全部证明（含此前提交的）立即对后续判定失效。
func (s *Store) Revoke(signer string) { s.revoked[signer] = true }

// IsRevoked 报告签名者是否已被撤销。
func (s *Store) IsRevoked(signer string) bool { return s.revoked[signer] }

// CountFor 返回某摘要自身的证明条数（测试对照用）。
func (s *Store) CountFor(digest string) int { return len(s.byDigest[digest]) }

// Scanned 返回上次 MissingRequired 检视的证明条数。
func (s *Store) Scanned() int { return s.scanned }

// MissingRequired 检查在时刻 now 进入要求 trusted 集合的一级时，required
// （有序）中第一个没有任何有效证明的类型；全部满足返回 ""。
// 一次遍历该摘要的证明桶即完成：scanned 恒等于该摘要自身证明条数。
func (s *Store) MissingRequired(digest string, required []string, trusted map[string]bool, now int64) string {
	s.scanned = 0
	satisfied := make(map[string]bool, len(required))
	for _, a := range s.byDigest[digest] {
		s.scanned++
		if now < a.Exp && !s.revoked[a.Signer] && trusted[a.Signer] {
			satisfied[a.Type] = true
		}
	}
	for _, t := range required {
		if !satisfied[t] {
			return t
		}
	}
	return ""
}
