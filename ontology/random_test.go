package ontology

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

var rndKeys = [][]byte{
	[]byte("a"), []byte("b"), []byte("c"), []byte("m"),
	[]byte("n"), []byte("x"), []byte("y"), []byte("z"),
}

func pu64(v uint64) *uint64 { return &v }

func rndBounds(r *rand.Rand) ([]byte, []byte) {
	i, j := r.Intn(len(rndKeys)), r.Intn(len(rndKeys))
	if i > j {
		i, j = j, i
	}
	return append([]byte(nil), rndKeys[i]...), append([]byte(nil), rndKeys[j]...)
}

func existingFiles(m *naiveModel) []File {
	files := m.snapshotFiles()
	var out []File
	for level := range files {
		out = append(out, files[level]...)
	}
	return out
}

// genEdit 生成一条编辑。大多数分支构造“可能成功”的输入，少量分支
// 专门制造各类校验失败。
func genEdit(r *rand.Rand, m *naiveModel) Edit {
	var e Edit
	switch r.Intn(14) {
	case 0:
		return e // 空编辑 -> ErrParam
	case 1:
		switch r.Intn(4) { // 非法文件 -> ErrParam
		case 0:
			e.Adds = []File{{Level: 7, Num: m.next, Smallest: []byte("a"), Largest: []byte("b")}}
		case 1:
			e.Adds = []File{{Level: 0, Num: 0, Smallest: []byte("a"), Largest: []byte("b")}}
		case 2:
			e.Adds = []File{{Level: 0, Num: m.next, Smallest: []byte{}, Largest: []byte("b")}}
		default:
			e.Adds = []File{{Level: 0, Num: m.next, Smallest: []byte("z"), Largest: []byte("a")}}
		}
		return e
	case 2:
		// 删除不存在的文件 -> ErrNoFile
		e.Dels = []File{{Level: r.Intn(7), Num: m.next + 50}}
		return e
	case 3:
		// 重复编号：与现存文件同号 -> ErrDupFile
		if have := existingFiles(m); len(have) > 0 {
			f := have[r.Intn(len(have))]
			s, l := rndBounds(r)
			e.Adds = []File{{Level: r.Intn(7), Num: f.Num, Smallest: s, Largest: l}}
			return e
		}
	case 4:
		// 本编辑内两个 Add 同号 -> ErrDupFile
		s1, l1 := rndBounds(r)
		s2, l2 := rndBounds(r)
		num := m.next
		e.Adds = []File{
			{Level: 0, Num: num, Smallest: s1, Largest: l1},
			{Level: 1, Num: num, Smallest: s2, Largest: l2},
		}
		return e
	case 5:
		// NextFile 回退 -> ErrRegress（当 m.next > 2 时）
		if m.next > 2 {
			e.NextFile = pu64(m.next - 1)
			e.LastSeq = pu64(m.seq)
			return e
		}
	case 6:
		// LogNumber 恰等于有效 NextFile -> ErrLogAhead
		num := m.next
		s, l := rndBounds(r)
		e.Adds = []File{{Level: 0, Num: num, Smallest: s, Largest: l}}
		e.LogNumber = pu64(num + 1) // 有效 NextFile = num+1
		return e
	case 7:
		// 删后同号再加：先删后加，合法（编号被释放）。
		if have := existingFiles(m); len(have) > 0 {
			f := have[r.Intn(len(have))]
			e.Dels = []File{{Level: f.Level, Num: f.Num}}
			s, l := rndBounds(r)
			e.Adds = []File{{Level: f.Level, Num: f.Num, Smallest: s, Largest: l}}
			return e
		}
	case 8:
		// 仅指针编辑（可能触发回退/LogAhead，也可能成功）。
		e.NextFile = pu64(m.next + uint64(r.Intn(3)))
		if r.Intn(2) == 0 {
			e.LogNumber = pu64(m.logNo + uint64(r.Intn(2)))
		}
		if r.Intn(2) == 0 {
			e.LastSeq = pu64(m.seq + uint64(r.Intn(3)))
		}
		return e
	}

	// 默认：加入一个大概率成功的新文件，NextFile 静默抬高；
	// 层与区间随机（可能造成重叠而被拒，两侧都会被拒，是好的对照）。
	num := m.next + uint64(r.Intn(4))
	level := r.Intn(7)
	s, l := rndBounds(r)
	e.Adds = []File{{Level: level, Num: num, Smallest: s, Largest: l}}
	if r.Intn(3) == 0 {
		// 同时给一个偏大的 NextFile，验证静默 max 语义。
		e.NextFile = pu64(num + 1 + uint64(r.Intn(5)))
	}
	if r.Intn(3) == 0 && m.logNo < num {
		e.LogNumber = pu64(m.logNo)
	}
	if r.Intn(4) == 0 {
		e.LastSeq = pu64(m.seq + 1)
	}
	return e
}

func sameSentinel(a, b error) bool {
	if a == nil || b == nil {
		return a == b
	}
	return errors.Is(a, b) && errors.Is(b, a)
}

// describeEdit 打印编辑内容，用于失败诊断日志。
func describeEdit(e Edit) string {
	s := "Edit{"
	for _, f := range e.Adds {
		s += fmt.Sprintf(" Add(L%d,#%d,%q..%q)", f.Level, f.Num, f.Smallest, f.Largest)
	}
	for _, f := range e.Dels {
		s += fmt.Sprintf(" Del(L%d,#%d)", f.Level, f.Num)
	}
	if e.LogNumber != nil {
		s += fmt.Sprintf(" Log=%d", *e.LogNumber)
	}
	if e.NextFile != nil {
		s += fmt.Sprintf(" Next=%d", *e.NextFile)
	}
	if e.LastSeq != nil {
		s += fmt.Sprintf(" Seq=%d", *e.LastSeq)
	}
	return s + " }"
}

func describeVersion(v Version) string {
	s := fmt.Sprintf("Ver{log=%d next=%d seq=%d files=[", v.LogNumber, v.NextFile, v.LastSeq)
	for level := range v.Files {
		for _, f := range v.Files[level] {
			s += fmt.Sprintf("L%d#%d(%q..%q) ", level, f.Num, f.Smallest, f.Largest)
		}
	}
	return s + "]}"
}

// TestRandomAgainstNaiveModel 以 2000 组随机编辑序列，把真实 Manager 与
// 朴素模型逐步对照：错误、View、磁盘结构，以及 Recover(Disk()) 与 View()。
func TestRandomAgainstNaiveModel(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	const sequences = 2000
	totalSteps := 0
	for seq := 0; seq < sequences; seq++ {
		threshold := 1 + rng.Intn(5)
		mgr, err := New(threshold)
		if err != nil {
			t.Fatalf("New(%d): %v", threshold, err)
		}
		model := newNaiveModel(threshold)
		steps := 5 + rng.Intn(35)
		for step := 0; step < steps; step++ {
			totalSteps++

			// 小概率触发手动 Rotate / RotateUnflipped。
			if rng.Intn(12) == 0 {
				var got uint64
				if rng.Intn(2) == 0 {
					got = mgr.Rotate()
					want := model.rotate()
					if got != want {
						t.Fatalf("seq=%d step=%d Rotate num got=%d want=%d", seq, step, got, want)
					}
				} else {
					got = mgr.RotateUnflipped()
					want := model.rotateUnflipped()
					if got != want {
						t.Fatalf("seq=%d step=%d RotateUnflipped num got=%d want=%d", seq, step, got, want)
					}
				}
			}

			e := genEdit(rng, model)
			gotErr := mgr.Apply(e)
			wantErr := model.apply(e)
			t.Logf("seq=%d step=%d T=%d %s -> err=%v (want=%v); 判定: %s",
				seq, step, threshold, describeEdit(e), gotErr, wantErr,
				func() string {
					if gotErr == nil {
						return "接受：先删后加、NextFile=max 后追加记录，超阈值则原子轮转"
					}
					return "拒绝：首条成立错误优先，版本与清单均不变"
				}())

			if !sameSentinel(gotErr, wantErr) {
				t.Fatalf("seq=%d step=%d 错误不一致: got=%v want=%v\n输入: %s",
					seq, step, gotErr, wantErr, describeEdit(e))
			}

			gotView := mgr.View()
			wantView := model.view()
			if !reflect.DeepEqual(gotView, wantView) {
				t.Fatalf("seq=%d step=%d View 不一致:\n got=%s\nwant=%s",
					seq, step, describeVersion(gotView), describeVersion(wantView))
			}

			// 真实磁盘与模型磁盘应完全一致。
			gotDisk := mgr.Disk()
			wantDisk := model.disk()
			if !reflect.DeepEqual(gotDisk, wantDisk) {
				t.Fatalf("seq=%d step=%d Disk 不一致:\n got=%+v\nwant=%+v",
					seq, step, gotDisk, wantDisk)
			}

			// 崩溃恢复结果必须等于活动版本（无撕裂记录时）。
			recView, recErr := Recover(mgr.Disk())
			if recErr != nil {
				t.Fatalf("seq=%d step=%d Recover 意外失败: %v", seq, step, recErr)
			}
			if !reflect.DeepEqual(recView, gotView) {
				t.Fatalf("seq=%d step=%d Recover 与 View 不一致:\nrec=%s\nview=%s",
					seq, step, describeVersion(recView), describeVersion(gotView))
			}

			// 朴素恢复也必须得到同一版本。
			modelRec, mErr := naiveRecover(model.disk())
			if mErr != nil || !reflect.DeepEqual(modelRec, gotView) {
				t.Fatalf("seq=%d step=%d 朴素 Recover 不一致: %v\nrec=%s",
					seq, step, mErr, describeVersion(modelRec))
			}

			// 重叠探针上限：任何被接受且含 1..6 层 Add 的编辑，
			// overlapProbes <= 2*|Adds|。
			if gotErr == nil {
				nAdds := 0
				for _, f := range e.Adds {
					if f.Level > 0 {
						nAdds++
					}
				}
				if nAdds > 0 && mgr.overlapProbes() > 2*len(e.Adds) {
					t.Fatalf("seq=%d step=%d overlapProbes=%d 超过 2*|Adds|=%d",
						seq, step, mgr.overlapProbes(), 2*len(e.Adds))
				}
			}
		}
	}
	t.Logf("随机对照完成：%d 组序列，共 %d 步", sequences, totalSteps)
}
