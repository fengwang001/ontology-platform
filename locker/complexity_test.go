package locker

import (
	"bytes"
	"testing"
)

// 分配时从「不小于快件规格的最低档」起逐档探测，每档固定 3 次字探测，
// 上限 3*3=9。该上限与格口总数无关——这是「选择开销不随格口总数增长」
// 的可观测证明：见 TestAllocationProbeBoundedAcrossScales。
const maxWordProbesPerAllocation = 9

func buildCabinet(nSmall int) *Cabinet {
	cells := make([]Cell, 0, nSmall)
	for i := 1; i <= nSmall; i++ {
		cells = append(cells, Cell{ID: CellID(i), Size: SizeSmall})
	}
	cfg := testCfg()
	cfg.CodeCount = nSmall + 5
	c, err := NewCabinet(cells, cfg, NewTextLogger(&bytes.Buffer{}))
	if err != nil {
		panic(err)
	}
	return c
}

func TestAllocationProbeBoundedAcrossScales(t *testing.T) {
	scales := []int{63, 64, 65, 4095, 4096, 4097, 30000, 100000}
	var prevAvg float64 = -1
	for _, n := range scales {
		c := buildCabinet(n)
		c.ResetStats()
		// 占掉一半格口（顺序占用，后续释放/再分配会触及位图各级）。
		for i := 1; i <= (n+1)/2; i++ {
			r, err := c.Deposit(int64(i), TrackingNo("X"+itoa(i)), SizeSmall, "13800003333")
			if err != nil {
				t.Fatalf("n=%d deposit %d: %v", n, i, err)
			}
			if r.Cell != CellID(i) {
				t.Fatalf("n=%d allocation order: got cell %d want %d", n, r.Cell, i)
			}
		}
		st := c.Stats()
		allocs := (n + 1) / 2
		if st.Allocations != allocs {
			t.Fatalf("n=%d allocations counted=%d want %d", n, st.Allocations, allocs)
		}
		avg := float64(st.WordProbes) / float64(st.Allocations)
		// 每次分配的探测数必须有与 n 无关的常数上界。
		if avg > maxWordProbesPerAllocation {
			t.Fatalf("n=%d avg probes %.2f exceeds bound %d", n, avg, maxWordProbesPerAllocation)
		}
		// 同样序列、同样规模下平均探测数不随规模上升（直观佐证 O(1)）。
		if prevAvg >= 0 && avg > prevAvg+0.001 {
			t.Fatalf("avg probes grew with scale: %.3f -> %.3f", prevAvg, avg)
		}
		prevAvg = avg
		t.Logf("scale=%6d cells: %5d allocations, %6d word probes, avg=%.3f (bound %d)",
			n, st.Allocations, st.WordProbes, avg, maxWordProbesPerAllocation)
	}
}

// TestProbeBoundUnderMultiTierScan：小、中格口全满时投小快件，
// 必须扫描空小档(1)+空中档(1)+命中大档(3)=最多 5 次，且与格口数无关。
func TestProbeBoundUnderMultiTierScan(t *testing.T) {
	mkCells := func(perTier int) []Cell {
		var cells []Cell
		id := 1
		for tier := Size(0); tier < numSizes; tier++ {
			for k := 0; k < perTier; k++ {
				cells = append(cells, Cell{ID: CellID(id), Size: tier})
				id++
			}
		}
		return cells
	}
	for _, perTier := range []int{10, 1000, 20000} {
		cfg := testCfg()
		cfg.CodeCount = perTier*2 + 5
		c, err := NewCabinet(mkCells(perTier), cfg, NewTextLogger(&bytes.Buffer{}))
		if err != nil {
			t.Fatal(err)
		}
		// 占满全部小、中格口。
		seq := 0
		for _, cell := range mkCells(perTier) {
			if cell.Size == SizeLarge {
				continue
			}
			seq++
			if _, err := c.Deposit(int64(seq), TrackingNo("F"+itoa(seq)),
				cell.Size, "13800003333"); err != nil {
				t.Fatalf("perTier=%d fill: %v", perTier, err)
			}
		}
		c.ResetStats()
		r, err := c.Deposit(int64(seq+1), "BIGISH", SizeSmall, "13800003333")
		if err != nil {
			t.Fatalf("perTier=%d: %v", perTier, err)
		}
		st := c.Stats()
		if st.WordProbes > maxWordProbesPerAllocation {
			t.Fatalf("perTier=%d probes=%d exceed bound; cell=%d",
				perTier, st.WordProbes, r.Cell)
		}
		t.Logf("perTier=%6d: small-parcel allocation into large tier used %d word probes",
			perTier, st.WordProbes)
	}
}
