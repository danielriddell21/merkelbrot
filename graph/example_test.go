package graph_test

import (
	"fmt"

	"github.com/danielriddell21/merkelbrot/graph"
)

func commitDAG() *graph.MemorySource[string] {
	return graph.NewMemorySource([]string{"c2"},
		graph.Node[string]{ID: "c2", Kind: "commit", Label: "add licence", Children: []string{"c1", "t2"}},
		graph.Node[string]{ID: "c1", Kind: "commit", Label: "initial commit", Children: []string{"t1"}},
		graph.Node[string]{ID: "t2", Kind: "tree", Label: "/", Children: []string{"readme", "licence"}},
		graph.Node[string]{ID: "t1", Kind: "tree", Label: "/", Children: []string{"readme"}},
		graph.Node[string]{ID: "readme", Kind: "blob", Label: "README.md"},
		graph.Node[string]{ID: "licence", Kind: "blob", Label: "LICENCE"},
	)
}

func ExampleNew() {
	g, err := graph.New(commitDAG())
	if err != nil {
		panic(err)
	}
	fmt.Println("nodes:", g.Len())
	fmt.Println("tree:", g.IsTree())
	// Output:
	// nodes: 6
	// tree: false
}

func ExampleGraph_Descendants() {
	g, _ := graph.New(commitDAG())
	for id := range g.Descendants("c2") {
		n, _ := g.Node(id)
		fmt.Printf("%s\t%s\n", n.Kind, n.Label)
	}
	// Output:
	// commit	add licence
	// commit	initial commit
	// tree	/
	// blob	README.md
	// tree	/
	// blob	LICENCE
}

func ExampleGraph_Shared() {
	g, _ := graph.New(commitDAG())
	for id := range g.Shared() {
		fmt.Println(id, "is referenced by:")
		for parent := range g.Parents(id) {
			fmt.Println("  ", parent)
		}
	}
	// Output:
	// readme is referenced by:
	//    t2
	//    t1
}

func ExampleGraph_TopologicalOrder() {
	g, _ := graph.New(commitDAG())
	for id := range g.TopologicalOrder() {
		fmt.Print(id, " ")
	}
	fmt.Println()
	// Output:
	// c2 c1 t2 t1 licence readme
}

// ExampleSource shows a Source reading from a consumer's own data structures
// rather than from a prebuilt slice of nodes.
func ExampleSource() {
	// A consumer's existing type, with no knowledge of merkelbrot.
	type entry struct {
		name  string
		parts []string
	}
	store := map[string]entry{
		"root":  {name: "batch 0041", parts: []string{"txn-1", "txn-2"}},
		"txn-1": {name: "£120.00 to Acme Ltd"},
		"txn-2": {name: "£8.50 to Notable Coffee"},
	}

	src := sourceFunc[string]{
		roots: func(yield func(string) bool) { yield("root") },
		node: func(id string) (graph.Node[string], bool) {
			e, ok := store[id]
			if !ok {
				return graph.Node[string]{}, false
			}
			return graph.Node[string]{ID: id, Label: e.name, Children: e.parts}, true
		},
	}

	g, err := graph.New(src)
	if err != nil {
		panic(err)
	}
	for id := range g.Descendants("root") {
		n, _ := g.Node(id)
		fmt.Println(n.Label)
	}
	// Output:
	// batch 0041
	// £120.00 to Acme Ltd
	// £8.50 to Notable Coffee
}
