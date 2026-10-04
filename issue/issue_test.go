package issue

import (
	"errors"
	"fmt"
	"testing"

	"ontology/bloodstock"
)

func errIs(got, want error) bool {
	if want == nil {
		return got == nil
	}
	return errors.Is(got, want)
}

// step 用最小编码描述一个操作，便于表驱动与随机序列共用、便于日志打印。
type step struct {
	kind                  string
	now                   int64
	user, n2, pat, bag    string
	sample, abo, rh, role string
	n                     int
	exp                   int64
}

func (s step) String() string {
	switch s.kind {
	case "add":
		return fmt.Sprintf("AddBag(now=%d bag=%s %s%s exp=%d)", s.now, s.bag, s.abo, s.rh, s.exp)
	case "grant":
		return fmt.Sprintf("Grant(%s,%s)", s.user, s.role)
	case "type":
		return fmt.Sprintf("Type(now=%d %s pat=%s sample=%s %s%s)", s.now, s.user, s.pat, s.sample, s.abo, s.rh)
	case "resolve":
		return fmt.Sprintf("Resolve(now=%d sup=%s pat=%s %s%s)", s.now, s.user, s.pat, s.abo, s.rh)
	case "xm":
		return fmt.Sprintf("Crossmatch(now=%d tech=%s pat=%s n=%d)", s.now, s.user, s.pat, s.n)
	case "issue":
		return fmt.Sprintf("Issue(now=%d %s,%s pat=%s bag=%s)", s.now, s.user, s.n2, s.pat, s.bag)
	case "return":
		return fmt.Sprintf("Return(now=%d %s bag=%s)", s.now, s.user, s.bag)
	case "discard":
		return fmt.Sprintf("Discard(now=%d %s bag=%s)", s.now, s.user, s.bag)
	}
	return s.kind
}

func runStep(m *Manager, s step) ([]string, error) {
	switch s.kind {
	case "add":
		return nil, m.AddBag(s.now, s.bag, s.abo, s.rh, s.exp)
	case "grant":
		m.Grant(s.user, s.role)
		return nil, nil
	case "type":
		return nil, m.Type(s.now, s.user, s.pat, s.sample, s.abo, s.rh)
	case "resolve":
		return nil, m.Resolve(s.now, s.user, s.pat, s.abo, s.rh)
	case "xm":
		return m.Crossmatch(s.now, s.user, s.pat, s.n)
	case "issue":
		return nil, m.Issue(s.now, s.user, s.n2, s.pat, s.bag)
	case "return":
		return nil, m.Return(s.now, s.user, s.bag)
	case "discard":
		return nil, m.Discard(s.now, s.user, s.bag)
	}
	return nil, nil
}

func must(m *Manager, s step) []string {
	got, err := runStep(m, s)
	if err != nil {
		panic(fmt.Sprintf("%s: %v", s, err))
	}
	return got
}

// 题面库存：M=500,H=4320
// A阳 a1=1000、a2=5000；A阴 n1=3000；O阳 o1=4000；O阴 z1=2000。
func setupExample() *Manager {
	m := New(500, 4320)
	must(m, step{kind: "add", now: 1, bag: "a1", abo: "A", rh: "阳", exp: 1000})
	must(m, step{kind: "add", now: 1, bag: "a2", abo: "A", rh: "阳", exp: 5000})
	must(m, step{kind: "add", now: 1, bag: "n1", abo: "A", rh: "阴", exp: 3000})
	must(m, step{kind: "add", now: 1, bag: "o1", abo: "O", rh: "阳", exp: 4000})
	must(m, step{kind: "add", now: 1, bag: "z1", abo: "O", rh: "阴", exp: 2000})
	must(m, step{kind: "grant", user: "tech", role: "配血"})
	must(m, step{kind: "grant", user: "i1", role: "发血"})
	must(m, step{kind: "grant", user: "i2", role: "发血"})
	must(m, step{kind: "grant", user: "boss", role: "主管"})
	return m
}

func confirmP(m *Manager) {
	must(m, step{kind: "type", now: 10, user: "t", pat: "P", sample: "sp1", abo: "A", rh: "阳"})
	must(m, step{kind: "type", now: 10, user: "t", pat: "P", sample: "sp2", abo: "A", rh: "阳"})
}

func xm(now int64, tech, pat string, n int) step {
	return step{kind: "xm", now: now, user: tech, pat: pat, n: n}
}

func TestSpecExample(t *testing.T) {
	t.Run("now=600 Crossmatch(P,3) 排除a1", func(t *testing.T) {
		m := setupExample()
		confirmP(m)
		got := must(m, xm(600, "tech", "P", 3))
		if fmt.Sprint(got) != "[a2 n1 o1]" {
			t.Fatalf("want [a2 n1 o1], got %v", got)
		}
		if ex := m.ExaminedCross(); ex > int64(3+1+8) {
			t.Fatalf("examined=%d 超 n+expired+8", ex)
		}
	})

	t.Run("n=4 再加 z1", func(t *testing.T) {
		m := setupExample()
		confirmP(m)
		got := must(m, xm(600, "tech", "P", 4))
		if fmt.Sprint(got) != "[a2 n1 o1 z1]" {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("n=5 库存不足且零预留", func(t *testing.T) {
		m := setupExample()
		confirmP(m)
		_, err := runStep(m, xm(600, "tech", "P", 5))
		if !errIs(err, bloodstock.ErrStock) {
			t.Fatalf("want ErrStock got %v", err)
		}
		for name, b := range m.Snapshot().Bags {
			if b.Status != bloodstock.Available {
				t.Fatalf("失败后 %s 状态=%v，应全部可用", name, b.Status)
			}
		}
	})

	t.Run("now=499 a1可选；now=500 a1恰排除", func(t *testing.T) {
		m := setupExample()
		confirmP(m)
		if got := must(m, xm(499, "tech", "P", 3)); fmt.Sprint(got) != "[a1 a2 n1]" {
			t.Fatalf("499: got %v", got)
		}
		m2 := setupExample()
		confirmP(m2)
		if got := must(m2, xm(500, "tech", "P", 3)); fmt.Sprint(got) != "[a2 n1 o1]" {
			t.Fatalf("500 取等: got %v", got)
		}
	})

	t.Run("到期：报废先于释放、恰等", func(t *testing.T) {
		m := setupExample()
		confirmP(m)
		must(m, xm(600, "tech", "P", 3))
		probe := func(now int64) {
			must(m, step{kind: "type", now: now, user: "t", pat: fmt.Sprintf("Z%d", now),
				sample: fmt.Sprintf("zz%d", now), abo: "O", rh: "阴"})
		}
		probe(3000)
		if b := m.Snapshot().Bags["n1"]; b.Status != bloodstock.Discarded {
			t.Fatalf("n1@3000 = %v", b.Status)
		}
		probe(4000)
		if b := m.Snapshot().Bags["o1"]; b.Status != bloodstock.Discarded {
			t.Fatalf("o1@4000 = %v", b.Status)
		}
		if b := m.Snapshot().Bags["a2"]; b.Status != bloodstock.Reserved {
			t.Fatalf("a2@4000 应仍预留 = %v", b.Status)
		}
		probe(4919)
		if b := m.Snapshot().Bags["a2"]; b.Status != bloodstock.Reserved {
			t.Fatalf("a2@4919 未到释放 = %v", b.Status)
		}
		probe(4920)
		if b := m.Snapshot().Bags["a2"]; b.Status != bloodstock.Available || b.Patient != "" {
			t.Fatalf("a2@4920 = %v patient=%q", b.Status, b.Patient)
		}
		if m.ExaminedLandSlack() > 1 {
			t.Fatalf("land slack=%d > 1", m.ExaminedLandSlack())
		}
	})
}

func TestTypingRules(t *testing.T) {
	t.Run("单次只用O 先阳后阴（Q A阳 -> o1,z1）", func(t *testing.T) {
		m := setupExample()
		must(m, step{kind: "type", now: 600, user: "t", pat: "Q", sample: "q1", abo: "A", rh: "阳"})
		got := must(m, xm(600, "tech", "Q", 2))
		if fmt.Sprint(got) != "[o1 z1]" {
			t.Fatalf("got %v", got)
		}
	})

	t.Run("未知只用O阴（V -> z1；n=2 库存不足）", func(t *testing.T) {
		m := setupExample()
		if got := must(m, xm(600, "tech", "V", 1)); fmt.Sprint(got) != "[z1]" {
			t.Fatalf("got %v", got)
		}
		m2 := setupExample()
		if _, err := runStep(m2, xm(600, "tech", "V", 2)); !errIs(err, bloodstock.ErrStock) {
			t.Fatalf("want stock got %v", err)
		}
	})

	t.Run("不一致转存疑立即释放，此后只用O阴", func(t *testing.T) {
		m := setupExample()
		must(m, step{kind: "type", now: 600, user: "t", pat: "Q", sample: "q1", abo: "A", rh: "阳"})
		must(m, xm(600, "tech", "Q", 2))
		must(m, step{kind: "type", now: 601, user: "t", pat: "Q", sample: "q2", abo: "B", rh: "阳"})
		for _, name := range []string{"o1", "z1"} {
			b := m.Snapshot().Bags[name]
			if b.Patient != "" || b.Status != bloodstock.Available {
				t.Fatalf("%s 应已释放: %+v", name, b)
			}
		}
		if got := must(m, xm(602, "tech", "Q", 1)); fmt.Sprint(got) != "[z1]" {
			t.Fatalf("存疑后应只取 z1, got %v", got)
		}
		if _, gotErr := runStep(m, step{kind: "type", now: 604, user: "t", pat: "Q", sample: "q1", abo: "O", rh: "阴"}); !errIs(gotErr, bloodstock.ErrDuplicate) {
			t.Fatalf("标本重复 got %v", gotErr)
		}
	})

	t.Run("Resolve 主管裁定为O阴", func(t *testing.T) {
		m := setupExample()
		must(m, step{kind: "type", now: 600, user: "t", pat: "R", sample: "r1", abo: "A", rh: "阳"})
		must(m, step{kind: "type", now: 600, user: "t", pat: "R", sample: "r2", abo: "B", rh: "阳"})
		must(m, step{kind: "resolve", now: 600, user: "boss", pat: "R", abo: "O", rh: "阴"})
		if got := must(m, xm(600, "tech", "R", 1)); fmt.Sprint(got) != "[z1]" {
			t.Fatalf("裁定O阴后取 z1, got %v", got)
		}
	})
}

// TestGroupOrders 覆盖各血型次序与「ABO 先于 Rh」：
// 8 个组各放一袋且 exp 相同，受者确认型不同，选中顺序必须匹配规则。
func TestGroupOrders(t *testing.T) {
	groups8 := []struct{ name, abo, rh string }{
		{"g_A+", "A", "阳"}, {"g_A-", "A", "阴"},
		{"g_B+", "B", "阳"}, {"g_B-", "B", "阴"},
		{"g_AB+", "AB", "阳"}, {"g_AB-", "AB", "阴"},
		{"g_O+", "O", "阳"}, {"g_O-", "O", "阴"},
	}
	cases := []struct {
		abo, rh string
		want    []string
	}{
		{"A", "阳", []string{"g_A+", "g_A-", "g_O+", "g_O-"}},
		{"A", "阴", []string{"g_A-", "g_O-"}},
		{"B", "阳", []string{"g_B+", "g_B-", "g_O+", "g_O-"}},
		{"B", "阴", []string{"g_B-", "g_O-"}},
		{"AB", "阳", []string{"g_AB+", "g_AB-", "g_A+", "g_A-", "g_B+", "g_B-", "g_O+", "g_O-"}},
		{"AB", "阴", []string{"g_AB-", "g_A-", "g_B-", "g_O-"}},
		{"O", "阳", []string{"g_O+", "g_O-"}},
		{"O", "阴", []string{"g_O-"}},
	}
	for _, c := range cases {
		m := New(0, 4320)
		for i, g := range groups8 {
			must(m, step{kind: "add", now: 1, bag: g.name, abo: g.abo, rh: g.rh, exp: 100000 + int64(i)})
		}
		m.Grant("tech", "配血")
		pat := c.abo + c.rh
		must(m, step{kind: "type", now: 2, user: "t", pat: pat, sample: pat + "1", abo: c.abo, rh: c.rh})
		must(m, step{kind: "type", now: 2, user: "t", pat: pat, sample: pat + "2", abo: c.abo, rh: c.rh})
		got := must(m, xm(2, "tech", pat, len(c.want)))
		if fmt.Sprint(got) != fmt.Sprint(c.want) {
			t.Fatalf("%s: got %v want %v", pat, got, c.want)
		}
	}
}

func TestIssueReturnDiscard(t *testing.T) {
	t.Run("双人不同发血、退回30分钟取等", func(t *testing.T) {
		m := setupExample()
		confirmP(m)
		bags := must(m, xm(600, "tech", "P", 4)) // a2,n1,o1,z1
		bag := bags[0]                           // a2
		if err := m.Issue(700, "i1", "i1", "P", bag); !errIs(err, bloodstock.ErrInvalid) {
			t.Fatalf("同人 n1==n2 want invalid got %v", err)
		}
		if err := m.Issue(700, "i1", "nobody", "P", bag); !errIs(err, bloodstock.ErrUnauthorized) {
			t.Fatalf("无资格 got %v", err)
		}
		if err := m.Issue(700, "i1", "i2", "X", bag); !errIs(err, bloodstock.ErrState) {
			t.Fatalf("他人预留 got %v", err)
		}
		if err := m.Issue(700, "i1", "i2", "P", bag); err != nil {
			t.Fatalf("正常发血: %v", err)
		}
		// 另一袋 o1 701 发出：已发血袋不参与到期（exp=4000）。
		must(m, step{kind: "issue", now: 701, user: "i1", n2: "i2", pat: "P", bag: "o1"})
		// 731 超时，状态不变
		if err := m.Return(731, "i1", bag); !errIs(err, bloodstock.ErrTimeout) {
			t.Fatalf("731 want timeout got %v", err)
		}
		if b := m.Snapshot().Bags[bag]; b.Status != bloodstock.Issued {
			t.Fatalf("超时退回失败不应改状态: %v", b.Status)
		}
		// 730 恰等可退
		if err := m.Return(730, "i1", bag); err != nil {
			t.Fatalf("730 可退: %v", err)
		}
		if b := m.Snapshot().Bags[bag]; b.Status != bloodstock.Available {
			t.Fatalf("退回后应可用: %v", b.Status)
		}
		must(m, step{kind: "type", now: 4000, user: "t", pat: "W", sample: "w1", abo: "O", rh: "阴"})
		if b := m.Snapshot().Bags["o1"]; b.Status != bloodstock.Issued {
			t.Fatalf("已发血袋不参与到期: %v", b.Status)
		}
	})

	t.Run("退回时已过exp直接报废，原预留不恢复", func(t *testing.T) {
		m := New(10, 100000)
		m.Grant("tech", "配血")
		m.Grant("i1", "发血")
		m.Grant("i2", "发血")
		must(m, step{kind: "type", now: 1, user: "t", pat: "P", sample: "p1", abo: "O", rh: "阴"})
		must(m, step{kind: "type", now: 1, user: "t", pat: "P", sample: "p2", abo: "O", rh: "阴"})
		// exp=35：10 发出，35 退回恰等 exp（间隔 25 <= 30）应直接报废。
		must(m, step{kind: "add", now: 1, bag: "b1", abo: "O", rh: "阴", exp: 35})
		bags := must(m, xm(2, "tech", "P", 1))
		must(m, step{kind: "issue", now: 10, user: "i1", n2: "i2", pat: "P", bag: bags[0]})
		if err := m.Return(35, "i1", bags[0]); err != nil {
			t.Fatalf("退回窗口内且 now==exp 不报错: %v", err)
		}
		b := m.Snapshot().Bags[bags[0]]
		if b.Status != bloodstock.Discarded || b.Patient != "" {
			t.Fatalf("退回时 now==exp 应报废且不恢复预留: %+v", b)
		}
	})

	t.Run("Discard 权限与状态", func(t *testing.T) {
		m := setupExample()
		if err := m.Discard(10, "tech", "a1"); !errIs(err, bloodstock.ErrUnauthorized) {
			t.Fatalf("配血无权报废 got %v", err)
		}
		if err := m.Discard(10, "boss", "a1"); err != nil {
			t.Fatal(err)
		}
		if err := m.Discard(10, "boss", "a1"); !errIs(err, bloodstock.ErrState) {
			t.Fatalf("重复报废 want state got %v", err)
		}
	})
}

func TestRejectOrder(t *testing.T) {
	// 参数非法 > 时钟回退 > 不存在(含重复) > 无资格 > 状态不符 > 库存不足/超时
	m := setupExample()
	confirmP(m)

	if _, err := m.Crossmatch(600, "tech", "P", 0); !errIs(err, bloodstock.ErrInvalid) {
		t.Fatalf("n=0 want invalid got %v", err)
	}
	if _, err := m.Crossmatch(600, "tech", "P", 21); !errIs(err, bloodstock.ErrInvalid) {
		t.Fatalf("n=21 want invalid got %v", err)
	}
	if err := m.AddBag(600, "x", "X", "阳", 1000); !errIs(err, bloodstock.ErrInvalid) {
		t.Fatalf("非法ABO got %v", err)
	}
	if err := m.AddBag(600, "x", "O", "阳", 600); !errIs(err, bloodstock.ErrInvalid) {
		t.Fatalf("exp==now want invalid got %v", err)
	}
	if _, err := m.Crossmatch(5, "tech", "P", 1); !errIs(err, bloodstock.ErrClock) {
		t.Fatalf("时钟回退 got %v", err)
	}
	if err := m.Discard(600, "nobody", "ghost"); !errIs(err, bloodstock.ErrNotFound) {
		t.Fatalf("不存在>无资格 got %v", err)
	}
	if err := m.AddBag(600, "a1", "O", "阴", 5000); !errIs(err, bloodstock.ErrDuplicate) {
		t.Fatalf("血袋重复 got %v", err)
	}
	if err := m.Discard(600, "tech", "a1"); !errIs(err, bloodstock.ErrUnauthorized) {
		t.Fatalf("无资格>状态 got %v", err)
	}
	must(m, step{kind: "discard", now: 600, user: "boss", bag: "a1"})
	if err := m.Issue(600, "i1", "i2", "P", "a1"); !errIs(err, bloodstock.ErrState) {
		t.Fatalf("已报废发血 want state got %v", err)
	}
	if err := m.Resolve(600, "nobody", "GHOST", "O", "阴"); !errIs(err, bloodstock.ErrNotFound) {
		t.Fatalf("resolve 不存在患者>无资格 got %v", err)
	}
}

func TestRejectedDoesNotAdvanceClock(t *testing.T) {
	m := setupExample()
	confirmP(m)
	// 参数非法的回退操作被拒绝：零效果、不推进时钟。
	if _, err := m.Crossmatch(9, "tech", "P", 0); !errIs(err, bloodstock.ErrInvalid) {
		t.Fatalf("want invalid got %v", err)
	}
	if _, err := m.Crossmatch(9, "tech", "P", 1); !errIs(err, bloodstock.ErrClock) {
		t.Fatalf("被拒绝后时钟仍为10，9应回退 got %v", err)
	}
	// 库存不足为只读判定：即使 @4999 本会使 n1/o1 报废，被拒绝也不落地、不推进时钟。
	m2 := setupExample()
	confirmP(m2)
	must(m2, xm(600, "tech", "P", 3)) // a2,n1,o1
	if _, err := m2.Crossmatch(4999, "tech", "P", 5); !errIs(err, bloodstock.ErrStock) {
		t.Fatalf("want stock got %v", err)
	}
	if m2.Snapshot().Now != 600 {
		t.Fatalf("被拒绝操作不推进时钟: %d", m2.Snapshot().Now)
	}
	if b := m2.Snapshot().Bags["n1"]; b.Status != bloodstock.Reserved {
		t.Fatalf("n1 不应落地，仍预留: %v", b.Status)
	}
	must(m2, step{kind: "type", now: 4999, user: "t", pat: "W", sample: "w1", abo: "O", rh: "阴"})
	if b := m2.Snapshot().Bags["n1"]; b.Status != bloodstock.Discarded {
		t.Fatalf("n1 应在 4999 随被接受操作落地报废: %v", b.Status)
	}
}
