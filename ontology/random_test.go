package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

type genSchema struct {
	fields []Field
	rng    *rand.Rand
}

func genFields(rng *rand.Rand, depth, maxDepth int, leafBudget *int) []Field {
	n := 1 + rng.Intn(3)
	var fs []Field
	names := map[string]bool{}
	for i := 0; i < n; i++ {
		base := string(rune('a' + rng.Intn(6)))
		name := base
		for k := 0; names[name]; k++ {
			name = fmt.Sprintf("%s%d", base, k)
		}
		names[name] = true

		rep := Rep(rng.Intn(3))
		f := Field{Name: name, Rep: rep}
		canGroup := depth < maxDepth && *leafBudget > 0 && rng.Intn(2) == 0
		if canGroup {
			f.Children = genFields(rng, depth+1, maxDepth, leafBudget)
			if len(f.Children) == 0 {
				f.Children = nil
			}
		}
		if len(f.Children) == 0 {
			*leafBudget--
		}
		fs = append(fs, f)
		if *leafBudget <= 0 {
			break
		}
	}
	// 保证至少一个叶子
	hasLeaf := false
	var check func(fs2 []Field) bool
	check = func(fs2 []Field) bool {
		for _, f := range fs2 {
			if len(f.Children) == 0 || check(f.Children) {
				return true
			}
		}
		return false
	}
	if !check(fs) {
		for i := range fs {
			if len(fs[i].Children) == 0 {
				hasLeaf = true
				break
			}
		}
		if !hasLeaf {
			fs[0].Children = nil
		}
	}
	return fs
}

func genRecord(rng *rand.Rand, fs []Field) map[string]any {
	g := map[string]any{}
	for _, f := range fs {
		missing := rng.Intn(3) == 0
		if f.Rep == Required {
			missing = false
		}
		if missing {
			continue
		}
		empty := f.Rep != Required && rng.Intn(4) == 0
		if len(f.Children) == 0 {
			switch f.Rep {
			case Repeated:
				if empty {
					if rng.Intn(2) == 0 {
						g[f.Name] = []any{}
					} // 否则缺键
					continue
				}
				k := rng.Intn(4)
				list := make([]any, k)
				for i := range list {
					list[i] = int64(rng.Intn(20) - 10)
				}
				g[f.Name] = list
			default:
				g[f.Name] = int64(rng.Intn(20) - 10)
			}
			continue
		}
		if f.Rep == Repeated {
			if empty {
				g[f.Name] = []any{}
				continue
			}
			k := rng.Intn(3)
			list := make([]any, k)
			for i := range list {
				list[i] = genRecord(rng, f.Children)
			}
			g[f.Name] = list
		} else {
			g[f.Name] = genRecord(rng, f.Children)
		}
	}
	return g
}

func TestRandom2000(t *testing.T) {
	if testing.Short() {
		t.Skip("short")
	}
	rng := rand.New(rand.NewSource(20261002))
	pass := 0
	for iter := 0; iter < 2000; iter++ {
		leafBudget := 1 + rng.Intn(16)
		maxDepth := 1 + rng.Intn(6)
		schema := genFields(rng, 1, maxDepth, &leafBudget)

		s, err := New(schema, 1+rng.Intn(5), 1+rng.Intn(200))
		if err != nil {
			t.Fatalf("iter %d: valid schema rejected: %v\n%+v", iter, err, schema)
		}

		nRec := 1 + rng.Intn(6)
		prevLen := map[string]int{}
		accepted := 0
		for r := 0; r < nRec; r++ {
			rec := genRecord(rng, schema)
			wantEntries := naiveShred(schema, rec)
			before := s.Records()
			err := s.Shred(rec)
			if errors.Is(err, ErrTooLarge) {
				// 超过 maxEntries 是合法拒绝：状态不得变化，本条记录跳过。
				if s.Records() != before {
					t.Fatalf("iter %d: rejected record changed record count", iter)
				}
				continue
			}
			if err != nil {
				t.Fatalf("iter %d rec %d: valid record rejected: %v\nrec=%#v", iter, r, err, rec)
			}

			for _, name := range s.LeafNames() {
				got, _ := s.Entries(name)
				newPart := got[prevLen[name]:]
				if !reflect.DeepEqual(newPart, wantEntries[name]) {
					t.Fatalf("iter %d rec %d col %s entry mismatch:\n got %v\nwant %v\nrec=%#v\nschema=%+v",
						iter, r, name, got, wantEntries[name], rec, schema)
				}
				prevLen[name] = len(got)
			}

			// 分页对照
			// 对所有列累计校验朴素分页与实现一致
			for _, name := range s.LeafNames() {
				es, _ := s.Entries(name)
				wantPages := naivePages(es, s.pageSize, 0)
				gotPages, _ := s.Pages(name)
				if !reflect.DeepEqual(gotPages, wantPages) {
					t.Fatalf("iter %d col %s page mismatch:\n got %+v\nwant %+v\nentries=%v",
						iter, name, gotPages, wantPages, es)
				}
			}

			out, err := s.Assemble()
			if err != nil {
				t.Fatalf("iter %d: assemble: %v", iter, err)
			}
			if len(out) != accepted+1 {
				t.Fatalf("iter %d: records=%d want %d", iter, len(out), accepted+1)
			}
			wantRec := naiveNormalize(schema, rec)
			if !reflect.DeepEqual(out[accepted], wantRec) {
				t.Fatalf("iter %d rec %d assemble mismatch:\n got %#v\nwant %#v\ninput=%#v\nschema=%+v",
					iter, accepted, out[accepted], wantRec, rec, schema)
			}
			accepted++
			pass++
		}

		total := 0
		for _, name := range s.LeafNames() {
			es, _ := s.Entries(name)
			total += len(es)
		}
		if got := s.EntriesRead(); got != total {
			t.Fatalf("iter %d entriesRead=%d total=%d", iter, got, total)
		}

		t.Logf("case %d: schema=%+v records=%d leaves=%d pageSize=%d verdict=PASS 输入/输出已逐列比对条目、分页与重装结果",
			iter, schema, nRec, len(s.LeafNames()), s.pageSize)
	}
	t.Logf("random cases passed: %d iterations, %d records", 2000, pass)
}
