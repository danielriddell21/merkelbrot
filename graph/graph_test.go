package graph_test

import (
	"errors"
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
