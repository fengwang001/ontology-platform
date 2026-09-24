// Command demo prints check results for the content-disposition toolchain.
package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"ontology/filename"
	"ontology/paramjoin"
	"ontology/paramlex"
)

var failed bool

func check(label string, ok bool, detail string) {
	status := "PASS"
	if !ok {
		status = "FAIL"
		failed = true
	}
	fmt.Printf("%s %-20s %s\n", status, label, detail)
}

func mustJoin(header string) ([]paramjoin.Param, error) {
	_, items, err := paramlex.Parse(header)
	if err != nil {
		return nil, err
	}
	return paramjoin.Join(items)
}

func main() {
	// 1. quoted value keeps ';' and spaces, is not split apart.
	_, items, err := paramlex.Parse(`attachment; filename="a b;c.txt"`)
	ok := err == nil && len(items) == 1 && items[0].Name == "filename" && items[0].Value == "a b;c.txt"
	check("quoted-semicolon", ok, fmt.Sprint(items))

	// 2. utf-8 extended value decodes to a Chinese file name.
	p2, err := mustJoin(`attachment; filename*=utf-8''%E4%B8%AD%E6%96%87.txt`)
	ok = err == nil && len(p2) == 1 && p2[0].Value == "中文.txt" && p2[0].Ext
	check("utf8-ext-value", ok, fmt.Sprint(p2))

	// 3. three-section continuation joins in order without separators.
	p3, err := mustJoin(`attachment; n*0*=utf-8''hel; n*1=lo-; n*2=world`)
	ok = err == nil && len(p3) == 1 && p3[0].Value == "hello-world"
	check("continuation-join", ok, fmt.Sprint(p3))

	// 4. missing section 1 reports ErrIncomplete with Missing == 1.
	_, err = mustJoin(`attachment; n*0=a; n*2=c`)
	var inc *paramjoin.IncompleteError
	ok = errors.Is(err, paramjoin.ErrIncomplete) && errors.As(err, &inc) && inc.Missing == 1
	check("missing-section-1", ok, fmt.Sprint(err))

	// 5. gbk reports ErrCharset with Charset == "gbk".
	_, err = mustJoin(`attachment; filename*=gbk''%41`)
	var cs *paramjoin.CharsetError
	ok = errors.Is(err, paramjoin.ErrCharset) && errors.As(err, &cs) && cs.Charset == "gbk"
	check("unsupported-gbk", ok, fmt.Sprint(err))

	// 6. filename* beats plain filename regardless of order.
	h6 := `attachment; filename="plain.txt"; filename*=utf-8''star.txt`
	typ, items6, _ := paramlex.Parse(h6)
	params6, _ := paramjoin.Join(items6)
	name6 := filename.Choose(typ, params6)
	src := "filename"
	for _, p := range params6 {
		if p.Name == "filename" && p.Ext && p.Value == name6 {
			src = "filename*"
		}
	}
	check("ext-form-wins", name6 == "star.txt" && src == "filename*", name6+" (from "+src+")")

	// 7. path traversal is stripped: no separators, no ".." left.
	name7, err := filename.FromHeader(`attachment; filename="../../etc/passwd"`)
	ok = err == nil && !strings.ContainsAny(name7, `/\`) && !strings.Contains(name7, "..")
	check("traversal-stripped", ok, name7)

	// 8. "%2e%2e" and ".." both fall back to the same deterministic name;
	// NOTES.md invariant 3: they differ from real names only in stripped
	// parts, and Fallback is unreachable from unstripped input.
	n8a, _ := filename.FromHeader(`attachment; filename="%2e%2e"`)
	n8b, _ := filename.FromHeader(`attachment; filename=".."`)
	ok = n8a == filename.Fallback && n8b == filename.Fallback
	check("dot-fallback", ok, n8a+" / "+n8b)

	if failed {
		fmt.Println("SOME CHECKS FAILED")
		os.Exit(1)
	}
	fmt.Println("ALL OK")
}
