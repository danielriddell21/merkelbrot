package cli

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

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
