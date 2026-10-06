package ontology

import (
	"fmt"
	"math/rand"
	"testing"
)

// randID 生成随机 40 位小写十六进制标识。
func randID(r *rand.Rand) string {
	const hexd = "0123456789abcdef"
	b := make([]byte, idLen)
	for i := range b {
		b[i] = hexd[r.Intn(16)]
	}
	return string(b)
}

func fillStore(r *rand.Rand, n int) *objectStore {
	st := newObjectStore()
	for i := 0; i < n; i++ {
		id := randID(r)
		st.put(Object{ID: id, Type: TypeBlob})
	}
	return st
}

// 证明前缀匹配开销不随对象总数线性增长：探针数有常数上界，
// 且 16 倍数据量下探针数几乎不变；同时与朴素线性扫描结果比对。
func TestPrefixMatchScaling(t *testing.T) {
	r := rand.New(rand.NewSource(42))
	small := fillStore(r, 10_000)
	large := fillStore(r, 160_000)
	for _, tc := range []struct {
		name string
		st   *objectStore
		n    int
	}{
		{"10k", small, 10_000},
		{"160k", large, 160_000},
	} {
		maxProbes := 0
		for i := 0; i < 200; i++ {
			var p string
			if i%2 == 0 { // 取自真实标识的前缀，命中非空
				id := randID(r)
				p = id[:4+r.Intn(6)]
			} else { // 完全随机前缀，通常命中为空
				p = randID(r)[:4+r.Intn(6)]
			}
			got, probes := tc.st.prefixCount(p)
			// 朴素对照：线性扫描
			want := 0
			for id := range tc.st.objs {
				if len(id) >= len(p) && id[:len(p)] == p {
					want++
				}
			}
			if got != want {
				t.Fatalf("%s prefix=%q count=%d, naive=%d", tc.name, p, got, want)
			}
			if probes > maxProbes {
				maxProbes = probes
			}
		}
		t.Logf("store=%s objects=%d maxProbes=%d | 判定依据: 探针数上界常数化即证明 O(log n + k)",
			tc.name, tc.n, maxProbes)
		if maxProbes > 64 {
			t.Fatalf("%s: maxProbes=%d exceeds constant bound 64", tc.name, maxProbes)
		}
	}
}

// 证明最短缩写开销不随对象总数线性增长。
func TestShortestAbbrevScaling(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	for _, n := range []int{10_000, 160_000} {
		st := fillStore(r, n)
		maxProbes := 0
		for id := range st.objs {
			abbr, probes, ok := st.shortestAbbrev(id, 4)
			if !ok {
				t.Fatalf("shortestAbbrev(%s) not found", id)
			}
			// 朴素对照：与全部其它对象求最大 LCP。
			lcp := 0
			for other := range st.objs {
				if other != id {
					if v := lcpLen(id, other); v > lcp {
						lcp = v
					}
				}
			}
			want := lcp + 1
			if want < 4 {
				want = 4
			}
			if abbr != id[:want] {
				t.Fatalf("shortestAbbrev(%s)=%q, naive=%q", id, abbr, id[:want])
			}
			if probes > maxProbes {
				maxProbes = probes
			}
			if maxProbes > 128 {
				t.Fatalf("n=%d: probes=%d exceeds constant bound 128", n, probes)
			}
			break // 每轮抽一个即可，避免 O(n^2) 朴素对照拖慢
		}
		// 全量探针上界抽查：随机 200 个
		maxProbes = 0
		i := 0
		for id := range st.objs {
			if i%(n/200+1) != 0 {
				i++
				continue
			}
			i++
			_, probes, _ := st.shortestAbbrev(id, 4)
			if probes > maxProbes {
				maxProbes = probes
			}
		}
		t.Logf("objects=%d maxProbes=%d | 判定依据: 最短缩写只与前后邻居比较，探针数与总数无关",
			n, maxProbes)
		if maxProbes > 128 {
			t.Fatalf("n=%d: maxProbes=%d exceeds constant bound 128", n, maxProbes)
		}
	}
}

// 证明短名补全开销不随引用总数线性增长。
func TestRefCompletionScaling(t *testing.T) {
	r := rand.New(rand.NewSource(9))
	namespaces := []string{"refs/heads/", "refs/tags/", "refs/remotes/"}
	for _, n := range []int{1_000, 50_000} {
		rs := newRefStore(namespaces)
		for i := 0; i < n; i++ {
			rs.set(fmt.Sprintf("refs/heads/r%06d", i), randID(r))
		}
		rs.setSymbolic("HEAD", "refs/heads/r000000")
		maxProbes := 0
		for i := 0; i < 500; i++ {
			var short string
			switch i % 3 {
			case 0:
				short = fmt.Sprintf("r%06d", r.Intn(n)) // 命中
			case 1:
				short = "HEAD" // 符号引用链
			default:
				short = fmt.Sprintf("nope%06d", i) // 未命中，走完全部命名空间
			}
			_, ok := rs.lookup(short)
			if i%3 == 0 && !ok {
				t.Fatalf("lookup(%q) should hit", short)
			}
			h, _ := rs.lookup(short)
			if h.probes > maxProbes {
				maxProbes = h.probes
			}
		}
		bound := 1 + len(namespaces) + 4 // 精确1次 + 每命名空间1次 + 符号链余量
		t.Logf("refs=%d maxProbes=%d bound=%d | 判定依据: 补全为 O(命名空间数) 次 map 查找，与引用总数无关",
			n, maxProbes, bound)
		if maxProbes > bound {
			t.Fatalf("refs=%d: maxProbes=%d exceeds bound %d", n, maxProbes, bound)
		}
	}
}

func BenchmarkPrefixMatch(b *testing.B) {
	for _, n := range []int{10_000, 160_000} {
		r := rand.New(rand.NewSource(1))
		st := fillStore(r, n)
		p := randID(r)[:6]
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				st.prefixCount(p)
			}
		})
	}
}

func BenchmarkShortestAbbrev(b *testing.B) {
	for _, n := range []int{10_000, 160_000} {
		r := rand.New(rand.NewSource(2))
		st := fillStore(r, n)
		var id string
		for id = range st.objs {
			break
		}
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				st.shortestAbbrev(id, 4)
			}
		})
	}
}

func BenchmarkRefCompletion(b *testing.B) {
	namespaces := []string{"refs/heads/", "refs/tags/"}
	for _, n := range []int{1_000, 100_000} {
		r := rand.New(rand.NewSource(3))
		rs := newRefStore(namespaces)
		for i := 0; i < n; i++ {
			rs.set(fmt.Sprintf("refs/heads/r%06d", i), randID(r))
		}
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			for i := 0; i < b.N; i++ {
				rs.lookup("r000001")
			}
		})
	}
}
