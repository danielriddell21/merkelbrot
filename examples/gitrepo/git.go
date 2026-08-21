/*
Package gitrepo reads a git repository's object graph directly from disk.

It is the counterpart to the generated example sources: where those prove the
interface against data invented for the purpose, this one proves it against the
Merkle DAG most people already have on their machine. Commits chain to their
parents and fan out into merges, trees nest inside trees, and blobs are shared by
every tree whose content matches — the deduplication a containment layout has to
resolve is real rather than contrived.

Objects are read with the standard library alone. Loose objects are zlib streams
under .git/objects, and packed objects are resolved through the version 2 pack
index, including both offset and reference deltas.

# Naming

A git blob has no name of its own; names live in the trees that point at it. A
node's label is therefore the first path the walk encountered for it, which is a
presentational choice rather than a property of the object.

This package is example code. It is here to demonstrate the
[github.com/danielriddell21/merkelbrot/graph.Source] interface and carries no
compatibility promise. It reads pack files into memory, so it suits repositories
you would be willing to open in an editor rather than the largest monorepos.
*/
package gitrepo

import (
	"bytes"
	"compress/zlib"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/danielriddell21/merkelbrot/graph"
)

// ErrNotARepository reports that no git directory was found.
var ErrNotARepository = errors.New("gitrepo: not a git repository")

// Config controls how much of a repository is read. The zero value is usable.
type Config struct {
	// MaxCommits limits how far back the history walk goes, with zero meaning no
	// limit. Default 40.
	MaxCommits int
	// Ref is the reference to start from. Default "HEAD".
	Ref string
}

func (c Config) withDefaults() Config {
	if c.MaxCommits == 0 {
		c.MaxCommits = 40
	}
	if c.MaxCommits < 0 {
		c.MaxCommits = 0
	}
	if c.Ref == "" {
		c.Ref = "HEAD"
	}
	return c
}

// Source is a git repository presented as a Merkle DAG.
type Source struct {
	dir   string
	packs []*pack
	head  string
	nodes map[string]graph.Node[string]
}

// Open reads the repository containing path and materialises its object graph.
//
// The path may be a working tree or a .git directory; parent directories are
// searched until one is found. Open returns [ErrNotARepository] if none is.
func Open(path string, cfg Config) (*Source, error) {
	cfg = cfg.withDefaults()
	dir, err := findGitDir(path)
	if err != nil {
		return nil, err
	}

	s := &Source{dir: dir, nodes: make(map[string]graph.Node[string])}
	if err := s.loadPacks(); err != nil {
		return nil, err
	}

	head, err := s.resolve(cfg.Ref)
	if err != nil {
		return nil, err
	}
	s.head = head

	if err := s.walk(head, cfg.MaxCommits); err != nil {
		return nil, err
	}
	return s, nil
}

// Roots yields the commit the requested reference pointed at.
func (s *Source) Roots() iter.Seq[string] {
	return func(yield func(string) bool) { yield(s.head) }
}

// Node returns the object with the given ID.
func (s *Source) Node(id string) (graph.Node[string], bool) {
	n, ok := s.nodes[id]
	return n, ok
}

// Len reports how many objects were read.
func (s *Source) Len() int { return len(s.nodes) }

func findGitDir(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	for dir := abs; ; dir = filepath.Dir(dir) {
		candidate := filepath.Join(dir, ".git")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate, nil
		}
		// The path may already be a git directory.
		if info, err := os.Stat(filepath.Join(dir, "objects")); err == nil && info.IsDir() {
			return dir, nil
		}
		if parent := filepath.Dir(dir); parent == dir {
			return "", fmt.Errorf("%w: %s", ErrNotARepository, path)
		}
	}
}

func (s *Source) resolve(ref string) (string, error) {
	seen := 0
	for {
		if seen++; seen > 10 {
			return "", fmt.Errorf("gitrepo: reference %q is too deeply symbolic", ref)
		}
		if len(ref) == 40 && isHex(ref) {
			return ref, nil
		}

		data, err := os.ReadFile(filepath.Join(s.dir, filepath.FromSlash(ref)))
		if err == nil {
			line := strings.TrimSpace(string(data))
			if target, ok := strings.CutPrefix(line, "ref: "); ok {
				ref = target
				continue
			}
			ref = line
			continue
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}

		sha, err := s.packedRef(ref)
		if err != nil {
			return "", err
		}
		return sha, nil
	}
}

func (s *Source) packedRef(ref string) (string, error) {
	data, err := os.ReadFile(filepath.Join(s.dir, "packed-refs"))
	if err != nil {
		return "", fmt.Errorf("gitrepo: cannot resolve %q: %w", ref, err)
	}
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "^") {
			continue
		}
		sha, name, ok := strings.Cut(line, " ")
		if ok && name == ref {
			return sha, nil
		}
	}
	return "", fmt.Errorf("gitrepo: cannot resolve %q", ref)
}

func isHex(s string) bool {
	_, err := hex.DecodeString(s)
	return err == nil
}

// walk materialises commits from head backwards, along with the trees and blobs
// each of them reaches.
func (s *Source) walk(head string, maxCommits int) error {
	queue := []string{head}
	seen := map[string]bool{head: true}
	commits := 0

	for len(queue) > 0 {
		sha := queue[0]
		queue = queue[1:]

		typ, data, err := s.object(sha)
		if err != nil {
			return err
		}
		if typ != "commit" {
			return fmt.Errorf("gitrepo: %s is a %s, want a commit", sha, typ)
		}
		commits++

		c := parseCommit(data)
		children := make([]string, 0, len(c.parents)+1)
		withinLimit := maxCommits == 0 || commits < maxCommits
		for _, parent := range c.parents {
			if !withinLimit {
				break
			}
			children = append(children, parent)
			if !seen[parent] {
				seen[parent] = true
				queue = append(queue, parent)
			}
		}
		if c.tree != "" {
			children = append(children, c.tree)
			if err := s.readTree(c.tree, "/"); err != nil {
				return err
			}
		}

		s.nodes[sha] = graph.Node[string]{
			ID:       sha,
			Hash:     mustHex(sha),
			Kind:     "commit",
			Label:    c.subject(),
			Children: children,
			Payload: []graph.Field{
				{Key: "commit", Value: short(sha)},
				{Key: "author", Value: c.author},
				{Key: "date", Value: c.date},
				{Key: "parents", Value: strconv.Itoa(len(c.parents))},
			},
		}
	}
	return nil
}

func (s *Source) readTree(sha, name string) error {
	if _, done := s.nodes[sha]; done {
		return nil
	}
	typ, data, err := s.object(sha)
	if err != nil {
		return err
	}
	if typ != "tree" {
		return fmt.Errorf("gitrepo: %s is a %s, want a tree", sha, typ)
	}

	// Reserve the slot before recursing so a tree that somehow reaches itself
	// cannot loop.
	s.nodes[sha] = graph.Node[string]{ID: sha, Kind: "tree", Label: name}

	entries, err := parseTree(data)
	if err != nil {
		return fmt.Errorf("gitrepo: tree %s: %w", sha, err)
	}

	children := make([]string, 0, len(entries))
	var dirs, files int
	for _, e := range entries {
		if _, known := s.nodes[e.sha]; !known {
			if e.dir() {
				if err := s.readTree(e.sha, e.name); err != nil {
					return err
				}
			} else if err := s.readBlob(e); err != nil {
				return err
			}
		}
		if _, ok := s.nodes[e.sha]; !ok {
			continue
		}
		children = append(children, e.sha)
		if e.dir() {
			dirs++
		} else {
			files++
		}
	}

	s.nodes[sha] = graph.Node[string]{
		ID:       sha,
		Hash:     mustHex(sha),
		Kind:     "tree",
		Label:    name,
		Children: children,
		Payload: []graph.Field{
			{Key: "tree", Value: short(sha)},
			{Key: "entries", Value: strconv.Itoa(len(children))},
			{Key: "directories", Value: strconv.Itoa(dirs)},
			{Key: "files", Value: strconv.Itoa(files)},
		},
	}
	return nil
}

func (s *Source) readBlob(e treeEntry) error {
	// Submodules appear as commit entries inside a tree and have no object in
	// this repository, so they are skipped rather than treated as missing.
	if e.mode == "160000" {
		return nil
	}
	typ, data, err := s.object(e.sha)
	if err != nil {
		return err
	}
	if typ != "blob" {
		return fmt.Errorf("gitrepo: %s is a %s, want a blob", e.sha, typ)
	}
	s.nodes[e.sha] = graph.Node[string]{
		ID:    e.sha,
		Hash:  mustHex(e.sha),
		Kind:  "blob",
		Label: e.name,
		Payload: []graph.Field{
			{Key: "blob", Value: short(e.sha)},
			{Key: "size", Value: humanBytes(len(data))},
			{Key: "mode", Value: e.mode},
		},
	}
	return nil
}

type commit struct {
	tree    string
	parents []string
	author  string
	date    string
	message string
}

func (c commit) subject() string {
	line, _, _ := strings.Cut(strings.TrimSpace(c.message), "\n")
	if line == "" {
		return "(no message)"
	}
	return line
}

func parseCommit(data []byte) commit {
	var c commit
	header, message, _ := strings.Cut(string(data), "\n\n")
	c.message = message
	for line := range strings.Lines(header) {
		line = strings.TrimRight(line, "\n")
		key, value, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		switch key {
		case "tree":
			c.tree = value
		case "parent":
			c.parents = append(c.parents, value)
		case "author":
			c.author, c.date = parseIdentity(value)
		}
	}
	return c
}

// parseIdentity splits "Name <email> 1700000000 +0000" into a name and a date.
func parseIdentity(v string) (who, when string) {
	close := strings.LastIndex(v, ">")
	if close < 0 {
		return v, ""
	}
	who = strings.TrimSpace(v[:close+1])
	rest := strings.Fields(v[close+1:])
	if len(rest) == 0 {
		return who, ""
	}
	secs, err := strconv.ParseInt(rest[0], 10, 64)
	if err != nil {
		return who, ""
	}
	return who, formatUnix(secs)
}

type treeEntry struct {
	mode string
	name string
	sha  string
}

func (e treeEntry) dir() bool { return e.mode == "40000" || e.mode == "040000" }

func parseTree(data []byte) ([]treeEntry, error) {
	var entries []treeEntry
	for len(data) > 0 {
		space := bytes.IndexByte(data, ' ')
		if space < 0 {
			return nil, errors.New("truncated entry mode")
		}
		zero := bytes.IndexByte(data[space:], 0)
		if zero < 0 {
			return nil, errors.New("truncated entry name")
		}
		zero += space
		if len(data) < zero+1+20 {
			return nil, errors.New("truncated entry hash")
		}
		entries = append(entries, treeEntry{
			mode: string(data[:space]),
			name: string(data[space+1 : zero]),
			sha:  hex.EncodeToString(data[zero+1 : zero+21]),
		})
		data = data[zero+21:]
	}
	return entries, nil
}

func (s *Source) object(sha string) (string, []byte, error) {
	if typ, data, err := s.looseObject(sha); err == nil {
		return typ, data, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", nil, err
	}
	for _, p := range s.packs {
		if off, ok := p.find(sha); ok {
			return p.object(off, s)
		}
	}
	return "", nil, fmt.Errorf("gitrepo: object %s not found", sha)
}

func (s *Source) looseObject(sha string) (string, []byte, error) {
	if len(sha) < 3 {
		return "", nil, os.ErrNotExist
	}
	f, err := os.Open(filepath.Join(s.dir, "objects", sha[:2], sha[2:]))
	if err != nil {
		return "", nil, err
	}
	defer f.Close()

	zr, err := zlib.NewReader(f)
	if err != nil {
		return "", nil, fmt.Errorf("gitrepo: object %s: %w", sha, err)
	}
	defer zr.Close()

	raw, err := io.ReadAll(zr)
	if err != nil {
		return "", nil, fmt.Errorf("gitrepo: object %s: %w", sha, err)
	}
	head, body, ok := bytes.Cut(raw, []byte{0})
	if !ok {
		return "", nil, fmt.Errorf("gitrepo: object %s has no header", sha)
	}
	typ, _, _ := strings.Cut(string(head), " ")
	return typ, body, nil
}

func short(sha string) string {
	if len(sha) > 8 {
		return sha[:8]
	}
	return sha
}

func mustHex(sha string) []byte {
	b, err := hex.DecodeString(sha)
	if err != nil {
		return nil
	}
	return b
}

func formatUnix(secs int64) string {
	return time.Unix(secs, 0).UTC().Format("2006-01-02 15:04")
}

func humanBytes(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f kB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
}
