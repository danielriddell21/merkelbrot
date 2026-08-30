package graph_test

import (
	"fmt"
	"math"

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

// ExampleWeigh turns file sizes into drawing weights. The ordering survives, so
// a bigger file is drawn bigger, but four orders of magnitude of bytes become a
// factor of three in radius rather than a factor of a hundred.
func ExampleWeigh() {
	for _, size := range []float64{0, 512, 8192, 1 << 20} {
		w := graph.Weigh(size, 512)
		fmt.Printf("%8.0f B  weight %5.2f  radius %.2f\n", size, w, math.Sqrt(w))
	}
	// Output:
	//        0 B  weight  1.00  radius 1.00
	//      512 B  weight  2.00  radius 1.41
	//     8192 B  weight  5.09  radius 2.26
	//  1048576 B  weight 12.00  radius 3.46
}

// ExampleNewLimited reads only part of a source, which is what makes a graph too
// large to hold in memory usable. The result is a consistent graph: every
// reference in it still resolves, and the nodes where it was cut short are
// reported so a renderer can say the graph continues there.
func ExampleNewLimited() {
	g, err := graph.NewLimited(commitDAG(), graph.Limit{MaxNodes: 3})
	if err != nil {
		panic(err)
	}

	fmt.Println("read:", g.Len())
	fmt.Println("truncated:", g.Truncated())
	for id := range g.Frontier() {
		n, _ := g.Node(id)
		fmt.Printf("continues below %s (%s)\n", id, n.Kind)
	}
	// Output:
	// read: 3
	// truncated: true
	// continues below c1 (commit)
	// continues below t2 (tree)
}

// ExampleGraph_Grow widens a bounded read without paying for it twice. The first
// read stops early; growing it carries on from the frontier, and the source is
// never asked for a node it has already supplied.
func ExampleGraph_Grow() {
	src := graph.NewMemorySource([]string{"commit"},
		graph.Node[string]{ID: "commit", Kind: "commit", Children: []string{"tree"}},
		graph.Node[string]{ID: "tree", Kind: "tree", Children: []string{"readme", "licence"}},
		graph.Node[string]{ID: "readme", Kind: "blob"},
		graph.Node[string]{ID: "licence", Kind: "blob"},
	)

	part, err := graph.NewLimited(src, graph.Limit{MaxNodes: 2})
	if err != nil {
		panic(err)
	}
	fmt.Println("read:", part.Len(), "truncated:", part.Truncated())

	whole, err := part.Grow(src, graph.Limit{})
	if err != nil {
		panic(err)
	}
	fmt.Println("grown:", whole.Len(), "truncated:", whole.Truncated())
	fmt.Println("the first graph is untouched:", part.Len())
	// Output:
	// read: 2 truncated: true
	// grown: 4 truncated: false
	// the first graph is untouched: 2
}
