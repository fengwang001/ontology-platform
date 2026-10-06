package hd_test

import (
	"errors"
	"fmt"

	"ontology/hd"
)

func Example() {
	sys, err := hd.New(hd.Config{
		CleanNegative: 30, CleanHBV: 40, CleanHCV: 50,
		DeepClean: 120, MinRecovery: 60,
	})
	if err != nil {
		panic(err)
	}
	_ = sys.RegisterBay(0, "G1", hd.ZoneGeneral, false, true)
	_ = sys.RegisterBay(0, "I1", hd.ZoneIsolation, false, true)
	_ = sys.RegisterPatient(1, "p", hd.InfectionHBV)

	bay, err := sys.BookTreatment(2, "t1", "p", 1000, 240)
	fmt.Println(bay, err)

	// 同一患者还须满足最短恢复 60 -> 最早 1300；1279 被拒。
	_, err = sys.BookTreatment(3, "t2", "p", 1279, 10)
	var hdErr *hd.Error
	if errors.As(err, &hdErr) {
		fmt.Println(hdErr.Code)
	}
	// Output:
	// I1 <nil>
	// PATIENT_CONFLICT
}
