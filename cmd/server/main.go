// Command server is a small end-to-end demo of the ontology view subsystem:
// it builds object types with different default timezones, migrates one of
// them, replays late-arriving events, and prints the grouped view plus a
// slice of the decision audit log.
package main

import (
	"fmt"
	"log"

	"ontology"
)

func main() {
	e := ontology.NewEngine()
	must := func(err error) {
		if err != nil {
			log.Fatal(err)
		}
	}

	must(e.DefineObjectType("Order", "placedAt", "placedAt"))
	must(e.DefineObjectType("Shipment", "shippedAt", "shippedAt"))
	must(e.DefineTimezone("Order", "Asia/Shanghai"))
	must(e.DefineTimezone("Shipment", "America/New_York"))
	must(e.DefineLinkType("OrderShipment", "Order", "Shipment"))
	must(e.NewView("fulfillment", "OrderShipment"))

	// Writes anchored to timezone definition version 1 of each type.
	w1, err := e.Write("order-1", "Order", "placedAt", ontology.MustWall("2026-03-01T09:00:00"))
	must(err)
	w2, err := e.Write("ship-1", "Shipment", "shippedAt", ontology.MustWall("2026-02-28T20:30:00"))
	must(err)
	l1, err := e.Link("OrderShipment", "order-1", "Order", "ship-1", "Shipment")
	must(err)
	// Written before the migration, so anchored to version 1, but held back
	// and only dispatched after the migration-era events: the late arrival.
	w4, err := e.Write("order-3", "Order", "placedAt", ontology.MustWall("2026-03-01T09:00:00"))
	must(err)
	l3, err := e.Link("OrderShipment", "order-3", "Order", "ship-1", "Shipment")
	must(err)

	// Migrate Order's default timezone: version 1 -> version 2.
	must(e.MigrateTimezone("Order", "Europe/Berlin", 1))

	// A write anchored to version 2.
	w3, err := e.Write("order-2", "Order", "placedAt", ontology.MustWall("2026-03-01T09:00:00"))
	must(err)
	l2, err := e.Link("OrderShipment", "order-2", "Order", "ship-1", "Shipment")
	must(err)

	// Deliver the v2-anchored write BEFORE the held-back v1-anchored one:
	// arrival order does not matter, write-time anchoring does.
	e.Dispatch(w3, l2, w1, w2, l1, w4, l3)

	groups, rep, err := e.Query("fulfillment")
	must(err)
	if rep.Err != nil {
		fmt.Println("view error:", rep.Err)
	}
	fmt.Println("== view (base timezone UTC, grouped by day) ==")
	for _, g := range groups {
		fmt.Println(g.Key)
		for _, it := range g.Items {
			fmt.Printf("  %-8s %-8s %s (tz v%d)\n",
				it.ObjID, it.TypeID, it.Normalized.Format("2006-01-02 15:04"), it.TZVersion)
		}
	}

	audit, err := e.Audit("fulfillment")
	must(err)
	fmt.Println("== audit (all decisions) ==")
	for _, a := range audit {
		fmt.Printf("  #%d %-7s obj=%-8s tzv=%d wall=%s -> %s %s\n",
			a.Seq, a.EventKind, a.ObjID, a.TZVersion, a.Wall, a.Decision, a.Group)
	}
}
