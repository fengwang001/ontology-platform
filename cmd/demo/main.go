package main

import "fmt"

type check struct {
	name string
	ok   bool
}

func main() {
	var checks []check

	// record 包：深度超限记录必须被拒绝并计数。
	before := record.DepthRejections()
	deep := map[string]any{}
	cur := deep
	for i := 0; i < record.MaxDepth+1; i++ {
		next := map[string]any{}
		cur["k"] = next
		cur = next
	}
	_, depthErr := record.New(record.LevelInfo, "t", true, deep)
	checks = append(checks, check{"depth-overflow rejected & counted",
		errors.Is(depthErr, record.ErrDepth) && record.DepthRejections() > before})

	nFail := 0
	for _, c := range checks {
		if c.ok {
			fmt.Println("OK  " + c.name)
		} else {
			nFail++
			fmt.Println("FAIL " + c.name)
		}
	}
	fmt.Printf("SUMMARY %d/%d checks passed\n", len(checks)-nFail, len(checks))
	if nFail > 0 {
		fmt.Println("FAIL demo")
		os.Exit(1)
	}
}
