package ontology

import (
	"math/rand"
	"reflect"
	"testing"
)

// TestRandomAgainstNaive 用 2000 组随机输入对照朴素模拟器，
// 校验每页字节完全一致、解码往返还原、重放字节确定。
func TestRandomAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	for iter := 0; iter < 2000; iter++ {
		maxDict := 1 + rng.Intn(48)
		maxRows := 1 + rng.Intn(48)
		poolSize := 1 + rng.Intn(80)
		pool := make([]uint32, poolSize)
		for i := range pool {
			pool[i] = rng.Uint32()
		}
		w1 := mustNew(t, maxDict, maxRows)
		w2 := mustNew(t, maxDict, maxRows) // 重放确定性对照
		nv := newNaive(maxDict, maxRows)
		nPages := 1 + rng.Intn(6)
		for pg := 0; pg < nPages; pg++ {
			var rows []uint32
			for len(rows) < maxRows && rng.Intn(100) < 92 {
				v := pool[rng.Intn(poolSize)]
				l := 1 + rng.Intn(20)
				for k := 0; k < l && len(rows) < maxRows; k++ {
					rows = append(rows, v)
				}
			}
			if len(rows) == 0 {
				rows = append(rows, pool[rng.Intn(poolSize)])
			}
			for _, v := range rows {
				if err := w1.Append(v); err != nil {
					t.Fatalf("iter=%d Append(%d) = %v", iter, v, err)
				}
				if err := w2.Append(v); err != nil {
					t.Fatalf("iter=%d 重放 Append(%d) = %v", iter, v, err)
				}
				if err := nv.append(v); err != nil {
					t.Fatalf("iter=%d 朴素模拟 Append(%d) = %v", iter, v, err)
				}
			}
			p1, err := w1.Flush()
			if err != nil {
				t.Fatalf("iter=%d Flush = %v", iter, err)
			}
			p2, err := w2.Flush()
			if err != nil {
				t.Fatalf("iter=%d 重放 Flush = %v", iter, err)
			}
			pn, dec, err := nv.flush()
			if err != nil {
				t.Fatalf("iter=%d 朴素模拟 Flush = %v", iter, err)
			}
			t.Logf("iter=%d page=%d maxDict=%d maxRows=%d in=%v", iter, pg, maxDict, maxRows, rows)
			t.Logf("  判定: D=%d w=%d N=%d |H|=%d dictSize=%d plainSize=%d via=%s miss=%d fallback=%v",
				dec.d, dec.width, dec.nNew, dec.hLen, dec.dictSize, dec.plainSize, dec.via, nv.miss, nv.fallback)
			t.Logf("  输出: enc=%d rows=%d width=%d dictLen=%d data=% X", p1.Enc, p1.Rows, p1.Width, p1.DictLen, p1.Data)
			if !reflect.DeepEqual(p1, pn) {
				t.Fatalf("iter=%d page=%d 与朴素模拟不一致:\n实现=%+v\n朴素=%+v\n输入=%v", iter, pg, p1, pn, rows)
			}
			if !reflect.DeepEqual(p1, p2) {
				t.Fatalf("iter=%d page=%d 重放字节不一致:\n首次=%+v\n重放=%+v", iter, pg, p1, p2)
			}
			got, err := w1.Decode(p1)
			if err != nil {
				t.Fatalf("iter=%d page=%d Decode = %v, 页=%+v", iter, pg, err, p1)
			}
			if !reflect.DeepEqual(got, rows) {
				t.Fatalf("iter=%d page=%d 解码往返不一致:\n输入=%v\n解码=%v", iter, pg, rows, got)
			}
			if w1.touches > 2*p1.Rows {
				t.Fatalf("iter=%d page=%d touches=%d 超过 2×Rows=%d", iter, pg, w1.touches, 2*p1.Rows)
			}
		}
	}
}
