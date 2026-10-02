package acl

import (
	"reflect"
	"testing"
)

// 继承变换矩阵：构造 /p/c（容器）与 /p/o（对象），在 p 上放单条指定标志的条目，
// 检查 c 与 o 上继承到的副本标志。
func TestTransformMatrix(t *testing.T) {
	cases := []struct {
		name    string
		flags   uint8
		wantCtr []uint8 // 容器子节点上副本的标志（空表示不产生副本）
		wantObj []uint8 // 对象子节点上副本的标志
	}{
		{"no flags", 0, nil, nil},
		{"OI only", FlagOI, []uint8{FlagOI | FlagIO}, []uint8{0}},
		{"CI only", FlagCI, []uint8{FlagCI}, nil},
		{"OI+CI", FlagOI | FlagCI, []uint8{FlagOI | FlagCI}, []uint8{0}},
		{"OI+NP", FlagOI | FlagNP, nil, []uint8{0}},
		{"CI+NP", FlagCI | FlagNP, []uint8{0}, nil},
		{"OI+CI+NP", FlagOI | FlagCI | FlagNP, []uint8{0}, []uint8{0}},
		{"OI+IO", FlagOI | FlagIO, []uint8{FlagOI | FlagIO}, []uint8{0}},
		{"CI+IO", FlagCI | FlagIO, []uint8{FlagCI}, nil},
		{"OI+CI+IO", FlagOI | FlagCI | FlagIO, []uint8{FlagOI | FlagCI}, []uint8{0}},
		{"OI+CI+NP+IO", FlagOI | FlagCI | FlagNP | FlagIO, []uint8{0}, []uint8{0}},
		{"OI+NP+IO", FlagOI | FlagNP | FlagIO, nil, []uint8{0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := mustStore(t, 4, 8)
			mustAdd(t, s, "p", "/", true)
			mustAdd(t, s, "c", "p", true)
			mustAdd(t, s, "o", "p", false)
			mustSetACL(t, s, "p", []ACE{
				{Allow: true, Principal: "u", Mask: 1, Flags: tc.flags},
			}, false)
			effC := mustEffective(t, s, "c")
			var gotCtr []uint8
			for _, e := range effC {
				if e.Source == "p" {
					gotCtr = append(gotCtr, e.Flags)
				}
			}
			if !reflect.DeepEqual(gotCtr, tc.wantCtr) {
				t.Fatalf("container copies = %v, want %v", gotCtr, tc.wantCtr)
			}
			effO := mustEffective(t, s, "o")
			var gotObj []uint8
			for _, e := range effO {
				if e.Source == "p" {
					gotObj = append(gotObj, e.Flags)
				}
			}
			if !reflect.DeepEqual(gotObj, tc.wantObj) {
				t.Fatalf("object copies = %v, want %v", gotObj, tc.wantObj)
			}
		})
	}
}

// OI 条目对容器子节点只给 OI|IO 副本，且该副本不再向更深层传播（IO 变 0 需 OI 再次携带）。
func TestOIContainerCopyBecomesInheritOnly(t *testing.T) {
	s := mustStore(t, 6, 8)
	mustAdd(t, s, "a", "/", true)
	mustAdd(t, s, "b", "a", true)
	mustAdd(t, s, "c", "b", true)
	mustSetACL(t, s, "/", []ACE{
		{Allow: true, Principal: "u", Mask: 1, Flags: FlagOI},
	}, false)
	// E(a)：OI|IO 副本；E(b)：副本带 OI → 再变 OI|IO；但 a 上的副本不带 CI，b 是容器：
	// f=OI|IO → 无 CI 有 OI 无 NP → 产生 OI|IO 副本。
	// 注意：副本在 a 上带 IO，不参与 a 的判定，但继续向下。
	effA := mustEffective(t, s, "a")
	if len(effA) != 1 || effA[0].Flags != FlagOI|FlagIO {
		t.Fatalf("E(a) = %+v", effA)
	}
	effB := mustEffective(t, s, "b")
	if len(effB) != 1 || effB[0].Flags != FlagOI|FlagIO {
		t.Fatalf("E(b) = %+v", effB)
	}
	// a 与 b 上都因 IO 被跳过。
	checkResult(t, mustEval(t, s, []string{"u"}, "a", 1), ResultImplicitDeny, -1, "", false, 0)
	checkResult(t, mustEval(t, s, []string{"u"}, "b", 1), ResultImplicitDeny, -1, "", false, 0)
}

// NP 使 CI 副本标志归零，归零后不再向下传播。
func TestNPStopsAfterOneLevel(t *testing.T) {
	s := mustStore(t, 6, 8)
	mustAdd(t, s, "a", "/", true)
	mustAdd(t, s, "b", "a", true)
	mustSetACL(t, s, "/", []ACE{
		{Allow: true, Principal: "u", Mask: 1, Flags: FlagCI | FlagNP},
	}, false)
	effA := mustEffective(t, s, "a")
	if len(effA) != 1 || effA[0].Flags != 0 {
		t.Fatalf("E(a) = %+v, want single flags=0 copy", effA)
	}
	checkResult(t, mustEval(t, s, []string{"u"}, "a", 1), ResultGranted, 0, "/", true, 1)
	// 副本标志为 0，不再传播到 b。
	if effB := mustEffective(t, s, "b"); len(effB) != 0 {
		t.Fatalf("E(b) = %+v, want empty", effB)
	}
}
