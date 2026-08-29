package cli

import (
	"fmt"
	"slices"
	"strings"

	"github.com/danielriddell21/merkelbrot/examples/gitrepo"
	"github.com/danielriddell21/merkelbrot/examples/ledger"
	"github.com/danielriddell21/merkelbrot/examples/synthetic"
	"github.com/danielriddell21/merkelbrot/graph"
	"github.com/danielriddell21/merkelbrot/layout"
	"github.com/danielriddell21/merkelbrot/proof"
	"github.com/danielriddell21/merkelbrot/scene"
)

// options holds the flags shared by every command that reads a graph.
type options struct {
	source   string
	repo     string
	seed     uint64
	count    int
	maxDepth int
	maxNodes int
	prove    string
	diff     string
	addr     string
	separate bool
	maxChain int
}

// sources are the readable source names, in the order they appear in help.
var sources = []string{"ledger", "synthetic", "git"}

// build reads the selected source and lays it out as a scene.
func (o *options) build() (*scene.Scene, error) {
	src, title, chains, err := o.open()
	if err != nil {
		return nil, err
	}
	// Nesting is the default: separating a history leaves most objects shared
	// between the links rather than owned by any one of them, so dominance has
	// almost nothing left to express and the picture flattens out.

	g, err := graph.NewLimited(src, graph.Limit{MaxNodes: o.maxNodes})
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", o.source, err)
	}

	b := scene.Builder[string]{Title: title}
	s := b.Scene(layout.Pack(g, layout.Options{
		MaxDepth:       o.maxDepth,
		ChainKinds:     chains,
		MaxChain:       o.maxChain,
		SeparateChains: o.separate,
	}))

	if o.prove != "" {
		id, err := resolve(g, o.prove)
		if err != nil {
			return nil, err
		}
		roots := slices.Collect(g.Roots())
		path, ok := proof.Inclusion(g, id, roots[0])
		if !ok {
			return nil, fmt.Errorf("no inclusion path from %s to %s", id, roots[0])
		}
		s.Add(b.Inclusion(path)...)
	}

	if o.diff != "" {
		before, after, ok := strings.Cut(o.diff, "..")
		if !ok || before == "" || after == "" {
			return nil, fmt.Errorf(`--diff wants two node IDs separated by "..", got %q`, o.diff)
		}
		oldID, err := resolve(g, before)
		if err != nil {
			return nil, err
		}
		newID, err := resolve(g, after)
		if err != nil {
			return nil, err
		}
		d, ok := proof.Consistency(g, oldID, newID)
		if !ok {
			return nil, fmt.Errorf("cannot compare %s with %s", oldID, newID)
		}
		s.Add(b.Consistency(d)...)
	}
	return s, nil
}

// resolve turns a node reference into an ID, accepting any unique prefix.
//
// Content-addressed IDs are long, and a reference typed by hand is almost always
// the first few characters of one, as it would be for git.
func resolve(g *graph.Graph[string], ref string) (string, error) {
	if _, ok := g.Node(ref); ok {
		return ref, nil
	}
	var found []string
	for id := range g.IDs() {
		if strings.HasPrefix(id, ref) {
			found = append(found, id)
			if len(found) > 1 {
				break
			}
		}
	}
	switch len(found) {
	case 0:
		return "", fmt.Errorf("no node with ID %q", ref)
	case 1:
		return found[0], nil
	default:
		return "", fmt.Errorf("%q matches more than one node", ref)
	}
}

// buildWith reads the selected source and lays it out with the given limit on how
// many links of a chain are nested, which is what a served page asks for when it
// wants history the limit left out.
func (o *options) buildWith(maxChain int) (*scene.Scene, error) {
	// The options are copied so that answering one request for more history does
	// not change what every later request gets.
	with := *o
	with.maxChain = maxChain
	return with.build()
}

// open returns the source, its title, and the kinds whose same-kind edges are
// history rather than content.
func (o *options) open() (graph.Source[string], string, []string, error) {
	switch o.source {
	case "ledger":
		cfg := ledger.Config{Seed: o.seed, Transactions: o.count}
		return ledger.New(cfg), "UK payments ledger", []string{"transaction"}, nil
	case "synthetic":
		cfg := synthetic.Config{Seed: o.seed, Commits: o.count, Depth: 3, Branching: 3, Vocabulary: 16}
		return synthetic.New(cfg), "synthetic Merkle DAG", []string{"commit"}, nil
	case "git":
		repo, err := gitrepo.Open(o.repo, gitrepo.Config{MaxCommits: o.count})
		if err != nil {
			return nil, "", nil, fmt.Errorf("reading repository: %w", err)
		}
		return repo, "git objects: " + o.repo, []string{"commit"}, nil
	default:
		return nil, "", nil, fmt.Errorf("unknown source %q, want one of %v", o.source, sources)
	}
}
