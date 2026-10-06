package sigchain

import (
	"fmt"
	"sync/atomic"
	"testing"
)

// countingVerifier 统计校验器调用次数，用于证明判定开销与无关对象数量无关。
type countingVerifier struct {
	calls atomic.Int64
}

func (c *countingVerifier) Verify(string, Signature) CryptoResult {
	c.calls.Add(1)
	return CryptoValid
}

// buildKeyScaleService 构造签名密钥背书深度固定（root→m1→m2→signer）、
// 另有 extra 把无关密钥的服务。
func buildKeyScaleService(b testing.TB, extra int) (*Service, *countingVerifier) {
	v := &countingVerifier{}
	s := NewService(v)
	must(b, s.AddCommit(Commit{ID: "c0", AuthorTime: 1, Sig: &Signature{KeyID: "signer", Time: 20}}))
	must(b, s.AddTag(Tag{Name: "anchor", CommitID: "c0", Sig: &Signature{KeyID: "root", Time: 10}}))
	keys := []Key{
		{ID: "root", ValidFrom: 0, ValidUntil: pt(1000)},
		{ID: "m1", ValidFrom: 0, Endorsement: &Endorsement{Endorser: "root", Time: 10}},
		{ID: "m2", ValidFrom: 0, Endorsement: &Endorsement{Endorser: "m1", Time: 11}},
		{ID: "signer", ValidFrom: 0, Endorsement: &Endorsement{Endorser: "m2", Time: 12}},
	}
	for i := 0; i < extra; i++ {
		keys = append(keys, Key{ID: fmt.Sprintf("noise-%d", i), ValidFrom: 0,
			Endorsement: &Endorsement{Endorser: "root", Time: 10}})
	}
	must(b, s.RegisterKeys(keys...))
	must(b, s.SetAnchor("anchor", "root"))
	return s, v
}

// 单个提交的签名判定开销不随密钥总数增长：背书链结论按密钥备忘，
// 预热后重复判定不再触碰任何背书链求值，校验器调用次数也与密钥总数无关。
func TestJudgeCostIndependentOfKeyCount(t *testing.T) {
	for _, extra := range []int{100, 10000} {
		s, v := buildKeyScaleService(t, extra)
		if _, err := s.VerifyBranch("c0", 50); err != nil { // 预热备忘
			t.Fatal(err)
		}
		evalsAfterWarmup := s.reg.trustEvals
		callsAfterWarmup := v.calls.Load()
		for i := 0; i < 200; i++ {
			if _, err := s.VerifyBranch("c0", 50); err != nil {
				t.Fatal(err)
			}
		}
		evals := s.reg.trustEvals - evalsAfterWarmup
		calls := v.calls.Load() - callsAfterWarmup
		t.Logf("输入: 无关密钥=%d，预热后 200 次判定; 实际输出: 背书链求值=%d 次，校验器调用=%d 次; 判定依据: 两者均与密钥总数无关",
			extra, evals, calls)
		if evals != 0 {
			t.Errorf("extra=%d: 预热后仍发生 %d 次背书链求值", extra, evals)
		}
		if calls != 200 {
			t.Errorf("extra=%d: 校验器调用 %d 次，期望 200", extra, calls)
		}
	}
}

// 一次链路裁决的开销不随分支外提交数增长：校验器调用次数与检查提交数
// 只取决于第一父链长度。
func TestChainCostIndependentOfOffChainCommits(t *testing.T) {
	v := &countingVerifier{}
	s := NewService(v)
	must(t, s.AddCommit(Commit{ID: "c0", AuthorTime: 1, Sig: &Signature{KeyID: "root", Time: 10}}))
	must(t, s.AddTag(Tag{Name: "anchor", CommitID: "c0", Sig: &Signature{KeyID: "root", Time: 10}}))
	must(t, s.RegisterKeys(Key{ID: "root", ValidFrom: 0, ValidUntil: pt(1000)}))
	must(t, s.SetAnchor("anchor", "root"))
	// 第一父链 c0→…→c39。
	prev := "c0"
	for i := 1; i < 40; i++ {
		id := fmt.Sprintf("c%d", i)
		must(t, s.AddCommit(Commit{ID: id, Parents: []string{prev}, AuthorTime: 1,
			Sig: &Signature{KeyID: "root", Time: 10}}))
		prev = id
	}
	verify := func() (Verdict, int64) {
		before := v.calls.Load()
		got, err := s.VerifyBranch("c39", 50)
		if err != nil {
			t.Fatal(err)
		}
		return got, v.calls.Load() - before
	}
	first, firstCalls := verify()
	// 追加 5000 个与第一父链无关的提交（各自挂在 c0 下的侧枝）。
	for i := 0; i < 5000; i++ {
		must(t, s.AddCommit(Commit{ID: fmt.Sprintf("off-%d", i), Parents: []string{"c0"},
			AuthorTime: 1, Sig: &Signature{KeyID: "root", Time: 10}}))
	}
	second, secondCalls := verify()
	t.Logf("输入: 链长 40，分支外提交 0→5000; 实际输出: 校验器调用 %d→%d 次，检查提交 %d→%d 个; 判定依据: 只走第一父链",
		firstCalls, secondCalls, first.Checked, second.Checked)
	if firstCalls != secondCalls || first.Checked != second.Checked {
		t.Errorf("分支外提交影响了裁决开销: 调用 %d→%d，检查 %d→%d",
			firstCalls, secondCalls, first.Checked, second.Checked)
	}
	if !second.Trusted {
		t.Errorf("裁决应可信: %+v", second)
	}
}

// 基准：签名判定开销 vs 密钥总数。
func BenchmarkJudgeKeyCount(b *testing.B) {
	for _, extra := range []int{100, 10000, 100000} {
		b.Run(fmt.Sprintf("keys=%d", extra+4), func(b *testing.B) {
			s, _ := buildKeyScaleService(b, extra)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.VerifyBranch("c0", 50); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// 基准：链路裁决开销 vs 分支外提交数。
func BenchmarkVerifyBranchOffChain(b *testing.B) {
	for _, off := range []int{1000, 100000} {
		b.Run(fmt.Sprintf("offchain=%d", off), func(b *testing.B) {
			s, _ := buildKeyScaleService(b, 0)
			prev := "c0"
			for i := 1; i < 64; i++ {
				id := fmt.Sprintf("c%d", i)
				must(b, s.AddCommit(Commit{ID: id, Parents: []string{prev}, AuthorTime: Time(i),
					Sig: &Signature{KeyID: "signer", Time: 20}}))
				prev = id
			}
			for i := 0; i < off; i++ {
				must(b, s.AddCommit(Commit{ID: fmt.Sprintf("off-%d", i), Parents: []string{"c0"},
					AuthorTime: 1, Sig: &Signature{KeyID: "signer", Time: 20}}))
			}
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.VerifyBranch("c63", 50); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
