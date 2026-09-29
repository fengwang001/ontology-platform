package addr_test

import (
	"testing"

	"ontology/addr"
)

func TestOf(t *testing.T) {
	cases := []struct {
		name string
		data []byte
	}{
		{"empty", []byte{}},
		{"short", []byte("hello")},
		{"long", make([]byte, 4096)},
	}
	for i := range cases {
		if i == 2 {
			for j := range cases[i].data {
				cases[i].data[j] = byte(j * 7)
			}
		}
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := addr.Of(tc.data)
			b := addr.Of(append([]byte{}, tc.data...))
			if !a.Equal(b) || a.IsZero() || len(a.String()) != 2*addr.Size {
				t.Fatal("stable address / encoding mismatch")
			}
		})
	}
	a, b := addr.Of([]byte("abc")), addr.Of([]byte("abd"))
	if a.Equal(b) {
		t.Fatal("distinct content must not share an address")
	}
	if addr.Of(nil).Equal(addr.Of([]byte{0})) {
		t.Fatal("empty and one-byte content must differ")
	}
}
