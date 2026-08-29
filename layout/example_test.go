package layout_test

import (
	"fmt"
	"strings"

	"github.com/danielriddell21/merkelbrot/graph"
	"github.com/danielriddell21/merkelbrot/layout"
)

func ExamplePack() {
	g, err := graph.New(graph.NewMemorySource([]string{"commit"},
		graph.Node[string]{ID: "commit", Kind: "commit", Children: []string{"tree"}},
		graph.Node[string]{ID: "tree", Kind: "tree", Children: []string{"readme", "licence"}},
		graph.Node[string]{ID: "readme", Kind: "blob"},
		graph.Node[string]{ID: "licence", Kind: "blob"},
	))
	if err != nil {
		panic(err)
	}

	p := layout.Pack(g, layout.Options{})
	for _, n := range p.Nodes {
		fmt.Printf("%s%s covers %.0f%% of the view\n",
			strings.Repeat("  ", n.Depth), n.ID, 100*n.R/p.Bounds.R)
	}
	// Output:
	// commit covers 100% of the view
	//   tree covers 82% of the view
	//     readme covers 39% of the view
	//     licence covers 39% of the view
}

// ExamplePack_sharedSubtree shows how a node reachable from two parents is nested
// once, inside the node that dominates both, with the remaining edges reported as
// links rather than duplicated geometry.
func ExamplePack_sharedSubtree() {
	g, err := graph.New(graph.NewMemorySource([]string{"root"},
		graph.Node[string]{ID: "root", Children: []string{"left", "right"}},
		graph.Node[string]{ID: "left", Children: []string{"shared"}},
		graph.Node[string]{ID: "right", Children: []string{"shared"}},
		graph.Node[string]{ID: "shared"},
	))
	if err != nil {
		panic(err)
	}

	p := layout.Pack(g, layout.Options{})
	for _, n := range p.Nodes {
		where := "top level"
		if n.HasParent {
			where = "inside " + n.Parent
		}
		fmt.Printf("%s: %s, shared=%t\n", n.ID, where, n.Shared)
	}
	for _, l := range p.Links {
		fmt.Printf("link: %s -> %s\n", l.From, l.To)
	}
	// Output:
	// root: top level, shared=false
	// left: inside root, shared=false
	// right: inside root, shared=false
	// shared: inside root, shared=true
	// link: left -> shared
	// link: right -> shared
}

// ExampleOptions_maxDepth limits the layout to the outermost levels, which is what
// a renderer does when it only needs an overview of a very large graph.
func ExampleOptions_maxDepth() {
	g, err := graph.New(graph.NewMemorySource([]string{"a"},
		graph.Node[string]{ID: "a", Children: []string{"b"}},
		graph.Node[string]{ID: "b", Children: []string{"c"}},
		graph.Node[string]{ID: "c"},
	))
	if err != nil {
		panic(err)
	}

	p := layout.Pack(g, layout.Options{MaxDepth: 2})
	for _, n := range p.Nodes {
		fmt.Println(n.ID, "at depth", n.Depth)
	}
	// Output:
	// a at depth 0
	// b at depth 1
}
