package tree

import (
	"errors"
	"reflect"
	"strconv"
	"testing"

	"ontology/seg"
)

// TestLoadSegment 表驱动：截断恢复、首条不完整、非法 tag、长度溢出。
func TestLoadSegment(t *testing.T) {
	mk := seg.Segment{ID: 1, Recs: []seg.Record{{Key: "a", Val: "1"}, {Key: "b", Val: "22"}, {Key: "c", Del: true}}}
	enc := seg.Encode(mk)
	full, drop, err := seg.LoadSegment(enc)
	if err != nil || drop != 0 || !reflect.DeepEqual(full.Recs, mk.Recs) {
		t.Fatalf("roundtrip: recs=%v drop=%d err=%v", full.Recs, drop, err)
	}
	overflow := []byte{0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0x01}
	midBadTag := append([]byte{1, 'a', 0, 0}, 1, 'b', 9)
	cases := []struct {
		name             string
		in               []byte
		wantRecs, wantDr int
		wantErr          error
	}{
		{"空输入", nil, 0, 0, nil},
		{"截断1字节", enc[:len(enc)-1], 2, 1, nil},
		{"截断到key中", enc[:7], 1, 1, nil},
		{"首条不完整", enc[:1], 0, 0, seg.ErrCorrupt},
		{"非法tag", []byte{1, 'a', 9, 0, 0}, 0, 0, seg.ErrCorrupt},
		{"中间非法tag", midBadTag, 0, 0, seg.ErrCorrupt},
		{"长度溢出", overflow, 0, 0, seg.ErrCorrupt},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, dr, err := seg.LoadSegment(c.in)
			if !errors.Is(err, c.wantErr) || len(s.Recs) != c.wantRecs || dr != c.wantDr {
				t.Fatalf("got recs=%d drop=%d err=%v", len(s.Recs), dr, err)
			}
		})
	}
}

// TestMergeScanBounded 决定合并候选的扫描段数不随层内段数 m 线性增长。
func TestMergeScanBounded(t *testing.T) {
	for _, m := range []int{100, 1000, 5000, 9999} {
		tr, err := New(1, 10000, 1) // fanout 足够大，不触发合并
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < m; i++ {
			_ = tr.Put(strconv.Itoa(i), "v")
		}
		if tr.scanned != 0 { // 参与合并段数 0 + 小常数
			t.Fatalf("m=%d: scanned=%d，随 m 线性增长", m, tr.scanned)
		}
	}
	tr, _ := New(1, 2, 1)
	for i := 0; i < 3; i++ { // 第 3 次写触发 flush 且 L0 满：合并 2 段
		_ = tr.Put("k", "v")
	}
	if tr.scanned != 2 {
		t.Fatalf("scanned=%d, want 2（恰为参与合并段数）", tr.scanned)
	}
	tr2, _ := New(1, 2, 2)
	for i := 0; i < 5; i++ { // 第 5 次写触发级联：L0 合并 2 段 + L1 合并 2 段
		_ = tr2.Put("k", "v")
	}
	if tr2.scanned != 4 {
		t.Fatalf("级联 scanned=%d, want 4", tr2.scanned)
	}
}

// TestTombstoneRule 墓碑仅当全系统无该键 Put 记录时才丢弃。
func TestTombstoneRule(t *testing.T) {
	tr, _ := New(1, 2, 1) // s3 有更老的 b:2，−b 必须保留，否则 b:2 复活
	_ = tr.Put("b", "2")
	_ = tr.Put("c", "3")
	_ = tr.Del("b")
	_ = tr.Put("d", "4")
	_ = tr.Put("e", "5")
	segs := tr.Segments()
	if len(segs) != 1 {
		t.Fatalf("segments=%d, want 1", len(segs))
	}
	if r, ok := segs[0].Latest("b"); !ok || !r.Del {
		t.Fatalf("墓碑被过早丢弃: %v %v", r, ok)
	}
	if v, ok := tr.Get("b"); ok {
		t.Fatalf("Get(b)=%q, want 不存在", v)
	}
	tr2, _ := New(1, 2, 1) // z 无任何 Put 记录，墓碑必须丢弃
	_ = tr2.Del("z")
	_ = tr2.Put("y", "1")
	_ = tr2.Put("x", "1")
	segs2 := tr2.Segments()
	if len(segs2) != 1 || len(segs2[0].Recs) != 1 {
		t.Fatalf("segs=%v", segs2)
	}
	if _, ok := segs2[0].Latest("z"); ok {
		t.Fatal("无更早 Put 的墓碑应被丢弃")
	}
}

// TestStructureInvariant 随机操作序列中每步结构不变量成立。
func TestStructureInvariant(t *testing.T) {
	tr, _ := New(2, 2, 2)
	s := uint64(9)
	rnd := func() uint64 { s = s*6364136223846793005 + 1442695040888963407; return s >> 33 }
	for i := 0; i < 2000; i++ {
		k := strconv.Itoa(int(rnd() % 20))
		if rnd()%4 == 0 {
			_ = tr.Del(k)
		} else {
			_ = tr.Put(k, "v")
		}
		if err := tr.CheckStructure(); err != nil {
			t.Fatalf("step %d: %v", i, err)
		}
	}
}
