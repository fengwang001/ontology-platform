package pack_test

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"math/rand/v2"
	"sync"
	"sync/atomic"
	"testing"

	"ontology/api"
	"ontology/layout"
	"ontology/pack"
)

// demo 是第三节给定的 schema；demoValues 是给定的测试向量。
var demo, _ = layout.NewSchema([]layout.Spec{{Name: "id", Width: 2}, {Name: "flags", Width: 1}, {Name: "count", Width: 4}, {Name: "score", Width: 2, Signed: true}})

func demoValues() map[string]int64 {
	return map[string]int64{"id": 0x1234, "flags": 0xAB, "count": 0xDEADBEEF, "score": -1}
}
func randValues(rng *rand.Rand, s *layout.Schema) map[string]int64 {
	out := map[string]int64{}
	for _, f := range s.Fields() {
		bits := uint(8 * f.Width)
		switch {
		case f.Signed:
			out[f.Name] = int64(rng.Uint64()<<(64-bits)) >> (64 - bits)
		case bits >= 64:
			out[f.Name] = int64(rng.Uint64() >> 1)
		default:
			out[f.Name] = int64(rng.Uint64() & (1<<bits - 1))
		}
	}
	return out
}

// TestRoundTrip 不变量 1：多 schema × 多档随机值，Pack 再 Unpack 逐字段相同。
func TestRoundTrip(t *testing.T) {
	schemas := []*layout.Schema{demo}
	for _, m := range []int{1, 7, 33} { // 循环生成多档规模
		specs := make([]layout.Spec, m)
		for i := range specs {
			specs[i] = layout.Spec{Name: fmt.Sprintf("f%d", i), Width: 1 << (i % 4), Signed: i%2 == 0}
		}
		s, _ := layout.NewSchema(specs)
		schemas = append(schemas, s) // 构造参数合法，err 必为 nil
	}
	rng := rand.New(rand.NewPCG(7, 8))
	for _, s := range schemas {
		for trial := 0; trial < 200; trial++ {
			in := randValues(rng, s)
			buf, err := pack.Pack(s, in)
			out, err2 := pack.Unpack(s, buf)
			if err != nil || err2 != nil || !maps.Equal(in, out) {
				t.Fatalf("roundtrip: in=%v buf=%x out=%v errs=%v,%v", in, buf, out, err, err2)
			}
		}
	}
}

// TestMatchesNaiveReference 不变量 3：与手写逐字节小端参照字节级一致。
func TestMatchesNaiveReference(t *testing.T) {
	want := []byte{0x34, 0x12, 0xAB, 0xEF, 0xBE, 0xAD, 0xDE, 0xFF, 0xFF}
	if got, err := pack.Pack(demo, demoValues()); err != nil || !bytes.Equal(got, want) {
		t.Fatalf("record: got %x err %v, want %x", got, err, want)
	}
	rng := rand.New(rand.NewPCG(9, 10))
	for trial := 0; trial < 200; trial++ {
		in := randValues(rng, demo)
		got, _ := pack.Pack(demo, in)
		naive := make([]byte, demo.Total())
		for _, f := range demo.Fields() { // 教科书参照：按偏移逐字节写小端
			u := uint64(in[f.Name])
			for i := 0; i < f.Width; i++ {
				naive[f.Offset+i] = byte(u >> (8 * i))
			}
		}
		if !bytes.Equal(got, naive) {
			t.Fatalf("naive mismatch: got %x want %x", got, naive)
		}
	}
}

// TestFailuresAtomic 不变量 4：三类故障互不相同、整体失败、不留痕、之后可用。
func TestFailuresAtomic(t *testing.T) {
	good := demoValues()
	unk, over, under := maps.Clone(good), maps.Clone(good), maps.Clone(good)
	unk["nope"], over["count"], under["score"] = 1, 0x1DEADBEEF, -32769 // 未知名 / 33 位越界 / 有符号下溢
	if b, err := pack.Pack(demo, unk); !errors.Is(err, pack.ErrUnknownField) || b != nil {
		t.Errorf("unknown field: got (%x, %v)", b, err)
	}
	if b, err := pack.Pack(demo, over); !errors.Is(err, pack.ErrOverflow) || b != nil {
		t.Errorf("overflow: got (%x, %v)", b, err)
	}
	if b, err := pack.Pack(demo, under); !errors.Is(err, pack.ErrOverflow) || b != nil {
		t.Errorf("signed underflow: got (%x, %v)", b, err)
	}
	if m, err := pack.Unpack(demo, make([]byte, demo.Total()-1)); !errors.Is(err, pack.ErrShortBuffer) || m != nil {
		t.Errorf("short buffer: got (%v, %v)", m, err)
	}
	if errors.Is(pack.ErrUnknownField, pack.ErrOverflow) || errors.Is(pack.ErrOverflow, pack.ErrShortBuffer) {
		t.Error("sentinel errors not distinct")
	}
	if _, err := pack.Pack(demo, good); err != nil { // 被拒后仍可正常使用
		t.Errorf("unusable after rejection: %v", err)
	}
	if v, err := pack.Unpack(demo, []byte{0, 0, 0, 0, 0, 0, 0, 0xFF, 0xFF}); err != nil || v["score"] != -1 {
		t.Errorf("sign extension: got %d, %v; want -1", v["score"], err)
	}
	if err := api.New(demo).SelfCheck(); err != nil { // 自检四条不变量
		t.Errorf("SelfCheck: %v", err)
	}
}

// TestConcurrentPackUnpack 并发 Pack/Unpack 与串行结果一致。
func TestConcurrentPackUnpack(t *testing.T) {
	const n = 64
	rng := rand.New(rand.NewPCG(11, 12))
	sets := make([]map[string]int64, n)
	wantBufs := make([][]byte, n)
	for i := range sets { // 串行基准
		sets[i] = randValues(rng, demo)
		wantBufs[i], _ = pack.Pack(demo, sets[i])
	}
	wantFields, _ := pack.Unpack(demo, wantBufs[0])
	var wg sync.WaitGroup
	var bad atomic.Int64
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func(i int) { // 并发 Pack 各自不同的字段集合
			defer wg.Done()
			if buf, err := pack.Pack(demo, sets[i]); err != nil || !bytes.Equal(buf, wantBufs[i]) {
				bad.Add(1)
			}
		}(i)
		go func() { // 并发 Unpack 同一段只读字节
			defer wg.Done()
			if out, err := pack.Unpack(demo, wantBufs[0]); err != nil || !maps.Equal(out, wantFields) {
				bad.Add(1)
			}
		}()
	}
	wg.Wait()
	if bad.Load() != 0 {
		t.Errorf("%d concurrent mismatches", bad.Load())
	}
}
