package sigchain

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
)

// 以下是独立的朴素对照模型：不做任何备忘与优化，直接按定义递归求值，
// 用于与服务的实现做随机比对。

type naiveModel struct {
	keys    map[string]Key
	commits map[string]Commit
	root    string
	policy  Policy
	v       Verifier
}

func (m naiveModel) trustedAt(id string, t Time) bool {
	k, ok := m.keys[id]
	if !ok || t < k.ValidFrom ||
		(k.ValidUntil != nil && t >= *k.ValidUntil) ||
		(k.RevokedAt != nil && t >= *k.RevokedAt) {
		return false
	}
	if id == m.root {
		return true
	}
	if k.Endorsement == nil {
		return false
	}
	return m.trustedAt(k.Endorsement.Endorser, k.Endorsement.Time)
}

func (m naiveModel) classify(c Commit, now Time) Reason {
	sig := c.Sig
	if sig == nil {
		return ReasonUnsigned
	}
	if sig.Time > now {
		return ReasonFromFuture
	}
	if sig.Time < c.AuthorTime {
		return ReasonTimeInversion
	}
	k, known := m.keys[sig.KeyID]
	if known && m.policy.RevocationRetroactive && k.RevokedAt != nil && *k.RevokedAt < now {
		return ReasonRevoked
	}
	switch m.v.Verify(c.ID, *sig) {
	case CryptoInvalid:
		return ReasonForged
	case CryptoKeyUnknown:
		return ReasonUnknownKey
	}
	if !known {
		return ReasonUnknownKey
	}
	if sig.Time < k.ValidFrom {
		return ReasonNotYetValid
	}
	if k.ValidUntil != nil && sig.Time >= *k.ValidUntil {
		return ReasonExpired
	}
	if k.RevokedAt != nil && sig.Time >= *k.RevokedAt {
		return ReasonRevoked
	}
	if !m.trustedAt(sig.KeyID, sig.Time) {
		return ReasonBrokenEndorsement
	}
	return ReasonTrusted
}

func (m naiveModel) verify(anchorCommit, tip string, now Time) Verdict {
	var path []string
	cur := tip
	for {
		path = append(path, cur)
		if cur == anchorCommit {
			break
		}
		c := m.commits[cur]
		if len(c.Parents) == 0 {
			return Verdict{Reason: ReasonAnchorNotOnChain}
		}
		cur = c.Parents[0]
	}
	checked := 0
	for i := len(path) - 1; i >= 0; i-- {
		c := m.commits[path[i]]
		if m.policy.MergeExempt && len(c.Parents) >= 2 {
			continue
		}
		checked++
		if r := m.classify(c, now); r != ReasonTrusted {
			return Verdict{CommitID: c.ID, Reason: r, Checked: checked}
		}
	}
	return Verdict{Trusted: true, Reason: ReasonTrusted, Checked: checked}
}

func hashString(s string) uint64 {
	var h uint64 = 1469598103934665603
	for i := 0; i < len(s); i++ {
		h ^= uint64(s[i])
		h *= 1099511628211
	}
	return h
}

// 随机密钥图与随机提交链：服务实现与独立朴素模型逐种子比对。
func TestRandomModelComparison(t *testing.T) {
	v := verifyFunc(func(objectID string, sig Signature) CryptoResult {
		switch hashString(objectID+"|"+sig.KeyID) % 7 {
		case 0:
			return CryptoInvalid
		case 1:
			return CryptoKeyUnknown
		}
		return CryptoValid
	})
	for seed := int64(1); seed <= 40; seed++ {
		rng := rand.New(rand.NewSource(seed))
		s := NewService(v)
		// 随机密钥图：key i 只背书更早的 key，保证无环；背书时刻随机，可能越界。
		const nKeys = 25
		keys := make([]Key, nKeys)
		for i := 0; i < nKeys; i++ {
			from := Time(rng.Intn(40))
			k := Key{ID: fmt.Sprintf("k%d", i), ValidFrom: from}
			if rng.Intn(2) == 0 {
				k.ValidUntil = pt(from + 1 + Time(rng.Intn(60)))
			}
			if rng.Intn(10) < 3 {
				k.RevokedAt = pt(from + Time(rng.Intn(80)))
			}
			if i > 0 && rng.Intn(10) < 7 {
				k.Endorsement = &Endorsement{
					Endorser: fmt.Sprintf("k%d", rng.Intn(i)),
					Time:     Time(rng.Intn(80)),
				}
			}
			keys[i] = k
		}
		if err := s.RegisterKeys(keys...); err != nil {
			t.Fatalf("seed=%d 登记失败: %v", seed, err)
		}
		// 随机提交链：父引用更早提交，部分合并，部分签名。
		const nCommits = 30
		for i := 0; i < nCommits; i++ {
			c := Commit{ID: fmt.Sprintf("c%d", i), AuthorTime: Time(rng.Intn(60))}
			if i > 0 {
				first := i - 1
				if rng.Intn(10) < 2 {
					first = rng.Intn(i)
				}
				c.Parents = []string{fmt.Sprintf("c%d", first)}
				if rng.Intn(10) < 3 {
					c.Parents = append(c.Parents, fmt.Sprintf("c%d", rng.Intn(i)))
				}
			}
			if rng.Intn(10) < 8 {
				keyID := fmt.Sprintf("k%d", rng.Intn(nKeys))
				if rng.Intn(10) == 0 {
					keyID = "ghost"
				}
				c.Sig = &Signature{KeyID: keyID, Time: Time(rng.Intn(90))}
			}
			if err := s.AddCommit(c); err != nil {
				t.Fatalf("seed=%d 提交失败: %v", seed, err)
			}
		}
		anchorCommit := fmt.Sprintf("c%d", rng.Intn(nCommits))
		must(t, s.AddTag(Tag{Name: "anchor", CommitID: anchorCommit,
			Sig: &Signature{KeyID: "k0", Time: 10}}))
		must(t, s.SetAnchor("anchor", "k0"))
		policy := Policy{MergeExempt: rng.Intn(2) == 0, RevocationRetroactive: rng.Intn(2) == 0}
		s.SetPolicy(policy)
		tip := fmt.Sprintf("c%d", rng.Intn(nCommits))
		now := Time(rng.Intn(120))

		got, err := s.VerifyBranch(tip, now)
		if err != nil {
			t.Fatalf("seed=%d 裁决报错: %v", seed, err)
		}
		model := naiveModel{keys: s.reg.keys, commits: s.commits, root: "k0", policy: policy, v: v}
		want := model.verify(anchorCommit, tip, now)
		t.Logf("seed=%d 输入: 锚点=%s 顶端=%s 验证时刻=%d 策略=%+v; 实际输出: %+v; 朴素模型: %+v",
			seed, anchorCommit, tip, now, policy, got, want)
		if got != want {
			t.Fatalf("seed=%d 不一致: 服务=%+v 朴素模型=%+v", seed, got, want)
		}
	}
}

// 无并发修改时，任意并发裁决的结果必须与串行结果一致。
func TestConcurrentVerdictsAgree(t *testing.T) {
	s := newRootedService(t, nil,
		Key{ID: "a", ValidFrom: 10, Endorsement: &Endorsement{Endorser: "root", Time: 15}})
	prev := "c0"
	for i := 1; i <= 50; i++ {
		id := fmt.Sprintf("c%d", i)
		must(t, s.AddCommit(Commit{ID: id, Parents: []string{prev}, AuthorTime: Time(i),
			Sig: &Signature{KeyID: "a", Time: Time(10 + i)}}))
		prev = id
	}
	want, err := s.VerifyBranch("c50", 200)
	must(t, err)
	var wg sync.WaitGroup
	for g := 0; g < 32; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				got, err := s.VerifyBranch("c50", 200)
				if err != nil || got != want {
					t.Errorf("并发裁决不一致: got=%+v err=%v want=%+v", got, err, want)
					return
				}
			}
		}()
	}
	wg.Wait()
	t.Logf("输入: 32 协程 x 50 次裁决同一分支; 实际输出: 全部等于 %+v; 判定依据: 无修改时快照必然一致", want)
}

// 登记、吊销与裁决并发：结果须等价于某个串行顺序。
// 无关密钥的并发登记/吊销不得影响裁决；关键密钥吊销完成后，
// 其后的裁决必须观察到吊销后的状态（线性一致性）。
func TestConcurrentMutationsSerialize(t *testing.T) {
	s := newRootedService(t, nil,
		Key{ID: "a", ValidFrom: 10, Endorsement: &Endorsement{Endorser: "root", Time: 15}})
	must(t, s.AddCommit(Commit{ID: "c1", Parents: []string{"c0"}, AuthorTime: 5,
		Sig: &Signature{KeyID: "a", Time: 20}}))
	pre, err := s.VerifyBranch("c1", 50)
	must(t, err)
	if !pre.Trusted {
		t.Fatalf("前置裁决应可信: %+v", pre)
	}
	revoked := make(chan struct{})
	var wg sync.WaitGroup
	// 并发登记无关密钥与吊销无关密钥。
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 25; i++ {
				id := fmt.Sprintf("noise-%d-%d", g, i)
				if err := s.RegisterKeys(Key{ID: id, ValidFrom: 0,
					Endorsement: &Endorsement{Endorser: "root", Time: 15}}); err != nil {
					t.Errorf("登记 %s 失败: %v", id, err)
				}
				if err := s.RevokeKey(id, 1000); err != nil {
					t.Errorf("吊销 %s 失败: %v", id, err)
				}
			}
		}(g)
	}
	// 吊销关键密钥 a：吊销时刻 15 <= 签署时刻 20，裁决从可信翻转为已吊销。
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := s.RevokeKey("a", 15); err != nil {
			t.Errorf("吊销 a 失败: %v", err)
		}
		close(revoked)
	}()
	// 并发裁决：吊销完成前两种结果都合法；完成后必须观察到已吊销。
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				got, err := s.VerifyBranch("c1", 50)
				if err != nil {
					t.Errorf("裁决报错: %v", err)
					return
				}
				select {
				case <-revoked:
					if got.Reason != ReasonRevoked || got.CommitID != "c1" {
						t.Errorf("吊销完成后观察到旧状态: %+v", got)
						return
					}
				default:
					if !got.Trusted && got.Reason != ReasonRevoked {
						t.Errorf("出现两种串行结果之外的裁决: %+v", got)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	// 终态与朴素模型在最终快照上的结果一致。
	final, err := s.VerifyBranch("c1", 50)
	must(t, err)
	model := naiveModel{keys: s.reg.keys, commits: s.commits, root: "root", v: s.verifier}
	want := model.verify("c0", "c1", 50)
	t.Logf("输入: 并发登记/吊销/裁决; 实际输出: 终态裁决=%+v; 朴素模型=%+v; 判定依据: 终态须等于最终快照的串行结果", final, want)
	if final != want {
		t.Fatalf("终态不一致: 服务=%+v 朴素模型=%+v", final, want)
	}
	if final.Reason != ReasonRevoked {
		t.Fatalf("吊销后终态应为已吊销: %+v", final)
	}
}
