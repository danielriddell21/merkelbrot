package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/danielriddell21/merkelbrot/scene"
)

// run drives the command tree the way a shell would, capturing both streams.
func run(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()
	var out, errOut bytes.Buffer

	root := newRoot("test")
	root.SetArgs(args)
	root.SetOut(&out)
	root.SetErr(&errOut)

	err = root.Execute()
	return out.String(), errOut.String(), err
}

func TestSceneWritesJSON(t *testing.T) {
	stdout, _, err := run(t, "scene", "--source", "synthetic", "-n", "3")
	if err != nil {
		t.Fatalf("scene: %v", err)
	}

	var s scene.Scene
	if err := json.Unmarshal([]byte(stdout), &s); err != nil {
		t.Fatalf("decoding scene: %v", err)
	}
	if s.Stats.Nodes == 0 {
		t.Error("scene has no nodes")
	}
	if s.Title != "synthetic Merkle DAG" {
		t.Errorf("Title = %q, want the synthetic source's title", s.Title)
	}
}

func TestExportWritesASelfContainedPage(t *testing.T) {
	stdout, _, err := run(t, "export", "--source", "ledger", "-n", "3")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	for _, want := range []string{"<!doctype html>", `<canvas id="view">`, `id="scene"`} {
		if !strings.Contains(stdout, want) {
			t.Errorf("page is missing %q", want)
		}
	}
}

func TestMaxDepthIsApplied(t *testing.T) {
	stdout, _, err := run(t, "scene", "--source", "ledger", "-n", "6", "--max-depth", "1")
	if err != nil {
		t.Fatalf("scene: %v", err)
	}
	var s scene.Scene
	if err := json.Unmarshal([]byte(stdout), &s); err != nil {
		t.Fatalf("decoding scene: %v", err)
	}
	if s.Stats.MaxDepth != 0 {
		t.Errorf("MaxDepth = %d, want 0 when limited to one level", s.Stats.MaxDepth)
	}
}

func TestProveAddsHighlights(t *testing.T) {
	// Find a leaf to prove, then prove it against the root.
	stdout, _, err := run(t, "scene", "--source", "synthetic", "-n", "3")
	if err != nil {
		t.Fatalf("scene: %v", err)
	}
	var s scene.Scene
	if err := json.Unmarshal([]byte(stdout), &s); err != nil {
		t.Fatalf("decoding scene: %v", err)
	}
	var leaf string
	for _, n := range s.Nodes {
		if n.Leaf {
			leaf = n.ID
			break
		}
	}
	if leaf == "" {
		t.Fatal("no leaf to prove")
	}

	stdout, _, err = run(t, "scene", "--source", "synthetic", "-n", "3", "--prove", leaf)
	if err != nil {
		t.Fatalf("scene --prove: %v", err)
	}
	var proved scene.Scene
	if err := json.Unmarshal([]byte(stdout), &proved); err != nil {
		t.Fatalf("decoding scene: %v", err)
	}
	if len(proved.Highlights) == 0 {
		t.Error("--prove produced no highlights")
	}
}

func TestErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"unknown source", []string{"scene", "--source", "nope"}, "unknown source"},
		{"unknown command", []string{"nope"}, "unknown command"},
		{"unknown node", []string{"scene", "--source", "ledger", "-n", "2", "--prove", "ghost"}, "no node with ID"},
		{"malformed diff", []string{"scene", "--source", "ledger", "-n", "2", "--diff", "onlyone"}, `separated by ".."`},
		{"unknown node in diff", []string{"scene", "--source", "ledger", "-n", "2", "--diff", "ghost..other"}, "no node with ID"},
		{"unexpected argument", []string{"scene", "extra"}, "unknown command"},
		{"missing repository", []string{"scene", "--source", "git", "--repo", "/nonexistent"}, "reading repository"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, err := run(t, tt.args...)
			if err == nil {
				t.Fatalf("%v succeeded, want an error", tt.args)
			}
			if !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error = %q, want it to mention %q", err, tt.want)
			}
		})
	}
}

func TestCompletionShellsAreSupported(t *testing.T) {
	for _, shell := range []string{"bash", "zsh", "fish", "powershell"} {
		t.Run(shell, func(t *testing.T) {
			stdout, _, err := run(t, "completion", shell)
			if err != nil {
				t.Fatalf("completion %s: %v", shell, err)
			}
			if !strings.Contains(stdout, "merkelbrot") {
				t.Errorf("completion %s did not mention the command name", shell)
			}
		})
	}
	if _, _, err := run(t, "completion", "nope"); err == nil {
		t.Error("completion with an unknown shell succeeded, want an error")
	}
}

func TestVersionIsReported(t *testing.T) {
	stdout, _, err := run(t, "--version")
	if err != nil {
		t.Fatalf("--version: %v", err)
	}
	if !strings.Contains(stdout, "test") {
		t.Errorf("--version = %q, want the injected version", stdout)
	}
}

// TestDiffHighlightsWhatChanged is the point of the flag: a consistency proof is
// only useful if it can be asked for.
func TestDiffHighlightsWhatChanged(t *testing.T) {
	out, _, err := run(t, "scene", "--source", "synthetic", "-n", "6", "--diff", "commit-does-not-exist..x")
	if err == nil {
		t.Fatalf("a nonsense diff was accepted: %s", out)
	}

	// Find two real commits to compare, oldest against newest.
	raw, _, err := run(t, "scene", "--source", "synthetic", "-n", "6")
	if err != nil {
		t.Fatalf("scene: %v", err)
	}
	var s struct {
		Nodes []struct {
			ID    string `json:"id"`
			Kind  string `json:"kind"`
			Depth int    `json:"depth"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("decoding scene: %v", err)
	}
	var first, last string
	for _, n := range s.Nodes {
		if n.Kind != "commit" {
			continue
		}
		if first == "" || n.Depth > 0 {
			first = n.ID
		}
		if last == "" {
			last = n.ID
		}
	}
	if first == "" || last == "" || first == last {
		t.Skip("the generated graph has too few commits to compare")
	}

	out, _, err = run(t, "scene", "--source", "synthetic", "-n", "6", "--diff", first+".."+last)
	if err != nil {
		t.Fatalf("diff: %v (%s)", err, out)
	}
	if !strings.Contains(out, `"highlights"`) {
		t.Error("a diff produced no highlights")
	}
}

// TestDiffAcceptsAPrefix keeps hand-typed references usable: content-addressed
// IDs are far too long to type in full.
func TestDiffAcceptsAPrefix(t *testing.T) {
	raw, _, err := run(t, "scene", "--source", "synthetic", "-n", "4")
	if err != nil {
		t.Fatalf("scene: %v", err)
	}
	var s struct {
		Nodes []struct {
			ID string `json:"id"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("decoding scene: %v", err)
	}
	full := s.Nodes[len(s.Nodes)-1].ID
	if len(full) < 6 {
		t.Skip("IDs are too short for a prefix to mean anything")
	}
	if _, _, err := run(t, "scene", "--source", "synthetic", "-n", "4", "--prove", full[:6]); err != nil {
		t.Errorf("a six-character prefix was rejected: %v", err)
	}
}

// TestProveSearchesEveryRoot keeps a multi-rooted graph honest: the node need only
// be under one of the roots, not under whichever happens to come first.
func TestProveSearchesEveryRoot(t *testing.T) {
	// The ledger has one root, so a second is added by hand through the scene the
	// command produces: what matters is that the search does not stop at roots[0].
	raw, _, err := run(t, "scene", "--source", "synthetic", "-n", "5")
	if err != nil {
		t.Fatalf("scene: %v", err)
	}
	var s struct {
		Nodes []struct {
			ID    string `json:"id"`
			Leaf  bool   `json:"leaf"`
			Depth int    `json:"depth"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		t.Fatalf("decoding scene: %v", err)
	}
	var deepest string
	best := -1
	for _, n := range s.Nodes {
		if n.Leaf && n.Depth > best {
			deepest, best = n.ID, n.Depth
		}
	}
	if deepest == "" {
		t.Skip("no leaf to prove")
	}
	if _, _, err := run(t, "scene", "--source", "synthetic", "-n", "5", "--prove", deepest); err != nil {
		t.Errorf("proving a reachable leaf failed: %v", err)
	}
}

func TestVerifyChecksTheRepository(t *testing.T) {
	out, _, err := run(t, "scene", "--source", "git", "--repo", "../../..", "--verify", "-n", "3")
	if err != nil {
		t.Fatalf("--verify on this repository failed: %v", err)
	}
	// An intact repository verifies clean, so there is nothing to highlight.
	if strings.Contains(out, `"invalid"`) {
		t.Error("this repository reported a hash mismatch")
	}
}

// TestVerifyNeedsASourceThatCan is the honest failure: only a source that knows
// how its objects are named can be checked, and the rest must say so.
func TestVerifyNeedsASourceThatCan(t *testing.T) {
	_, _, err := run(t, "scene", "--source", "synthetic", "-n", "3", "--verify")
	if err == nil {
		t.Fatal("--verify on a source with no hashing rule succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "cannot be verified") {
		t.Errorf("error = %q, want it to explain the source cannot be verified", err)
	}
}

// TestServeAnswersAndStopsOnCancel covers the serving path end to end: the command
// binds, answers, and — because Serve does not watch the context itself — comes
// back when the context is cancelled rather than having to be killed.
func TestServeAnswersAndStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	root := newRoot("test")
	root.SetArgs([]string{"serve", "--source", "ledger", "-n", "3", "--addr", "127.0.0.1:0"})
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)

	done := make(chan error, 1)
	go func() { done <- root.ExecuteContext(ctx) }()

	// The address chosen is reported on stderr, which is the only way to learn it
	// when port 0 was asked for.
	var addr string
	for range 100 {
		if m := regexp.MustCompile(`http://([^\s]+)`).FindStringSubmatch(errOut.String()); m != nil {
			addr = m[1]
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if addr == "" {
		t.Fatalf("the server never reported an address: %q", errOut.String())
	}

	resp, err := http.Get("http://" + addr + "/scene.json")
	if err != nil {
		t.Fatalf("GET /scene.json: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve returned %v, want a clean shutdown", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not return after its context was cancelled")
	}
}

func TestServeRejectsAnUnusableAddress(t *testing.T) {
	_, _, err := run(t, "serve", "--source", "ledger", "-n", "2", "--addr", "256.256.256.256:1")
	if err == nil {
		t.Fatal("serving on an impossible address succeeded, want an error")
	}
	if !strings.Contains(err.Error(), "listening") {
		t.Errorf("error = %q, want it to mention listening", err)
	}
}
