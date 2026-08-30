// Package cli wires the merkelbrot command tree.
package cli

import (
	"fmt"

	"github.com/spf13/cobra"
)

// Execute runs the command tree, reporting any error to the caller.
func Execute(version string) error {
	if err := newRoot(version).Execute(); err != nil {
		return fmt.Errorf("execute: %w", err)
	}
	return nil
}

// newRoot builds the command tree. Tests drive this directly so they can set
// arguments and capture output without going through os.Args.
func newRoot(version string) *cobra.Command {
	opts := &options{}

	root := &cobra.Command{
		Use:   "merkelbrot",
		Short: "Zoomable, fractal-style visualiser for Merkle DAGs and trees",
		Long: `merkelbrot draws a Merkle DAG as nested circles you can zoom through
continuously — from the whole graph, into a commit, into its trees, down to the
fields inside a single blob.

A source is either generated (a UK payments ledger, or a content-addressed
Merkle DAG) or read from a real git repository's object store.`,
		Version:      version,
		SilenceUsage: true,
	}

	root.PersistentFlags().StringVar(&opts.source, "source", "ledger", "source to read: ledger, synthetic or git")
	root.PersistentFlags().StringVar(&opts.repo, "repo", ".", "repository to read when --source=git")
	root.PersistentFlags().Uint64Var(&opts.seed, "seed", 1, "seed for a generated source")
	root.PersistentFlags().IntVarP(&opts.count, "count", "n", 24, "how much to read: transactions, or commits for synthetic and git")
	root.PersistentFlags().IntVar(&opts.maxDepth, "max-depth", 0, "limit containment levels, 0 for no limit")
	root.PersistentFlags().IntVar(&opts.maxNodes, "max-nodes", 0,
		"stop reading the source after this many nodes, 0 for no limit")
	root.PersistentFlags().StringVar(&opts.prove, "prove", "", "highlight the inclusion path for this node ID")
	root.PersistentFlags().StringVar(&opts.diff, "diff", "",
		"highlight what changed between two nodes, as old..new")
	root.PersistentFlags().BoolVar(&opts.verify, "verify", false,
		"recompute every hash and highlight anything that disagrees")
	root.PersistentFlags().BoolVar(&opts.separate, "separate-chains", false,
		"lay a history out side by side instead of nesting it as concentric rings")
	root.PersistentFlags().IntVar(&opts.maxChain, "max-chain", 12,
		"nest at most this many links of a history, 0 for no limit")

	root.AddCommand(serveCmd(opts), exportCmd(opts), sceneCmd(opts), completionCmd())
	return root
}
