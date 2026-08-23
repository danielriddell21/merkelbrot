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
	order, number := f.reversePostorder()

	idom := make([]int, len(f.children))
	for i := range idom {
		idom[i] = -1
	}
	idom[f.start] = f.start

	for changed := true; changed; {
		changed = false
		for _, b := range order {
			if b == f.start {
				continue
			}
			candidate := f.candidate(b, idom, number)
			if candidate != -1 && idom[b] != candidate {
				idom[b] = candidate
				changed = true
			}
		}
	}
	return idom
}

// candidate folds b's already-processed predecessors together, which is the
// running intersection that the iteration drives to a fixed point.
func (f flow) candidate(b int, idom, number []int) int {
	found := -1
	for _, p := range f.preds[b] {
		if idom[p] == -1 {
			continue
		}
		if found == -1 {
			found = p
			continue
		}
		found = intersect(p, found, idom, number)
	}
	return found
}

// intersect walks two nodes up the partially built dominator tree until they
// meet, always advancing whichever sits later in reverse postorder.
func intersect(a, b int, idom, number []int) int {
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
