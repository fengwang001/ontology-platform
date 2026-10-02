package hygiene

import (
	"math/rand"
	"strconv"
	"strings"
	"testing"
)

type rt struct {
	atom   bool
	text   string
	items  []*rt
	kind   int
	bindID int
	binder *rb
}

type rb struct {
	id   int
	base string
}

type rs struct {
	name string
	bind *rb
}

type rm struct {
	name     string
	params   []string
	template *rt
}

type rmodel struct {
	macros map[string]rm
	nextN  int
	x      int
}

const (
	rUser = iota
	rGlobal
	rBound
	rLiteral
)

func TestRandomNaiveComparison(t *testing.T) {
	rng := rand.New(rand.NewSource(20261002))
	for iter := 0; iter < 2000; iter++ {
		real := NewSession()
		model := &rmodel{macros: map[string]rm{}, nextN: 1}
		defs := []string{}
		macroNames := []string{}
		for i := 0; i < rng.Intn(5); i++ {
			name := macroName(i)
			params := macroParams(i)
			tmpl := randomTemplate(rng, name, params)
			defs = append(defs, name+" "+tmpl)
			errReal := real.DefMacro(name, params, tmpl)
			parsed, errParse := rParse(tmpl)
			var errModel error
			if errParse != nil {
				errModel = ErrInvalidDefinition
			}
			if !sameErr(errReal, errModel) {
				t.Fatalf("iter %d def mismatch %v/%v: %v", iter, errReal, errModel, defs)
			}
			if errReal == nil {
				model.macros[name] = rm{name: name, params: params, template: parsed}
				macroNames = append(macroNames, name)
			}
		}
		expr := randomExpr(rng, macroNames, 0)
		got, gotErr := real.Expand(expr)
		want, wantErr := model.expand(expr)
		if !sameErr(gotErr, wantErr) || got != want {
			t.Fatalf("iter %d\ninput=%s\ngot=%q (%v)\nwant=%q (%v)\ndefs=%v", iter, expr, got, gotErr, want, wantErr, defs)
		}
		if gotErr == nil {
			if real.N() != model.nextN-1 || real.X() != model.x {
				t.Fatalf("iter %d counters got N=%d X=%d want N=%d X=%d", iter, real.N(), real.X(), model.nextN-1, model.x)
			}
			if err := checkOutput(got); err != nil {
				t.Fatalf("iter %d output binder check: %v\n%s", iter, err, got)
			}
		}
		t.Logf("iter=%d input=%q output=%q rejected=%v N=%d X=%d basis=tagged-naive-simulation", iter, expr, got, gotErr, real.N(), real.X())
	}
}

func sameErr(a, b error) bool {
	return (a == nil) == (b == nil) && (a == nil || a.Error() == b.Error())
}

func macroName(i int) string {
	return string(rune('m'+i)) + "0"
}

func macroParams(i int) []string {
	switch i % 4 {
	case 0:
		return nil
	case 1:
		return []string{"a"}
	case 2:
		return []string{"a", "b"}
	default:
		return []string{"a", "b", "c"}
	}
}

func randomTemplate(rng *rand.Rand, name string, params []string) string {
	choices := []string{"z", "(quote (a b))", "(lam (t) t)", "(lam (t) (lam (t) t))", "(f g)", "(" + name + ")"}
	if len(params) > 0 {
		choices = append(choices, params[0], "(f "+params[0]+")", "(lam ("+params[0]+") "+params[0]+")")
	}
	return choices[rng.Intn(len(choices))]
}

func randomExpr(rng *rand.Rand, macros []string, depth int) string {
	if depth >= 3 || rng.Intn(4) == 0 {
		return randomAtom(rng, macros)
	}
	switch rng.Intn(6) {
	case 0:
		return "(lam (" + randomBinder(rng) + ") " + randomExpr(rng, macros, depth+1) + ")"
	case 1:
		return "(quote (m0 f t))"
	case 2:
		return "(" + randomAtom(rng, macros) + " " + randomExpr(rng, macros, depth+1) + ")"
	default:
		n := 1 + rng.Intn(3)
		parts := make([]string, n)
		for i := range parts {
			parts[i] = randomAtom(rng, macros)
		}
		return "(" + strings.Join(parts, " ") + ")"
	}
}

func randomAtom(rng *rand.Rand, macros []string) string {
	names := []string{"a", "b", "c", "f", "g", "t", "x", "y", "z", "1", "2", "+"}
	if len(macros) > 0 && rng.Intn(3) == 0 {
		return macros[rng.Intn(len(macros))]
	}
	return names[rng.Intn(len(names))]
}

func randomBinder(rng *rand.Rand) string {
	return []string{"t", "f", "g", "x", "y"}[rng.Intn(5)]
}

func (m *rmodel) expand(input string) (string, error) {
	root, err := rParse(input)
	if err != nil {
		return "", ErrInvalidForm
	}
	result, x, total, _, err := rProcess(m.macros, root, nil, m.nextN, 0, 0, 0, 0)
	if err != nil {
		return "", err
	}
	m.nextN += total
	m.x += x
	return rPrint(result), nil
}

var _ = strconv.Itoa
