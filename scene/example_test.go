package scene_test

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"

	"github.com/danielriddell21/merkelbrot/graph"
	"github.com/danielriddell21/merkelbrot/layout"
	"github.com/danielriddell21/merkelbrot/proof"
	"github.com/danielriddell21/merkelbrot/scene"
)

func payments() *graph.Graph[string] {
	g, err := graph.New(graph.NewMemorySource([]string{"txn"},
		graph.Node[string]{ID: "txn", Kind: "transaction", Label: "£120.00 to Acme Ltd", Children: []string{"log", "posting"}},
		graph.Node[string]{ID: "log", Kind: "log", Label: "status history", Children: []string{"settled"}},
		graph.Node[string]{ID: "posting", Kind: "log", Label: "double entry", Children: []string{"debit", "credit"}},
		graph.Node[string]{ID: "settled", Kind: "entry", Label: "settled"},
		graph.Node[string]{ID: "debit", Kind: "entry", Label: "DR 5000 Cost of sales", Payload: []graph.Field{
			{Key: "nominal", Value: "5000"},
			{Key: "amount", Value: "120.00"},
		}},
		graph.Node[string]{ID: "credit", Kind: "entry", Label: "CR 1200 Bank current account"},
	))
	if err != nil {
		panic(err)
	}
	return g
}

func ExampleBuilder_Scene() {
	b := scene.Builder[string]{Title: "payments"}
	s := b.Scene(layout.Pack(payments(), layout.Options{}))

	fmt.Println("title:", s.Title)
	fmt.Println("kinds:", s.Kinds)
	fmt.Printf("stats: %d nodes, %d leaves, %d shared, depth %d\n",
		s.Stats.Nodes, s.Stats.Leaves, s.Stats.Shared, s.Stats.MaxDepth)
	for _, n := range s.Nodes {
		fmt.Printf("%s depth=%d leaf=%t fields=%d\n", n.ID, n.Depth, n.Leaf, len(n.Fields))
	}
	// Output:
	// title: payments
	// kinds: [transaction log entry]
	// stats: 6 nodes, 3 leaves, 0 shared, depth 2
	// txn depth=0 leaf=false fields=0
	// log depth=1 leaf=false fields=0
	// posting depth=1 leaf=false fields=0
	// settled depth=2 leaf=true fields=0
	// debit depth=2 leaf=true fields=2
	// credit depth=2 leaf=true fields=0
}

// ExampleBuilder_Inclusion attaches Merkle evidence to a scene so the renderer can
// pick out why a ledger entry belongs to its transaction.
func ExampleBuilder_Inclusion() {
	g := payments()
	b := scene.Builder[string]{Title: "payments"}
	s := b.Scene(layout.Pack(g, layout.Options{}))

	path, ok := proof.Inclusion(g, "debit", "txn")
	if !ok {
		panic("not reachable")
	}
	s.Add(b.Inclusion(path)...)

	for _, h := range s.Highlights {
		fmt.Printf("%s (%s): %v\n", h.Name, h.Kind, h.Nodes)
	}
	// Output:
	// debit in txn (path): [debit posting txn]
	// supporting hashes (evidence): [credit log]
}

// ExampleScene_WriteJSON shows the payload the web renderer receives. A scene is
// plain data, so any front end can consume it.
func ExampleScene_WriteJSON() {
	g, _ := graph.New(graph.NewMemorySource([]string{"root"},
		graph.Node[string]{ID: "root", Kind: "commit", Label: "initial", Hash: []byte{0xde, 0xad, 0xbe, 0xef}},
	))
	s := scene.Builder[string]{Title: "one node"}.Scene(layout.Pack(g, layout.Options{}))

	// Re-encode with indentation purely to keep this example readable.
	var pretty any
	data, _ := json.Marshal(s)
	_ = json.Unmarshal(data, &pretty)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(pretty)
	// Output:
	// {
	//   "bounds": {
	//     "r": 1,
	//     "x": 0,
	//     "y": 0
	//   },
	//   "kinds": [
	//     "commit"
	//   ],
	//   "nodes": [
	//     {
	//       "depth": 0,
	//       "hash": "deadbeef",
	//       "id": "root",
	//       "kind": "commit",
	//       "label": "initial",
	//       "leaf": true,
	//       "r": 1,
	//       "x": 0,
	//       "y": 0
	//     }
	//   ],
	//   "stats": {
	//     "leaves": 1,
	//     "links": 0,
	//     "maxDepth": 0,
	//     "nodes": 1,
	//     "shared": 0
	//   },
	//   "title": "one node",
	//   "version": 1
	// }
}

// ExampleBuilder_Consistency marks what one version of a graph kept from another
// and what it added, which is what a consistency proof is for.
func ExampleBuilder_Consistency() {
	g, err := graph.New(graph.NewMemorySource([]string{"c2"},
		graph.Node[string]{ID: "c2", Kind: "commit", Children: []string{"c1", "t2"}},
		graph.Node[string]{ID: "c1", Kind: "commit", Children: []string{"t1"}},
		graph.Node[string]{ID: "t2", Kind: "tree", Children: []string{"kept", "added"}},
		graph.Node[string]{ID: "t1", Kind: "tree", Children: []string{"kept"}},
		graph.Node[string]{ID: "kept", Kind: "blob"},
		graph.Node[string]{ID: "added", Kind: "blob"},
	))
	if err != nil {
		panic(err)
	}

	b := scene.Builder[string]{Title: "two commits"}
	s := b.Scene(layout.Pack(g, layout.Options{}))

	d, ok := proof.Consistency(g, "t1", "t2")
	if !ok {
		panic("unknown roots")
	}
	s.Add(b.Consistency(d)...)

	for _, h := range s.Highlights {
		fmt.Printf("%s (%s): %v\n", h.Name, h.Kind, h.Nodes)
	}
	// Output:
	// retained (shared): [kept]
	// added (added): [t2 added]
	// removed (removed): [t1]
}

// ExampleBuilder_Invalid turns the result of a verification into something the
// renderer can show: the nodes whose stored hash no longer matches what their
// contents produce.
func ExampleBuilder_Invalid() {
	// A node's hash covers its label and the hashes of its children.
	hash := func(n graph.Node[string], children [][]byte) ([]byte, error) {
		h := sha256.New()
		h.Write([]byte(n.Label))
		for _, c := range children {
			h.Write(c)
		}
		return h.Sum(nil), nil
	}

	leaf := graph.Node[string]{ID: "entry", Label: "DR 5000 Cost of sales"}
	leaf.Hash, _ = hash(leaf, nil)
	root := graph.Node[string]{ID: "txn", Label: "payment", Children: []string{"entry"}}
	root.Hash, _ = hash(root, [][]byte{leaf.Hash})

	// The entry is edited after the fact, but its stored hash is left alone.
	leaf.Label = "DR 5000 Cost of sales (adjusted)"

	g, err := graph.New(graph.NewMemorySource([]string{"txn"}, root, leaf))
	if err != nil {
		panic(err)
	}
	bad, err := proof.Verify(g, hash)
	if err != nil {
		panic(err)
	}

	b := scene.Builder[string]{Title: "tampered"}
	s := b.Scene(layout.Pack(g, layout.Options{}))
	s.Add(b.Invalid(bad)...)

	for _, h := range s.Highlights {
		fmt.Printf("%s (%s): %v\n", h.Name, h.Kind, h.Nodes)
	}
	// Output:
	// hash mismatch (invalid): [txn entry]
}
