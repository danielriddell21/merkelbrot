package graph

import (
	"iter"
	"slices"
)

// Descendants yields id and everything reachable from it, depth-first and deduplicated.
func (g *Graph[K]) Descendants(id K) iter.Seq[K] {
	return g.walk(id, func(k K) []K { return g.nodes[k].Children })
}

// Ancestors yields id and everything that reaches it, depth-first and deduplicated.
func (g *Graph[K]) Ancestors(id K) iter.Seq[K] {
	return g.walk(id, func(k K) []K { return g.parents[k] })
}

func (g *Graph[K]) walk(id K, next func(K) []K) iter.Seq[K] {
	return func(yield func(K) bool) {
		if _, ok := g.nodes[id]; !ok {
			return
		}
		seen := make(map[K]bool)
		stack := []K{id}
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[cur] {
				continue
			}
			seen[cur] = true
			if !yield(cur) {
				return
			}
			kids := next(cur)
			for i := len(kids) - 1; i >= 0; i-- {
				if !seen[kids[i]] {
					stack = append(stack, kids[i])
				}
			}
		}
	}
}

// BreadthFirst yields id and everything reachable from it, level by level.
func (g *Graph[K]) BreadthFirst(id K) iter.Seq[K] {
	return func(yield func(K) bool) {
		if _, ok := g.nodes[id]; !ok {
			return
		}
		seen := map[K]bool{id: true}
		queue := []K{id}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			if !yield(cur) {
				return
			}
			for _, child := range g.nodes[cur].Children {
				if !seen[child] {
					seen[child] = true
					queue = append(queue, child)
				}
			}
		}
	}
}

// TopologicalOrder yields every node with parents always preceding their children.
func (g *Graph[K]) TopologicalOrder() iter.Seq[K] {
	return func(yield func(K) bool) {
		pending := make(map[K]int, len(g.order))
		for _, id := range g.order {
			pending[id] = len(g.parents[id])
		}
		ready := make([]K, 0, len(g.roots))
		for _, id := range g.order {
			if pending[id] == 0 {
				ready = append(ready, id)
			}
		}
		for len(ready) > 0 {
			cur := ready[0]
			ready = ready[1:]
			if !yield(cur) {
				return
			}
			for _, child := range g.nodes[cur].Children {
				pending[child]--
				if pending[child] == 0 {
					ready = append(ready, child)
				}
			}
		}
	}
}

// Depths reports the shortest distance from any root to each node.
func (g *Graph[K]) Depths() map[K]int {
	depth := make(map[K]int, len(g.order))
	queue := make([]K, 0, len(g.roots))
	for _, id := range g.roots {
		depth[id] = 0
		queue = append(queue, id)
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, child := range g.nodes[cur].Children {
			if _, ok := depth[child]; !ok {
				depth[child] = depth[cur] + 1
				queue = append(queue, child)
			}
		}
	}
	return depth
}

// MemorySource is a [Source] backed by an in-memory node slice.
type MemorySource[K comparable] struct {
	roots []K
	nodes map[K]Node[K]
}

// NewMemorySource builds a source from the given roots and nodes.
func NewMemorySource[K comparable](roots []K, nodes ...Node[K]) *MemorySource[K] {
	s := &MemorySource[K]{roots: slices.Clone(roots), nodes: make(map[K]Node[K], len(nodes))}
	for _, n := range nodes {
		s.nodes[n.ID] = n
	}
	return s
}

// Roots yields the source's entry points.
func (s *MemorySource[K]) Roots() iter.Seq[K] { return slices.Values(s.roots) }

// Node returns the node with the given ID.
func (s *MemorySource[K]) Node(id K) (Node[K], bool) {
	n, ok := s.nodes[id]
	return n, ok
}
