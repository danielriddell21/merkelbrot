/*
Package ledger generates a deterministic UK payments ledger shaped like a git
object graph.

The three layers map onto git's three object types, which is what lets one
visualiser drill through either without special cases:

  - a transaction is the commit: the money movement itself, from payer to payee,
    chained to the transaction before it and occasionally to two, where a
    settlement nets several payments together.
  - a log is the tree: the history of the transaction's state transitions. One log
    per status the payment reached as it moved through its scheme, gathered under
    a history log for the transaction as a whole.
  - an entry is the blob: the double entry posted at one of those transitions,
    balanced across its debit and credit legs.

Postings therefore hang off the transition that caused them rather than off the
transaction. Authorisation recognises the liability or the debt, clearing or
settlement moves the money at the bank, and a return reverses that movement.
Transitions where nothing is posted, such as submission to the scheme, carry no
entry at all.

Everything is content-addressed, so entries deduplicate exactly as blobs do in
git. A recurring charge posting the same legs at the same amounts resolves to one
entry node shared by every transaction that posted it, which is what turns the
ledger from a tree into a DAG and gives a containment layout something real to
handle.

Amounts are held in pence and posted gross, with VAT at the standard rate split
out to its own nominal account so every entry balances.

This package is example code. It is here to demonstrate the
[github.com/danielriddell21/merkelbrot/graph.Source] interface and carries no
compatibility promise.
*/
package ledger

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"iter"
	"math/rand/v2"
	"slices"
	"strings"

	"github.com/danielriddell21/merkelbrot/graph"
)

// vatBasisPoints is the standard rate of VAT, in hundredths of a percent.
const vatBasisPoints = 2000

// Config describes the ledger to generate. The zero value is usable.
type Config struct {
	// Seed makes generation reproducible. Default 1.
	Seed uint64
	// Transactions is how many transactions to post. Default 12.
	Transactions int
	// Settlements is how many of those net several earlier payments together,
	// giving the chain merge points. Default is one per eight transactions.
	Settlements int
}

func (c Config) withDefaults() Config {
	if c.Seed == 0 {
		c.Seed = 1
	}
	if c.Transactions <= 0 {
		c.Transactions = 12
	}
	if c.Settlements <= 0 {
		c.Settlements = c.Transactions / 8
	}
	return c
}

type nominal struct {
	code string
	name string
}

func (n nominal) String() string { return n.code + " " + n.name }

type counterparty struct {
	name     string
	sortCode string
	account  string
	nominal  nominal
	sale     bool
}

var (
	bankCurrent   = nominal{"1200", "Bank Current Account"}
	tradeDebtors  = nominal{"1100", "Trade Debtors"}
	tradeCreditor = nominal{"2100", "Trade Creditors"}
	vatOnSales    = nominal{"2200", "VAT on Sales"}
	vatOnPurchase = nominal{"2201", "VAT on Purchases"}
	sales         = nominal{"4000", "Sales"}

	counterparties = []counterparty{
		{"Acme Ltd", "04-00-04", "41123456", nominal{"5000", "Cost of Sales"}, false},
		{"Notable Coffee Roasters", "20-32-53", "80014429", nominal{"7406", "Subsistence"}, false},
		{"Thames Valley Utilities", "60-16-13", "31200987", nominal{"7200", "Electricity"}, false},
		{"Camden Print Co", "40-47-84", "62330145", nominal{"7500", "Printing & Stationery"}, false},
		{"Whitfield & Sons", "23-14-70", "10457781", nominal{"7502", "Telephone & Broadband"}, false},
		{"Peckham Provisions Ltd", "09-01-29", "55620034", sales, true},
		{"Southbank Studios", "30-96-26", "70118823", sales, true},
	}

	schemes = []string{"Faster Payments", "BACS", "CHAPS", "Direct Debit"}

	// amounts is deliberately a short list: recurring charges at identical values
	// are what make ledger entries deduplicate into shared nodes.
	amounts = []int64{85_00, 120_00, 249_99, 480_00, 1_250_00, 8_50, 32_40, 99_00}
)

// Statuses a payment passes through. Recognition posts at Authorised, and the
// bank movement posts at whichever of Cleared or Settled the scheme reaches first.
const (
	statusInitiated = "Initiated"
	statusMandate   = "Mandate Checked"
	statusSubmitted = "Submitted"
	statusAuthorise = "Authorised"
	statusCleared   = "Cleared"
	statusSettled   = "Settled"
	statusReturned  = "Returned"
)

func statusesFor(scheme string, returned bool) []string {
	var flow []string
	switch scheme {
	case "CHAPS":
		flow = []string{statusInitiated, statusAuthorise, statusSettled}
	case "BACS":
		flow = []string{statusInitiated, statusSubmitted, statusAuthorise, statusCleared, statusSettled}
	case "Direct Debit":
		flow = []string{statusInitiated, statusMandate, statusSubmitted, statusAuthorise, statusCleared, statusSettled}
	default:
		flow = []string{statusInitiated, statusSubmitted, statusAuthorise, statusSettled}
	}
	if returned {
		flow[len(flow)-1] = statusReturned
	}
	return flow
}

// leg is one side of a double entry, in pence.
type leg struct {
	side    string
	account nominal
	amount  int64
}

// entry is a complete, balanced double entry: every debit and every credit posted
// at a single state transition.
type entry struct {
	narrative string
	legs      []leg
}

func (e entry) totals() (debits, credits int64) {
	for _, l := range e.legs {
		if l.side == "DR" {
			debits += l.amount
		} else {
			credits += l.amount
		}
	}
	return debits, credits
}

// recognition raises the liability or the debt when a payment is authorised.
func recognition(cp counterparty, gross int64) entry {
	net := gross * 10000 / (10000 + vatBasisPoints)
	vat := gross - net
	if cp.sale {
		return entry{"Sales invoice raised", []leg{
			{"DR", tradeDebtors, gross},
			{"CR", sales, net},
			{"CR", vatOnSales, vat},
		}}
	}
	return entry{"Purchase recognised", []leg{
		{"DR", cp.nominal, net},
		{"DR", vatOnPurchase, vat},
		{"CR", tradeCreditor, gross},
	}}
}

// settlement moves the money at the bank and clears what recognition raised.
func settlement(cp counterparty, gross int64) entry {
	if cp.sale {
		return entry{"Customer receipt", []leg{
			{"DR", bankCurrent, gross},
			{"CR", tradeDebtors, gross},
		}}
	}
	return entry{"Supplier payment", []leg{
		{"DR", tradeCreditor, gross},
		{"CR", bankCurrent, gross},
	}}
}

func reverse(e entry) entry {
	out := entry{narrative: e.narrative + " reversed", legs: make([]leg, len(e.legs))}
	for i, l := range e.legs {
		if l.side == "DR" {
			l.side = "CR"
		} else {
			l.side = "DR"
		}
		out.legs[i] = l
	}
	return out
}

// Source is a generated payments ledger.
type Source struct {
	nodes map[string]graph.Node[string]
	head  string
}

// New generates a ledger from the configuration.
func New(cfg Config) *Source {
	cfg = cfg.withDefaults()
	rng := rand.New(rand.NewPCG(cfg.Seed, 0x6c65646765_7200))
	s := &Source{nodes: make(map[string]graph.Node[string])}

	var prev string
	var unsettled []string
	settleEvery := 0
	if cfg.Settlements > 0 {
		settleEvery = max(2, cfg.Transactions/(cfg.Settlements+1))
	}

	for i := range cfg.Transactions {
		cp := counterparties[rng.IntN(len(counterparties))]
		scheme := schemes[rng.IntN(len(schemes))]
		gross := amounts[rng.IntN(len(amounts))]
		returned := rng.IntN(11) == 0
		day := 1 + i%28

		history := s.history(cp, gross, scheme, returned, day, rng)

		parents := []string{}
		if prev != "" {
			parents = append(parents, prev)
		}
		// A settlement nets earlier payments together, so it carries several
		// parents in the same way a merge commit does.
		if settleEvery > 0 && i > 0 && i%settleEvery == 0 && len(unsettled) > 1 {
			parents = append(parents, unsettled[0])
			unsettled = nil
		}

		direction := "Payment out"
		if cp.sale {
			direction = "Receipt in"
		}
		txn := s.put("transaction", fmt.Sprintf("%s %s", formatPence(gross), cp.name),
			append(parents, history), []graph.Field{
				{Key: "scheme", Value: scheme},
				{Key: "direction", Value: direction},
				{Key: "counterparty", Value: cp.name},
				{Key: "sort code", Value: cp.sortCode},
				{Key: "account", Value: cp.account},
				{Key: "amount", Value: formatPence(gross)},
				{Key: "reference", Value: reference(cp.name, i)},
				{Key: "value date", Value: fmt.Sprintf("2026-03-%02d", day)},
			})
		unsettled = append(unsettled, txn)
		prev = txn
	}
	s.head = prev
	return s
}

func (s *Source) history(cp counterparty, gross int64, scheme string, returned bool, day int, rng *rand.Rand) string {
	flow := statusesFor(scheme, returned)

	// The bank movement belongs to the first clearing status the scheme reaches;
	// anything later only confirms it, so nothing further is posted there.
	banksAt := statusSettled
	if slices.Contains(flow, statusCleared) {
		banksAt = statusCleared
	}

	logs := make([]string, 0, len(flow))
	minute := 14 + rng.IntN(20)
	for _, status := range flow {
		var posted []string
		switch status {
		case statusAuthorise:
			posted = []string{s.entry(recognition(cp, gross))}
		case statusReturned:
			posted = []string{s.entry(reverse(settlement(cp, gross)))}
		case banksAt:
			posted = []string{s.entry(settlement(cp, gross))}
		}
		logs = append(logs, s.put("log", status, posted, []graph.Field{
			{Key: "status", Value: status},
			{Key: "scheme", Value: scheme},
			{Key: "at", Value: fmt.Sprintf("2026-03-%02dT09:%02d:%02dZ", day, minute, rng.IntN(60))},
			{Key: "posted", Value: fmt.Sprint(len(posted) > 0)},
		}))
		minute += 1 + rng.IntN(4)
	}
	return s.put("log", "history", logs, []graph.Field{
		{Key: "scheme", Value: scheme},
		{Key: "transitions", Value: fmt.Sprint(len(flow))},
		{Key: "outcome", Value: flow[len(flow)-1]},
	})
}

func (s *Source) entry(e entry) string {
	debits, credits := e.totals()
	payload := make([]graph.Field, 0, len(e.legs)+3)
	payload = append(payload, graph.Field{Key: "narrative", Value: e.narrative})
	for _, l := range e.legs {
		payload = append(payload, graph.Field{
			Key:   l.side + " " + l.account.String(),
			Value: formatPence(l.amount),
		})
	}
	payload = append(payload,
		graph.Field{Key: "total debits", Value: formatPence(debits)},
		graph.Field{Key: "total credits", Value: formatPence(credits)},
	)

	var accounts []string
	for _, l := range e.legs {
		accounts = append(accounts, l.side+" "+l.account.code)
	}
	label := fmt.Sprintf("%s %s (%s)", e.narrative, formatPence(debits), strings.Join(accounts, ", "))
	id := s.put("entry", label, nil, payload)

	// An entry is drawn in proportion to the money it moves, on a scale where a
	// pound is one step, so a large payment reads as larger without a small one
	// vanishing beside it.
	n := s.nodes[id]
	n.Weight = graph.Weigh(float64(debits), 100)
	s.nodes[id] = n
	return id
}

func (s *Source) put(kind, label string, children []string, payload []graph.Field) string {
	// Content addressing can make two children resolve to the same node, and a
	// repeated child would otherwise show up as a spurious extra parent.
	children = slices.Compact(slices.Clone(children))

	h := sha256.New()
	fmt.Fprintf(h, "%s\x00%s\x00", kind, label)
	for _, c := range children {
		fmt.Fprintf(h, "%s\x00", c)
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

// Roots yields the most recent transaction, which reaches the whole chain.
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

func reference(name string, i int) string {
	var initials strings.Builder
	for _, word := range strings.Fields(name) {
		if r := []rune(word)[0]; r >= 'A' && r <= 'Z' {
			initials.WriteRune(r)
		}
	}
	return fmt.Sprintf("%s/%05d", initials.String(), 40000+i)
}

// formatPence renders an amount in pence as pounds sterling with thousands
// separators, for example 1250000 as "£12,500.00".
func formatPence(p int64) string {
	sign := ""
	if p < 0 {
		sign, p = "-", -p
	}
	pounds := fmt.Sprint(p / 100)
	var grouped strings.Builder
	for i, r := range pounds {
		if i > 0 && (len(pounds)-i)%3 == 0 {
			grouped.WriteByte(',')
		}
		grouped.WriteRune(r)
	}
	return fmt.Sprintf("%s£%s.%02d", sign, grouped.String(), p%100)
}
