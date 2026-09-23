package parse

import (
	"errors"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name  string
		line  string
		key   string
		val   int64
		isBad bool
	}{
		{"normal", "a=1", "a", 1, false},
		{"empty key legal", "=7", "", 7, false},
		{"zero value", "g=0", "g", 0, false},
		{"big value", "k=9223372036854775807", "k", 9223372036854775807, false},
		{"trailing newline", "a=2\n", "a", 2, false},
		{"missing separator", "garbage", "", 0, true},
		{"negative value", "a=-1", "", 0, true},
		{"non numeric value", "a=xx", "", 0, true},
		{"double separator", "a=1=2", "", 0, true},
		{"empty value", "a=", "", 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := Parse(3, []byte(tc.line))
			if tc.isBad {
				if !errors.Is(err, ErrBad) {
					t.Fatalf("err=%v want ErrBad", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err %v", err)
			}
			if r.Key != tc.key || r.Val != tc.val || r.Offset != 3 {
				t.Fatalf("record=%+v want key=%q val=%d", r, tc.key, tc.val)
			}
		})
	}
}
