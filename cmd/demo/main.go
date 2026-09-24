package main

import (
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"

	"ontology/agg"
	"ontology/change"
	"ontology/journal"
)

type check struct {
	name string
	ok   bool
	got  string
}

func main() {
	src := change.Change{
		Op: change.Update, Version: 7, Group: "", GroupSet: true,
		Value: -0.0, NewGroup: "b", NewValue: 3.5,
	}
	dec, derr := change.Decode(src.Encode())
	changeOK := derr == nil && dec.Op == src.Op && dec.Version == 7 &&
		dec.Group == "" && dec.NewGroup == "b" &&
		mathBits(dec.Value) == mathBits(0.0) && mathBits(dec.NewValue) == mathBits(3.5)

	aggOK := agg.New(agg.Count).NeedsMembersOnDelete() == false &&
		agg.New(agg.Sum).NeedsMembersOnDelete() == false &&
		agg.New(agg.Min).NeedsMembersOnDelete() == true &&
		agg.New(agg.Max).NeedsMembersOnDelete() == true &&
		agg.New(agg.DistinctCount).NeedsMembersOnDelete() == true

	jpath := filepath.Join(tmpDir(), "demo.journal")
	jr, _ := journal.Create(jpath)
	for i := 1; i <= 3; i++ {
		jr.Append(change.Change{Op: change.Insert, Version: int64(i),
			Group: "g", GroupSet: true, Value: float64(i), NewGroup: "n", NewValue: 1})
	}
	jr.Close()
	jdata, _ := os.ReadFile(jpath)
	classOK := map[string]bool{}
	probe := func(name string, b []byte, want error) {
		err := journal.Parse(b, func(change.Change) error { return nil })
		classOK[name] = errors.Is(err, want)
	}
	probe("header", jdata[:3], journal.ErrHeader)
	probe("length", jdata[:9], journal.ErrLength)   // 头后仅 1 字节
	probe("body", jdata[:15], journal.ErrBody)      // 长度读全，体不够
	probe("crc", append(append([]byte(nil), jdata[:48]...), 0, 0), journal.ErrCRC)
	journalOK := len(classOK) == 4
	for _, v := range classOK {
		journalOK = journalOK && v
	}

	checks := []check{
		{"change encode/decode + 正负零归一", changeOK, ""},
		{"agg 撤回需成员声明 Count/Sum=false Min/Max/Distinct=true", aggOK, ""},
		{"journal 截断四类 header/length/body/crc 可判定", journalOK, fmt.Sprint(classOK)},
	}
	fail := 0
	for _, c := range checks {
		status := "OK"
		if !c.ok {
			status = "FAIL"
			fail++
		}
		fmt.Printf("%s %s %s\n", status, c.name, c.got)
	}
	fmt.Printf("TOTAL %d/%d passed\n", len(checks)-fail, len(checks))
	if fail > 0 {
		fmt.Println("FAIL overall")
		return
	}
}

func mathBits(f float64) uint64 {
	return math.Float64bits(f)
}

func tmpDir() string {
	d, err := os.MkdirTemp("", "ontology-demo-")
	if err != nil {
		panic(err)
	}
	return d
}
