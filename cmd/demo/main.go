package main

import (
	"fmt"

	"ontology/bucket"
	"ontology/hyper"
	"ontology/vec"
)

type check struct {
	name   string
	ok     bool
	detail string
}

func (c check) print() {
	tag := "OK"
	if !c.ok {
		tag = "FAIL"
	}
	fmt.Printf("%s %s %s\n", tag, c.name, c.detail)
}

func main() {
	var results []check

	// 1) 同种子超平面/签名逐字节相同；不同种子有差异。
	v := []vec.Vector{{1, -2, 3, 0.5}, {-1, 2, -3, -0.5}, {0, 0, 0, 0}}
	f1 := hyper.NewFamily(4, 8, 8, 42)
	f2 := hyper.NewFamily(4, 8, 8, 42)
	f3 := hyper.NewFamily(4, 8, 8, 7)
	identical, differ := true, false
	for _, x := range v {
		for t := 0; t < 8; t++ {
			s1, _ := f1.Signature(t, x)
			s2, _ := f2.Signature(t, x)
			s3, _ := f3.Signature(t, x)
			if s1 != s2 {
				identical = false
			}
			if s1 != s3 {
				differ = true
			}
		}
	}
	results = append(results, check{"same-seed signatures byte-identical",
		identical && differ && f1.Equal(f2), ""})

	// 2) 多表候选去重；恢复时剔除悬挂 ID，桶表无悬挂引用。
	mt := bucket.NewMultiTable(2)
	mt.Add(0, 1, 10)
	mt.Add(0, 1, 11)
	mt.Add(1, 1, 10)
	mt.Add(1, 2, 99) // 悬挂 ID
	cand := mt.Candidates([]bucket.SigPair{{0, 1}, {1, 1}, {1, 2}})
	dedup := len(cand) == 3
	dropped := mt.DropMissing(map[int32]bool{10: true, 11: true})
	rest := mt.Candidates([]bucket.SigPair{{0, 1}, {1, 1}, {1, 2}})
	nodangle := dropped == 1
	for _, id := range rest {
		if id == 99 {
			nodangle = false
		}
	}
	results = append(results, check{"bucket dedup + no dangling id",
		dedup && nodangle, fmt.Sprintf("cand=%d dropped=%d", len(cand), dropped)})

	fail := 0
	for _, r := range results {
		r.print()
		if !r.ok {
			fail++
		}
	}
	fmt.Printf("TOTAL %d/%d passed\n", len(results)-fail, len(results))
	if fail > 0 {
		fmt.Println("FAIL overall")
	}
}
