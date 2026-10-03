package meta

import (
	"fmt"
	"reflect"
	"testing"
)

func strptr(s string) *string { return &s }

func TestStorePutDeleteCounts(t *testing.T) {
	s := New()
	s.Put("k1", 1, Fields{Size: 10})
	s.Put("k2", 3, Fields{Size: 1, Etag: strptr("e"), Tags: &[]string{"t"}})
	if c := s.CountByLevel(); c != [4]int{0, 1, 0, 1} {
		t.Fatalf("计数 = %v", c)
	}
	// 覆盖写：旧级别减一、新级别加一
	s.Put("k1", 2, Fields{Size: 10, Etag: strptr("")})
	if c := s.CountByLevel(); c != [4]int{0, 0, 1, 1} {
		t.Fatalf("覆盖后计数 = %v", c)
	}
	if r, ok := s.Get("k1"); !ok || r.Level != 2 || r.Etag == nil || *r.Etag != "" {
		t.Fatalf("空串 etag 应视为已提供: %+v", r)
	}
	if !s.Delete("k2") {
		t.Fatal("Delete 应成功")
	}
	if c := s.CountByLevel(); c != [4]int{0, 0, 1, 0} {
		t.Fatalf("删除后计数 = %v", c)
	}
	if s.Delete("k2") {
		t.Fatal("重复 Delete 应返回 false")
	}
}

func TestReadDegradedAndFieldFilter(t *testing.T) {
	s := New()
	s.Put("j", 3, Fields{Size: 5, Etag: strptr("e"), Tags: &[]string{"t"}})

	v3, _ := s.Read("j", 3)
	if v3.Degraded || v3.Etag == nil || v3.Tags == nil {
		t.Fatalf("max=3 应见全部字段且不降级: %+v", v3)
	}
	v2, _ := s.Read("j", 2)
	if !v2.Degraded || v2.Etag == nil || *v2.Etag != "e" || v2.Tags != nil {
		t.Fatalf("max=2 应见 size/etag、隐藏 tags 且降级: %+v", v2)
	}
	v1, _ := s.Read("j", 1)
	if !v1.Degraded || v1.Etag != nil || v1.Tags != nil || v1.Size != 5 {
		t.Fatalf("max=1 应只见 size 且降级: %+v", v1)
	}

	// 缺省字段不参与过滤；空集合与缺省不同
	s.Put("m", 3, Fields{Size: 1, Tags: &[]string{}})
	vm, _ := s.Read("m", 3)
	if vm.Etag != nil || vm.Tags == nil || !reflect.DeepEqual(*vm.Tags, []string{}) {
		t.Fatalf("空 tags 集合应保留且 etag 缺省: %+v", vm)
	}
}

func TestResidueUsesCountersWithoutScan(t *testing.T) {
	for _, n := range []int{100, 10000} {
		s := New()
		for i := 0; i < n; i++ {
			s.Put(keyOf(i), 1, Fields{Size: 1})
		}
		if cnt, hi := s.Residue(1); cnt != 0 || hi != 0 {
			t.Fatalf("n=%d 级别等于 target 不算残留, got cnt=%d hi=%d", n, cnt, hi)
		}
		// 大 1 算残留
		s.Put(keyOf(0), 2, Fields{Size: 1})
		if cnt, hi := s.Residue(1); cnt != 1 || hi != 2 {
			t.Fatalf("n=%d 残留 cnt=%d hi=%d", n, cnt, hi)
		}
		s.Put(keyOf(1), 3, Fields{Size: 1})
		if cnt, hi := s.Residue(1); cnt != 2 || hi != 3 {
			t.Fatalf("n=%d 最高级别应为 3, got cnt=%d hi=%d", n, cnt, hi)
		}
		// 等于 target 不算
		if cnt, _ := s.Residue(3); cnt != 0 {
			t.Fatalf("n=%d target=3 应无残留, got %d", n, cnt)
		}
		if got := s.Scanned(); got != 0 {
			t.Fatalf("n=%d 残留判定扫描了记录, scanned=%d", n, got)
		}
	}
}

func keyOf(i int) string {
	return fmt.Sprintf("k%05d", i)
}
