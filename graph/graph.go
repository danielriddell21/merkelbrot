/*
Package graph defines the plug-in interface that merkelbrot uses to read Merkle
DAGs and trees, together with an indexed, immutable view over them.

Nothing in this package assumes a particular data source. A consumer describes
its own objects — git commits, IPFS blocks, payment transactions, a bespoke
Merkle tree — by implementing [Source], which has just two methods: one to yield
the graph's roots and one to resolve an ID to a [Node]. The ID type is a type
parameter constrained only by comparable, so a consumer may key its graph by
string, by a fixed-size hash array, or by any other comparable type.

# Nodes

A [Node] is a plain description of one vertex: its ID, its content hash, a
consumer-defined kind such as "commit" or "transaction", a short label, the IDs
of the nodes it references, and a payload of key/value fields holding its
internals. Payload fields are what the renderer reveals at the deepest zoom
level, so a commit might carry its author and message while a ledger entry
carries its nominal account code and amount.

Edges point from a node to its children. Parent edges are derived rather than
declared: [New] indexes them while materialising the graph, so a source never
has to maintain back-references itself.

# Trees and DAGs

Both strict trees and general DAGs are supported through the same interface. A
node referenced by several parents — a shared subtree, a blob reused across
commits, a nominal account posted to by many transactions — is stored once and
reports each of its parents from [Graph.Parents]. [Graph.IsTree] reports which
case a given graph turned out to be, and [Graph.Shared] yields exactly those
nodes with more than one parent.

Merkle structures are acyclic by construction, and [New] enforces that: a source
that reports a cycle fails with [ErrCycle] rather than producing a graph that
would hang a traversal.

# Traversal

Traversals are returned as iterators from the standard iter package, so they
compose with range-over-func and with the slices and maps helpers:

	for id := range g.Descendants(root) {
		n, _ := g.Node(id)
		fmt.Println(n.Label)
	}

[Graph.Descendants] and [Graph.Ancestors] walk depth-first, [Graph.BreadthFirst]
walks level by level, and [Graph.TopologicalOrder] yields every node with parents
ahead of children. All of them deduplicate, which matters on a DAG where the same
node is reachable by more than one path.

# Determinism

Layout and rendering must be reproducible, so every iterator in this package
yields nodes in a deterministic order derived from the order the source supplied
them, never from Go's randomised map iteration.
*/
package graph

import (
	"errors"
	"fmt"
	"iter"
	"math"
	"slices"
)

// ErrCycle reports that a source contained a cycle.
var ErrCycle = errors.New("graph: cycle detected")

// ErrMissingNode reports a reference to a node the source could not supply.
var ErrMissingNode = errors.New("graph: missing node")

// ErrNoRoots reports that a source supplied no roots.
var ErrNoRoots = errors.New("graph: source has no roots")

// Field is one key/value entry of a node's payload.
type Field struct {
	Key   string
	Value string
}

// Node describes a single vertex of a Merkle DAG.
type Node[K comparable] struct {
	// ID uniquely identifies the node within its source.
	ID K
	// Hash is the node's content hash, or nil if it has none.
	Hash []byte
	// Kind is a consumer-defined category such as "commit" or "transaction".
	Kind string
	// Label is a short human-readable name.
	Label string
	// Children lists the nodes this node references, in display order.
	Children []K
	// Payload holds the node's internals, revealed at the deepest zoom.
	Payload []Field
	// Weight is a relative size hint; values <= 0 are treated as 1. It is an area,
	// so a node's radius grows with its square root. See [Weigh] for turning a real
	// quantity into one.
	Weight float64
}

// Weigh maps a real quantity onto a [Node.Weight] on a logarithmic scale.
//
// Quantities worth drawing — file sizes, amounts of money, row counts — routinely
// span several orders of magnitude. Weight is an area, so passing one through
// directly gives the largest node a radius hundreds of times the smallest and
// leaves everything else a speck on the screen. Weigh compresses the range
// instead: the ordering is kept, so more is always visibly larger, while the
// whole graph stays within a factor of a few.
//
// unit is the quantity that earns a node one step above the smallest — a few
// hundred bytes for a file size, say, or a pound for money — and so decides how
// much of the range is spent on small values. A quantity of zero or less weighs
// 1, the same as a node with nothing to measure.
func Weigh(quantity, unit float64) float64 {
	if quantity <= 0 || unit <= 0 {
		return 1
	}
	return 1 + math.Log2(1+quantity/unit)
}

// Source supplies nodes to merkelbrot.
//
// Implementations decide what a node is and where it comes from; the two methods
// may read from memory, from disk, or from a network store. [New] calls Node once
// per reachable ID and caches the result, so implementations need not memoise.
//
// A source must be internally consistent for the duration of a [New] call: every
// ID reported by [Node.Children] must resolve, or New fails with [ErrMissingNode].
type Source[K comparable] interface {
	// Roots yields the entry points of the graph.
	Roots() iter.Seq[K]
	// Node returns the node with the given ID, reporting whether it exists.
	Node(id K) (Node[K], bool)
}

// Graph is an immutable indexed view over a [Source].
type Graph[K comparable] struct {
	nodes   map[K]Node[K]
	parents map[K][]K
	pos     map[K]int
	order   []K
	roots   []K
	tree    bool
}

// New materialises every node reachable from the source's roots.
//
// It resolves each node exactly once, indexes parent edges, and verifies that the
// result is acyclic. Duplicate roots are collapsed. New returns [ErrNoRoots] if the
// source yields no roots, [ErrMissingNode] if a child reference cannot be resolved,
// and [ErrCycle] if the source is not a DAG.
//
// The returned graph is a snapshot: later changes to the source are not observed.
func New[K comparable](src Source[K]) (*Graph[K], error) {
	g := &Graph[K]{
		nodes:   make(map[K]Node[K]),
		parents: make(map[K][]K),
		pos:     make(map[K]int),
	}
	seenRoot := make(map[K]bool)
	for id := range src.Roots() {
		if seenRoot[id] {
			continue
		}
		seenRoot[id] = true
		g.roots = append(g.roots, id)
	}
	if len(g.roots) == 0 {
		return nil, ErrNoRoots
	}
	for _, id := range g.roots {
		if err := g.load(src, id); err != nil {
			return nil, err
		}
	}
	// Children are popped in reverse push order, so parent lists are normalised
	// against the deterministic discovery order once loading has finished.
	for _, list := range g.parents {
		slices.SortStableFunc(list, func(a, b K) int { return g.pos[a] - g.pos[b] })
	}
	if err := g.checkAcyclic(); err != nil {
		return nil, err
	}
	g.tree = true
	for _, id := range g.order {
		if len(g.parents[id]) > 1 {
			g.tree = false
			break
		}
	}
	return g, nil
}

func (g *Graph[K]) load(src Source[K], id K) error {
	stack := []K{id}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if _, seen := g.nodes[cur]; seen {
			continue
		}
		n, ok := src.Node(cur)
		if !ok {
			return fmt.Errorf("%w: %v", ErrMissingNode, cur)
		}
		n.ID = cur
		g.nodes[cur] = n
		g.pos[cur] = len(g.order)
		g.order = append(g.order, cur)
		for _, child := range n.Children {
			g.parents[child] = append(g.parents[child], cur)
			stack = append(stack, child)
		}
	}
	return nil
}

func (g *Graph[K]) checkAcyclic() error {
	const (
		white = 0
		grey  = 1
		black = 2
	)
	colour := make(map[K]int, len(g.nodes))
	type frame struct {
		id   K
		next int
	}
	for _, root := range g.roots {
		if colour[root] == black {
			continue
		}
		stack := []frame{{id: root}}
		colour[root] = grey
		for len(stack) > 0 {
			top := &stack[len(stack)-1]
			kids := g.nodes[top.id].Children
			if top.next >= len(kids) {
				colour[top.id] = black
				stack = stack[:len(stack)-1]
				continue
			}
			child := kids[top.next]
			top.next++
			switch colour[child] {
			case grey:
				return fmt.Errorf("%w: %v -> %v", ErrCycle, top.id, child)
			case white:
				colour[child] = grey
				stack = append(stack, frame{id: child})
			}
		}
	}
	return nil
}

// Len reports the number of nodes in the graph.
func (g *Graph[K]) Len() int { return len(g.order) }

// IsTree reports whether every node has at most one parent.
func (g *Graph[K]) IsTree() bool { return g.tree }

// Node returns the node with the given ID.
func (g *Graph[K]) Node(id K) (Node[K], bool) {
	n, ok := g.nodes[id]
	return n, ok
}

// Roots yields the graph's entry points in source order.
func (g *Graph[K]) Roots() iter.Seq[K] { return slices.Values(g.roots) }

// IDs yields every node ID in deterministic discovery order.
func (g *Graph[K]) IDs() iter.Seq[K] { return slices.Values(g.order) }

// All yields every node in deterministic discovery order.
func (g *Graph[K]) All() iter.Seq2[K, Node[K]] {
	return func(yield func(K, Node[K]) bool) {
		for _, id := range g.order {
			if !yield(id, g.nodes[id]) {
				return
			}
		}
	}
}

// Children yields the nodes referenced by id, in display order.
func (g *Graph[K]) Children(id K) iter.Seq[K] {
	return slices.Values(g.nodes[id].Children)
}

// Parents yields the nodes referencing id, in discovery order.
func (g *Graph[K]) Parents(id K) iter.Seq[K] {
	return slices.Values(g.parents[id])
}

// Shared yields every node reachable from more than one parent.
func (g *Graph[K]) Shared() iter.Seq[K] {
	return func(yield func(K) bool) {
		for _, id := range g.order {
			if len(g.parents[id]) > 1 && !yield(id) {
				return
			}
		}
	}
}
