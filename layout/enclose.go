package layout

import "math"

// Smallest-enclosing-circle by Welzl's move-to-front algorithm, following the
// formulation used by d3-hierarchy. The incremental basis is rebuilt whenever a
// circle falls outside the current candidate, which is expected-linear once the
// input has been shuffled.

const (
	// enclosingSlack absorbs floating point error when testing containment, so a
	// circle sitting exactly on the boundary counts as enclosed.
	enclosingSlack = 1e-9
	// tangentSlack lets circles touch without registering as an intersection.
	tangentSlack = 1e-6
)

func enclose(circles []Circle) Circle {
	switch len(circles) {
	case 0:
		return Circle{}
	case 1:
		return circles[0]
	}

	shuffled := make([]Circle, len(circles))
	copy(shuffled, circles)
	shuffleCircles(shuffled)

	var basis []Circle
	var best Circle
	var haveBest bool
	for i := 0; i < len(shuffled); {
		if haveBest && enclosesWeak(best, shuffled[i]) {
			i++
			continue
		}
		basis = extendBasis(basis, shuffled[i])
		best, haveBest = encloseBasis(basis), true
		i = 0
	}
	return best
}

func extendBasis(basis []Circle, p Circle) []Circle {
	if enclosesWeakAll(p, basis) {
		return []Circle{p}
	}
	for _, b := range basis {
		if enclosesNot(p, b) && enclosesWeakAll(encloseBasis2(b, p), basis) {
			return []Circle{b, p}
		}
	}
	for i := range len(basis) - 1 {
		for j := i + 1; j < len(basis); j++ {
			if enclosesNot(encloseBasis2(basis[i], basis[j]), p) &&
				enclosesNot(encloseBasis2(basis[i], p), basis[j]) &&
				enclosesNot(encloseBasis2(basis[j], p), basis[i]) &&
				enclosesWeakAll(encloseBasis3(basis[i], basis[j], p), basis) {
				return []Circle{basis[i], basis[j], p}
			}
		}
	}
	// Degenerate input, such as coincident circles, can defeat the basis search.
	// Falling back to the new circle alone keeps the caller making progress.
	return []Circle{p}
}

func enclosesNot(a, b Circle) bool {
	dr := a.R - b.R
	dx := b.X - a.X
	dy := b.Y - a.Y
	return dr < 0 || dr*dr < dx*dx+dy*dy
}

func enclosesWeak(a, b Circle) bool {
	dr := a.R - b.R + max(a.R, b.R, 1)*enclosingSlack
	dx := b.X - a.X
	dy := b.Y - a.Y
	return dr > 0 && dr*dr > dx*dx+dy*dy
}

func enclosesWeakAll(a Circle, basis []Circle) bool {
	for _, b := range basis {
		if !enclosesWeak(a, b) {
			return false
		}
	}
	return true
}

func encloseBasis(basis []Circle) Circle {
	switch len(basis) {
	case 1:
		return basis[0]
	case 2:
		return encloseBasis2(basis[0], basis[1])
	case 3:
		return encloseBasis3(basis[0], basis[1], basis[2])
	default:
		return Circle{}
	}
}

func encloseBasis2(a, b Circle) Circle {
	x21, y21, r21 := b.X-a.X, b.Y-a.Y, b.R-a.R
	l := math.Hypot(x21, y21)
	if l == 0 {
		return Circle{X: a.X, Y: a.Y, R: max(a.R, b.R)}
	}
	return Circle{
		X: (a.X + b.X + x21/l*r21) / 2,
		Y: (a.Y + b.Y + y21/l*r21) / 2,
		R: (l + a.R + b.R) / 2,
	}
}

func encloseBasis3(a, b, c Circle) Circle {
	a2, a3 := a.X-b.X, a.X-c.X
	b2, b3 := a.Y-b.Y, a.Y-c.Y
	c2, c3 := b.R-a.R, c.R-a.R
	ab := a3*b2 - a2*b3
	if ab == 0 {
		// The three centres are collinear, so no circumscribing circle exists.
		return encloseBasis2(encloseBasis2(a, b), c)
	}

	d1 := a.X*a.X + a.Y*a.Y - a.R*a.R
	d2 := d1 - b.X*b.X - b.Y*b.Y + b.R*b.R
	d3 := d1 - c.X*c.X - c.Y*c.Y + c.R*c.R

	xa := (b2*d3-b3*d2)/(ab*2) - a.X
	xb := (b3*c2 - b2*c3) / ab
	ya := (a3*d2-a2*d3)/(ab*2) - a.Y
	yb := (a2*c3 - a3*c2) / ab

	qa := xb*xb + yb*yb - 1
	qb := 2 * (a.R + xa*xb + ya*yb)
	qc := xa*xa + ya*ya - a.R*a.R

	var r float64
	if math.Abs(qa) > tangentSlack {
		disc := qb*qb - 4*qa*qc
		if disc < 0 {
			disc = 0
		}
		r = -((qb + math.Sqrt(disc)) / (2 * qa))
	} else if qb != 0 {
		r = -(qc / qb)
	} else {
		return encloseBasis2(encloseBasis2(a, b), c)
	}

	return Circle{X: a.X + xa + xb*r, Y: a.Y + ya + yb*r, R: r}
}
