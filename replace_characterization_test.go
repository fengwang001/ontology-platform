package ontology

import (
	"math"
	"reflect"
	"testing"
)

// 本文件是 characterization tests：所有断言钉住当前实现的真实行为
// （包括与 selector.go 文档承诺不一致的行为），因此全部应当通过。
// 所有"同 ID 覆盖"形态都收敛到同一张表，用循环生成，不展开成多个测试函数。

type replaceCase struct {
	name        string
	dir         Direction
	k           int
	seed        []Element
	overwriteID string
	newScore    float64
	want        []Element
	wantSkipped int
}

func TestOverwriteCharacterization(t *testing.T) {
	negZero := math.Copysign(0, -1)

	cases := []replaceCase{
		// --- 非满容器：覆盖为更差分值（核心缺陷路径）---
		{
			name: "underfull/desc/overwrite-worse-drops",
			dir:  Desc, k: 4,
			seed:        []Element{{"a", 10}, {"b", 20}, {"c", 30}},
			overwriteID: "a", newScore: 1,
			want: []Element{{"c", 30}, {"b", 20}},
		},
		{
			name: "underfull/asc/overwrite-worse-drops",
			dir:  Asc, k: 4,
			seed:        []Element{{"a", 30}, {"b", 20}, {"c", 10}},
			overwriteID: "a", newScore: 40,
			want: []Element{{"c", 10}, {"b", 20}},
		},
		// --- 满容器对照：与既有 TestOverwriteWorseDropsOutOfTopK 一致 ---
		{
			name: "full/desc/overwrite-worse-drops",
			dir:  Desc, k: 3,
			seed:        []Element{{"a", 10}, {"b", 20}, {"c", 30}},
			overwriteID: "a", newScore: 1,
			want: []Element{{"c", 30}, {"b", 20}},
		},
		{
			name: "full/asc/overwrite-worse-drops",
			dir:  Asc, k: 3,
			seed:        []Element{{"a", 30}, {"b", 20}, {"c", 10}},
			overwriteID: "a", newScore: 40,
			want: []Element{{"c", 10}, {"b", 20}},
		},
		// --- 非满容器：覆盖为更优分值，正常保留 ---
		{
			name: "underfull/desc/overwrite-better-kept",
			dir:  Desc, k: 4,
			seed:        []Element{{"a", 10}, {"b", 20}, {"c", 30}},
			overwriteID: "a", newScore: 40,
			want: []Element{{"a", 40}, {"c", 30}, {"b", 20}},
		},
		{
			name: "underfull/asc/overwrite-better-kept",
			dir:  Asc, k: 4,
			seed:        []Element{{"a", 30}, {"b", 20}, {"c", 10}},
			overwriteID: "a", newScore: 5,
			want: []Element{{"a", 5}, {"c", 10}, {"b", 20}},
		},
		// --- 非满容器：覆盖为与旧值相同的分数，当前实现同样丢弃 ---
		{
			name: "underfull/desc/overwrite-same-drops",
			dir:  Desc, k: 4,
			seed:        []Element{{"a", 10}, {"b", 20}, {"c", 30}},
			overwriteID: "a", newScore: 10,
			want: []Element{{"c", 30}, {"b", 20}},
		},
		{
			name: "underfull/asc/overwrite-same-drops",
			dir:  Asc, k: 4,
			seed:        []Element{{"a", 30}, {"b", 20}, {"c", 10}},
			overwriteID: "a", newScore: 30,
			want: []Element{{"c", 10}, {"b", 20}},
		},
		// --- 非满容器：新分恰好等于截断阈值，按 ID 字典序决定去留 ---
		{
			name: "underfull/desc/tie-threshold-smaller-id-kept",
			dir:  Desc, k: 4,
			seed:        []Element{{"a", 10}, {"b", 20}, {"c", 30}},
			overwriteID: "a", newScore: 20,
			want: []Element{{"c", 30}, {"a", 20}, {"b", 20}},
		},
		{
			name: "underfull/desc/tie-threshold-larger-id-dropped",
			dir:  Desc, k: 4,
			seed:        []Element{{"z", 10}, {"b", 20}, {"c", 30}},
			overwriteID: "z", newScore: 20,
			want: []Element{{"c", 30}, {"b", 20}},
		},
		{
			name: "underfull/asc/tie-threshold-smaller-id-kept",
			dir:  Asc, k: 4,
			seed:        []Element{{"a", 30}, {"b", 20}, {"c", 10}},
			overwriteID: "a", newScore: 20,
			want: []Element{{"c", 10}, {"a", 20}, {"b", 20}},
		},
		{
			name: "underfull/asc/tie-threshold-larger-id-dropped",
			dir:  Asc, k: 4,
			seed:        []Element{{"z", 30}, {"b", 20}, {"c", 10}},
			overwriteID: "z", newScore: 20,
			want: []Element{{"c", 10}, {"b", 20}},
		},
		// --- 满容器并列对照：并列阈值时同样只留小 ID ---
		{
			name: "full/desc/tie-threshold-smaller-id-kept",
			dir:  Desc, k: 3,
			seed:        []Element{{"a", 10}, {"b", 20}, {"c", 30}},
			overwriteID: "a", newScore: 20,
			want: []Element{{"c", 30}, {"a", 20}, {"b", 20}},
		},
		{
			name: "full/asc/tie-threshold-smaller-id-kept",
			dir:  Asc, k: 3,
			seed:        []Element{{"a", 30}, {"b", 20}, {"c", 10}},
			overwriteID: "a", newScore: 20,
			want: []Element{{"c", 10}, {"a", 20}, {"b", 20}},
		},
		// --- NaN 覆盖既有 ID：整体拒绝，旧分数保留 ---
		{
			name: "underfull/nan-overwrite-keeps-old-score",
			dir:  Desc, k: 3,
			seed:        []Element{{"a", 9}, {"b", 4}},
			overwriteID: "a", newScore: math.NaN(),
			want: []Element{{"a", 9}, {"b", 4}}, wantSkipped: 1,
		},
		{
			name: "full/nan-overwrite-keeps-old-score",
			dir:  Desc, k: 2,
			seed:        []Element{{"a", 9}, {"b", 4}},
			overwriteID: "a", newScore: math.NaN(),
			want: []Element{{"a", 9}, {"b", 4}}, wantSkipped: 1,
		},
	}

	// 覆盖形态循环 1：±0.0 在覆盖路径上判等——与阈值 0 并列时按 ID 决定去留。
	for _, dir := range []Direction{Desc, Asc} {
		dirName := map[Direction]string{Desc: "desc", Asc: "asc"}[dir]
		for _, sign := range []struct {
			tag string
			val float64
		}{{"poszero", 0}, {"negzero", negZero}} {
			// Desc 下其他人分数更低；Asc 下其他人分数更高；阈值都是 b=0。
			seed := []Element{{"a", 5}, {"b", 0}}
			other := 5.0
			if dir == Asc {
				seed = []Element{{"a", -5}, {"b", 0}}
				other = -5
			}
			cases = append(cases, replaceCase{
				name: "underfull/" + dirName + "/overwrite-to-" + sign.tag + "-tie-kept",
				dir:  dir, k: 3,
				seed:        seed,
				overwriteID: "a", newScore: sign.val,
				want: []Element{{"a", sign.val}, {"b", 0}},
			})
			// 同并列但被覆盖 ID 字典序更大：当前实现丢弃（空位也不补）。
			bigSeed := []Element{{"z", other}, {"b", 0}}
			cases = append(cases, replaceCase{
				name: "underfull/" + dirName + "/overwrite-to-" + sign.tag + "-tie-larger-id-dropped",
				dir:  dir, k: 3,
				seed:        bigSeed,
				overwriteID: "z", newScore: sign.val,
				want: []Element{{"b", 0}},
			})
		}
	}

	// 覆盖形态循环 2：非满容器内既有元素个数 1..3。
	// replace 对 len==1 有特判（无条件保留），len>=2 才走阈值截断，
	// 同一条"覆盖为更差分值"语义随持有数出现不连续行为。
	for _, dir := range []Direction{Desc, Asc} {
		dirName := map[Direction]string{Desc: "desc", Asc: "asc"}[dir]
		for n := 1; n <= 3; n++ {
			seed := []Element{{"a", 10.0}}
			for i := 1; i < n; i++ {
				id := string(rune('a' + i))
				var score float64
				if dir == Asc {
					score = float64(10 - i*5) // Asc: 其他人更好（b5,c1）
				} else {
					score = float64(10 * (i + 1)) // Desc: 其他人更好（b20,c30）
				}
				seed = append(seed, Element{id, score})
			}

			worse := 1.0 // Desc: a 降到最差
			if dir == Asc {
				worse = 20 // Asc: a 升到最差
			}

			var want []Element
			if n >= 2 {
				// 实际行为：a 被阈值截断丢弃；期望快照 = 仅含其余元素时的排序。
				ref, _ := New(4, dir)
				pushAll(ref, seed[1:])
				want = ref.Snapshot()
			} else {
				want = []Element{{"a", worse}}
			}
			cases = append(cases, replaceCase{
				name: "underfull/" + dirName + "/occupants-" + string(rune('0'+n)) + "-worse-overwrite",
				dir:  dir, k: 4,
				seed:        seed,
				overwriteID: "a",
				newScore:    worse,
				want:        want,
			})
		}
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sel, err := New(tc.k, tc.dir)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			pushAll(sel, tc.seed)
			sel.Push(tc.overwriteID, tc.newScore)

			got := sel.Snapshot()
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Snapshot: got %+v want %+v", got, tc.want)
			}
			if sel.Len() != len(tc.want) {
				t.Fatalf("Len: got %d want %d", sel.Len(), len(tc.want))
			}
			if sel.Skipped() != tc.wantSkipped {
				t.Fatalf("Skipped: got %d want %d", sel.Skipped(), tc.wantSkipped)
			}
		})
	}
}
