package snippets

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// randomBoundaryText 生成一个字节序列及其合法字符边界：
// 以 1~3 字节的 rune 单元拼接，保证文本合法 UTF-8 且边界明确。
func randomBoundaryText(rng *rand.Rand) ([]byte, []bool) {
	n := 1 + rng.Intn(40)
	allowed := [][]byte{
		[]byte("a"), []byte("b"),
		{0xC3, 0xA9},       // é, 2 字节
		{0xE3, 0x81, 0x82}, // あ, 3 字节
	}
	var text []byte
	boundary := []bool{true}
	for len(text) < n {
		u := allowed[rng.Intn(len(allowed))]
		text = append(text, u...)
		for k := 0; k < len(u)-1; k++ {
			boundary = append(boundary, false)
		}
		boundary = append(boundary, true)
	}
	return text, boundary
}

func randomHits(rng *rand.Rand, n int, boundary []bool, count int) []Hit {
	edges := []int{}
	for i, b := range boundary {
		if b {
			edges = append(edges, i)
		}
	}
	hits := make([]Hit, 0, count)
	for i := 0; i < count; i++ {
		s := edges[rng.Intn(len(edges))]
		e := edges[rng.Intn(len(edges))]
		if s == e {
			continue
		}
		if s > e {
			s, e = e, s
		}
		hits = append(hits, Hit{s, e})
	}
	return hits
}

// TestRandomDifferential 用 2000 组随机输入将生产实现与朴素实现对拍，
// 每组都打印输入、双方输出与判定依据。
func TestRandomDifferential(t *testing.T) {
	if testing.Verbose() {
		t.Log("判定依据: 生产实现 selectSnippets 与逐轮朴素实现 naiveSnippets 的结构完全一致（含 Start/End/Highlights/Score 与顺序）")
	}
	const rounds = 2000
	rng := rand.New(rand.NewSource(20261001))

	for iter := 0; iter < rounds; iter++ {
		text, boundary := randomBoundaryText(rng)
		n := len(text)
		W := 1 + rng.Intn(12)
		K := 1 + rng.Intn(16)
		count := rng.Intn(20)
		hits := randomHits(rng, n, boundary, count)

		// 随机打乱输入顺序，验证结果与命中给出顺序无关。
		rng.Shuffle(len(hits), func(i, j int) { hits[i], hits[j] = hits[j], hits[i] })

		// 生产实现路径：通过 Registry 走完整校验。
		reg := NewRegistry()
		if err := reg.Register("doc", text); err != nil {
			t.Fatalf("iter %d register: %v", iter, err)
		}
		got, gotErr := reg.Snippets("doc", hits, W, K)

		if gotErr != nil {
			t.Fatalf("iter %d unexpected error: %v", iter, gotErr)
		}
		want := naiveSnippets(append([]Hit(nil), hits...), n, W, K)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("iter %d mismatch\ninput: text=%q hits=%v W=%d K=%d\ngot:  %s\nwant: %s",
				iter, text, hits, W, K, formatSnippets(got), formatSnippets(want))
		}
		reason := fmt.Sprintf("生产输出与朴素实现 DeepEqual 一致；选中 %d 个片段（上限 K=%d）", len(got), K)

		if testing.Verbose() {
			t.Logf("iter %d 输入: text=%q(len=%d) hits=%v W=%d K=%d\n输出: %s\n判定: %s",
				iter, text, n, hits, W, K, formatSnippets(got), reason)
		}
	}
}
