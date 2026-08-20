/*
Package scene turns a packed graph into a flat, serialisable view model.

A [Scene] is the boundary between merkelbrot's pure-Go core and whatever draws
it. It holds no type parameters, no interfaces and no behaviour: just numbers and
strings that marshal straight to JSON. A renderer — the templ web front end in
this repository, or anything else — consumes a scene without needing to know how
the graph was sourced, dominated or packed.

# Building a scene

Because the core is generic over node ID types and a scene is not, IDs are
stringified on the way through. A [Builder] holds that conversion, along with the
handful of presentation choices worth making in Go rather than in the client:

	b := scene.Builder[string]{Title: "payments"}
	s := b.Scene(layout.Pack(g, layout.Options{}))

The default conversion is fmt.Sprint, which is right for string and integer IDs.
Supply [Builder.ID] for anything else, such as a hash array that should render as
hex.

# Highlights

Merkle evidence travels alongside the geometry as a [Highlight]: a named set of
nodes and links the renderer can pick out. [Builder.Inclusion] turns an audit path
into two highlights, one for the path itself and one for the sibling hashes it
depends on, and [Builder.Consistency] turns a delta into shared, added and removed
sets. Highlights are additive and carry a [Highlight.Kind] the client maps to a
colour, so several can be shown at once.

# Coordinates

Positions and radii are in layout units, centred on the origin, with [Scene.Bounds]
giving the enclosing circle. Nothing here is in pixels: the client picks a scale
and applies its own transform, which is what allows zoom to be continuous rather
than a series of pre-rendered levels. Field positions inside a node stay relative
to that node and in units of its radius, exactly as the layout reported them.
*/
package scene

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"slices"

	"github.com/danielriddell21/merkelbrot/layout"
	"github.com/danielriddell21/merkelbrot/proof"
)

// Version is the schema version embedded in every scene.
const Version = 1

// Highlight kinds produced by [Builder.Inclusion] and [Builder.Consistency].
const (
	KindPath     = "path"
	KindEvidence = "evidence"
	KindShared   = "shared"
	KindAdded    = "added"
	KindRemoved  = "removed"
	KindInvalid  = "invalid"
)

// Circle is a disc in scene coordinates.
type Circle struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
	R float64 `json:"r"`
}

// Field is one payload entry positioned inside its node.
//
// Its coordinates are relative to the owning node and expressed in units of that
// node's radius.
type Field struct {
	Circle
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Node is a placed node ready to draw.
type Node struct {
	Circle
	ID     string  `json:"id"`
	Parent string  `json:"parent,omitempty"`
	Kind   string  `json:"kind,omitempty"`
	Label  string  `json:"label,omitempty"`
	Hash   string  `json:"hash,omitempty"`
	Depth  int     `json:"depth"`
	Leaf   bool    `json:"leaf,omitempty"`
	Shared bool    `json:"shared,omitempty"`
	Fields []Field `json:"fields,omitempty"`
}

// Link is an edge that containment does not already show.
type Link struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Highlight is a named set of nodes and links for the renderer to pick out.
type Highlight struct {
	Name  string   `json:"name"`
	Kind  string   `json:"kind"`
	Nodes []string `json:"nodes,omitempty"`
	Links []Link   `json:"links,omitempty"`
}

// Stats summarises a scene, for a caption or an overview panel.
type Stats struct {
	Nodes    int `json:"nodes"`
	Links    int `json:"links"`
	Shared   int `json:"shared"`
	Leaves   int `json:"leaves"`
	MaxDepth int `json:"maxDepth"`
}

// Scene is a flat, serialisable description of a laid-out graph.
type Scene struct {
	Version    int         `json:"version"`
	Title      string      `json:"title,omitempty"`
	Bounds     Circle      `json:"bounds"`
	Nodes      []Node      `json:"nodes"`
	Links      []Link      `json:"links,omitempty"`
	Kinds      []string    `json:"kinds,omitempty"`
	Highlights []Highlight `json:"highlights,omitempty"`
	Stats      Stats       `json:"stats"`
}

// Builder converts packings and proofs for a particular node ID type into scenes.
//
// The zero value is usable and stringifies IDs with fmt.Sprint.
type Builder[K comparable] struct {
	// Title labels the scene in the renderer.
	Title string
	// ID converts a node ID to its scene identifier. Defaults to fmt.Sprint.
	ID func(K) string
}

func (b Builder[K]) id(k K) string {
	if b.ID != nil {
		return b.ID(k)
	}
	return fmt.Sprint(k)
}

// Scene converts a packing into a serialisable scene.
func (b Builder[K]) Scene(p *layout.Packing[K]) *Scene {
	s := &Scene{
		Version: Version,
		Title:   b.Title,
		Bounds:  Circle{X: p.Bounds.X, Y: p.Bounds.Y, R: p.Bounds.R},
		Nodes:   make([]Node, 0, len(p.Nodes)),
	}

	seenKind := make(map[string]bool)
	for _, n := range p.Nodes {
		out := Node{
			Circle: Circle{X: n.X, Y: n.Y, R: n.R},
			ID:     b.id(n.ID),
			Kind:   n.Kind,
			Label:  n.Label,
			Depth:  n.Depth,
			Leaf:   n.Leaf,
			Shared: n.Shared,
		}
		if n.HasParent {
			out.Parent = b.id(n.Parent)
		}
		if len(n.Hash) > 0 {
			out.Hash = hex.EncodeToString(n.Hash)
		}
		for _, f := range n.Payload {
			out.Fields = append(out.Fields, Field{
				Circle: Circle{X: f.X, Y: f.Y, R: f.R},
				Key:    f.Field.Key,
				Value:  f.Field.Value,
			})
		}
		if n.Kind != "" && !seenKind[n.Kind] {
			seenKind[n.Kind] = true
			s.Kinds = append(s.Kinds, n.Kind)
		}
		if n.Shared {
			s.Stats.Shared++
		}
		if n.Leaf {
			s.Stats.Leaves++
		}
		s.Stats.MaxDepth = max(s.Stats.MaxDepth, n.Depth)
		s.Nodes = append(s.Nodes, out)
	}

	for _, l := range p.Links {
		s.Links = append(s.Links, Link{From: b.id(l.From), To: b.id(l.To)})
	}
	s.Stats.Nodes = len(s.Nodes)
	s.Stats.Links = len(s.Links)
	return s
}

// Inclusion converts an audit path into highlights for the path and its evidence.
//
// The first highlight holds the chain from leaf to root; the second holds the
// sibling nodes whose hashes the chain depends on. A path with no siblings yields
// only the first.
func (b Builder[K]) Inclusion(p proof.Path[K]) []Highlight {
	if len(p.Nodes) == 0 {
		return nil
	}
	path := Highlight{
		Name:  fmt.Sprintf("%s in %s", b.id(p.Leaf), b.id(p.Root)),
		Kind:  KindPath,
		Nodes: make([]string, 0, len(p.Nodes)),
	}
	for i, id := range p.Nodes {
		path.Nodes = append(path.Nodes, b.id(id))
		if i+1 < len(p.Nodes) {
			path.Links = append(path.Links, Link{From: b.id(p.Nodes[i+1]), To: b.id(id)})
		}
	}

	evidence := Highlight{Name: "supporting hashes", Kind: KindEvidence}
	for _, siblings := range p.Siblings {
		for _, id := range siblings {
			evidence.Nodes = append(evidence.Nodes, b.id(id))
		}
	}
	if len(evidence.Nodes) == 0 {
		return []Highlight{path}
	}
	slices.Sort(evidence.Nodes)
	evidence.Nodes = slices.Compact(evidence.Nodes)
	return []Highlight{path, evidence}
}

// Consistency converts a delta into shared, added and removed highlights.
//
// Empty sets are omitted, so comparing a root with itself yields a single
// highlight.
func (b Builder[K]) Consistency(d proof.Delta[K]) []Highlight {
	var out []Highlight
	add := func(name, kind string, ids []K) {
		if len(ids) == 0 {
			return
		}
		h := Highlight{Name: name, Kind: kind, Nodes: make([]string, 0, len(ids))}
		for _, id := range ids {
			h.Nodes = append(h.Nodes, b.id(id))
		}
		out = append(out, h)
	}
	add("retained", KindShared, d.Shared)
	add("added", KindAdded, d.Added)
	add("removed", KindRemoved, d.Removed)
	return out
}

// Invalid converts failing nodes from [proof.Verify] into a highlight.
func (b Builder[K]) Invalid(ids []K) []Highlight {
	if len(ids) == 0 {
		return nil
	}
	h := Highlight{Name: "hash mismatch", Kind: KindInvalid, Nodes: make([]string, 0, len(ids))}
	for _, id := range ids {
		h.Nodes = append(h.Nodes, b.id(id))
	}
	return []Highlight{h}
}

// Add appends highlights to the scene.
func (s *Scene) Add(highlights ...Highlight) {
	s.Highlights = append(s.Highlights, highlights...)
}

// WriteJSON writes the scene as compact JSON.
func (s *Scene) WriteJSON(w io.Writer) error {
	return json.NewEncoder(w).Encode(s)
}
