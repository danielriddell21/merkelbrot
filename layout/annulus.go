package layout

import (
	"math"
	"slices"
)

// Annular sizing for the links of a chain.
//
// A chain link contains its predecessor and its own content, and packing those
// as siblings gives the predecessor almost the whole disc: the ring around it
// holds a single small disc off to one side, and the rest is void. Placing the
// predecessor at the centre and spreading the content around it instead fills
// that ring, and gives it a meaning — everything in the ring is what this link
// added over the one inside it.

// labelGap is the angle left clear at the top of a ring, so the node keeps
// somewhere to write its own label.
const labelGap = 0.5

// chainCore picks the link a node is built around, or -1 where there is none.
//
// A ring means "this continues that": the link at the centre is the state before,
// and what surrounds it is what this one added. Only a node continuing exactly
// one link can say that. A merge joins two lines and neither is inside the other,
// so it has no core and its parents are packed side by side instead — which is
// what a merge looks like. Ringing one around the other would claim a single
// predecessor the graph does not have, and after a merge the line not chosen
// keeps nothing of its own, so the centre would be an empty stub.
func (p *packer[K]) chainCore(v int, kids []int) int {
	if v == p.start {
		return -1
	}
	core := -1
	for _, c := range p.chainChildren(v) {
		// A link nested somewhere else is not this node's to build around.
		if !slices.Contains(kids, c) {
			continue
		}
		if core >= 0 {
			return -1
		}
		core = c
	}
	return core
}

// sizeAnnulus lays a node out as a core disc with everything else ringed around
// it, and sizes the node to hold them.
func (p *packer[K]) sizeAnnulus(v, core int, kids []int, fields int) {
	offsets := make([]Circle, len(kids))
	inner := p.radius[core]

	// Everything except the core goes in the ring, the payload disc included. An
	// index of -1 marks that disc, which belongs to the node itself rather than to
	// any of its children.
	at := make([]int, 0, len(kids)+1)
	radii := make([]float64, 0, len(kids)+1)
	for i, kid := range kids {
		if kid == core {
			offsets[i] = Circle{R: inner}
			continue
		}
		at = append(at, i)
		radii = append(radii, p.radius[kid])
	}
	if fields > 0 {
		at = append(at, -1)
		radii = append(radii, payloadRadius(fields, p.opts))
	}

	if len(at) == 0 {
		p.radius[v] = max(inner+p.opts.Padding, p.minRing(kids))
		p.offsets[v] = offsets
		return
	}

	widest := 0.0
	for _, r := range radii {
		widest = max(widest, r)
	}
	distance := ringDistance(inner+p.opts.Padding, radii)
	// A link that added very little would otherwise get a ring too thin to be
	// labelled, so the same minimum applies here as to any other container.
	p.radius[v] = max(distance+widest+p.opts.Padding, p.minRing(kids))

	// Whatever room is left over after the label's gap is shared out evenly, so a
	// ring holding one disc spaces it opposite the label rather than beside it.
	half := halfWidths(distance, radii)
	var span float64
	for _, w := range half {
		span += 2 * w
	}
	spare := math.Max(0, 2*math.Pi-span-labelGap) / float64(len(half))

	angle := -math.Pi/2 + (spare+labelGap)/2 + half[0]
	for i, r := range radii {
		if i > 0 {
			angle += half[i-1] + spare + half[i]
		}
		c := Circle{X: distance * math.Cos(angle), Y: distance * math.Sin(angle), R: r}
		if at[i] >= 0 {
			offsets[at[i]] = c
			continue
		}
		p.area[v] = p.payloadArea(c, p.radius[v])
	}
	p.offsets[v] = offsets
}

// halfWidths gives half the angle each disc covers when its centre sits at the
// given distance, which is what decides how many of them will go round.
func halfWidths(distance float64, radii []float64) []float64 {
	out := make([]float64, len(radii))
	for i, r := range radii {
		out[i] = math.Asin(math.Min(1, r/distance))
	}
	return out
}

// ringDistance finds how far out the ring has to sit for its discs to fit around
// the core without touching and with the label's gap still to spare, never
// closer than the core it surrounds.
func ringDistance(inner float64, radii []float64) float64 {
	widest := 0.0
	for _, r := range radii {
		widest = max(widest, r)
	}
	low := inner + widest

	fits := func(d float64) bool {
		var span float64
		for _, w := range halfWidths(d, radii) {
			span += 2 * w
		}
		return span+labelGap <= 2*math.Pi
	}

	high := low
	for range 40 {
		if fits(high) {
			break
		}
		high *= 1.6
	}
	for range 40 {
		mid := (low + high) / 2
		if fits(mid) {
			high = mid
		} else {
			low = mid
		}
	}
	return high
}
