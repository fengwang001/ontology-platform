package speqtest_test

import (
	"bufio"
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"ontology/speq"
	"ontology/speqtest/naive"
)

type opKind int

const (
	opRegDev opKind = iota
	opRegSV
	opRegPG
	opInspect
	opSeal
	opUnseal
	opMount
	opUse
	opScrap
	opWarn
)

func setupCats(s *speq.System, m *naive.Model) {
	cats := []struct {
		speq.CategoryConfig
		n naive.Category
	}{
		{
			CategoryConfig: speq.CategoryConfig{
				Code: "B", Kind: speq.KindDevice, PeriodMonths: 12,
				EarlyWindowDays: 30, MinUnsealDays: 10, WarningLeadDays: 15,
			},
			n: naive.Category{Code: "B", Kind: naive.KDevice, PeriodM: 12, WindowD: 30, MinUnseal: 10, WarnLead: 15},
		},
		{
			CategoryConfig: speq.CategoryConfig{
				Code: "P", Kind: speq.KindDevice, PeriodMonths: 24,
				EarlyWindowDays: 60, MinUnsealDays: 20, WarningLeadDays: 30,
			},
			n: naive.Category{Code: "P", Kind: naive.KDevice, PeriodM: 24, WindowD: 60, MinUnseal: 20, WarnLead: 30},
		},
		{
			CategoryConfig: speq.CategoryConfig{
				Code: "SV", Kind: speq.KindSafetyValve, PeriodMonths: 12,
				EarlyWindowDays: 30, MinUnsealDays: 5, WarningLeadDays: 10,
			},
			n: naive.Category{Code: "SV", Kind: naive.KSV, PeriodM: 12, WindowD: 30, MinUnseal: 5, WarnLead: 10},
		},
		{
			CategoryConfig: speq.CategoryConfig{
				Code: "PG", Kind: speq.KindPressureGauge, PeriodMonths: 6,
				EarlyWindowDays: 15, MinUnsealDays: 5, WarningLeadDays: 7,
			},
			n: naive.Category{Code: "PG", Kind: naive.KPG, PeriodM: 6, WindowD: 15, MinUnseal: 5, WarnLead: 7},
		},
	}
	for _, c := range cats {
		if err := s.AddCategory(c.CategoryConfig); err != nil {
			panic(err)
		}
		if err := m.AddCategory(c.n); err != nil {
			panic(err)
		}
	}
}

type universe struct {
	all, devs, attaches []string
}

func speqCode(err error) int {
	var oe *speq.OpError
	if errors.As(err, &oe) {
		return int(oe.Code)
	}
	var re *speq.RejectError
	if errors.As(err, &re) {
		return 100 + int(re.Reason)
	}
	return -1
}

func naiveCode(err error) int {
	var oe *naive.Err
	if errors.As(err, &oe) {
		return int(oe.Code)
	}
	var re *naive.UseError
	if errors.As(err, &re) {
		return 100 + int(re.Reason)
	}
	return -1
}

func TestDifferentialRandom(t *testing.T) {
	const runs = 40
	const opsPerRun = 1500
	logPath := filepath.Join("..", "testlogs", "diff_random.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatal(err)
	}
	lf, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	defer lf.Close()
	w := bufio.NewWriter(lf)
	defer w.Flush()

	for run := 0; run < runs; run++ {
		seed := int64(1000 + run)
		rng := rand.New(rand.NewSource(seed))
		s := speq.New()
		m := naive.New()
		setupCats(s, m)
		date := 0
		counter := 0
		var uni universe

		fmt.Fprintf(w, "==== RUN %d seed=%d ====\n", run, seed)

		for step := 0; step < opsPerRun; step++ {
			// 日期以非降为主，偶尔小回退以触发日期回退分支。
			if rng.Intn(5) != 0 {
				date += rng.Intn(20)
			} else if date > 0 {
				date -= 1 + rng.Intn(3)
				if date < 0 {
					date = 0
				}
			}

			kind := opKind(rng.Intn(int(opWarn) + 1))
			var input, basis string
			var errS, errM error
			var expS, expM int

			switch kind {
			case opRegDev, opRegSV, opRegPG:
				counter++
				id := fmt.Sprintf("%s%04d", map[opKind]string{
					opRegDev: "D", opRegSV: "V", opRegPG: "G"}[kind], counter)
				cat := map[opKind]string{opRegDev: "B", opRegSV: "SV", opRegPG: "PG"}[kind]
				first := date - rng.Intn(40)
				if first < 0 {
					first = 0
				}
				// 偶尔复用已存在编号制造重复登记。
				if rng.Intn(20) == 0 && len(uni.all) > 0 {
					id = uni.all[rng.Intn(len(uni.all))]
				}
				input = fmt.Sprintf("REGISTER kind=%v date=%d id=%s cat=%s first=%d", kind, date, id, cat, first)
				if kind == opRegDev {
					expS, errS = s.RegisterDevice(date, id, cat, first)
					expM, errM = m.RegisterDevice(date, id, cat, first)
				} else {
					k := speq.KindSafetyValve
					kn := naive.KSV
					if kind == opRegPG {
						k, kn = speq.KindPressureGauge, naive.KPG
					}
					expS, errS = s.RegisterAttachment(date, id, cat, k, first)
					expM, errM = m.RegisterAttachment(date, id, cat, kn, first)
				}
				if errS == nil {
					uni.all = append(uni.all, id)
					if kind == opRegDev {
						uni.devs = append(uni.devs, id)
					} else {
						uni.attaches = append(uni.attaches, id)
					}
				}
				if expS != expM || speqCode(errS) != naiveCode(errM) {
					t.Fatalf("seed=%d step=%d %s\nspeq: exp=%d err=%v\nnaive: exp=%d err=%v",
						seed, step, input, expS, errS, expM, errM)
				}
				basis = fmt.Sprintf("expiry=%d code=%d", expS, speqCode(errS))

			case opInspect:
				if len(uni.all) == 0 {
					continue
				}
				id := uni.all[rng.Intn(len(uni.all))]
				r := speq.InspectionResult(rng.Intn(3))
				rn := naive.Result(r)
				rect := rng.Intn(200)
				input = fmt.Sprintf("INSPECT date=%d id=%s result=%d rectify=%d", date, id, r, rect)
				expS, errS = s.Inspect(date, id, r, rect)
				expM, errM = m.Inspect(date, id, rn, rect)
				if expS != expM || speqCode(errS) != naiveCode(errM) {
					t.Fatalf("seed=%d step=%d %s\nspeq: exp=%d err=%v\nnaive: exp=%d err=%v",
						seed, step, input, expS, errS, expM, errM)
				}
				basis = fmt.Sprintf("expiry=%d code=%d", expS, speqCode(errS))

			case opSeal:
				if len(uni.all) == 0 {
					continue
				}
				id := uni.all[rng.Intn(len(uni.all))]
				input = fmt.Sprintf("SEAL date=%d id=%s", date, id)
				errS = s.Seal(date, id)
				errM = m.Seal(date, id)
				if speqCode(errS) != naiveCode(errM) {
					t.Fatalf("seed=%d step=%d %s\nspeq=%v\nnaive=%v", seed, step, input, errS, errM)
				}
				basis = fmt.Sprintf("code=%d", speqCode(errS))

			case opUnseal:
				if len(uni.all) == 0 {
					continue
				}
				id := uni.all[rng.Intn(len(uni.all))]
				input = fmt.Sprintf("UNSEAL date=%d id=%s", date, id)
				expS, errS = s.Unseal(date, id)
				expM, errM = m.Unseal(date, id)
				if expS != expM || speqCode(errS) != naiveCode(errM) {
					t.Fatalf("seed=%d step=%d %s\nspeq: exp=%d err=%v\nnaive: exp=%d err=%v",
						seed, step, input, expS, errS, expM, errM)
				}
				basis = fmt.Sprintf("expiry=%d code=%d", expS, speqCode(errS))

			case opMount:
				if len(uni.attaches) == 0 || len(uni.devs) == 0 {
					continue
				}
				aid := uni.attaches[rng.Intn(len(uni.attaches))]
				did := uni.devs[rng.Intn(len(uni.devs))]
				input = fmt.Sprintf("MOUNT date=%d attach=%s device=%s", date, aid, did)
				errS = s.MountAttachment(date, aid, did)
				errM = m.Mount(date, aid, did)
				if speqCode(errS) != naiveCode(errM) {
					t.Fatalf("seed=%d step=%d %s\nspeq=%v\nnaive=%v", seed, step, input, errS, errM)
				}
				basis = fmt.Sprintf("code=%d", speqCode(errS))

			case opUse:
				if len(uni.devs) == 0 {
					continue
				}
				id := uni.devs[rng.Intn(len(uni.devs))]
				input = fmt.Sprintf("USE date=%d device=%s", date, id)
				errS = s.RegisterUse(date, id)
				errM = m.RegisterUse(date, id)
				if speqCode(errS) != naiveCode(errM) {
					sReason, mReason := "", ""
					var re1 *speq.RejectError
					if errors.As(errS, &re1) {
						sReason = re1.Attachment
					}
					var re2 *naive.UseError
					if errors.As(errM, &re2) {
						mReason = re2.Attach
					}
					t.Fatalf("seed=%d step=%d %s\nspeq=%v(%s)\nnaive=%v(%s)",
						seed, step, input, errS, sReason, errM, mReason)
				}
				basis = fmt.Sprintf("code=%d", speqCode(errS))

			case opScrap:
				if len(uni.all) == 0 {
					continue
				}
				id := uni.all[rng.Intn(len(uni.all))]
				input = fmt.Sprintf("SCRAP date=%d id=%s", date, id)
				errS = s.Scrap(date, id)
				errM = m.Scrap(date, id)
				if speqCode(errS) != naiveCode(errM) {
					t.Fatalf("seed=%d step=%d %s\nspeq=%v\nnaive=%v", seed, step, input, errS, errM)
				}
				basis = fmt.Sprintf("code=%d", speqCode(errS))

			case opWarn:
				input = fmt.Sprintf("WARN date=%d", date)
				ws, errS := s.QueryWarnings(date)
				wm := m.Warnings(date)
				if errS != nil {
					t.Fatalf("seed=%d step=%d %s warnings err %v", seed, step, input, errS)
				}
				if !warningsEqual(ws, wm) {
					t.Fatalf("seed=%d step=%d %s warnings mismatch\nspeq=%+v\nnaive=%+v",
						seed, step, input, ws, wm)
				}
				basis = fmt.Sprintf("hits=%d", len(ws))
			}

			// 每个操作后完整快照必须一致。
			if !snapEqual(s.Snapshot(), m.Snapshot()) {
				t.Fatalf("seed=%d step=%d %s snapshot mismatch\nspeq=%+v\nnaive=%+v",
					seed, step, input, s.Snapshot(), m.Snapshot())
			}
			if s.LastDate() != m.LastDate() {
				t.Fatalf("seed=%d step=%d last-date mismatch %d vs %d",
					seed, step, s.LastDate(), m.LastDate())
			}
			fmt.Fprintf(w, "step=%04d | IN  %s\n          | OUT %s\n", step, input, basis)
		}
		fmt.Fprintf(w, "==== RUN %d DONE objects=%d devices=%d attachments=%d ====\n\n",
			run, len(uni.all), len(uni.devs), len(uni.attaches))
	}
}

func warningsEqual(ws []speq.WarningEntry, wm []naive.Warn) bool {
	if len(ws) != len(wm) {
		return false
	}
	for i := range ws {
		a, b := ws[i], wm[i]
		if a.ID != b.ID || a.Expiry != b.Exp || a.SortExpiry != b.Sort {
			return false
		}
		if len(a.Triggers) != len(b.Trig) {
			return false
		}
		for j := range a.Triggers {
			if a.Triggers[j] != b.Trig[j] {
				return false
			}
		}
	}
	return true
}

func snapEqual(ss []speq.Snapshot, ms []naive.Snap) bool {
	if len(ss) != len(ms) {
		return false
	}
	for i := range ss {
		a, b := ss[i], ms[i]
		if a.ID != b.ID || a.Category != b.Cat || a.Expiry != b.Exp ||
			a.Scrapped != b.Scrap || a.Sealed != b.Seal || a.Disabled != b.Dis ||
			a.Host != b.Host {
			return false
		}
		if (a.Kind == speq.KindDevice) != (b.Kind == naive.KDevice) {
			return false
		}
	}
	return true
}
