package main

import (
	"errors"
	"fmt"
	"math/rand"
	"reflect"

	"ontology/logical"
	"ontology/props"
)

type probe struct {
	name  string
	check func() (bool, string)
}

func get(p *props.Properties, k string) string {
	v, _ := p.Get(k)
	return v
}

func pairs(p *props.Properties) [][2]string {
	out := [][2]string{}
	for k, v := range p.All() {
		out = append(out, [2]string{k, v})
	}
	return out
}

func main() {
	probes := []probe{}

	probes = append(probes, probe{"separators-and-whitespace", func() (bool, string) {
		p, err := props.Load("key=value\nkey2:value2\nkey3 value3\nkey4 = value  \nk==v\n=v\nonly\n")
		if err != nil {
			return false, err.Error()
		}
		got := []string{get(p, "key"), get(p, "key2"), get(p, "key3"),
			get(p, "key4"), get(p, "k"), get(p, ""), get(p, "only")}
		want := []string{"value", "value2", "value3", "value  ", "=v", "v", ""}
		return reflect.DeepEqual(got, want), fmt.Sprint(got)
	}})

	probes = append(probes, probe{"comments", func() (bool, string) {
		p, err := props.Load("# head\n! bang\n   # indented\nk=v # x\n")
		if err != nil {
			return false, err.Error()
		}
		return p.Len() == 1 && get(p, "k") == "v # x", fmt.Sprint(pairs(p))
	}})

	probes = append(probes, probe{"continuations", func() (bool, string) {
		p, err := props.Load("k=v\\\n   w\nk2=v\\\\\nx=y\nk3=v\\\\\\\n  w\nk4=a\\\n#b\nk5=v\\\n")
		if err != nil {
			return false, err.Error()
		}
		want := map[string]string{"k": "vw", "k2": `v\`, "x": "y",
			"k3": `v\w`, "k4": "a#b", "k5": "v"}
		for k, v := range want {
			if get(p, k) != v {
				return false, fmt.Sprintf("%s got %q want %q", k, get(p, k), v)
			}
		}
		return p.Len() == 6, fmt.Sprint(pairs(p))
	}})

	probes = append(probes, probe{"blank-continuation", func() (bool, string) {
		p, err := props.Load("k=a\\\n   \\\nb\n")
		if err != nil {
			return false, err.Error()
		}
		return get(p, "k") == "ab" && p.Len() == 1, fmt.Sprint(pairs(p))
	}})

	probes = append(probes, probe{"escapes", func() (bool, string) {
		p, err := props.Load(`a\=b=c` + "\n" + `k\ 2=v` + "\n" +
			`t=\t\n\r\f\q\=\ \u0041` + "\n")
		if err != nil {
			return false, err.Error()
		}
		ok := get(p, "a=b") == "c" && get(p, "k 2") == "v" &&
			get(p, "t") == "\t\n\r\fq= A"
		return ok, fmt.Sprint(pairs(p))
	}})

	probes = append(probes, probe{"unicode-error-position", func() (bool, string) {
		_, err := props.Load("k=a\nx=\\u12zz\n")
		pe := &props.PositionError{}
		if err == nil || !errors.As(err, &pe) {
			return false, fmt.Sprint(err)
		}
		ok := pe.Line == 2 && pe.Col == 3
		return ok, fmt.Sprintf("line=%d col=%d", pe.Line, pe.Col)
	}})

	probes = append(probes, probe{"duplicate-order", func() (bool, string) {
		p, err := props.Load("a=1\nb=2\na=3\n")
		if err != nil {
			return false, err.Error()
		}
		got := pairs(p)
		want := [][2]string{{"a", "3"}, {"b", "2"}}
		return reflect.DeepEqual(got, want), fmt.Sprint(got)
	}})

	probes = append(probes, probe{"store-specials", func() (bool, string) {
		q := props.New()
		q.Set("", "empty key")
		q.Set(" lead", "v")
		q.Set("k", " leading space")
		q.Set("#hash", "x")
		q.Set("eq=ua:tor", "#! 中\ttab\nnl")
		out := props.Store(q)
		p, err := props.Load(out)
		if err != nil {
			return false, err.Error()
		}
		return reflect.DeepEqual(pairs(p), pairs(q)), out
	}})

	probes = append(probes, probe{"roundtrip-1000", func() (bool, string) {
		alpha := []rune("ab =:#!\\\t\n\u0041\u4e2d")
		rng := rand.New(rand.NewSource(1))
		for n := 0; n < 1000; n++ {
			q := props.New()
			for j := 0; j < 8; j++ {
				k := randStr(rng, alpha)
				v := randStr(rng, alpha)
				q.Set(k, v)
			}
			p, err := props.Load(props.Store(q))
			if err != nil || !reflect.DeepEqual(pairs(p), pairs(q)) {
				return false, fmt.Sprintf("n=%d err=%v", n, err)
			}
		}
		return true, ""
	}})

	probes = append(probes, probe{"scan-check-budget", func() (bool, string) {
		var sb []byte
		for i := 0; i < 1024; i++ {
			line := make([]byte, 1024)
			for j := range line {
				line[j] = 'a'
			}
			line[1023] = '\\'
			sb = append(sb, line...)
			sb = append(sb, '\n')
		}
		src := string(sb)
		sc := logical.NewScanner(src)
		for {
			_, ok := sc.Next()
			if !ok {
				break
			}
		}
		limit := 2 * len(src)
		ok := sc.Checks() <= limit
		return ok, fmt.Sprintf("%d/%d", sc.Checks(), limit)
	}})

	failed := 0
	for _, p := range probes {
		ok, detail := p.check()
		status := "OK"
		if !ok {
			status = "FAIL"
			failed++
		}
		if ok {
			fmt.Printf("%s %s\n", status, p.name)
		} else {
			fmt.Printf("%s %s (%s)\n", status, p.name, detail)
		}
	}
	fmt.Printf("TOTAL %d/%d\n", len(probes)-failed, len(probes))
	if failed != 0 {
		fmt.Println("RESULT FAIL")
	}
}

func randStr(rng *rand.Rand, alpha []rune) string {
	n := rng.Intn(6)
	b := make([]rune, n)
	for i := range b {
		b[i] = alpha[rng.Intn(len(alpha))]
	}
	return string(b)
}
