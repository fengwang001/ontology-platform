// Demo: nested action transaction with validation hooks.
package main

import (
	"fmt"

	"ontology/action"
	"ontology/hooks"
)

func main() {
	reg := hooks.NewRegistry()
	rec := hooks.NewRecorder()
	e := action.NewEngine(reg, rec)
	defer e.Close()

	e.RegisterType(action.ObjectType{
		Name:  "Employee",
		Props: map[string]action.PropType{"name": action.String, "salary": action.Int},
	})
	e.RegisterType(action.ObjectType{
		Name:  "Department",
		Props: map[string]action.PropType{"budget": action.Int},
	})

	reg.RegisterPre("Employee", "salary-non-negative", func(c hooks.Context) error {
		return nil
	})
	reg.RegisterPost("Department", "budget-positive", func(c hooks.Context) error {
		if v, ok := c.View.Get("Department", "eng"); ok && v["budget"].(int) < 0 {
			return fmt.Errorf("department eng budget went negative")
		}
		return nil
	})

	e.RegisterAction(action.Definition{Name: "hire", Run: func(ctx action.Ctx, p map[string]any) error {
		return ctx.Create("Employee", p["id"].(string), map[string]any{
			"name": p["name"], "salary": p["salary"],
		})
	}})
	e.RegisterAction(action.Definition{Name: "hire-with-budget", Run: func(ctx action.Ctx, p map[string]any) error {
		dept, _ := ctx.Get("Department", "eng")
		if err := ctx.Update("Department", "eng", map[string]any{
			"budget": dept["budget"].(int) - p["salary"].(int),
		}); err != nil {
			return err
		}
		// Nested action: joins the same transaction boundary.
		return ctx.Invoke("hire", p)
	}})
	e.RegisterAction(action.Definition{Name: "seed", Run: func(ctx action.Ctx, p map[string]any) error {
		return ctx.Create("Department", "eng", map[string]any{"budget": 100_000})
	}})

	fmt.Println("== seed department ==")
	report(e.Execute("seed", nil))

	fmt.Println("== affordable hire (commits) ==")
	report(e.Execute("hire-with-budget", map[string]any{"id": "e1", "name": "Ada", "salary": 60_000}))

	fmt.Println("== unaffordable hire (post hook rolls everything back) ==")
	report(e.Execute("hire-with-budget", map[string]any{"id": "e2", "name": "Bo", "salary": 50_000}))

	fmt.Println("== hook firing log ==")
	for _, r := range rec.Records() {
		fmt.Printf("  #%d txn=%d action=%s phase=%d type=%s hook=%s\n",
			r.Seq, r.TxnID, r.Action, r.Phase, r.TypeName, r.HookName)
	}
	if v, ok := e.Get("Department", "eng"); ok {
		fmt.Println("final department:", v)
	}
	fmt.Println("committed employees:", e.CountCommitted("Employee"))
}

func report(r action.Result) {
	if r.Err != nil {
		fmt.Println("  rejected:", r.Err)
		return
	}
	fmt.Println("  committed")
}
