package ledger_test

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/danielriddell21/merkelbrot/examples/ledger"
	"github.com/danielriddell21/merkelbrot/graph"
	"github.com/danielriddell21/merkelbrot/layout"
)

func build(t *testing.T, cfg ledger.Config) *graph.Graph[string] {
	t.Helper()
	g, err := graph.New(ledger.New(cfg))
	if err != nil {
		t.Fatalf("graph.New: %v", err)
	}
	return g
}

func fields(n graph.Node[string]) map[string]string {
	m := make(map[string]string, len(n.Payload))
	for _, f := range n.Payload {
		m[f.Key] = f.Value
	}
	return m
}

func TestLedgerLoads(t *testing.T) {
	g := build(t, ledger.Config{Transactions: 20})
	if g.Len() < 20 {
		t.Errorf("generated %d nodes, want at least 20", g.Len())
	}
	if g.IsTree() {
		t.Error("IsTree() = true, want a DAG with shared entries")
	}
}

func TestLedgerIsDeterministic(t *testing.T) {
	first := slices.Collect(build(t, ledger.Config{Seed: 7, Transactions: 15}).TopologicalOrder())
	second := slices.Collect(build(t, ledger.Config{Seed: 7, Transactions: 15}).TopologicalOrder())
	if !slices.Equal(first, second) {
		t.Error("the same seed produced a different ledger")
	}
}

func TestDifferentSeedsDiffer(t *testing.T) {
	a := slices.Collect(build(t, ledger.Config{Seed: 1, Transactions: 15}).TopologicalOrder())
	b := slices.Collect(build(t, ledger.Config{Seed: 2, Transactions: 15}).TopologicalOrder())
	if slices.Equal(a, b) {
		t.Error("different seeds produced an identical ledger")
	}
}

func TestKindsMirrorGitObjects(t *testing.T) {
	g := build(t, ledger.Config{Transactions: 12})
	counts := map[string]int{}
	for _, n := range g.All() {
		counts[n.Kind]++
	}
	for _, kind := range []string{"transaction", "log", "entry"} {
		if counts[kind] == 0 {
			t.Errorf("no nodes of kind %q, want the commit/tree/blob analogue", kind)
		}
	}
	if len(counts) != 3 {
		t.Errorf("kinds = %v, want exactly transaction, log and entry", counts)
	}
}

// TestTransactionsPointAtOneHistory mirrors a commit pointing at exactly one root
// tree: the transaction's only non-parent child is its history log.
func TestTransactionsPointAtOneHistory(t *testing.T) {
	g := build(t, ledger.Config{Transactions: 12})
	for id, n := range g.All() {
		if n.Kind != "transaction" {
			continue
		}
		histories, parents := 0, 0
		for child := range g.Children(id) {
			c, _ := g.Node(child)
			switch {
			case c.Kind == "log" && c.Label == "history":
				histories++
			case c.Kind == "transaction":
				parents++
			default:
				t.Errorf("transaction %s points at a %s %q", id, c.Kind, c.Label)
			}
		}
		if histories != 1 {
			t.Errorf("transaction %s points at %d history logs, want exactly 1", id, histories)
		}
		if parents > 2 {
			t.Errorf("transaction %s has %d parents, want at most 2", id, parents)
		}
	}
}

// TestHistoryIsASequenceOfTransitions checks the log layer really is the state
// transition history: every child of a history log is a status log, in scheme order.
func TestHistoryIsASequenceOfTransitions(t *testing.T) {
	g := build(t, ledger.Config{Transactions: 20})
	terminal := map[string]bool{"Settled": true, "Returned": true}
	checked := 0
	for id, n := range g.All() {
		if n.Kind != "log" || n.Label != "history" {
			continue
		}
		checked++

		var flow []string
		for child := range g.Children(id) {
			c, _ := g.Node(child)
			if c.Kind != "log" {
				t.Errorf("history %s contains a %s, want only status logs", id, c.Kind)
				continue
			}
			flow = append(flow, c.Label)
		}
		if len(flow) < 3 {
			t.Errorf("history %s has %d transitions, want at least 3", id, len(flow))
			continue
		}
		if flow[0] != "Initiated" {
			t.Errorf("history %s starts at %q, want Initiated", id, flow[0])
		}
		if last := flow[len(flow)-1]; !terminal[last] {
			t.Errorf("history %s ends at %q, want a terminal status", id, last)
		}
		if got, want := fields(n)["transitions"], fmt.Sprint(len(flow)); got != want {
			t.Errorf("history %s reports %s transitions, want %s", id, got, want)
		}
	}
	if checked == 0 {
		t.Fatal("no history logs were checked")
	}
}

// TestOnlyPostingTransitionsCarryEntries checks that entries hang off the transition
// that caused them, and that purely procedural transitions post nothing.
func TestOnlyPostingTransitionsCarryEntries(t *testing.T) {
	g := build(t, ledger.Config{Transactions: 25})
	posts := map[string]bool{"Authorised": true, "Cleared": true, "Settled": true, "Returned": true}
	quiet := map[string]bool{"Initiated": true, "Submitted": true, "Mandate Checked": true}
	seenQuiet, seenPosting := false, false

	for id, n := range g.All() {
		if n.Kind != "log" || n.Label == "history" {
			continue
		}
		entries := 0
		for child := range g.Children(id) {
			c, _ := g.Node(child)
			if c.Kind != "entry" {
				t.Errorf("status log %s contains a %s, want only entries", id, c.Kind)
			}
			entries++
		}
		switch {
		case quiet[n.Label]:
			seenQuiet = true
			if entries != 0 {
				t.Errorf("%s posted %d entries, want none", n.Label, entries)
			}
		case posts[n.Label]:
			if entries > 1 {
				t.Errorf("%s posted %d entries, want at most one double entry", n.Label, entries)
			}
			if entries == 1 {
				seenPosting = true
			}
		default:
			t.Errorf("unexpected status %q", n.Label)
		}
	}
	if !seenQuiet || !seenPosting {
		t.Errorf("coverage gap: sawQuiet=%t sawPosting=%t", seenQuiet, seenPosting)
	}
}

// TestEveryEntryBalances is the accounting invariant: an entry is a complete double
// entry, so its debit legs must equal its credit legs.
func TestEveryEntryBalances(t *testing.T) {
	g := build(t, ledger.Config{Transactions: 30})
	checked := 0
	for id, n := range g.All() {
		if n.Kind != "entry" {
			continue
		}
		checked++

		var debits, credits int64
		legs := 0
		for _, f := range n.Payload {
			switch {
			case strings.HasPrefix(f.Key, "DR "):
				debits += parsePence(t, f.Value)
				legs++
			case strings.HasPrefix(f.Key, "CR "):
				credits += parsePence(t, f.Value)
				legs++
			}
		}
		if legs < 2 {
			t.Errorf("entry %s has %d legs, want both a debit and a credit", id, legs)
		}
		if debits != credits {
			t.Errorf("entry %s does not balance: debits %d, credits %d", id, debits, credits)
		}

		f := fields(n)
		if got, want := parsePence(t, f["total debits"]), debits; got != want {
			t.Errorf("entry %s reports total debits %d, want %d", id, got, want)
		}
		if got, want := parsePence(t, f["total credits"]), credits; got != want {
			t.Errorf("entry %s reports total credits %d, want %d", id, got, want)
		}
	}
	if checked == 0 {
		t.Fatal("no entries were checked")
	}
}

// TestEntriesAreShared is the property that makes this a DAG rather than a tree:
// identical double entries must resolve to one node with several parent logs.
func TestEntriesAreShared(t *testing.T) {
	g := build(t, ledger.Config{Transactions: 30})
	shared := 0
	for id := range g.Shared() {
		n, _ := g.Node(id)
		if n.Kind == "entry" {
			shared++
		}
	}
	if shared == 0 {
		t.Error("no entry was shared, want content addressing to deduplicate recurring postings")
	}
}

// TestLedgerPacks checks the generated graph survives the real layout, which is
// the case that matters: shared entries must still nest somewhere sensible.
func TestLedgerPacks(t *testing.T) {
	g := build(t, ledger.Config{Transactions: 40})
	p := layout.Pack(g, layout.Options{})
	if got, want := len(p.Nodes), g.Len(); got != want {
		t.Fatalf("placed %d nodes, want %d", got, want)
	}
	if len(p.Links) == 0 {
		t.Error("no reference links, want shared entries to produce some")
	}
}

func parsePence(t *testing.T, s string) int64 {
	t.Helper()
	if s == "" {
		t.Fatal("empty amount")
	}
	s = strings.NewReplacer("£", "", ",", "", ".", "").Replace(s)
	var p int64
	if _, err := fmt.Sscanf(s, "%d", &p); err != nil {
		t.Fatalf("parsing amount %q: %v", s, err)
	}
	return p
}
