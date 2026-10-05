package ontology

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"ontology/job"
	"ontology/ladder"
	"ontology/manifest"
)

func rg(name string, h, b int, req bool) ladder.Rung {
	return ladder.Rung{Name: name, Height: h, Bitrate: b, Required: req}
}

func mustTemplate(t *testing.T, rungs []ladder.Rung, r, c int) ladder.Template {
	t.Helper()
	tmpl, err := ladder.NewTemplate(rungs, r, c)
	if err != nil {
		t.Fatalf("NewTemplate: %v", err)
	}
	return tmpl
}

// 例一模板：360/800/必需、720/2500/必需、1080/5000、2160/16000。
func exampleRungs() []ladder.Rung {
	return []ladder.Rung{
		rg("360p", 360, 800, true),
		rg("720p", 720, 2500, true),
		rg("1080p", 1080, 5000, false),
		rg("2160p", 2160, 16000, false),
	}
}

func mustSubmit(t *testing.T, s *Service, now int64, id string, h, b int) {
	t.Helper()
	if err := s.Submit(now, id, h, b); err != nil {
		t.Fatalf("Submit(%s): %v", id, err)
	}
}

func mustStart(t *testing.T, s *Service, now int64, id, rung string) {
	t.Helper()
	if err := s.Start(now, id, rung); err != nil {
		t.Fatalf("Start(%s,%s): %v", id, rung, err)
	}
}

func mustFinish(t *testing.T, s *Service, now int64, id, rung string, ok bool, size int64) {
	t.Helper()
	if err := s.Finish(now, id, rung, ok, size); err != nil {
		t.Fatalf("Finish(%s,%s): %v", id, rung, err)
	}
}

func entryNames(es []manifest.Entry) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = e.Name
	}
	return out
}

func TestTemplateValidation(t *testing.T) {
	valid := exampleRungs()
	cases := []struct {
		name  string
		rungs []ladder.Rung
		r, c  int
	}{
		{"零档", nil, 1, 1},
		{"空名", []ladder.Rung{rg("", 360, 800, true)}, 1, 1},
		{"重名", []ladder.Rung{rg("a", 360, 800, true), rg("a", 720, 900, false)}, 1, 1},
		{"高度零", []ladder.Rung{rg("a", 0, 800, true)}, 1, 1},
		{"高度超界", []ladder.Rung{rg("a", 4321, 800, true)}, 1, 1},
		{"码率零", []ladder.Rung{rg("a", 360, 0, true)}, 1, 1},
		{"码率超界", []ladder.Rung{rg("a", 360, 100_000_001, true)}, 1, 1},
		{"高度不递增", []ladder.Rung{rg("a", 720, 800, true), rg("b", 720, 900, false)}, 1, 1},
		{"无必需档", []ladder.Rung{rg("a", 360, 800, false)}, 1, 1},
		{"R为负", valid, -1, 1},
		{"R超界", valid, 6, 1},
		{"C为零", valid, 1, 0},
		{"C超界", valid, 1, 17},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ladder.NewTemplate(tc.rungs, tc.r, tc.c); !errors.Is(err, ladder.ErrInvalidTemplate) {
				t.Fatalf("got %v, want ErrInvalidTemplate", err)
			}
		})
	}
}

func TestLadderDerive(t *testing.T) {
	tmpl := mustTemplate(t, exampleRungs(), 1, 2)
	cases := []struct {
		name         string
		srcH, srcB   int
		wantErr      error
		wantNames    []string
		wantBitrates []int
	}{
		{"例一a源1080码率3000", 1080, 3000, nil,
			[]string{"360p", "720p", "1080p"}, []int{800, 2500, 3000}},
		{"例一b钳制后相等被丢弃", 1080, 2500, nil,
			[]string{"360p", "720p"}, []int{800, 2500}},
		{"例一c必需档第三步被丢弃", 720, 600, nil,
			[]string{"360p"}, []int{600}},
		{"例一d源高480必需档第一步被丢弃", 480, 5000, ladder.ErrSourceInsufficient, nil, nil},
		{"高度恰等保留", 720, 2500, nil,
			[]string{"360p", "720p"}, []int{800, 2500}},
		{"源高恰等最高档", 2160, 20000, nil,
			[]string{"360p", "720p", "1080p", "2160p"}, []int{800, 2500, 5000, 16000}},
		{"码率恰等不钳制", 2160, 16000, nil,
			[]string{"360p", "720p", "1080p", "2160p"}, []int{800, 2500, 5000, 16000}},
		{"码率极低仅留最低档", 2160, 800, nil,
			[]string{"360p"}, []int{800}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lad, err := tmpl.Derive(tc.srcH, tc.srcB)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				return
			}
			var names []string
			var bitrates []int
			for _, r := range lad.Rungs() {
				names = append(names, r.Name)
				bitrates = append(bitrates, r.Bitrate)
			}
			if !reflect.DeepEqual(names, tc.wantNames) {
				t.Fatalf("names = %v, want %v", names, tc.wantNames)
			}
			if !reflect.DeepEqual(bitrates, tc.wantBitrates) {
				t.Fatalf("bitrates = %v, want %v", bitrates, tc.wantBitrates)
			}
		})
	}
}

// TestSubmitRejectionOrder 参数非法 > 时钟回退 > 作业已存在 > 源不足。
func TestSubmitRejectionOrder(t *testing.T) {
	newSvc := func() *Service {
		s := NewService(mustTemplate(t, exampleRungs(), 1, 2))
		mustSubmit(t, s, 100, "a", 1080, 3000)
		return s
	}
	cases := []struct {
		name       string
		now        int64
		id         string
		srcH, srcB int
		want       error
	}{
		{"空id且时钟回退报参数非法", 50, "", 1080, 3000, ErrInvalidParam},
		{"源高越界且作业已存在报参数非法", 200, "a", 0, 3000, ErrInvalidParam},
		{"源码率越界报参数非法", 200, "b", 1080, 100_000_001, ErrInvalidParam},
		{"now越界报参数非法", 1_000_000_000_001, "b", 1080, 3000, ErrInvalidParam},
		{"时钟回退优先于作业已存在", 50, "a", 1080, 3000, ErrClockSkew},
		{"时钟回退优先于源不足", 50, "b", 480, 3000, ErrClockSkew},
		{"作业已存在优先于源不足", 200, "a", 480, 3000, ErrJobExists},
		{"源不足", 200, "b", 480, 3000, ladder.ErrSourceInsufficient},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSvc()
			if err := s.Submit(tc.now, tc.id, tc.srcH, tc.srcB); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			// 被拒操作不改时钟：now=150 仍应被接受。
			if err := s.Submit(150, "c", 1080, 3000); err != nil {
				t.Fatalf("时钟被被拒操作修改: %v", err)
			}
		})
	}
}

// TestStartRejectionOrder 参数非法 > 时钟回退 > 作业不存在 > 作业已终止 >
// 档不在阶梯 > 状态不符 > 繁忙。
func TestStartRejectionOrder(t *testing.T) {
	tmpl := mustTemplate(t, exampleRungs(), 0, 1)

	t.Run("参数非法优先于时钟回退", func(t *testing.T) {
		s := NewService(tmpl)
		mustSubmit(t, s, 100, "a", 1080, 3000)
		if err := s.Start(50, "", "360p"); !errors.Is(err, ErrInvalidParam) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("时钟回退优先于作业不存在", func(t *testing.T) {
		s := NewService(tmpl)
		mustSubmit(t, s, 100, "a", 1080, 3000)
		if err := s.Start(50, "ghost", "360p"); !errors.Is(err, ErrClockSkew) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("作业不存在", func(t *testing.T) {
		s := NewService(tmpl)
		mustSubmit(t, s, 100, "a", 1080, 3000)
		if err := s.Start(200, "ghost", "360p"); !errors.Is(err, ErrJobNotFound) {
			t.Fatalf("err = %v", err)
		}
	})
	t.Run("作业已终止优先于档不在阶梯", func(t *testing.T) {
		s := NewService(tmpl)
		mustSubmit(t, s, 100, "a", 720, 600) // 阶梯只剩 360p
		mustStart(t, s, 200, "a", "360p")
		mustFinish(t, s, 300, "a", "360p", true, 10) // 作业完成
		if err := s.Start(400, "a", "2160p"); !errors.Is(err, job.ErrTerminated) {
			t.Fatalf("err = %v, want ErrTerminated", err)
		}
	})
	t.Run("档不在阶梯优先于状态不符", func(t *testing.T) {
		s := NewService(tmpl)
		mustSubmit(t, s, 100, "a", 720, 600) // 2160p 不在阶梯
		if err := s.Start(200, "a", "2160p"); !errors.Is(err, job.ErrRungNotFound) {
			t.Fatalf("err = %v, want ErrRungNotFound", err)
		}
	})
	t.Run("状态不符优先于繁忙", func(t *testing.T) {
		s := NewService(tmpl) // C=1
		mustSubmit(t, s, 100, "a", 1080, 3000)
		mustStart(t, s, 200, "a", "360p")
		if err := s.Start(300, "a", "360p"); !errors.Is(err, job.ErrBadState) {
			t.Fatalf("err = %v, want ErrBadState", err)
		}
	})
	t.Run("繁忙", func(t *testing.T) {
		s := NewService(tmpl) // C=1
		mustSubmit(t, s, 100, "a", 1080, 3000)
		mustStart(t, s, 200, "a", "360p")
		if err := s.Start(300, "a", "720p"); !errors.Is(err, job.ErrBusy) {
			t.Fatalf("err = %v, want ErrBusy", err)
		}
		// 被拒的 Start 不加 attempt。
		if got := s.jobs["a"].job.Attempt("720p"); got != 0 {
			t.Fatalf("attempt = %d, want 0", got)
		}
	})
}

// TestFinishRejectionOrder 参数非法 > 时钟回退 > 作业不存在 > 档不在阶梯 > 状态不符。
func TestFinishRejectionOrder(t *testing.T) {
	tmpl := mustTemplate(t, exampleRungs(), 1, 2)
	newSvc := func() *Service {
		s := NewService(tmpl)
		mustSubmit(t, s, 100, "a", 1080, 3000)
		return s
	}
	cases := []struct {
		name string
		now  int64
		id   string
		rung string
		ok   bool
		size int64
		want error
	}{
		{"size越界且时钟回退报参数非法", 50, "a", "360p", true, 1_000_000_000_001, ErrInvalidParam},
		{"size为负报参数非法", 200, "a", "360p", true, -1, ErrInvalidParam},
		{"时钟回退优先于作业不存在", 50, "ghost", "360p", true, 1, ErrClockSkew},
		{"作业不存在优先于档不在阶梯", 200, "ghost", "2160p", true, 1, ErrJobNotFound},
		{"档不在阶梯优先于状态不符", 200, "a", "2160p", true, 1, job.ErrRungNotFound},
		{"状态不符", 200, "a", "360p", true, 1, job.ErrBadState},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newSvc()
			if err := s.Finish(tc.now, tc.id, tc.rung, tc.ok, tc.size); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if err := s.Submit(150, "c", 1080, 3000); err != nil {
				t.Fatalf("时钟被被拒操作修改: %v", err)
			}
		})
	}
}

// 例二模板：360 必需、720 必需、1080 可选，R=1，C=2。
func example2Svc(t *testing.T) *Service {
	t.Helper()
	tmpl := mustTemplate(t, []ladder.Rung{
		rg("360p", 360, 800, true),
		rg("720p", 720, 2500, true),
		rg("1080p", 1080, 5000, false),
	}, 1, 2)
	s := NewService(tmpl)
	mustSubmit(t, s, 0, "v", 1080, 5000)
	return s
}

func TestExample2(t *testing.T) {
	s := example2Svc(t)
	mustStart(t, s, 1, "v", "360p")
	mustStart(t, s, 2, "v", "1080p")
	if err := s.Start(3, "v", "720p"); !errors.Is(err, job.ErrBusy) {
		t.Fatalf("Start 720p err = %v, want ErrBusy", err)
	}
	mustFinish(t, s, 4, "v", "1080p", true, 900)
	// 360p 未到终态，L 为空，未就绪。
	if _, _, err := s.Get("v"); !errors.Is(err, manifest.ErrNotReady) {
		t.Fatalf("Get err = %v, want ErrNotReady", err)
	}
	mustFinish(t, s, 5, "v", "360p", true, 100)
	// L={360p}，但 720p 为必需且不在 L，仍未就绪。
	if _, _, err := s.Get("v"); !errors.Is(err, manifest.ErrNotReady) {
		t.Fatalf("Get err = %v, want ErrNotReady", err)
	}
	mustStart(t, s, 6, "v", "720p")
	mustFinish(t, s, 7, "v", "720p", false, 0) // attempt=1 <= R，回到 Pending
	if st, _ := s.jobs["v"].job.StateOf("720p"); st != job.Pending {
		t.Fatalf("720p state = %v, want Pending", st)
	}
	mustStart(t, s, 8, "v", "720p")
	mustFinish(t, s, 9, "v", "720p", true, 300)
	num, list, err := s.Get("v")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if num != 1 {
		t.Fatalf("version = %d, want 1", num)
	}
	want := []manifest.Entry{
		{Name: "360p", Height: 360, Bitrate: 800, Size: 100, Required: true},
		{Name: "720p", Height: 720, Bitrate: 2500, Size: 300, Required: true},
		{Name: "1080p", Height: 1080, Bitrate: 5000, Size: 900},
	}
	if !reflect.DeepEqual(list, want) {
		t.Fatalf("manifest = %+v, want %+v", list, want)
	}
	// 作业完成后 Start 报已终止。
	if err := s.Start(10, "v", "360p"); !errors.Is(err, job.ErrTerminated) {
		t.Fatalf("Start err = %v, want ErrTerminated", err)
	}
}

func TestExample2RequiredFinalFail(t *testing.T) {
	s := example2Svc(t)
	mustStart(t, s, 1, "v", "360p")
	mustStart(t, s, 2, "v", "1080p")
	mustFinish(t, s, 3, "v", "1080p", true, 900)
	mustFinish(t, s, 4, "v", "360p", true, 100)
	mustStart(t, s, 5, "v", "720p")
	mustFinish(t, s, 6, "v", "720p", false, 0) // 回 Pending
	mustStart(t, s, 7, "v", "720p")
	mustFinish(t, s, 8, "v", "720p", false, 0) // attempt=2 = R+1，Failed
	if st, _ := s.jobs["v"].job.StateOf("720p"); st != job.Failed {
		t.Fatalf("720p state = %v, want Failed", st)
	}
	if _, _, err := s.Get("v"); !errors.Is(err, ErrJobFailed) {
		t.Fatalf("Get err = %v, want ErrJobFailed", err)
	}
	// 未就绪与作业失败可区分。
	if errors.Is(ErrJobFailed, manifest.ErrNotReady) {
		t.Fatal("ErrJobFailed 与 ErrNotReady 不可区分")
	}
}

// R=0 时首次失败即 Failed。
func TestRetryZero(t *testing.T) {
	tmpl := mustTemplate(t, []ladder.Rung{
		rg("360p", 360, 800, true),
		rg("720p", 720, 2500, false),
	}, 0, 2)
	s := NewService(tmpl)
	mustSubmit(t, s, 0, "v", 720, 2500)
	mustStart(t, s, 1, "v", "720p")
	mustFinish(t, s, 2, "v", "720p", false, 0)
	if st, _ := s.jobs["v"].job.StateOf("720p"); st != job.Failed {
		t.Fatalf("720p state = %v, want Failed", st)
	}
	// 可选档 Failed 不使作业失败；360p Done 后发首版。
	mustStart(t, s, 3, "v", "360p")
	mustFinish(t, s, 4, "v", "360p", true, 50)
	num, list, err := s.Get("v")
	if err != nil || num != 1 {
		t.Fatalf("Get = %d, %v", num, err)
	}
	if got := entryNames(list); !reflect.DeepEqual(got, []string{"360p"}) {
		t.Fatalf("manifest = %v", got)
	}
}

// 较高档先完成不发版；可选档终败解除阻塞使其上方已 Done 档进入 L 而增版。
func TestFrontierBlockingAndUnblock(t *testing.T) {
	tmpl := mustTemplate(t, []ladder.Rung{
		rg("360p", 360, 800, true),
		rg("720p", 720, 2500, false),
		rg("1080p", 1080, 5000, false),
	}, 0, 3)
	s := NewService(tmpl)
	mustSubmit(t, s, 0, "v", 1080, 5000)
	mustStart(t, s, 1, "v", "360p")
	mustStart(t, s, 2, "v", "720p")
	mustStart(t, s, 3, "v", "1080p")
	mustFinish(t, s, 4, "v", "1080p", true, 900) // 被 360p/720p 阻塞
	mustFinish(t, s, 5, "v", "360p", true, 100)  // L={360p}，发第 1 版
	num, list, err := s.Get("v")
	if err != nil || num != 1 {
		t.Fatalf("Get = %d, %v", num, err)
	}
	if got := entryNames(list); !reflect.DeepEqual(got, []string{"360p"}) {
		t.Fatalf("v1 = %v", got)
	}
	mustFinish(t, s, 6, "v", "720p", false, 0) // 可选档终败，解除对 1080p 的阻塞
	num, list, err = s.Get("v")
	if err != nil || num != 2 {
		t.Fatalf("Get = %d, %v, want version 2", num, err)
	}
	if got := entryNames(list); !reflect.DeepEqual(got, []string{"360p", "1080p"}) {
		t.Fatalf("v2 = %v", got)
	}
	// 历史版本仍可取，越界报版本不存在。
	v1, err := s.GetVersion("v", 1)
	if err != nil {
		t.Fatalf("GetVersion(1): %v", err)
	}
	if got := entryNames(v1); !reflect.DeepEqual(got, []string{"360p"}) {
		t.Fatalf("GetVersion(1) = %v", got)
	}
	if _, err := s.GetVersion("v", 3); !errors.Is(err, manifest.ErrVersionNotFound) {
		t.Fatalf("GetVersion(3) err = %v", err)
	}
	if _, err := s.GetVersion("v", 0); !errors.Is(err, manifest.ErrVersionNotFound) {
		t.Fatalf("GetVersion(0) err = %v", err)
	}
}

// 作业失败后仍在 Running 的档照常接受 Finish 并记录结果，但不再发版。
func TestFailedJobFinishAcceptedNoPublish(t *testing.T) {
	tmpl := mustTemplate(t, []ladder.Rung{
		rg("360p", 360, 800, true),
		rg("720p", 720, 2500, true),
		rg("1080p", 1080, 5000, false),
	}, 0, 3)
	s := NewService(tmpl)
	mustSubmit(t, s, 0, "v", 1080, 5000)
	mustStart(t, s, 1, "v", "360p")
	mustStart(t, s, 2, "v", "720p")
	mustStart(t, s, 3, "v", "1080p")
	mustFinish(t, s, 4, "v", "360p", false, 0) // 必需档终败，作业失败
	mustFinish(t, s, 5, "v", "720p", true, 300)
	mustFinish(t, s, 6, "v", "1080p", true, 900)
	if sz, _ := s.jobs["v"].job.Size("720p"); sz != 300 {
		t.Fatalf("720p size = %d, want 300", sz)
	}
	if _, _, err := s.Get("v"); !errors.Is(err, ErrJobFailed) {
		t.Fatalf("Get err = %v, want ErrJobFailed", err)
	}
	if _, err := s.GetVersion("v", 1); !errors.Is(err, ErrJobFailed) {
		t.Fatalf("GetVersion err = %v, want ErrJobFailed", err)
	}
	if err := s.Start(7, "v", "360p"); !errors.Is(err, job.ErrTerminated) {
		t.Fatalf("Start err = %v, want ErrTerminated", err)
	}
}

// 被拒操作不改任何状态，含时钟与 attempt。
func TestRejectedOpsKeepState(t *testing.T) {
	tmpl := mustTemplate(t, exampleRungs(), 1, 1)
	s := NewService(tmpl)
	mustSubmit(t, s, 100, "a", 1080, 3000)
	mustStart(t, s, 200, "a", "360p") // attempt(360p)=1，时钟=200

	// 状态不符的 Start 被拒：attempt 不变。
	if err := s.Start(300, "a", "360p"); !errors.Is(err, job.ErrBadState) {
		t.Fatalf("err = %v", err)
	}
	if got := s.jobs["a"].job.Attempt("360p"); got != 1 {
		t.Fatalf("attempt = %d, want 1", got)
	}
	// 繁忙的 Start 被拒：目标档 attempt 不变。
	if err := s.Start(400, "a", "720p"); !errors.Is(err, job.ErrBusy) {
		t.Fatalf("err = %v", err)
	}
	if got := s.jobs["a"].job.Attempt("720p"); got != 0 {
		t.Fatalf("attempt = %d, want 0", got)
	}
	// 时钟回退的 Finish 被拒：时钟不被抬高，状态不变。
	if err := s.Finish(150, "a", "360p", true, 10); !errors.Is(err, ErrClockSkew) {
		t.Fatalf("err = %v", err)
	}
	// 作业不存在的 Submit 冲突被拒后，时钟仍为 200。
	if err := s.Submit(500, "a", 1080, 3000); !errors.Is(err, ErrJobExists) {
		t.Fatalf("err = %v", err)
	}
	if err := s.Finish(250, "a", "360p", true, 10); err != nil {
		t.Fatalf("时钟被被拒操作修改: %v", err)
	}
}

// touched 计数器证明 Get 与 Finish 触碰的作业记录数为 1，与作业总数无关。
func TestTouchedCounter(t *testing.T) {
	for _, n := range []int{100, 10000} {
		t.Run(fmt.Sprintf("jobs=%d", n), func(t *testing.T) {
			tmpl := mustTemplate(t, []ladder.Rung{rg("360p", 360, 800, true)}, 0, 1)
			s := NewService(tmpl)
			for i := 0; i < n; i++ {
				mustSubmit(t, s, int64(i), fmt.Sprintf("j%d", i), 360, 800)
			}
			mustStart(t, s, int64(n), "j7", "360p")

			s.touched = 0
			if _, _, err := s.Get("j7"); !errors.Is(err, manifest.ErrNotReady) {
				t.Fatalf("Get err = %v", err)
			}
			if s.touched != 1 {
				t.Fatalf("Get touched = %d, want 1", s.touched)
			}

			s.touched = 0
			mustFinish(t, s, int64(n)+1, "j7", "360p", true, 10)
			if s.touched != 1 {
				t.Fatalf("Finish touched = %d, want 1", s.touched)
			}

			s.touched = 0
			if _, _, err := s.Get("j7"); err != nil {
				t.Fatalf("Get: %v", err)
			}
			if s.touched != 1 {
				t.Fatalf("Get touched = %d, want 1", s.touched)
			}
		})
	}
}

// 并发调用等价于某个串行顺序：跑完后用同一时钟重放关键不变量。
func TestConcurrent(t *testing.T) {
	tmpl := mustTemplate(t, []ladder.Rung{
		rg("360p", 360, 800, true),
		rg("720p", 720, 2500, false),
	}, 1, 2)
	s := NewService(tmpl)
	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			id := fmt.Sprintf("j%d", w)
			for i := 0; i < 50; i++ {
				now := int64(w*1000 + i)
				_ = s.Submit(now, id, 720, 2500)
				_ = s.Start(now, id, "360p")
				_ = s.Start(now, id, "720p")
				_ = s.Finish(now, id, "360p", i%3 != 0, int64(i))
				_ = s.Finish(now, id, "720p", true, int64(i))
				_, _, _ = s.Get(id)
				_, _ = s.GetVersion(id, 1)
			}
		}(w)
	}
	wg.Wait()
	// 不变量：Running 档数不超过 C；已发版作业不会失败。
	for id, rec := range s.jobs {
		if rec.job.Running() > tmpl.Concurrency() {
			t.Fatalf("%s: running = %d > C", id, rec.job.Running())
		}
		if _, err := rec.manifest.Latest(); err == nil && rec.job.Failed() {
			t.Fatalf("%s: 发版后作业失败", id)
		}
	}
}
