package gitrepo_test

import (
	"bytes"
	"compress/zlib"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/danielriddell21/merkelbrot/examples/gitrepo"
	"github.com/danielriddell21/merkelbrot/graph"
	"github.com/danielriddell21/merkelbrot/layout"
)

// repoBuilder writes loose git objects by hand, so these tests exercise the
// object parser without depending on a git binary being installed.
type repoBuilder struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) *repoBuilder {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "repo", ".git")
	if err := os.MkdirAll(filepath.Join(dir, "objects"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "refs", "heads"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &repoBuilder{t: t, dir: dir}
}

func (r *repoBuilder) write(typ string, body []byte) string {
	r.t.Helper()
	raw := append([]byte(fmt.Sprintf("%s %d\x00", typ, len(body))), body...)
	sum := sha1.Sum(raw)
	sha := hex.EncodeToString(sum[:])

	dir := filepath.Join(r.dir, "objects", sha[:2])
	if err := os.MkdirAll(dir, 0o755); err != nil {
		r.t.Fatal(err)
	}
	var buf bytes.Buffer
	zw := zlib.NewWriter(&buf)
	if _, err := zw.Write(raw); err != nil {
		r.t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, sha[2:]), buf.Bytes(), 0o644); err != nil {
		r.t.Fatal(err)
	}
	return sha
}

func (r *repoBuilder) blob(content string) string {
	return r.write("blob", []byte(content))
}

type entry struct {
	mode, name, sha string
}

func (r *repoBuilder) tree(entries ...entry) string {
	r.t.Helper()
	var body []byte
	// Git stores tree entries sorted by name.
	slices.SortFunc(entries, func(a, b entry) int {
		switch {
		case a.name < b.name:
			return -1
		case a.name > b.name:
			return 1
		}
		return 0
	})
	for _, e := range entries {
		raw, err := hex.DecodeString(e.sha)
		if err != nil {
			r.t.Fatal(err)
		}
		body = append(body, fmt.Sprintf("%s %s\x00", e.mode, e.name)...)
		body = append(body, raw...)
	}
	return r.write("tree", body)
}

func (r *repoBuilder) commit(tree, message string, parents ...string) string {
	var b bytes.Buffer
	fmt.Fprintf(&b, "tree %s\n", tree)
	for _, p := range parents {
		fmt.Fprintf(&b, "parent %s\n", p)
	}
	const who = "Ada Lovelace <ada@example.co.uk> 1772000000 +0000"
	fmt.Fprintf(&b, "author %s\ncommitter %s\n\n%s\n", who, who, message)
	return r.write("commit", b.Bytes())
}

func (r *repoBuilder) setHead(sha string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, "refs", "heads", "main"), []byte(sha+"\n"), 0o644); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.dir, "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// sample builds a two-commit repository where one blob is shared by both trees.
func sample(t *testing.T) (dir, head, sharedBlob string) {
	t.Helper()
	r := newRepo(t)
	hello := r.blob("hello\n")
	world := r.blob("world\n")

	t1 := r.tree(entry{"100644", "hello.txt", hello})
	c1 := r.commit(t1, "initial commit")

	t2 := r.tree(entry{"100644", "hello.txt", hello}, entry{"100644", "world.txt", world})
	c2 := r.commit(t2, "add world", c1)

	r.setHead(c2)
	return filepath.Dir(r.dir), c2, hello
}

func TestOpenReadsLooseObjects(t *testing.T) {
	dir, head, shared := sample(t)

	src, err := gitrepo.Open(dir, gitrepo.Config{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	g, err := graph.New(src)
	if err != nil {
		t.Fatalf("graph.New: %v", err)
	}

	if got := slices.Collect(g.Roots()); len(got) != 1 || got[0] != head {
		t.Errorf("Roots() = %v, want [%s]", got, head)
	}
	// Two commits, two trees and two blobs.
	if got, want := g.Len(), 6; got != want {
		t.Errorf("Len() = %d, want %d", got, want)
	}

	counts := map[string]int{}
	for _, n := range g.All() {
		counts[n.Kind]++
	}
	for kind, want := range map[string]int{"commit": 2, "tree": 2, "blob": 2} {
		if counts[kind] != want {
			t.Errorf("%d %s objects, want %d", counts[kind], kind, want)
		}
	}

	// The shared blob is the whole point: both trees point at the same object.
	if got := slices.Collect(g.Shared()); !slices.Contains(got, shared) {
		t.Errorf("Shared() = %v, want it to contain the reused blob %s", got, shared)
	}
	if g.IsTree() {
		t.Error("IsTree() = true, want a DAG")
	}
}

func TestCommitMetadataIsParsed(t *testing.T) {
	dir, head, _ := sample(t)
	src, err := gitrepo.Open(dir, gitrepo.Config{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	n, ok := src.Node(head)
	if !ok {
		t.Fatalf("head %s is missing", head)
	}
	if got, want := n.Label, "add world"; got != want {
		t.Errorf("Label = %q, want %q", got, want)
	}
	if got, want := n.Kind, "commit"; got != want {
		t.Errorf("Kind = %q, want %q", got, want)
	}
	if len(n.Hash) != 20 {
		t.Errorf("Hash is %d bytes, want 20", len(n.Hash))
	}

	fields := map[string]string{}
	for _, f := range n.Payload {
		fields[f.Key] = f.Value
	}
	if got, want := fields["author"], "Ada Lovelace <ada@example.co.uk>"; got != want {
		t.Errorf("author = %q, want %q", got, want)
	}
	if got, want := fields["date"], "2026-02-25 06:13"; got != want {
		t.Errorf("date = %q, want %q", got, want)
	}
	if got, want := fields["parents"], "1"; got != want {
		t.Errorf("parents = %q, want %q", got, want)
	}
}

func TestBlobsAreLabelledWithTheirPath(t *testing.T) {
	dir, _, shared := sample(t)
	src, err := gitrepo.Open(dir, gitrepo.Config{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	n, ok := src.Node(shared)
	if !ok {
		t.Fatal("shared blob is missing")
	}
	if got, want := n.Label, "hello.txt"; got != want {
		t.Errorf("Label = %q, want %q", got, want)
	}
	if len(n.Children) != 0 {
		t.Errorf("blob has %d children, want none", len(n.Children))
	}
}

func TestMaxCommitsTruncatesHistory(t *testing.T) {
	dir, _, _ := sample(t)
	src, err := gitrepo.Open(dir, gitrepo.Config{MaxCommits: 1})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	g, err := graph.New(src)
	if err != nil {
		t.Fatalf("graph.New: %v", err)
	}
	commits := 0
	for _, n := range g.All() {
		if n.Kind == "commit" {
			commits++
		}
	}
	if got, want := commits, 1; got != want {
		t.Errorf("%d commits, want %d", got, want)
	}
}

func TestOpenFindsTheRepositoryFromAWorkingTree(t *testing.T) {
	dir, _, _ := sample(t)
	nested := filepath.Join(dir, "a", "b")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := gitrepo.Open(nested, gitrepo.Config{}); err != nil {
		t.Errorf("Open from a subdirectory: %v", err)
	}
	if _, err := gitrepo.Open(filepath.Join(dir, ".git"), gitrepo.Config{}); err != nil {
		t.Errorf("Open on the git directory itself: %v", err)
	}
}

func TestOpenRejectsNonRepositories(t *testing.T) {
	if _, err := gitrepo.Open(t.TempDir(), gitrepo.Config{}); !errors.Is(err, gitrepo.ErrNotARepository) {
		t.Errorf("Open() error = %v, want %v", err, gitrepo.ErrNotARepository)
	}
}

func TestPacksAndLaysOut(t *testing.T) {
	dir, _, _ := sample(t)
	src, err := gitrepo.Open(dir, gitrepo.Config{})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	g, err := graph.New(src)
	if err != nil {
		t.Fatalf("graph.New: %v", err)
	}
	p := layout.Pack(g, layout.Options{})
	if got, want := len(p.Nodes), g.Len(); got != want {
		t.Fatalf("placed %d nodes, want %d", got, want)
	}
	if len(p.Links) == 0 {
		t.Error("no reference links, want the shared blob to produce one")
	}
}

// TestReadsThisRepository exercises the pack reader, including delta resolution,
// against real data. It is skipped where the checkout is unavailable.
func TestReadsThisRepository(t *testing.T) {
	if _, err := os.Stat("../../.git"); err != nil {
		t.Skip("not running inside a git checkout")
	}
	src, err := gitrepo.Open("../..", gitrepo.Config{MaxCommits: 5})
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	g, err := graph.New(src)
	if err != nil {
		t.Fatalf("graph.New: %v", err)
	}
	if g.Len() < 5 {
		t.Errorf("read %d objects, want a real graph", g.Len())
	}
	for id, n := range g.All() {
		switch n.Kind {
		case "commit", "tree", "blob":
		default:
			t.Errorf("object %s has kind %q", id, n.Kind)
		}
	}
}

// TestShallowCloneStopsAtTheOldestPresentCommit covers the checkout CI actually
// performs: a commit that names a parent the repository does not carry.
func TestShallowCloneStopsAtTheOldestPresentCommit(t *testing.T) {
	r := newRepo(t)
	blob := r.blob("hello\n")
	tree := r.tree(entry{"100644", "hello.txt", blob})
	// The parent is a plausible object name that was never written.
	head := r.commit(tree, "only commit", "0123456789abcdef0123456789abcdef01234567")
	r.setHead(head)

	src, err := gitrepo.Open(filepath.Dir(r.dir), gitrepo.Config{})
	if err != nil {
		t.Fatalf("Open on a shallow clone: %v", err)
	}
	g, err := graph.New(src)
	if err != nil {
		t.Fatalf("graph.New: %v", err)
	}
	if got, want := g.Len(), 3; got != want {
		t.Errorf("Len() = %d, want %d", got, want)
	}
	for child := range g.Children(head) {
		n, _ := g.Node(child)
		if n.Kind == "commit" {
			t.Errorf("head points at commit %s, want the missing parent to be dropped", child)
		}
	}
}

// TestPartialCloneSkipsFilteredEntries covers a tree naming a blob that a
// blobless clone never fetched.
func TestPartialCloneSkipsFilteredEntries(t *testing.T) {
	r := newRepo(t)
	present := r.blob("here\n")
	tree := r.tree(
		entry{"100644", "here.txt", present},
		entry{"100644", "gone.txt", "89abcdef0123456789abcdef0123456789abcdef"},
	)
	head := r.commit(tree, "partial")
	r.setHead(head)

	src, err := gitrepo.Open(filepath.Dir(r.dir), gitrepo.Config{})
	if err != nil {
		t.Fatalf("Open on a partial clone: %v", err)
	}
	g, err := graph.New(src)
	if err != nil {
		t.Fatalf("graph.New: %v", err)
	}
	if got, want := g.Len(), 3; got != want {
		t.Errorf("Len() = %d, want %d", got, want)
	}
	n, _ := g.Node(tree)
	if got, want := len(n.Children), 1; got != want {
		t.Errorf("tree has %d entries, want %d", got, want)
	}
}
