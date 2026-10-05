package ingest_test

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"

	"ontology/ingest"
	"ontology/mapping"
)

func mustEngine(t *testing.T, dyn mapping.Dynamic, fmax int) *ingest.Engine {
	t.Helper()
	e, err := ingest.New(dyn, fmax)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return e
}

func checkErr(t *testing.T, err error, sentinel error, path string) {
	t.Helper()
	if err == nil {
		t.Fatalf("应报错 %v(%s)，实际成功", sentinel, path)
	}
	if !errors.Is(err, sentinel) {
		t.Fatalf("错误类别应为 %v，实际 %v", sentinel, err)
	}
	var pe *mapping.PathError
	if !errors.As(err, &pe) || pe.Path != path {
		t.Fatalf("错误路径应为 %q，实际 %v", path, err)
	}
}

func mustIndex(t *testing.T, e *ingest.Engine, id string, doc map[string]any) (map[string]any, []string, int) {
	t.Helper()
	norm, ign, mv, err := e.Index(id, doc)
	if err != nil {
		t.Fatalf("Index(%s) 应成功: %v", id, err)
	}
	return norm, ign, mv
}

// TestWorkedExample 逐条复现题目中的 d1..d7 示例。
func TestWorkedExample(t *testing.T) {
	e := mustEngine(t, mapping.True, 4)

	// d1: 新建 a(object)、a.b(long)、c(keyword)，mv=1，c 存为 ["2","3"]。
	norm, ign, mv := mustIndex(t, e, "d1", map[string]any{
		"a": map[string]any{"b": int64(1)},
		"c": []any{"2", int64(3)},
	})
	if mv != 1 {
		t.Fatalf("d1 后 mv 应为 1，实际 %d", mv)
	}
	wantC := []any{"2", "3"}
	if !reflect.DeepEqual(norm["c"], wantC) {
		t.Fatalf("d1.c 应为 %v，实际 %v", wantC, norm["c"])
	}
	if len(ign) != 0 {
		t.Fatalf("d1 不应有 Ignored，实际 %v", ign)
	}
	wantFields := map[string]mapping.Type{"a": mapping.Object, "a.b": mapping.Long, "c": mapping.Keyword}
	if !reflect.DeepEqual(e.Fields(), wantFields) {
		t.Fatalf("d1 后字段应为 %v，实际 %v", wantFields, e.Fields())
	}

	// d2: "07" 前导零，报类型冲突 a.b。
	_, _, _, err := e.Index("d2", map[string]any{"a": map[string]any{"b": "07"}})
	checkErr(t, err, mapping.ErrTypeConflict, "a.b")

	// d3: a.b 转 -7，a.x 新建为第 4 个字段，e 将是第 5 个，报字段超限 e；
	// 整份拒绝，a.x 不留下，mv 仍为 1。
	_, _, _, err = e.Index("d3", map[string]any{
		"a": map[string]any{"b": "-7", "x": 1.5},
		"e": true,
	})
	checkErr(t, err, mapping.ErrFieldLimit, "e")
	if !reflect.DeepEqual(e.Fields(), wantFields) {
		t.Fatalf("d3 被拒后字段不应变化，实际 %v", e.Fields())
	}
	if e.MV() != 1 {
		t.Fatalf("d3 被拒后 mv 应为 1，实际 %d", e.MV())
	}

	// d4: a 是 object，标量 5 报类型冲突 a。
	_, _, _, err = e.Index("d4", map[string]any{"a": int64(5)})
	checkErr(t, err, mapping.ErrTypeConflict, "a")

	// d5: a.b 存 int64(3)，a.x 新建为 long，mv=2。
	norm, _, mv = mustIndex(t, e, "d5", map[string]any{
		"a": map[string]any{"b": 3.0, "x": int64(2)},
	})
	if mv != 2 {
		t.Fatalf("d5 后 mv 应为 2，实际 %d", mv)
	}
	wantA := map[string]any{"b": int64(3), "x": int64(2)}
	if !reflect.DeepEqual(norm["a"], wantA) {
		t.Fatalf("d5.a 应为 %v，实际 %v", wantA, norm["a"])
	}
	if e.Fields()["a.x"] != mapping.Long {
		t.Fatalf("a.x 应为 long，实际 %v", e.Fields()["a.x"])
	}

	// d6: a.x 是 long，2.5 报类型冲突 a.x。
	_, _, _, err = e.Index("d6", map[string]any{"a": map[string]any{"x": 2.5}})
	checkErr(t, err, mapping.ErrTypeConflict, "a.x")

	// d7: c 存 "true"，a.b 为 nil 不触碰，mv 不变。
	norm, _, mv = mustIndex(t, e, "d7", map[string]any{
		"c": true,
		"a": map[string]any{"b": nil},
	})
	if mv != 2 {
		t.Fatalf("d7 后 mv 应为 2，实际 %d", mv)
	}
	if norm["c"] != "true" {
		t.Fatalf("d7.c 应为 \"true\"，实际 %v", norm["c"])
	}
	if !reflect.DeepEqual(norm["a"], map[string]any{"b": nil}) {
		t.Fatalf("d7.a 应为 {b:nil}，实际 %v", norm["a"])
	}
}

// TestDynamicFalse 同一份 d1 在 dynamic=false 空映射下只存空文档。
func TestDynamicFalse(t *testing.T) {
	e := mustEngine(t, mapping.False, 4)
	norm, ign, mv := mustIndex(t, e, "d1", map[string]any{
		"a": map[string]any{"b": int64(1)},
		"c": []any{"2", int64(3)},
	})
	if len(norm) != 0 {
		t.Fatalf("dynamic=false 应只存空文档，实际 %v", norm)
	}
	if !reflect.DeepEqual(ign, []string{"a", "c"}) {
		t.Fatalf("Ignored 应为 [a c]，实际 %v", ign)
	}
	if mv != 0 || len(e.Fields()) != 0 {
		t.Fatalf("dynamic=false 不应建字段，mv=%d fields=%v", mv, e.Fields())
	}

	// 已有字段的键正常处理，未知键忽略且整棵子树不校验。
	e2 := mustEngine(t, mapping.False, 4)
	if _, err := e2.PutMapping("a", mapping.Object); err != nil {
		t.Fatal(err)
	}
	if _, err := e2.PutMapping("a.b", mapping.Long); err != nil {
		t.Fatal(err)
	}
	norm, ign, _ = mustIndex(t, e2, "d", map[string]any{
		"a": map[string]any{"b": int64(1), "x": map[string]any{"y": "07"}},
		"z": []any{int64(1)},
	})
	wantA := map[string]any{"b": int64(1)}
	if !reflect.DeepEqual(norm["a"], wantA) {
		t.Fatalf("a 应只保留 b，实际 %v", norm["a"])
	}
	if !reflect.DeepEqual(ign, []string{"a.x", "z"}) {
		t.Fatalf("Ignored 应为 [a.x z]，实际 %v", ign)
	}
}

// TestDynamicStrict 未知字段报严格模式拒绝，已有字段正常强转。
func TestDynamicStrict(t *testing.T) {
	e := mustEngine(t, mapping.Strict, 4)
	if _, err := e.PutMapping("a.b", mapping.Long); err != nil {
		t.Fatal(err)
	}
	_, _, _, err := e.Index("d1", map[string]any{"a": map[string]any{"b": int64(1), "c": int64(2)}})
	checkErr(t, err, mapping.ErrStrict, "a.c")
	_, _, _, err = e.Index("d2", map[string]any{"z": int64(1)})
	checkErr(t, err, mapping.ErrStrict, "z")
	norm, _, mv := mustIndex(t, e, "d3", map[string]any{"a": map[string]any{"b": "7"}})
	if !reflect.DeepEqual(norm["a"], map[string]any{"b": int64(7)}) {
		t.Fatalf("a.b 应强转为 int64(7)，实际 %v", norm["a"])
	}
	if mv != 1 {
		t.Fatalf("strict 下未新增字段，mv 应为 1（仅 PutMapping），实际 %d", mv)
	}
}

// TestArrayTyping 数组由首个非 nil 元素定型，其余元素按该类型强转。
func TestArrayTyping(t *testing.T) {
	cases := []struct {
		name    string
		arr     []any
		wantTyp mapping.Type
		want    []any
	}{
		{"int 定型 long", []any{int64(1), "2"}, mapping.Long, []any{int64(1), int64(2)}},
		{"string 定型 keyword", []any{"2", int64(1)}, mapping.Keyword, []any{"2", "1"}},
		{"nil 开头由后续定型", []any{nil, "2", int64(1)}, mapping.Keyword, []any{nil, "2", "1"}},
		{"bool 定型", []any{true, "false"}, mapping.Bool, []any{true, false}},
		{"double 定型", []any{2.0, int64(1)}, mapping.Double, []any{2.0, 1.0}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := mustEngine(t, mapping.True, 10)
			norm, _, _ := mustIndex(t, e, "d", map[string]any{"f": c.arr})
			if e.Fields()["f"] != c.wantTyp {
				t.Fatalf("f 应为 %v，实际 %v", c.wantTyp, e.Fields()["f"])
			}
			if !reflect.DeepEqual(norm["f"], c.want) {
				t.Fatalf("f 应为 %v，实际 %v", c.want, norm["f"])
			}
		})
	}

	// [1, 2.5]：long 定型后 2.5 报类型冲突。
	e := mustEngine(t, mapping.True, 10)
	_, _, _, err := e.Index("d", map[string]any{"f": []any{int64(1), 2.5}})
	checkErr(t, err, mapping.ErrTypeConflict, "f")

	// 全 nil 数组与空数组不建字段、原样保留。
	e2 := mustEngine(t, mapping.True, 10)
	norm, _, mv := mustIndex(t, e2, "d", map[string]any{
		"n": []any{nil, nil},
		"e": []any{},
	})
	if len(e2.Fields()) != 0 || mv != 0 {
		t.Fatalf("全 nil/空数组不应建字段，fields=%v mv=%d", e2.Fields(), mv)
	}
	if !reflect.DeepEqual(norm["n"], []any{nil, nil}) || !reflect.DeepEqual(norm["e"], []any{}) {
		t.Fatalf("应原样保留，实际 %v", norm)
	}

	// 已有字段时数组元素按字段类型强转，与首元素类型无关。
	e3 := mustEngine(t, mapping.True, 10)
	mustIndex(t, e3, "d", map[string]any{"f": "x"}) // f: keyword
	norm, _, _ = mustIndex(t, e3, "d", map[string]any{"f": []any{int64(1), int64(2)}})
	if !reflect.DeepEqual(norm["f"], []any{"1", "2"}) {
		t.Fatalf("已有 keyword 字段应强转元素，实际 %v", norm["f"])
	}
}

// TestScalarCoerce 覆盖 2.0/2.5、前导零、-0、2^53 边界。
func TestScalarCoerce(t *testing.T) {
	cases := []struct {
		name    string
		typ     mapping.Type
		seed    any
		in      any
		want    any
		wantErr bool
	}{
		{"long 收 2.0", mapping.Long, int64(0), 2.0, int64(2), false},
		{"long 拒 2.5", mapping.Long, int64(0), 2.5, nil, true},
		{"long 拒前导零", mapping.Long, int64(0), "07", nil, true},
		{"long 拒 -0", mapping.Long, int64(0), "-0", nil, true},
		{"long 收规范串", mapping.Long, int64(0), "-7", int64(-7), false},
		{"double 收 2^53", mapping.Double, 0.0, int64(1) << 53, float64(1 << 53), false},
		{"double 拒 2^53+1", mapping.Double, 0.0, int64(1)<<53 + 1, nil, true},
		{"keyword 收 bool", mapping.Keyword, "", true, "true", false},
		{"bool 拒 True", mapping.Bool, false, "True", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := mustEngine(t, mapping.True, 10)
			mustIndex(t, e, "seed", map[string]any{"f": c.seed})
			if e.Fields()["f"] != c.typ {
				t.Fatalf("种子字段应为 %v，实际 %v", c.typ, e.Fields()["f"])
			}
			norm, _, _, err := e.Index("d", map[string]any{"f": c.in})
			if c.wantErr {
				checkErr(t, err, mapping.ErrTypeConflict, "f")
				return
			}
			if err != nil {
				t.Fatalf("应成功: %v", err)
			}
			if !reflect.DeepEqual(norm["f"], c.want) {
				t.Fatalf("f 应为 %v(%T)，实际 %v(%T)", c.want, c.want, norm["f"], norm["f"])
			}
		})
	}
}

// TestObjectLeafCollision 对象与叶子互撞，两个方向都报类型冲突。
func TestObjectLeafCollision(t *testing.T) {
	// 叶子字段拒对象。
	e := mustEngine(t, mapping.True, 10)
	mustIndex(t, e, "d", map[string]any{"a": int64(5)})
	_, _, _, err := e.Index("d", map[string]any{"a": map[string]any{"b": int64(1)}})
	checkErr(t, err, mapping.ErrTypeConflict, "a")

	// object 字段拒标量。
	e2 := mustEngine(t, mapping.True, 10)
	mustIndex(t, e2, "d", map[string]any{"a": map[string]any{"b": int64(1)}})
	_, _, _, err = e2.Index("d", map[string]any{"a": int64(5)})
	checkErr(t, err, mapping.ErrTypeConflict, "a")

	// object 字段拒数组。
	_, _, _, err = e2.Index("d", map[string]any{"a": []any{int64(1)}})
	checkErr(t, err, mapping.ErrTypeConflict, "a")

	// 空对象也建字段。
	e3 := mustEngine(t, mapping.True, 10)
	mustIndex(t, e3, "d", map[string]any{"o": map[string]any{}})
	if e3.Fields()["o"] != mapping.Object {
		t.Fatalf("空对象应建 object 字段，实际 %v", e3.Fields())
	}
}

// TestFieldLimit 超限恰等与大 1。
func TestFieldLimit(t *testing.T) {
	// 恰等 Fmax 成功，大 1 报字段超限。
	e := mustEngine(t, mapping.True, 2)
	_, _, _, err := e.Index("d", map[string]any{"a": int64(1), "b": int64(2)})
	if err != nil {
		t.Fatalf("恰等 Fmax 应成功: %v", err)
	}
	_, _, _, err = e.Index("d2", map[string]any{"c": int64(3)})
	checkErr(t, err, mapping.ErrFieldLimit, "c")

	// 嵌套路径上中间 object 也占名额。
	e2 := mustEngine(t, mapping.True, 2)
	_, _, _, err = e2.Index("d", map[string]any{"a": map[string]any{"b": map[string]any{"c": int64(1)}}})
	checkErr(t, err, mapping.ErrFieldLimit, "a.b.c")
	if len(e2.Fields()) != 0 {
		t.Fatalf("fields 应为空，实际 %v", e2.Fields())
	}
}

// TestRejectedLeavesNoFields 被拒文档不留下任何新字段，mv 不变。
func TestRejectedLeavesNoFields(t *testing.T) {
	e := mustEngine(t, mapping.True, 10)
	mustIndex(t, e, "ok", map[string]any{"keep": int64(1)})
	before := e.Fields()
	mv := e.MV()

	// 类型冲突：新字段 x、y 都不能留下。
	_, _, _, err := e.Index("bad", map[string]any{
		"keep": "not-a-long!",
		"x":    int64(1),
		"y":    map[string]any{"z": int64(2)},
	})
	checkErr(t, err, mapping.ErrTypeConflict, "keep")
	if !reflect.DeepEqual(e.Fields(), before) || e.MV() != mv {
		t.Fatalf("拒绝后映射不应变化: fields=%v mv=%d", e.Fields(), e.MV())
	}
	if _, err := e.Get("bad"); !errors.Is(err, mapping.ErrDocNotFound) {
		t.Fatalf("被拒文档不应存入: %v", err)
	}

	// 字段超限同样全有或全无。
	e2 := mustEngine(t, mapping.True, 1)
	_, _, _, err = e2.Index("bad", map[string]any{"a": int64(1), "b": int64(2)})
	checkErr(t, err, mapping.ErrFieldLimit, "b")
	if len(e2.Fields()) != 0 || e2.MV() != 0 {
		t.Fatalf("拒绝后应为空映射: fields=%v mv=%d", e2.Fields(), e2.MV())
	}
}

// TestPutMapping 显式映射的拒绝次序与空操作。
func TestPutMapping(t *testing.T) {
	e := mustEngine(t, mapping.True, 4)

	// 参数非法优先。
	if _, err := e.PutMapping("a", mapping.Type(99)); !errors.Is(err, mapping.ErrInvalidArgument) {
		t.Fatalf("非法类型应报参数非法: %v", err)
	}
	if _, err := e.PutMapping("", mapping.Long); !errors.Is(err, mapping.ErrInvalidArgument) {
		t.Fatalf("空路径应报参数非法: %v", err)
	}
	if _, err := e.PutMapping("a..b", mapping.Long); !errors.Is(err, mapping.ErrInvalidArgument) {
		t.Fatalf("空段应报参数非法: %v", err)
	}
	if _, err := e.PutMapping(strings.Repeat("k", 65), mapping.Long); !errors.Is(err, mapping.ErrInvalidArgument) {
		t.Fatalf("超长键应报参数非法: %v", err)
	}

	// 自动建父节点：a.b.c 一次建 3 个字段，mv=1。
	mv, err := e.PutMapping("a.b.c", mapping.Double)
	if err != nil || mv != 1 {
		t.Fatalf("PutMapping a.b.c: mv=%d err=%v", mv, err)
	}
	want := map[string]mapping.Type{"a": mapping.Object, "a.b": mapping.Object, "a.b.c": mapping.Double}
	if !reflect.DeepEqual(e.Fields(), want) {
		t.Fatalf("字段应为 %v，实际 %v", want, e.Fields())
	}

	// 同类型空操作，mv 不变。
	mv, err = e.PutMapping("a.b.c", mapping.Double)
	if err != nil || mv != 1 {
		t.Fatalf("空操作不应加 mv: mv=%d err=%v", mv, err)
	}

	// 已存在且类型不同报类型冲突。
	_, err = e.PutMapping("a.b.c", mapping.Long)
	checkErr(t, err, mapping.ErrTypeConflict, "a.b.c")

	// 父节点是叶子报类型冲突。
	e2 := mustEngine(t, mapping.True, 10)
	if _, err := e2.PutMapping("a", mapping.Long); err != nil {
		t.Fatal(err)
	}
	_, err = e2.PutMapping("a.b", mapping.Long)
	checkErr(t, err, mapping.ErrTypeConflict, "a")

	// 新建后将超 Fmax 报字段超限（e 已有 3 个，Fmax=4，a.x.y 需 2 个）。
	_, err = e.PutMapping("a.x.y", mapping.Long)
	checkErr(t, err, mapping.ErrFieldLimit, "a.x.y")
	// 恰等上限成功。
	mv, err = e.PutMapping("z", mapping.Keyword)
	if err != nil || mv != 2 {
		t.Fatalf("恰等上限应成功: mv=%d err=%v", mv, err)
	}
}

// TestValidation 参数非法在动任何映射之前整份查完。
func TestValidation(t *testing.T) {
	deep := map[string]any{}
	cur := deep
	for i := 0; i < 8; i++ {
		nxt := map[string]any{}
		cur["k"] = nxt
		cur = nxt
	}
	cur["k"] = int64(1) // 第 9 层

	cases := []struct {
		name string
		id   string
		doc  map[string]any
	}{
		{"空 id", "", map[string]any{"a": int64(1)}},
		{"超长 id", strings.Repeat("i", 513), map[string]any{"a": int64(1)}},
		{"空键", "d", map[string]any{"": int64(1)}},
		{"键含点", "d", map[string]any{"a.b": int64(1)}},
		{"键超长", "d", map[string]any{strings.Repeat("k", 65): int64(1)}},
		{"嵌套超 8 层", "d", deep},
		{"数组含对象", "d", map[string]any{"a": []any{map[string]any{}}}},
		{"数组含数组", "d", map[string]any{"a": []any{[]any{int64(1)}}}},
		{"非法值类型", "d", map[string]any{"a": int(1)}},
		{"嵌套键含点", "d", map[string]any{"a": map[string]any{"b.c": int64(1)}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := mustEngine(t, mapping.True, 10)
			_, _, _, err := e.Index(c.id, c.doc)
			if !errors.Is(err, mapping.ErrInvalidArgument) {
				t.Fatalf("应报参数非法: %v", err)
			}
			if len(e.Fields()) != 0 || e.MV() != 0 {
				t.Fatalf("参数非法不应动映射: fields=%v mv=%d", e.Fields(), e.MV())
			}
		})
	}

	// 嵌套 8 层合法。
	ok := map[string]any{}
	cur = ok
	for i := 0; i < 7; i++ {
		nxt := map[string]any{}
		cur["k"] = nxt
		cur = nxt
	}
	cur["k"] = int64(1)
	e := mustEngine(t, mapping.True, 20)
	if _, _, _, err := e.Index("d", ok); err != nil {
		t.Fatalf("8 层嵌套应合法: %v", err)
	}

	// 构造参数校验。
	if _, err := ingest.New(mapping.Dynamic(9), 10); !errors.Is(err, mapping.ErrInvalidArgument) {
		t.Fatalf("非法 dynamic: %v", err)
	}
	if _, err := ingest.New(mapping.True, 0); !errors.Is(err, mapping.ErrInvalidArgument) {
		t.Fatalf("fmax=0: %v", err)
	}
	if _, err := ingest.New(mapping.True, 100001); !errors.Is(err, mapping.ErrInvalidArgument) {
		t.Fatalf("fmax=100001: %v", err)
	}
}

// TestOverwriteAndGet 同 id 覆盖写，Get 返回副本。
func TestOverwriteAndGet(t *testing.T) {
	e := mustEngine(t, mapping.True, 10)
	mustIndex(t, e, "d", map[string]any{"a": int64(1)})
	norm, _, _ := mustIndex(t, e, "d", map[string]any{"a": int64(2), "b": "x"})
	got, err := e.Get("d")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, norm) {
		t.Fatalf("Get 应返回覆盖后的文档: %v vs %v", got, norm)
	}
	// 返回的是副本，篡改不影响存储。
	got["a"] = int64(999)
	got2, _ := e.Get("d")
	if got2["a"] != int64(2) {
		t.Fatalf("Get 应返回副本，实际 %v", got2)
	}
	if _, err := e.Get("nope"); !errors.Is(err, mapping.ErrDocNotFound) {
		t.Fatalf("不存在应报文档不存在: %v", err)
	}
}

// TestConcurrency 并发调用等价于某串行序：字段不超上限、已存文档合规。
func TestConcurrency(t *testing.T) {
	const fmax = 20
	e := mustEngine(t, mapping.True, fmax)
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				id := fmt.Sprintf("g%d-%d", g, i%5)
				doc := map[string]any{
					fmt.Sprintf("k%d", (g+i)%7): int64(g + i),
					"n":                         map[string]any{"v": int64(i)},
				}
				_, _, _, _ = e.Index(id, doc)
				_, _ = e.Get(id)
				_, _ = e.PutMapping(fmt.Sprintf("p%d", i%3), mapping.Keyword)
			}
		}(g)
	}
	wg.Wait()
	fields := e.Fields()
	if len(fields) > fmax {
		t.Fatalf("字段总数 %d 超过 Fmax %d", len(fields), fmax)
	}
}
