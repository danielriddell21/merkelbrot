// Command merkelbrot serves or exports a zoomable view of a Merkle DAG.
//
// Usage:
//
//	merkelbrot serve  [flags]   start the viewer on a local address
//	merkelbrot export [flags]   write a self-contained HTML page to stdout
//	merkelbrot scene  [flags]   write the scene as JSON to stdout
//	merkelbrot version          print the build version
//
// Sources are a generated UK payments ledger, a generated content-addressed
// Merkle DAG, or the object graph of a real git repository. Run with -h for the
// full flag set.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"slices"
	"time"

	"github.com/danielriddell21/merkelbrot/examples/gitrepo"
	"github.com/danielriddell21/merkelbrot/examples/ledger"
	"github.com/danielriddell21/merkelbrot/examples/synthetic"
	"github.com/danielriddell21/merkelbrot/graph"
	"github.com/danielriddell21/merkelbrot/layout"
	"github.com/danielriddell21/merkelbrot/proof"
	"github.com/danielriddell21/merkelbrot/scene"
	"github.com/danielriddell21/merkelbrot/web"
)

// version is set by the release build.
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "merkelbrot:", err)
		os.Exit(1)
	}
}

type options struct {
	source   string
	repo     string
	seed     uint64
	count    int
	maxDepth int
	addr     string
	prove    string
}

func (o *options) bind(fs *flag.FlagSet) {
	fs.StringVar(&o.source, "source", "ledger", "source: ledger, synthetic or git")
	fs.StringVar(&o.repo, "repo", ".", "repository to read when -source=git")
	fs.Uint64Var(&o.seed, "seed", 1, "seed for the generated source")
	fs.IntVar(&o.count, "n", 24, "how much to read: transactions, or commits for synthetic and git")
	fs.IntVar(&o.maxDepth, "max-depth", 0, "limit containment levels, 0 for no limit")
	fs.StringVar(&o.prove, "prove", "", "highlight the inclusion path for this node ID")
	fs.StringVar(&o.addr, "addr", "127.0.0.1:8080", "address to listen on")
}

func run(args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("no command given")
	}

	var opts options
	fs := flag.NewFlagSet("merkelbrot "+args[0], flag.ContinueOnError)
	opts.bind(fs)

	switch args[0] {
	case "serve":
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		return serve(&opts)
	case "export":
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		s, err := build(&opts)
		if err != nil {
			return err
		}
		return web.Render(context.Background(), os.Stdout, s)
	case "scene":
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		s, err := build(&opts)
		if err != nil {
			return err
		}
		return s.WriteJSON(os.Stdout)
	case "version", "-v", "--version":
		fmt.Println("merkelbrot", version)
		return nil
	case "-h", "--help", "help":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `merkelbrot visualises a Merkle DAG as a zoomable, nested packing.

Usage:
  merkelbrot serve  [flags]   start the viewer on a local address
  merkelbrot export [flags]   write a self-contained HTML page to stdout
  merkelbrot scene  [flags]   write the scene as JSON to stdout
  merkelbrot version          print the build version

Run "merkelbrot serve -h" for the available flags.
`)
}

func build(o *options) (*scene.Scene, error) {
	var src graph.Source[string]
	var title string
	switch o.source {
	case "ledger":
		src = ledger.New(ledger.Config{Seed: o.seed, Transactions: o.count})
		title = "UK payments ledger"
	case "synthetic":
		src = synthetic.New(synthetic.Config{Seed: o.seed, Commits: o.count, Depth: 3, Branching: 3, Vocabulary: 16})
		title = "synthetic Merkle DAG"
	case "git":
		repo, err := gitrepo.Open(o.repo, gitrepo.Config{MaxCommits: o.count})
		if err != nil {
			return nil, err
		}
		src = repo
		title = "git objects: " + o.repo
	default:
		return nil, fmt.Errorf("unknown source %q, want ledger, synthetic or git", o.source)
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

func serve(o *options) error {
	s, err := build(o)
	if err != nil {
		return err
	}

	listener, err := net.Listen("tcp", o.addr)
	if err != nil {
		return fmt.Errorf("listening on %s: %w", o.addr, err)
	}
	fmt.Fprintf(os.Stderr, "merkelbrot: %d nodes on http://%s\n", s.Stats.Nodes, listener.Addr())

	srv := &http.Server{
		Handler:           web.Handler(s),
		ReadHeaderTimeout: 5 * time.Second,
	}
	if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
