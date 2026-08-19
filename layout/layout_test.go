package layout_test

import (
	"math"
	"slices"
	"testing"

	"github.com/danielriddell21/merkelbrot/graph"
	"github.com/danielriddell21/merkelbrot/layout"
)

const epsilon = 1e-6

func mustGraph(t *testing.T, src graph.Source[string]) *graph.Graph[string] {
	t.Helper()
	g, err := graph.New(src)
	if err != nil {
		t.Fatalf("graph.New: %v", err)
	}
	return g
}

// sharedDAG has one blob referenced by two trees, so its containment home must be
// resolved by dominance rather than by picking a parent.
func sharedDAG(t *testing.T) *graph.Graph[string] {
	t.Helper()
	return mustGraph(t, graph.NewMemorySource([]string{"c2"},
		graph.Node[string]{ID: "c2", Kind: "commit", Children: []string{"c1", "t2"}},
		graph.Node[string]{ID: "c1", Kind: "commit", Children: []string{"t1"}},
		graph.Node[string]{ID: "t2", Kind: "tree", Children: []string{"readme", "licence"}},
		graph.Node[string]{ID: "t1", Kind: "tree", Children: []string{"readme"}},
		graph.Node[string]{ID: "readme", Kind: "blob", Payload: []graph.Field{{Key: "size", Value: "184"}}},
		graph.Node[string]{ID: "licence", Kind: "blob"},
	))
}

func byID(p *layout.Packing[string]) map[string]layout.Placed[string] {
	m := make(map[string]layout.Placed[string], len(p.Nodes))
	for _, n := range p.Nodes {
		m[n.ID] = n
	}
	return m
}

func TestPackPlacesEveryNode(t *testing.T) {
	g := sharedDAG(t)
	p := layout.Pack(g, layout.Options{})
	if got, want := len(p.Nodes), g.Len(); got != want {
		t.Fatalf("placed %d nodes, want %d", got, want)
	}
	for _, n := range p.Nodes {
		if n.R <= 0 {
			t.Errorf("%s has radius %g, want positive", n.ID, n.R)
		}
		if math.IsNaN(n.X) || math.IsNaN(n.Y) || math.IsNaN(n.R) {
			t.Errorf("%s has a NaN coordinate: %+v", n.ID, n.Circle)
		}
	}
}

// TestChildrenAreInsideTheirParents is the invariant the whole zoom idea rests on:
// if a disc is not fully within its container, zooming into the container loses it.
func TestChildrenAreInsideTheirParents(t *testing.T) {
	g := sharedDAG(t)
	p := layout.Pack(g, layout.Options{})
	nodes := byID(p)
	for _, n := range p.Nodes {
		if !n.HasParent {
			if d := math.Hypot(n.X, n.Y) + n.R; d > p.Bounds.R+epsilon {
				t.Errorf("%s escapes the bounds: %g > %g", n.ID, d, p.Bounds.R)
			}
			continue
		}
		parent := nodes[n.Parent]
		if d := math.Hypot(n.X-parent.X, n.Y-parent.Y) + n.R; d > parent.R+epsilon {
			t.Errorf("%s escapes %s: %g > %g", n.ID, parent.ID, d, parent.R)
		}
	}
}

func TestSiblingsDoNotOverlap(t *testing.T) {
	g := sharedDAG(t)
	p := layout.Pack(g, layout.Options{})
	groups := make(map[string][]layout.Placed[string])
	for _, n := range p.Nodes {
		groups[n.Parent] = append(groups[n.Parent], n)
	}
	for parent, siblings := range groups {
		for i := range siblings {
			for j := i + 1; j < len(siblings); j++ {
				a, b := siblings[i], siblings[j]
				gap := math.Hypot(a.X-b.X, a.Y-b.Y) - (a.R + b.R)
				if gap < -epsilon {
					t.Errorf("under %q: %s and %s overlap by %g", parent, a.ID, b.ID, -gap)
				}
			}
		}
	}
}

// TestSharedNodeNestsInItsDominator checks that a blob reachable through two trees
// is hoisted to the commit that dominates both, not duplicated or nested arbitrarily.
func TestSharedNodeNestsInItsDominator(t *testing.T) {
	g := sharedDAG(t)
	p := layout.Pack(g, layout.Options{})
	nodes := byID(p)

	readme, ok := nodes["readme"]
	if !ok {
		t.Fatal("readme was not placed")
	}
	if !readme.Shared {
		t.Error("readme.Shared = false, want true")
	}
	if got, want := readme.Parent, "c2"; got != want {
		t.Errorf("readme nested in %q, want its dominator %q", got, want)
	}
	if got, want := nodes["licence"].Parent, "t2"; got != want {
		t.Errorf("licence nested in %q, want %q", got, want)
	}
}

func TestLinksCoverNonContainmentEdges(t *testing.T) {
	g := sharedDAG(t)
	p := layout.Pack(g, layout.Options{})
	want := []layout.Link[string]{
		{From: "t2", To: "readme"},
		{From: "t1", To: "readme"},
	}
	if !slices.Equal(p.Links, want) {
		t.Errorf("Links = %v, want %v", p.Links, want)
	}
}

func TestTreeHasNoLinks(t *testing.T) {
	g := mustGraph(t, graph.NewMemorySource([]string{"root"},
		graph.Node[string]{ID: "root", Children: []string{"a", "b"}},
		graph.Node[string]{ID: "a", Children: []string{"a1", "a2"}},
		graph.Node[string]{ID: "b"},
		graph.Node[string]{ID: "a1"},
		graph.Node[string]{ID: "a2"},
	))
	p := layout.Pack(g, layout.Options{})
	if len(p.Links) != 0 {
		t.Errorf("Links = %v, want none for a strict tree", p.Links)
	}
	nodes := byID(p)
	if got, want := nodes["a1"].Parent, "a"; got != want {
		t.Errorf("a1 nested in %q, want %q", got, want)
	}
	if got, want := nodes["a"].Depth, 1; got != want {
		t.Errorf("a.Depth = %d, want %d", got, want)
	}
	if got, want := nodes["root"].Depth, 0; got != want {
		t.Errorf("root.Depth = %d, want %d", got, want)
	}
}

func TestParentsComeBeforeChildren(t *testing.T) {
	p := layout.Pack(sharedDAG(t), layout.Options{})
	seen := make(map[string]bool)
	for _, n := range p.Nodes {
		if n.HasParent && !seen[n.Parent] {
			t.Errorf("%s appears before its parent %s", n.ID, n.Parent)
		}
		seen[n.ID] = true
	}
}

func TestPackIsDeterministic(t *testing.T) {
	g := sharedDAG(t)
	first := layout.Pack(g, layout.Options{})
	for range 20 {
		again := layout.Pack(g, layout.Options{})
		if !slices.EqualFunc(first.Nodes, again.Nodes, func(a, b layout.Placed[string]) bool {
			return a.ID == b.ID && a.Circle == b.Circle
		}) {
			t.Fatal("Pack produced different coordinates for the same graph")
		}
		if first.Bounds != again.Bounds {
			t.Fatalf("Bounds = %+v, want stable %+v", again.Bounds, first.Bounds)
		}
	}
}

func TestMaxDepthTruncates(t *testing.T) {
	g := sharedDAG(t)
	p := layout.Pack(g, layout.Options{MaxDepth: 1})
	if got, want := len(p.Nodes), 1; got != want {
		t.Fatalf("placed %d nodes, want %d top-level node", got, want)
	}
	if got := p.Nodes[0].ID; got != "c2" {
		t.Errorf("kept %q, want the root %q", got, "c2")
	}
	for _, n := range p.Nodes {
		if n.Depth >= 1 {
			t.Errorf("%s has depth %d, want < 1", n.ID, n.Depth)
		}
	}
}

func TestPayloadSlotsFitInsideTheNode(t *testing.T) {
	fields := make([]graph.Field, 9)
	for i := range fields {
		fields[i] = graph.Field{Key: "k", Value: "v"}
	}
	g := mustGraph(t, graph.NewMemorySource([]string{"only"},
		graph.Node[string]{ID: "only", Payload: fields},
	))
	p := layout.Pack(g, layout.Options{PayloadFill: 0.8})
	node := p.Nodes[0]
	if got, want := len(node.Payload), len(fields); got != want {
		t.Fatalf("%d slots, want %d", got, want)
	}
	for i, s := range node.Payload {
		if d := math.Hypot(s.X, s.Y) + s.R; d > 0.8+epsilon {
			t.Errorf("slot %d reaches %g, want <= PayloadFill 0.8", i, d)
		}
		for j := i + 1; j < len(node.Payload); j++ {
			o := node.Payload[j]
			if gap := math.Hypot(s.X-o.X, s.Y-o.Y) - (s.R + o.R); gap < -epsilon {
				t.Errorf("slots %d and %d overlap by %g", i, j, -gap)
			}
		}
	}
}

func TestMultipleRootsSitSideBySide(t *testing.T) {
	g := mustGraph(t, graph.NewMemorySource([]string{"a", "b"},
		graph.Node[string]{ID: "a"},
		graph.Node[string]{ID: "b"},
	))
	p := layout.Pack(g, layout.Options{})
	for _, n := range p.Nodes {
		if n.HasParent {
			t.Errorf("%s has parent %q, want none", n.ID, n.Parent)
		}
		if n.Depth != 0 {
			t.Errorf("%s.Depth = %d, want 0", n.ID, n.Depth)
		}
	}
	if p.Bounds.R <= 0 {
		t.Errorf("Bounds.R = %g, want positive", p.Bounds.R)
	}
}

// TestDeepChainDoesNotRecurse guards the iterative traversals: a commit history is
// a long thin chain and a recursive layout would overflow the stack on real data.
func TestDeepChainDoesNotRecurse(t *testing.T) {
	const depth = 20000
	nodes := make([]graph.Node[int], depth)
	for i := range depth - 1 {
		nodes[i] = graph.Node[int]{ID: i, Children: []int{i + 1}}
	}
	nodes[depth-1] = graph.Node[int]{ID: depth - 1}
	g, err := graph.New(graph.NewMemorySource([]int{0}, nodes...))
	if err != nil {
		t.Fatalf("graph.New: %v", err)
	}
	p := layout.Pack(g, layout.Options{})
	if got, want := len(p.Nodes), depth; got != want {
		t.Fatalf("placed %d nodes, want %d", got, want)
	}
}

func TestEmptyOptionsMatchExplicitDefaults(t *testing.T) {
	g := sharedDAG(t)
	zero := layout.Pack(g, layout.Options{})
	explicit := layout.Pack(g, layout.Options{LeafRadius: 1, Padding: 0.1, PayloadFill: 0.72})
	if zero.Bounds != explicit.Bounds {
		t.Errorf("zero-value options gave bounds %+v, want %+v", zero.Bounds, explicit.Bounds)
	}
}
