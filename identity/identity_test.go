package identity

import (
	"errors"
	"testing"

	"ontology/meter"
)

func TestLoginLogoutTable(t *testing.T) {
	type step struct {
		name    string
		device  string
		act     func(s *Service) error
		wantErr error
		who     string // 期望主体；"self" 表示设备自身
		bound   bool
	}

	newSvc := func() *Service {
		mt, _ := meter.New(3, 1000)
		mt.RedeemQuota([]byte("u"), []byte("a3"), 5)
		mt.RedeemQuota([]byte("u"), []byte("a4"), 15)
		mt.RedeemQuota([]byte("d"), []byte("a1"), 10)
		mt.RedeemQuota([]byte("d"), []byte("a2"), 20)
		return New(mt)
	}

	cases := []struct {
		name  string
		steps []step
	}{
		{
			name: "login merge then logout restores anonymous book",
			steps: []step{
				{"login", "d", func(s *Service) error { return s.Login(30, []byte("d"), []byte("u")) }, nil, "u", true},
				{"login same rejected", "d", func(s *Service) error { return s.Login(31, []byte("d"), []byte("u")) }, ErrAlreadyBound, "u", true},
				{"login other rejected", "d", func(s *Service) error { return s.Login(32, []byte("d"), []byte("v")) }, ErrAlreadyBound, "u", true},
				{"logout", "d", func(s *Service) error { return s.Logout([]byte("d")) }, nil, "self", false},
				{"logout again is no-op; not-bound checked by facade", "d", func(s *Service) error {
					if s.IsBound([]byte("d")) {
						return ErrAlreadyBound
					}
					return s.Logout([]byte("d"))
				}, nil, "self", false},
			},
		},
		{
			name: "anonymous principal before bind",
			steps: []step{
				{"noop", "d", func(s *Service) error { return nil }, nil, "self", false},
			},
		},
		{
			name: "multi devices same user",
			steps: []step{
				{"bind d1", "d1", func(s *Service) error { return s.Login(1, []byte("d1"), []byte("u")) }, nil, "u", true},
				{"bind d2", "d2", func(s *Service) error { return s.Login(1, []byte("d2"), []byte("u")) }, nil, "u", true},
				{"logout d1; d2 stays bound", "d2", func(s *Service) error { return s.Logout([]byte("d1")) }, nil, "u", true},
				{"d1 back to self", "d1", func(s *Service) error { return nil }, nil, "self", false},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSvc()
			for _, st := range tc.steps {
				err := st.act(s)
				if !errors.Is(err, st.wantErr) {
					t.Fatalf("%s: err=%v want %v", st.name, err, st.wantErr)
				}
				p, bound := s.Principal([]byte(st.device))
				if bound != st.bound {
					t.Fatalf("%s: bound=%v want %v", st.name, bound, st.bound)
				}
				want := st.who
				if want == "self" {
					want = st.device
				}
				if string(p) != want {
					t.Fatalf("%s: principal=%q want %q", st.name, p, want)
				}
			}
		})
	}
}

func TestMergeOnlyTouchesCurrentMonth(t *testing.T) {
	mt, _ := meter.New(2, 1000)
	mt.RedeemQuota([]byte("d"), []byte("old-a"), 10)
	mt.RedeemQuota([]byte("u"), []byte("old-u"), 20)

	mt.ResetTouched()
	mt.RedeemQuota([]byte("d"), []byte("n1"), 1001)
	mt.RedeemQuota([]byte("u"), []byte("n2"), 1002)
	mt.ResetTouched()
	s := New(mt)
	if err := s.Login(1003, []byte("d"), []byte("u")); err != nil {
		t.Fatal(err)
	}
	// 触碰数不超过双方本月解锁数之和（此处 2 条，旧月不可见）。
	if got := mt.Touched(); got > 2 {
		t.Fatalf("merge touched=%d, want <=2", got)
	}
	if mt.Used([]byte("u"), 1003) != 2 {
		t.Fatal("new-month merge should not see old month")
	}
}
