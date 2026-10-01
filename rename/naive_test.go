package rename

import (
	"fmt"
	"math/rand"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// naiveFile is one input record for the independent reference implementation.
type naiveFile struct {
	path    string
	content []byte
}

func naiveCommonBytes(a, b []byte) int {
	ca := lineMultiset(a)
	cb := lineMultiset(b)
	common := 0
	for line, countA := range ca {
		countB := cb[line]
		n := countA
		if countB < n {
			n = countB
		}
		common += n * len(line)
	}
	return common
}

func lineMultiset(content []byte) map[string]int {
	counts := map[string]int{}
	start := 0
	for i, b := range content {
		if b == '\n' {
			counts[string(content[start:i+1])]++
			start = i + 1
		}
	}
	if start < len(content) {
		counts[string(content[start:])]++
	}
	return counts
}

func naiveSortPaths(files []naiveFile) {
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
}

// naiveDetect is an independent implementation of the same specification.
// Deliberately it performs NO size-ratio pruning: C is computed for every
// remaining delete/add combination in phase two.
func naiveDetect(threshold int, deletes, adds []naiveFile) *Result {
	// Phase one: group by identical content.
	contentGroups := map[string]struct {
		d []naiveFile
		a []naiveFile
	}{}
	for _, f := range deletes {
		g := contentGroups[string(f.content)]
		g.d = append(g.d, f)
		contentGroups[string(f.content)] = g
	}
	for _, f := range adds {
		g := contentGroups[string(f.content)]
		g.a = append(g.a, f)
		contentGroups[string(f.content)] = g
	}

	renames := []Rename{}
	usedDel := map[string]bool{}
	usedAdd := map[string]bool{}

	keys := make([]string, 0, len(contentGroups))
	for key := range contentGroups {
		keys = append(keys, key)
	}
	sort.Strings(keys) // group iteration order cannot affect the pairing.

	for _, key := range keys {
		group := contentGroups[key]
		// First: sub-groups by basename.
		subs := map[string]struct {
			d []naiveFile
			a []naiveFile
		}{}
		for _, f := range group.d {
			sub := subs[basename(f.path)]
			sub.d = append(sub.d, f)
			subs[basename(f.path)] = sub
		}
		for _, f := range group.a {
			sub := subs[basename(f.path)]
			sub.a = append(sub.a, f)
			subs[basename(f.path)] = sub
		}
		names := make([]string, 0, len(subs))
		for name := range subs {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			sub := subs[name]
			if len(sub.d) == 0 || len(sub.a) == 0 {
				continue
			}
			naiveSortPaths(sub.d)
			naiveSortPaths(sub.a)
			n := min(len(sub.d), len(sub.a))
			for i := 0; i < n; i++ {
				renames = append(renames, Rename{sub.d[i].path, sub.a[i].path, 100})
				usedDel[sub.d[i].path] = true
				usedAdd[sub.a[i].path] = true
			}
		}
		// Then: leftovers of the content group.
		var leftD, leftA []naiveFile
		for _, f := range group.d {
			if !usedDel[f.path] {
				leftD = append(leftD, f)
			}
		}
		for _, f := range group.a {
			if !usedAdd[f.path] {
				leftA = append(leftA, f)
			}
		}
		naiveSortPaths(leftD)
		naiveSortPaths(leftA)
		n := min(len(leftD), len(leftA))
		for i := 0; i < n; i++ {
			renames = append(renames, Rename{leftD[i].path, leftA[i].path, 100})
			usedDel[leftD[i].path] = true
			usedAdd[leftA[i].path] = true
		}
	}

	var remD, remA []naiveFile
	for _, f := range deletes {
		if !usedDel[f.path] {
			remD = append(remD, f)
		}
	}
	for _, f := range adds {
		if !usedAdd[f.path] {
			remA = append(remA, f)
		}
	}

	// Phase two: no pruning; compute C for the full Cartesian product.
	type cand struct {
		di, ai int
		score  int
	}
	var cands []cand
	for di, df := range remD {
		for ai, af := range remA {
			maxSize := max(len(df.content), len(af.content))
			if maxSize == 0 {
				continue
			}
			score := naiveCommonBytes(df.content, af.content) * 100 / maxSize
			if score >= threshold {
				cands = append(cands, cand{di, ai, score})
			}
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		x, y := cands[i], cands[j]
		if x.score != y.score {
			return x.score > y.score
		}
		sx := basename(remD[x.di].path) == basename(remA[x.ai].path)
		sy := basename(remD[y.di].path) == basename(remA[y.ai].path)
		if sx != sy {
			return sx
		}
		if remD[x.di].path != remD[y.di].path {
			return remD[x.di].path < remD[y.di].path
		}
		return remA[x.ai].path < remA[y.ai].path
	})
	takenD := map[string]bool{}
	takenA := map[string]bool{}
	for _, c := range cands {
		dp, ap := remD[c.di].path, remA[c.ai].path
		if takenD[dp] || takenA[ap] {
			continue
		}
		takenD[dp] = true
		takenA[ap] = true
		renames = append(renames, Rename{dp, ap, c.score})
	}

	sort.Slice(renames, func(i, j int) bool { return renames[i].Source < renames[j].Source })
	unpairedD := []string{}
	unpairedA := []string{}
	for _, f := range remD {
		if !takenD[f.path] {
			unpairedD = append(unpairedD, f.path)
		}
	}
	for _, f := range remA {
		if !takenA[f.path] {
			unpairedA = append(unpairedA, f.path)
		}
	}
	sort.Strings(unpairedD)
	sort.Strings(unpairedA)
	return &Result{Renames: renames, UnpairedDeleted: unpairedD, UnpairedAdded: unpairedA}
}

// randomInput generates one small random change with realistic duplicate
// basenames, identical contents, shared lines and occasional empty/huge files.
func randomInput(rng *rand.Rand) (threshold int, deletes, adds []naiveFile) {
	threshold = 1 + rng.Intn(100)
	linePool := []string{"a\n", "b\n", "c\n", "d\n", "x\n", "y\n", "z\n", "no-newline"}
	pickContent := func() []byte {
		switch rng.Intn(8) {
		case 0:
			return []byte{}
		case 1:
			return []byte("same-content\n")
		case 2:
			// Larger file to exercise size-ratio pruning against small files.
			return []byte(strings.Repeat("q\n", 60+rng.Intn(40)))
		}
		n := rng.Intn(6)
		lines := make([]string, n)
		for i := range lines {
			lines[i] = linePool[rng.Intn(len(linePool))]
		}
		return []byte(strings.Join(lines, ""))
	}

	used := map[string]bool{}
	mkPath := func(side string, i int) string {
		dir := rng.Intn(3)
		name := rng.Intn(3) // small name alphabet -> repeated basenames
		path := fmt.Sprintf("%s/d%d/f%d", side, dir, name)
		if used[path] {
			path = fmt.Sprintf("%s/d%d/f%d_%d", side, dir, name, i)
		}
		used[path] = true
		return path
	}

	nDel := rng.Intn(6)
	nAdd := rng.Intn(6)
	for i := 0; i < nDel; i++ {
		deletes = append(deletes, naiveFile{mkPath("del", i), pickContent()})
	}
	for i := 0; i < nAdd; i++ {
		adds = append(adds, naiveFile{mkPath("add", i), pickContent()})
	}
	return threshold, deletes, adds
}

func runDetector(threshold int, deletes, adds []naiveFile, shuffled bool) *Result {
	total := len(deletes) + len(adds)
	detector, _ := NewDetector(threshold, total+1)
	type reg struct {
		deleted bool
		file    naiveFile
	}
	regs := make([]reg, 0, total)
	for _, f := range deletes {
		regs = append(regs, reg{true, f})
	}
	for _, f := range adds {
		regs = append(regs, reg{false, f})
	}
	if shuffled {
		rand.New(rand.NewSource(int64(total*31))).Shuffle(len(regs), func(i, j int) {
			regs[i], regs[j] = regs[j], regs[i]
		})
	}
	for _, r := range regs {
		var err error
		if r.deleted {
			err = detector.RegisterDeleted(r.file.path, r.file.content)
		} else {
			err = detector.RegisterAdded(r.file.path, r.file.content)
		}
		if err != nil {
			panic(fmt.Sprintf("unexpected registration error: %v", err))
		}
	}
	return detector.Detect()
}

// expectedCommonCalls counts post-phase-one delete/add combinations whose
// size upper bound floor(min*100/max) is at least threshold: exactly those
// combinations may invoke the common-bytes computation.
func expectedCommonCalls(threshold int, deletes, adds []naiveFile) int {
	usedDel := map[string]bool{}
	usedAdd := map[string]bool{}
	// Replicate phase one only to learn which files enter phase two.
	contentGroups := map[string]struct{ d, a []naiveFile }{}
	for _, f := range deletes {
		g := contentGroups[string(f.content)]
		g.d = append(g.d, f)
		contentGroups[string(f.content)] = g
	}
	for _, f := range adds {
		g := contentGroups[string(f.content)]
		g.a = append(g.a, f)
		contentGroups[string(f.content)] = g
	}
	for _, g := range contentGroups {
		subs := map[string]struct{ d, a []naiveFile }{}
		for _, f := range g.d {
			sub := subs[basename(f.path)]
			sub.d = append(sub.d, f)
			subs[basename(f.path)] = sub
		}
		for _, f := range g.a {
			sub := subs[basename(f.path)]
			sub.a = append(sub.a, f)
			subs[basename(f.path)] = sub
		}
		for _, sub := range subs {
			naiveSortPaths(sub.d)
			naiveSortPaths(sub.a)
			n := min(len(sub.d), len(sub.a))
			for i := 0; i < n; i++ {
				usedDel[sub.d[i].path] = true
				usedAdd[sub.a[i].path] = true
			}
		}
		var leftD, leftA []naiveFile
		for _, f := range g.d {
			if !usedDel[f.path] {
				leftD = append(leftD, f)
			}
		}
		for _, f := range g.a {
			if !usedAdd[f.path] {
				leftA = append(leftA, f)
			}
		}
		naiveSortPaths(leftD)
		naiveSortPaths(leftA)
		n := min(len(leftD), len(leftA))
		for i := 0; i < n; i++ {
			usedDel[leftD[i].path] = true
			usedAdd[leftA[i].path] = true
		}
	}

	calls := 0
	for _, df := range deletes {
		if usedDel[df.path] {
			continue
		}
		for _, af := range adds {
			if usedAdd[af.path] {
				continue
			}
			maxSize := max(len(df.content), len(af.content))
			minSize := min(len(df.content), len(af.content))
			if maxSize > 0 && minSize*100/maxSize >= threshold {
				calls++
			}
		}
	}
	return calls
}

func runDetectorWithCalls(threshold int, deletes, adds []naiveFile) (*Result, int) {
	total := len(deletes) + len(adds)
	detector, _ := NewDetector(threshold, total+1)
	for _, f := range deletes {
		if err := detector.RegisterDeleted(f.path, f.content); err != nil {
			panic(err)
		}
	}
	for _, f := range adds {
		if err := detector.RegisterAdded(f.path, f.content); err != nil {
			panic(err)
		}
	}
	result := detector.Detect()
	return result, detector.commonBytesCalls
}

func formatInput(threshold int, deletes, adds []naiveFile) string {
	var b strings.Builder
	fmt.Fprintf(&b, "T=%d deletes=[", threshold)
	for i, f := range deletes {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%q=%q", f.path, f.content)
	}
	b.WriteString("] adds=[")
	for i, f := range adds {
		if i > 0 {
			b.WriteByte(' ')
		}
		fmt.Fprintf(&b, "%q=%q", f.path, f.content)
	}
	b.WriteByte(']')
	return b.String()
}

func formatResult(r *Result) string {
	var b strings.Builder
	fmt.Fprintf(&b, "renames=%v unpairedDel=%v unpairedAdd=%v", r.Renames, r.UnpairedDeleted, r.UnpairedAdded)
	return b.String()
}

// TestDifferential2000 runs 2000 random cases against the independent naive
// implementation (which never prunes), twice per case with different
// registration orders, and logs input / both outputs / verdict for each.
func TestDifferential2000(t *testing.T) {
	if !testing.Verbose() {
		t.Log("re-run with -v to see every input, output and verdict")
	}
	rng := rand.New(rand.NewSource(20261001))
	casesWithPruning := 0
	for iter := 0; iter < 2000; iter++ {
		threshold, deletes, adds := randomInput(rng)

		got, gotCalls := runDetectorWithCalls(threshold, deletes, adds)
		shuffled := runDetector(threshold, deletes, adds, true)
		want := naiveDetect(threshold,
			append([]naiveFile(nil), deletes...),
			append([]naiveFile(nil), adds...))
		wantCalls := expectedCommonCalls(threshold,
			append([]naiveFile(nil), deletes...),
			append([]naiveFile(nil), adds...))
		if wantCalls < len(naivePhaseTwoPairs(deletes, adds)) {
			casesWithPruning++
		}

		reason := "MATCH: optimized result == naive no-pruning result; shuffled registration identical"
		orderOK := reflect.DeepEqual(got, shuffled)
		diffOK := reflect.DeepEqual(got, want)
		callsOK := gotCalls == wantCalls
		if !orderOK || !diffOK || !callsOK {
			reason = fmt.Sprintf("MISMATCH: orderIndependent=%v differential=%v commonBytesCalls got=%d want=%d",
				orderOK, diffOK, gotCalls, wantCalls)
		}
		t.Logf("case %04d INPUT %s", iter, formatInput(threshold, deletes, adds))
		t.Logf("case %04d GOT  %s", iter, formatResult(got))
		t.Logf("case %04d SHUF %s", iter, formatResult(shuffled))
		t.Logf("case %04d NAIV %s", iter, formatResult(want))
		t.Logf("case %04d PRUNE commonBytesCalls=%d (unpruned combinations expected=%d)", iter, gotCalls, wantCalls)
		t.Logf("case %04d VERDICT %s", iter, reason)

		if !orderOK {
			t.Fatalf("case %d result depends on registration order", iter)
		}
		if !diffOK {
			t.Fatalf("case %d optimized result differs from naive", iter)
		}
		if !callsOK {
			t.Fatalf("case %d commonBytesCalls=%d want %d unpruned combinations", iter, gotCalls, wantCalls)
		}
	}
	t.Logf("pruning exercised in %d of 2000 random cases", casesWithPruning)
}

// naivePhaseTwoPairs returns the delete/add pairs that survive phase one;
// used only to report whether a random case exercised pruning.
func naivePhaseTwoPairs(deletes, adds []naiveFile) []struct{ d, a string } {
	usedDel := map[string]bool{}
	usedAdd := map[string]bool{}
	contentGroups := map[string]struct{ d, a []naiveFile }{}
	for _, f := range deletes {
		g := contentGroups[string(f.content)]
		g.d = append(g.d, f)
		contentGroups[string(f.content)] = g
	}
	for _, f := range adds {
		g := contentGroups[string(f.content)]
		g.a = append(g.a, f)
		contentGroups[string(f.content)] = g
	}
	for _, g := range contentGroups {
		subs := map[string]struct{ d, a []naiveFile }{}
		for _, f := range g.d {
			sub := subs[basename(f.path)]
			sub.d = append(sub.d, f)
			subs[basename(f.path)] = sub
		}
		for _, f := range g.a {
			sub := subs[basename(f.path)]
			sub.a = append(sub.a, f)
			subs[basename(f.path)] = sub
		}
		for _, sub := range subs {
			naiveSortPaths(sub.d)
			naiveSortPaths(sub.a)
			n := min(len(sub.d), len(sub.a))
			for i := 0; i < n; i++ {
				usedDel[sub.d[i].path] = true
				usedAdd[sub.a[i].path] = true
			}
		}
		var leftD, leftA []naiveFile
		for _, f := range g.d {
			if !usedDel[f.path] {
				leftD = append(leftD, f)
			}
		}
		for _, f := range g.a {
			if !usedAdd[f.path] {
				leftA = append(leftA, f)
			}
		}
		naiveSortPaths(leftD)
		naiveSortPaths(leftA)
		n := min(len(leftD), len(leftA))
		for i := 0; i < n; i++ {
			usedDel[leftD[i].path] = true
			usedAdd[leftA[i].path] = true
		}
	}
	var pairs []struct{ d, a string }
	for _, df := range deletes {
		if usedDel[df.path] {
			continue
		}
		for _, af := range adds {
			if usedAdd[af.path] {
				continue
			}
			pairs = append(pairs, struct{ d, a string }{df.path, af.path})
		}
	}
	return pairs
}
