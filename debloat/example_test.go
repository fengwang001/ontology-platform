package debloat_test

import (
	"fmt"

	"ontology/debloat"
)

func Example() {
	c, err := debloat.New(debloat.Config{
		Bmin: 1024, Bmax: 32768, G: 1024, B0: 4096,
		T: 1000, W: 3, ThU: 25, ThD: 50, Kc: 2,
		C0: 2, Pool: 65536, H: 3,
	})
	if err != nil {
		panic(err)
	}

	for i := 0; i < 2; i++ {
		r, _ := c.Sample(100000, 1000)
		fmt.Println(r.Action, r.Cur, r.Streak)
	}
	r, _ := c.Sample(1, 1) // first sample after an Applied is Skipped
	fmt.Println(r.Action)

	if err := c.SetChannels(32); err != nil { // Beff(32)=2048: forced shrink
		panic(err)
	}
	s := c.State()
	fmt.Println(s.Cur, s.ForcedCount, s.LastDir)
	// Output:
	// Pending 4096 1
	// Applied 32768 0
	// Skipped
	// 2048 1 down
}
