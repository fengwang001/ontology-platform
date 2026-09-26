package api

import (
	"slices"
	"sync"
	"testing"

	"ontology/codec"
	"ontology/ord"
)

func TestSelfCheck(t *testing.T) {
	if err := New().SelfCheck(); err != nil {
		t.Fatalf("SelfCheck: %v", err)
	}
}

func TestMarshalUnmarshal(t *testing.T) {
	a := New()
	cases := []struct {
		w    int
		vals []int64
	}{
		{2, []int64{-1, 0, 32767, -32768}},
		{4, []int64{1<<31 - 1, -1 << 31}},
		{8, []int64{1<<63 - 1, -1 << 63}},
	}
	for _, o := range []ord.ByteOrder{ord.BigEndian, ord.LittleEndian} {
		for _, c := range cases {
			buf, err := a.Marshal(o, c.w, c.vals)
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			back, err := a.Unmarshal(o, c.w, buf)
			if err != nil || len(back) != len(c.vals) {
				t.Fatalf("Unmarshal: %v", err)
			}
			for i, v := range c.vals {
				if back[i] != v {
					t.Fatalf("w=%d: got %d want %d", c.w, back[i], v)
				}
			}
		}
	}
	if got, err := a.Marshal(ord.BigEndian, 3, []int64{1}); got != nil || err != codec.ErrInvalidWidth {
		t.Fatalf("非法宽度: got=%v err=%v", got, err)
	}
	if got, err := a.Unmarshal(ord.BigEndian, 4, make([]byte, 6)); got != nil || err != codec.ErrMisaligned {
		t.Fatalf("不对齐: got=%v err=%v", got, err)
	}
}

func TestConcurrentSelfCheck(t *testing.T) {
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(v uint64) {
			defer wg.Done()
			if err := New().SelfCheck(); err != nil {
				t.Error(err)
			}
			if ord.Swap64(ord.Swap64(v)) != v {
				t.Errorf("并发 Swap 未还原: %d", v)
			}
		}(uint64(i)*2654435761 + 1)
	}
	wg.Wait()
}

func TestConcurrentCodec(t *testing.T) {
	vals := make([]int64, 500)
	for i := range vals {
		vals[i] = int64(int32(i*2654435761 - 1<<30)) // 限定在 int32 范围内
	}
	buf := codec.Encode(ord.BigEndian, 4, vals)
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			got, err := codec.Decode(ord.BigEndian, 4, buf) // 同一段只读字节
			if err != nil || !slices.Equal(got, vals) {
				t.Errorf("并发 Decode 不一致: %v", err)
			}
			mine := make([]int64, 20) // 各自不同的值序列
			for j := range mine {
				mine[j] = int64(seed*1000 + j)
			}
			enc := codec.Encode(ord.LittleEndian, 8, mine)
			if back, _ := codec.Decode(ord.LittleEndian, 8, enc); !slices.Equal(back, mine) {
				t.Error("并发 Encode 与串行结果不一致")
			}
		}(i)
	}
	wg.Wait()
}
