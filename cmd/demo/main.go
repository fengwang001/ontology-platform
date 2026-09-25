package main

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"

	"ontology/api"
	"ontology/rjoin"
)

type joiner interface {
	Left(string, string) error
	UpsertRight(string, string) error
	DeleteRight(string) error
}

func run(j joiner, step string) error { // 一步小 DSL："L:id:key"、"U:key:val"、"D:key"
	p := strings.SplitN(step, ":", 3)
	switch p[0] {
	case "L":
		return j.Left(p[1], p[2])
	case "U":
		return j.UpsertRight(p[1], p[2])
	}
	return j.DeleteRight(p[1])
}
func main() {
	failed := false
	check := func(name string, ok bool) {
		if !ok {
			failed = true
		}
		fmt.Println(map[bool]string{true: "OK", false: "FAIL"}[ok], name)
	}
	// 1. 九步序列：每步之后核验受影响 leftID 的视图与反向索引一致性（Check），再核验最终四值。
	j := rjoin.New()
	chk := func(list []string) bool {
		for _, w := range list {
			p := strings.SplitN(w, "=", 2)
			if v, has := j.GetView(p[0]); v != p[1] || has != (p[1] != "") {
				return false
			}
		}
		return true
	}
	seq := []string{"U:a:v1", "L:L1:a", "U:b:w1", "L:L2:b", "U:a:v2", "L:L3:a", "D:b", "L:L4:b", "U:b:w2"}
	wants := [][]string{nil, {"L1=v1"}, nil, {"L2=w1"}, {"L1=v2"}, {"L3=v2"}, {"L2="}, {"L4="}, {"L4=w2"}}
	ok := true
	for i, st := range seq {
		if run(j, st) != nil || j.Check() != nil || !chk(wants[i]) {
			ok = false
		}
	}
	ok = ok && chk([]string{"L1=v2", "L2=", "L3=v2", "L4=w2"})
	check("九步视图+反向索引+最终四值", ok)
	// 2. api：SelfCheck、四类可判定错误、被拒后状态不变且仍可用。
	s := api.New()
	ok = s.SelfCheck() == nil && run(s, "L:x:k") == nil
	bad := []string{"L::k", "L:y:", "L:x:k", "U::v", "D:"}
	errs := []error{api.ErrEmptyLeftID, api.ErrEmptyLeftKey, api.ErrDupLeftID, api.ErrEmptyRightKey, api.ErrEmptyRightKey}
	for i, st := range bad {
		ok = ok && errors.Is(run(s, st), errs[i])
	}
	v1, h1 := s.GetView("x")
	ok = ok && v1 == "" && !h1 // 被拒操作不得留痕
	ok = ok && run(s, "U:k:v") == nil
	v2, h2 := s.GetView("x")
	ok = ok && v2 == "v" && h2 // 拒绝后仍可正常使用
	check("SelfCheck+四类错误+被拒不变", ok)
	// 3. 与批量重算一致：终态期望 = 右表[keyOf[id]]（被注销者恒为 ∅）。
	s2 := api.New()
	right, keyOf, gone := map[string]string{}, map[string]string{}, map[string]bool{}
	ok = true
	for i := 0; i < 200; i++ {
		k := fmt.Sprintf("k%d", i%5)
		switch i % 3 {
		case 0:
			ok = ok && s2.Left(fmt.Sprintf("L%d", i), k) == nil
			keyOf[fmt.Sprintf("L%d", i)] = k
		case 1:
			ok = ok && s2.UpsertRight(k, fmt.Sprintf("v%d", i)) == nil
			right[k] = fmt.Sprintf("v%d", i)
		case 2:
			ok = ok && s2.DeleteRight(k) == nil
			delete(right, k)
			for id, kk := range keyOf {
				if kk == k {
					gone[id] = true
				}
			}
		}
	}
	for id, kk := range keyOf {
		wantV, wantOk := right[kk]
		if gone[id] {
			wantV, wantOk = "", false
		}
		if v, has := s2.GetView(id); v != wantV || has != wantOk {
			ok = false
		}
	}
	check("与批量重算一致", ok)
	// 4. 大 m 下重估只触及目标 key 的左事件（检查个数的直接证明见 rjoin 包内测试）。
	s3 := api.New()
	for i := 0; i < 10000; i++ {
		k := fmt.Sprintf("k%d", i%97)
		if i < 3 {
			k = "hot"
		}
		_ = s3.Left(fmt.Sprintf("L%d", i), k)
	}
	_ = s3.UpsertRight("hot", "v")
	ok = true
	for i := 0; i < 10000; i++ {
		if v, has := s3.GetView(fmt.Sprintf("L%d", i)); (i < 3) != (has && v == "v") {
			ok = false
		}
	}
	check("大m重估不随m增长", ok)
	// 5. 并发：多 goroutine 只读（含 SelfCheck），更新期间只能看到旧值或新值。
	s4 := api.New()
	_ = s4.UpsertRight("k", "old")
	_ = s4.Left("L", "k")
	var stop, bad2 atomic.Bool
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = s4.SelfCheck()
			for !stop.Load() {
				if v, has := s4.GetView("L"); !has || (v != "old" && v != "new") {
					bad2.Store(true)
				}
			}
		}()
	}
	_ = s4.UpsertRight("k", "new")
	stop.Store(true)
	wg.Wait()
	v, has := s4.GetView("L")
	check("并发只读一致+更新原子", !bad2.Load() && v == "new" && has)
	if failed {
		os.Exit(1)
	}
}
