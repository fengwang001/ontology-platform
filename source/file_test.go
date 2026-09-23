package source

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestParseFile(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    []Entry
		wantErr error
	}{
		{"empty", "", nil, nil},
		{"comments and blanks", "# hi\n\na = 1\n", []Entry{{Key: "a", Value: "1", Layer: LayerFile}}, nil},
		{"empty value legal", "a =\n", []Entry{{Key: "a", Value: "", Layer: LayerFile}}, nil},
		{"value keeps equals", "a = b=c\n", []Entry{{Key: "a", Value: "b=c", Layer: LayerFile}}, nil},
		{"bad key", "a..b = 1\n", nil, ErrEmptySegment},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			entries, err := ParseFile([]byte(c.content))
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if c.wantErr != nil {
				return
			}
			if len(entries) != len(c.want) {
				t.Fatalf("got %v, want %v", entries, c.want)
			}
			for i, e := range entries {
				if e != c.want[i] {
					t.Fatalf("entry %d = %+v, want %+v", i, e, c.want[i])
				}
			}
		})
	}
}

// TestParseFileTruncation 逐字节截断单行文件，每个截断点都要被分类。
func TestParseFileTruncation(t *testing.T) {
	content := "greeting = hello ${name}\n"
	eq := strings.Index(content, "=") // 9
	for n := 1; n < len(content); n++ {
		_, err := ParseFile([]byte(content[:n]))
		if err == nil {
			t.Fatalf("truncate to %d: want error, got nil", n)
		}
		want := ErrLineIncomplete
		switch {
		case n <= eq: // 截断点在 '=' 之前（含两侧空白）
			want = ErrKeyIncomplete
		case strings.TrimSpace(content[:n]) == strings.TrimSpace(content[:eq+1]):
			want = ErrValueIncomplete // '=' 后还没有值字节
		}
		if !errors.Is(err, want) {
			t.Fatalf("truncate to %d: err = %v, want %v", n, err, want)
		}
		if !strings.Contains(err.Error(), "line 1") {
			t.Fatalf("truncate to %d: err %v missing line number", n, err)
		}
	}
}

// TestParseFileTruncationLineNo 验证多行文件截断时报出的行号。
func TestParseFileTruncationLineNo(t *testing.T) {
	cases := []struct {
		content string
		n       int
		wantErr error
		line    int
	}{
		{"a = 1\nb = 2\n", 7, ErrKeyIncomplete, 2},   // "b"
		{"a = 1\nb = 2\n", 11, ErrLineIncomplete, 2}, // "b = 2"
		{"a = 1\nb =\n", 9, ErrValueIncomplete, 2},   // "b ="
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("n=%d", c.n), func(t *testing.T) {
			_, err := ParseFile([]byte(c.content[:c.n]))
			if !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if !strings.Contains(err.Error(), fmt.Sprintf("line %d", c.line)) {
				t.Fatalf("err %v missing line %d", err, c.line)
			}
		})
	}
}
