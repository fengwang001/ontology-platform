package pipeline

import (
	"errors"
	"fmt"
	"testing"

	"ontology/merge"
	"ontology/record"
	"ontology/spill"
)

func ingestN(t *testing.T, p *Pipeline, n int, keyFn func(i int) string) {
	t.Helper()
	for i := 0; i < n; i++ {
		if err := p.Ingest(keyFn(i), []byte("payload!")); err != nil {
			t.Fatalf("Ingest %d: %v", i, err)
		}
	}
}

func assertSorted(t *testing.T, out []record.Record) {
	t.Helper()
	for i := 1; i < len(out); i++ {
		if out[i-1].Key > out[i].Key {
			t.Fatalf("output not non-decreasing at %d: %q > %q", i, out[i-1].Key, out[i].Key)
		}
		if out[i-1].Key == out[i].Key && out[i-1].Seq > out[i].Seq {
			t.Fatalf("equal key %q out of arrival order at %d", out[i].Key, i)
		}
	}
}

// 不变量 1+3：20 万条记录、预算为总量 1/50，溢写文件数 >= 40，
// 历史最大驻留从未越界，输出条数守恒且键非降序。
func TestResidentBoundAndIntegrity(t *testing.T) {
	const n = 200000
	recSize := int64(record.Record{Key: "12345678", Value: []byte("payload!")}.Size())
	limit := int64(n) * recSize / 50
	p, err := Open(t.TempDir(), limit, nil)
	if err != nil {
		t.Fatal(err)
	}
	ingestN(t, p, n, func(i int) string {
		return fmt.Sprintf("%08x", uint32(i)*2654435761)
	})
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if p.MaxResident() > limit {
		t.Fatalf("max resident %d exceeds limit %d", p.MaxResident(), limit)
	}
	if p.RunCount() < 40 {
		t.Fatalf("run count %d < 40", p.RunCount())
	}
	out, err := spill.ReadAll(p.OutputPath())
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(out)) != p.Ingested() || len(out) != n {
		t.Fatalf("output %d records, ingested %d, want %d", len(out), p.Ingested(), n)
	}
	assertSorted(t, out)
	t.Logf("runs=%d maxResident=%d limit=%d compares=%d",
		p.RunCount(), p.MaxResident(), limit, p.Compares())
}

// 不变量 2：归并比较次数不超过 4*N*ceil(log2(K+1))。
func TestComparesBound(t *testing.T) {
	p, err := Open(t.TempDir(), 4096, nil)
	if err != nil {
		t.Fatal(err)
	}
	const n = 5000
	ingestN(t, p, n, func(i int) string {
		return fmt.Sprintf("%08x", uint32(i)*2654435761)
	})
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	k := int64(p.RunCount())
	if k < 2 {
		t.Fatalf("want multiple runs, got %d", k)
	}
	bound := merge.Bound(int64(n), k)
	if p.Compares() > bound {
		t.Fatalf("compares=%d exceeds bound %d (N=%d K=%d)", p.Compares(), bound, n, k)
	}
}

// 等键跨 run：同一键被切进至少 3 个 run，输出必须是到达顺序。
func TestEqualKeyAcrossRuns(t *testing.T) {
	recSize := int64(record.Record{Key: "x", Value: []byte("payload!")}.Size())
	p, err := Open(t.TempDir(), 10*recSize, nil) // 每个 run 至多 10 条
	if err != nil {
		t.Fatal(err)
	}
	const n = 25
	ingestN(t, p, n, func(i int) string { return "x" })
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if p.RunCount() < 3 {
		t.Fatalf("want >= 3 runs, got %d", p.RunCount())
	}
	out, err := spill.ReadAll(p.OutputPath())
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != n {
		t.Fatalf("got %d records, want %d", len(out), n)
	}
	for i, rec := range out {
		if rec.Seq != uint64(i) {
			t.Fatalf("position %d: seq=%d, want arrival order", i, rec.Seq)
		}
	}
}

// 边界语义：零记录、单记录、全同键、空键、空值。
func TestEdgeCases(t *testing.T) {
	cases := []struct {
		name string
		keys []string
		vals [][]byte
	}{
		{"zero records", nil, nil},
		{"single record", []string{"a"}, [][]byte{[]byte("v")}},
		{"all same key", []string{"s", "s", "s", "s"}, [][]byte{[]byte("1"), []byte("2"), []byte("3"), []byte("4")}},
		{"empty keys", []string{"", "", ""}, [][]byte{[]byte("a"), []byte("b"), []byte("c")}},
		{"empty values", []string{"b", "a", "c"}, [][]byte{nil, {}, nil}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := Open(t.TempDir(), 256, nil)
			if err != nil {
				t.Fatal(err)
			}
			for i, key := range tc.keys {
				if err := p.Ingest(key, tc.vals[i]); err != nil {
					t.Fatalf("Ingest: %v", err)
				}
			}
			if err := p.Close(); err != nil {
				t.Fatal(err)
			}
			out, err := spill.ReadAll(p.OutputPath())
			if err != nil {
				t.Fatal(err)
			}
			if len(out) != len(tc.keys) {
				t.Fatalf("got %d records, want %d", len(out), len(tc.keys))
			}
			assertSorted(t, out)
		})
	}
}

// 记录大小超过预算上限：返回可判定错误而不是死循环。
func TestOversizedRecord(t *testing.T) {
	p, err := Open(t.TempDir(), 32, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Ingest("this-key-alone-exceeds-the-budget", nil); !errors.Is(err, ErrRecordTooLarge) {
		t.Fatalf("want ErrRecordTooLarge, got %v", err)
	}
	if err := p.Ingest("ok", nil); err != nil {
		t.Fatalf("pipeline must stay usable: %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := spill.ReadAll(p.OutputPath())
	if err != nil || len(out) != 1 {
		t.Fatalf("got %d records err=%v, want 1", len(out), err)
	}
}
