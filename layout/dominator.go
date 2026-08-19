package layout

// Immediate dominators by the iterative algorithm of Cooper, Harvey and Kennedy,
// "A Simple, Fast Dominance Algorithm" (2001).
//
// A containment layout has to decide where a node with several parents lives, and
// the dominator tree gives the principled answer: every path from the root to a
// node passes through its immediate dominator, so nesting the node there is always
// truthful. Edges from the node's other parents become reference links instead.

type flow struct {
	start    int
	children [][]int
	preds    [][]int
}

func (f flow) dominators() []int {
	n := len(f.children)
	order, number := f.reversePostorder()

	idom := make([]int, n)
	for i := range idom {
		idom[i] = -1
	}
	idom[f.start] = f.start

	intersect := func(a, b int) int {
		for a != b {
			for number[a] > number[b] {
				a = idom[a]
			}
			for number[b] > number[a] {
				b = idom[b]
			}
		}
		return a
	}

	for changed := true; changed; {
		changed = false
		for _, b := range order {
			if b == f.start {
				continue
			}
			candidate := -1
			for _, p := range f.preds[b] {
				if idom[p] == -1 {
					continue
				}
				if candidate == -1 {
					candidate = p
				} else {
					candidate = intersect(p, candidate)
				}
			}
			if candidate != -1 && idom[b] != candidate {
				idom[b] = candidate
				changed = true
			}
		}
	}
	return idom
}

func (f flow) reversePostorder() (order []int, number []int) {
	n := len(f.children)
	number = make([]int, n)
	for i := range number {
		number[i] = -1
	}

	type frame struct {
		node int
		next int
	}
	var post []int
	visited := make([]bool, n)
	stack := []frame{{node: f.start}}
	visited[f.start] = true
	for len(stack) > 0 {
		top := &stack[len(stack)-1]
		kids := f.children[top.node]
		if top.next >= len(kids) {
			post = append(post, top.node)
			stack = stack[:len(stack)-1]
			continue
		}
		child := kids[top.next]
		top.next++
		if !visited[child] {
			visited[child] = true
			stack = append(stack, frame{node: child})
		}
	}

	order = make([]int, 0, len(post))
	for i := len(post) - 1; i >= 0; i-- {
		order = append(order, post[i])
		number[post[i]] = len(order) - 1
	}
	return order, number
}
