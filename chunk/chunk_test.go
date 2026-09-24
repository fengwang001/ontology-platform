package chunk

import (
	"bytes"
	"errors"
	"math/rand"
	"testing"
)

// 滚动校验和：从偏移 i 推进到 i+1 必须与在 i+1 处从头重算完全相同，
// 对每一段随机数据的每一个偏移逐个断言。
func TestRollMatchesRecompute(t *testing.T) {
	cases := []struct{ dataLen, blockSize int }{
		{64, 4}, {100, 7}, {257, 16}, {33, 32}, {5, 5},
	}
	for _, tc := range cases {
		rng := rand.New(rand.NewSource(int64(tc.dataLen*1000 + tc.blockSize)))
		data := make([]byte, tc.dataLen)
		rng.Read(data)
		w := SumWeak(data[:tc.blockSize])
		for i := 0; i+tc.blockSize < len(data); i++ {
			w = w.Roll(data[i], data[i+tc.blockSize], tc.blockSize)
			fresh := SumWeak(data[i+1 : i+1+tc.blockSize])
			if w != fresh {
				t.Fatalf("len=%d bs=%d offset=%d: roll=%v fresh=%v",
					tc.dataLen, tc.blockSize, i+1, w.Value(), fresh.Value())
			}
		}
	}
}

// 弱校验和碰撞：[1,0,0,1] 与 [0,1,1,0] 弱值相同、内容不同、强值不同。
func TestWeakCollision(t *testing.T) {
	cases := []struct{ a, b []byte }{
		{[]byte{1, 0, 0, 1}, []byte{0, 1, 1, 0}},
		{[]byte{2, 0, 0, 2}, []byte{0, 2, 2, 0}},
	}
	for _, tc := range cases {
		if SumWeak(tc.a) != SumWeak(tc.b) {
			t.Fatalf("expected weak collision for %v vs %v", tc.a, tc.b)
		}
		if bytes.Equal(tc.a, tc.b) {
			t.Fatalf("test blocks must differ: %v", tc.a)
		}
		if SumStrong(tc.a) == SumStrong(tc.b) {
			t.Fatalf("strong checksum must distinguish %v vs %v", tc.a, tc.b)
		}
	}
}

// 滚动全程的基本运算次数不超过 4 × 数据长度；逐偏移重算则是 长度×块大小。
func TestWeakOpsBound(t *testing.T) {
	cases := []struct{ dataLen, blockSize int }{
		{4096, 64}, {1000, 16}, {97, 97},
	}
	for _, tc := range cases {
		rng := rand.New(rand.NewSource(int64(tc.dataLen)))
		data := make([]byte, tc.dataLen)
		rng.Read(data)
		ResetWeakOps()
		w := SumWeak(data[:tc.blockSize])
		for i := 0; i+tc.blockSize < len(data); i++ {
			w = w.Roll(data[i], data[i+tc.blockSize], tc.blockSize)
		}
		if got, limit := WeakOps(), uint64(4*len(data)); got > limit {
			t.Fatalf("len=%d bs=%d: weakOps=%d > %d", tc.dataLen, tc.blockSize, got, limit)
		}
		if naive := uint64(len(data) * tc.blockSize); WeakOps() >= naive {
			t.Fatalf("rolling cost %d not better than recompute %d", WeakOps(), naive)
		}
	}
}

// 切分边界：空数据、整数倍、非整数倍、块大于数据、块大小为 0、末块不满。
func TestSplitEdges(t *testing.T) {
	cases := []struct {
		dataLen, size int
		wantBlocks    int
		wantLastLen   int
		wantErr       error
	}{
		{0, 8, 0, 0, nil},
		{16, 8, 2, 8, nil},
		{17, 8, 3, 1, nil},
		{5, 100, 1, 5, nil},
		{8, 8, 1, 8, nil},
		{10, 0, 0, 0, ErrInvalidBlockSize},
		{0, 0, 0, 0, ErrInvalidBlockSize},
	}
	for _, tc := range cases {
		data := make([]byte, tc.dataLen)
		for i := range data {
			data[i] = byte(i)
		}
		n, err := NumBlocks(len(data), tc.size)
		if !errors.Is(err, tc.wantErr) {
			t.Fatalf("len=%d size=%d: err=%v want %v", tc.dataLen, tc.size, err, tc.wantErr)
		}
		if err != nil {
			continue
		}
		if n != tc.wantBlocks {
			t.Fatalf("len=%d size=%d: blocks=%d want %d", tc.dataLen, tc.size, n, tc.wantBlocks)
		}
		if n > 0 {
			if last := Block(data, tc.size, n-1); len(last) != tc.wantLastLen {
				t.Fatalf("len=%d size=%d: last block %d want %d",
					tc.dataLen, tc.size, len(last), tc.wantLastLen)
			}
		}
		if got := Block(data, tc.size, n); got != nil {
			t.Fatalf("out-of-range block should be nil, got %v", got)
		}
	}
}
