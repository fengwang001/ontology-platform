package serialize

import (
	"errors"
	"path/filepath"
	"testing"

	"ontology/conflict"
	"ontology/doc"
)

func sample() (doc.Set, conflict.Report) {
	s := doc.Set{
		"k1": {"a": doc.Str("x"), "n": doc.Num(2.5)},
		"":   {"": doc.Str("")},
	}
	rep := conflict.Report{List: []conflict.Conflict{
		{Kind: conflict.FieldValue, Key: "k1", Field: "a",
			Left: doc.Str("x"), LeftOK: true, Right: doc.Num(1), RightOK: true},
		{Kind: conflict.DeleteVsModify, Key: "k2", RightOK: true},
	}}
	return s, rep
}

func TestRoundtrip(t *testing.T) {
	s, rep := sample()
	gotS, gotRep, err := Unmarshal(Marshal(s, rep))
	if err != nil {
		t.Fatal(err)
	}
	if len(gotS) != len(s) || !doc.RecordEqual(gotS["k1"], s["k1"]) || !doc.RecordEqual(gotS[""], s[""]) {
		t.Errorf("集合往返不一致: %v", gotS)
	}
	if len(gotRep.List) != 2 || gotRep.List[0] != rep.List[0] || gotRep.List[1] != rep.List[1] {
		t.Errorf("报告往返不一致: %v", gotRep.List)
	}
}

func TestFileRoundtrip(t *testing.T) {
	s, rep := sample()
	p := filepath.Join(t.TempDir(), "merge.ont3")
	if err := WriteFile(p, s, rep); err != nil {
		t.Fatal(err)
	}
	gotS, gotRep, err := ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !doc.RecordEqual(gotS["k1"], s["k1"]) || len(gotRep.List) != 2 {
		t.Errorf("落盘读回不一致")
	}
}

func TestTruncation(t *testing.T) {
	s, rep := sample()
	full := Marshal(s, rep)
	counts := map[error]int{}
	for i := 1; i < len(full); i++ {
		_, _, err := Unmarshal(full[:i])
		switch {
		case errors.Is(err, ErrHeaderIncomplete):
			counts[ErrHeaderIncomplete]++
		case errors.Is(err, ErrRecordIncomplete):
			counts[ErrRecordIncomplete]++
		case errors.Is(err, ErrCRCMismatch):
			counts[ErrCRCMismatch]++
		default:
			t.Fatalf("截断点 %d 产生未分类错误: %v", i, err)
		}
	}
	for _, e := range []error{ErrHeaderIncomplete, ErrRecordIncomplete, ErrCRCMismatch} {
		if counts[e] == 0 {
			t.Errorf("分类 %v 没有覆盖任何截断点", e)
		}
	}
	t.Logf("截断分类计数: %v", counts)
}

func TestCorruption(t *testing.T) {
	s, rep := sample()
	full := Marshal(s, rep)
	full[len(full)-1] ^= 0xFF // 破坏 CRC 本身
	if _, _, err := Unmarshal(full); !errors.Is(err, ErrCRCMismatch) {
		t.Errorf("CRC 字节损坏: %v", err)
	}
	full = Marshal(s, rep)
	full[headerLen] ^= 0xFF // 破坏负载首字节
	if _, _, err := Unmarshal(full); !errors.Is(err, ErrCRCMismatch) {
		t.Errorf("负载损坏: %v", err)
	}
}
