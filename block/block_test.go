package block

import (
	"encoding/binary"
	"errors"
	"fmt"
	"slices"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	sets := []struct {
		name    string
		entries []string
	}{
		{"empty", nil},
		{"single", []string{"only"}},
		{"empty-first", []string{"", "a", "ab"}},
		{"same-prefix", []string{"prefix-a", "prefix-b", "prefix-c", "prefix-d"}},
		{"no-common", []string{"apple", "banana", "cherry", "date"}},
		{"utf8", []string{"café", "cafés", "naïve", "日本", "日本語"}},
	}
	ks := []int{1, 4, 16, 64, 1000}
	for _, tc := range sets {
		for _, k := range ks {
			data, err := Encode(tc.entries, k)
			if err != nil {
				t.Fatalf("%s K=%d encode: %v", tc.name, k, err)
			}
			got, err := Decode(data)
			if err != nil {
				t.Fatalf("%s K=%d decode: %v", tc.name, k, err)
			}
			if !slices.Equal(got, tc.entries) {
				t.Fatalf("%s K=%d roundtrip = %v, want %v", tc.name, k, got, tc.entries)
			}
			for i, want := range tc.entries {
				s, n, err := DecodeEntry(data, i)
				if err != nil || s != want {
					t.Fatalf("%s K=%d entry %d = %q,%v want %q", tc.name, k, i, s, err, want)
				}
				if maxN := min(k, len(tc.entries)); n > maxN {
					t.Fatalf("%s K=%d entry %d decoded %d > %d", tc.name, k, i, n, maxN)
				}
			}
		}
	}
}

func TestEncodeRejectsUnsorted(t *testing.T) {
	cases := [][]string{
		{"a", "a"},
		{"ab", "a"},
		{"b", "a", "c"},
	}
	for _, entries := range cases {
		if _, err := Encode(entries, 4); !errors.Is(err, ErrUnsorted) {
			t.Fatalf("Encode(%v) err = %v, want ErrUnsorted", entries, err)
		}
	}
}

func TestCommonPrefixByBytes(t *testing.T) {
	cases := []struct {
		a, b          string
		bytes, runesN int
	}{
		{"café", "cafés", 5, 4},
		{"café", "café", 5, 4},
		{"apple", "apricot", 2, 2},
		{"", "abc", 0, 0},
	}
	for _, tc := range cases {
		if got := CommonPrefix(tc.a, tc.b); got != tc.bytes {
			t.Fatalf("CommonPrefix(%q,%q) = %d bytes, want %d (码点口径为 %d)",
				tc.a, tc.b, got, tc.bytes, tc.runesN)
		}
	}
}

func TestTruncation(t *testing.T) {
	entries := make([]string, 1000)
	for i := range entries {
		entries[i] = fmt.Sprintf("key-%06d", i)
	}
	data, err := Encode(entries, 16)
	if err != nil {
		t.Fatal(err)
	}
	_, _, table, entryPos, err := parseHeader(data)
	if err != nil {
		t.Fatal(err)
	}
	tableEnd := entryPos
	crcStart := len(data) - 4
	classRange := map[string][2]int{}
	recovered := map[string][2]int{}
	record := func(class string, cut, n int) {
		if r, ok := classRange[class]; !ok {
			classRange[class], recovered[class] = [2]int{cut, cut}, [2]int{n, n}
		} else {
			classRange[class] = [2]int{r[0], cut}
			recovered[class] = [2]int{min(recovered[class][0], n), max(recovered[class][1], n)}
		}
	}
	for cut := 1; cut < len(data); cut++ {
		got, derr := Decode(data[:cut])
		var class string
		switch {
		case errors.Is(derr, ErrHeaderIncomplete):
			class = "header"
		case errors.Is(derr, ErrRestartTable):
			class = "restart-table"
		case errors.Is(derr, ErrEntryIncomplete):
			class = "entry"
		case errors.Is(derr, ErrCRC):
			class = "crc"
		default:
			t.Fatalf("cut=%d unexpected err %v", cut, derr)
		}
		want := "entry"
		switch {
		case cut < headerSize:
			want = "header"
		case cut < tableEnd:
			want = "restart-table"
		case cut >= crcStart:
			want = "crc"
		}
		if class != want {
			t.Fatalf("cut=%d class=%s want %s", cut, class, want)
		}
		if !slices.IsSorted(got) {
			t.Fatalf("cut=%d recovered entries not strictly sorted", cut)
		}
		record(class, cut, len(got))
	}
	_ = table
	for _, c := range []string{"header", "restart-table", "entry", "crc"} {
		r, n := classRange[c], recovered[c]
		t.Logf("class=%s cuts=[%d,%d] recovered=[%d,%d]", c, r[0], r[1], n[0], n[1])
	}
}

func TestPrefixLenLie(t *testing.T) {
	data, err := Encode([]string{"apple", "apricot", "banana"}, 4)
	if err != nil {
		t.Fatal(err)
	}
	pos := headerSize + 4
	_, n := binary.Uvarint(data[pos:])
	pos += n
	slen, n := binary.Uvarint(data[pos:])
	pos += n + int(slen)
	data[pos] = 0x7f // sharedLen=127 > len("apple")
	if _, err := Decode(data); !errors.Is(err, ErrPrefixLen) {
		t.Fatalf("Decode err = %v, want ErrPrefixLen", err)
	}
	if _, _, err := DecodeEntry(data, 1); !errors.Is(err, ErrPrefixLen) {
		t.Fatalf("DecodeEntry err = %v, want ErrPrefixLen", err)
	}
}

func TestRestartOffsetFallback(t *testing.T) {
	entries := make([]string, 32)
	for i := range entries {
		entries[i] = fmt.Sprintf("e%03d", i)
	}
	data, err := Encode(entries, 4)
	if err != nil {
		t.Fatal(err)
	}
	binary.LittleEndian.PutUint32(data[headerSize+4:], // restart[1] 指向条目中间
		binary.LittleEndian.Uint32(data[headerSize+4:])+1)
	s, n, err := DecodeEntry(data, 5)
	if !errors.Is(err, ErrRestartOffset) || s != "e005" || n != 6 {
		t.Fatalf("DecodeEntry = %q,%d,%v want e005,6,ErrRestartOffset", s, n, err)
	}
	got, err := Decode(data)
	if !errors.Is(err, ErrRestartOffset) || !slices.Equal(got, entries) {
		t.Fatalf("Decode = %v,%v want full entries + ErrRestartOffset", got, err)
	}
}
