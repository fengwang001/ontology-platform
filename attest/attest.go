// Package attest 实现绑定摘要的证明存储与签名者撤销。
// 证明按摘要建索引，一次闸门判定只扫描该摘要自身的证明。
package attest

import (
	"fmt"

	"ontology/registry"
)

// Attestation 是一条绑定摘要的证明。
type Attestation struct {
	Digest string
	Type   string
	Signer string
	Exp    int64
}

// Store 是证明存储，与 registry.Repo 共用同一把锁和同一时钟。
type Store struct {
	repo     *registry.Repo
	byDigest map[string][]Attestation
	revoked  map[string]bool
	scanned  int
}

// New 创建依附于 repo 的证明存储。
func New(repo *registry.Repo) *Store {
	return &Store{
		repo:     repo,
		byDigest: make(map[string][]Attestation),
		revoked:  make(map[string]bool),
	}
}

// Attest 提交一条证明；exp 须严格大于 now。
func (s *Store) Attest(now int64, digest, typ, signer string, exp int64) error {
	if now < 0 || now > registry.MaxNow || digest == "" || typ == "" || signer == "" {
		return fmt.Errorf("%w: Attest(now=%d, digest=%q, type=%q, signer=%q)", registry.ErrInvalidArgument, now, digest, typ, signer)
	}
	if exp <= now {
		return fmt.Errorf("%w: exp=%d must be > now=%d", registry.ErrInvalidArgument, exp, now)
	}
	s.repo.Lock()
	defer s.repo.Unlock()
	if err := s.repo.CheckClockLocked(now); err != nil {
		return err
	}
	s.byDigest[digest] = append(s.byDigest[digest], Attestation{Digest: digest, Type: typ, Signer: signer, Exp: exp})
	s.repo.CommitClockLocked(now)
	return nil
}

// RevokeSigner 撤销签名者，其全部证明（含之前提交的）自此刻起无效。
func (s *Store) RevokeSigner(now int64, signer string) error {
	if now < 0 || now > registry.MaxNow || signer == "" {
		return fmt.Errorf("%w: RevokeSigner(now=%d, signer=%q)", registry.ErrInvalidArgument, now, signer)
	}
	s.repo.Lock()
	defer s.repo.Unlock()
	if err := s.repo.CheckClockLocked(now); err != nil {
		return err
	}
	s.revoked[signer] = true
	s.repo.CommitClockLocked(now)
	return nil
}

// ValidTypesLocked 返回时刻 t 对指定 trusted 集合有效的证明类型集合。
// 一条证明有效当且仅当 t 严格小于 exp、签名者未被撤销且属于 trusted。
// 只扫描该摘要自身的证明。调用方须持有 repo 的锁。
func (s *Store) ValidTypesLocked(t int64, digest string, trusted map[string]bool) map[string]bool {
	valid := make(map[string]bool)
	for _, a := range s.byDigest[digest] {
		s.scanned++
		if t < a.Exp && !s.revoked[a.Signer] && trusted[a.Signer] {
			valid[a.Type] = true
		}
	}
	return valid
}
