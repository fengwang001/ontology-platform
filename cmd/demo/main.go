package main

import "fmt"

import "ontology/stack"

type verdict struct {
	name string
	ok   bool
	detail string
}

func (v verdict) line() string {
	tag := "OK"
	if !v.ok {
		tag = "FAIL"
	}
	if v.detail == "" {
		return fmt.Sprintf("%s %s", tag, v.name)
	}
	return fmt.Sprintf("%s %s (%s)", tag, v.name, v.detail)
}

func main() {
	st, ok := stack.Normalize([]string{"a", "a", "b", "", "c"}, 4)
	atLimit, _ := stack.Normalize([]string{"a", "b", "c"}, 3)
	overLimit, _ := stack.Normalize([]string{"a", "b", "c", "d"}, 3)
	verdicts := []verdict{
		{name: "stack normalize: dedup+empty-frame+truncate",
			ok: ok && len(st.Frames) == 4 && st.Frames[3] == "" && !st.Truncated &&
				!atLimit.Truncated && overLimit.Truncated && len(overLimit.Frames) == 3,
			detail: "dedup adj dup, empty frame legal, ==limit kept, limit+1 cut"},
	}
	fails := 0
	for _, v := range verdicts {
		fmt.Println(v.line())
		if !v.ok {
			fails++
		}
	}
	fmt.Printf("TOTAL %d/%d pass\n", len(verdicts)-fails, len(verdicts))
	if fails > 0 {
		panic("demo checks failed")
	}
}
