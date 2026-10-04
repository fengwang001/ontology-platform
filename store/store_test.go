package store

import (
	"errors"
	"reflect"
	"testing"
)

func TestAddValidation(t *testing.T) {
	cases := []struct {
		name    string
		id      int64
		owners  []string
		wantErr error
	}{
		{"id 下界", 1, []string{"s"}, nil},
		{"id 上界", 1_000_000_000, []string{"s"}, nil},
		{"id 为 0", 0, []string{"s"}, ErrInvalidArgument},
		{"id 为负", -1, []string{"s"}, ErrInvalidArgument},
		{"id 超上界", 1_000_000_001, []string{"s"}, ErrInvalidArgument},
		{"无主体", 10, nil, nil},
		{"四主体", 11, []string{"a", "b", "c", "d"}, nil},
		{"五主体", 12, []string{"a", "b", "c", "d", "e"}, ErrInvalidArgument},
		{"主体重复", 13, []string{"a", "a"}, ErrInvalidArgument},
		{"空主体名", 14, []string{""}, ErrInvalidArgument},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := New()
			err := s.Add(tc.id, tc.owners)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("Add(%d, %v) = %v, want %v", tc.id, tc.owners, err, tc.wantErr)
			}
			if tc.wantErr == nil && !s.Exists(tc.id) {
				t.Fatalf("Add(%d) 成功后记录不存在", tc.id)
			}
			if tc.wantErr != nil && s.Exists(tc.id) {
				t.Fatalf("Add(%d) 被拒后记录不应存在", tc.id)
			}
		})
	}
}

func TestAddDuplicate(t *testing.T) {
	s := New()
	if err := s.Add(1, []string{"s"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(1, []string{"t"}); !errors.Is(err, ErrExists) {
		t.Fatalf("重复 Add = %v, want ErrExists", err)
	}
	if got := s.Owners(1); !reflect.DeepEqual(got, []string{"s"}) {
		t.Fatalf("重复 Add 改变了 owners: %v", got)
	}
}

func TestOwnerIndex(t *testing.T) {
	s := New()
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(s.Add(3, []string{"s", "t"}))
	must(s.Add(1, []string{"s"}))
	must(s.Add(2, nil))
	if got := s.OwnedBy("s"); !reflect.DeepEqual(got, []int64{1, 3}) {
		t.Fatalf("OwnedBy(s) = %v", got)
	}
	if got := s.OwnedBy("nobody"); len(got) != 0 {
		t.Fatalf("OwnedBy(nobody) = %v", got)
	}
	s.RemoveOwner(3, "s")
	if got := s.OwnedBy("s"); !reflect.DeepEqual(got, []int64{1}) {
		t.Fatalf("摘除后 OwnedBy(s) = %v", got)
	}
	if got := s.Owners(3); !reflect.DeepEqual(got, []string{"t"}) {
		t.Fatalf("摘除后 Owners(3) = %v", got)
	}
	s.Delete(3)
	if s.Exists(3) || len(s.OwnedBy("t")) != 0 {
		t.Fatalf("Delete 后索引未清理: exists=%v t=%v", s.Exists(3), s.OwnedBy("t"))
	}
	if s.Len() != 2 {
		t.Fatalf("Len = %d, want 2", s.Len())
	}
}
