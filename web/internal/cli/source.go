package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"

	"github.com/danielriddell21/merkelbrot/examples/gitrepo"
	"github.com/danielriddell21/merkelbrot/examples/ledger"
	"github.com/danielriddell21/merkelbrot/examples/synthetic"
	"github.com/danielriddell21/merkelbrot/graph"
	"github.com/danielriddell21/merkelbrot/layout"
	"github.com/danielriddell21/merkelbrot/proof"
	"github.com/danielriddell21/merkelbrot/scene"
	"github.com/danielriddell21/merkelbrot/web"
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
	verify   bool
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
	g, err := graph.NewLimited(src, graph.Limit{MaxNodes: o.maxNodes})
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", o.source, err)
	}
	return o.sceneFrom(src, g, title, chains, o.maxChain, o.maxNodes)
}

// sceneFrom lays a graph out and attaches whatever evidence was asked for.
func (o *options) sceneFrom(src graph.Source[string], g *graph.Graph[string], title string, chains []string, maxChain, read int) (*scene.Scene, error) {
	b := scene.Builder[string]{Title: title}
	s := b.Scene(layout.Pack(g, layout.Options{
		MaxDepth:       o.maxDepth,
		ChainKinds:     chains,
		MaxChain:       maxChain,
		SeparateChains: o.separate,
	}))

	// The limit the source was read under is the caller's to report: the layout is
	// given a graph and cannot know how much of one it is.
	s.Stats.Read = read

	if o.prove != "" {
		marks, err := o.inclusion(g, b)
		if err != nil {
			return nil, err
		}
		s.Add(marks...)
	}
	if o.diff != "" {
		marks, err := o.consistency(g, b)
		if err != nil {
			return nil, err
		}
		s.Add(marks...)
	}
	if o.verify {
		marks, err := verify(src, g, b)
		if err != nil {
			return nil, err
		}
		s.Add(marks...)
	}
	return s, nil
}

// verifiable is a source that knows how its own objects are named, which is the
// one thing [proof.Verify] cannot work out for itself.
type verifiable interface {
	Hasher() proof.Hasher[string]
}

// verify recomputes every node's hash and highlights those that disagree.
func verify(src graph.Source[string], g *graph.Graph[string], b scene.Builder[string]) ([]scene.Highlight, error) {
	v, ok := src.(verifiable)
	if !ok {
		return nil, errors.New("this source cannot be verified: only a source that knows how its objects are named can be")
	}
	bad, err := proof.Verify(g, v.Hasher())
	if err != nil {
		return nil, fmt.Errorf("verifying: %w", err)
	}
	return b.Invalid(bad), nil
}

// inclusion highlights the path proving the named node belongs to the graph.
func (o *options) inclusion(g *graph.Graph[string], b scene.Builder[string]) ([]scene.Highlight, error) {
	id, err := resolve(g, o.prove)
	if err != nil {
		return nil, err
	}
	// A graph may have several roots, and the node need only be under one of them.
	roots := slices.Collect(g.Roots())
	for _, root := range roots {
		if path, ok := proof.Inclusion(g, id, root); ok {
			return b.Inclusion(path), nil
		}
	}
	return nil, fmt.Errorf("no inclusion path from %s to any of %v", id, roots)
}

// consistency highlights what is shared, added and removed between two nodes.
func (o *options) consistency(g *graph.Graph[string], b scene.Builder[string]) ([]scene.Highlight, error) {
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
	return b.Consistency(d), nil
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

// grower answers a page's requests for more of the graph, keeping what it has
// already read between them.
//
// Rebuilding from scratch would ask the source for every node again each time
// somebody asked to see a little more, which on a large repository is the whole
// cost of the view. Holding the graph means a wider read costs only the part that
// is new, and asking for more history costs nothing at all — the same graph is
// simply laid out again.
type grower struct {
	opts   options
	src    graph.Source[string]
	title  string
	chains []string

	mu    sync.Mutex
	graph *graph.Graph[string]
	read  int
}

func newGrower(o *options) (*grower, error) {
	src, title, chains, err := o.open()
	if err != nil {
		return nil, err
	}
	g, err := graph.NewLimited(src, graph.Limit{MaxNodes: o.maxNodes})
	if err != nil {
		return nil, fmt.Errorf("loading %s: %w", o.source, err)
	}
	return &grower{opts: *o, src: src, title: title, chains: chains, graph: g, read: o.maxNodes}, nil
}

// scene serves the graph as it stands, reading further into the source first if
// that is what was asked for.
func (gr *grower) scene(ask web.Ask) (*scene.Scene, error) {
	gr.mu.Lock()
	defer gr.mu.Unlock()

	// The window only ever widens. A request for less than has already been read
	// cannot un-read it, so recording a smaller limit would only mislead the page
	// about what to ask for next; and once the source has been read whole, a limit
	// of zero, there is nothing further to ask for.
	if ask.Nodes != nil && gr.read != 0 && (*ask.Nodes == 0 || *ask.Nodes > gr.read) {
		grown, err := gr.graph.Grow(gr.src, graph.Limit{MaxNodes: *ask.Nodes})
		if err != nil {
			return nil, fmt.Errorf("reading further into %s: %w", gr.opts.source, err)
		}
		gr.graph, gr.read = grown, *ask.Nodes
	}

	maxChain := gr.opts.maxChain
	if ask.Chain != nil {
		maxChain = *ask.Chain
	}
	return gr.opts.sceneFrom(gr.src, gr.graph, gr.title, gr.chains, maxChain, gr.read)
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
