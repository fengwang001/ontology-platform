// Command demo 演示带溢出的哈希聚合器：逐条打印检查项并以 OK/FAIL 判定。
package main

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"path/filepath"
	"time"

	"ontology/acc"
	"ontology/hashagg"
	"ontology/hashpart"
	"ontology/row"
	"ontology/verify"
)

var failed int

func check(name string, ok bool, detail string) {
	status := "OK"
	if !ok {
		status = "FAIL"
		failed++
	}
	fmt.Printf("%s %s %s\n", status, name, detail)
}

func distinctRows(n int) []row.Row {
	rows := make([]row.Row, n)
	for i := range rows {
		rows[i] = row.Row{Key: fmt.Sprintf("k%d", i), Val: float64(i)}
	}
	return rows
}

func runAgg(numParts, budget int, rows []row.Row) ([]acc.Group, hashagg.Stats, error) {
	dir, err := os.MkdirTemp("", "hashagg-demo")
	if err != nil {
		return nil, hashagg.Stats{}, err
	}
	defer os.RemoveAll(dir)
	a, err := hashagg.New(numParts, budget, dir)
	if err != nil {
		return nil, hashagg.Stats{}, err
	}
	for _, r := range rows {
		if err := a.Add(r); err != nil {
			return nil, a.Stats(), err
		}
	}
	groups, err := a.Finish()
	return groups, a.Stats(), err
}

func main() {
	// 1. 单分区溢出 3 次：1 分区、预算 3、10 个不同键 → 恰好 3 次溢出。
	rows10 := distinctRows(10)
	g1, s1, err1 := runAgg(1, 3, rows10)
	ok1 := err1 == nil && s1.SpillsByPart[0] == 3 && verify.CompareGroups(g1, verify.InMemory(rows10)) == nil
	check("单分区溢出3次结果逐位相同", ok1, fmt.Sprintf("spills=%d", s1.Spills))

	// 2+3. 5 万分组预算 500：峰值不超预算、溢出已发生、写出==读回。
	rows50k := distinctRows(50000)
	g2, s2, err2 := runAgg(16, 500, rows50k)
	ok2 := err2 == nil && s2.PeakResident <= 500 && s2.Spills > 0 &&
		verify.CompareGroups(g2, verify.InMemory(rows50k)) == nil
	check("驻留峰值不超预算且溢出已发生", ok2, fmt.Sprintf("peak=%d spills=%d", s2.PeakResident, s2.Spills))
	ok3 := s2.RowsWritten == s2.RowsRead && s2.RowsWritten > 0 && s2.RowOps <= 2*50000
	check("写出行数等于读回行数", ok3, fmt.Sprintf("written=%d read=%d ops=%d", s2.RowsWritten, s2.RowsRead, s2.RowOps))

	// 4. 分组数==预算不溢出；==预算+1 溢出一次。
	_, sa, _ := runAgg(4, 4, distinctRows(4))
	_, sb, _ := runAgg(4, 4, distinctRows(5))
	check("预算边界不溢出/加一溢出一次", sa.Spills == 0 && sb.Spills == 1, fmt.Sprintf("eq=%d plus1=%d", sa.Spills, sb.Spills))

	// 5. 四类截断分类各一例。
	checkTruncationClasses()

	// 6. 截断的分区文件让聚合中止并指出分区与偏移。
	checkTruncateAborts()

	// 7. 写失败后临时文件残留为 0。
	checkWriteFailure()

	// 8. 打乱回读完成顺序 20 次输出一致。
	checkDeterministic()

	if failed > 0 {
		fmt.Printf("TOTAL FAIL %d 项未通过\n", failed)
		os.Exit(1)
	}
	fmt.Println("TOTAL OK 全部通过")
}

func checkTruncationClasses() {
	dir, _ := os.MkdirTemp("", "hashagg-demo")
	defer os.RemoveAll(dir)
	sp, _ := hashpart.NewSpiller(dir, 1)
	_ = sp.WriteSegment(0, [][]byte{[]byte("payload-0000")})
	data, _ := os.ReadFile(sp.Segments(0)[0])
	cuts := []struct {
		name string
		cut  int
		want error
	}{
		{"头部", 5, hashpart.ErrHeader},
		{"长度前缀", hashpart.HeaderLen + 2, hashpart.ErrLengthPrefix},
		{"行体", hashpart.HeaderLen + 6, hashpart.ErrBody},
		{"CRC", len(data) - 2, hashpart.ErrCRC},
	}
	for _, c := range cuts {
		p := filepath.Join(dir, "part-0000-seg-000009")
		_ = os.WriteFile(p, data[:c.cut], 0o644)
		check("截断分类-"+c.name, errors.Is(verify.CheckFile(p), c.want), "")
	}
}

func checkTruncateAborts() {
	dir, _ := os.MkdirTemp("", "hashagg-demo")
	defer os.RemoveAll(dir)
	a, _ := hashagg.New(2, 4, dir)
	for _, r := range distinctRows(20) {
		_ = a.Add(r)
	}
	entries, _ := os.ReadDir(dir)
	p := filepath.Join(dir, entries[0].Name())
	data, _ := os.ReadFile(p)
	_ = os.WriteFile(p, data[:len(data)-3], 0o644)
	_, err := a.Finish()
	var fe *hashpart.FileError
	ok := errors.As(err, &fe) && fe.Part >= 0 && fe.Offset >= 0
	check("截断中止并指出分区与偏移", ok, fmt.Sprintf("err=%v", err))
}

func checkWriteFailure() {
	dir, _ := os.MkdirTemp("", "hashagg-demo")
	defer os.RemoveAll(dir)
	a, _ := hashagg.New(2, 4, dir)
	a.SetFailOnWrite(2)
	var err error
	for _, r := range distinctRows(20) {
		if err = a.Add(r); err != nil {
			break
		}
	}
	entries, _ := os.ReadDir(dir)
	check("写失败后临时文件残留为0", errors.Is(err, hashpart.ErrWrite) && len(entries) == 0,
		fmt.Sprintf("leftover=%d", len(entries)))
}

func checkDeterministic() {
	rows := make([]row.Row, 1500)
	for i := range rows {
		rows[i] = row.Row{Key: fmt.Sprintf("k%d", i%100), Val: float64(i)}
	}
	var want []acc.Group
	ok := true
	for trial := 0; trial < 20; trial++ {
		dir, _ := os.MkdirTemp("", "hashagg-demo")
		a, _ := hashagg.New(8, 50, dir)
		a.SetReadDelay(func(int) { time.Sleep(time.Duration(rand.IntN(3)) * time.Millisecond) })
		for _, r := range rows {
			_ = a.Add(r)
		}
		got, err := a.Finish()
		os.RemoveAll(dir)
		if err != nil {
			ok = false
			break
		}
		if trial == 0 {
			want = got
		} else if !verify.Identical(want, got) {
			ok = false
			break
		}
	}
	check("打乱回读顺序20次输出一致", ok, "")
}
