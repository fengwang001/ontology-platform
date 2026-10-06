package sigchain

import (
	"errors"
	"testing"
)

// verifyFunc 把函数适配为 Verifier，便于按用例注入密码学回答。
type verifyFunc func(objectID string, sig Signature) CryptoResult

func (f verifyFunc) Verify(objectID string, sig Signature) CryptoResult {
	return f(objectID, sig)
}

func pt(v Time) *Time { return &v }

func must(t testing.TB, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("意外失败: %v", err)
	}
}

// 错误次序中每对相邻错误的优先关系：参数非法 > 提交不存在 > 标签不存在
// > 密钥不存在 > 背书成环 > 吊销提前。每次调用只报最靠前的一个，
// 被拒绝的调用不得改变任何登记。
func TestErrorOrderAdjacentPairs(t *testing.T) {
	s := newRootedService(t, nil)

	// 参数非法 vs 提交不存在：空标识且父提交缺失，报参数非法。
	err := s.AddCommit(Commit{ID: "", Parents: []string{"missing"}})
	t.Logf("输入: 空标识+父提交缺失; 实际输出: %v; 判定依据: 参数非法优先于提交不存在", err)
	if !errors.Is(err, ErrInvalidParam) {
		t.Errorf("得到 %v，期望 %v", err, ErrInvalidParam)
	}

	// 提交不存在 vs 标签不存在：顶端提交缺失且锚点未设置，报提交不存在。
	s2 := NewService(verifyFunc(func(string, Signature) CryptoResult { return CryptoValid }))
	_, err = s2.VerifyBranch("missing", 0)
	t.Logf("输入: 顶端缺失+锚点未设置; 实际输出: %v; 判定依据: 提交不存在优先于标签不存在", err)
	if !errors.Is(err, ErrCommitNotFound) {
		t.Errorf("得到 %v，期望 %v", err, ErrCommitNotFound)
	}

	// 标签不存在 vs 密钥不存在：锚点标签与根密钥都不存在，报标签不存在。
	err = s.SetAnchor("missing-tag", "missing-key")
	t.Logf("输入: 标签缺失+根密钥未登记; 实际输出: %v; 判定依据: 标签不存在优先于密钥不存在", err)
	if !errors.Is(err, ErrTagNotFound) {
		t.Errorf("得到 %v，期望 %v", err, ErrTagNotFound)
	}

	// 密钥不存在 vs 背书成环：批次内既有缺失的背书者，又有成环对，报密钥不存在。
	err = s.RegisterKeys(
		Key{ID: "e1", ValidFrom: 10, Endorsement: &Endorsement{Endorser: "ghost", Time: 15}},
		Key{ID: "e2", ValidFrom: 10, Endorsement: &Endorsement{Endorser: "e3", Time: 15}},
		Key{ID: "e3", ValidFrom: 10, Endorsement: &Endorsement{Endorser: "e2", Time: 15}},
	)
	t.Logf("输入: 背书者 ghost 缺失+e2↔e3 成环; 实际输出: %v; 判定依据: 密钥不存在优先于背书成环", err)
	if !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("得到 %v，期望 %v", err, ErrKeyNotFound)
	}

	// 背书成环 vs 吊销提前：批次内既有成环对，又有对已吊销密钥的提前吊销，报背书成环。
	must(t, s.RegisterKeys(Key{ID: "w", ValidFrom: 10, RevokedAt: pt(50)}))
	err = s.RegisterKeys(
		Key{ID: "e4", ValidFrom: 10, Endorsement: &Endorsement{Endorser: "e5", Time: 15}},
		Key{ID: "e5", ValidFrom: 10, Endorsement: &Endorsement{Endorser: "e4", Time: 15}},
		Key{ID: "w", ValidFrom: 10, RevokedAt: pt(40)},
	)
	t.Logf("输入: e4↔e5 成环+w 吊销从 50 提前到 40; 实际输出: %v; 判定依据: 背书成环优先于吊销提前", err)
	if !errors.Is(err, ErrEndorsementCycle) {
		t.Errorf("得到 %v，期望 %v", err, ErrEndorsementCycle)
	}

	// 被拒绝的调用不得改变登记：w 的吊销时刻仍是 50，e4/e5 均未登记。
	if err := s.RegisterKeys(Key{ID: "w", ValidFrom: 10, RevokedAt: pt(40)}); !errors.Is(err, ErrRevokeEarlier) {
		t.Errorf("w 吊销时刻应仍为 50: 得到 %v，期望 %v", err, ErrRevokeEarlier)
	}
	if err := s.RevokeKey("e4", 20); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("e4 不应存在: 得到 %v，期望 %v", err, ErrKeyNotFound)
	}
	t.Log("被拒绝的调用未改变任何登记，状态校验通过")
}

// 来自未来优先于时刻倒置；两者各自独立命中时也正确分类。
func TestFutureBeatsTimeInversion(t *testing.T) {
	s := newRootedService(t, nil)
	cases := []struct {
		name   string
		author Time
		signAt Time
		now    Time
		want   Reason
		basis  string
	}{
		{"既未来又倒置", 200, 150, 100, ReasonFromFuture, "签署时刻 150>验证时刻 100 且 <作者时刻 200，来自未来优先"},
		{"仅来自未来", 100, 150, 120, ReasonFromFuture, "签署时刻晚于验证时刻"},
		{"仅时刻倒置", 200, 150, 300, ReasonTimeInversion, "签署时刻早于作者时刻且不晚于验证时刻"},
	}
	for _, tc := range cases {
		got := judgeTip(t, s, Commit{ID: "c-" + tc.name, Parents: []string{"c0"}, AuthorTime: tc.author,
			Sig: &Signature{KeyID: "root", Time: tc.signAt}}, tc.now)
		t.Logf("输入: 作者时刻=%d 签署时刻=%d 验证时刻=%d; 实际输出: %v; 判定依据: %s",
			tc.author, tc.signAt, tc.now, got.Reason, tc.basis)
		if got.Reason != tc.want {
			t.Errorf("%s: 得到 %s，期望 %s", tc.name, got.Reason, tc.want)
		}
	}
}

// 吊销追溯开关：开启后吊销时刻早于验证时刻的密钥所签一律降级为已吊销。
func TestRetroactiveRevocation(t *testing.T) {
	s := newRootedService(t, nil,
		Key{ID: "k", ValidFrom: 10, RevokedAt: pt(80), Endorsement: &Endorsement{Endorser: "root", Time: 15}})
	must(t, s.AddCommit(Commit{ID: "c1", Parents: []string{"c0"}, AuthorTime: 5,
		Sig: &Signature{KeyID: "k", Time: 50}}))
	s.SetPolicy(Policy{RevocationRetroactive: false})
	got, err := s.VerifyBranch("c1", 90)
	must(t, err)
	t.Logf("输入: 签署时刻=50 吊销时刻=80 验证时刻=90 追溯=关; 实际输出: %v; 判定依据: 吊销不追溯，签署时仍受信", got.Reason)
	if got.Reason != ReasonTrusted {
		t.Errorf("追溯关闭: 得到 %s，期望 %s", got.Reason, ReasonTrusted)
	}
	s.SetPolicy(Policy{RevocationRetroactive: true})
	got, err = s.VerifyBranch("c1", 90)
	must(t, err)
	t.Logf("输入: 同上但追溯=开; 实际输出: %v; 判定依据: 吊销时刻 80<验证时刻 90，降级为签署时已吊销", got.Reason)
	if got.Reason != ReasonRevoked || got.CommitID != "c1" {
		t.Errorf("追溯开启: 得到 (%s,%s)，期望 (c1,%s)", got.CommitID, got.Reason, ReasonRevoked)
	}
}

// 合并提交豁免：开启时合并提交自身不需签名，第二父引入的提交本就不纳入；
// 关闭时合并提交同样须签名可信。
func TestMergeExemption(t *testing.T) {
	build := func() *Service {
		s := newRootedService(t, verifyFunc(func(objectID string, sig Signature) CryptoResult {
			if objectID == "side" {
				return CryptoInvalid // 第二父引入的提交签名伪造
			}
			return CryptoValid
		}))
		must(t, s.AddCommit(Commit{ID: "c1", Parents: []string{"c0"}, AuthorTime: 5,
			Sig: &Signature{KeyID: "root", Time: 11}}))
		must(t, s.AddCommit(Commit{ID: "side", Parents: []string{"c0"}, AuthorTime: 5,
			Sig: &Signature{KeyID: "root", Time: 11}}))
		must(t, s.AddCommit(Commit{ID: "m", Parents: []string{"c1", "side"}, AuthorTime: 6})) // 未签名合并
		must(t, s.AddCommit(Commit{ID: "tip", Parents: []string{"m"}, AuthorTime: 7,
			Sig: &Signature{KeyID: "root", Time: 12}}))
		return s
	}
	s := build()
	s.SetPolicy(Policy{MergeExempt: true})
	got, err := s.VerifyBranch("tip", 200)
	must(t, err)
	t.Logf("输入: 豁免=开，合并提交 m 未签名，第二父 side 签名伪造; 实际输出: %v 检查数=%d; 判定依据: 合并提交豁免且第二父不纳入", got.Reason, got.Checked)
	if !got.Trusted || got.Checked != 3 {
		t.Errorf("豁免开启: 得到 (trusted=%v, checked=%d)，期望 (true, 3)", got.Trusted, got.Checked)
	}
	s = build()
	s.SetPolicy(Policy{MergeExempt: false})
	got, err = s.VerifyBranch("tip", 200)
	must(t, err)
	t.Logf("输入: 豁免=关; 实际输出: %v (%s); 判定依据: 合并提交同样须签名可信，m 未签名", got.Reason, got.CommitID)
	if got.Reason != ReasonUnsigned || got.CommitID != "m" {
		t.Errorf("豁免关闭: 得到 (%s,%s)，期望 (m,%s)", got.CommitID, got.Reason, ReasonUnsigned)
	}
}

// 锚点不是顶端的第一父祖先时报「锚点不在链上」；锚点即顶端时单提交链可信。
func TestAnchorNotOnChain(t *testing.T) {
	s := newRootedService(t, nil)
	must(t, s.AddCommit(Commit{ID: "other-root", AuthorTime: 5, Sig: &Signature{KeyID: "root", Time: 10}}))
	must(t, s.AddCommit(Commit{ID: "other-tip", Parents: []string{"other-root"}, AuthorTime: 6,
		Sig: &Signature{KeyID: "root", Time: 11}}))
	got, err := s.VerifyBranch("other-tip", 200)
	must(t, err)
	t.Logf("输入: 锚点链 c0 与分支 other-root→other-tip 不相交; 实际输出: %v; 判定依据: 第一父下行到根仍未遇锚点", got.Reason)
	if got.Reason != ReasonAnchorNotOnChain || got.Trusted {
		t.Errorf("得到 %v，期望 %s", got.Reason, ReasonAnchorNotOnChain)
	}
	got, err = s.VerifyBranch("c0", 200)
	must(t, err)
	t.Logf("输入: 顶端即锚点提交 c0; 实际输出: %v; 判定依据: 单提交链且签名可信", got.Reason)
	if !got.Trusted {
		t.Errorf("锚点即顶端: 得到 %v，期望全链可信", got.Reason)
	}
}

// 多级背书可以锚定到根；背书时刻越出背书者有效区间则背书无效。
func TestMultiLevelEndorsement(t *testing.T) {
	s := newRootedService(t, nil,
		Key{ID: "a", ValidFrom: 10, Endorsement: &Endorsement{Endorser: "root", Time: 15}},
		Key{ID: "b", ValidFrom: 10, Endorsement: &Endorsement{Endorser: "a", Time: 16}},
		Key{ID: "c", ValidFrom: 10, Endorsement: &Endorsement{Endorser: "b", Time: 17}},
		Key{ID: "d", ValidFrom: 10, Endorsement: &Endorsement{Endorser: "root", Time: 5}},
	)
	got := judgeTip(t, s, Commit{ID: "c-ok", Parents: []string{"c0"}, AuthorTime: 5,
		Sig: &Signature{KeyID: "c", Time: 20}}, 200)
	t.Logf("输入: 三级背书 root→a→b→c，签署时刻=20; 实际输出: %v; 判定依据: 每级背书时刻均在背书者有效区间内", got.Reason)
	if got.Reason != ReasonTrusted {
		t.Errorf("三级背书: 得到 %s，期望 %s", got.Reason, ReasonTrusted)
	}
	got = judgeTip(t, s, Commit{ID: "c-bad", Parents: []string{"c0"}, AuthorTime: 5,
		Sig: &Signature{KeyID: "d", Time: 20}}, 200)
	t.Logf("输入: d 的背书时刻=5 早于 root 生效时刻=10; 实际输出: %v; 判定依据: 背书时刻越界，背书无效", got.Reason)
	if got.Reason != ReasonBrokenEndorsement {
		t.Errorf("背书越界: 得到 %s，期望 %s", got.Reason, ReasonBrokenEndorsement)
	}
}

// 背书成环（含自背书）的登记整体拒绝，且不改变任何已有登记。
func TestEndorsementCycleRejected(t *testing.T) {
	s := newRootedService(t, nil)
	err := s.RegisterKeys(
		Key{ID: "x", ValidFrom: 10, Endorsement: &Endorsement{Endorser: "y", Time: 15}},
		Key{ID: "y", ValidFrom: 10, Endorsement: &Endorsement{Endorser: "x", Time: 15}},
	)
	t.Logf("输入: x↔y 互相背书; 实际输出: %v; 判定依据: 背书成环整体拒绝", err)
	if !errors.Is(err, ErrEndorsementCycle) {
		t.Fatalf("得到 %v，期望 %v", err, ErrEndorsementCycle)
	}
	if err := s.RevokeKey("x", 20); !errors.Is(err, ErrKeyNotFound) {
		t.Fatalf("成环登记后 x 不应存在: 得到 %v，期望 %v", err, ErrKeyNotFound)
	}
	err = s.RegisterKeys(Key{ID: "z", ValidFrom: 10, Endorsement: &Endorsement{Endorser: "z", Time: 15}})
	t.Logf("输入: z 自背书; 实际输出: %v; 判定依据: 自背书是长度为 1 的环", err)
	if !errors.Is(err, ErrEndorsementCycle) {
		t.Fatalf("自背书: 得到 %v，期望 %v", err, ErrEndorsementCycle)
	}
	must(t, s.RegisterKeys(Key{ID: "ok", ValidFrom: 10, Endorsement: &Endorsement{Endorser: "root", Time: 15}}))
	t.Log("被拒绝的登记未改变状态，后续合法登记照常成功")
}

// 七类签名结果互斥：每个用例精确命中且仅命中一类。
func TestSevenReasonsMutualExclusion(t *testing.T) {
	forged := map[string]bool{"c-forged": true}
	v := verifyFunc(func(objectID string, sig Signature) CryptoResult {
		if forged[objectID] {
			return CryptoInvalid
		}
		if sig.KeyID == "ghost" {
			return CryptoKeyUnknown
		}
		return CryptoValid
	})
	s := newRootedService(t, v,
		Key{ID: "k-notyet", ValidFrom: 50, Endorsement: &Endorsement{Endorser: "root", Time: 15}},
		Key{ID: "k-expired", ValidFrom: 10, ValidUntil: pt(30), Endorsement: &Endorsement{Endorser: "root", Time: 15}},
		Key{ID: "k-revoked", ValidFrom: 10, RevokedAt: pt(30), Endorsement: &Endorsement{Endorser: "root", Time: 15}},
		Key{ID: "k-mid", ValidFrom: 10}, // 无背书，链断
		Key{ID: "k-broken", ValidFrom: 10, Endorsement: &Endorsement{Endorser: "k-mid", Time: 15}},
	)
	cases := []struct {
		id     string
		keyID  string
		signAt Time
		want   Reason
		basis  string
	}{
		{"c-trusted", "root", 20, ReasonTrusted, "签名有效且签署时刻受信"},
		{"c-forged", "root", 20, ReasonForged, "校验器回答无效"},
		{"c-unknown", "ghost", 20, ReasonUnknownKey, "校验器回答密钥未知"},
		{"c-notyet", "k-notyet", 20, ReasonNotYetValid, "签署时刻 20 < 生效时刻 50"},
		{"c-expired", "k-expired", 40, ReasonExpired, "签署时刻 40 >= 失效时刻 30"},
		{"c-revoked", "k-revoked", 40, ReasonRevoked, "签署时刻 40 >= 吊销时刻 30"},
		{"c-broken", "k-broken", 20, ReasonBrokenEndorsement, "背书者 k-mid 无背书，链未锚定到根"},
	}
	for _, tc := range cases {
		got := judgeTip(t, s, Commit{ID: tc.id, Parents: []string{"c0"}, AuthorTime: 5,
			Sig: &Signature{KeyID: tc.keyID, Time: tc.signAt}}, 200)
		t.Logf("输入: 提交=%s 密钥=%s 签署时刻=%d; 实际输出: %v; 判定依据: %s",
			tc.id, tc.keyID, tc.signAt, got.Reason, tc.basis)
		wantID := tc.id
		if tc.want == ReasonTrusted {
			wantID = "" // 全链可信时无不可信提交
		}
		if got.Reason != tc.want || got.CommitID != wantID {
			t.Errorf("%s: 得到 (%s,%s)，期望 (%s,%s)", tc.id, got.CommitID, got.Reason, wantID, tc.want)
		}
	}
}

// newRootedService 登记根密钥 root（区间 [10,100)）、锚点提交 c0 与锚点标签，
// 并把 extraKeys 一并登记。校验器默认全部回答「有效」。
func newRootedService(t *testing.T, v Verifier, extraKeys ...Key) *Service {
	t.Helper()
	if v == nil {
		v = verifyFunc(func(string, Signature) CryptoResult { return CryptoValid })
	}
	s := NewService(v)
	must(t, s.AddCommit(Commit{ID: "c0", AuthorTime: 5, Sig: &Signature{KeyID: "root", Time: 10}}))
	must(t, s.AddTag(Tag{Name: "anchor", CommitID: "c0", Sig: &Signature{KeyID: "root", Time: 10}}))
	keys := append([]Key{{ID: "root", ValidFrom: 10, ValidUntil: pt(100)}}, extraKeys...)
	must(t, s.RegisterKeys(keys...))
	must(t, s.SetAnchor("anchor", "root"))
	return s
}

// judgeTip 在 c0 之上追加一个提交，并以其为顶端在 now 时刻做链路裁决。
func judgeTip(t *testing.T, s *Service, c Commit, now Time) Verdict {
	t.Helper()
	must(t, s.AddCommit(c))
	v, err := s.VerifyBranch(c.ID, now)
	if err != nil {
		t.Fatalf("裁决意外报错: %v", err)
	}
	return v
}

// 有效区间 [from,until) 与吊销时刻的边界取等：生效取等可信，
// 失效/吊销取等即不受信；背书时刻在背书者边界上同理。
func TestBoundaryEquality(t *testing.T) {
	s := newRootedService(t, nil,
		Key{ID: "k1", ValidFrom: 10, Endorsement: &Endorsement{Endorser: "root", Time: 15}},
		Key{ID: "k2", ValidFrom: 10, ValidUntil: pt(20), Endorsement: &Endorsement{Endorser: "root", Time: 15}},
		Key{ID: "k3", ValidFrom: 10, RevokedAt: pt(20), Endorsement: &Endorsement{Endorser: "root", Time: 15}},
		// mid 是背书者：区间 [10,100)，吊销于 30，背书链经 root 锚定
		Key{ID: "mid", ValidFrom: 10, ValidUntil: pt(100), RevokedAt: pt(30),
			Endorsement: &Endorsement{Endorser: "root", Time: 15}},
		Key{ID: "k4", ValidFrom: 10, Endorsement: &Endorsement{Endorser: "mid", Time: 10}},
		Key{ID: "k5", ValidFrom: 10, Endorsement: &Endorsement{Endorser: "mid", Time: 100}},
		Key{ID: "k6", ValidFrom: 10, Endorsement: &Endorsement{Endorser: "mid", Time: 30}},
	)
	cases := []struct {
		name     string
		keyID    string
		signTime Time
		want     Reason
		basis    string
	}{
		{"生效取等", "k1", 10, ReasonTrusted, "签署时刻==生效时刻，闭区间受信"},
		{"失效前一刻", "k2", 19, ReasonTrusted, "签署时刻<失效时刻，仍受信"},
		{"失效取等", "k2", 20, ReasonExpired, "签署时刻==失效时刻，开区间不受信"},
		{"吊销前一刻", "k3", 19, ReasonTrusted, "签署时刻<吊销时刻，仍受信"},
		{"吊销取等", "k3", 20, ReasonRevoked, "签署时刻==吊销时刻，吊销后（含相等）不受信"},
		{"背书时刻取生效等", "k4", 15, ReasonTrusted, "背书时刻==背书者生效时刻，背书有效"},
		{"背书时刻取失效等", "k5", 15, ReasonBrokenEndorsement, "背书时刻==背书者失效时刻，背书无效"},
		{"背书时刻取吊销等", "k6", 15, ReasonBrokenEndorsement, "背书时刻==背书者吊销时刻，背书无效"},
	}
	for _, tc := range cases {
		got := judgeTip(t, s, Commit{
			ID: "c-" + tc.name, Parents: []string{"c0"}, AuthorTime: 5,
			Sig: &Signature{KeyID: tc.keyID, Time: tc.signTime},
		}, 200)
		t.Logf("输入: 密钥=%s 签署时刻=%d 验证时刻=200; 实际输出: %v (%s); 判定依据: %s",
			tc.keyID, tc.signTime, got.Reason, got.CommitID, tc.basis)
		if got.Reason != tc.want {
			t.Errorf("%s: 得到 %s，期望 %s", tc.name, got.Reason, tc.want)
		}
	}
}
