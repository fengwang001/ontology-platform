package pretty

import (
	"fmt"
	"testing"
)

// 本文件提供可验证性能特征的基准：
//
//	go test -bench=. -benchmem ./pretty/
//
// 规模翻倍时耗时应近似翻倍（近线性），深度链不应出现随深度平方的
// 增长，也不会栈溢出。

// benchWideDoc 构造含 n 个组的宽文档。
func benchWideDoc(n int) Doc {
	kids := make([]Doc, 0, n*2)
	for i := 0; i < n; i++ {
		kids = append(kids,
			Group(Seq(Text("key:"), Space(), Group(Seq(Text("val"), Blank(), Text("end"))))),
			Space())
	}
	return Seq(kids...)
}

// benchDeepDoc 构造 depth 层组/缩进/对齐混合嵌套。
func benchDeepDoc(depth int) Doc {
	d := Doc(Group(Seq(Text("leaf"), Space(), Text("node"))))
	for i := 0; i < depth; i++ {
		switch i % 3 {
		case 0:
			d = Group(d)
		case 1:
			d = Indent(1, Group(d))
		default:
			d = Align(Group(d))
		}
	}
	return d
}

func benchmarkWide(b *testing.B, n int) {
	doc := benchWideDoc(n)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Render(doc, 60); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkWide10k(b *testing.B) { benchmarkWide(b, 10_000) }
func BenchmarkWide20k(b *testing.B) { benchmarkWide(b, 20_000) }
func BenchmarkWide40k(b *testing.B) { benchmarkWide(b, 40_000) }
func BenchmarkWide80k(b *testing.B) { benchmarkWide(b, 80_000) }

func benchmarkDeep(b *testing.B, depth int) {
	doc := benchDeepDoc(depth)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := Render(doc, 5); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDeep150(b *testing.B) { benchmarkDeep(b, 150) }
func BenchmarkDeep300(b *testing.B) { benchmarkDeep(b, 300) }
func BenchmarkDeep590(b *testing.B) { benchmarkDeep(b, 590) }

// Example 演示基本用法。
func Example() {
	doc := Group(Seq(
		Text("fn("),
		Indent(2, Seq(
			Group(Seq(Text("alpha"), Space(), Text("beta"))),
			Space(),
			Text("gamma"),
		)),
		Text(")"),
	))
	res, err := Render(doc, 12)
	if err != nil {
		panic(err)
	}
	fmt.Print(res.Text)
	// Output:
	// fn(alpha
	//   beta
	//   gamma)
}
