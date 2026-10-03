package meta

import (
	"errors"
	"testing"
)

func strptr(v string) *string { return &v }

func TestStore(t *testing.T) {
	s := New()
	etag := strptr("e")
	tags := map[string]string{"t": "v"}
	if err := s.Put("k", Record{Level: 3, Size: 5, Etag: etag, Tags: tags}); err != nil {
		t.Fatal(err)
	}
	if st := s.Stats(); st.CountByLvl != [3]int{0, 0, 1} {
		t.Fatalf("首次写入后计数=%v", st.CountByLvl)
	}
	// 覆盖写：级别 3 -> 1，旧级别减一、新级别加一
	if err := s.Put("k", Record{Level: 1, Size: 6}); err != nil {
		t.Fatal(err)
	}
	if st := s.Stats(); st.CountByLvl != [3]int{1, 0, 0} {
		t.Fatalf("覆盖后计数=%v", st.CountByLvl)
	}
	// 缺省(nil) 与空值的区别被原样保留
	got, ok := s.Get("k")
	if !ok || got.Etag != nil || got.Tags != nil {
		t.Fatalf("缺省字段应为 nil: %+v", got)
	}
	if err := s.Put("k2", Record{Level: 2, Size: 1, Etag: strptr(""), Tags: map[string]string{}}); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get("k2"); got.Etag == nil || *got.Etag != "" || got.Tags == nil || len(got.Tags) != 0 {
		t.Fatalf("空串/空集合应与缺省区分: %+v", got)
	}

	t.Run("参数校验", func(t *testing.T) {
		if err := s.Put("", Record{Level: 1}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("空 key: %v", err)
		}
		if err := s.Put("x", Record{Level: 4}); !errors.Is(err, ErrInvalid) {
			t.Fatalf("级别越界: %v", err)
		}
		if err := s.Delete("nope"); !errors.Is(err, ErrNotFound) {
			t.Fatalf("删除不存在: %v", err)
		}
	})

	t.Run("残留：等于 target 不算，大 1 算", func(t *testing.T) {
		_ = s.Delete("k2")
		// k 在级别 1；target=1 时无残留
		if _, dirty := s.Residue(1); dirty {
			t.Fatal("级别等于 target 不应算残留")
		}
		_ = s.Put("k", Record{Level: 2, Size: 6})
		_ = s.Put("k3", Record{Level: 3, Size: 7})
		res, dirty := s.Residue(1)
		if !dirty || res.Count != 2 || res.HighLevel != 3 {
			t.Fatalf("target=1 残留=%+v dirty=%v", res, dirty)
		}
		if res, dirty = s.Residue(2); !dirty || res.Count != 1 || res.HighLevel != 3 {
			t.Fatalf("target=2 仅级别3残留=%+v dirty=%v", res, dirty)
		}
		if _, dirty = s.Residue(3); dirty {
			t.Fatal("target=3 不应有残留")
		}
	})

	t.Run("Delete 减计数且正式路径零扫描", func(t *testing.T) {
		if err := s.Delete("k3"); err != nil {
			t.Fatal(err)
		}
		if st := s.Stats(); st.CountByLvl != [3]int{0, 1, 0} {
			t.Fatalf("删除后计数=%v", st.CountByLvl)
		}
		if n := s.Scanned(); n != 0 {
			t.Fatalf("正式 Residue 路径不应产生扫描, scanned=%d", n)
		}
		s.ScanAbove(0)
		if n := s.Scanned(); n != 1 {
			t.Fatalf("朴素扫描应自增, scanned=%d", n)
		}
	})
}
