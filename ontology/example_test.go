package ontology

import (
	"reflect"
	"testing"
)

func TestSpecWorkedExample(t *testing.T) {
	r, err := NewReadahead(100, 2, 8, 4)
	if err != nil {
		t.Fatal(err)
	}

	d, a, e, err := r.Read(0, 1)
	if err != nil || !reflect.DeepEqual(d, []int{0}) || !reflect.DeepEqual(a, []int{1, 2}) || len(e) != 0 {
		t.Fatalf("Read(0,1)=%v,%v,%v,%v", d, a, e, err)
	}
	if st := r.State(); st.Prev != 0 || st.Window != (Window{1, 2}) || st.Mk != 2 || !st.HasMk {
		t.Fatalf("state=%+v", st)
	}

	d, a, e, _ = r.Read(1, 1)
	if len(d) != 0 || len(a) != 0 || len(e) != 0 {
		t.Fatalf("Read(1,1)=%v,%v,%v", d, a, e)
	}

	d, a, e, _ = r.Read(2, 1)
	if !reflect.DeepEqual(d, []int{}) || !reflect.DeepEqual(a, []int{3, 4, 5, 6}) || !reflect.DeepEqual(e, []int{0, 1, 2}) {
		t.Fatalf("Read(2,1)=%v,%v,%v", d, a, e)
	}
	if st := r.State(); st.Window != (Window{3, 4}) || st.Mk != 5 {
		t.Fatalf("state=%+v", st)
	}

	for _, p := range []int{3, 4} {
		if d, a, e, _ := r.Read(p, 1); len(d) != 0 || len(a) != 0 || len(e) != 0 {
			t.Fatalf("Read(%d)=%v,%v,%v", p, d, a, e)
		}
	}

	d, a, e, _ = r.Read(5, 1)
	if !reflect.DeepEqual(a, []int{7, 8, 9, 10, 11, 12, 13, 14}) ||
		!reflect.DeepEqual(e, []int{6, 3, 4, 5, 7, 8, 9, 10}) {
		t.Fatalf("Read(5,1)=%v,%v,%v", d, a, e)
	}
	st := r.State()
	if st.Prev != 5 || st.Window != (Window{7, 4}) || st.Mk != 11 || !st.HasMk {
		t.Fatalf("state=%+v", st)
	}
}
