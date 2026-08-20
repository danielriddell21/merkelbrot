package scene_test

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/danielriddell21/merkelbrot/graph"
	"github.com/danielriddell21/merkelbrot/layout"
	"github.com/danielriddell21/merkelbrot/proof"
	"github.com/danielriddell21/merkelbrot/scene"
)

func TestSceneMirrorsThePacking(t *testing.T) {
	g := payments()
	p := layout.Pack(g, layout.Options{})
	s := scene.Builder[string]{}.Scene(p)

	if got, want := len(s.Nodes), len(p.Nodes); got != want {
		t.Fatalf("%d scene nodes, want %d", got, want)
	}
	if s.Version != scene.Version {
		t.Errorf("Version = %d, want %d", s.Version, scene.Version)
	}
	for i, n := range s.Nodes {
		src := p.Nodes[i]
		if n.ID != src.ID {
			t.Errorf("node %d ID = %q, want %q", i, n.ID, src.ID)
		}
		if n.X != src.X || n.Y != src.Y || n.R != src.R {
			t.Errorf("node %s geometry = %+v, want %+v", n.ID, n.Circle, src.Circle)
		}
	}
	if s.Bounds.R != p.Bounds.R {
		t.Errorf("Bounds.R = %g, want %g", s.Bounds.R, p.Bounds.R)
	}
}

// TestBuilderIDConvertsNonStringKeys covers the case the default fmt.Sprint cannot
// handle well: a hash key that should render as hex.
func TestBuilderIDConvertsNonStringKeys(t *testing.T) {
	type hash [2]byte
	g, err := graph.New(graph.NewMemorySource([]hash{{0xab, 0xcd}},
		graph.Node[hash]{ID: hash{0xab, 0xcd}, Children: []hash{{0x01, 0x02}}},
		graph.Node[hash]{ID: hash{0x01, 0x02}},
	))
	if err != nil {
		t.Fatalf("graph.New: %v", err)
	}
	b := scene.Builder[hash]{ID: func(h hash) string {
		const digits = "0123456789abcdef"
		return string([]byte{digits[h[0]>>4], digits[h[0]&0xf], digits[h[1]>>4], digits[h[1]&0xf]})
	}}
	s := b.Scene(layout.Pack(g, layout.Options{}))
	if got, want := s.Nodes[0].ID, "abcd"; got != want {
		t.Errorf("Nodes[0].ID = %q, want %q", got, want)
	}
	if got, want := s.Nodes[1].Parent, "abcd"; got != want {
		t.Errorf("Nodes[1].Parent = %q, want %q", got, want)
	}
}

func TestDefaultIDUsesFmtSprint(t *testing.T) {
	g, err := graph.New(graph.NewMemorySource([]int{7}, graph.Node[int]{ID: 7}))
	if err != nil {
		t.Fatalf("graph.New: %v", err)
	}
	s := scene.Builder[int]{}.Scene(layout.Pack(g, layout.Options{}))
	if got, want := s.Nodes[0].ID, "7"; got != want {
		t.Errorf("Nodes[0].ID = %q, want %q", got, want)
	}
}

func TestKindsAreOrderedByFirstAppearance(t *testing.T) {
	s := scene.Builder[string]{}.Scene(layout.Pack(payments(), layout.Options{}))
	if got, want := s.Kinds, []string{"transaction", "log", "entry"}; !slices.Equal(got, want) {
		t.Errorf("Kinds = %v, want %v", got, want)
	}
}

func TestSharedNodesAreCounted(t *testing.T) {
	g, err := graph.New(graph.NewMemorySource([]string{"root"},
		graph.Node[string]{ID: "root", Kind: "commit", Children: []string{"a", "b"}},
		graph.Node[string]{ID: "a", Kind: "tree", Children: []string{"shared"}},
		graph.Node[string]{ID: "b", Kind: "tree", Children: []string{"shared"}},
		graph.Node[string]{ID: "shared", Kind: "blob"},
	))
	if err != nil {
		t.Fatalf("graph.New: %v", err)
	}
	s := scene.Builder[string]{}.Scene(layout.Pack(g, layout.Options{}))
	if got, want := s.Stats.Shared, 1; got != want {
		t.Errorf("Stats.Shared = %d, want %d", got, want)
	}
	if got, want := s.Stats.Links, 2; got != want {
		t.Errorf("Stats.Links = %d, want %d", got, want)
	}
	if got, want := len(s.Links), 2; got != want {
		t.Fatalf("%d links, want %d", got, want)
	}
	if s.Links[0].To != "shared" {
		t.Errorf("Links[0].To = %q, want %q", s.Links[0].To, "shared")
	}
}

func TestInclusionOfASingleNodeHasNoEvidence(t *testing.T) {
	g := payments()
	path, ok := proof.Inclusion(g, "txn", "txn")
	if !ok {
		t.Fatal("Inclusion = false, want true")
	}
	hs := scene.Builder[string]{}.Inclusion(path)
	if got, want := len(hs), 1; got != want {
		t.Fatalf("%d highlights, want %d", got, want)
	}
	if hs[0].Kind != scene.KindPath {
		t.Errorf("Kind = %q, want %q", hs[0].Kind, scene.KindPath)
	}
	if len(hs[0].Links) != 0 {
		t.Errorf("Links = %v, want none", hs[0].Links)
	}
}

func TestInclusionLinksRunParentToChild(t *testing.T) {
	g := payments()
	path, ok := proof.Inclusion(g, "debit", "txn")
	if !ok {
		t.Fatal("Inclusion = false, want true")
	}
	hs := scene.Builder[string]{}.Inclusion(path)
	want := []scene.Link{{From: "posting", To: "debit"}, {From: "txn", To: "posting"}}
	if !slices.Equal(hs[0].Links, want) {
		t.Errorf("Links = %v, want %v", hs[0].Links, want)
	}
}

func TestInclusionOfEmptyPath(t *testing.T) {
	if hs := (scene.Builder[string]{}).Inclusion(proof.Path[string]{}); hs != nil {
		t.Errorf("Inclusion of an empty path = %v, want nil", hs)
	}
}

func TestConsistencyOmitsEmptySets(t *testing.T) {
	g := payments()
	d, ok := proof.Consistency(g, "txn", "txn")
	if !ok {
		t.Fatal("Consistency = false, want true")
	}
	hs := scene.Builder[string]{}.Consistency(d)
	if got, want := len(hs), 1; got != want {
		t.Fatalf("%d highlights, want %d", got, want)
	}
	if hs[0].Kind != scene.KindShared {
		t.Errorf("Kind = %q, want %q", hs[0].Kind, scene.KindShared)
	}
}

func TestInvalid(t *testing.T) {
	b := scene.Builder[string]{}
	if hs := b.Invalid(nil); hs != nil {
		t.Errorf("Invalid(nil) = %v, want nil", hs)
	}
	hs := b.Invalid([]string{"a", "b"})
	if got, want := len(hs), 1; got != want {
		t.Fatalf("%d highlights, want %d", got, want)
	}
	if got, want := hs[0].Nodes, []string{"a", "b"}; !slices.Equal(got, want) {
		t.Errorf("Nodes = %v, want %v", got, want)
	}
	if hs[0].Kind != scene.KindInvalid {
		t.Errorf("Kind = %q, want %q", hs[0].Kind, scene.KindInvalid)
	}
}

func TestAddAppends(t *testing.T) {
	s := scene.Builder[string]{}.Scene(layout.Pack(payments(), layout.Options{}))
	s.Add(scene.Highlight{Name: "one", Kind: scene.KindPath})
	s.Add(scene.Highlight{Name: "two", Kind: scene.KindAdded})
	if got, want := len(s.Highlights), 2; got != want {
		t.Fatalf("%d highlights, want %d", got, want)
	}
	if s.Highlights[0].Name != "one" || s.Highlights[1].Name != "two" {
		t.Errorf("highlights out of order: %v", s.Highlights)
	}
}

func TestWriteJSONRoundTrips(t *testing.T) {
	s := scene.Builder[string]{Title: "payments"}.Scene(layout.Pack(payments(), layout.Options{}))
	var buf bytes.Buffer
	if err := s.WriteJSON(&buf); err != nil {
		t.Fatalf("WriteJSON: %v", err)
	}
	if strings.Count(buf.String(), "\n") != 1 {
		t.Error("WriteJSON produced multiple lines, want compact output")
	}

	var back scene.Scene
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if back.Title != s.Title || back.Stats != s.Stats {
		t.Errorf("round trip lost data: %+v, want %+v", back.Stats, s.Stats)
	}
	if len(back.Nodes) != len(s.Nodes) {
		t.Fatalf("%d nodes after round trip, want %d", len(back.Nodes), len(s.Nodes))
	}
	for i := range back.Nodes {
		got, want := back.Nodes[i], s.Nodes[i]
		if got.ID != want.ID || got.Circle != want.Circle || got.Hash != want.Hash {
			t.Errorf("node %d changed: %+v, want %+v", i, got, want)
		}
		if !slices.Equal(got.Fields, want.Fields) {
			t.Errorf("node %s fields = %v, want %v", got.ID, got.Fields, want.Fields)
		}
	}
}
