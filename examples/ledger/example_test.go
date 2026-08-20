package ledger_test

import (
	"fmt"
	"slices"

	"github.com/danielriddell21/merkelbrot/examples/ledger"
	"github.com/danielriddell21/merkelbrot/graph"
)

// ExampleNew walks one transaction the way the visualiser zooms into it: the
// transaction, its state transition history, and the double entry posted at each
// transition that moved money.
func ExampleNew() {
	g, err := graph.New(ledger.New(ledger.Config{Seed: 4, Transactions: 6}))
	if err != nil {
		panic(err)
	}

	head := slices.Collect(g.Roots())[0]
	txn, _ := g.Node(head)
	fmt.Println("transaction:", txn.Label)

	for child := range g.Children(head) {
		history, _ := g.Node(child)
		if history.Kind != "log" {
			continue
		}
		for step := range g.Children(child) {
			status, _ := g.Node(step)
			fmt.Printf("  %s\n", status.Label)
			for posted := range g.Children(step) {
				entry, _ := g.Node(posted)
				for _, f := range entry.Payload {
					if f.Key == "narrative" {
						fmt.Printf("    %s\n", f.Value)
					}
				}
			}
		}
	}
	// Output:
	// transaction: £1,250.00 Camden Print Co
	//   Initiated
	//   Mandate Checked
	//   Submitted
	//   Authorised
	//     Purchase recognised
	//   Cleared
	//     Supplier payment
	//   Settled
}
