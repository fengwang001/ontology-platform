// Package explain renders a plan tree as line-oriented text, one operator
// per line, with estimated cardinality, cumulative cost and annotations for
// unreliable estimates, stale statistics, cartesian products and the
// independence assumption.
package explain

import (
	"fmt"
	"strings"

	"ontology/catalog"
	"ontology/plan"
)

// Render prints one operator per line with two-space indentation. The
// catalog is used only to annotate stale scans with the corrected row count.
func Render(root *plan.Node, cat *catalog.Catalog) string {
	var b strings.Builder
	renderNode(&b, root, cat, 0)
	return b.String()
}

func renderNode(b *strings.Builder, n *plan.Node, cat *catalog.Catalog, depth int) {
	indent := strings.Repeat("  ", depth)
	if n.Leaf() {
		b.WriteString(indent + fmt.Sprintf("Scan %s card=%g cost=%g", n.Table, n.Card, n.Cost))
		if n.Stale {
			rows := n.Card
			if info, ok := cat.Table(n.Table); ok {
				rows = float64(info.Rows)
			}
			b.WriteString(fmt.Sprintf(" [stale stats: catalog rows %g used]", rows))
		}
		b.WriteString("\n")
		return
	}
	kind := "Join"
	if n.Cartesian {
		kind = "CartesianJoin"
	}
	preds := ""
	if len(n.Predicates) > 0 {
		names := make([]string, len(n.Predicates))
		for i, p := range n.Predicates {
			names[i] = p.String()
		}
		preds = "(" + strings.Join(names, ", ") + ")"
	}
	b.WriteString(indent + fmt.Sprintf("%s%s card=%g cost=%g", kind, preds, n.Card, n.Cost))
	if n.Cartesian {
		b.WriteString(" [cartesian]")
	}
	if len(n.Predicates) > 1 {
		b.WriteString(" [independence assumed]")
	}
	if n.Unreliable {
		b.WriteString(" [unreliable estimate]")
	}
	if n.Stale {
		b.WriteString(" [stale stats]")
	}
	b.WriteString("\n")
	renderNode(b, n.Left, cat, depth+1)
	renderNode(b, n.Right, cat, depth+1)
}
