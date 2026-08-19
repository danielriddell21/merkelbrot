package proof_test

import (
	"errors"
	"slices"
	"testing"

	"github.com/danielriddell21/merkelbrot/graph"
	"github.com/danielriddell21/merkelbrot/proof"
)

func TestInclusionSelf(t *testing.T) {
	p, ok := proof.Inclusion(ledger(), "batch-2", "batch-2")
	if !ok {
		t.Fatal("Inclusion of a root against itself = false, want true")
	}
	if got, want := p.Nodes, []string{"batch-2"}; !slices.Equal(got, want) {
		t.Errorf("Nodes = %v, want %v", got, want)
	}
	if p.Len() != 0 {
		t.Errorf("Len() = %d, want 0", p.Len())
	}
	if len(p.Siblings) != 0 {
		t.Errorf("Siblings = %v, want none", p.Siblings)
	}
}

func TestInclusionUnknownNodes(t *testing.T) {
	g := ledger()
	if _, ok := proof.Inclusion(g, "ghost", "batch-2"); ok {
		t.Error("Inclusion with unknown leaf = true, want false")
	}
	if _, ok := proof.Inclusion(g, "cr-cash", "ghost"); ok {
		t.Error("Inclusion with unknown root = true, want false")
	}
}

func TestInclusionInvariants(t *testing.T) {
	g := ledger()
	p, ok := proof.Inclusion(g, "cr-bank", "batch-2")
	if !ok {
		t.Fatal("Inclusion = false, want true")
	}
	if got := p.Nodes[0]; got != p.Leaf {
		t.Errorf("Nodes[0] = %s, want leaf %s", got, p.Leaf)
	}
	if got := p.Nodes[len(p.Nodes)-1]; got != p.Root {
		t.Errorf("last node = %s, want root %s", got, p.Root)
	}
	if len(p.Siblings) != len(p.Nodes)-1 {
		t.Fatalf("len(Siblings) = %d, want %d", len(p.Siblings), len(p.Nodes)-1)
	}
	// Each step's siblings must be the parent's other children, and must never
	// include the path node they sit beside.
	for i, siblings := range p.Siblings {
		parent := p.Nodes[i+1]
		want := 0
		for child := range g.Children(parent) {
			if child != p.Nodes[i] {
				want++
			}
		}
		if len(siblings) != want {
			t.Errorf("step %d: %d siblings, want %d", i, len(siblings), want)
		}
		if slices.Contains(siblings, p.Nodes[i]) {
			t.Errorf("step %d: siblings %v contain the path node %s", i, siblings, p.Nodes[i])
		}
	}
	if !p.Contains("cr-bank") || p.Contains("txn-b") {
		t.Errorf("Contains gave the wrong membership for %v", p.Nodes)
	}
}

// TestInclusionPrefersShortestPath uses a graph where a leaf is reachable both
// directly and through an intermediate node.
func TestInclusionPrefersShortestPath(t *testing.T) {
	g, err := graph.New(graph.NewMemorySource([]string{"root"},
		graph.Node[string]{ID: "root", Children: []string{"mid", "leaf"}},
		graph.Node[string]{ID: "mid", Children: []string{"leaf"}},
		graph.Node[string]{ID: "leaf"},
	))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	p, ok := proof.Inclusion(g, "leaf", "root")
	if !ok {
		t.Fatal("Inclusion = false, want true")
	}
	if got, want := p.Nodes, []string{"leaf", "root"}; !slices.Equal(got, want) {
		t.Errorf("Nodes = %v, want the direct route %v", got, want)
	}
}

func TestConsistencyIdenticalRoots(t *testing.T) {
	d, ok := proof.Consistency(ledger(), "batch-2", "batch-2")
	if !ok {
		t.Fatal("Consistency = false, want true")
	}
	if got, want := d.Shared, []string{"batch-2"}; !slices.Equal(got, want) {
		t.Errorf("Shared = %v, want %v", got, want)
	}
	if len(d.Added) != 0 || len(d.Removed) != 0 {
		t.Errorf("Added = %v, Removed = %v, want both empty", d.Added, d.Removed)
	}
}

func TestConsistencyUnrelatedRoots(t *testing.T) {
	g, err := graph.New(graph.NewMemorySource([]string{"a", "b"},
		graph.Node[string]{ID: "a", Children: []string{"a1"}},
		graph.Node[string]{ID: "b", Children: []string{"b1"}},
		graph.Node[string]{ID: "a1"},
		graph.Node[string]{ID: "b1"},
	))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	d, ok := proof.Consistency(g, "a", "b")
	if !ok {
		t.Fatal("Consistency = false, want true")
	}
	if len(d.Shared) != 0 {
		t.Errorf("Shared = %v, want none", d.Shared)
	}
	if got, want := d.Added, []string{"b", "b1"}; !slices.Equal(got, want) {
		t.Errorf("Added = %v, want %v", got, want)
	}
	if got, want := d.Removed, []string{"a", "a1"}; !slices.Equal(got, want) {
		t.Errorf("Removed = %v, want %v", got, want)
	}
}

func TestConsistencyUnknownRoot(t *testing.T) {
	if _, ok := proof.Consistency(ledger(), "ghost", "batch-2"); ok {
		t.Error("Consistency with unknown old root = true, want false")
	}
	if _, ok := proof.Consistency(ledger(), "batch-1", "ghost"); ok {
		t.Error("Consistency with unknown new root = true, want false")
	}
}

// TestConsistencySharedIsMaximal checks that nodes beneath a shared subtree are
// reported only through their topmost shared ancestor.
func TestConsistencySharedIsMaximal(t *testing.T) {
	d, ok := proof.Consistency(ledger(), "batch-1", "batch-2")
	if !ok {
		t.Fatal("Consistency = false, want true")
	}
	if slices.Contains(d.Shared, "cr-bank") {
		t.Errorf("Shared = %v, want cr-bank covered by its ancestor txn-a", d.Shared)
	}
	if !slices.Contains(d.Shared, "txn-a") {
		t.Errorf("Shared = %v, want it to contain txn-a", d.Shared)
	}
}

// countingHash concatenates the label and child hashes without any real hashing,
// which keeps the expected values in these tests readable.
func countingHash(n graph.Node[string], children [][]byte) ([]byte, error) {
	out := []byte(n.Label)
	for _, c := range children {
		out = append(out, c...)
	}
	return out, nil
}

func TestVerifyClean(t *testing.T) {
	g, err := graph.New(graph.NewMemorySource([]string{"root"},
		graph.Node[string]{ID: "root", Label: "r", Hash: []byte("rab"), Children: []string{"a", "b"}},
		graph.Node[string]{ID: "a", Label: "a", Hash: []byte("a")},
		graph.Node[string]{ID: "b", Label: "b", Hash: []byte("b")},
	))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	bad, err := proof.Verify(g, countingHash)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(bad) != 0 {
		t.Errorf("Verify() = %v, want no failures", bad)
	}
}

func TestVerifySkipsUnhashedNodes(t *testing.T) {
	g, err := graph.New(graph.NewMemorySource([]string{"root"},
		graph.Node[string]{ID: "root", Label: "r", Children: []string{"a"}},
		graph.Node[string]{ID: "a", Label: "a"},
	))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	bad, err := proof.Verify(g, countingHash)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if len(bad) != 0 {
		t.Errorf("Verify() = %v, want unhashed nodes to be skipped", bad)
	}
}

func TestVerifyReportsParentsFirst(t *testing.T) {
	g, err := graph.New(graph.NewMemorySource([]string{"root"},
		graph.Node[string]{ID: "root", Label: "r", Hash: []byte("stale"), Children: []string{"a"}},
		graph.Node[string]{ID: "a", Label: "a", Hash: []byte("wrong")},
	))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	bad, err := proof.Verify(g, countingHash)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got, want := bad, []string{"root", "a"}; !slices.Equal(got, want) {
		t.Errorf("Verify() = %v, want %v", got, want)
	}
}

func TestVerifyPropagatesHasherError(t *testing.T) {
	g, err := graph.New(graph.NewMemorySource([]string{"root"},
		graph.Node[string]{ID: "root", Label: "r"},
	))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sentinel := errors.New("boom")
	if _, err := proof.Verify(g, func(graph.Node[string], [][]byte) ([]byte, error) {
		return nil, sentinel
	}); !errors.Is(err, sentinel) {
		t.Errorf("Verify() error = %v, want %v", err, sentinel)
	}
}
