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
}

// sources are the readable source names, in the order they appear in help.
var sources = []string{"ledger", "synthetic", "git"}

// build reads the selected source and lays it out as a scene.
func (o *options) build() (*scene.Scene, error) {
	src, title, err := o.open()
	if err != nil {
		return nil, err
	}

	g, err := graph.New(src)
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", o.source, err)
	}

	b := scene.Builder[string]{Title: title}
	s := b.Scene(layout.Pack(g, layout.Options{MaxDepth: o.maxDepth}))

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

func (o *options) open() (graph.Source[string], string, error) {
	switch o.source {
	case "ledger":
		return ledger.New(ledger.Config{Seed: o.seed, Transactions: o.count}), "UK payments ledger", nil
	case "synthetic":
		cfg := synthetic.Config{Seed: o.seed, Commits: o.count, Depth: 3, Branching: 3, Vocabulary: 16}
		return synthetic.New(cfg), "synthetic Merkle DAG", nil
	case "git":
		repo, err := gitrepo.Open(o.repo, gitrepo.Config{MaxCommits: o.count})
		if err != nil {
			return nil, "", fmt.Errorf("reading repository: %w", err)
		}
		return repo, "git objects: " + o.repo, nil
	default:
		return nil, "", fmt.Errorf("unknown source %q, want one of %v", o.source, sources)
	}
}
