package main

import (
	"errors"
	"fmt"
	"math/rand"
	"strings"

	"ontology/mtype"
	"ontology/negotiate"
	"ontology/rank"
)

var pass int

func main() {
	check("特异性排序", func() bool {
		accept := "text/*;q=0.5, text/plain;q=0.5"
		got, err := negotiate.Select(&accept, []string{"text/html", "text/plain"})
		return err == nil && got.Index == 1
	}())
	check("q 优先于特异性", func() bool {
		accept := "text/plain;q=0.1, text/*;q=0.2"
		got, err := negotiate.Select(&accept, []string{"text/plain", "text/html"})
		return err == nil && got.Index == 1
	}())
	check("q=0 拒绝", func() bool {
		accept := "text/plain;q=0"
		_, err := negotiate.Select(&accept, []string{"text/plain"})
		return errors.Is(err, negotiate.ErrNotAcceptable)
	}())
	check("非法 q 处理", func() bool {
		_, err := rank.ParseAcceptItem("text/plain;q=1.5", 3)
		return errors.Is(err, rank.ErrInvalidQ)
	}())
	check("参数集合匹配", func() bool {
		accept := "text/plain;format=flowed"
		_, err := negotiate.Select(&accept, []string{"text/plain"})
		return errors.Is(err, negotiate.ErrNotAcceptable)
	}())
	check("参数值大小写敏感", func() bool {
		accept := "text/plain;format=flowed"
		_, err := negotiate.Select(&accept, []string{"text/plain;format=Flowed"})
		return errors.Is(err, negotiate.ErrNotAcceptable)
	}())
	check("同分取服务端顺序", func() bool {
		accept := "text/plain, application/json"
		first, _ := negotiate.Select(&accept, []string{"application/json", "text/plain"})
		second, _ := negotiate.Select(&accept, []string{"text/plain", "application/json"})
		return first.Index == 0 && second.Index == 0
	}())
	check("Accept 顺序无关", func() bool {
		left := "text/plain, application/json"
		right := "application/json, text/plain"
		a, _ := negotiate.Select(&left, []string{"application/json", "text/plain"})
		b, _ := negotiate.Select(&right, []string{"application/json", "text/plain"})
		return a.Index == b.Index
	}())
	check("缺失 vs 空串", func() bool {
		empty := ""
		_, missingErr := negotiate.Select(nil, []string{"text/plain"})
		_, emptyErr := negotiate.Select(&empty, []string{"text/plain"})
		return missingErr == nil && errors.Is(emptyErr, negotiate.ErrNotAcceptable)
	}())
	check("无法接受不回退", func() bool {
		accept := "application/json"
		_, err := negotiate.Select(&accept, []string{"text/plain"})
		return errors.Is(err, negotiate.ErrNotAcceptable)
	}())
	check("引号内分号", func() bool {
		got, err := mtype.Parse(`TEXT/Plain;X="a;b\"c"`, 0)
		return err == nil && got.Type == "text" && got.Subtype == "plain" && got.Params["x"] == `a;b"c`
	}())
	check("五类错误可区分", func() bool {
		cases := []string{"text", "text/", "text/plain;x", `text/plain;x="`, "text/plain;q=abc"}
		kinds := []error{mtype.ErrMissingSlash, mtype.ErrEmptySubtype, mtype.ErrMissingEqual, mtype.ErrUnclosedQuote, rank.ErrInvalidQ}
		for i, input := range cases {
			_, err := rank.ParseAcceptItem(input, 7)
			if !errors.Is(err, kinds[i]) || !strings.Contains(err.Error(), "7") {
				return false
			}
		}
		return true
	}())
	check("50 次打乱一致", func() bool {
		candidates := []string{"application/json", "text/html", "text/plain"}
		random := rand.New(rand.NewSource(1))
		base := "text/html, application/json, text/plain"
		want, err := negotiate.Select(&base, candidates)
		if err != nil {
			return false
		}
		for i := 0; i < 50; i++ {
			items := []string{"text/html", "application/json", "text/plain"}
			random.Shuffle(len(items), func(a, b int) { items[a], items[b] = items[b], items[a] })
			accept := strings.Join(items, ", ")
			got, err := negotiate.Select(&accept, candidates)
			if err != nil || got.Index != want.Index {
				return false
			}
		}
		return true
	}())
	fmt.Printf("总计 %d/13\n", pass)
}

func check(name string, ok bool) {
	if ok {
		pass++
		fmt.Println(name + " OK")
	} else {
		fmt.Println(name + " FAIL")
	}
}
