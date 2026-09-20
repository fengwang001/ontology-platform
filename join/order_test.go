package join_test

import (
	"fmt"
	"reflect"
	"testing"

	"ontology/join"
)

// scramble 用确定性的伪随机置换打乱行切片（不依赖输入顺序之外的种子）。
func scramble(rows []join.Row, seed uint64) []join.Row {
	out := make([]join.Row, len(rows))
	perm := make([]int, len(rows))
	for i := range perm {
		perm[i] = i
	}
	s := seed
	for i := len(perm) - 1; i > 0; i-- {
		s = s*6364136223846793005 + 1442695040888963407
		j := int(s>>33) % (i + 1)
		perm[i], perm[j] = perm[j], perm[i]
	}
	for i, p := range perm {
		out[i] = rows[p]
	}
	return out
}

func buildTables() (left, right []join.Row) {
	for i := 0; i < 60; i++ {
		left = append(left, join.Row{
			"g":  fmt.Sprintf("g%02d", i%7),
			"n":  int64(i % 3),
			"lv": fmt.Sprintf("L%02d", i),
		})
	}
	for j := 0; j < 40; j++ {
		right = append(right, join.Row{
			"g":  fmt.Sprintf("g%02d", j%7),
			"n":  int64(j % 3),
			"rv": fmt.Sprintf("R%02d", j),
		})
	}
	return left, right
}

func TestShuffleDeterministic(t *testing.T) {
	left, right := buildTables()
	base, err := join.Join(left, right, []string{"g", "n"}, join.Inner)
	if err != nil {
		t.Fatal(err)
	}
	for seed := uint64(1); seed <= 5; seed++ {
		res, err := join.Join(scramble(left, seed), scramble(right, seed*7),
			[]string{"g", "n"}, join.Inner)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(base.Rows, res.Rows) {
			t.Fatalf("seed %d: shuffled join differs", seed)
		}
		if base.Stats != res.Stats {
			t.Fatalf("seed %d: stats differ", seed)
		}
	}
}

func TestShuffleDeterministicLeftMode(t *testing.T) {
	left := []join.Row{
		{"id": int64(1), "lv": "a"},
		{"id": nil, "lv": "b"},
		{"id": int64(9), "lv": "c"},
		{"lv": "d"},
	}
	right := []join.Row{{"id": int64(1), "rv": "x"}}
	base, err := join.Join(left, right, []string{"id"}, join.Left)
	if err != nil {
		t.Fatal(err)
	}
	res, err := join.Join(scramble(left, 42), scramble(right, 7),
		[]string{"id"}, join.Left)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(base.Rows, res.Rows) {
		t.Fatalf("left mode shuffle differs:\n%v\n%v", base.Rows, res.Rows)
	}
}

func TestOrderByKeyThenRowID(t *testing.T) {
	// 键升序；同键内按内容派生的行标识升序，与输入下标无关。
	left := []join.Row{
		{"id": int64(2), "lv": "b"},
		{"id": int64(1), "lv": "z"},
		{"id": int64(1), "lv": "a"},
	}
	right := []join.Row{
		{"id": int64(1), "rv": "r2"},
		{"id": int64(2), "rv": "r1"},
		{"id": int64(1), "rv": "r1"},
	}
	res, err := join.Join(left, right, []string{"id"}, join.Inner)
	if err != nil {
		t.Fatal(err)
	}
	type pair struct{ lv, rv string }
	var got []pair
	for _, r := range res.Rows {
		got = append(got, pair{r["lv"].(string), r["rv"].(string)})
	}
	// 左行标识：{id:1,lv:a} < {id:1,lv:z} < {id:2,lv:b}（属性名升序、值编码字典序）
	// 右行标识：{id:1,rv:r1} < {id:1,rv:r2} < {id:2,rv:r1}
	want := []pair{
		{"a", "r1"}, {"a", "r2"},
		{"z", "r1"}, {"z", "r2"},
		{"b", "r1"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("order:\n got %v\nwant %v", got, want)
	}
}

func TestLeftUnmatchedAppendedAfterMatched(t *testing.T) {
	left := []join.Row{
		{"id": int64(5), "lv": "unmatched"},
		{"id": int64(1), "lv": "matched"},
		{"id": nil, "lv": "null-key"},
	}
	right := []join.Row{{"id": int64(1), "rv": "x"}}
	res, err := join.Join(left, right, []string{"id"}, join.Left)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 3 {
		t.Fatalf("want 3 rows, got %v", res.Rows)
	}
	if res.Rows[0]["lv"] != "matched" {
		t.Fatalf("matched rows must come first: %v", res.Rows)
	}
	// 未匹配行排在匹配行之后，且相对顺序与输入顺序无关。
	again, err := join.Join(scramble(left, 3), right, []string{"id"}, join.Left)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Rows, again.Rows) {
		t.Fatal("unmatched tail order must be deterministic")
	}
}
