package ontology

import (
	"math"
	"reflect"
	"testing"
)

// overwriteCharacterizationCase 用一组有序 Push 序列描述一次覆盖场景，
// 并钉住当前实现产出的真实 Snapshot / Len / Skipped。
type overwriteCharacterizationCase struct {
	name    string
	k       int
	dir     Direction
	pushes  []Element
	want    []Element
	skipped int
}

// negZero 是带负号的 -0.0，用于钉住符号零覆盖后的存储值。
func negZero() float64 { return math.Copysign(0, -1) }

// 下列断言全部是对【当前真实行为】的刻画（characterization），
// 不是期望的正确语义：非满容器下覆盖为更差分数、甚至并列分数，
// 该 ID 都会被立即误删。详见 FINDINGS.md。
func TestOverwriteCharacterization(t *testing.T) {
	cases := []overwriteCharacterizationCase{
		// --- 非满容器：覆盖为更差分值，当前实现会误删该 ID ---
		{
			name:   "underfull_desc_worse_drops",
			k:      3,
			dir:    Desc,
			pushes: []Element{{"a", 100}, {"b", 50}, {"a", 1}},
			want:   []Element{{"b", 50}},
		},
		{
			name:   "underfull_asc_worse_drops",
			k:      3,
			dir:    Asc,
			pushes: []Element{{"a", 1}, {"b", 50}, {"a", 100}},
			want:   []Element{{"b", 50}},
		},
		{
			// 覆盖后成为集合中新的“最差”但容器未满，同样被误删。
			name:   "underfull_desc_becomes_new_worst_drops",
			k:      4,
			dir:    Desc,
			pushes: []Element{{"a", 100}, {"b", 10}, {"c", 20}, {"a", 5}},
			want:   []Element{{"c", 20}, {"b", 10}},
		},
		{
			name:   "underfull_asc_becomes_new_worst_drops",
			k:      4,
			dir:    Asc,
			pushes: []Element{{"a", 1}, {"b", 10}, {"c", 20}, {"a", 30}},
			want:   []Element{{"b", 10}, {"c", 20}},
		},
		{
			// 非满容器里只剩一个元素时 replace 有专门分支：无条件保留，
			// 与多元素非满路径的“误删”行为自相矛盾。
			name:   "underfull_single_held_worse_is_kept",
			k:      3,
			dir:    Desc,
			pushes: []Element{{"a", 5}, {"a", 1}},
			want:   []Element{{"a", 1}},
		},

		// --- 满容器：覆盖为更差分值掉出，现有已验证行为（对照组） ---
		{
			name:   "full_desc_worse_drops",
			k:      2,
			dir:    Desc,
			pushes: []Element{{"a", 100}, {"b", 50}, {"a", 1}},
			want:   []Element{{"b", 50}},
		},
		{
			name:   "full_asc_worse_drops",
			k:      2,
			dir:    Asc,
			pushes: []Element{{"a", 1}, {"b", 50}, {"a", 100}},
			want:   []Element{{"b", 50}},
		},

		// --- 非满容器：覆盖为更优 / 相同分数，正常更新并保留 ---
		{
			name:   "underfull_desc_better_kept",
			k:      3,
			dir:    Desc,
			pushes: []Element{{"a", 1}, {"b", 50}, {"a", 100}},
			want:   []Element{{"a", 100}, {"b", 50}},
		},
		{
			name:   "underfull_asc_better_kept",
			k:      3,
			dir:    Asc,
			pushes: []Element{{"a", 50}, {"b", 100}, {"a", 1}},
			want:   []Element{{"a", 1}, {"b", 100}},
		},
		{
			name:   "underfull_desc_equal_score_kept",
			k:      3,
			dir:    Desc,
			pushes: []Element{{"a", 100}, {"b", 50}, {"a", 100}},
			want:   []Element{{"a", 100}, {"b", 50}},
		},
		{
			name:   "underfull_asc_equal_score_kept",
			k:      3,
			dir:    Asc,
			pushes: []Element{{"a", 1}, {"b", 50}, {"a", 1}},
			want:   []Element{{"a", 1}, {"b", 50}},
		},

		// --- 非满误删后，该 ID 被当作新元素：以好分数可重新入选 ---
		{
			name:   "underfull_dropped_id_can_reenter",
			k:      3,
			dir:    Desc,
			pushes: []Element{{"a", 100}, {"b", 50}, {"a", 1}, {"a", 200}},
			want:   []Element{{"a", 200}, {"b", 50}},
		},

		// --- +0.0 / -0.0 判等：非满覆盖走并列误删；单元素分支保留并钉住符号 ---
		{
			name:   "signed_zero_overwrite_underfull_drops",
			k:      3,
			dir:    Desc,
			pushes: []Element{{"a", negZero()}, {"b", 5}, {"a", 0}},
			want:   []Element{{"b", 5}},
		},
		{
			name:   "signed_zero_single_held_preserves_sign",
			k:      3,
			dir:    Asc,
			pushes: []Element{{"a", 0}, {"a", negZero()}},
			want:   []Element{{"a", negZero()}},
		},

		// --- NaN 覆盖既有 ID：整体拒绝，旧分数保留，跳过计数 +1 ---
		{
			name:    "nan_overwrite_underfull_keeps_old_score",
			k:       3,
			dir:     Desc,
			pushes:  []Element{{"a", 9}, {"b", 4}, {"a", math.NaN()}},
			want:    []Element{{"a", 9}, {"b", 4}},
			skipped: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			sel, err := New(tc.k, tc.dir)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			pushAll(sel, tc.pushes)
			if got := sel.Len(); got != len(tc.want) {
				t.Fatalf("Len = %d, want %d; snapshot = %+v", got, len(tc.want), sel.Snapshot())
			}
			if got := sel.Snapshot(); !elementsEqual(got, tc.want) {
				t.Fatalf("Snapshot = %+v, want %+v", got, tc.want)
			}
			if got := sel.Skipped(); got != tc.skipped {
				t.Fatalf("Skipped = %d, want %d", got, tc.skipped)
			}
		})
	}
}

// elementsEqual 比较 Snapshot：ID 相同且分数严格相等，
// 用 reflect.DeepEqual 区分 +0.0 与 -0.0（NaNs 不应出现在结果中）。
func elementsEqual(got, want []Element) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if !reflect.DeepEqual(got[i], want[i]) {
			return false
		}
	}
	return true
}

// 覆盖后的新分数恰好落在截断阈值上（与剩余集合的最差元素同分）：
// replace 用 rankLess（同分比 ID 升序）决定去留，比较方向与
// “满容器淘汰更差者”所需的方向相反；非满时再叠加误删。
// 用循环遍历 {Desc,Asc} × {满,非满} × {覆盖 ID 字典序更大,更小}。
func TestOverwriteAtThresholdTieCharacterization(t *testing.T) {
	type tieCase struct {
		name   string
		k      int
		dir    Direction
		pushes []Element
		want   []Element
	}
	var tcs []tieCase
	for _, dir := range []Direction{Desc, Asc} {
		dirName := "desc"
		best, threshold := 9.0, 5.0
		if dir == Asc {
			dirName = "asc"
			best, threshold = 1.0, 5.0
		}
		for _, fill := range []struct {
			name string
			k    int
		}{
			{"full", 2},      // 覆盖前恰好填满 k=2
			{"underfull", 4}, // 覆盖前在榜 3 个，仍空一个名额
		} {
			for _, ov := range []struct {
				name string
				id   string
			}{
				{"larger_id", "zzz"},
				{"smaller_id", "aa"},
			} {
				pushes := []Element{{"a", threshold}, {ov.id, best}}
				if fill.name == "underfull" {
					pushes = append(pushes, Element{"b", threshold})
				}
				pushes = append(pushes, Element{ov.id, threshold})
				tcs = append(tcs, tieCase{
					name:   dirName + "_" + fill.name + "_overwrite_" + ov.name,
					k:      fill.k,
					dir:    dir,
					pushes: pushes,
					want:   tieSnapshotFor(ov.id, fill.name),
				})
			}
		}
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			sel, err := New(tc.k, tc.dir)
			if err != nil {
				t.Fatalf("New: %v", err)
			}
			pushAll(sel, tc.pushes)
			if got := sel.Len(); got != len(tc.want) {
				t.Fatalf("Len = %d, want %d; snapshot = %+v", got, len(tc.want), sel.Snapshot())
			}
			if got := sel.Snapshot(); !elementsEqual(got, tc.want) {
				t.Fatalf("Snapshot = %+v, want %+v", got, tc.want)
			}
		})
	}
}

// tieSnapshotFor 钉住阈值并列覆盖时各形态的真实结果（与方向无关）。
// 决定去留的不是覆盖 ID 与集合中“其他 ID”的相对大小，而是它与
// 阈值元素 a 的 ID 比较：rankLess 同分比 ID 升序，ID 更小才保留。
//   - zzz > a：满/非满都掉出；非满还白丢一个名额（剩 a、b）。
//   - aa < a：满/非满都保留；Snapshot 按 ID 升序，aa 排在 a 前。
func tieSnapshotFor(overwriteID, fill string) []Element {
	switch {
	case overwriteID == "zzz" && fill == "full":
		return []Element{{"a", 5}}
	case overwriteID == "zzz" && fill == "underfull":
		return []Element{{"a", 5}, {"b", 5}}
	case overwriteID == "aa" && fill == "full":
		return []Element{{"a", 5}}
	default: // aa, underfull
		return []Element{{"a", 5}, {"aa", 5}, {"b", 5}}
	}
}
