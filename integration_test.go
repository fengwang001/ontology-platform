package ontology_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"ontology/ckpt"
	"ontology/parse"
	"ontology/sink"
	"ontology/source"
	"ontology/stage"
)

func cfg(n int, out, ck string) stage.Config {
	return stage.Config{
		Src: source.NewGen(source.GenConfig{N: n, BadEvery: 17}, 0),
		OutDir: out, CkptDir: ck, QueueCap: 2, Epoch: 10,
		Workers: 2, SinkDelay: int64(time.Microsecond),
	}
}

func run(t *testing.T, c stage.Config) stage.Report {
	t.Helper()
	p := stage.NewPipe(c)
	return p.Run(context.Background())
}

func baseline(t *testing.T, n int) []byte {
	t.Helper()
	r := run(t, cfg(n, t.TempDir(), t.TempDir()))
	if r.Err != nil {
		t.Fatalf("baseline err: %v", r.Err)
	}
	return r.OutputBytes
}

func recoverAndCompare(t *testing.T, n int, crash stage.CrashAt) {
	t.Helper()
	want := baseline(t, n)
	out, ckdir := t.TempDir(), t.TempDir()
	c := cfg(n, out, ckdir)
	c.Crash = crash
	r1 := run(t, c)
	if !errors.Is(r1.Err, stage.ErrCrashed) {
		t.Fatalf("first run err=%v want ErrCrashed", r1.Err)
	}
	c2 := cfg(n, out, ckdir)
	r2 := run(t, c2)
	if r2.Err != nil {
		t.Fatalf("recovery err: %v", r2.Err)
	}
	if !bytes.Equal(r2.OutputBytes, want) {
		t.Fatalf("recovery output differs:\n got=%q\nwant=%q", r2.OutputBytes, want)
	}
	if removed, _ := sink.CleanupTemp(out); removed != 0 {
		t.Fatalf("temp files remain: %d", removed)
	}
}

func TestCrashMomentsTable(t *testing.T) {
	cases := []struct {
		name  string
		crash stage.CrashAt
	}{
		{"source drained queue nonempty", stage.CrashAt{SourceDrained: true}},
		{"mid aggregate", stage.CrashAt{MidAggregate: true}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recoverAndCompare(t, 300, tc.crash)
		})
	}
	t.Run("sink tmp pre rename subprocess", func(t *testing.T) {
		testPreRenameSubprocess(t)
	})
}

func testPreRenameSubprocess(t *testing.T) {
	if os.Getenv("ONTOLOGY_PRERENAME") == "1" {
		dir := os.Getenv("ONTOLOGY_DIR")
		c := cfg(300, filepath.Join(dir, "out"), filepath.Join(dir, "ck"))
		c.Crash.PreRename = true
		stage.NewPipe(c).Run(context.Background())
		return
	}
	dir := t.TempDir()
	cmd := exec.Command(os.Args[0], "-test.run=TestCrashMomentsTable/sink_tmp_pre_rename_subprocess")
	cmd.Env = append(os.Environ(), "ONTOLOGY_PRERENAME=1", "ONTOLOGY_DIR="+dir)
	cmd.Run() // 子进程应被 os.Exit(88) 终止
	if n, _ := sink.CleanupTemp(filepath.Join(dir, "out")); n == 0 {
		t.Fatal("expected leftover .tmp from hard crash")
	}
	want := baseline(t, 300)
	c := cfg(300, filepath.Join(dir, "out"), filepath.Join(dir, "ck"))
	r := run(t, c)
	if r.Err != nil {
		t.Fatalf("recovery err: %v", r.Err)
	}
	if !bytes.Equal(r.OutputBytes, want) {
		t.Fatal("pre-rename recovery output differs from baseline")
	}
	if n, _ := sink.CleanupTemp(filepath.Join(dir, "out")); n != 0 {
		t.Fatalf("temp not cleaned after recovery: %d", n)
	}
}

func TestBackpressure(t *testing.T) {
	out, ckdir := t.TempDir(), t.TempDir()
	c := stage.Config{
		Src: source.NewGen(source.GenConfig{N: 50000}, 0),
		OutDir: out, CkptDir: ckdir, QueueCap: 4, Epoch: 200,
		Workers: 8, SinkDelay: int64(time.Millisecond),
	}
	p := stage.NewPipe(c)
	r := p.Run(context.Background())
	if r.Err != nil {
		t.Fatalf("err: %v", r.Err)
	}
	if p.Blocked() == 0 {
		t.Fatal("source was never blocked")
	}
	if r.MaxInFlight > p.MaxInFlightBound() {
		t.Fatalf("max in flight %d > bound %d", r.MaxInFlight, p.MaxInFlightBound())
	}
	if r.MaxInFlight >= 50000 {
		t.Fatal("no backpressure: in flight approached total")
	}
}

func TestFaultsAndBoundariesTable(t *testing.T) {
	cases := []struct {
		name string
		conf func(out, ck string) stage.Config
		want func(t *testing.T, r stage.Report)
	}{
		{
			"zero records", func(o, k string) stage.Config {
				c := cfg(0, o, k)
				return c
			}, func(t *testing.T, r stage.Report) {
				if r.Err != nil || r.Pos != 0 {
					t.Fatalf("r=%+v", r)
				}
			},
		},
		{
			"single record", func(o, k string) stage.Config { return cfg(1, o, k) },
			func(t *testing.T, r stage.Report) {
				if r.Err != nil || !strings.Contains(string(r.OutputBytes), "a=1,1") {
					t.Fatalf("r=%+v", r)
				}
			},
		},
		{
			"all same group", func(o, k string) stage.Config {
				c := cfg(50, o, k)
				c.Src = sameGroup(50)
				return c
			}, func(t *testing.T, r stage.Report) {
				if r.Err != nil || strings.Count(string(r.OutputBytes), "\n") != 2 {
					t.Fatalf("r=%+v bytes=%q", r, r.OutputBytes)
				}
			},
		},
		{
			"queue cap 1", func(o, k string) stage.Config {
				c := cfg(60, o, k)
				c.QueueCap, c.Epoch = 1, 1
				return c
			}, func(t *testing.T, r stage.Report) {
				if r.Err != nil {
					t.Fatalf("r=%+v", r.Err)
				}
			},
		},
		{
			"source errors midstream", func(o, k string) stage.Config {
				c := cfg(100, o, k)
				c.Src = source.NewGen(source.GenConfig{N: 100, ErrAt: 40}, 0)
				return c
			}, func(t *testing.T, r stage.Report) {
				if !errors.Is(r.Err, source.ErrEnd) || r.Pos < 30 {
					t.Fatalf("r=%+v", r)
				}
			},
		},
		{
			"source ends immediately", func(o, k string) stage.Config {
				c := cfg(10, o, k)
				c.Src = source.NewGen(source.GenConfig{N: 10, EndAfter: 0}, 0)
				return c
			}, func(t *testing.T, r stage.Report) {
				if r.Err != nil || r.Pos != -1 {
					t.Fatalf("r=%+v", r.Err)
				}
			},
		},
		{
			"bad records counted", func(o, k string) stage.Config {
				c := cfg(100, o, k)
				c.Src = source.NewGen(source.GenConfig{N: 100, BadEvery: 10}, 0)
				return c
			}, func(t *testing.T, r stage.Report) {
				if r.Err != nil || r.Bad != 10 {
					t.Fatalf("bad=%d err=%v", r.Bad, r.Err)
				}
			},
		},
		{
			"group hard limit", func(o, k string) stage.Config {
				c := cfg(200, o, k)
				c.MaxGroups = 2
				return c
			}, func(t *testing.T, r stage.Report) {
				if !errors.Is(r.Err, stage.ErrTooManyGroups) {
					t.Fatalf("r=%+v", r.Err)
				}
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.want(t, run(t, tc.conf(t.TempDir(), t.TempDir())))
		})
	}
}

func sameGroup(n int) *source.Gen {
	return source.NewGen(source.GenConfig{N: n}, 0)
}

func TestCheckpointFallbackEndToEnd(t *testing.T) {
	out, ckdir := t.TempDir(), t.TempDir()
	c := cfg(100, out, ckdir)
	r1 := run(t, c)
	if r1.Err != nil || r1.FellBack {
		t.Fatalf("r1=%+v", r1)
	}
	store := ckpt.NewStore(ckdir)
	store.Save(ckpt.State{Pos: 200, Groups: map[string]ckpt.Group{}})
	if err := store.Corrupt(1); err != nil {
		t.Fatal(err)
	}
	c2 := cfg(100, out, ckdir)
	c2.Src = source.NewGen(source.GenConfig{N: 100}, r1.Pos)
	r2 := run(t, c2)
	if !r2.FellBack {
		t.Fatal("expected fallback flag in report")
	}
}

func TestStopGoroutineBaseline(t *testing.T) {
	base := runtime.NumGoroutine()
	c := stage.Config{
		Src: source.NewGen(source.GenConfig{N: 100000, Rate: time.Microsecond}, 0),
		OutDir: t.TempDir(), CkptDir: t.TempDir(),
		QueueCap: 2, Epoch: 20, Workers: 2,
		SinkDelay: int64(100 * time.Microsecond),
	}
	p := stage.NewPipe(c)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); p.Run(context.Background()) }()
	time.Sleep(20 * time.Millisecond)
	p.Stop()
	p.Stop() // 幂等
	wg.Wait()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && runtime.NumGoroutine() > base+1 {
		time.Sleep(10 * time.Millisecond)
	}
	if runtime.NumGoroutine() > base+1 {
		t.Fatalf("goroutines leaked: %d > baseline %d",
			runtime.NumGoroutine(), base)
	}

	// 启动前到达停止信号：Run 立即结束，无悬挂 goroutine。
	p2 := stage.NewPipe(cfg(10, t.TempDir(), t.TempDir()))
	p2.Stop()
	r := p2.Run(context.Background())
	if r.MaxInFlight != 0 {
		t.Fatal("pre-stopped pipe must not process anything")
	}
}

func TestParseAndSinkUnitsTable(t *testing.T) {
	t.Run("parse", func(t *testing.T) {
		rows := []struct {
			data string
			ok   bool
			key  string
		}{
			{"a=1", true, "a"}, {"=2", true, ""}, {"x", false, ""},
			{"a=", false, ""}, {"b=z", false, ""}, {"k=1=2", false, ""},
		}
		ps := &parse.Parser{}
		for i, r := range rows {
			_, ok := ps.Parse(source.Frame{Pos: int64(i), Data: []byte(r.data)})
			if ok != r.ok {
				t.Fatalf("%q ok=%v want %v", r.data, ok, r.ok)
			}
		}
		if ps.Bad != 4 {
			t.Fatalf("bad=%d want 4", ps.Bad)
		}
	})
	t.Run("sink truncation every byte", func(t *testing.T) {
		dir := t.TempDir()
		payload := sink.Encode([]sink.Row{{Key: "a", Sum: 1, Cnt: 1},
			{Key: "b", Sum: 2, Cnt: 2}})
		for n := 0; n <= len(payload); n++ {
			tmp := filepath.Join(dir, "out-1.tmp")
			if err := os.WriteFile(tmp, payload[:n], 0o644); err != nil {
				t.Fatal(err)
			}
			if sink.Verify(tmp) == nil {
				t.Fatalf("truncation %d accepted", n)
			}
			rm, _ := sink.CleanupTemp(dir)
			if rm != 1 {
				t.Fatalf("n=%d removed=%d", n, rm)
			}
		}
		sk := sink.New(dir, 0, nil, -1)
		path, err := sk.Commit(9, []sink.Row{{Key: "a", Sum: 1, Cnt: 1}})
		if err != nil || sink.Verify(path) != nil {
			t.Fatalf("commit/verify: %v %v", path, err)
		}
})
}

func init() {
	// 避免未使用 fmt（保留给子进程标记扩展）。
	_ = fmt.Sprint
}
