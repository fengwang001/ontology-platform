package attest

import "testing"

func TestMissingRequiredValidityAndScanned(t *testing.T) {
	s := NewStore()
	trusted := map[string]bool{"ci": true}
	Add := func(typ, signer string, exp int64) {
		s.Add(Attestation{Digest: "d", Type: typ, Signer: signer, Exp: exp})
	}
	Add("test", "ci", 10)     // 有效（t=5）
	Add("test", "robot", 100) // 签名者不受信
	Add("scan", "ci", 4)      // 已过期（t=5）
	Add("test", "ci", 100)    // 重复但有效

	if got := s.MissingRequired("d", []string{"test", "scan"}, trusted, 5); got != "scan" {
		t.Fatalf("missing=%q want scan", got)
	}
	if s.Scanned() != 4 {
		t.Fatalf("scanned=%d want 4 (digest's own bucket only)", s.Scanned())
	}
	s.Revoke("ci")
	if got := s.MissingRequired("d", []string{"test"}, trusted, 5); got != "test" {
		t.Fatalf("after revoke missing=%q want test", got)
	}
	// 不存在的摘要：检视 0 条，首个 required 即缺失。
	if got := s.MissingRequired("other", []string{"test"}, trusted, 5); got != "test" {
		t.Fatalf("unknown digest missing=%q", got)
	}
	if s.Scanned() != 0 {
		t.Fatalf("unknown digest scanned=%d want 0", s.Scanned())
	}
}
