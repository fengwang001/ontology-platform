package record

import "testing"

func TestFrameRoundTrip(t *testing.T) {
	cases := []Record{
		{Key: "", Value: nil, Seq: 0},
		{Key: "k", Value: []byte{}, Seq: 1},
		{Key: "中文键", Value: []byte("v\x00\xff"), Seq: 1 << 40},
	}
	var buf []byte
	for _, r := range cases {
		buf = AppendFrame(buf, r)
	}
	for _, want := range cases {
		got, n, err := DecodeFrame(buf)
		if err != nil {
			t.Fatalf("decode: %v", err)
		}
		if got.Key != want.Key || got.Seq != want.Seq ||
			string(got.Value) != string(want.Value) {
			t.Fatalf("got %+v want %+v", got, want)
		}
		buf = buf[n:]
	}
	if len(buf) != 0 {
		t.Fatalf("trailing bytes: %d", len(buf))
	}
}

func TestDecodeShort(t *testing.T) {
	r := Record{Key: "abc", Value: []byte("xy"), Seq: 7}
	full := AppendFrame(nil, r)
	for n := 0; n < len(full)-1; n++ {
		if _, _, err := DecodeFrame(full[:n]); err != ErrShortFrame {
			if n >= 4 {
				t.Fatalf("len=%d: want ErrShortFrame, got %v", n, err)
			}
		}
	}
}

func TestLessAndSort(t *testing.T) {
	rs := []Record{
		{Key: "b", Seq: 0}, {Key: "a", Seq: 9},
		{Key: "a", Seq: 2}, {Key: "", Seq: 5},
	}
	Sort(rs)
	want := []struct {
		key string
		seq uint64
	}{{"", 5}, {"a", 2}, {"a", 9}, {"b", 0}}
	for i, w := range want {
		if rs[i].Key != w.key || rs[i].Seq != w.seq {
			t.Fatalf("pos %d = %+v, want %+v", i, rs[i], w)
		}
	}
}
