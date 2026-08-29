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

# Chains

A chain defeats containment in a different way. In a run of commits or
transactions every node dominates the next, so the history nests inside itself
and draws as concentric rings with the oldest innermost. [Options.ChainKinds]
names the kinds whose same-kind edges are history rather than content — a
distinction only the source can draw — and everything else about a chain follows
from that naming.

Nesting is kept by default, because separating a history costs more than it
saves on real data. Most objects in a repository are shared across commits, and
it is the chain that gives them a single node every path runs through. Break it
with [Options.SeparateChains] and their immediate dominator becomes the root, so
they surface as top-level siblings and the hierarchy flattens into a scatter.

What is fixed instead is how a link is drawn. Each one is built around the link
it continues: the predecessor sits at the centre, and everything the link added
is spread around it as a ring rather than packed beside it as one more sibling.
That fills a ring which would otherwise be almost entirely void, and gives it a
reading — the ring is the difference between one link and the next.
[Options.MaxChain] then bounds how deep the nesting runs, so a long history does
not spend the whole zoom range on itself.

# Sizing

Leaves are sized from [github.com/danielriddell21/merkelbrot/graph.Node.Weight]
and from how many payload fields they carry, so a node with more inside it is
drawn larger. Every other node is sized to enclose its children plus
[Options.Padding]. Sibling discs are arranged with the front-chain algorithm,
which keeps the enclosing circle tight and therefore keeps the useful zoom range
as wide as possible; the links of a chain are the exception, and are ringed
around their predecessor instead.

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
	// Truncated reports that this node is the last link of a chain that [Options.MaxChain]
	// cut short, and Omitted counts the nodes dropped with the rest of it.
	Truncated bool
	Omitted   int
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
	// ChainKinds names the kinds whose same-kind edges are history rather than
	// content: "commit" for a git graph, "transaction" for a ledger. It only
	// identifies chains; [Options.MaxChain] and [Options.SeparateChains] act on
	// what it names.
	//
	// Only the source can draw this distinction. A commit points at both its
	// predecessor and its tree; a tree points at its subtrees. The first pair
	// shares a kind and the second does not, yet both are content in one case and
	// history in the other, and no property of the graph says which. Left empty,
	// every edge is treated as containment.
	ChainKinds []string
	// MaxChain limits how many links of a chain are nested before the remainder is
	// dropped, with zero meaning no limit.
	//
	// Nesting costs a constant factor of scale per link, so an unbounded history
	// spends the whole zoom range on itself: at a hundred commits the oldest is
	// around 10^-11 of the frame and can never share the screen with recent work.
	// Capping the chain keeps depth and scale bounded however long the history
	// grows. The last link kept is marked [Placed.Truncated] and counts what went
	// with it in [Placed.Omitted], so a renderer can say what is missing.
	//
	// Objects still reachable from the links that remain are unaffected: only what
	// nothing else refers to disappears.
	MaxChain int
	// SeparateChains lays the links of a chain out side by side rather than nested,
	// demoting the edges between them to reference [Link] values.
	//
	// It is off by default because it costs more than it saves on real data. Most
	// objects in a repository are shared across commits, and it is the chain that
	// gives them a single node every path runs through. Break it and their
	// immediate dominator becomes the root, so they surface as top-level siblings
	// and the hierarchy flattens into a scatter.
	SeparateChains bool
	// MinRing is the least thickness of the ring between a node's edge and its
	// largest child, as a fraction of that node's radius. It reserves room for a
	// container to be labelled where its contents would otherwise fill it. Default
	// 0.18; set a negative value for none.
	MinRing float64
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
	if o.MaxChain < 0 {
		o.MaxChain = 0
	}
	if o.MinRing == 0 {
		o.MinRing = 0.18
	}
	if o.MinRing < 0 || o.MinRing >= 1 {
		o.MinRing = 0
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
	// Omitted counts the nodes left out because [Options.MaxChain] cut the chain.
	Omitted int
}

// Pack lays the graph out as nested circles.
//
// The result is centred on the origin. Nodes are returned parents-first, so a
// renderer can draw them in order and have children land on top of their
// containers.
func Pack[K comparable](g *graph.Graph[K], opts Options) *Packing[K] {
	p := newPacker(g, opts.withDefaults())
	if p == nil {
		return &Packing[K]{}
	}
	p.contain()
	p.prune()
	p.countOmitted()
	p.size()
	return p.place()
}

// packer carries the working state of a single [Pack] call between its phases:
// build the flow graph, derive the containment tree, size every disc bottom-up,
// then walk down assigning absolute positions.
type packer[K comparable] struct {
	g     *graph.Graph[K]
	opts  Options
	ids   []K
	index map[K]int
	n     int
	start int

	f         flow
	chained   map[string]bool
	truncated []bool
	omitted   []int
	idom      []int
	rank      []int
	domKids   [][]int
	depth     []int
	include   []bool

	radius  []float64
	offsets [][]Circle
	// area is where a node's payload fields live, relative to the node's centre
	// and in units of its radius. A node holding both children and fields packs
	// the fields into a disc of their own so the two never overlap.
	area      []Circle
	unitCache map[int][]Circle
}

func newPacker[K comparable](g *graph.Graph[K], opts Options) *packer[K] {
	ids := make([]K, 0, g.Len())
	index := make(map[K]int, g.Len())
	for id := range g.IDs() {
		index[id] = len(ids)
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil
	}

	n := len(ids)
	p := &packer[K]{
		g:         g,
		opts:      opts,
		ids:       ids,
		index:     index,
		n:         n,
		start:     n,
		unitCache: make(map[int][]Circle),
	}

	// A virtual start node parents every root, which gives the dominator search a
	// single entry point even when the graph has several roots.
	p.f = flow{
		start:    p.start,
		children: make([][]int, n+1),
		preds:    make([][]int, n+1),
	}
	p.chained = make(map[string]bool, len(opts.ChainKinds))
	for _, kind := range opts.ChainKinds {
		if kind != "" {
			p.chained[kind] = true
		}
	}
	p.truncated = make([]bool, n)
	p.omitted = make([]int, n)

	for i, id := range ids {
		for child := range g.Children(id) {
			// A chain edge stays in the graph — and so is still reported as a link —
			// but is kept out of the containment tree when the links are separated.
			if opts.SeparateChains && p.isChainEdge(i, index[child]) {
				continue
			}
			c := index[child]
			p.f.children[i] = append(p.f.children[i], c)
			p.f.preds[c] = append(p.f.preds[c], i)
		}
	}
	for root := range g.Roots() {
		r := index[root]
		p.f.children[p.start] = append(p.f.children[p.start], r)
		p.f.preds[r] = append(p.f.preds[r], p.start)
	}
	p.capChain()

	// Demoting an edge can leave its child with no way in, so it becomes a root of
	// its own rather than dropping out of the layout entirely.
	if opts.SeparateChains && len(p.chained) > 0 {
		for i := range n {
			if len(p.f.preds[i]) == 0 {
				p.f.children[p.start] = append(p.f.children[p.start], i)
				p.f.preds[i] = append(p.f.preds[i], p.start)
			}
		}
	}
	return p
}

func (p *packer[K]) isChainEdge(parent, child int) bool {
	if parent == p.start || child == p.start || len(p.chained) == 0 {
		return false
	}
	a, _ := p.g.Node(p.ids[parent])
	b, _ := p.g.Node(p.ids[child])
	return p.chained[a.Kind] && a.Kind == b.Kind
}

func (p *packer[K]) chainChildren(i int) []int {
	var out []int
	for child := range p.g.Children(p.ids[i]) {
		if c := p.index[child]; p.isChainEdge(i, c) {
			out = append(out, c)
		}
	}
	return out
}

// capChain walks the chain out from each root and cuts it once it has run for
// [Options.MaxChain] links, so the depth the layout has to spend on a history
// does not grow with the history.
func (p *packer[K]) capChain() {
	if p.opts.MaxChain <= 0 || len(p.chained) == 0 {
		return
	}

	depth := make([]int, p.n)
	for i := range depth {
		depth[i] = -1
	}
	queue := make([]int, 0, len(p.f.children[p.start]))
	for _, r := range p.f.children[p.start] {
		if depth[r] < 0 {
			depth[r] = 1
			queue = append(queue, r)
		}
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		kids := p.chainChildren(cur)
		if depth[cur] >= p.opts.MaxChain {
			for _, c := range kids {
				p.f.children[cur] = removeInt(p.f.children[cur], c)
				p.f.preds[c] = removeInt(p.f.preds[c], cur)
			}
			if len(kids) > 0 {
				p.truncated[cur] = true
			}
			continue
		}
		for _, c := range kids {
			if depth[c] < 0 {
				depth[c] = depth[cur] + 1
				queue = append(queue, c)
			}
		}
	}
}

// countOmitted tallies what each cut left behind, once it is known which nodes
// made it into the layout.
func (p *packer[K]) countOmitted() {
	for i := range p.n {
		if !p.truncated[i] {
			continue
		}
		for _, c := range p.chainChildren(i) {
			for id := range p.g.Descendants(p.ids[c]) {
				if j := p.index[id]; !p.include[j] {
					p.omitted[i]++
				}
			}
		}
	}
}

func (p *packer[K]) contain() {
	p.idom = p.f.dominators()
	p.domKids = make([][]int, p.n+1)
	for i := range p.n {
		if parent := p.idom[i]; parent >= 0 && parent != i {
			p.domKids[parent] = append(p.domKids[parent], i)
		}
	}

	// Siblings are ordered by breadth-first rank rather than by the graph's own
	// discovery order, so discs appear in the order the source declared its
	// children instead of in the order a depth-first walk happened to reach them.
	p.rank = breadthFirstRank(p.f, p.n)
	for _, kids := range p.domKids {
		slices.SortFunc(kids, func(a, b int) int { return p.rank[a] - p.rank[b] })
	}
}

func (p *packer[K]) prune() {
	p.depth = make([]int, p.n+1)
	p.include = make([]bool, p.n+1)
	p.include[p.start] = true
	p.depth[p.start] = -1

	for queue := []int{p.start}; len(queue) > 0; {
		cur := queue[0]
		queue = queue[1:]
		for _, kid := range p.domKids[cur] {
			p.depth[kid] = p.depth[cur] + 1
			if p.opts.MaxDepth > 0 && p.depth[kid] >= p.opts.MaxDepth {
				continue
			}
			p.include[kid] = true
			queue = append(queue, kid)
		}
	}
	for i := range p.n {
		// A node the chain cap made unreachable has no dominator to detach it from.
		if !p.include[i] && p.idom[i] >= 0 {
			p.domKids[p.idom[i]] = removeInt(p.domKids[p.idom[i]], i)
		}
	}
}

func (p *packer[K]) size() {
	p.radius = make([]float64, p.n+1)
	p.offsets = make([][]Circle, p.n+1)
	p.area = make([]Circle, p.n+1)

	for _, v := range postorder(p.domKids, p.start) {
		kids := p.domKids[v]
		if len(kids) == 0 {
			p.radius[v] = leafRadius(p.g, p.ids, v, p.start, p.opts)
			p.area[v] = Circle{R: p.opts.PayloadFill}
			continue
		}

		fields := payloadCount(p.g, p.ids, v, p.start)
		// A link of a chain is built around the link it continues, so the ring it
		// adds holds its own content rather than a void.
		if core := p.chainCore(v, kids); core >= 0 {
			p.sizeAnnulus(v, core, kids, fields)
			continue
		}
		p.sizePacked(v, kids, fields)
	}
}

// sizePacked arranges a node's contents as siblings and sizes the node to
// enclose them, which is how everything but a chain is laid out.
func (p *packer[K]) sizePacked(v int, kids []int, fields int) {
	circles := make([]Circle, len(kids), len(kids)+1)
	for i, kid := range kids {
		circles[i].R = p.radius[kid]
	}
	if fields > 0 {
		circles = append(circles, Circle{R: payloadRadius(fields, p.opts)})
	}

	p.radius[v] = packSiblings(circles)
	// The virtual start is not drawn, so padding it would leave a dead ring around
	// the whole packing and shrink the useful zoom range.
	if v != p.start {
		p.radius[v] += p.opts.Padding
		p.radius[v] = max(p.radius[v], p.minRing(kids))
	}
	p.offsets[v] = circles[:len(kids)]
	if fields > 0 && p.radius[v] > 0 {
		p.area[v] = p.payloadArea(circles[len(kids)], p.radius[v])
	}
}

// minRing gives the radius a node needs for [Options.MinRing] to hold, which is
// what keeps a container whose largest child nearly fills it from having no room
// left to carry its own label.
func (p *packer[K]) minRing(kids []int) float64 {
	if p.opts.MinRing <= 0 {
		return 0
	}
	widest := 0.0
	for _, kid := range kids {
		widest = max(widest, p.radius[kid])
	}
	return widest / (1 - p.opts.MinRing)
}

func (p *packer[K]) payloadArea(slot Circle, radius float64) Circle {
	return Circle{
		X: slot.X / radius,
		Y: slot.Y / radius,
		R: slot.R / radius * p.opts.PayloadFill,
	}
}

func (p *packer[K]) place() *Packing[K] {
	centre := make([]Circle, p.n+1)
	centre[p.start] = Circle{R: p.radius[p.start]}
	out := &Packing[K]{Bounds: centre[p.start]}

	for queue := []int{p.start}; len(queue) > 0; {
		cur := queue[0]
		queue = queue[1:]
		for i, kid := range p.domKids[cur] {
			centre[kid] = Circle{
				X: centre[cur].X + p.offsets[cur][i].X,
				Y: centre[cur].Y + p.offsets[cur][i].Y,
				R: p.radius[kid],
			}
			out.Nodes = append(out.Nodes, p.placed(kid, cur, centre[kid]))
			queue = append(queue, kid)
		}
	}

	for i := range p.n {
		if p.include[i] {
			continue
		}
		out.Omitted++
	}
	out.Links = p.links()
	return out
}

func (p *packer[K]) links() []Link[K] {
	byRank := make([]int, p.n)
	for i := range p.n {
		byRank[i] = i
	}
	slices.SortFunc(byRank, func(a, b int) int { return p.rank[a] - p.rank[b] })

	var links []Link[K]
	for _, i := range byRank {
		if !p.include[i] {
			continue
		}
		for child := range p.g.Children(p.ids[i]) {
			if c := p.index[child]; p.include[c] && p.idom[c] != i {
				links = append(links, Link[K]{From: p.ids[i], To: child})
			}
		}
	}
	return links
}

func (p *packer[K]) placed(v, parent int, c Circle) Placed[K] {
	id := p.ids[v]
	n, _ := p.g.Node(id)
	out := Placed[K]{
		Circle:    c,
		ID:        id,
		Kind:      n.Kind,
		Label:     n.Label,
		Hash:      n.Hash,
		Depth:     p.depth[v],
		Leaf:      len(p.domKids[v]) == 0,
		Shared:    countAtLeastTwo(p.g, id),
		Truncated: p.truncated[v],
		Omitted:   p.omitted[v],
	}
	if parent != p.start {
		out.Parent, out.HasParent = p.ids[parent], true
	}
	if len(n.Payload) == 0 {
		return out
	}

	unit, ok := p.unitCache[len(n.Payload)]
	if !ok {
		unit = packUnit(len(n.Payload))
		p.unitCache[len(n.Payload)] = unit
	}
	area := p.area[v]
	out.Payload = make([]Slot, len(n.Payload))
	for i, field := range n.Payload {
		out.Payload[i] = Slot{
			Circle: Circle{
				X: area.X + unit[i].X*area.R,
				Y: area.Y + unit[i].Y*area.R,
				R: unit[i].R * area.R,
			},
			Field: field,
		}
	}
	return out
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
