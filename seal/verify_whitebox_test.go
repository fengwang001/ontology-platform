package seal

import (
	"testing"

	"ontology/record"
	"ontology/sign"
)

// Verify 必须检出版本链、补记链与 sealHash 的任何不一致。
func TestVerifyDetectsTamper(t *testing.T) {
	r := record.New(4320, 60)
	s := Wrap(r)
	sn := sign.Wrap(r)
	if err := r.AddUser("r", "内科", 2, 0); err != nil {
		t.Fatal(err)
	}
	if err := r.OpenEnc("e"); err != nil {
		t.Fatal(err)
	}
	if err := r.Create(0, "r", "e", "d", []byte("c")); err != nil {
		t.Fatal(err)
	}
	if err := sn.Sign(1, "r", "d"); err != nil {
		t.Fatal(err)
	}
	if err := s.Seal(2, "r", "d"); err == nil {
		t.Fatal("非归档员手动封存应拒绝")
	}

	// 给归档角色后封存。
	r.Lock()
	r.UserOf("r").Roles |= record.RoleArchivist
	r.Unlock()
	if err := s.Seal(3, "r", "d"); err != nil {
		t.Fatal(err)
	}
	if err := s.Amend(4, "r", "d", []byte("a")); err != nil {
		t.Fatal(err)
	}

	res, err := s.Verify("d")
	if err != nil || !res.VersionChainOK || !res.AmendChainOK || res.SealHash == nil {
		t.Fatalf("初始应完好: %+v %v", res, err)
	}

	// 篡改某一版内容：版本链重算失败。
	r.Lock()
	d := r.Lookup("d")
	d.Versions[0].Content[0]++
	r.Unlock()
	if res, _ := s.Verify("d"); res.VersionChainOK {
		t.Fatal("版本链被篡改应检出")
	}

	// 篡改补记哈希：补记链失败。
	r.Lock()
	d = r.Lookup("d")
	d.Versions[0].Content[0]--
	d.Amends[0].Hash[0]++
	r.Unlock()
	if res, _ := s.Verify("d"); res.AmendChainOK || !res.VersionChainOK {
		t.Fatal("应只检出补记链失败")
	}

	// gen 与 sealHash 不一致：Verify 重算的 sealHash 与存储不同。
	r.Lock()
	d = r.Lookup("d")
	d.Amends[0].Hash[0]--
	stored := append([]byte(nil), d.SealHash...)
	d.Gen++
	r.Unlock()
	res, _ = s.Verify("d")
	if string(res.SealHash) == string(stored) {
		t.Fatal("gen 变化后重算 sealHash 必须变化")
	}
}
