package main

import (
	"errors"
	"fmt"

	"ontology/expresshub"
)

func main() {
	system, err := expresshub.New(expresshub.Config{
		MaxItems:       2,
		MaxWeightGrams: 1000,
		DwellLimitSec:  60,
	})
	must(err)

	first, err := system.AddParcel(expresshub.AddParcelInput{
		Waybill: "SF1001", Destination: "A", WeightGrams: 300,
		Category: expresshub.CategoryFragile, At: 0,
	})
	must(err)

	second, err := system.AddParcel(expresshub.AddParcelInput{
		Waybill: "SF1002", Destination: "A", WeightGrams: 400,
		Category: expresshub.CategoryNormal, At: 10,
	})
	must(err)

	third, err := system.AddParcel(expresshub.AddParcelInput{
		Waybill: "SF1003", Destination: "A", WeightGrams: 200,
		Category: expresshub.CategoryNormal, At: 20,
	})
	must(err)

	fmt.Printf("first bag=%d, second bag=%d, third sealed=%d opened=%d\n",
		first.BagID, second.BagID, third.SealedBagID, third.OpenedBagID)

	sealed, err := system.SealBag(expresshub.SealBagInput{Destination: "A", At: 30})
	must(err)
	_, err = system.DispatchBag(expresshub.DispatchBagInput{
		BagID: sealed.BagID, VehicleID: "TRUCK-7", At: 40,
	})
	must(err)

	result, err := system.VerifyBag(expresshub.VerifyBagInput{
		BagID: sealed.BagID, Destination: "A",
		Scanned: []string{"SF9999"}, At: 50,
	})
	must(err)
	fmt.Printf("bag=%d missing=%v extra=%v\n", result.BagID, result.Missing, result.Extra)

	_, err = system.AddParcel(expresshub.AddParcelInput{
		Waybill: "SF1003", Destination: "A", WeightGrams: 200,
		Category: expresshub.CategoryNormal, At: 60,
	})
	var domainErr *expresshub.Error
	if errors.As(err, &domainErr) {
		fmt.Printf("rejoin missing parcel rejected: %s\n", domainErr.Code)
	} else {
		panic("expected business rejection")
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
