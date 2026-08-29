package layout_test

import (
	"fmt"
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

// TestPayloadDoesNotOverlapChildren guards the rule that keeps a node readable:
// fields and nested content must occupy separate ground.
func TestPayloadDoesNotOverlapChildren(t *testing.T) {
	fields := make([]graph.Field, 6)
	for i := range fields {
		fields[i] = graph.Field{Key: "k", Value: "v"}
	}
	g := mustGraph(t, graph.NewMemorySource([]string{"root"},
		graph.Node[string]{ID: "root", Payload: fields, Children: []string{"a", "b", "c"}},
		graph.Node[string]{ID: "a"},
		graph.Node[string]{ID: "b"},
		graph.Node[string]{ID: "c"},
	))
	p := layout.Pack(g, layout.Options{})
	nodes := byID(p)
	root := nodes["root"]

	if len(root.Payload) != len(fields) {
		t.Fatalf("%d slots, want %d", len(root.Payload), len(fields))
	}
	for i, s := range root.Payload {
		// Slots are relative to the node, so scale them into layout coordinates.
		sx := root.X + s.X*root.R
		sy := root.Y + s.Y*root.R
		sr := s.R * root.R
		if d := math.Hypot(sx-root.X, sy-root.Y) + sr; d > root.R+epsilon {
			t.Errorf("slot %d escapes its node: %g > %g", i, d, root.R)
		}
		for _, kid := range []string{"a", "b", "c"} {
			c := nodes[kid]
			if gap := math.Hypot(sx-c.X, sy-c.Y) - (sr + c.R); gap < -epsilon {
				t.Errorf("slot %d overlaps child %s by %g", i, kid, -gap)
			}
		}
	}
}

// chainDAG is three commits in a line, each pointing at its predecessor and at a
// tree of its own, which is the shape that makes containment degenerate.
func chainDAG(t *testing.T) *graph.Graph[string] {
	t.Helper()
	return mustGraph(t, graph.NewMemorySource([]string{"c3"},
		graph.Node[string]{ID: "c3", Kind: "commit", Children: []string{"c2", "t3"}},
		graph.Node[string]{ID: "c2", Kind: "commit", Children: []string{"c1", "t2"}},
		graph.Node[string]{ID: "c1", Kind: "commit", Children: []string{"t1"}},
		graph.Node[string]{ID: "t3", Kind: "tree", Children: []string{"b3"}},
		graph.Node[string]{ID: "t2", Kind: "tree", Children: []string{"b2"}},
		graph.Node[string]{ID: "t1", Kind: "tree", Children: []string{"b1"}},
		graph.Node[string]{ID: "b3", Kind: "blob"},
		graph.Node[string]{ID: "b2", Kind: "blob"},
		graph.Node[string]{ID: "b1", Kind: "blob"},
	))
}

// TestChainKindsSeparateHistory is the point of the option: a history reads as a
// row of siblings rather than as rings turned inside out.
func TestChainKindsSeparateHistory(t *testing.T) {
	p := layout.Pack(chainDAG(t), layout.Options{ChainKinds: []string{"commit"}, SeparateChains: true})
	nodes := byID(p)

	for _, id := range []string{"c1", "c2", "c3"} {
		if nodes[id].HasParent {
			t.Errorf("%s nested in %q, want it at the top level", id, nodes[id].Parent)
		}
		if got := nodes[id].Depth; got != 0 {
			t.Errorf("%s.Depth = %d, want 0", id, got)
		}
	}
	// The demoted edges are still reported, so no relationship is lost.
	for _, want := range []layout.Link[string]{{From: "c3", To: "c2"}, {From: "c2", To: "c1"}} {
		if !slices.Contains(p.Links, want) {
			t.Errorf("Links = %v, want it to contain %v", p.Links, want)
		}
	}
	// Content still nests: each commit keeps its own tree.
	if got, want := nodes["t3"].Parent, "c3"; got != want {
		t.Errorf("t3 nested in %q, want %q", got, want)
	}
}

// TestChainsNestWhenUndeclared is the default: with no kind named as history,
// every edge is containment and the chain turns inside out.
func TestChainsNestWhenUndeclared(t *testing.T) {
	p := layout.Pack(chainDAG(t), layout.Options{})
	nodes := byID(p)

	if got, want := nodes["c2"].Parent, "c3"; got != want {
		t.Errorf("c2 nested in %q, want %q", got, want)
	}
	if got, want := nodes["c1"].Parent, "c2"; got != want {
		t.Errorf("c1 nested in %q, want %q", got, want)
	}
	if got, want := nodes["c1"].Depth, 2; got != want {
		t.Errorf("c1.Depth = %d, want %d", got, want)
	}
	for _, unwanted := range []layout.Link[string]{{From: "c3", To: "c2"}, {From: "c2", To: "c1"}} {
		if slices.Contains(p.Links, unwanted) {
			t.Errorf("Links = %v, want %v expressed by containment instead", p.Links, unwanted)
		}
	}
}

// TestUnnamedKindsAreNeverChained keeps a plain hierarchy intact: a kind that was
// not named as history is content, however lopsided it is.
func TestUnnamedKindsAreNeverChained(t *testing.T) {
	g := mustGraph(t, graph.NewMemorySource([]string{"root"},
		graph.Node[string]{ID: "root", Children: []string{"a"}},
		graph.Node[string]{ID: "a", Children: []string{"a1", "a2"}},
		graph.Node[string]{ID: "a1"},
		graph.Node[string]{ID: "a2"},
	))
	p := layout.Pack(g, layout.Options{ChainKinds: []string{"commit"}, SeparateChains: true})
	nodes := byID(p)

	if got, want := nodes["a"].Parent, "root"; got != want {
		t.Errorf("a nested in %q, want %q", got, want)
	}
	if len(p.Links) != 0 {
		t.Errorf("Links = %v, want none", p.Links)
	}
}

// TestContentEdgesSurviveDemotion checks that only same-kind edges move: a commit
// and its tree share no kind, so the tree stays inside it.
func TestContentEdgesSurviveDemotion(t *testing.T) {
	g := mustGraph(t, graph.NewMemorySource([]string{"commit"},
		graph.Node[string]{ID: "commit", Kind: "commit", Children: []string{"tree"}},
		graph.Node[string]{ID: "tree", Kind: "tree", Children: []string{"blob"}},
		graph.Node[string]{ID: "blob", Kind: "blob"},
	))
	p := layout.Pack(g, layout.Options{ChainKinds: []string{"commit"}, SeparateChains: true})
	nodes := byID(p)

	if got, want := nodes["tree"].Parent, "commit"; got != want {
		t.Errorf("tree nested in %q, want %q", got, want)
	}
	if len(p.Links) != 0 {
		t.Errorf("Links = %v, want none", p.Links)
	}
}

// TestSubtreesStayNestedAlongsideAChain is the case a size heuristic cannot get
// right: tree points at tree exactly as commit points at commit, and only the
// declaration separates them.
func TestSubtreesStayNestedAlongsideAChain(t *testing.T) {
	g := mustGraph(t, graph.NewMemorySource([]string{"c2"},
		graph.Node[string]{ID: "c2", Kind: "commit", Children: []string{"c1", "t2"}},
		graph.Node[string]{ID: "c1", Kind: "commit", Children: []string{"t1"}},
		graph.Node[string]{ID: "t2", Kind: "tree", Children: []string{"sub"}},
		graph.Node[string]{ID: "t1", Kind: "tree"},
		graph.Node[string]{ID: "sub", Kind: "tree", Children: []string{"b"}},
		graph.Node[string]{ID: "b", Kind: "blob"},
	))
	p := layout.Pack(g, layout.Options{ChainKinds: []string{"commit"}, SeparateChains: true})
	nodes := byID(p)

	if nodes["c1"].HasParent {
		t.Errorf("c1 nested in %q, want the chain separated", nodes["c1"].Parent)
	}
	if got, want := nodes["sub"].Parent, "t2"; got != want {
		t.Errorf("sub nested in %q, want %q: tree is not a declared chain kind", got, want)
	}
}

// TestMaxChainBoundsTheDepth is the scaling fix: nesting spends a constant factor
// of scale per link, so an uncapped history consumes the whole zoom range.
func TestMaxChainBoundsTheDepth(t *testing.T) {
	// A ten-commit history, each commit carrying a tree of its own.
	nodes := []graph.Node[string]{}
	for i := 10; i >= 1; i-- {
		kids := []string{fmt.Sprintf("t%d", i)}
		if i > 1 {
			kids = append([]string{fmt.Sprintf("c%d", i-1)}, kids...)
		}
		nodes = append(nodes,
			graph.Node[string]{ID: fmt.Sprintf("c%d", i), Kind: "commit", Children: kids},
			graph.Node[string]{ID: fmt.Sprintf("t%d", i), Kind: "tree", Children: []string{fmt.Sprintf("b%d", i)}},
			graph.Node[string]{ID: fmt.Sprintf("b%d", i), Kind: "blob"},
		)
	}
	g := mustGraph(t, graph.NewMemorySource([]string{"c10"}, nodes...))

	full := layout.Pack(g, layout.Options{ChainKinds: []string{"commit"}})
	capped := layout.Pack(g, layout.Options{ChainKinds: []string{"commit"}, MaxChain: 3})

	if full.Omitted != 0 {
		t.Errorf("uncapped packing omitted %d nodes, want none", full.Omitted)
	}
	if capped.Omitted == 0 {
		t.Fatal("capped packing omitted nothing, want the tail of the chain dropped")
	}

	commits := func(p *layout.Packing[string]) int {
		n := 0
		for _, x := range p.Nodes {
			if x.Kind == "commit" {
				n++
			}
		}
		return n
	}
	if got, want := commits(capped), 3; got != want {
		t.Errorf("%d commits kept, want %d", got, want)
	}
	if commits(full) != 10 {
		t.Errorf("%d commits without a cap, want 10", commits(full))
	}

	// The whole point: the newest commit stays legible instead of shrinking away.
	deepest := func(p *layout.Packing[string]) int {
		d := 0
		for _, x := range p.Nodes {
			d = max(d, x.Depth)
		}
		return d
	}
	if deepest(capped) >= deepest(full) {
		t.Errorf("capped depth %d, uncapped %d, want the cap to be shallower", deepest(capped), deepest(full))
	}

	// The last link kept says what went missing.
	var marked int
	for _, x := range capped.Nodes {
		if x.Truncated {
			marked++
			if x.Omitted == 0 {
				t.Errorf("%s is truncated but reports nothing omitted", x.ID)
			}
		}
	}
	if marked != 1 {
		t.Errorf("%d truncated nodes, want exactly 1", marked)
	}
}

// TestMaxChainKeepsSharedContent checks the cap only drops what nothing else
// refers to: a blob still reachable from a kept commit survives.
func TestMaxChainKeepsSharedContent(t *testing.T) {
	g := mustGraph(t, graph.NewMemorySource([]string{"c3"},
		graph.Node[string]{ID: "c3", Kind: "commit", Children: []string{"c2", "t3"}},
		graph.Node[string]{ID: "c2", Kind: "commit", Children: []string{"c1", "t2"}},
		graph.Node[string]{ID: "c1", Kind: "commit", Children: []string{"t1"}},
		graph.Node[string]{ID: "t3", Kind: "tree", Children: []string{"shared"}},
		graph.Node[string]{ID: "t2", Kind: "tree", Children: []string{"shared"}},
		graph.Node[string]{ID: "t1", Kind: "tree", Children: []string{"shared", "old"}},
		graph.Node[string]{ID: "shared", Kind: "blob"},
		graph.Node[string]{ID: "old", Kind: "blob"},
	))
	p := layout.Pack(g, layout.Options{ChainKinds: []string{"commit"}, MaxChain: 2})
	nodes := byID(p)

	if _, ok := nodes["shared"]; !ok {
		t.Error("shared blob was dropped, but c3 and c2 still reach it")
	}
	if _, ok := nodes["old"]; ok {
		t.Error("old blob was kept, but only the dropped commit reached it")
	}
	if _, ok := nodes["c1"]; ok {
		t.Error("c1 was kept, want the chain cut at two links")
	}
}

// TestMinRingLeavesRoomForALabel is the readability fix: a container whose child
// nearly fills it has no ring to write in.
func TestMinRingLeavesRoomForALabel(t *testing.T) {
	g := mustGraph(t, graph.NewMemorySource([]string{"outer"},
		graph.Node[string]{ID: "outer", Kind: "commit", Children: []string{"inner"}},
		graph.Node[string]{ID: "inner", Kind: "tree", Children: []string{"a", "b", "c"}},
		graph.Node[string]{ID: "a", Kind: "blob"},
		graph.Node[string]{ID: "b", Kind: "blob"},
		graph.Node[string]{ID: "c", Kind: "blob"},
	))

	tight := layout.Pack(g, layout.Options{MinRing: -1})
	roomy := layout.Pack(g, layout.Options{MinRing: 0.25})

	ring := func(p *layout.Packing[string]) float64 {
		n := byID(p)
		return (n["outer"].R - n["inner"].R) / n["outer"].R
	}
	if got := ring(tight); got > 0.2 {
		t.Errorf("ring without a minimum = %.2f, want it tight", got)
	}
	if got := ring(roomy); got < 0.25-epsilon {
		t.Errorf("ring with MinRing 0.25 = %.2f, want at least 0.25", got)
	}
}

// TestChainLinkRingsItsOwnContent is the annular case: a commit is built around
// the commit it continues, so the ring it adds holds what it added.
func TestChainLinkRingsItsOwnContent(t *testing.T) {
	p := layout.Pack(chainDAG(t), layout.Options{ChainKinds: []string{"commit"}})
	nodes := byID(p)

	for _, step := range []struct{ outer, core, content string }{
		{"c3", "c2", "t3"},
		{"c2", "c1", "t2"},
	} {
		outer, core, content := nodes[step.outer], nodes[step.core], nodes[step.content]
		if off := math.Hypot(core.X-outer.X, core.Y-outer.Y); off > epsilon {
			t.Errorf("%s sits %g from the centre of %s, want it concentric", step.core, off, step.outer)
		}
		// The content of the newer commit is in the ring: clear of the core, inside
		// the commit that added it.
		d := math.Hypot(content.X-outer.X, content.Y-outer.Y)
		if d-content.R < core.R-epsilon {
			t.Errorf("%s reaches into %s, want it in the ring around it", step.content, step.core)
		}
		if d+content.R > outer.R+epsilon {
			t.Errorf("%s escapes %s", step.content, step.outer)
		}
	}
}

// TestChainRingLeavesRoomForALabel checks the gap the ring reserves at the top,
// which is the one place a container can write its own name.
func TestChainRingLeavesRoomForALabel(t *testing.T) {
	p := layout.Pack(chainDAG(t), layout.Options{ChainKinds: []string{"commit"}})
	nodes := byID(p)
	outer, core := nodes["c3"], nodes["c2"]

	// Walk up from the top of the core to the edge of the commit: nothing placed in
	// the ring may stand in the way.
	for d := core.R; d <= outer.R; d += (outer.R - core.R) / 32 {
		x, y := outer.X, outer.Y-d
		for _, n := range p.Nodes {
			if n.ID == "c3" || n.ID == "c2" {
				continue
			}
			if math.Hypot(x-n.X, y-n.Y) < n.R {
				t.Fatalf("%s covers the label gap at %g above the centre", n.ID, d)
			}
		}
	}
}

// TestChainRingDoesNotOverlap guards the ring arithmetic, which places discs by
// angle rather than by the collision search the other siblings use.
func TestChainRingDoesNotOverlap(t *testing.T) {
	p := layout.Pack(chainDAG(t), layout.Options{ChainKinds: []string{"commit"}})
	for i, a := range p.Nodes {
		for _, b := range p.Nodes[i+1:] {
			if a.Parent != b.Parent || a.HasParent != b.HasParent {
				continue
			}
			if gap := math.Hypot(a.X-b.X, a.Y-b.Y) - (a.R + b.R); gap < -epsilon {
				t.Errorf("%s and %s overlap by %g", a.ID, b.ID, -gap)
			}
		}
	}
}

// TestChainLinkRingsItsPayload puts a commit's own fields in the ring with its
// content, since the commit it continues has the middle.
func TestChainLinkRingsItsPayload(t *testing.T) {
	g := mustGraph(t, graph.NewMemorySource([]string{"c2"},
		graph.Node[string]{ID: "c2", Kind: "commit", Children: []string{"c1", "t2"}, Payload: []graph.Field{
			{Key: "author", Value: "dan"},
			{Key: "when", Value: "today"},
		}},
		graph.Node[string]{ID: "c1", Kind: "commit", Children: []string{"t1"}},
		graph.Node[string]{ID: "t2", Kind: "tree"},
		graph.Node[string]{ID: "t1", Kind: "tree"},
	))
	p := layout.Pack(g, layout.Options{ChainKinds: []string{"commit"}})
	nodes := byID(p)
	c2, c1, t2 := nodes["c2"], nodes["c1"], nodes["t2"]

	if got, want := len(c2.Payload), 2; got != want {
		t.Fatalf("c2 has %d slots, want %d", got, want)
	}
	for i, s := range c2.Payload {
		x, y, r := c2.X+s.X*c2.R, c2.Y+s.Y*c2.R, s.R*c2.R
		if d := math.Hypot(x-c2.X, y-c2.Y) + r; d > c2.R+epsilon {
			t.Errorf("slot %d escapes c2: %g > %g", i, d, c2.R)
		}
		for _, n := range []layout.Placed[string]{c1, t2} {
			if gap := math.Hypot(x-n.X, y-n.Y) - (r + n.R); gap < -epsilon {
				t.Errorf("slot %d overlaps %s by %g", i, n.ID, -gap)
			}
		}
	}
}

// TestChainLinkWithNothingNewKeepsARing covers a commit that added nothing: it
// still has to be larger than the one it continues, or it cannot be seen at all.
func TestChainLinkWithNothingNewKeepsARing(t *testing.T) {
	g := mustGraph(t, graph.NewMemorySource([]string{"c3"},
		graph.Node[string]{ID: "c3", Kind: "commit", Children: []string{"c2"}},
		graph.Node[string]{ID: "c2", Kind: "commit", Children: []string{"c1"}},
		graph.Node[string]{ID: "c1", Kind: "commit"},
	))
	p := layout.Pack(g, layout.Options{ChainKinds: []string{"commit"}})
	nodes := byID(p)

	for _, step := range [][2]string{{"c3", "c2"}, {"c2", "c1"}} {
		outer, inner := nodes[step[0]], nodes[step[1]]
		if off := math.Hypot(inner.X-outer.X, inner.Y-outer.Y); off > epsilon {
			t.Errorf("%s sits %g from the centre of %s, want it concentric", step[1], off, step[0])
		}
		if ring := (outer.R - inner.R) / outer.R; ring < 0.18-epsilon {
			t.Errorf("ring around %s = %.2f, want the minimum kept", step[1], ring)
		}
	}
}

// crowdedChain is a commit whose ring has to hold a dozen new blobs, which is
// what the ring arithmetic exists for: one item fits anywhere, twelve do not.
func crowdedChain(t *testing.T) *graph.Graph[string] {
	t.Helper()
	nodes := []graph.Node[string]{
		{ID: "c2", Kind: "commit", Children: []string{"c1"}},
		{ID: "c1", Kind: "commit", Children: []string{"old"}},
		{ID: "old", Kind: "blob"},
	}
	for i := range 12 {
		id := fmt.Sprintf("b%d", i)
		nodes[0].Children = append(nodes[0].Children, id)
		nodes = append(nodes, graph.Node[string]{ID: id, Kind: "blob", Weight: float64(1 + i%4)})
	}
	return mustGraph(t, graph.NewMemorySource([]string{"c2"}, nodes...))
}

// TestCrowdedRingFitsInsideItsNode checks the ring widens far enough to hold
// everything a commit added without the discs running into one another.
func TestCrowdedRingFitsInsideItsNode(t *testing.T) {
	p := layout.Pack(crowdedChain(t), layout.Options{ChainKinds: []string{"commit"}})
	nodes := byID(p)
	c2, c1 := nodes["c2"], nodes["c1"]

	if off := math.Hypot(c1.X-c2.X, c1.Y-c2.Y); off > epsilon {
		t.Errorf("c1 sits %g from the centre of c2, want it concentric", off)
	}
	var ring []layout.Placed[string]
	for _, n := range p.Nodes {
		if n.Parent == "c2" && n.ID != "c1" {
			ring = append(ring, n)
		}
	}
	if got, want := len(ring), 12; got != want {
		t.Fatalf("ring holds %d nodes, want %d", got, want)
	}
	for i, a := range ring {
		if d := math.Hypot(a.X-c2.X, a.Y-c2.Y) + a.R; d > c2.R+epsilon {
			t.Errorf("%s escapes c2: %g > %g", a.ID, d, c2.R)
		}
		if gap := math.Hypot(a.X-c1.X, a.Y-c1.Y) - (a.R + c1.R); gap < -epsilon {
			t.Errorf("%s overlaps the commit it surrounds by %g", a.ID, -gap)
		}
		for _, b := range ring[i+1:] {
			if gap := math.Hypot(a.X-b.X, a.Y-b.Y) - (a.R + b.R); gap < -epsilon {
				t.Errorf("%s and %s overlap by %g", a.ID, b.ID, -gap)
			}
		}
	}
}

// TestCrowdedRingStillLeavesALabelGap is the same case as
// [TestChainRingLeavesRoomForALabel] with the ring full, where the room has to
// be taken out of the ring rather than found in what was left over.
func TestCrowdedRingStillLeavesALabelGap(t *testing.T) {
	p := layout.Pack(crowdedChain(t), layout.Options{ChainKinds: []string{"commit"}})
	nodes := byID(p)
	c2, c1 := nodes["c2"], nodes["c1"]

	for d := c1.R; d <= c2.R; d += (c2.R - c1.R) / 64 {
		x, y := c2.X, c2.Y-d
		for _, n := range p.Nodes {
			if n.ID == "c2" || n.ID == "c1" {
				continue
			}
			if math.Hypot(x-n.X, y-n.Y) < n.R {
				t.Fatalf("%s covers the label gap at %g above the centre", n.ID, d)
			}
		}
	}
}
