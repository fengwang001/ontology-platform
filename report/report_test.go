package report

import (
	"math/rand"
	"strings"
	"testing"
)

func build(cols, paths []string) *Report {
	r := New()
	r.AddPruned(cols...)
	for _, p := range paths {
		r.AddRef("secret", p)
	}
	r.AddRef("salary", "CMP")
	r.SetRejected(true)
	return r
}

func TestDeterministic(t *testing.T) {
	cols := []string{"salary", "secret", "ssn", "bonus"}
	paths := []string{"CMP", "AND[0]/CMP", "OR[1]/NOT/CMP"}
	base := build(cols, paths).String()
	for trial := 0; trial < 20; trial++ {
		pc := append([]string(nil), cols...)
		rand.Shuffle(len(pc), func(i, j int) { pc[i], pc[j] = pc[j], pc[i] })
		pp := append([]string(nil), paths...)
		rand.Shuffle(len(pp), func(i, j int) { pp[i], pp[j] = pp[j], pp[i] })
		if got := build(pc, pp).String(); got != base {
			t.Fatalf("trial %d: report differs:\n%s\nwant:\n%s", trial, got, base)
		}
	}
}

func TestContent(t *testing.T) {
	r := New()
	r.AddPruned("secret", "salary", "secret")
	r.AddRef("secret", "OR[1]/CMP")
	r.AddRef("secret", "CMP")
	r.AddRef("secret", "CMP")
	r.SetRejected(true)
	if got := strings.Join(r.Pruned(), ","); got != "salary,secret" {
		t.Errorf("pruned = %q, want sorted unique salary,secret", got)
	}
	refs := r.Refs()
	if len(refs) != 1 || refs[0].Col != "secret" ||
		strings.Join(refs[0].Paths, ";") != "CMP;OR[1]/CMP" {
		t.Errorf("refs = %+v, want one secret ref with sorted deduped paths", refs)
	}
	if !r.Rejected() {
		t.Error("rejected flag lost")
	}
	want := "rejected=true\npruned=salary,secret\nref=secret@CMP;OR[1]/CMP\n"
	if got := r.String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
	empty := New()
	if empty.Rejected() || len(empty.Pruned()) != 0 || len(empty.Refs()) != 0 {
		t.Error("zero report must be empty and not rejected")
	}
}
