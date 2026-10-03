package ontology

import (
	"errors"
	"math/rand"
	"reflect"
	"testing"
)

type addCall struct {
	id   string
	refs []string
	irt  string
	subj string
	ts   int64
}

func errKind(err error) string {
	switch {
	case errors.Is(err, ErrInvalidArgument):
		return "invalid"
	case errors.Is(err, ErrDuplicate):
		return "duplicate"
	case errors.Is(err, ErrCapacity):
		return "capacity"
	case err == nil:
		return ""
	default:
		return "other"
	}
}

// TestDifferentialAgainstNaive 2000 组随机登记序列与朴素模拟逐步对拍，
// 日志打印输入、输出与判定依据（-v 可见）。
func TestDifferentialAgainstNaive(t *testing.T) {
	const groups = 2000
	rng := rand.New(rand.NewSource(20261003))

	subjects := []string{
		"Hello", "hello", "RE: Hello", "Re: Re: Hello", "Fwd: Hello",
		"回复：你好", "回复: 回复: 你好", "fwd:  plan", "Newsletter",
		"Re: ", "  re:x", "FW: Hello",
	}

	for g := 0; g < groups; g++ {
		w := int64(rng.Intn(30))
		n := 1 + rng.Intn(12)
		m, _ := New(w, n)
		na := newNaive(w, n)

		steps := 6 + rng.Intn(20)
		// 已知节点池，便于生成引用与占位。
		pool := []string{}
		for step := 0; step < steps; step++ {
			var call addCall
			if rng.Intn(6) == 0 {
				// 偶发非法 id / ts，验证错误类别一致。
				if rng.Intn(2) == 0 {
					call = addCall{id: "", ts: 1}
				} else {
					call = addCall{id: "bad-ts", ts: -1}
				}
			} else {
				// 生成 id：大部分新 id，小概率复用池中 id（制造重复登记或占位补登记）。
				if len(pool) > 0 && rng.Intn(3) == 0 {
					call.id = pool[rng.Intn(len(pool))]
				} else {
					call.id = genID(rng, g, step)
					pool = append(pool, call.id)
				}
				call.subj = subjects[rng.Intn(len(subjects))]
				call.ts = int64(rng.Intn(60))
				// 引用：从 pool（含未来可能只是占位的名字）挑 0~3 个。
				k := rng.Intn(4)
				for i := 0; i < k; i++ {
					var ref string
					if len(pool) > 0 && rng.Intn(2) == 0 {
						ref = pool[rng.Intn(len(pool))]
					} else {
						ref = genID(rng, g, step) + "-p"
					}
					call.refs = append(call.refs, ref)
				}
				if rng.Intn(4) == 0 && len(pool) > 0 {
					call.irt = pool[rng.Intn(len(pool))]
				}
				// 偶发把 refs 撑到超 50。
				if rng.Intn(40) == 0 {
					call.refs = make([]string, 51)
					for i := range call.refs {
						call.refs[i] = "z"
					}
				}
				// 偶发自引用 / 空引用。
				if rng.Intn(30) == 0 {
					call.refs = append(call.refs, call.id)
				}
				if rng.Intn(30) == 0 {
					call.refs = append(call.refs, "")
				}
			}

			got, err := m.Add(call.id, call.refs, call.irt, call.subj, call.ts)
			want, kind := na.add(call.id, call.refs, call.irt, call.subj, call.ts)
			t.Logf("组=%d 步=%d 输入 id=%q refs=%q irt=%q subject=%q ts=%d -> 实得 {%+v,%v} 朴素 {%+v,%s}",
				g, step, call.id, call.refs, call.irt, call.subj, call.ts, got, errKind(err), want, kind)

			if kind != errKind(err) {
				t.Fatalf("错误类别不一致: 实得=%v 朴素=%s (输入 %+v)", err, kind, call)
			}
			if err == nil && !reflect.DeepEqual(got, want) {
				t.Fatalf("结果不一致: 实得=%+v 朴素=%+v (输入 %+v)", got, want, call)
			}

			// 每步对拍查询结果。
			if err == nil {
				if threadsM, threadsN := m.Threads(), na.threads(); !reflect.DeepEqual(threadsM, threadsN) {
					t.Fatalf("Threads 不一致: %v vs %v", threadsM, threadsN)
				}
				for _, id := range pool {
					gm, errM := m.ThreadOf(id)
					gn, okN := na.threadOf(id)
					if (errM == nil) != okN {
						t.Fatalf("ThreadOf(%q) 存在性不一致: %v vs %v", id, errM, okN)
					}
					if errM == nil && gm != gn {
						t.Fatalf("ThreadOf(%q) = %q vs 朴素 %q", id, gm, gn)
					}
				}
				for _, root := range m.Threads() {
					mm, _ := m.Members(root)
					nm, ok := na.members(root)
					if !ok {
						t.Fatalf("朴素缺少线程 %s", root)
					}
					if !reflect.DeepEqual(mm, nm) {
						t.Fatalf("Members(%s) = %v vs %v", root, mm, nm)
					}
				}
			}
		}
	}
}

func genID(rng *rand.Rand, g, step int) string {
	const letters = "abcde"
	b := make([]byte, 1+rng.Intn(3))
	for i := range b {
		b[i] = letters[rng.Intn(len(letters))]
	}
	// 加入组/步信息避免跨组碰撞；同组内仍可能出现 a、ab 等短 id 碰撞，制造真实复用。
	return string(b) + "-" + itoa(g%7) + itoa(step)
}
