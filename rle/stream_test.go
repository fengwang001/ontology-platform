package rle

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"ontology/runs"
)

func TestSplits(t *testing.T) {
	inputs := []string{
		"2a3a", "12aé\\1中", `3\\`, "a", "2a", "10\U0001F600x",
		"1a", "0a", "01a", "2a3", `\`, `\a`, "é2é", "a\xc3",
	}
	for _, in := range inputs {
		want, wantErr := Decode(in)
		for i := 0; i <= len(in); i++ { // 遍历所有切分点
			d := &Decoder{}
			got, err := func() (string, error) {
				if _, err := d.Write([]byte(in[:i])); err != nil {
					return "", err
				}
				if _, err := d.Write([]byte(in[i:])); err != nil {
					return "", err
				}
				return d.Close()
			}()
			if got != want || !sameErr(err, wantErr) {
				t.Errorf("%q 切分点 %d: got %q,%v; want %q,%v", in, i, got, err, want, wantErr)
			}
		}
		got, err := decodeBytes(in) // 1 字节一段
		if got != want || !sameErr(err, wantErr) {
			t.Errorf("%q 按字节喂入: got %q,%v; want %q,%v", in, got, err, want, wantErr)
		}
	}
}

func TestBigCount(t *testing.T) {
	cases := []struct {
		in   string
		max  int64
		want string
		sent error
	}{
		{"5a", 0, "aaaaa", nil},
		{"5a", 5, "aaaaa", nil},
		{"5a", 4, "", ErrTooLong},
		{"3é", 6, "ééé", nil},
		{"3é", 5, "", ErrTooLong},
		{"1000000a", 1 << 20, strings.Repeat("a", 1000000), nil},
		{"99999999999999999999a", 0, "", ErrTooLong}, // 次数超 int64：不溢出、不巨额分配
		{"99999999999999999999a", 1 << 60, "", ErrTooLong},
	}
	for _, c := range cases {
		d := &Decoder{MaxOutput: c.max}
		var got string
		err := error(nil)
		for _, b := range []byte(c.in) {
			if _, err = d.Write([]byte{b}); err != nil {
				break
			}
		}
		if err == nil {
			got, err = d.Close()
		}
		if !errors.Is(err, c.sent) || (err == nil && got != c.want) {
			t.Errorf("Decode(%q, max=%d) = %d字节, %v; want %v", c.in, c.max, len(got), err, c.sent)
		}
	}
}

func TestInspected(t *testing.T) {
	in := strings.Repeat("2a2bé\\1", 1<<17) // 恰好 1 MiB 合法编码
	inspected = 0
	got, err := decodeBytes(in)
	if err != nil {
		t.Fatal(err)
	}
	if inspected != int64(len(in)) {
		t.Errorf("inspected = %d, want %d", inspected, len(in))
	}
	if len(got) != 7<<17 {
		t.Errorf("decoded %d bytes, want %d", len(got), 7<<17)
	}
}

func TestRuns(t *testing.T) {
	type run struct {
		s rune
		n int
	}
	cases := []struct {
		in   string
		want []run
	}{
		{"", nil},
		{"a", []run{{'a', 1}}},
		{"aaabb", []run{{'a', 3}, {'b', 2}}},
		{"é中é", []run{{'é', 1}, {'中', 1}, {'é', 1}}},
	}
	for _, c := range cases {
		var got []run
		runs.Split(c.in, func(s rune, n int) { got = append(got, run{s, n}) })
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("Split(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	if n := runs.ParseCount("99999999999999999999"); n.String() != "99999999999999999999" {
		t.Errorf("ParseCount 溢出: %s", n)
	}
	if got := string(runs.AppendCount(nil, 12)); got != "12" {
		t.Errorf("AppendCount = %q", got)
	}
}
