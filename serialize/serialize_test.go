package serialize

import (
	"bytes"
	"errors"
	"path/filepath"
	"testing"

	"ontology/conflict"
	"ontology/doc"
)

func sample() (doc.Set, []conflict.Conflict) {
	m := doc.Set{
		"R1": {"a": 1, "b": "x", "": "empty-name"},
		"R2": {"f": 1.5, "g": true, "h": int64(7)},
	}
	cs := []conflict.Conflict{
		conflict.New("R1", "a", conflict.FieldValue, 1, 2),
		conflict.New("R2", "", conflict.DeleteModify, nil, doc.Record{"f": 9}),
	}
	return m, cs
}

func TestRoundTrip(t *testing.T) {
	m, cs := sample()
	gotM, gotC, err := Decode(Encode(m, cs))
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if !doc.Equal(m["R1"], gotM["R1"]) || !doc.Equal(m["R2"], gotM["R2"]) {
		t.Fatalf("merged mismatch: %v", gotM)
	}
	if len(gotC) != len(cs) {
		t.Fatalf("conflicts = %v, want %d", gotC, len(cs))
	}
	for i, c := range cs {
		if gotC[i].Kind != c.Kind || gotC[i].Key != c.Key || gotC[i].Field != c.Field {
			t.Fatalf("conflict[%d] = %+v, want %+v", i, gotC[i], c)
		}
	}
	// 落盘读回
	p := filepath.Join(t.TempDir(), "merge.out")
	if err := WriteFile(p, m, cs); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	rm, rc, err := ReadFile(p)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !doc.Equal(rm["R1"], m["R1"]) || len(rc) != len(cs) {
		t.Fatalf("file round trip mismatch")
	}
}

// TestTruncation 逐截断点（1 到 len-1）验证分类：
// [1,8) 头部不完整；[8, 8+plen) 记录不完整；[8+plen, len) CRC 不匹配。
func TestTruncation(t *testing.T) {
	m, cs := sample()
	full := Encode(m, cs)
	plen := len(full) - headerLen - 4
	counts := map[error]int{}
	for cut := 1; cut < len(full); cut++ {
		_, _, err := Decode(full[:cut])
		var want error
		switch {
		case cut < headerLen:
			want = ErrHeaderIncomplete
		case cut < headerLen+plen:
			want = ErrRecordIncomplete
		default:
			want = ErrCRCMismatch
		}
		if !errors.Is(err, want) {
			t.Fatalf("cut=%d: err = %v, want %v", cut, err, want)
		}
		counts[want]++
	}
	for _, e := range []error{ErrHeaderIncomplete, ErrRecordIncomplete, ErrCRCMismatch} {
		if counts[e] == 0 {
			t.Fatalf("class %v never produced", e)
		}
	}
	t.Logf("header=%d record=%d crc=%d",
		counts[ErrHeaderIncomplete], counts[ErrRecordIncomplete], counts[ErrCRCMismatch])
}

// TestCorruption 全文长度不变、逐字节翻转 payload，必为 CRC 不匹配。
func TestCorruption(t *testing.T) {
	m, cs := sample()
	full := Encode(m, cs)
	plen := len(full) - headerLen - 4
	for i := 0; i < plen; i++ {
		bad := bytes.Clone(full)
		bad[headerLen+i] ^= 0xFF
		if _, _, err := Decode(bad); !errors.Is(err, ErrCRCMismatch) {
			t.Fatalf("flip payload byte %d: err = %v, want ErrCRCMismatch", i, err)
		}
	}
	// 三类错误互相可区分
	if errors.Is(ErrHeaderIncomplete, ErrRecordIncomplete) ||
		errors.Is(ErrRecordIncomplete, ErrCRCMismatch) ||
		errors.Is(ErrCRCMismatch, ErrHeaderIncomplete) {
		t.Fatalf("error classes must be distinct")
	}
}
