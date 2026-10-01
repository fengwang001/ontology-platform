package regbank

import (
	"fmt"
	"sync"
	"testing"
)

func concDefs() []RegDef {
	return []RegDef{
		{Name: "R", Fields: []FieldDef{
			{Name: "rw", Lo: 0, Width: 8, Access: RW, Reset: 0x01},
			{Name: "w1c", Lo: 8, Width: 8, Access: W1C, Reset: 0xFF},
			{Name: "w1s", Lo: 16, Width: 8, Access: W1S, Reset: 0x00},
		}},
		{Name: "RC", Fields: []FieldDef{
			{Name: "rc", Lo: 0, Width: 32, Access: RC, Reset: 0xDEADBEEF},
		}},
	}
}

// TestReplayDeterminism 相同脚本在两个独立 Bank 上重放，返回值与最终存值必须完全一致。
func TestReplayDeterminism(t *testing.T) {
	script := func(b *Bank) []uint32 {
		var log []uint32
		r, _ := b.Read("R")
		log = append(log, r)
		_ = b.Write("R", 0x0000_FF00, 15) // W1C 全清
		r, s, _ := b.ReadModifyWrite("R", 0x0000_00FF, 0x0000_0007)
		log = append(log, r, s)
		_ = b.HwSet("R", "w1c", 0x80)
		_ = b.Write("R", 0x00FF_0000, 15) // W1S 置位
		r, _ = b.Read("R")
		log = append(log, r)
		raw, _ := b.Raw("R")
		log = append(log, raw)
		r, _ = b.Read("RC")
		log = append(log, r)
		return log
	}
	b1 := mustBank(t, concDefs())
	b2 := mustBank(t, concDefs())
	l1, l2 := script(b1), script(b2)
	if len(l1) != len(l2) {
		t.Fatalf("log length %d vs %d", len(l1), len(l2))
	}
	for i := range l1 {
		if l1[i] != l2[i] {
			t.Fatalf("replay step %d: %#08x vs %#08x", i, l1[i], l2[i])
		}
	}
	raw1, _ := b1.Raw("R")
	raw2, _ := b2.Raw("R")
	if raw1 != raw2 {
		t.Fatalf("replay final Raw %#08x vs %#08x", raw1, raw2)
	}
	t.Logf("input=固定脚本重放两次 | output=%v | verdict=返回值序列与存值完全相同(确定性)", fmtStrings(l1))
}

func fmtStrings(xs []uint32) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = fmt.Sprintf("%#08x", x)
	}
	return out
}

// TestConcurrent 高并发混合访问：配合 -race 检测数据竞争，
// 同时校验读改的字段不越界、RC 读后必然为 0（含 RMW 内的读）等不变式。
func TestConcurrent(t *testing.T) {
	b := mustBank(t, concDefs())
	const goroutines = 16
	const iterations = 400
	var wg sync.WaitGroup
	var rcMu sync.Mutex

	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(seed int) {
			defer wg.Done()
			for i := 0; i < iterations; i++ {
				switch (seed + i) % 7 {
				case 0:
					if err := b.Write("R", uint32(seed*256+i)&0x0000FF00, 15); err != nil {
						panic(err)
					}
				case 1:
					if _, err := b.Read("R"); err != nil {
						panic(err)
					}
				case 2:
					r, _, err := b.ReadModifyWrite("R", 0x000000FF, uint32(i)&0xFF)
					if err != nil {
						panic(err)
					}
					if r&0xFF00_0000 != 0 { // 未覆盖位永远读 0
						panic("uncovered bits leaked")
					}
				case 3:
					rcMu.Lock()
					r, err := b.Read("RC")
					if err != nil {
						panic(err)
					}
					again, err := b.Read("RC")
					if err != nil || again != 0 {
						panic(fmt.Sprintf("RC second read = %#x after first %#x", again, r))
					}
					rcMu.Unlock()
				case 4:
					rcMu.Lock()
					if err := b.HwSet("RC", "rc", uint32(i)); err != nil {
						panic(err)
					}
					rcMu.Unlock()
				case 5:
					raw, err := b.Raw("R")
					if err != nil {
						panic(err)
					}
					if raw&0xFF00_0000 != 0 {
						panic("uncovered bits stored")
					}
				case 6:
					if err := b.Write("R", 0xFFFFFFFF, 1+((seed+i)%3)); err != nil {
						panic(err)
					}
				}
			}
		}(g)
	}
	wg.Wait()

	raw, _ := b.Raw("R")
	if raw&0xFF00_0000 != 0 {
		t.Fatalf("uncovered bits non-zero after concurrent run: %#08x", raw)
	}
	t.Logf("input=%d goroutines x %d 混合访问 | output=Raw(R)=%#08x | verdict=无竞态,未覆盖位恒 0,字段始终在位宽内",
		goroutines, iterations, raw)
}
