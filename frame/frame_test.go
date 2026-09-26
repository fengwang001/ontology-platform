package frame

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"

	"ontology/lenp"
)

func naiveEncode(fields [][]byte) []byte { // 教科书参照：4 字节小端长度 + 负载
	var buf []byte
	for _, f := range fields {
		buf = append(buf, lenp.PutLength(len(f))...)
		buf = append(buf, f...)
	}
	return buf
}

func cases() [][][]byte { // 固定向量 + 循环生成随机向量（含空字段）
	c := [][][]byte{
		{[]byte("hi"), {}, []byte("world!"), []byte("A")},
		{},
		{{}},
		{[]byte("a"), {}},
	}
	rng := rand.New(rand.NewSource(1))
	for i := 0; i < 20; i++ {
		fs := make([][]byte, rng.Intn(8))
		for j := range fs {
			f := make([]byte, rng.Intn(300))
			rng.Read(f)
			fs[j] = f
		}
		c = append(c, fs)
	}
	return c
}

func equalLists(t *testing.T, got, want [][]byte) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("field count %d, want %d", len(got), len(want))
		return
	}
	for i := range want {
		if !bytes.Equal(got[i], want[i]) {
			t.Errorf("field %d mismatch", i)
		}
	}
}

func TestRoundTrip(t *testing.T) { // 不变量 1：往返一致（含空字段）
	for _, fs := range cases() {
		got, err := Decode(Encode(fs))
		if err != nil {
			t.Fatal(err)
		}
		equalLists(t, got, fs)
	}
}

func TestPrefixSelfConsistent(t *testing.T) { // 不变量 2：前缀==负载长，无重叠无空洞
	for _, fs := range cases() {
		rec := Encode(fs)
		r := NewReader(rec)
		for r.Pos() < len(rec) {
			l, err := lenp.GetLength(rec[r.Pos():])
			f, err2 := r.NextField()
			if err != nil || err2 != nil || len(f) != l {
				t.Fatalf("prefix %d payload %d err %v/%v", l, len(f), err, err2)
			}
		}
		if r.Pos() != len(rec) {
			t.Fatalf("pos %d, buf %d", r.Pos(), len(rec))
		}
	}
}

func TestMatchesNaive(t *testing.T) { // 不变量 3：与朴素参照字节级一致
	for _, fs := range cases() {
		if !bytes.Equal(Encode(fs), naiveEncode(fs)) {
			t.Fatalf("Encode differs from naive, %d fields", len(fs))
		}
	}
}

func TestFailureAtomic(t *testing.T) { // 不变量 4 + 故障注入
	rec := Encode([][]byte{[]byte("hi"), {}, []byte("world!")})
	for _, cut := range []int{1, 2, 5} { // 截断：整体失败，无部分字段
		if got, err := Decode(rec[:len(rec)-cut]); got != nil || err == nil {
			t.Fatalf("cut %d: got %v, err %v", cut, got, err)
		}
	}
	for _, n := range []int{1, 2, 3} { // 非法前缀：不足 4 字节
		if got, err := Decode(rec[:n]); got != nil || !errors.Is(err, lenp.ErrShortPrefix) {
			t.Fatalf("n %d: got %v, err %v", n, got, err)
		}
	}
	if _, err := Decode(rec[:len(rec)-1]); !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
	r := NewReader(rec[:len(rec)-1]) // 跳过前两字段后，第 3 字段已截断
	if err := r.SkipField(); err != nil {
		t.Fatal(err)
	}
	if err := r.SkipField(); err != nil {
		t.Fatal(err)
	}
	pos := r.Pos()
	if err := r.SkipField(); err == nil || r.Pos() != pos { // 失败不推进游标
		t.Fatalf("failed skip advanced to %d", r.Pos())
	}
	if _, err := r.NextField(); err == nil || r.Pos() != pos { // 失败后游标仍可用
		t.Fatalf("failed next advanced to %d", r.Pos())
	}
	r2 := NewReader(rec) // 正常记录连续跳过全部字段
	for i := 0; i < 3; i++ {
		if err := r2.SkipField(); err != nil {
			t.Fatal(err)
		}
	}
	if r2.Pos() != len(rec) {
		t.Fatalf("pos %d, want %d", r2.Pos(), len(rec))
	}
}

func TestSkipZeroPayloadTouch(t *testing.T) { // 跳过 O(1)：负载触碰恒为 0
	for _, m := range []int{100, 1000, 10000} {
		fs := make([][]byte, m)
		for i := range fs {
			fs[i] = make([]byte, 2048+i%2048)
		}
		rec := Encode(fs)
		r := NewReader(rec)
		total := 0
		for i := 0; i < m; i++ {
			if err := r.SkipField(); err != nil {
				t.Fatal(err)
			}
			total += r.lastSkipPayload
		}
		if total != 0 || r.Pos() != len(rec) {
			t.Fatalf("m=%d touched %d bytes, pos %d/%d", m, total, r.Pos(), len(rec))
		}
	}
}
