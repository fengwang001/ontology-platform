package bits

import "testing"

func TestReaderCounter(t *testing.T) {
	// 字节 0xA3 = 10100011：白盒核验非导出计数器随读取自增、随打点重置。
	r := NewReader([]byte{0xA3})
	r.StartCode()
	for i := uint64(1); i <= 5; i++ {
		if _, err := r.ReadBit(); err != nil {
			t.Fatal(err)
		}
		if r.lastCodeBits != i {
			t.Fatalf("读 %d 位后计数器=%d, 期望 %d", i, r.lastCodeBits, i)
		}
	}
	r.StartCode()
	if r.lastCodeBits != 0 {
		t.Fatalf("打点后计数器应归零, 实际 %d", r.lastCodeBits)
	}
	if _, err := r.ReadBit(); err != nil || r.lastCodeBits != 1 {
		t.Fatal("打点后计数器未重新计数")
	}
}

func TestWriterRoundTrip(t *testing.T) {
	cases := []struct {
		v uint64
		n uint
		b byte
	}{
		{1, 1, 0x80},
		{0b0100, 4, 0x40},
		{0, 8, 0x00},
		{0xFF, 8, 0xFF},
	}
	for _, c := range cases {
		w := NewWriter()
		w.WriteBits(c.v, c.n)
		if len(w.Bytes()) != 1 || w.Bytes()[0] != c.b {
			t.Fatalf("WriteBits(%b,%d)=%02x, 期望 %02x", c.v, c.n, w.Bytes()[0], c.b)
		}
		got, err := NewReader(w.Bytes()).ReadBits(c.n)
		if err != nil || got != c.v {
			t.Fatalf("读回 %b, 期望 %b", got, c.v)
		}
	}
}
