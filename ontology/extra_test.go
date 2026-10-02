package ontology

import "testing"

func TestLevelZeroOrderingAndDeepCopy(t *testing.T) {
	m, _ := New(100)
	mustApply(t, m, Edit{Adds: []File{
		{Level: 0, Num: 3, Smallest: b("a"), Largest: b("b")},
		{Level: 0, Num: 9, Smallest: b("c"), Largest: b("d")},
		{Level: 0, Num: 5, Smallest: b("e"), Largest: b("f")},
	}})
	v := m.View()
	nums := []uint64{v.Files[0][0].Num, v.Files[0][1].Num, v.Files[0][2].Num}
	if nums[0] != 9 || nums[1] != 5 || nums[2] != 3 {
		t.Fatalf("L0 order = %v, want [9 5 3]", nums)
	}
	v.Files[0][0].Num = 999
	v.Files[0][0].Smallest[0] = 'Z'
	d := m.Disk()
	d.Current = 4242
	d.Manifests[1][0].Snapshot.NextFile = 4242
	again := m.View()
	if again.Files[0][0].Num != 9 || again.Files[0][0].Smallest[0] != 'c' ||
		m.Disk().Current != 1 {
		t.Fatalf("deep copy leaked mutations into manager state")
	}
}

func TestConcurrentSerializability(t *testing.T) {
	m, _ := New(100000)
	done := make(chan error, 8)
	for w := 0; w < 8; w++ {
		go func(id int) {
			for i := 0; i < 200; i++ {
				num := uint64(id*200 + i + 100)
				err := m.Apply(Edit{Adds: []File{{
					Level:    0,
					Num:      num,
					Smallest: []byte{byte(id), byte(i)},
					Largest:  []byte{byte(id), byte(i)},
				}}})
				if err != nil {
					done <- err
					return
				}
			}
			done <- nil
		}(w)
	}
	for w := 0; w < 8; w++ {
		if err := <-done; err != nil {
			t.Fatal(err)
		}
	}
	v := m.View()
	total := 0
	for l := range v.Files {
		total += len(v.Files[l])
	}
	if total != 8*200 {
		t.Fatalf("file count = %d, want %d", total, 8*200)
	}
	rv, err := Recover(m.Disk())
	if err != nil {
		t.Fatal(err)
	}
	if !versionsEqual(rv, v) {
		t.Fatalf("recovered version differs after concurrent appends")
	}
}
