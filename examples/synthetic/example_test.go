package synthetic_test

import (
	"fmt"
	"slices"

	"github.com/danielriddell21/merkelbrot/examples/synthetic"
	"github.com/danielriddell21/merkelbrot/graph"
)

// ExampleNew generates a graph and reports how much of it is shared, which is
// what content addressing buys: identical subtrees become one node.
func ExampleNew() {
	src := synthetic.New(synthetic.Config{Seed: 2, Commits: 5, Depth: 2, Branching: 3, Vocabulary: 8})
	g, err := graph.New(src)
	if err != nil {
		panic(err)
	}

	kinds := map[string]int{}
	for _, n := range g.All() {
		kinds[n.Kind]++
	}
	fmt.Println("commits:", kinds["commit"])
	fmt.Println("trees:", kinds["tree"])
	fmt.Println("blobs:", kinds["blob"])
	fmt.Println("shared:", len(slices.Collect(g.Shared())))
	fmt.Println("tree:", g.IsTree())
	// Output:
	// commits: 5
	// trees: 13
	// blobs: 8
	// shared: 7
	// tree: false
}
