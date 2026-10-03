package notify

import (
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

var fuzzLangs = []string{
	"en", "zh", "zh-Hant", "zh-TW", "zh-Hant-TW", "fr", "fr-CA", "de-Latn-DE",
}

var fuzzBadLangs = []string{"e", "EN", "zh-hant", "de-1996", "en-US-X"}

var fuzzBodies = []string{
	"hello",
	"Hi {name}",
	"{a}{b}{c}",
	"{a|def}",
	"{a|}",
	"{{literal}}",
	"{{ {v} }}",
	"你好{name}，{code|0000}",
	"<&>\"{x}",
	"{a}}",
	"}bad{",
	"{}",
	strings.Repeat("好", 10),
}

var fuzzChannels = []string{"email", "sms", "push", "*"}
var fuzzConcrete = []string{"email", "sms", "push"}
var fuzzVars = []string{"name", "code", "a", "b", "c", "x", "v"}
var fuzzValues = []string{"", "Li", "你好", `<&>"`, "000000", strings.Repeat("字", 40)}

type opResult struct {
	kind    string
	desc    string
	outcome string
	reason  string
}

func TestFuzzAgainstOracle2000(t *testing.T) {
	const iterations = 2000
	rng := rand.New(rand.NewSource(20261003))
	var log []string

	for iter := 0; iter < iterations; iter++ {
		dl := fuzzLangs[rng.Intn(3)]
		store := New(dl)
		oracle := newOracle(dl)
		nOps := 1 + rng.Intn(12)
		var iterLog []string
		iterLog = append(iterLog, fmt.Sprintf("== iter=%d dl=%s", iter, dl))

		for op := 0; op < nOps; op++ {
			switch rng.Intn(3) {
			case 0, 1:
				retire := rng.Intn(4) == 0
				name := pickName(rng)
				loc := pickMaybeBadLang(rng)
				ch := fuzzChannels[rng.Intn(len(fuzzChannels))]
				eff := int64(rng.Intn(6))
				if retire {
					e1 := store.Retire(name, loc, ch, eff)
					e2 := oracle.retire(name, loc, ch, eff)
					assertSameError(t, e1, e2)
					iterLog = append(iterLog, fmt.Sprintf("Retire(%s,%s,%s,%d) -> %s",
						name, loc, ch, eff, errorLabel(e1)))
				} else {
					body := fuzzBodies[rng.Intn(len(fuzzBodies))]
					if rng.Intn(15) == 0 {
						body = strings.Repeat("x", 1001)
					}
					e1 := store.Publish(name, loc, ch, body, eff)
					e2 := oracle.publish(name, loc, ch, body, eff)
					assertSameError(t, e1, e2)
					iterLog = append(iterLog, fmt.Sprintf("Publish(%s,%s,%s,%q,%d) -> %s",
						name, loc, ch, body, eff, errorLabel(e1)))
				}
			case 2:
				name := pickName(rng)
				loc := pickMaybeBadLang(rng)
				ch := fuzzConcrete[rng.Intn(3)]
				at := int64(rng.Intn(7))
				vars := map[string]string{}
				for _, v := range fuzzVars {
					switch rng.Intn(3) {
					case 0:
						vars[v] = fuzzValues[rng.Intn(len(fuzzValues))]
					}
				}
				if rng.Intn(10) == 0 {
					vars["Bad-Key"] = "x"
				}
				r1, e1 := store.Render(name, loc, ch, at, vars)
				o2 := oracle.render(name, loc, ch, at, vars)
				assertSameOutcome(t, r1, e1, o2)
				iterLog = append(iterLog, fmt.Sprintf("Render(%s,%s,%s,%d,%v) -> %s",
					name, loc, ch, at, vars, describeOutcome(r1, e1)))
			}
		}
		log = append(log, strings.Join(iterLog, "\n"))
	}
	// 日志打印全部输入、输出与判定依据（默认输出，-v 时直接可见；
	// 失败时 testing 也会保留输出）。
	t.Logf("fuzz log (%d iterations):\n%s", iterations, strings.Join(log, "\n"))
}

func pickName(rng *rand.Rand) string {
	names := []string{"welcome", "n", "a.b_c", "t1", "BAD", "x-y", strings.Repeat("n", 33), ""}
	return names[rng.Intn(len(names))]
}

func pickMaybeBadLang(rng *rand.Rand) string {
	if rng.Intn(6) == 0 {
		return fuzzBadLangs[rng.Intn(len(fuzzBadLangs))]
	}
	return fuzzLangs[rng.Intn(len(fuzzLangs))]
}

func errorLabel(e *Error) string {
	if e == nil {
		return "accepted"
	}
	switch e.Reason {
	case ReasonSyntax:
		return fmt.Sprintf("rejected:syntax@%d", e.Offset)
	case ReasonMissingVars:
		return fmt.Sprintf("rejected:missing%v", e.Missing)
	case ReasonTooLong:
		return fmt.Sprintf("rejected:long(%d)", e.Codepoints)
	default:
		return "rejected:" + reasonString(e.Reason)
	}
}

func describeOutcome(r *Result, e *Error) string {
	if e != nil {
		return errorLabel(e)
	}
	if r == nil {
		return "accepted"
	}
	return fmt.Sprintf("text=%q loc=%s ch=%s eff=%d", r.Text, r.Loc, r.ChannelKey, r.Eff)
}

func assertSameError(t *testing.T, e1, e2 *Error) {
	t.Helper()
	if (e1 == nil) != (e2 == nil) {
		t.Fatalf("error mismatch: %v vs %v", e1, e2)
	}
	if e1 == nil {
		return
	}
	if e1.Reason != e2.Reason || e1.Offset != e2.Offset || e1.Codepoints != e2.Codepoints {
		t.Fatalf("error detail mismatch: %+v vs %+v", e1, e2)
	}
	if strings.Join(e1.Missing, ",") != strings.Join(e2.Missing, ",") {
		t.Fatalf("missing mismatch: %v vs %v", e1.Missing, e2.Missing)
	}
}

func assertSameOutcome(t *testing.T, r *Result, e *Error, o oracleOutcome) {
	t.Helper()
	if e != nil || o.err != nil {
		assertSameError(t, e, o.err)
		return
	}
	if r.Text != o.text || r.Loc != o.loc || r.ChannelKey != o.ck || r.Eff != o.eff {
		t.Fatalf("result mismatch: %+v vs %+v", r, o)
	}
}
