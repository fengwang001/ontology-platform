package main

import (
	"fmt"
	"reflect"

	"ontology/check"
	"ontology/hash"
	"ontology/match"
)

func main() {
	pass := 0
	checks := 4

	if true {
		fmt.Println("OK skeleton runs")
		pass++
	} else {
		fmt.Println("FAIL skeleton runs")
	}

	roller, err := hash.New(2, 251, 2)
	if err == nil {
		roller.Append(' ')
		roller.Append('"')
		if roller.Value() == 98 {
			fmt.Println("OK rolling hash collision value")
			pass++
		} else {
			fmt.Println("FAIL rolling hash collision value")
		}
	} else {
		fmt.Println("FAIL rolling hash construction")
	}

	positions, err := match.FindAll(`! " "`, ` "`)
	if err == nil && len(positions) == 2 && positions[0] == 1 && positions[1] == 3 {
		fmt.Println("OK match rejects hash collision")
		pass++
	} else {
		fmt.Println("FAIL match rejects hash collision")
	}

	if reflect.DeepEqual(check.NaiveFindAll("abababa", "aba"), []int{0, 2, 4}) {
		fmt.Println("OK naive reference finds overlaps")
		pass++
	} else {
		fmt.Println("FAIL naive reference finds overlaps")
	}

	fmt.Printf("TOTAL %d/%d\n", pass, checks)
	if pass != checks {
		panic("demo checks failed")
	}
}
