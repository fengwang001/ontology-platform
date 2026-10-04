package secret

import (
	"errors"
	"testing"
)

func TestValidName(t *testing.T) {
	ok := []string{"A", "TOKEN", "_A", "A_B1", "ABCDEFGHIJKLMNOPQRSTUVWXYZ_0123456789_ABCDEFGHIJKLMNOPQRSTUVWXYZ"}
	bad := []string{"", "a", "1A", "A-B", "A B", "A.B", "A1\n", longName()}
	for _, n := range ok {
		if !ValidName(n) {
			t.Errorf("ValidName(%q) = false, want true", n)
		}
	}
	for _, n := range bad {
		if ValidName(n) {
			t.Errorf("ValidName(%q) = true, want false", n)
		}
	}
}

func longName() string {
	b := make([]byte, 65)
	for i := range b {
		b[i] = 'A'
	}
	return string(b)
}

func TestSetVersionsAndDelete(t *testing.T) {
	s := NewStore()
	if err := s.AddRepo("r1", true, []string{"main"}); err != nil {
		t.Fatal(err)
	}
	if err := s.AddEnv("r1", "prod", []string{"main"}); err != nil {
		t.Fatal(err)
	}

	if err := s.SetOrg("TOKEN", "o1", All, nil, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetOrg("TOKEN", "o2", All, nil, false); err != nil {
		t.Fatal(err)
	}
	if err := s.SetRepo("r1", "TOKEN", "rv", true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEnv("r1", "prod", "KEY", "e1", false); err != nil {
		t.Fatal(err)
	}

	got := s.orgMeta["TOKEN"]
	if got.Version != 2 {
		t.Fatalf("org version = %d, want 2", got.Version)
	}

	err := s.View("r1", "prod", func(rd *Reader) error {
		e, ok := rd.Lookup("KEY")
		if !ok || e.Level != EnvLevel || e.Version != 1 || e.Value != "e1" {
			t.Fatalf("env lookup = %+v ok=%v", e, ok)
		}
		rv, ok := rd.Lookup("TOKEN")
		if !ok || rv.Level != RepoLevel || rv.Version != 1 || !rv.ProtectedOnly {
			t.Fatalf("repo lookup = %+v ok=%v", rv, ok)
		}
		if rd.Probes() != 3 {
			t.Fatalf("probes = %d, want 3", rd.Probes())
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteOrg("TOKEN"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteOrg("TOKEN"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second delete = %v, want ErrNotFound", err)
	}
}

func TestOrgVisibility(t *testing.T) {
	s := NewStore()
	if err := s.AddRepo("r1", true, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.AddRepo("r2", false, nil); err != nil {
		t.Fatal(err)
	}
	_ = s.SetOrg("ALLV", "a", All, nil, false)
	_ = s.SetOrg("PRIV", "p", Private, nil, false)
	_ = s.SetOrg("SEL", "x", Selected, []string{"r2"}, false)

	check := func(repo, name string, want bool) {
		t.Helper()
		err := s.View(repo, "", func(rd *Reader) error {
			_, ok := rd.Lookup(name)
			if ok != want {
				t.Errorf("%s/%s visible = %v, want %v (probes=%d)", repo, name, ok, want, rd.Probes())
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	check("r1", "ALLV", true)
	check("r2", "ALLV", true)
	check("r1", "PRIV", true)
	check("r2", "PRIV", false)
	check("r1", "SEL", false)
	check("r2", "SEL", true)
}

func TestRejectionOrder(t *testing.T) {
	s := NewStore()

	if err := s.AddRepo("", false, nil); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("AddRepo bad name = %v", err)
	}
	if err := s.AddRepo("r1", false, []string{"a*b"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("AddRepo bad pattern = %v", err)
	}
	if err := s.AddRepo("r1", false, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.AddRepo("r1", false, nil); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("dup AddRepo = %v", err)
	}

	if err := s.AddEnv("nope", "e", nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("AddEnv missing repo = %v", err)
	}
	if err := s.AddEnv("r1", "prod", []string{"x*y"}); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("AddEnv bad pattern = %v", err)
	}
	if err := s.AddEnv("r1", "prod", nil); err != nil {
		t.Fatal(err)
	}
	if err := s.AddEnv("r1", "prod", nil); !errors.Is(err, ErrAlreadyExists) {
		t.Fatalf("dup AddEnv = %v", err)
	}

	if err := s.SetRepo("ghost", "A", "v", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SetRepo missing repo = %v", err)
	}
	if err := s.SetRepo("r1", "bad-name", "v", false); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetRepo bad name = %v", err)
	}
	if err := s.SetRepo("r1", "A", "", false); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetRepo empty value = %v", err)
	}

	_ = s.SetOrg("O", "v", Selected, []string{"r1"}, false)
	if err := s.SetOrg("O", "v", Visibility(9), nil, false); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("SetOrg bad vis = %v", err)
	}

	// Invalid argument beats nonexistent repo/env for deletes.
	if err := s.DeleteRepoSecret("ghost", "bad-name"); !errors.Is(err, ErrInvalidArgument) {
		t.Fatalf("Delete invalid beats missing = %v", err)
	}
	if err := s.DeleteEnvSecret("ghost", "prod", "A"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("DeleteEnv missing repo = %v", err)
	}
	if err := s.DeleteRepoSecret("r1", "MISSING"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete missing secret = %v", err)
	}
}

func TestFailedSetKeepsVersion(t *testing.T) {
	s := NewStore()
	if err := s.AddRepo("r1", false, nil); err != nil {
		t.Fatal(err)
	}
	_ = s.SetOrg("A", "one", All, nil, false)
	if err := s.SetOrg("A", "", All, nil, false); !errors.Is(err, ErrInvalidArgument) {
		t.Fatal(err)
	}
	if s.orgMeta["A"].Version != 1 || s.orgMeta["A"].Value != "one" {
		t.Fatalf("failed set changed state: %+v", s.orgMeta["A"])
	}
}
