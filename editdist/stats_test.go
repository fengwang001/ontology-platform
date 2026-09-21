package editdist

import (
	"math/rand"
	"strings"
	"testing"
)

func TestCellsFilledIsBandedNotQuadratic(t *testing.T) {
	// 两个长度各 1000、完全不同的串，k=2。
	a := strings.Repeat("a", 1000)
	b := strings.Repeat("b", 1000)
	r, err := Distance(a, b, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !r.Exceeded {
		t.Fatal("completely different strings must exceed k=2")
	}
	// 上界：每行至多 2k+1 个带内单元，共 max(len)+1 行，
	// 即 (2k+1)*(n+1) = 5005；行最小值超限会进一步提前终止。
	// 关键是 O(k*len) 量级，而非 len*len = 1_000_000。
	bound := (2*2 + 1) * (1000 + 1)
	if r.Stats.CellsFilled > bound {
		t.Fatalf("CellsFilled=%d exceeds band bound %d", r.Stats.CellsFilled, bound)
	}
	if r.Stats.CellsFilled == 0 {
		t.Fatal("expected some cells to be filled before early stop")
	}
	t.Logf("cells=%d (bound %d, full grid 1000000)", r.Stats.CellsFilled, bound)
}

func TestWorkArrayLengthIsMinLenScale(t *testing.T) {
	// 两侧各十万个码点，k=3：工作数组长度应为 min(2k+1, m+1) = 7，
	// 远小于 100000*100000。
	a := strings.Repeat("x", 100000)
	b := a[:99997] + "abc" // 末尾 3 处替换，距离恰为 3
	r, err := Distance(a, b, 3)
	if err != nil {
		t.Fatal(err)
	}
	if r.Exceeded || r.Distance != 3 {
		t.Fatalf("want exact distance 3, got %+v", r)
	}
	if r.Stats.MaxWorkLen > 2*3+1 {
		t.Fatalf("MaxWorkLen=%d, want <= %d", r.Stats.MaxWorkLen, 2*3+1)
	}
	t.Logf("MaxWorkLen=%d for 100k x 100k input", r.Stats.MaxWorkLen)
}

func TestFullDistanceWorkArrayIsMinLen(t *testing.T) {
	// 无上限时滚动数组长度为 min(lenA,lenB)+1，仍是 O(min(len))。
	r, err := Full("kitten", "sitting", Options{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Stats.MaxWorkLen != 7 { // min(6,7)+1
		t.Fatalf("MaxWorkLen=%d, want 7", r.Stats.MaxWorkLen)
	}
}

func TestFuzzAgainstNaive(t *testing.T) {
	rng := rand.New(rand.NewSource(42))
	alphabet := []rune("abéc🙂")
	for iter := 0; iter < 500; iter++ {
		as := randomString(rng, alphabet, 12)
		bs := randomString(rng, alphabet, 12)
		want := naiveDist(as, bs)
		k := rng.Intn(14)
		r, err := Distance(as, bs, k)
		if err != nil {
			t.Fatal(err)
		}
		if want <= k {
			if r.Exceeded || r.Distance != want {
				t.Fatalf("Distance(%q,%q,%d)=%+v, want exact %d", as, bs, k, r, want)
			}
		} else if !r.Exceeded {
			t.Fatalf("Distance(%q,%q,%d)=%+v, want exceeded (naive=%d)", as, bs, k, r, want)
		}
		// 对称性同步抽查。
		rev, _ := Distance(bs, as, k)
		if rev.Exceeded != r.Exceeded || rev.Distance != r.Distance ||
			rev.Stats.CellsFilled != r.Stats.CellsFilled {
			t.Fatalf("asymmetry on %q/%q k=%d: %+v vs %+v", as, bs, k, r, rev)
		}
	}
}

func randomString(rng *rand.Rand, alphabet []rune, maxLen int) string {
	n := rng.Intn(maxLen + 1)
	var sb strings.Builder
	for i := 0; i < n; i++ {
		sb.WriteRune(alphabet[rng.Intn(len(alphabet))])
	}
	return sb.String()
}
