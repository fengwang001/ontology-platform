package ontology

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

type randOp struct {
	name    string
	a, b    string
	content string
}

func validName(s string) bool {
	return s != "" && s != "." && s != ".." && !strings.ContainsAny(s, "/")
}

// genPath builds paths from names that frequently coincide with existing
// lower or merged nodes, so conflicts/whiteouts are exercised.
func genPath(rng *rand.Rand, names []string) string {
	depth := rng.Intn(4)
	if depth == 0 {
		return ""
	}
	parts := make([]string, depth)
	for i := range parts {
		parts[i] = names[rng.Intn(len(names))]
	}
	return strings.Join(parts, "/")
}

func TestRandomAgainstNaive(t *testing.T) {
	if testing.Verbose() {
		t.Logf("running 2000 random operation sequences against the naive model")
	}
	for seed := int64(0); seed < 2000; seed++ {
		rng := rand.New(rand.NewSource(seed))
		names := []string{"a", "b", "c", "d", "x", "y", "z", "p", "q", "g"}
		// Random lower tree: pick a few valid directory/file keys with
		// guaranteed directory closure.
		lower := map[string]string{}
		for _, n := range names {
			if rng.Intn(2) == 0 {
				lower[n+"/"] = ""
				used := map[string]bool{}
				for k := 0; k < 2; k++ {
					child := names[rng.Intn(len(names))]
					p := n + "/" + child
					if used[child] {
						continue
					}
					used[child] = true
					if rng.Intn(2) == 0 {
						lower[p+"/"] = ""
						if rng.Intn(2) == 0 {
							g := names[rng.Intn(len(names))]
							lower[p+"/"+g] = "deep"
						}
					} else {
						lower[p] = "L:" + p
					}
				}
			} else if rng.Intn(2) == 0 {
				lower[n] = "L:" + n
			}
		}
		v, err := New(lower)
		if err != nil {
			t.Fatalf("seed %d: New rejected valid lower: %v (%v)", seed, lower, err)
		}
		m := newNaive(lower)

		var log strings.Builder
		fmt.Fprintf(&log, "seed=%d lower=%v\n", seed, lower)

		ops := 30 + rng.Intn(40)
		for i := 0; i < ops; i++ {
			var o randOp
			switch rng.Intn(7) {
			case 0:
				o = randOp{"lookup", genPath(rng, names), "", ""}
				if rng.Intn(10) == 0 {
					o.a = "../bad"
				}
			case 1:
				o = randOp{"readdir", genPath(rng, names), "", ""}
			case 2:
				o = randOp{"mkdir", genPathNonRoot(rng, names), "", ""}
			case 3:
				p := genPathNonRoot(rng, names)
				o = randOp{"write", p, "", fmt.Sprintf("v%d", rng.Intn(5))}
			case 4:
				o = randOp{"remove", genPathNonRoot(rng, names), "", ""}
			case 5:
				o = randOp{"rename", genPathNonRoot(rng, names), genPathNonRoot(rng, names), ""}
			default:
				// Invalid path shape injection.
				bad := []string{"", "/x", "x/", "a//b", "a/../b", "x/./y"}
				names2 := []string{"mkdir", "write", "remove", "lookup", "readdir", "rename"}
				nm := names2[rng.Intn(len(names2))]
				p := bad[rng.Intn(len(bad))]
				if nm == "rename" {
					o = randOp{nm, p, genPathNonRoot(rng, names), ""}
				} else {
					o = randOp{nm, p, "", "z"}
				}
			}
			fmt.Fprintf(&log, " op %d %s a=%q b=%q c=%q\n", i, o.name, o.a, o.b, o.content)

			var gotErr error
			var gotLookup LookupResult
			var gotDirs []string
			switch o.name {
			case "lookup":
				r, e := v.Lookup(o.a)
				gotErr, gotLookup = e, r
			case "readdir":
				gotDirs, gotErr = v.ReadDir(o.a)
			case "mkdir":
				gotErr = v.Mkdir(o.a)
			case "write":
				gotErr = v.Write(o.a, o.content)
			case "remove":
				gotErr = v.Remove(o.a)
			case "rename":
				gotErr = v.Rename(o.a, o.b)
			}

			var wantErr error
			var wantLookup LookupResult
			var wantDirs []string
			switch o.name {
			case "lookup":
				wantErr = withValidLookup(m, o.a, &wantLookup)
			case "readdir":
				wantDirs, wantErr = naiveReadDir(m, o.a)
			case "mkdir":
				wantErr = m.mkdir(o.a)
			case "write":
				wantErr = m.write(o.a, o.content)
			case "remove":
				wantErr = m.remove(o.a)
			case "rename":
				wantErr = m.rename(o.a, o.b)
			}
			fmt.Fprintf(&log, "    got: err=%s\n", errName(gotErr))
			fmt.Fprintf(&log, "   want: err=%s\n", errName(wantErr))
			if !sameErr(gotErr, wantErr) {
				t.Fatalf("seed %d op %d %s a=%q b=%q error mismatch\ngot=%v\nwant=%v\n%s",
					seed, i, o.name, o.a, o.b, errName(gotErr), errName(wantErr), log.String())
			}
			if o.name == "lookup" && gotErr == nil {
				if !reflect.DeepEqual(gotLookup, wantLookup) {
					t.Fatalf("seed %d lookup mismatch got=%+v want=%+v\n%s", seed, gotLookup, wantLookup, log.String())
				}
			}
			if o.name == "readdir" && gotErr == nil {
				if !reflect.DeepEqual(gotDirs, wantDirs) {
					t.Fatalf("seed %d readdir mismatch got=%v want=%v\n%s", seed, gotDirs, wantDirs, log.String())
				}
			}
			if !reflect.DeepEqual(v.Upper(), m.snapshot()) {
				t.Fatalf("seed %d upper mismatch after op %d %s\nGOT=%v\nWANT=%v\n%s",
					seed, i, o.name, dumpUpper(v.Upper()), dumpUpper(m.snapshot()), log.String())
			}
			// ReadDir invariant: listed lookups succeed, unlisted fail.
			if o.name == "readdir" && gotErr == nil {
				prefix := o.a
				if prefix != "" {
					prefix += "/"
				}
				for _, n := range gotDirs {
					if _, e := v.Lookup(prefix + n); e != nil {
						t.Fatalf("listed %s not lookupable: %v\n%s", n, e, log.String())
					}
				}
			}
		}
		if testing.Verbose() && seed < 3 {
			t.Log("\n" + log.String())
		}
	}
}

func genPathNonRoot(rng *rand.Rand, names []string) string {
	for {
		p := genPath(rng, names)
		if p != "" {
			return p
		}
	}
}

func withValidLookup(m *naiveModel, p string, out *LookupResult) error {
	if !isValidPath(p, true) {
		return ErrInvalidPath
	}
	if p == "" {
		out.Type = TypeDirectory
		return nil
	}
	if err := m.checkAncestors(p); err != nil {
		return err
	}
	t, c := m.resolve(p)
	switch t {
	case "missing":
		return ErrNotFound
	case "file":
		*out = LookupResult{Type: TypeFile, Content: c}
	default:
		*out = LookupResult{Type: TypeDirectory}
	}
	return nil
}

func naiveReadDir(m *naiveModel, p string) ([]string, error) {
	if !isValidPath(p, true) {
		return nil, ErrInvalidPath
	}
	if p == "" {
		return m.children(""), nil
	}
	if err := m.checkAncestors(p); err != nil {
		return nil, err
	}
	t, _ := m.resolve(p)
	switch t {
	case "missing":
		return nil, ErrNotFound
	case "file":
		return nil, ErrNotDirectory
	default:
		return m.children(p), nil
	}
}

func sameErr(a, b error) bool {
	return errName(a) == errName(b)
}

func dumpUpper(up map[string]UpperRecord) string {
	keys := make([]string, 0, len(up))
	for k := range up {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		r := up[k]
		if r.Kind == KindFile {
			parts = append(parts, fmt.Sprintf("%s=%s(%q)", k, r.Kind, r.Content))
		} else {
			parts = append(parts, k+"="+r.Kind)
		}
	}
	return "{" + strings.Join(parts, ", ") + "}"
}
