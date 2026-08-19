package layout

import (
	"math"
	"math/rand/v2"
)

// Sibling packing by the front-chain algorithm of Wang et al., following the
// formulation used by d3-hierarchy. Circles are placed one at a time tangent to
// the two chain members nearest the centroid, and the chain is rewound whenever
// the new circle collides with one already on it.

// packSeed keeps the shuffle inside enclose deterministic, so identical input
// always produces an identical layout.
var packSeed = [2]uint64{0x6d65726b_656c6272, 0x6f745f7061_636b_00}

func shuffleCircles(c []Circle) {
	r := rand.New(rand.NewPCG(packSeed[0], packSeed[1]))
	r.Shuffle(len(c), func(i, j int) { c[i], c[j] = c[j], c[i] })
}

func intersects(a, b Circle) bool {
	dr := a.R + b.R - tangentSlack
	dx := b.X - a.X
	dy := b.Y - a.Y
	return dr > 0 && dr*dr > dx*dx+dy*dy
}

func place(pb, pa Circle, c *Circle) {
	dx := pb.X - pa.X
	dy := pb.Y - pa.Y
	d2 := dx*dx + dy*dy
	if d2 == 0 {
		c.X = pa.X + c.R
		c.Y = pa.Y
		return
	}
	a2 := (pa.R + c.R) * (pa.R + c.R)
	b2 := (pb.R + c.R) * (pb.R + c.R)
	if a2 > b2 {
		x := (d2 + b2 - a2) / (2 * d2)
		y := math.Sqrt(max(0, b2/d2-x*x))
		c.X = pb.X - x*dx - y*dy
		c.Y = pb.Y - x*dy + y*dx
		return
	}
	x := (d2 + a2 - b2) / (2 * d2)
	y := math.Sqrt(max(0, a2/d2-x*x))
	c.X = pa.X + x*dx - y*dy
	c.Y = pa.Y + x*dy + y*dx
}

func score(a, b Circle) float64 {
	ab := a.R + b.R
	if ab == 0 {
		return 0
	}
	dx := (a.X*b.R + b.X*a.R) / ab
	dy := (a.Y*b.R + b.Y*a.R) / ab
	return dx*dx + dy*dy
}

// Circles are positioned in place, centred on the origin, and the enclosing radius
// is returned.
func packSiblings(circles []Circle) float64 {
	n := len(circles)
	switch n {
	case 0:
		return 0
	case 1:
		circles[0].X, circles[0].Y = 0, 0
		return circles[0].R
	}

	circles[0].Y, circles[1].Y = 0, 0
	circles[0].X = -circles[1].R
	circles[1].X = circles[0].R
	if n == 2 {
		return circles[0].R + circles[1].R
	}
	place(circles[1], circles[0], &circles[2])

	prev := make([]int, n)
	next := make([]int, n)
	ai, bi, ci := 0, 1, 2
	next[ai], prev[ci] = bi, bi
	next[bi], prev[ai] = ci, ci
	next[ci], prev[bi] = ai, ai

pack:
	for i := 3; i < n; i++ {
		place(circles[ai], circles[bi], &circles[i])
		ci = i

		// Walk outwards along the chain in both directions, always advancing the
		// side that has covered the least arc, and rewind on the first collision.
		j, k := next[bi], prev[ai]
		sj, sk := circles[bi].R, circles[ai].R
		for {
			if sj <= sk {
				if intersects(circles[j], circles[ci]) {
					bi = j
					next[ai], prev[bi] = bi, ai
					i--
					continue pack
				}
				sj += circles[j].R
				j = next[j]
			} else {
				if intersects(circles[k], circles[ci]) {
					ai = k
					next[ai], prev[bi] = bi, ai
					i--
					continue pack
				}
				sk += circles[k].R
				k = prev[k]
			}
			if j == next[k] {
				break
			}
		}

		prev[ci], next[ci] = ai, bi
		next[ai], prev[bi] = ci, ci

		// Re-anchor the chain on the pair now closest to the centroid.
		bi = ci
		best := score(circles[ai], circles[next[ai]])
		for c := next[ci]; c != bi; c = next[c] {
			if s := score(circles[c], circles[next[c]]); s < best {
				ai, best = c, s
			}
		}
		bi = next[ai]
	}

	chain := []Circle{circles[bi]}
	for c := next[bi]; c != bi; c = next[c] {
		chain = append(chain, circles[c])
	}
	e := enclose(chain)
	for i := range circles {
		circles[i].X -= e.X
		circles[i].Y -= e.Y
	}
	return e.R
}

// Equal circles packed into a unit disc is how a node's payload fields are laid
// out inside it.
func packUnit(n int) []Circle {
	circles := make([]Circle, n)
	for i := range circles {
		circles[i].R = 1
	}
	r := packSiblings(circles)
	if r <= 0 {
		return circles
	}
	for i := range circles {
		circles[i].X /= r
		circles[i].Y /= r
		circles[i].R /= r
	}
	return circles
}
