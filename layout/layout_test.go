package layout

import (
	"fmt"
	"testing"
)

// TestOffsetsTightNoOverlap 偏移=前缀和，区间互不重叠、覆盖整个缓冲无空洞。
func TestOffsetsTightNoOverlap(t *testing.T) {
	cases := [][]Spec{
		{{Name: "a", Width: 1}},
		{{Name: "a", Width: 2}, {Name: "b", Width: 1}, {Name: "c", Width: 4}, {Name: "d", Width: 2, Signed: true}},
		{{Name: "x", Width: 8}, {Name: "y", Width: 8, Signed: true}, {Name: "z", Width: 4}},
		{{Name: "p", Width: 1}, {Name: "q", Width: 1}, {Name: "r", Width: 1}, {Name: "s", Width: 1}},
	}
	for _, specs := range cases {
		s, err := NewSchema(specs)
		if err != nil {
			t.Fatalf("NewSchema(%v): %v", specs, err)
		}
		covered := make([]bool, s.Total())
		off := 0
		for i, f := range s.Fields() {
			if f.Offset != off {
				t.Errorf("case %d field %q: offset %d, want %d", i, f.Name, f.Offset, off)
			}
			if f.Width != specs[i].Width {
				t.Errorf("field %q: width %d, want %d", f.Name, f.Width, specs[i].Width)
			}
			for j := f.Offset; j < f.Offset+f.Width; j++ {
				if covered[j] {
					t.Errorf("field %q: byte %d overlaps another field", f.Name, j)
				}
				covered[j] = true
			}
			off += f.Width
		}
		for j, c := range covered {
			if !c {
				t.Errorf("byte %d is a hole", j)
			}
		}
	}
}

// TestNewSchemaRejects 非法宽度与重名必须报错。
func TestNewSchemaRejects(t *testing.T) {
	if _, err := NewSchema([]Spec{{Name: "a", Width: 3}}); err == nil {
		t.Error("width 3 accepted")
	}
	if _, err := NewSchema([]Spec{{Name: "a", Width: 1}, {Name: "a", Width: 2}}); err == nil {
		t.Error("duplicate name accepted")
	}
}

// TestLookupConstantComparisons 按名查最后一个字段的比较次数不随 m 增长。
func TestLookupConstantComparisons(t *testing.T) {
	const limit = 3 // 与 m 无关的小常数
	for _, m := range []int{100, 1000, 10000} {
		specs := make([]Spec, m)
		for i := range specs {
			specs[i] = Spec{Name: fmt.Sprintf("f%d", i), Width: 1}
		}
		s, err := NewSchema(specs)
		if err != nil {
			t.Fatalf("m=%d: %v", m, err)
		}
		last := fmt.Sprintf("f%d", m-1)
		if _, ok := s.Field(last); !ok {
			t.Fatalf("m=%d: last field not found", m)
		}
		if n := s.lastN.Load(); n > limit {
			t.Errorf("m=%d: lookup compared %d fields, want <= %d (O(1))", m, n, limit)
		}
	}
}
