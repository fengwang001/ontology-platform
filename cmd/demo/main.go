// Command demo 演示批量导入的幂等与断点续传能力，逐条打印判定结果。
package main

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"time"

	"ontology/batch"
	"ontology/importer"
	"ontology/progress"
	"ontology/reconcile"
	"ontology/store"
)

func gen(id string, n int) (batch.Manifest, importer.Source) {
	m := batch.Manifest{ID: id}
	for i := 0; i < n; i++ {
		m.Keys = append(m.Keys, fmt.Sprintf("%s-%06d", id, i))
	}
	return m, func(i int) (string, []byte) { return m.Keys[i], []byte(m.Keys[i]) }
}

func tmp() string {
	dir, err := os.MkdirTemp("", "demo")
	if err != nil {
		panic(err)
	}
	return dir
}

var passed, total int

func check(name string, ok bool) {
	total++
	verdict := "OK"
	if !ok {
		verdict = "FAIL"
	}
	if ok {
		passed++
	}
	fmt.Printf("%s %s\n", verdict, name)
}

func main() {
	m := batch.Manifest{ID: "b0", Keys: []string{"a", "", "c"}}
	back, err := batch.Decode(batch.Encode(m))
	var dup *batch.DupKeyError
	dupErr := batch.Manifest{ID: "b1", Keys: []string{"x", "y", "x"}}.Validate()
	check("batch manifest roundtrip+dup reject",
		err == nil && back.ID == m.ID && len(back.Keys) == 3 && back.Keys[1] == "" &&
			errors.As(dupErr, &dup) && dup.Key == "x" && dup.First == 0 && dup.Second == 2)
	st := store.New()
	ex0, e0 := st.Write("k", []byte("v"))
	ex1, e1 := st.Write("k", []byte("v"))
	_, e2 := st.Write("k", []byte("w"))
	st.FailAt(4)
	_, e3 := st.Write("z", nil)
	_, e4 := st.Write("z", nil)
	check("store idempotent write + conflict + injected failure",
		!ex0 && e0 == nil && ex1 && e1 == nil && e2 != nil &&
			errors.Is(e3, store.ErrInjected) && e4 == nil && st.Len() == 2)
	dir, _ := os.MkdirTemp("", "demo")
	defer os.RemoveAll(dir)
	pg := &progress.Progress{ID: "b", Total: 10, Ivls: [][2]int{{0, 5}, {7, 9}}}
	progress.Save(dir, pg)
	raw, _ := os.ReadFile(progress.Path(dir, "b"))
	kindAt := func(cut int) progress.ErrKind {
		os.WriteFile(progress.Path(dir, "b"), raw[:cut], 0o644)
		_, err := progress.Load(dir, "b")
		var le *progress.LoadError
		if errors.As(err, &le) {
			return le.Kind
		}
		return -1
	}
	hdrLen := bytes.IndexByte(raw, '\n') + 1
	check("progress truncation classes: header/interval/crc",
		kindAt(1) == progress.ErrHeader &&
			kindAt(hdrLen+2) == progress.ErrInterval &&
			kindAt(len(raw)-1) == progress.ErrCRC)
	const n, gran = 100000, 4096
	mBig, srcBig := gen("big", n)
	stA, dirA := store.New(), tmp()
	defer os.RemoveAll(dirA)
	stA.FailAt(70001)
	importer.New(stA, dirA, gran).Import(mBig, srcBig)
	imA := importer.New(stA, dirA, gran)
	_, errA := imA.Import(mBig, srcBig)
	check("crash at 70k: resume rewrites <= N",
		errA == nil && imA.Rewritten() <= gran && stA.Len() == n)
	stB, dirB := store.New(), tmp()
	defer os.RemoveAll(dirB)
	mRep, srcRep := gen("rep", 5000)
	importer.New(stB, dirB, 512).Import(mRep, srcRep)
	snap1 := stB.Snapshot()
	doneB, errB := importer.New(stB, dirB, 512).Import(mRep, srcRep)
	check("reimport byte-identical + reports done",
		doneB && errB == nil && bytes.Equal(snap1, stB.Snapshot()))
	stC, dirC := store.New(), tmp()
	defer os.RemoveAll(dirC)
	for i := 0; i < 30000; i++ {
		stC.Write(mBig.Keys[i], []byte(mBig.Keys[i]))
	}
	progress.Save(dirC, &progress.Progress{ID: "big", Total: n, Ivls: [][2]int{{0, 50000}}})
	_, errC := importer.New(stC, dirC, gran).Import(mBig, srcBig)
	check("progress/store mismatch: store wins",
		errC == nil && bytes.Equal(stA.Snapshot(), stC.Snapshot()))
	stD, dirD := store.New(), tmp()
	defer os.RemoveAll(dirD)
	t0 := time.Now()
	hold, _ := progress.Acquire(dirD, "c", t0, time.Hour)
	mLock, srcLock := gen("c", 100)
	imD := importer.New(stD, dirD, 10)
	imD.SetClock(func() time.Time { return t0.Add(time.Minute) })
	_, errBusy := imD.Import(mLock, srcLock)
	imD.SetClock(func() time.Time { return t0.Add(2 * time.Hour) })
	_, errTake := imD.Import(mLock, srcLock)
	hold()
	check("same batch busy; takeover after expiry",
		errors.Is(errBusy, progress.ErrBusy) && errTake == nil)
	stE := store.New()
	mRec, _ := gen("rec", 10)
	for i := 0; i < 5; i++ {
		stE.Write(mRec.Keys[i], []byte(mRec.Keys[i]))
	}
	for i := 7; i < 9; i++ {
		stE.Write(mRec.Keys[i], []byte(mRec.Keys[i]))
	}
	stE.Write("extra-1", nil)
	rep := reconcile.Reconcile(mRec, stE, false)
	check("reconcile: written/gap/extra segments",
		fmt.Sprint(rep.Written) == "[[0 5] [7 9]]" &&
			fmt.Sprint(rep.Gaps) == "[[5 7] [9 10]]" &&
			fmt.Sprint(rep.Extra) == "[extra-1]")
	check("in-transit excluded from committed",
		rep.Committed == 0 && rep.InTransit == 7)
	fmt.Printf("TOTAL %d/%d\n", passed, total)
	if passed != total {
		os.Exit(1)
	}
}
