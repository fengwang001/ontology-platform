package acl

import (
	"fmt"
	"math/rand"
	"reflect"
	"testing"
)

// 合法标志集合：0..15 中 IO 或 NP 出现时 OI 与 CI 至少一个。
var legalFlags = []uint8{0, 1, 2, 3, 5, 6, 7, 9, 10, 11, 13, 14, 15}

var simPrincipals = []string{"u1", "u2", "u3", "g1", "g2"}

func randACE(r *rand.Rand) ACE {
	return ACE{
		Allow:     r.Intn(2) == 0,
		Principal: simPrincipals[r.Intn(len(simPrincipals))],
		Mask:      uint32(1 + r.Intn(15)),
		Flags:     legalFlags[r.Intn(len(legalFlags))],
	}
}

// TestRandomAgainstNaiveSim 随机生成 2000 组树结构、条目与判定序列，
// 与按定义递归重算的朴素模拟逐一对照，并记录输入、输出与判定依据。
func TestRandomAgainstNaiveSim(t *testing.T) {
	const groups = 2000
	r := rand.New(rand.NewSource(20261002))
	for g := 0; g < groups; g++ {
		D := 1 + r.Intn(6)
		K := 1 + r.Intn(5)
		s, err := NewStore(D, K)
		if err != nil {
			t.Fatalf("group %d: NewStore: %v", g, err)
		}
		sm := newSim()
		ids := []string{"/"}
		ops := 10 + r.Intn(20)
		for op := 0; op < ops; op++ {
			switch r.Intn(5) {
			case 0: // AddNode
				id := fmt.Sprintf("g%d-n%d", g, len(ids))
				parent := ids[r.Intn(len(ids))]
				container := r.Intn(2) == 0
				if err := s.AddNode(id, parent, container); err == nil {
					sm.add(id, parent, container)
					ids = append(ids, id)
					t.Logf("group %d op %d: AddNode(%s, %s, %v) ok", g, op, id, parent, container)
				}
			case 1: // SetACL
				id := ids[r.Intn(len(ids))]
				n := r.Intn(K + 1)
				aces := make([]ACE, n)
				for i := range aces {
					aces[i] = randACE(r)
				}
				protected := r.Intn(4) == 0
				if err := s.SetACL(id, aces, protected); err == nil {
					sm.setACL(id, aces, protected)
					t.Logf("group %d op %d: SetACL(%s, %+v, %v) ok", g, op, id, aces, protected)
				}
			case 2: // Move
				if len(ids) > 1 {
					id := ids[1+r.Intn(len(ids)-1)]
					np := ids[r.Intn(len(ids))]
					if err := s.Move(id, np); err == nil {
						sm.move(id, np)
						t.Logf("group %d op %d: Move(%s, %s) ok", g, op, id, np)
					}
				}
			case 3: // Eval
				id := ids[r.Intn(len(ids))]
				tok := []string{simPrincipals[r.Intn(len(simPrincipals))]}
				if r.Intn(2) == 0 {
					tok = append(tok, simPrincipals[r.Intn(len(simPrincipals))])
				}
				R := uint32(1 + r.Intn(15))
				got, err := s.Eval(tok, id, R)
				if err != nil {
					t.Fatalf("group %d op %d: Eval(%v, %s, %#b): %v", g, op, tok, id, R, err)
				}
				want := sm.eval(tok, id, R)
				t.Logf("group %d op %d: Eval(%v, %s, %#b) = %+v", g, op, tok, id, R, got)
				if got != want {
					t.Fatalf("group %d op %d: Eval(%v, %s, %#b) = %+v, sim = %+v",
						g, op, tok, id, R, got, want)
				}
			case 4: // Effective
				id := ids[r.Intn(len(ids))]
				got, err := s.Effective(id)
				if err != nil {
					t.Fatalf("group %d op %d: Effective(%s): %v", g, op, id, err)
				}
				want := sm.effective(id)
				if len(got) == 0 && len(want) == 0 {
					break
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("group %d op %d: Effective(%s) = %+v, sim = %+v", g, op, id, got, want)
				}
			}
		}
		// 每组结束时对所有节点做一次全量 Effective 与一次 Eval 对照。
		for _, id := range ids {
			got, err := s.Effective(id)
			if err != nil {
				t.Fatalf("group %d final: Effective(%s): %v", g, id, err)
			}
			if want := sm.effective(id); !reflect.DeepEqual(got, want) &&
				!(len(got) == 0 && len(want) == 0) {
				t.Fatalf("group %d final: Effective(%s) = %+v, sim = %+v", g, id, got, want)
			}
			tok := []string{simPrincipals[r.Intn(len(simPrincipals))], simPrincipals[r.Intn(len(simPrincipals))]}
			R := uint32(1 + r.Intn(15))
			gotRes, err := s.Eval(tok, id, R)
			if err != nil {
				t.Fatalf("group %d final: Eval: %v", g, err)
			}
			if wantRes := sm.eval(tok, id, R); gotRes != wantRes {
				t.Fatalf("group %d final: Eval(%v, %s, %#b) = %+v, sim = %+v", g, tok, id, R, gotRes, wantRes)
			}
			t.Logf("group %d final: Eval(%v, %s, %#b) = %+v", g, tok, id, R, gotRes)
		}
	}
}

// TestReplayDeterminism 相同操作序列重放得到完全相同的有效列表与判定结果。
func TestReplayDeterminism(t *testing.T) {
	r := rand.New(rand.NewSource(7))
	type op struct {
		kind      int
		id        string
		parent    string
		container bool
		aces      []ACE
		protected bool
		token     []string
		R         uint32
	}
	var ops []op
	ids := []string{"/"}
	for i := 0; i < 100; i++ {
		switch r.Intn(4) {
		case 0:
			id := fmt.Sprintf("n%d", len(ids))
			ops = append(ops, op{kind: 0, id: id, parent: ids[r.Intn(len(ids))], container: r.Intn(2) == 0})
			ids = append(ids, id)
		case 1:
			n := r.Intn(4)
			aces := make([]ACE, n)
			for j := range aces {
				aces[j] = randACE(r)
			}
			ops = append(ops, op{kind: 1, id: ids[r.Intn(len(ids))], aces: aces, protected: r.Intn(3) == 0})
		case 2:
			ops = append(ops, op{kind: 2, id: ids[r.Intn(len(ids))],
				token: []string{"u1", "g1"}, R: uint32(1 + r.Intn(15))})
		case 3:
			ops = append(ops, op{kind: 3, id: ids[r.Intn(len(ids))]})
		}
	}
	run := func() []string {
		s := mustStore(t, 8, 8)
		var out []string
		for _, o := range ops {
			switch o.kind {
			case 0:
				out = append(out, fmt.Sprintf("add=%v", s.AddNode(o.id, o.parent, o.container)))
			case 1:
				out = append(out, fmt.Sprintf("set=%v", s.SetACL(o.id, o.aces, o.protected)))
			case 2:
				res, err := s.Eval(o.token, o.id, o.R)
				out = append(out, fmt.Sprintf("eval=%+v err=%v", res, err))
			case 3:
				eff, err := s.Effective(o.id)
				out = append(out, fmt.Sprintf("eff=%+v err=%v", eff, err))
			}
		}
		return out
	}
	first := run()
	second := run()
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("replay mismatch:\n%v\n%v", first, second)
	}
}
