package ontology

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"testing"
)

func testMac(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func mustNew(t *testing.T, cfg Config) *Service {
	t.Helper()
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

func baseCfg() Config {
	return Config{K: []byte("root-key"), Mac: testMac, Cm: 32, Lc: 256, Rm: 1000}
}

func asErr(t *testing.T, err error) *Error {
	t.Helper()
	var e *Error
	if !errors.As(err, &e) {
		t.Fatalf("not *ontology.Error: %v", err)
	}
	return e
}

func TestInvalidConfig(t *testing.T) {
	cfg := baseCfg()
	cfg.K = nil
	if _, err := New(cfg); err == nil {
		t.Fatal("empty K accepted")
	}
	for _, mutate := range []func(*Config){
		func(c *Config) { c.Cm = 0 },
		func(c *Config) { c.Cm = 33 },
		func(c *Config) { c.Lc = 0 },
		func(c *Config) { c.Lc = 257 },
		func(c *Config) { c.Rm = 0 },
		func(c *Config) { c.Rm = 1_000_001 },
		func(c *Config) { c.Mac = func(_, _ []byte) []byte { return nil } },
	} {
		bad := baseCfg()
		mutate(&bad)
		if _, err := New(bad); err == nil {
			t.Fatalf("bad config accepted: %+v", bad)
		}
	}
}

func TestAttenuateOrderChangesSig(t *testing.T) {
	cfg := baseCfg()
	s := mustNew(t, cfg)
	t0, err := s.Mint([]byte("id"), nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	t1, err := Attenuate(cfg, t0, []byte("ops:a"))
	if err != nil {
		t.Fatal(err)
	}
	t2, err := Attenuate(cfg, t1, []byte("amt:5"))
	if err != nil {
		t.Fatal(err)
	}
	u1, err := Attenuate(cfg, t0, []byte("amt:5"))
	if err != nil {
		t.Fatal(err)
	}
	u2, err := Attenuate(cfg, u1, []byte("ops:a"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(t2.Sig, u2.Sig) {
		t.Fatal("caveat order must change the signature")
	}
	// Attenuate does not authenticate the old token.
	fake := &Token{ID: []byte("x"), Sig: []byte("deadbeef")}
	if _, err := Attenuate(cfg, fake, []byte("ops:a")); err != nil {
		t.Fatalf("Attenuate must not check authenticity: %v", err)
	}
	// Invalid precedes limit.
	small := baseCfg()
	small.Cm = 1
	v1, err := s.Mint([]byte("id2"), [][]byte{[]byte("amt:1")}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Attenuate(small, v1, []byte("")); err == nil {
		t.Fatal("empty caveat must be invalid")
	}
	if e := asErr(t, func() error {
		_, err := Attenuate(small, v1, []byte("amt:2"))
		return err
	}()); e.Kind != RejectLimit {
		t.Fatalf("got %v want limit", e.Kind)
	}
}

func TestResPrefix(t *testing.T) {
	cfg := baseCfg()
	s := mustNew(t, cfg)
	mint := func(cav string) *Token {
		tok, err := s.Mint([]byte("r-"+cav), [][]byte{[]byte(cav)}, 0)
		if err != nil {
			t.Fatal(err)
		}
		return tok
	}
	check := func(tok *Token, res string, wantOK bool) {
		t.Helper()
		err := s.Verify(tok, Request{Op: "x", Res: res, Amount: 0}, 0)
		if wantOK && err != nil {
			t.Fatalf("res %s: unexpected %v", res, err)
		}
		if !wantOK {
			e := asErr(t, err)
			if e.Kind != RejectCaveat || e.Problem != CaveatUnsatisfied {
				t.Fatalf("res %s: got kind=%v problem=%v", res, e.Kind, e.Problem)
			}
		}
	}
	a := mint("res:/a")
	check(a, "/a", true)
	check(a, "/a/b", true)
	check(a, "/ab", false)
	check(a, "/", false)
	root := mint("res:/")
	check(root, "/", true)
	check(root, "/anything/deep", true)

	// Malformed res caveats can only enter via Attenuate.
	t0, _ := s.Mint([]byte("m"), nil, 1)
	for _, bad := range []string{"res:", "res:a", "res:/a/", "res:/a//b", "res://"} {
		tb, err := Attenuate(cfg, t0, []byte(bad))
		if err != nil {
			t.Fatal(err)
		}
		e := asErr(t, s.Verify(tb, Request{Op: "x", Res: "/a", Amount: 0}, 1))
		if e.Kind != RejectCaveat || e.Problem != CaveatMalformed || e.Index != 1 {
			t.Fatalf("%s: got %+v", bad, e)
		}
	}
}

func TestExpAndAmtBoundaries(t *testing.T) {
	cfg := baseCfg()
	s := mustNew(t, cfg)
	tok, err := s.Mint([]byte("b"), [][]byte{[]byte("exp:1000"), []byte("amt:5")}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Verify(tok, Request{Op: "x", Res: "/", Amount: 5}, 50); err != nil {
		t.Fatalf("amt at cap should pass at now=50: %v", err)
	}
	e := asErr(t, s.Verify(tok, Request{Op: "x", Res: "/", Amount: 6}, 51))
	if e.Problem != CaveatUnsatisfied || e.Index != 2 {
		t.Fatalf("amt over cap: kind=%s idx=%d", e.Error(), e.Index)
	}
	// Boundary: now strictly less than N; 999 < 1000 passes, 1000 fails.
	if err := s.Verify(tok, Request{Op: "x", Res: "/", Amount: 5}, 999); err != nil {
		t.Fatalf("now=999 should pass: %v", err)
	}
	e = asErr(t, s.Verify(tok, Request{Op: "x", Res: "/", Amount: 5}, 1000))
	if e.Problem != CaveatUnsatisfied || e.Index != 1 {
		t.Fatalf("exp equal now: kind=%s idx=%d", e.Error(), e.Index)
	}
	t0, _ := s.Mint([]byte("z"), nil, 1000)
	for _, bad := range []string{"exp:01", "exp:1000000000000001", "amt:1000000000001", "amt:"} {
		tb, _ := Attenuate(cfg, t0, []byte(bad))
		verr := s.Verify(tb, Request{Op: "x", Res: "/", Amount: 0}, 1000)
		if verr == nil {
			t.Fatalf("%s: unexpected success", bad)
		}
		e := asErr(t, verr)
		if e.Problem != CaveatMalformed {
			t.Fatalf("%s: want malformed got %+v", bad, e)
		}
	}
}

func TestOpsGrammar(t *testing.T) {
	cfg := baseCfg()
	s := mustNew(t, cfg)
	tok, _ := s.Mint([]byte("o"), [][]byte{[]byte("ops:read,list-1")}, 0)
	if err := s.Verify(tok, Request{Op: "list-1", Res: "/", Amount: 0}, 0); err != nil {
		t.Fatal(err)
	}
	e := asErr(t, s.Verify(tok, Request{Op: "write", Res: "/", Amount: 0}, 0))
	if e.Problem != CaveatUnsatisfied {
		t.Fatalf("got %+v", e)
	}
	t0, _ := s.Mint([]byte("o2"), nil, 0)
	for _, bad := range []string{"ops:", "ops:a,", "ops:,a", "ops:a,,b", "ops:A", "ops:a b"} {
		tb, _ := Attenuate(cfg, t0, []byte(bad))
		e := asErr(t, s.Verify(tb, Request{Op: "a", Res: "/", Amount: 0}, 0))
		if e.Problem != CaveatMalformed {
			t.Fatalf("%s: want malformed got %+v", bad, e)
		}
	}
}

func TestCaveatOrderUnknownMalformedUnsatisfied(t *testing.T) {
	cfg := baseCfg()
	s := mustNew(t, cfg)
	t0, _ := s.Mint([]byte("c"), nil, 0)
	req := Request{Op: "x", Res: "/", Amount: 0}
	cases := []struct {
		cavs    []string
		index   int
		problem CaveatProblem
	}{
		{[]string{"weird:1", "exp:bad"}, 1, CaveatUnknown},
		{[]string{"exp:bad", "weird:1"}, 1, CaveatMalformed},
		{[]string{"ops:nope", "weird:1"}, 1, CaveatUnsatisfied},
		{[]string{"ops:x", "exp:bad", "weird:1"}, 2, CaveatMalformed},
		{[]string{"ops:x", "weird:1"}, 2, CaveatUnknown},
	}
	for _, tc := range cases {
		tok := t0
		var err error
		for _, c := range tc.cavs {
			tok, err = Attenuate(cfg, tok, []byte(c))
			if err != nil {
				t.Fatal(err)
			}
		}
		e := asErr(t, s.Verify(tok, req, 0))
		if e.Index != tc.index || e.Problem != tc.problem {
			t.Fatalf("%v: got idx=%d prob=%d want idx=%d prob=%d",
				tc.cavs, e.Index, e.Problem, tc.index, tc.problem)
		}
	}
}
