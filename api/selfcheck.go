package api

import (
	"errors"
	"fmt"
	"reflect"

	"ontology/pred"
	"ontology/rewrite"
)

func sp(s string) *string { return &s }

func baseRows() []Row {
	return []Row{
		{pred.Region: "east", pred.Amount: 50, pred.Year: 2020},
		{pred.Region: "east", pred.Amount: 120, pred.Year: 2020},
		{pred.Region: "east", pred.Amount: 200, pred.Year: 2021},
		{pred.Region: "west", pred.Amount: 80, pred.Year: 2020},
		{pred.Region: "west", pred.Amount: 150, pred.Year: 2021},
		{pred.Region: "north", pred.Amount: 100, pred.Year: 2021},
		{pred.Region: "east", pred.Amount: 90, pred.Year: 2022},
		{pred.Region: "west", pred.Amount: 300, pred.Year: 2022},
	}
}

// SelfCheck runs the built-in 8-row/3-view/7-query scenario: each rewritten
// answer must equal both the hand-derived rows and an independent naive full
// scan; then the three distinct sentinel rejections and post-rejection state.
func SelfCheck() error {
	db, err := New(baseRows())
	if err != nil {
		return err
	}
	ra := []string{pred.Region, pred.Amount}
	for _, e := range []error{
		db.RegisterFilter("V1", pred.Pred{Region: sp("east")}, ra),
		db.RegisterFilter("V2", pred.Pred{Amount: pred.I(pred.Ge, 100)},
			[]string{pred.Region, pred.Amount, pred.Year}),
		db.RegisterAgg("V3", pred.Pred{}, []string{pred.Region}),
	} {
		if e != nil {
			return e
		}
	}
	type tc struct {
		p    pred.Pred
		proj []string
		agg  *AggSpec
		want []Row
	}
	qs := []tc{
		{pred.Pred{Region: sp("east")}, ra, nil, []Row{
			{pred.Region: "east", pred.Amount: 50}, {pred.Region: "east", pred.Amount: 90},
			{pred.Region: "east", pred.Amount: 120}, {pred.Region: "east", pred.Amount: 200}}},
		{pred.Pred{Region: sp("east"), Amount: pred.I(pred.Ge, 100)}, ra, nil, []Row{
			{pred.Region: "east", pred.Amount: 120}, {pred.Region: "east", pred.Amount: 200}}},
		{pred.Pred{Amount: pred.I(pred.Gt, 100)}, ra, nil, []Row{
			{pred.Region: "east", pred.Amount: 120}, {pred.Region: "east", pred.Amount: 200},
			{pred.Region: "west", pred.Amount: 150}, {pred.Region: "west", pred.Amount: 300}}},
		{pred.Pred{Region: sp("east")}, []string{pred.Region, pred.Amount, pred.Year}, nil, []Row{
			{pred.Region: "east", pred.Amount: 50, pred.Year: 2020},
			{pred.Region: "east", pred.Amount: 120, pred.Year: 2020},
			{pred.Region: "east", pred.Amount: 200, pred.Year: 2021},
			{pred.Region: "east", pred.Amount: 90, pred.Year: 2022}}},
		{pred.Pred{Region: sp("west"), Amount: pred.I(pred.Ge, 200)}, ra, nil, []Row{
			{pred.Region: "west", pred.Amount: 300}}},
		{pred.Pred{}, nil, &AggSpec{}, []Row{{pred.Amount: 1090}}},
		{pred.Pred{Amount: pred.I(pred.Ge, 100)}, nil, &AggSpec{Group: []string{pred.Region}}, []Row{
			{pred.Region: "east", pred.Amount: 320},
			{pred.Region: "north", pred.Amount: 100},
			{pred.Region: "west", pred.Amount: 450}}},
	}
	base := baseRows()
	for i, q := range qs {
		got, e := db.Query(q.p, q.proj, q.agg)
		var naive []Row
		if q.agg != nil {
			naive = aggregate(base, q.p, q.agg.Group)
		} else {
			naive = sel(base, q.p, q.proj)
		}
		if e != nil || !reflect.DeepEqual(got, q.want) || !reflect.DeepEqual(got, naive) {
			return fmt.Errorf("self-check Q%d mismatch: %v (want %v, naive %v, err %v)", i+1, got, q.want, naive, e)
		}
	}
	// Three distinct decidable rejections.
	if _, e := New([]Row{{pred.Region: "x", pred.Amount: -1, pred.Year: 1}}); !errors.Is(e, ErrInvalidRow) {
		return fmt.Errorf("negative amount: %v", e)
	}
	if _, e := New([]Row{{pred.Region: "x", pred.Amount: 1, pred.Year: 1, "z": 2}}); !errors.Is(e, ErrInvalidRow) {
		return fmt.Errorf("unknown column row: %v", e)
	}
	if e := db.RegisterFilter("bad", pred.Pred{}, []string{"nope"}); !errors.Is(e, rewrite.ErrInvalidColumn) {
		return fmt.Errorf("bad projection column: %v", e)
	}
	if _, e := db.Query(pred.Pred{}, []string{"nope"}, nil); !errors.Is(e, rewrite.ErrInvalidColumn) {
		return fmt.Errorf("bad query column: %v", e)
	}
	if _, e := pred.Build(pred.RawAtom{Col: pred.Region, Op: pred.Lt}); !errors.Is(e, pred.ErrInvalidAtom) {
		return fmt.Errorf("region non-equality: %v", e)
	}
	if ErrInvalidRow == rewrite.ErrInvalidColumn || ErrInvalidRow == pred.ErrInvalidAtom ||
		rewrite.ErrInvalidColumn == pred.ErrInvalidAtom {
		return errors.New("sentinel errors are not distinct")
	}
	// Failure leaves no trace: rejected name is still free, existing answers stable.
	if e := db.RegisterFilter("bad", pred.Pred{Region: sp("north")}, ra); e != nil {
		return fmt.Errorf("state not clean after rejection: %v", e)
	}
	if rs, _ := db.Query(pred.Pred{Region: sp("east")}, ra, nil); len(rs) != 4 {
		return errors.New("view set changed after rejected registration")
	}
	return nil
}
