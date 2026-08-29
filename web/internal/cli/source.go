package cli

import (
	"fmt"
	"slices"

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
	prove    string
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

	g, err := graph.New(src)
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
		roots := slices.Collect(g.Roots())
		path, ok := proof.Inclusion(g, o.prove, roots[0])
		if !ok {
			return nil, fmt.Errorf("no inclusion path from %s to %s", o.prove, roots[0])
		}
		s.Add(b.Inclusion(path)...)
	}
	return s, nil
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
