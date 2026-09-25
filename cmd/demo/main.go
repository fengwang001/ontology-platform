package main

import (
	"fmt"

	"ontology/snapshot"
	"ontology/store"
)

func main() {
	pass, total := 0, 0
	report := func(name string, ok bool) {
		total++
		if ok {
			pass++
			fmt.Printf("OK   %s\n", name)
		} else {
			fmt.Printf("FAIL %s\n", name)
		}
	}

	st := store.New()
	st.Put("a", []byte("1"))
	ver := st.Version()
	st.RegisterSnapshot(ver)
	st.Put("a", []byte("2"))
	got, ok := st.Read(ver, "a")
	report("store put/read baseline", ok && string(got) == "1")
	st.UnregisterSnapshot(ver)

	// snapshot：导出到 key50 时改写 key80，快照读 key80 仍是旧值。
	st2 := store.New()
	for i := 0; i < 100; i++ {
		st2.Put(fmt.Sprintf("k%03d", i), []byte(fmt.Sprintf("v%d", i)))
	}
	snap := snapshot.New(st2)
	keys2 := st2.Keys(snap.Version(), "asc")
	for i, k := range keys2 {
		if i == 50 {
			st2.Put("k080", []byte("NEW"))
		}
		_ = i
	}
	v80, _, err := snap.read("k080")
	report("mutated key keeps snapshot-old value", err == nil && string(v80) == "v80")

	// snapshot：两个重叠快照，只有都关闭后保留值才释放。
	sA := snapshot.New(st2)
	sB := snapshot.New(st2)
	st2.Put("k001", []byte("X"))
	sB.Close()
	midRetained := st2.Retained()
	sA.Close()
	report("retained values freed after both snapshots close", midRetained == 1 && st2.Retained() == 0)
	snap.Close()

	fmt.Printf("TOTAL %d/%d\n", pass, total)
	if pass != total {
		fmt.Println("RESULT FAIL")
	} else {
		fmt.Println("RESULT OK")
	}
}
