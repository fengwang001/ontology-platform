package pretty

import (
	"fmt"
	"reflect"
	"sync"
	"testing"
)

// TestConcurrentRenderAndRegister 多个调用方以不同行宽同时渲染同一
// 文档，同时其他 goroutine 登记新片段：结果必须与串行执行等价。
// 用 go test -race 运行以检测数据竞争。
func TestConcurrentRenderAndRegister(t *testing.T) {
	s := NewSession()
	if err := s.Register("base", Group(Seq(Text("x"), Space(), Text("y")))); err != nil {
		t.Fatal(err)
	}
	doc := Group(Seq(Text("["), Ref("base"), Text("]"), Space(), Align(Seq(Text("k"), Space(), Text("v")))))

	widths := []int{1, 2, 3, 5, 8, 13, 21, 100}
	want := make([]Result, len(widths))
	for i, w := range widths {
		res, err := s.Render(doc, w)
		if err != nil {
			t.Fatal(err)
		}
		want[i] = res
	}

	var wg sync.WaitGroup
	// 渲染方：反复以不同行宽渲染同一文档，结果必须等于串行结果。
	for i, w := range widths {
		wg.Add(1)
		go func(i, w int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				got, err := s.Render(doc, w)
				if err != nil {
					t.Errorf("width=%d: %v", w, err)
					return
				}
				if !reflect.DeepEqual(got, want[i]) {
					t.Errorf("width=%d: got %q %+v, want %q %+v", w, got.Text, got.Overlong, want[i].Text, want[i].Overlong)
					return
				}
			}
		}(i, w)
	}
	// 登记方：并发登记互不重名的新片段，全部应成功。
	for g := 0; g < 4; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				name := fmt.Sprintf("frag_%d_%d", g, j)
				if err := s.Register(name, Seq(Text("t"), Ref("base"))); err != nil {
					t.Errorf("Register(%s): %v", name, err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	// 登记完成后，引用新片段的文档可正常渲染。
	if _, err := s.Render(Seq(Ref("frag_0_0"), Ref("frag_3_24")), 10); err != nil {
		t.Fatal(err)
	}
}

// TestConcurrentSameDocument 同一文档被多个调用方以不同行宽同时渲染。
func TestConcurrentSameDocument(t *testing.T) {
	doc := Group(Seq(
		Text("fn("),
		Indent(2, Seq(
			Group(Seq(Text("a"), Space(), Text("b"))),
			Space(),
			Group(Seq(Text("界"), Space(), CondText(",", ";"), Text("c"))),
		)),
		Text(")"),
	))
	const goroutines = 16
	results := make([]Result, goroutines)
	var wg sync.WaitGroup
	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, err := Render(doc, 1+i*3)
			if err != nil {
				t.Errorf("%v", err)
			}
			results[i] = res
		}(i)
	}
	wg.Wait()
	for i, res := range results {
		want, err := Render(doc, 1+i*3)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(res, want) {
			t.Fatalf("width=%d: 并发 %q %+v != 串行 %q %+v", 1+i*3, res.Text, res.Overlong, want.Text, want.Overlong)
		}
	}
}
