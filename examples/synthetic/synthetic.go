/*
Package synthetic generates a deterministic, content-addressed Merkle DAG.

It exists so the library and its renderer have something to draw without reading
anything off disk, and so tests have a graph of arbitrary size whose shape is
reproducible from a seed.

Nodes are built bottom-up and addressed by the hash of their contents, exactly as
a real Merkle store would, which means sharing arises on its own: two subtrees
built from the same children collapse to one node with several parents. Lowering
[Config.Vocabulary] narrows the pool of distinct leaves, so more subtrees coincide
and the whole graph collapses to fewer, more heavily shared nodes.

This package is example code. It is here to demonstrate the
[github.com/danielriddell21/merkelbrot/graph.Source] interface and carries no
compatibility promise.
*/
package synthetic

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"iter"
	"math/rand/v2"
	"slices"

	"github.com/danielriddell21/merkelbrot/graph"
)

// Config describes the graph to generate. The zero value is usable.
type Config struct {
	// Seed makes generation reproducible. Default 1.
	Seed uint64
	// Commits is the length of the chain of roots. Default 4.
	Commits int
	// Depth is the number of tree levels beneath each commit. Default 2.
	Depth int
	// Branching is how many children each tree holds. Default 3.
	Branching int
	// Vocabulary is the number of distinct leaves available. Smaller values make
	// more subtrees coincide, collapsing the graph to fewer nodes. Default 12.
	Vocabulary int
}

func (c Config) withDefaults() Config {
	if c.Seed == 0 {
		c.Seed = 1
	}
	if c.Commits <= 0 {
		c.Commits = 4
	}
	if c.Depth <= 0 {
		c.Depth = 2
	}
	if c.Branching <= 0 {
		c.Branching = 3
	}
	if c.Vocabulary <= 0 {
		c.Vocabulary = 12
	}
	return c
}

// Source is a generated Merkle DAG.
type Source struct {
	nodes map[string]graph.Node[string]
	head  string
}

// New generates a graph from the configuration.
func New(cfg Config) *Source {
	cfg = cfg.withDefaults()
	rng := rand.New(rand.NewPCG(cfg.Seed, 0x6d65726b656c62))
	s := &Source{nodes: make(map[string]graph.Node[string])}

	leaves := make([]string, cfg.Vocabulary)
	for i := range leaves {
		label := fmt.Sprintf("blob-%03d", i)
		leaves[i] = s.put("blob", label, nil, []graph.Field{
			{Key: "size", Value: fmt.Sprint(64 + rng.IntN(4096))},
			{Key: "mode", Value: "100644"},
		})
	}

	var prev string
	for c := range cfg.Commits {
		level := leaves
		for d := range cfg.Depth {
			next := make([]string, 0, max(1, len(level)/cfg.Branching))
			for len(next)*cfg.Branching < len(level) {
				kids := make([]string, 0, cfg.Branching)
				for range cfg.Branching {
					kids = append(kids, level[rng.IntN(len(level))])
				}
				slices.Sort(kids)
				kids = slices.Compact(kids)
				next = append(next, s.put("tree", fmt.Sprintf("level-%d", d), kids, nil))
			}
			level = next
		}
		root := level[0]
		if len(level) > 1 {
			slices.Sort(level)
			root = s.put("tree", "/", slices.Compact(level), nil)
		}

		children := []string{}
		if prev != "" {
			children = append(children, prev)
		}
		children = append(children, root)
		prev = s.put("commit", fmt.Sprintf("commit %d", c+1), children, []graph.Field{
			{Key: "author", Value: "Ada Lovelace <ada@example.co.uk>"},
			{Key: "date", Value: fmt.Sprintf("2026-01-%02dT09:00:00Z", 1+c%28)},
		})
	}
	s.head = prev
	return s
}

func (s *Source) put(kind, label string, children []string, payload []graph.Field) string {
	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00", kind, label)
	for _, c := range children {
		h.Write([]byte(c))
		h.Write([]byte{0})
	}
	for _, f := range payload {
		fmt.Fprintf(h, "%s=%s\x00", f.Key, f.Value)
	}
	sum := h.Sum(nil)
	id := hex.EncodeToString(sum[:6])
	if _, ok := s.nodes[id]; !ok {
		s.nodes[id] = graph.Node[string]{
			ID:       id,
			Hash:     sum,
			Kind:     kind,
			Label:    label,
			Children: children,
			Payload:  payload,
		}
	}
	return id
}

// Roots yields the head of the generated chain.
func (s *Source) Roots() iter.Seq[string] {
	return func(yield func(string) bool) { yield(s.head) }
}

// Node returns the node with the given ID.
func (s *Source) Node(id string) (graph.Node[string], bool) {
	n, ok := s.nodes[id]
	return n, ok
}

// Len reports how many distinct nodes were generated.
func (s *Source) Len() int { return len(s.nodes) }
