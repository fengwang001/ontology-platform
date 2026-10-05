package dag_test

import (
	"errors"
	"fmt"
	"testing"

	"ontology/dag"
)

func job(name string, needs ...string) dag.Job {
	return dag.Job{Name: name, Needs: needs, When: "on_success"}
}

func TestNewValidationOrder(t *testing.T) {
	cases := []struct {
		name string
		jobs []dag.Job
		want error
	}{
		{"no jobs", nil, dag.ErrInvalidArgument},
		{"empty name", []dag.Job{{Name: "", When: "on_success"}}, dag.ErrInvalidArgument},
		{"bad when", []dag.Job{{Name: "a", When: "sometimes"}}, dag.ErrInvalidArgument},
		{"negative retry", []dag.Job{{Name: "a", When: "always", Retry: -1}}, dag.ErrInvalidArgument},
		{"retry too large", []dag.Job{{Name: "a", When: "always", Retry: 3}}, dag.ErrInvalidArgument},
		{
			"invalid beats duplicate",
			[]dag.Job{{Name: "a", When: "bad"}, {Name: "a", When: "always"}},
			dag.ErrInvalidArgument,
		},
		{
			"duplicate beats unknown dependency",
			[]dag.Job{job("a", "ghost"), job("a")},
			dag.ErrDuplicateName,
		},
		{
			"unknown dependency beats cycle",
			[]dag.Job{job("a", "b"), job("b", "a"), job("c", "ghost")},
			dag.ErrUnknownDependency,
		},
		{
			"self cycle",
			[]dag.Job{job("a", "a")},
			dag.ErrCycle,
		},
		{
			"indirect cycle",
			[]dag.Job{job("a", "c"), job("b", "a"), job("c", "b")},
			dag.ErrCycle,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := dag.New(c.jobs)
			if !errors.Is(err, c.want) {
				t.Fatalf("New() error = %v; want errors.Is %v", err, c.want)
			}
		})
	}
}

func TestNewJobCountLimit(t *testing.T) {
	jobs := make([]dag.Job, dag.MaxJobs+1)
	for i := range jobs {
		jobs[i] = dag.Job{Name: fmt.Sprintf("j%d", i), When: "always"}
	}
	if _, err := dag.New(jobs); !errors.Is(err, dag.ErrInvalidArgument) {
		t.Fatalf("expected ErrInvalidArgument for %d jobs, got %v", len(jobs), err)
	}
	if _, err := dag.New(jobs[:dag.MaxJobs]); err != nil {
		t.Fatalf("expected %d jobs to pass, got %v", dag.MaxJobs, err)
	}
}

func TestDownstream(t *testing.T) {
	g, err := dag.New([]dag.Job{
		job("build"),
		job("test", "build"),
		job("lint", "build"),
		job("deploy", "test", "lint"),
		job("notify", "test"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := g.DirectDownstream("build"); fmt.Sprint(got) != "[test lint]" {
		t.Fatalf("DirectDownstream(build) = %v", got)
	}
	closure := g.TransitiveDownstream("build")
	want := map[string]bool{"test": true, "lint": true, "deploy": true, "notify": true}
	if fmt.Sprint(closure) != fmt.Sprint(want) {
		t.Fatalf("TransitiveDownstream(build) = %v; want %v", closure, want)
	}
	if got := g.TransitiveDownstream("deploy"); len(got) != 0 {
		t.Fatalf("TransitiveDownstream(deploy) = %v; want empty", got)
	}
	if got := g.TransitiveDownstream("ghost"); len(got) != 0 {
		t.Fatalf("TransitiveDownstream(ghost) = %v; want empty", got)
	}
}
