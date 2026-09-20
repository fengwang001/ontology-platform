package semver

import (
	"math/rand"
	"testing"
)

// TestIntersectMatchEquivalence 是硬要求验证：
// 交集非空时，intersection.Match(v) 必须与「逐个 Range 分别 Match 再取与」
// 对所有版本（含大量预发布版本）完全一致。
func TestIntersectMatchEquivalence(t *testing.T) {
	rng := rand.New(rand.NewSource(20260920))

	rangeExprs := []string{
		">=1.0.0", "<2.0.0", ">=1.2.0 <1.8.0", "^1.3.0", "~1.4.2",
		">=1.5.0-0", "<1.6.0-beta", "=1.5.0", ">=1.0.0-rc.1 <2.0.0",
		"^0.9.0", ">=0.9.5", "=1.4.2-alpha.1",
		">=1.2.0 <=1.7.0", ">1.4.0", "<=1.6.5",
	}
	probes := buildProbeVersions()

	total := 0
	for trial := 0; trial < 400; trial++ {
		n := 2 + rng.Intn(2)
		rs := make([]Range, n)
		exprs := make([]string, n)
		for i := range rs {
			exprs[i] = rangeExprs[rng.Intn(len(rangeExprs))]
			rs[i] = mustRange(t, exprs[i])
		}
		inter, err := Intersect(rs...)
		if err != nil {
			// 交集为空：逐 Range 取与必须对每个探测版本都为 false。
			for _, v := range probes {
				total++
				if matchAll(rs, v) {
					t.Fatalf("%v reported empty but %s matches all ranges", exprs, v.original)
				}
			}
			continue
		}
		for _, v := range probes {
			total++
			got := inter.Match(v)
			want := matchAll(rs, v)
			if got != want {
				t.Fatalf("ranges=%v version=%s: intersection.Match=%v, AND of matches=%v",
					exprs, v.original, got, want)
			}
		}
	}
	t.Logf("equivalence probes executed: %d", total)
}

func matchAll(rs []Range, v Version) bool {
	for _, r := range rs {
		if !r.Match(v) {
			return false
		}
	}
	return true
}

// buildProbeVersions 构造覆盖 release、数字/字母预发布与边界的版本集合。
func buildProbeVersions() []Version {
	texts := []string{
		"0.0.1", "0.9.0", "0.9.5", "0.9.9", "0.10.0",
		"1.0.0", "1.0.1", "1.1.9",
		"1.2.0", "1.2.1", "1.3.0", "1.3.5", "1.4.0", "1.4.1",
		"1.4.2", "1.4.3", "1.5.0", "1.5.1", "1.6.0", "1.6.5",
		"1.7.0", "1.7.9", "1.8.0", "1.9.9", "2.0.0", "2.1.0",
		"1.0.0-alpha", "1.0.0-rc.1", "1.0.0-rc.2", "1.0.0-0",
		"1.2.0-alpha", "1.2.0-0", "1.3.0-9", "1.3.0-10",
		"1.4.2-alpha", "1.4.2-alpha.1", "1.4.2-beta", "1.4.2",
		"1.5.0-alpha", "1.5.0-beta", "1.5.0-rc.1", "1.5.0",
		"1.5.1-alpha", "1.6.0-alpha", "1.6.0-beta", "1.6.0-beta.2",
		"1.6.0-beta.11", "1.6.0-rc.1", "1.6.0",
		"1.7.0-x.7.z.92", "1.7.9-0", "1.8.0-alpha", "2.0.0-0",
		"2.0.0-alpha", "2.0.0-rc.1", "0.9.0-alpha", "0.9.5-1",
	}
	out := make([]Version, 0, len(texts))
	for _, s := range texts {
		v, err := Parse(s)
		if err != nil {
			panic(err)
		}
		out = append(out, v)
	}
	return out
}
