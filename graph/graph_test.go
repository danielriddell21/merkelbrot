package graph_test

import (
	"errors"
	"fmt"
	"iter"
	"math"
	"slices"
	"testing"

	"github.com/danielriddell21/merkelbrot/graph"
)

type sourceFunc[K comparable] struct {
	roots iter.Seq[K]
	node  func(K) (graph.Node[K], bool)
}

func (s sourceFunc[K]) Roots() iter.Seq[K]              { return s.roots }
func (s sourceFunc[K]) Node(id K) (graph.Node[K], bool) { return s.node(id) }

func TestNewIndexesParents(t *testing.T) {
	g, err := graph.New(commitDAG())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got, want := g.Len(), 6; got != want {
		t.Errorf("Len() = %d, want %d", got, want)
	}
	if g.IsTree() {
		t.Error("IsTree() = true, want false for a shared blob")
	}
	if got, want := slices.Collect(g.Parents("readme")), []string{"t2", "t1"}; !slices.Equal(got, want) {
		t.Errorf("Parents(readme) = %v, want %v", got, want)
	}
	if got := slices.Collect(g.Parents("c2")); len(got) != 0 {
		t.Errorf("Parents(c2) = %v, want none", got)
	}
}

func TestNewDetectsTree(t *testing.T) {
	src := graph.NewMemorySource([]string{"a"},
		graph.Node[string]{ID: "a", Children: []string{"b", "c"}},
		graph.Node[string]{ID: "b"},
		graph.Node[string]{ID: "c"},
	)
	g, err := graph.New(src)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !g.IsTree() {
		t.Error("IsTree() = false, want true")
	}
	if got := slices.Collect(g.Shared()); len(got) != 0 {
		t.Errorf("Shared() = %v, want none", got)
	}
}

func TestNewErrors(t *testing.T) {
	tests := []struct {
		name string
		src  graph.Source[string]
		want error
	}{
		{
			name: "no roots",
			src:  graph.NewMemorySource(nil, graph.Node[string]{ID: "a"}),
			want: graph.ErrNoRoots,
		},
		{
			name: "missing child",
			src: graph.NewMemorySource([]string{"a"},
				graph.Node[string]{ID: "a", Children: []string{"ghost"}},
			),
			want: graph.ErrMissingNode,
		},
		{
			name: "missing root",
			src:  graph.NewMemorySource([]string{"a"}),
			want: graph.ErrMissingNode,
		},
		{
			name: "direct cycle",
			src: graph.NewMemorySource([]string{"a"},
				graph.Node[string]{ID: "a", Children: []string{"b"}},
				graph.Node[string]{ID: "b", Children: []string{"a"}},
			),
			want: graph.ErrCycle,
		},
		{
			name: "self loop",
			src: graph.NewMemorySource([]string{"a"},
				graph.Node[string]{ID: "a", Children: []string{"a"}},
			),
			want: graph.ErrCycle,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := graph.New(tt.src); !errors.Is(err, tt.want) {
				t.Errorf("New() error = %v, want %v", err, tt.want)
			}
		})
	}
}

// TestDiamondIsNotACycle guards against a depth-first cycle check that mistakes a
// re-converging DAG path for a back edge.
func TestDiamondIsNotACycle(t *testing.T) {
	src := graph.NewMemorySource([]string{"top"},
		graph.Node[string]{ID: "top", Children: []string{"left", "right"}},
		graph.Node[string]{ID: "left", Children: []string{"bottom"}},
		graph.Node[string]{ID: "right", Children: []string{"bottom"}},
		graph.Node[string]{ID: "bottom"},
	)
	g, err := graph.New(src)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got, want := slices.Collect(g.Descendants("top")), []string{"top", "left", "bottom", "right"}; !slices.Equal(got, want) {
		t.Errorf("Descendants(top) = %v, want %v", got, want)
	}
}

func TestTraversalsDeduplicate(t *testing.T) {
	g, err := graph.New(commitDAG())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	for _, tt := range []struct {
		name string
		got  []string
		want []string
	}{
		{"descendants", slices.Collect(g.Descendants("c2")), []string{"c2", "c1", "t1", "readme", "t2", "licence"}},
		{"breadthfirst", slices.Collect(g.BreadthFirst("c2")), []string{"c2", "c1", "t2", "t1", "readme", "licence"}},
		{"ancestors", slices.Collect(g.Ancestors("readme")), []string{"readme", "t2", "c2", "t1", "c1"}},
		{"topological", slices.Collect(g.TopologicalOrder()), []string{"c2", "c1", "t2", "t1", "licence", "readme"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if !slices.Equal(tt.got, tt.want) {
				t.Errorf("got %v, want %v", tt.got, tt.want)
			}
		})
	}
}

func TestTraversalsAreDeterministic(t *testing.T) {
	g, err := graph.New(commitDAG())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	first := slices.Collect(g.TopologicalOrder())
	for range 50 {
		if got := slices.Collect(g.TopologicalOrder()); !slices.Equal(got, first) {
			t.Fatalf("TopologicalOrder() = %v, want stable %v", got, first)
		}
	}
}

func TestTraversalStopsEarly(t *testing.T) {
	g, err := graph.New(commitDAG())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	var seen []string
	for id := range g.Descendants("c2") {
		seen = append(seen, id)
		if len(seen) == 2 {
			break
		}
	}
	if got, want := seen, []string{"c2", "c1"}; !slices.Equal(got, want) {
		t.Errorf("early break yielded %v, want %v", got, want)
	}
}

func TestTraversalOfUnknownNode(t *testing.T) {
	g, err := graph.New(commitDAG())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got := slices.Collect(g.Descendants("nope")); len(got) != 0 {
		t.Errorf("Descendants(nope) = %v, want none", got)
	}
}

func TestDepths(t *testing.T) {
	g, err := graph.New(commitDAG())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := map[string]int{"c2": 0, "c1": 1, "t2": 1, "t1": 2, "readme": 2, "licence": 2}
	for id, depth := range want {
		if got := g.Depths()[id]; got != depth {
			t.Errorf("Depths()[%s] = %d, want %d", id, got, depth)
		}
	}
}

func TestDuplicateRootsCollapse(t *testing.T) {
	src := graph.NewMemorySource([]string{"a", "a"}, graph.Node[string]{ID: "a"})
	g, err := graph.New(src)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got, want := slices.Collect(g.Roots()), []string{"a"}; !slices.Equal(got, want) {
		t.Errorf("Roots() = %v, want %v", got, want)
	}
}

func TestWeighKeepsOrderAndCompressesTheRange(t *testing.T) {
	sizes := []float64{0, 64, 512, 4096, 65536, 1 << 20}
	var last float64
	for _, size := range sizes {
		w := graph.Weigh(size, 512)
		if w < last {
			t.Errorf("Weigh(%g) = %g, less than the weight of a smaller quantity (%g)", size, w, last)
		}
		last = w
	}

	// Radius grows with the square root of weight, so that is what has to stay
	// within a sane range across four orders of magnitude.
	small := math.Sqrt(graph.Weigh(64, 512))
	big := math.Sqrt(graph.Weigh(1<<20, 512))
	if ratio := big / small; ratio > 4 {
		t.Errorf("a megabyte draws %.1f times the radius of 64 bytes, want the range compressed", ratio)
	}
}

func TestWeighTreatsNothingAsTheMinimum(t *testing.T) {
	for _, tc := range []struct{ quantity, unit float64 }{{0, 512}, {-1, 512}, {100, 0}, {100, -1}} {
		if got := graph.Weigh(tc.quantity, tc.unit); got != 1 {
			t.Errorf("Weigh(%g, %g) = %g, want 1", tc.quantity, tc.unit, got)
		}
	}
}

// wide is a graph deep and broad enough that a limit has to cut it somewhere.
func wide(t *testing.T) graph.Source[string] {
	t.Helper()
	nodes := []graph.Node[string]{{ID: "root", Kind: "commit"}}
	for i := range 6 {
		mid := fmt.Sprintf("m%d", i)
		nodes[0].Children = append(nodes[0].Children, mid)
		n := graph.Node[string]{ID: mid, Kind: "tree"}
		for j := range 6 {
			leaf := fmt.Sprintf("l%d-%d", i, j)
			n.Children = append(n.Children, leaf)
			nodes = append(nodes, graph.Node[string]{ID: leaf, Kind: "blob"})
		}
		nodes = append(nodes, n)
	}
	return graph.NewMemorySource([]string{"root"}, nodes...)
}

func TestLimitStopsAtTheGivenSize(t *testing.T) {
	g, err := graph.NewLimited(wide(t), graph.Limit{MaxNodes: 10})
	if err != nil {
		t.Fatalf("NewLimited: %v", err)
	}
	if g.Len() > 10 {
		t.Errorf("read %d nodes, want at most 10", g.Len())
	}
	if !g.Truncated() {
		t.Error("Truncated reports false on a graph that was cut")
	}
	if len(slices.Collect(g.Frontier())) == 0 {
		t.Error("no node reports the cut, so a renderer cannot say where the graph continues")
	}
}

// TestLimitLeavesAConsistentGraph is what makes a limit safe to hand on: every
// reference that survives must resolve, or a consumer indexing by ID gets a node
// that is not there.
func TestLimitLeavesAConsistentGraph(t *testing.T) {
	g, err := graph.NewLimited(wide(t), graph.Limit{MaxNodes: 10})
	if err != nil {
		t.Fatalf("NewLimited: %v", err)
	}
	for id, n := range g.All() {
		for _, child := range n.Children {
			if _, ok := g.Node(child); !ok {
				t.Errorf("%v references %v, which was never read", id, child)
			}
		}
		for parent := range g.Parents(id) {
			if _, ok := g.Node(parent); !ok {
				t.Errorf("%v has parent %v, which was never read", id, parent)
			}
		}
	}
}

// TestLimitKeepsTheTopOfTheGraph is why the bounded walk is breadth-first: a
// depth-first cut would return one tendril to the bottom and none of the rest.
func TestLimitKeepsTheTopOfTheGraph(t *testing.T) {
	g, err := graph.NewLimited(wide(t), graph.Limit{MaxNodes: 7})
	if err != nil {
		t.Fatalf("NewLimited: %v", err)
	}
	kinds := map[string]int{}
	for _, n := range g.All() {
		kinds[n.Kind]++
	}
	if got, want := kinds["tree"], 6; got != want {
		t.Errorf("kept %d of the %d nodes below the root, want all of them before going deeper", got, want)
	}
}

func TestLimitAlwaysKeepsTheRoots(t *testing.T) {
	src := graph.NewMemorySource([]string{"a", "b", "c"},
		graph.Node[string]{ID: "a"}, graph.Node[string]{ID: "b"}, graph.Node[string]{ID: "c"},
	)
	g, err := graph.NewLimited(src, graph.Limit{MaxNodes: 1})
	if err != nil {
		t.Fatalf("NewLimited: %v", err)
	}
	if got, want := len(slices.Collect(g.Roots())), 3; got != want {
		t.Errorf("%d roots survived a limit of 1, want %d", got, want)
	}
}

func TestNoLimitReadsEverything(t *testing.T) {
	g, err := graph.NewLimited(wide(t), graph.Limit{})
	if err != nil {
		t.Fatalf("NewLimited: %v", err)
	}
	if got, want := g.Len(), 43; got != want {
		t.Errorf("read %d nodes, want the whole graph (%d)", got, want)
	}
	if g.Truncated() {
		t.Error("Truncated reports true on a graph read whole")
	}
}

// countingSource records how many times each node was asked for, which is what
// growing a graph is supposed to avoid repeating.
type countingSource struct {
	inner graph.Source[string]
	calls map[string]int
}

func counted(src graph.Source[string]) *countingSource {
	return &countingSource{inner: src, calls: map[string]int{}}
}

func (c *countingSource) Roots() iter.Seq[string] { return c.inner.Roots() }

func (c *countingSource) Node(id string) (graph.Node[string], bool) {
	c.calls[id]++
	return c.inner.Node(id)
}

func (c *countingSource) repeated() []string {
	var again []string
	for id, n := range c.calls {
		if n > 1 {
			again = append(again, id)
		}
	}
	slices.Sort(again)
	return again
}

func TestGrowReadsFurtherWithoutReadingAgain(t *testing.T) {
	src := counted(wide(t))
	small, err := graph.NewLimited(src, graph.Limit{MaxNodes: 8})
	if err != nil {
		t.Fatalf("NewLimited: %v", err)
	}

	big, err := small.Grow(src, graph.Limit{MaxNodes: 20})
	if err != nil {
		t.Fatalf("Grow: %v", err)
	}
	if big.Len() <= small.Len() {
		t.Errorf("grew to %d nodes from %d, want more", big.Len(), small.Len())
	}
	if big.Len() > 20 {
		t.Errorf("grew to %d nodes, want at most 20", big.Len())
	}
	if again := src.repeated(); len(again) > 0 {
		t.Errorf("the source was asked again for %v, want every node read once", again)
	}
	// The smaller graph is a snapshot and goes on working.
	if small.Len() != 8 {
		t.Errorf("the original graph changed to %d nodes, want it left alone", small.Len())
	}
}

func TestGrowKeepsTheGraphConsistent(t *testing.T) {
	src := wide(t)
	g, err := graph.NewLimited(src, graph.Limit{MaxNodes: 5})
	if err != nil {
		t.Fatalf("NewLimited: %v", err)
	}
	for _, to := range []int{9, 14, 0} {
		if g, err = g.Grow(src, graph.Limit{MaxNodes: to}); err != nil {
			t.Fatalf("Grow to %d: %v", to, err)
		}
		for id, n := range g.All() {
			for _, child := range n.Children {
				if _, ok := g.Node(child); !ok {
					t.Fatalf("after growing to %d, %v references %v, which was never read", to, id, child)
				}
			}
		}
	}
	if g.Truncated() {
		t.Error("growing without a limit left the graph truncated")
	}
	if got, want := g.Len(), 43; got != want {
		t.Errorf("grew to %d nodes, want the whole graph (%d)", got, want)
	}
}

// TestGrowKeepsChildOrder matters because the layout reads the first child of a
// node as the line a merge continues: a grown graph that reordered children would
// quietly redraw the history.
func TestGrowKeepsChildOrder(t *testing.T) {
	src := graph.NewMemorySource([]string{"root"},
		graph.Node[string]{ID: "root", Children: []string{"a", "b", "c", "d"}},
		graph.Node[string]{ID: "a"}, graph.Node[string]{ID: "b"},
		graph.Node[string]{ID: "c"}, graph.Node[string]{ID: "d"},
	)
	small, err := graph.NewLimited(src, graph.Limit{MaxNodes: 3})
	if err != nil {
		t.Fatalf("NewLimited: %v", err)
	}
	big, err := small.Grow(src, graph.Limit{})
	if err != nil {
		t.Fatalf("Grow: %v", err)
	}
	n, _ := big.Node("root")
	if got, want := n.Children, []string{"a", "b", "c", "d"}; !slices.Equal(got, want) {
		t.Errorf("children after growing = %v, want %v", got, want)
	}
}

func TestGrowIsANoOpWhenThereIsNothingMore(t *testing.T) {
	src := wide(t)
	whole, err := graph.New(src)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if got, err := whole.Grow(src, graph.Limit{}); err != nil || got != whole {
		t.Errorf("Grow on a complete graph = %p, %v, want the receiver unchanged", got, err)
	}

	part, err := graph.NewLimited(src, graph.Limit{MaxNodes: 6})
	if err != nil {
		t.Fatalf("NewLimited: %v", err)
	}
	if got, err := part.Grow(src, graph.Limit{MaxNodes: 4}); err != nil || got != part {
		t.Errorf("Grow to a smaller limit = %p, %v, want the receiver unchanged", got, err)
	}
}
