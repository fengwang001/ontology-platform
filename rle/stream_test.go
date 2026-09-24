package rle

import (
	"fmt"
	"strings"
	"testing"
)

func feed(in string, cut int) (string, error) {
	d := NewDecoder(1 << 30)
	d.Write([]byte(in[:cut]))
	d.Write([]byte(in[cut:]))
	return d.Close()
}

func feedBytes(in string) (string, error) {
	d := NewDecoder(1 << 30)
	for i := 0; i < len(in); i++ {
		d.Write([]byte{in[i]})
	}
	return d.Close()
}

func TestAllSplits(t *testing.T) {
	inputs := []string{
		"", "3ab", `a\1`, `3\\`, "12a", "2a3a", "aa", `2\`, "2", "\xff",
		"10é中", "1a", "0a", "01a", `\q`, "99999999999999999999a",
		Encode("e\u0301éé\\22"),
	}
	for _, in := range inputs {
		want, wantErr := Decode(in)
		for i := 0; i <= len(in); i++ {
			got, err := feed(in, i)
			if got != want || fmt.Sprint(err) != fmt.Sprint(wantErr) {
				t.Errorf("split %q at %d: (%q, %v), want (%q, %v)",
					in, i, got, err, want, wantErr)
			}
		}
		got, err := feedBytes(in)
		if got != want || fmt.Sprint(err) != fmt.Sprint(wantErr) {
			t.Errorf("1-byte feed %q: (%q, %v), want (%q, %v)", in, got, err, want, wantErr)
		}
	}
}

func TestCheckedCounter(t *testing.T) {
	in := strings.Repeat(`3ab\1`, 1<<20/5)
	d := NewDecoder(1 << 30)
	for i := 0; i < len(in); i++ {
		if _, err := d.Write([]byte{in[i]}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := d.Close(); err != nil {
		t.Fatal(err)
	}
	if d.checked != int64(len(in)) {
		t.Errorf("checked = %d, want %d", d.checked, len(in))
	}
}
