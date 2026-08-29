package layout

import "slices"

// Nesting by first introduction, for a chain laid out side by side.
//
// Cutting the edges between the links of a chain takes away the single path that
// everything beneath them ran through, so dominance puts almost the whole
// repository at the top level and the hierarchy collapses into a scatter. That is
// what used to make [Options.SeparateChains] cost more than it saved.
//
// The links themselves still say where each object came from. Giving an object to
// the oldest link that reaches it states something true — this is where it first
// appeared — and leaves every link holding what it introduced, which is how a
// history is usually read. Later links that reuse the object keep pointing at it,
// and those edges are reported as references.

// nestByIntroduction rewrites the flow graph so that each link of the chain
// reaches only what it introduced, before dominance decides the nesting.
//
// Rewriting the edges rather than reparenting the nodes directly is what keeps
// the structure inside a link intact: a blob still sits in the tree that names it,
// rather than being flattened into the commit alongside it.
func (p *packer[K]) nestByIntroduction() {
	links := p.chainOldestFirst()
	if len(links) == 0 {
		return
	}
	// Ownership is worked out over every link, including those past a chain cap, so
	// that what a dropped link introduced is dropped with it rather than surfacing
	// as though a surviving link had brought it in.
	owner := p.introducers(links)
	dropped := func(i int) bool {
		return p.beyond[i] || (owner[i] >= 0 && p.beyond[owner[i]])
	}

	children := make([][]int, p.n+1)
	preds := make([][]int, p.n+1)
	for i := range p.n {
		for _, c := range p.introduced(owner, i, dropped) {
			children[i] = append(children[i], c)
			preds[c] = append(preds[c], i)
		}
	}

	// Every surviving link is a top-level node now, along with whatever the source
	// declared as a root, and anything a cut edge would otherwise leave with no way
	// in.
	entry := make(map[int]bool, len(links))
	for _, link := range links {
		if !dropped(link) {
			entry[link] = true
		}
	}
	for root := range p.g.Roots() {
		entry[p.index[root]] = true
	}
	for i := range p.n {
		if !dropped(i) && (entry[i] || len(preds[i]) == 0) {
			children[p.start] = append(children[p.start], i)
			preds[i] = append(preds[i], p.start)
		}
	}
	p.f = flow{start: p.start, children: children, preds: preds}
}

// introduced lists the children a node keeps: those it introduced itself, or that
// were introduced alongside it, minus anything a chain cap dropped.
func (p *packer[K]) introduced(owner []int, i int, dropped func(int) bool) []int {
	if dropped(i) {
		return nil
	}
	out := make([]int, 0, len(p.f.children[i]))
	for child := range p.g.Children(p.ids[i]) {
		c := p.index[child]
		if dropped(c) || p.isChainEdge(i, c) || !introduces(owner, i, c) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// introducers maps each node to the oldest link of the chain that reaches it, or
// -1 for a node the chain never reaches.
func (p *packer[K]) introducers(links []int) []int {
	owner := make([]int, p.n)
	for i := range owner {
		owner[i] = -1
	}
	for _, link := range links {
		owner[link] = link
	}
	for _, link := range links {
		for _, node := range p.reachedWithoutChain(link) {
			if owner[node] < 0 {
				owner[node] = link
			}
		}
	}
	return owner
}

// introduces reports whether an edge belongs to the link that introduced its
// target: either that link points straight at it, or both ends were introduced
// together. Everything else is a later reuse, and is reported as a reference
// rather than drawn as containment.
func introduces(owner []int, from, to int) bool {
	if owner[to] < 0 {
		// Nothing in the chain reaches it, so introduction has no say.
		return true
	}
	return from == owner[to] || owner[from] == owner[to]
}

// reachedWithoutChain lists what a link reaches through its content, stopping at
// any other link: what a later commit went on to do is not what this one held.
func (p *packer[K]) reachedWithoutChain(from int) []int {
	seen := map[int]bool{from: true}
	var out []int
	for queue := []int{from}; len(queue) > 0; {
		cur := queue[0]
		queue = queue[1:]
		for child := range p.g.Children(p.ids[cur]) {
			c := p.index[child]
			if seen[c] || p.isChainEdge(cur, c) {
				continue
			}
			seen[c] = true
			out = append(out, c)
			queue = append(queue, c)
		}
	}
	return out
}

// chainOldestFirst orders the links of the chain with the oldest first, so the
// earliest link to reach an object is the one that gets it.
func (p *packer[K]) chainOldestFirst() []int {
	var links []int
	for i := range p.n {
		if p.isChainLink(i) {
			links = append(links, i)
		}
	}
	if len(links) == 0 {
		return nil
	}

	// A link's age is how far the chain still runs behind it, so the newest has the
	// longest run and the oldest has none. Where a merge offers two routes back the
	// longer one counts, which keeps every link newer than everything it followed.
	age := make(map[int]int, len(links))
	var walk func(int) int
	walk = func(i int) int {
		if a, ok := age[i]; ok {
			return a
		}
		// Set before recursing so that a source that reports a cycle cannot hang the
		// layout here, ahead of the check that would reject it.
		age[i] = 0
		longest := 0
		for _, c := range p.chainChildren(i) {
			longest = max(longest, walk(c)+1)
		}
		age[i] = longest
		return longest
	}
	for _, link := range links {
		walk(link)
	}
	slices.SortFunc(links, func(a, b int) int {
		if d := age[a] - age[b]; d != 0 {
			return d
		}
		return a - b
	})
	return links
}

func (p *packer[K]) hasChainParent(i int) bool {
	for parent := range p.g.Parents(p.ids[i]) {
		if p.isChainEdge(p.index[parent], i) {
			return true
		}
	}
	return false
}
