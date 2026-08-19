/*
Package proof computes the Merkle evidence that makes a DAG worth visualising:
which nodes justify a leaf's membership of a root, which subtrees two roots have
in common, and which stored hashes no longer match their children.

Everything here is expressed in terms of graph structure rather than any
particular hash construction, so it applies equally to a git object graph, an
append-only transparency log and a batch of payment transactions. The one place
a domain must supply its own rule is [Verify], which takes a [Hasher] describing
how a parent's hash is derived from its children.

# Inclusion

[Inclusion] answers "why is this leaf part of this root?". It returns the
ancestor chain from the leaf up to the root together with, at each step, the
sibling nodes whose hashes an auditor would need in order to recompute the next
hash up. That pair of node sets is exactly what a renderer wants to highlight:
the path itself, and the evidence hanging off it.

Where several paths exist — normal in a DAG, where a shared subtree is reachable
through more than one parent — Inclusion returns the shortest, breaking ties by
the child order the source supplied.

# Consistency

[Consistency] compares two roots and reports the maximal subtrees they share,
along with what was added and removed. For an append-only log this is the
familiar consistency proof: the shared subtrees are the retained history, and
the added nodes are the new entries. For a git-shaped graph the same call
describes what one commit changed relative to another.

# Verification

[Verify] walks the graph bottom-up, recomputes each parent's hash from its
children with the supplied [Hasher], and reports every node whose stored hash
disagrees. A tampered or truncated source shows up as a small set of failing
nodes near the point of damage, which a renderer can colour directly.
*/
package proof

import (
	"bytes"
	"fmt"
	"slices"

	"github.com/danielriddell21/merkelbrot/graph"
)

// Path is the evidence that a leaf belongs to a root.
type Path[K comparable] struct {
	// Leaf is the node whose membership is being shown.
	Leaf K
	// Root is the node the leaf was proven against.
	Root K
	// Nodes is the ancestor chain from Leaf to Root inclusive.
	Nodes []K
	// Siblings[i] holds the other children of Nodes[i+1], needed to recompute it.
	// Its length is one less than Nodes.
	Siblings [][]K
}

// Len reports the number of steps from leaf to root.
func (p Path[K]) Len() int {
	if len(p.Nodes) == 0 {
		return 0
	}
	return len(p.Nodes) - 1
}

// Contains reports whether id lies on the path itself, ignoring siblings.
func (p Path[K]) Contains(id K) bool { return slices.Contains(p.Nodes, id) }

// Inclusion returns the shortest path proving that leaf is reachable from root.
//
// It reports false if either node is unknown or the leaf is not reachable. When
// leaf and root are the same node the path is that single node with no siblings.
func Inclusion[K comparable](g *graph.Graph[K], leaf, root K) (Path[K], bool) {
	if _, ok := g.Node(leaf); !ok {
		return Path[K]{}, false
	}
	if _, ok := g.Node(root); !ok {
		return Path[K]{}, false
	}

	// Breadth-first from the root so the first route found is the shortest, with
	// ties resolved by the child order the source supplied.
	from := make(map[K]K)
	seen := map[K]bool{root: true}
	queue := []K{root}
	found := root == leaf
	for len(queue) > 0 && !found {
		cur := queue[0]
		queue = queue[1:]
		for child := range g.Children(cur) {
			if seen[child] {
				continue
			}
			seen[child] = true
			from[child] = cur
			if child == leaf {
				found = true
				break
			}
			queue = append(queue, child)
		}
	}
	if !found {
		return Path[K]{}, false
	}

	p := Path[K]{Leaf: leaf, Root: root, Nodes: []K{leaf}}
	for cur := leaf; cur != root; {
		parent := from[cur]
		var siblings []K
		for child := range g.Children(parent) {
			if child != cur {
				siblings = append(siblings, child)
			}
		}
		p.Nodes = append(p.Nodes, parent)
		p.Siblings = append(p.Siblings, siblings)
		cur = parent
	}
	return p, true
}

// Delta describes how the subtree under one root relates to the subtree under another.
type Delta[K comparable] struct {
	// Old and New are the roots that were compared.
	Old, New K
	// Shared holds the maximal subtree roots present under both, in the order they
	// are met descending from New. Nodes beneath these are shared too and omitted.
	Shared []K
	// Added holds nodes reachable from New but not from Old.
	Added []K
	// Removed holds nodes reachable from Old but not from New.
	Removed []K
}

// Consistency compares the subtrees under two roots.
//
// It reports false if either root is unknown. The two roots need not be related:
// comparing unrelated roots simply yields no shared subtrees.
func Consistency[K comparable](g *graph.Graph[K], oldRoot, newRoot K) (Delta[K], bool) {
	if _, ok := g.Node(oldRoot); !ok {
		return Delta[K]{}, false
	}
	if _, ok := g.Node(newRoot); !ok {
		return Delta[K]{}, false
	}

	inOld := make(map[K]bool)
	for id := range g.Descendants(oldRoot) {
		inOld[id] = true
	}
	inNew := make(map[K]bool)
	for id := range g.Descendants(newRoot) {
		inNew[id] = true
	}

	d := Delta[K]{Old: oldRoot, New: newRoot}

	// Descend from the new root, stopping at the first node that already existed:
	// everything below it is shared as well, so only the topmost node is recorded.
	visited := make(map[K]bool)
	stack := []K{newRoot}
	for len(stack) > 0 {
		cur := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if visited[cur] {
			continue
		}
		visited[cur] = true
		if inOld[cur] {
			d.Shared = append(d.Shared, cur)
			continue
		}
		kids := slices.Collect(g.Children(cur))
		for i := len(kids) - 1; i >= 0; i-- {
			if !visited[kids[i]] {
				stack = append(stack, kids[i])
			}
		}
	}

	for id := range g.IDs() {
		switch {
		case inNew[id] && !inOld[id]:
			d.Added = append(d.Added, id)
		case inOld[id] && !inNew[id]:
			d.Removed = append(d.Removed, id)
		}
	}
	return d, true
}

// Hasher recomputes a node's hash from the node and its children's hashes.
//
// Child hashes are supplied in the node's own child order. Returning an error
// aborts [Verify].
type Hasher[K comparable] func(n graph.Node[K], childHashes [][]byte) ([]byte, error)

// Verify recomputes every node's hash bottom-up and reports those that disagree.
//
// Nodes are returned in topological order, parents ahead of children, so the
// highest damaged node in the graph comes first. A node whose stored hash is
// empty is skipped rather than reported, so partially hashed graphs verify the
// part they describe. An empty result means every stored hash was reproduced.
func Verify[K comparable](g *graph.Graph[K], h Hasher[K]) ([]K, error) {
	order := slices.Collect(g.TopologicalOrder())
	if len(order) != g.Len() {
		return nil, fmt.Errorf("proof: graph is not fully ordered: %d of %d nodes", len(order), g.Len())
	}

	computed := make(map[K][]byte, len(order))
	var bad []K
	for i := len(order) - 1; i >= 0; i-- {
		id := order[i]
		n, _ := g.Node(id)
		childHashes := make([][]byte, 0, len(n.Children))
		for _, child := range n.Children {
			childHashes = append(childHashes, computed[child])
		}
		sum, err := h(n, childHashes)
		if err != nil {
			return nil, fmt.Errorf("proof: hashing %v: %w", id, err)
		}
		computed[id] = sum
		if len(n.Hash) > 0 && !bytes.Equal(sum, n.Hash) {
			bad = append(bad, id)
		}
	}
	slices.Reverse(bad)
	return bad, nil
}
