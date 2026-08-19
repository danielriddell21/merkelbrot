package proof_test

import (
	"crypto/sha256"
	"fmt"

	"github.com/danielriddell21/merkelbrot/graph"
	"github.com/danielriddell21/merkelbrot/proof"
)

func ledger() *graph.Graph[string] {
	src := graph.NewMemorySource([]string{"batch-2", "batch-1"},
		graph.Node[string]{ID: "batch-2", Kind: "batch", Label: "settlement 2", Children: []string{"txn-a", "txn-b"}},
		graph.Node[string]{ID: "batch-1", Kind: "batch", Label: "settlement 1", Children: []string{"txn-a"}},
		graph.Node[string]{ID: "txn-a", Kind: "transaction", Label: "£120.00 Acme Ltd", Children: []string{"dr-cogs", "cr-bank"}},
		graph.Node[string]{ID: "txn-b", Kind: "transaction", Label: "£8.50 Notable Coffee", Children: []string{"dr-cogs", "cr-cash"}},
		graph.Node[string]{ID: "dr-cogs", Kind: "entry", Label: "DR 5000 Cost of sales"},
		graph.Node[string]{ID: "cr-bank", Kind: "entry", Label: "CR 1200 Bank current account"},
		graph.Node[string]{ID: "cr-cash", Kind: "entry", Label: "CR 1230 Petty cash"},
	)
	g, err := graph.New(src)
	if err != nil {
		panic(err)
	}
	return g
}

func ExampleInclusion() {
	p, ok := proof.Inclusion(ledger(), "cr-cash", "batch-2")
	if !ok {
		panic("not reachable")
	}
	fmt.Println("path:", p.Nodes)
	fmt.Println("steps:", p.Len())
	for i, siblings := range p.Siblings {
		fmt.Printf("to recompute %s, also need %v\n", p.Nodes[i+1], siblings)
	}
	// Output:
	// path: [cr-cash txn-b batch-2]
	// steps: 2
	// to recompute txn-b, also need [dr-cogs]
	// to recompute batch-2, also need [txn-a]
}

// ExampleInclusion_unreachable shows the failure case: a leaf that belongs to a
// different root is not provable against this one.
func ExampleInclusion_unreachable() {
	_, ok := proof.Inclusion(ledger(), "cr-cash", "batch-1")
	fmt.Println("provable:", ok)
	// Output:
	// provable: false
}

func ExampleConsistency() {
	d, ok := proof.Consistency(ledger(), "batch-1", "batch-2")
	if !ok {
		panic("unknown root")
	}
	fmt.Println("shared subtrees:", d.Shared)
	fmt.Println("added:", d.Added)
	fmt.Println("removed:", d.Removed)
	// Output:
	// shared subtrees: [txn-a dr-cogs]
	// added: [batch-2 txn-b cr-cash]
	// removed: [batch-1]
}

// ExampleVerify recomputes a Merkle graph's hashes and finds a tampered node.
func ExampleVerify() {
	// A parent's hash covers its label and the hashes of its children.
	hash := func(n graph.Node[string], children [][]byte) ([]byte, error) {
		h := sha256.New()
		h.Write([]byte(n.Label))
		for _, c := range children {
			h.Write(c)
		}
		return h.Sum(nil), nil
	}

	leaf := graph.Node[string]{ID: "leaf", Label: "CR 1200 Bank current account"}
	leafHash, _ := hash(leaf, nil)
	leaf.Hash = leafHash

	root := graph.Node[string]{ID: "root", Label: "settlement", Children: []string{"leaf"}}
	root.Hash, _ = hash(root, [][]byte{leafHash})

	g, _ := graph.New(graph.NewMemorySource([]string{"root"}, root, leaf))
	bad, err := proof.Verify(g, hash)
	fmt.Println("intact:", bad, err)

	// Tamper with the leaf's content but leave every stored hash alone.
	leaf.Label = "CR 1200 Bank current account (adjusted)"
	g, _ = graph.New(graph.NewMemorySource([]string{"root"}, root, leaf))
	bad, err = proof.Verify(g, hash)
	fmt.Println("tampered:", bad, err)
	// Output:
	// intact: [] <nil>
	// tampered: [root leaf] <nil>
}
