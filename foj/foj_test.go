package foj

import (
	"strconv"
	"testing"
)

// TestCheckedKeysConstant proves Apply locates state by key hash: the number
// of keys examined per Apply stays bounded by a small constant independent
// of the total number of keys m.
func TestCheckedKeysConstant(t *testing.T) {
	for _, m := range []int{100, 1000, 10000} {
		t.Run(strconv.Itoa(m), func(t *testing.T) {
			e := New()
			for i := 0; i < m; i++ {
				if _, err := e.Apply(Op{Kind: PutLeft, Key: "k" + strconv.Itoa(i), ID: "l"}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := e.Apply(Op{Kind: PutRight, Key: "k0", ID: "r"}); err != nil {
				t.Fatal(err)
			}
			if e.checked > 2 {
				t.Fatalf("m=%d: checked %d keys, want <= 2", m, e.checked)
			}
		})
	}
}
