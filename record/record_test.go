package record

import (
	"encoding/hex"
	"errors"
	"testing"
)

func mustOK(t *testing.T, err error, ctx string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: unexpected error %v", ctx, err)
	}
}

func TestHashVectors(t *testing.T) {
	s := New(4320, 60)
	mustOK(t, s.AddUser("r", "内科", 1, nil), "add r")
	mustOK(t, s.OpenEnc("e1"), "open e1")
	mustOK(t, s.Create(1100, "r", "e1", "d1", "病史A"), "create")
	got := s.docs["d1"].Vers[0].Hash
	want := "ac16970d9b1f0d175ec726edef2a5a62045fd8b7de855135ce923e5f0e8a2d0d"
	if hex.EncodeToString(got) != want {
		t.Fatalf("h1=%s want %s", hex.EncodeToString(got), want)
	}
	mustOK(t, s.SignAt(1200, "r", "d1"), "sign")
	mustOK(t, s.Edit(1300, "r", "d1", "病史B"), "edit")
	got2 := s.docs["d1"].Vers[1].Hash
	want2 := "8b86e7bc1d957f65e892c9680ccf35daf92bb2eafdcdfd5dc00aa568896600cd"
	if hex.EncodeToString(got2) != want2 {
		t.Fatalf("h2=%s want %s", hex.EncodeToString(got2), want2)
	}
}

// SignAt 是供 record 包内测试直接使用的签名落地，避免与 sign 包循环依赖。
func (s *Store) SignAt(now int64, user, doc string) error {
	return s.RunOp(now, user, doc, func(u *User, d *Doc) error {
		if u.Name != d.Author {
			return ErrPermission
		}
		if d.Status != StatusDraft {
			return ErrState
		}
		s.MarkSigned(d, u, now, s.EffectiveSealed(d, now))
		return nil
	})
}

func TestHashCounts(t *testing.T) {
	build := func(n int) *Store {
		s := New(4320, 60)
		mustOK(t, s.AddUser("r", "内科", 2, nil), "add r")
		mustOK(t, s.OpenEnc("e"), "open")
		mustOK(t, s.Create(0, "r", "e", "d", "v0"), "create")
		for i := 1; i < n; i++ {
			mustOK(t, s.Edit(int64(i), "r", "d", "vx"), "edit")
		}
		return s
	}
	// Edit 每版恰好 1 次哈希，与版本数无关。
	s10 := build(10)
	c10 := s10.Hashes()
	s10k := build(10000)
	c10k := s10k.Hashes()
	if c10 != 10 || c10k != 10000 {
		t.Fatalf("edit counts = %d,%d, want 10,10000", c10, c10k)
	}
	// 一次封存恰好 1 次哈希。
	mustOK(t, s10.Discharge(20000, "e"), "discharge")
	before := s10.Hashes()
	mustOK(t, s10.SignAt(30000, "r", "d"), "late sign triggers sweep and seals")
	if s10.Hashes() != before+1 { // d 的封存恰好 1 次
		t.Fatalf("after seal hashes=%d, want %d", s10.Hashes(), before+1)
	}
	di, _ := s10.DocInfo("d")
	if di.Gen != 1 {
		t.Fatalf("d gen=%d want 1", di.Gen)
	}
	// Amend 恰好 1 次。
	before = s10.Hashes()
	mustOK(t, s10.AmendVia(30001, "r", "d", "补"), "amend")
	if s10.Hashes() != before+1 {
		t.Fatalf("amend hashes delta=%d want 1", s10.Hashes()-before)
	}
	// Verify 恰为 版本数 + 补记数 + 1。
	before = s10.Hashes()
	_, err := s10.Verify("d")
	mustOK(t, err, "verify")
	if s10.Hashes() != before+10+1+1 {
		t.Fatalf("verify delta=%d want %d", s10.Hashes()-before, 12)
	}
}

// AmendVia 为 record 包测试用补记落地。
func (s *Store) AmendVia(now int64, user, doc, content string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, d, snap, h0, err := s.BeginTx(now, user, doc)
	if err != nil {
		return err
	}
	s.Sweep(now)
	if !s.EffectiveSealed(d, now) {
		s.Rollback(snap, h0)
		return ErrState
	}
	s.AppendAmendment(d, now, u.Name, content)
	s.Commit(now)
	return nil
}

func TestErrorOrder(t *testing.T) {
	s := New(4320, 60)
	mustOK(t, s.AddUser("r", "内科", 1, nil), "add")
	mustOK(t, s.OpenEnc("e"), "open")
	mustOK(t, s.Create(100, "r", "e", "d", "x"), "create")
	cases := []struct {
		name string
		err  error
	}{
		{"invalid ts", s.Edit(-1, "r", "d", "c")},
		{"clock back", s.Edit(50, "r", "d", "c")},
		{"no user", s.Edit(120, "ghost", "d", "c")},
		{"no doc", s.Edit(120, "r", "ghost", "c")},
		{"permission before state", s.Edit(120, "ghost", "ghost", "c")},
	}
	for _, c := range cases {
		if !errors.Is(c.err, errFor(c.name)) {
			t.Fatalf("%s: got %v want %v", c.name, c.err, errFor(c.name))
		}
	}
	// 无权限先于状态不符：不存在的用户改文档报不存在；存在但非作者改已封存文档报无权限。
	mustOK(t, s.AddUser("k", "外科", 3, nil), "add k")
	mustOK(t, s.Discharge(100, "e"), "dis")
	// 用一次被接受的迟签把封存落地（时刻取等 4420）。
	mustOK(t, s.SignAt(4420, "r", "d"), "op at seal time")
	if err := s.Edit(4501, "k", "d", "c"); !errors.Is(err, ErrPermission) {
		t.Fatalf("non-author edit sealed = %v want ErrPermission", err)
	}
}

func errFor(name string) error {
	switch name {
	case "invalid ts":
		return ErrInvalid
	case "clock back":
		return ErrClock
	case "no user", "no doc", "permission before state":
		return ErrNotFound
	}
	return nil
}

func TestReplayDeterministic(t *testing.T) {
	run := func() []byte {
		s := New(4320, 60)
		mustOK(t, s.AddUser("r", "内科", 1, nil), "u")
		mustOK(t, s.OpenEnc("e"), "enc")
		mustOK(t, s.Discharge(1000, "e"), "dis")
		mustOK(t, s.Create(1100, "r", "e", "d", "病史A"), "c")
		mustOK(t, s.SignAt(1200, "r", "d"), "s1")
		mustOK(t, s.Edit(1300, "r", "d", "病史B"), "e")
		mustOK(t, s.SignAt(1400, "r", "d"), "s2")
		mustOK(t, s.AmendVia(5320, "r", "d", "补"), "accepted op seals")
		di, _ := s.DocInfo("d")
		return di.SealHash
	}
	a, b := run(), run()
	want := "07076f30f6c9152234bcece000b0a532b44740238d10555f0cc55080ee54ef99"
	if hex.EncodeToString(a) != want || hex.EncodeToString(a) != hex.EncodeToString(b) {
		t.Fatal("sealHash not byte-identical across replays")
	}
}
