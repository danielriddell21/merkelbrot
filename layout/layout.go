/*
Package layout turns a Merkle DAG into nested circles that can be zoomed
continuously, from the whole graph down to the fields inside a single node.

The layout is a containment packing: every node is a disc, and the discs beneath
it are packed inside it. That makes the picture self-similar, so zooming in is not
a change of screen but simply a change of scale — the same drawing viewed closer.
A commit contains its tree, which contains its blobs; a transaction contains its
status log, which contains its ledger entries.

# Where a shared node lives

Containment needs each node to have exactly one home, which a DAG does not
provide: a deduplicated blob or a nominal account may be referenced from many
places. Rather than duplicating such nodes or picking a parent arbitrarily, the
layout nests each node inside its immediate dominator — the deepest node through
which every route from the root must pass. That placement is always truthful,
because there is no way to reach the node without entering its container first.

Every graph edge that is not also a containment edge is reported separately as a
[Link], which a renderer can draw as an arc between two discs. For a strict tree
there are no such links and the packing is exactly the tree.

# Sizing

Leaves are sized from [github.com/danielriddell21/merkelbrot/graph.Node.Weight]
and from how many payload fields they carry, so a node with more inside it is
drawn larger. Every other node is sized to enclose its children plus
[Options.Padding]. Sibling discs are arranged with the front-chain algorithm,
which keeps the enclosing circle tight and therefore keeps the useful zoom range
as wide as possible.

# Node internals

A node's payload fields are packed as equal circles and reported as [Slot] values
in coordinates relative to the node: an offset in units of the node's own radius,
so a renderer multiplies through by [Placed.R] to draw them.

Where a node has children as well as fields, the fields are packed into a disc of
their own that takes its place among the children, so text never lands on top of
nested content. A childless node gives its fields the whole of its interior.
Either way a renderer reveals them only once they are large enough on screen to
read, which is what gives the deepest zoom level something to show.

# Determinism

Packing the same graph twice always produces the same coordinates. The shuffle
inside the smallest-enclosing-circle search is seeded from a constant, and every
traversal follows the source's own ordering, so layouts can be compared in tests
and cached safely.
*/
package layout

import (
	"math"
	"slices"

	"github.com/danielriddell21/merkelbrot/graph"
)

// Circle is a disc in layout coordinates.
type Circle struct {
	X, Y, R float64
}

// Slot is one payload field positioned inside its node.
//
// X, Y and R are relative to the node that owns the slot and are expressed in
// units of that node's radius, so a renderer scales them by [Placed.R].
type Slot struct {
	Circle
	Field graph.Field
}

// Placed is a node with a position and a radius.
type Placed[K comparable] struct {
	Circle
	// ID is the node's ID in the source graph.
	ID K
	// Kind, Label and Hash are copied from the source node.
	Kind  string
	Label string
	Hash  []byte
	// Depth is the containment depth, zero for a top-level node.
	Depth int
	// Parent is the node this one is nested inside, valid only if HasParent.
	Parent    K
	HasParent bool
	// Shared reports whether the source node has more than one parent.
	Shared bool
	// Leaf reports whether the node contains no other nodes.
	Leaf bool
	// Payload holds the node's fields, packed inside it.
	Payload []Slot
}

// Link is a graph edge that containment could not express.
//
// It connects a node to a child nested somewhere else, which happens whenever a
// child is shared and therefore lives inside its immediate dominator instead.
type Link[K comparable] struct {
	From, To K
}

// Options configures [Pack]. The zero value is usable and picks sensible defaults.
type Options struct {
	// LeafRadius is the radius of a childless node with no payload. Default 1.
	LeafRadius float64
	// Padding is the gap added between a node's boundary and its contents. Default
	// is a tenth of LeafRadius.
	Padding float64
	// PayloadFill is the fraction of a node's radius its payload circles occupy,
	// between 0 and 1. Default 0.72.
	PayloadFill float64
	// MaxDepth limits how many containment levels are laid out, with zero meaning
	// no limit. Nodes deeper than the limit are omitted along with their contents.
	MaxDepth int
}

func (o Options) withDefaults() Options {
	if o.LeafRadius <= 0 {
		o.LeafRadius = 1
	}
	if o.Padding <= 0 {
		o.Padding = o.LeafRadius / 10
	}
	if o.PayloadFill <= 0 || o.PayloadFill > 1 {
		o.PayloadFill = 0.72
	}
	if o.MaxDepth < 0 {
		o.MaxDepth = 0
	}
	return o
}

// Packing is a laid-out graph.
type Packing[K comparable] struct {
	// Nodes holds every placed node, parents before children.
	Nodes []Placed[K]
	// Links holds the graph edges that containment does not already show.
	Links []Link[K]
	// Bounds is the circle enclosing the whole packing, centred on the origin.
	Bounds Circle
}

// Pack lays the graph out as nested circles.
//
// The result is centred on the origin. Nodes are returned parents-first, so a
// renderer can draw them in order and have children land on top of their
// containers.
func Pack[K comparable](g *graph.Graph[K], opts Options) *Packing[K] {
	opts = opts.withDefaults()

	ids := make([]K, 0, g.Len())
	index := make(map[K]int, g.Len())
	for id := range g.IDs() {
		index[id] = len(ids)
		ids = append(ids, id)
	}
	n := len(ids)
	if n == 0 {
		return &Packing[K]{}
	}

	// A virtual start node parents every root, which gives the dominator search a
	// single entry point even when the graph has several roots.
	start := n
	f := flow{
		start:    start,
		children: make([][]int, n+1),
		preds:    make([][]int, n+1),
	}
	for i, id := range ids {
		for child := range g.Children(id) {
			c := index[child]
			f.children[i] = append(f.children[i], c)
			f.preds[c] = append(f.preds[c], i)
		}
	}
	for root := range g.Roots() {
		r := index[root]
		f.children[start] = append(f.children[start], r)
		f.preds[r] = append(f.preds[r], start)
	}

	idom := f.dominators()
	domKids := make([][]int, n+1)
	for i := range n {
		if p := idom[i]; p >= 0 && p != i {
			domKids[p] = append(domKids[p], i)
		}
	}

	// Siblings are ordered by breadth-first rank rather than by the graph's own
	// discovery order, so discs appear in the order the source declared its
	// children instead of in the order a depth-first walk happened to reach them.
	rank := breadthFirstRank(f, n)
	for _, kids := range domKids {
		slices.SortFunc(kids, func(a, b int) int { return rank[a] - rank[b] })
	}

	depth := make([]int, n+1)
	include := make([]bool, n+1)
	include[start] = true
	depth[start] = -1
	for queue := []int{start}; len(queue) > 0; {
		cur := queue[0]
		queue = queue[1:]
		for _, kid := range domKids[cur] {
			depth[kid] = depth[cur] + 1
			if opts.MaxDepth > 0 && depth[kid] >= opts.MaxDepth {
				continue
			}
			include[kid] = true
			queue = append(queue, kid)
		}
	}
	for i := range n {
		if !include[i] {
			domKids[idom[i]] = removeInt(domKids[idom[i]], i)
		}
	}

	unitCache := make(map[int][]Circle)
	radius := make([]float64, n+1)
	offsets := make([][]Circle, n+1)
	// area is where a node's payload fields live, relative to the node's centre
	// and in units of its radius. A node holding both children and fields packs
	// the fields into a disc of their own so the two never overlap.
	area := make([]Circle, n+1)
	for _, v := range postorder(domKids, start) {
		kids := domKids[v]
		fields := payloadCount(g, ids, v, start)

		if len(kids) == 0 {
			radius[v] = leafRadius(g, ids, v, start, opts)
			area[v] = Circle{R: opts.PayloadFill}
			continue
		}

		circles := make([]Circle, len(kids), len(kids)+1)
		for i, kid := range kids {
			circles[i].R = radius[kid]
		}
		if fields > 0 {
			circles = append(circles, Circle{R: payloadRadius(fields, opts)})
		}
		radius[v] = packSiblings(circles)
		// The virtual start is not drawn, so padding it would leave a dead ring
		// around the whole packing and shrink the useful zoom range.
		if v != start {
			radius[v] += opts.Padding
		}
		offsets[v] = circles[:len(kids)]
		if fields > 0 && radius[v] > 0 {
			slot := circles[len(kids)]
			area[v] = Circle{
				X: slot.X / radius[v],
				Y: slot.Y / radius[v],
				R: slot.R / radius[v] * opts.PayloadFill,
			}
		}
	}

	centre := make([]Circle, n+1)
	centre[start] = Circle{R: radius[start]}
	out := &Packing[K]{Bounds: centre[start]}
	for queue := []int{start}; len(queue) > 0; {
		cur := queue[0]
		queue = queue[1:]
		for i, kid := range domKids[cur] {
			centre[kid] = Circle{
				X: centre[cur].X + offsets[cur][i].X,
				Y: centre[cur].Y + offsets[cur][i].Y,
				R: radius[kid],
			}
			out.Nodes = append(out.Nodes, placed(g, ids, kid, cur, start, depth[kid], centre[kid], len(domKids[kid]) == 0, area[kid], opts, unitCache))
			queue = append(queue, kid)
		}
	}

	byRank := make([]int, n)
	for i := range n {
		byRank[i] = i
	}
	slices.SortFunc(byRank, func(a, b int) int { return rank[a] - rank[b] })
	for _, i := range byRank {
		if !include[i] {
			continue
		}
		for child := range g.Children(ids[i]) {
			c := index[child]
			if include[c] && idom[c] != i {
				out.Links = append(out.Links, Link[K]{From: ids[i], To: child})
			}
		}
	}
	return out
}

func placed[K comparable](g *graph.Graph[K], ids []K, v, parent, start int, depth int, c Circle, leaf bool, area Circle, opts Options, unitCache map[int][]Circle) Placed[K] {
	id := ids[v]
	n, _ := g.Node(id)
	p := Placed[K]{
		Circle: c,
		ID:     id,
		Kind:   n.Kind,
		Label:  n.Label,
		Hash:   n.Hash,
		Depth:  depth,
		Leaf:   leaf,
	}
	if parent != start {
		p.Parent, p.HasParent = ids[parent], true
	}
	p.Shared = countAtLeastTwo(g, id)

	if len(n.Payload) > 0 {
		unit, ok := unitCache[len(n.Payload)]
		if !ok {
			unit = packUnit(len(n.Payload))
			unitCache[len(n.Payload)] = unit
		}
		p.Payload = make([]Slot, len(n.Payload))
		for i, field := range n.Payload {
			p.Payload[i] = Slot{
				Circle: Circle{
					X: area.X + unit[i].X*area.R,
					Y: area.Y + unit[i].Y*area.R,
					R: unit[i].R * area.R,
				},
				Field: field,
			}
		}
	}
	return p
}

func countAtLeastTwo[K comparable](g *graph.Graph[K], id K) bool {
	seen := 0
	for range g.Parents(id) {
		seen++
		if seen == 2 {
			return true
		}
	}
	return false
}

func payloadCount[K comparable](g *graph.Graph[K], ids []K, v, start int) int {
	if v == start {
		return 0
	}
	n, _ := g.Node(ids[v])
	return len(n.Payload)
}

func payloadRadius(fields int, opts Options) float64 {
	return opts.LeafRadius * math.Sqrt(float64(fields))
}

func leafRadius[K comparable](g *graph.Graph[K], ids []K, v, start int, opts Options) float64 {
	if v == start {
		return opts.LeafRadius
	}
	n, _ := g.Node(ids[v])
	w := n.Weight
	if w <= 0 {
		w = 1
	}
	r := opts.LeafRadius * math.Sqrt(w)
	if k := len(n.Payload); k > 0 {
		r = max(r, payloadRadius(k, opts))
	}
	return r
}

func breadthFirstRank(f flow, n int) []int {
	rank := make([]int, n+1)
	for i := range rank {
		rank[i] = -1
	}
	seen := make([]bool, n+1)
	seen[f.start] = true
	next := 0
	for queue := []int{f.start}; len(queue) > 0; {
		cur := queue[0]
		queue = queue[1:]
		for _, kid := range f.children[cur] {
			if seen[kid] {
				continue
			}
			seen[kid] = true
			rank[kid] = next
			next++
			queue = append(queue, kid)
		}
	}
	return rank
}

func postorder(domKids [][]int, start int) []int {
	type frame struct {
		node int
		next int
	}
	var out []int
	stack := []frame{{node: start}}
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		kids := domKids[top.node]
		if top.next >= len(kids) {
			out = append(out, top.node)
			stack = stack[:len(stack)-1]
			continue
		}
		kid := kids[top.next]
		top.next++
		stack = append(stack, frame{node: kid})
	}
	return out
}

func removeInt(s []int, v int) []int {
	for i, x := range s {
		if x == v {
			return append(s[:i], s[i+1:]...)
		}
	}
	return s
}
