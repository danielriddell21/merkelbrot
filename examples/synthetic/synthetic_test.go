package synthetic_test

import (
	"slices"
	"testing"

	"github.com/danielriddell21/merkelbrot/examples/synthetic"
	"github.com/danielriddell21/merkelbrot/graph"
	"github.com/danielriddell21/merkelbrot/layout"
	"github.com/danielriddell21/merkelbrot/proof"
)

func build(t *testing.T, cfg synthetic.Config) *graph.Graph[string] {
	t.Helper()
	g, err := graph.New(synthetic.New(cfg))
	if err != nil {
		t.Fatalf("graph.New: %v", err)
	}
	return g
}

func TestGeneratesAcyclicGraph(t *testing.T) {
	g := build(t, synthetic.Config{Commits: 6, Depth: 3, Branching: 3, Vocabulary: 20})
	if g.Len() == 0 {
		t.Fatal("generated no nodes")
	}
	if n := len(slices.Collect(g.TopologicalOrder())); n != g.Len() {
		t.Errorf("topological order covered %d of %d nodes", n, g.Len())
	}
}

func TestIsDeterministic(t *testing.T) {
	a := slices.Collect(build(t, synthetic.Config{Seed: 3}).TopologicalOrder())
	b := slices.Collect(build(t, synthetic.Config{Seed: 3}).TopologicalOrder())
	if !slices.Equal(a, b) {
		t.Error("the same seed produced a different graph")
	}
}

// TestNarrowVocabularyCollapsesTheGraph checks the knob that controls how much
// content coincides: fewer distinct leaves must yield fewer distinct nodes overall,
// with sharing present either way.
func TestNarrowVocabularyCollapsesTheGraph(t *testing.T) {
	measure := func(vocab int) (nodes, shared int) {
		g := build(t, synthetic.Config{Seed: 5, Commits: 6, Depth: 3, Branching: 3, Vocabulary: vocab})
		return g.Len(), len(slices.Collect(g.Shared()))
	}
	narrowNodes, narrowShared := measure(4)
	wideNodes, wideShared := measure(60)

	if narrowNodes >= wideNodes {
		t.Errorf("narrow vocabulary gave %d nodes, wide gave %d, want the narrow graph to collapse smaller", narrowNodes, wideNodes)
	}
	if narrowShared == 0 || wideShared == 0 {
		t.Errorf("shared nodes: narrow=%d wide=%d, want content addressing to deduplicate in both", narrowShared, wideShared)
	}
}

func TestCommitsFormAChain(t *testing.T) {
	g := build(t, synthetic.Config{Commits: 5})
	commits := 0
	for _, n := range g.All() {
		if n.Kind == "commit" {
			commits++
		}
	}
	if got, want := commits, 5; got != want {
		t.Errorf("%d commits, want %d", got, want)
	}
	if got, want := len(slices.Collect(g.Roots())), 1; got != want {
		t.Errorf("%d roots, want %d", got, want)
	}
}

func TestZeroConfigWorks(t *testing.T) {
	g := build(t, synthetic.Config{})
	if g.Len() < 10 {
		t.Errorf("zero config generated %d nodes, want a usable graph", g.Len())
	}
}

func TestPacksAndProves(t *testing.T) {
	g := build(t, synthetic.Config{Commits: 5, Depth: 3, Branching: 3, Vocabulary: 10})
	p := layout.Pack(g, layout.Options{})
	if got, want := len(p.Nodes), g.Len(); got != want {
		t.Fatalf("placed %d nodes, want %d", got, want)
	}

	root := slices.Collect(g.Roots())[0]
	var leaf string
	for id, n := range g.All() {
		if n.Kind == "blob" {
			leaf = id
			break
		}
	}
	if leaf == "" {
		t.Fatal("no blob to prove")
	}
	if _, ok := proof.Inclusion(g, leaf, root); !ok {
		t.Errorf("Inclusion(%s, %s) = false, want every blob reachable from the head", leaf, root)
	}
}
