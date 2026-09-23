package sizeline

import (
	"bytes"
	"strings"
	"testing"
)

func TestEncodeAndParseRoundTrip(t *testing.T) {
	cases := []struct {
		name string
		size int
		exts []Ext
	}{
		{"no ext", 8, nil},
		{"single", 16, []Ext{{"name", "abc"}}},
		{"semicolon", 4, []Ext{{"a", "x;y"}}},
		{"equals", 4, []Ext{{"a", "x=y"}}},
		{"quote", 4, []Ext{{"a", `x"y`}}},
		{"backslash", 4, []Ext{{"a", `x\y`}}},
		{"crlf", 4, []Ext{{"a", "x\r\ny"}}},
		{"tricky mix", 255, []Ext{
			{"a", ";="},
			{"b", `q"b\`},
			{"c", "plain"},
		}},
		{"end marker", 0, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			line := Encode(tc.size, tc.exts)
			if !bytes.HasSuffix(line, []byte{'\r', '\n'}) {
				t.Fatalf("line missing CRLF: %q", line)
			}
			size, got, err := ParseLine(line[:len(line)-2])
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			if size != tc.size || len(got) != len(tc.exts) {
				t.Fatalf("size=%d exts=%v, want %d %v", size, got, tc.size, tc.exts)
			}
			for i := range tc.exts {
				if got[i] != tc.exts[i] {
					t.Fatalf("ext %d = %q=%q, want %q=%q",
						i, got[i].Key, got[i].Value, tc.exts[i].Key, tc.exts[i].Value)
				}
			}
		})
	}
}

func TestIndependentParser(t *testing.T) {
	// 独立最小解析器：不调用 ParseLine，直接按字节扫描，验证转义后的字节
	// 不改变块大小行的结构（引号内的 ; = 不被当成分隔符）。
	exts := []Ext{{"a", "x;y"}, {"b", "p=q"}, {"c", `s"t\u\r\n`}}
	line := Encode(10, exts)
	line = bytes.TrimSuffix(line, []byte{'\r', '\n'})
	size, got, err := indieParse(line)
	if err != nil {
		t.Fatal(err)
	}
	if size != 10 || len(got) != 3 {
		t.Fatalf("size=%d got=%v", size, got)
	}
	for i := range exts {
		if got[i] != exts[i] {
			t.Fatalf("ext %d mismatch: %q=%q", i, got[i].Key, got[i].Value)
		}
	}
}

func TestParseErrors(t *testing.T) {
	cases := [][]byte{
		nil,
		[]byte(";a=b"),
		[]byte("g"),  // 非法十六进制
		[]byte("8;a"), // 缺 = 与值
		[]byte("8;a="), // 空值合法；改用下一个
	}
	cases[4] = []byte(`8;a="unterminated`)
	for i, line := range cases {
		if _, _, err := ParseLine(line); err == nil {
			t.Errorf("case %d (%q): want error", i, line)
		}
	}
}

func TestEncodedExtLen(t *testing.T) {
	line := Encode(7, []Ext{{"k", "v"}})
	want := len(line) - len("7") - 2 // 去掉长度与 CRLF
	if got := EncodedExtLen([]Ext{{"k", "v"}}); got != want {
		t.Fatalf("EncodedExtLen=%d want %d", got, want)
	}
	// 转义字符也要计入：一个 ; 触发加引号，长度 = 2 引号 + 3 字符。
	if got := EncodedExtLen([]Ext{{"k", "a;b"}}); got != 1+1+1+5 {
		t.Fatalf("quoted ext len=%d", got)
	}
}

// indieParse 是测试内独立实现的最小大小行解析器。
func indieParse(line []byte) (int, []Ext, error) {
	i := bytes.IndexByte(line, ';')
	var hex []byte
	if i < 0 {
		hex = line
	} else {
		hex = line[:i]
	}
	size := 0
	for _, c := range hex {
		c -= '0'
		if c > 9 {
			c = c - ('a' - '0' - 10)
		}
		if c > 15 {
			return 0, nil, ErrBadSize
		}
		size = size*16 + int(c)
	}
	var out []Ext
	for i >= 0 && i < len(line) {
		i++ // 跳过 ;
		eq := bytes.IndexByte(line[i:], '=')
		if eq < 0 {
			return 0, nil, ErrBadExt
		}
		key := string(line[i : i+eq])
		i += eq + 1
		var val string
		if i < len(line) && line[i] == '"' {
			i++
			var b strings.Builder
			for i < len(line) {
				if line[i] == '"' {
					i++
					break
				}
				if line[i] == '\\' && i+1 < len(line) {
					i++
					switch line[i] {
					case 'r':
						b.WriteByte('\r')
					case 'n':
						b.WriteByte('\n')
					default:
						b.WriteByte(line[i])
					}
				} else {
					b.WriteByte(line[i])
				}
				i++
			}
			val = b.String()
		} else {
			j := bytes.IndexByte(line[i:], ';')
			if j < 0 {
				val = string(line[i:])
				i = len(line)
			} else {
				val = string(line[i : i+j])
				i += j
			}
		}
		out = append(out, Ext{Key: key, Value: val})
		if i < len(line) && line[i] != ';' {
			return 0, nil, ErrBadExt
		}
	}
	return size, out, nil
}
