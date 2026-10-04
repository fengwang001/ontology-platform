package job_test

import (
	"errors"
	"testing"

	"ontology/job"
	"ontology/ladder"
)

func rungs3() []ladder.Rung {
	return []ladder.Rung{
		{Name: "360", Height: 360, Bitrate: 800, Required: true},
		{Name: "720", Height: 720, Bitrate: 2500, Required: true},
		{Name: "1080", Height: 1080, Bitrate: 5000},
	}
}

func find(t *testing.T, j *job.Job, name string) *job.Task {
	t.Helper()
	for _, task := range j.Snapshot() {
		if task.Rung.Name == name {
			return &task
		}
	}
	return nil
}

func TestStartFinishAndRetries(t *testing.T) {
	j := job.New("j", rungs3(), 2) // R=1
	if err := j.Start("360", 2); err != nil {
		t.Fatal(err)
	}
	if err := j.Start("1080", 2); err != nil {
		t.Fatal(err)
	}
	if err := j.Start("720", 2); !errors.Is(err, job.ErrBusy) {
		t.Fatalf("busy want ErrBusy, got %v", err)
	}
	if err := j.Start("360", 2); !errors.Is(err, job.ErrNotPending) {
		t.Fatalf("double start want ErrNotPending, got %v", err)
	}
	if err := j.Finish("1080", true, 100); err != nil {
		t.Fatal(err)
	}
	if err := j.Finish("1080", true, 100); !errors.Is(err, job.ErrNotRunning) {
		t.Fatalf("double finish want ErrNotRunning, got %v", err)
	}

	if err := j.Finish("360", true, 50); err != nil {
		t.Fatal(err)
	}
	// 720 第一次失败：attempt=1 <= R，回 Pending。
	if err := j.Start("720", 2); err != nil {
		t.Fatal(err)
	}
	if err := j.Finish("720", false, 0); err != nil {
		t.Fatal(err)
	}
	task := find(t, j, "720")
	if task.State != job.Pending || task.Attempt != 1 {
		t.Fatalf("want Pending attempt=1, got state=%d attempt=%d", task.State, task.Attempt)
	}
	if err := j.Start("720", 2); err != nil {
		t.Fatal(err)
	}
	if err := j.Finish("720", true, 70); err != nil {
		t.Fatal(err)
	}
	task = find(t, j, "720")
	if task.State != job.Done || task.Attempt != 2 || task.Size != 70 {
		t.Fatalf("want Done attempt=2 size=70, got %+v", task)
	}
	if !j.Done() || j.Failed() {
		t.Fatalf("job should be done and not failed")
	}
}

func TestRequiredFailure(t *testing.T) {
	j := job.New("j", rungs3(), 2)
	startFinishFail := func(name string, times int) {
		for i := 0; i < times; i++ {
			if err := j.Start(name, 16); err != nil {
				t.Fatal(err)
			}
			if err := j.Finish(name, false, 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	startFinishFail("360", 2)
	if !j.Failed() {
		t.Fatal("required failure must mark job failed")
	}
	if j.Done() {
		t.Fatal("failed job is not done while other rungs pending")
	}
	// 仍在 Running 的档可 Finish：先把 1080 跑起来。
	if err := j.Start("1080", 16); err != nil {
		t.Fatal(err)
	}
	if err := j.Finish("1080", true, 9); err != nil {
		t.Fatalf("running rung still finishable: %v", err)
	}
}

func TestOptionalFailureAllowsDone(t *testing.T) {
	j := job.New("j", rungs3(), 1) // R=0
	for _, name := range []string{"360", "720"} {
		if err := j.Start(name, 16); err != nil {
			t.Fatal(err)
		}
		if err := j.Finish(name, true, 1); err != nil {
			t.Fatal(err)
		}
	}
	if err := j.Start("1080", 16); err != nil {
		t.Fatal(err)
	}
	if err := j.Finish("1080", false, 0); err != nil {
		t.Fatal(err)
	}
	task := find(t, j, "1080")
	if task.State != job.Failed || task.Attempt != 1 {
		t.Fatalf("R=0 first failure must be Failed, got %+v", task)
	}
	if j.Failed() {
		t.Fatal("optional failure must not fail the job")
	}
	if !j.Done() {
		t.Fatal("all terminal with no required failure => done")
	}
}

func TestRejections(t *testing.T) {
	j := job.New("j", rungs3(), 1)
	if err := j.Start("", 2); !errors.Is(err, job.ErrInvalidArgument) {
		t.Fatalf("empty rung: %v", err)
	}
	if err := j.Start("nope", 2); !errors.Is(err, job.ErrRungNotFound) {
		t.Fatalf("missing rung: %v", err)
	}
	if err := j.Finish("360", true, 0); !errors.Is(err, job.ErrNotRunning) {
		t.Fatalf("finish pending: %v", err)
	}
	if err := j.Finish("360", true, -1); !errors.Is(err, job.ErrInvalidArgument) {
		t.Fatalf("bad size: %v", err)
	}
	if err := j.Start("360", 0); !errors.Is(err, job.ErrBusy) {
		t.Fatalf("limit 0 busy: %v", err)
	}
	task := find(t, j, "360")
	if task.State != job.Pending || task.Attempt != 0 {
		t.Fatalf("rejected ops must not change state, got %+v", task)
	}
}
