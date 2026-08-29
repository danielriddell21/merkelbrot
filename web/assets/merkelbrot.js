// Zoomable viewer for a merkelbrot scene.
//
// The scene arrives in layout coordinates centred on the origin. One transform —
// a scale and an offset — maps those to pixels, and zooming only ever changes the
// scale, so movement from the whole graph to the inside of a single node is
// continuous. Detail is chosen per node from its radius on screen rather than
// from a discrete zoom level, which is what keeps the transitions smooth.
(function () {
	"use strict";

	var scene = JSON.parse(document.getElementById("scene").textContent);
	var canvas = document.getElementById("view");
	var ctx = canvas.getContext("2d");
	var tip = document.getElementById("tip");
	var crumb = document.getElementById("crumb");

	// Radii in CSS pixels at which a node earns more detail.
	var MIN_DRAW = 0.4;
	var MIN_RECURSE = 4;
	var MIN_LABEL = 22;
	var MIN_FIELD = 9;
	var MIN_FIELD_TEXT = 24;
	var MIN_LINK = 2.5;
	var MIN_RIBBON = 34;
	var MIN_SPOKES = 58;
	var MIN_DERIVED = 70;

	// Derived detail is generated from a node's digest rather than read from the
	// graph. It is what keeps the zoom unbounded once the data runs out, and it is
	// drawn faintly and unlabelled so it never reads as content. Press d to stop.
	var deepDetail = true;

	// Each kind anchors a hue; the node's own hash then shifts it within a band.
	// Two nodes with the same hash therefore get exactly the same colour, which is
	// the Merkle property made visible: identical content looks identical wherever
	// it is reached from.
	var HUES = [215, 42, 145, 340, 272, 178, 22, 205];
	var PALETTE = [
		"#4c8dff", "#f2b134", "#3fbf7f", "#e0607e",
		"#9b7ede", "#38b6b0", "#e08a3c", "#7f8c9b",
	];
	var MARKS = {
		path: "#ffd166",
		evidence: "#8ecae6",
		shared: "#4c8dff",
		added: "#3fbf7f",
		removed: "#e0607e",
		invalid: "#ff5d5d",
	};

	var dark = window.matchMedia("(prefers-color-scheme: dark)");
	var byId = new Map();
	var children = new Map();
	var roots = [];
	var kindIndex = new Map();
	var marked = new Map();
	var markedLinks = new Map();

	scene.kinds = scene.kinds || [];
	scene.nodes = scene.nodes || [];
	scene.links = scene.links || [];
	scene.highlights = scene.highlights || [];

	scene.kinds.forEach(function (k, i) { kindIndex.set(k, i); });
	scene.nodes.forEach(function (n) {
		byId.set(n.id, n);
		if (n.parent) {
			if (!children.has(n.parent)) children.set(n.parent, []);
			children.get(n.parent).push(n);
		} else {
			roots.push(n);
		}
	});
	scene.highlights.forEach(function (h) {
		(h.nodes || []).forEach(function (id) { marked.set(id, h.kind); });
		(h.links || []).forEach(function (l) { markedLinks.set(l.from + " " + l.to, h.kind); });
	});

	var colours = new Map();

	// hashSeed folds a hex digest into one 32-bit value (FNV-1a).
	function hashSeed(hex) {
		var h = 2166136261 >>> 0;
		for (var i = 0; i < hex.length; i++) {
			h ^= hex.charCodeAt(i);
			h = Math.imul(h, 16777619) >>> 0;
		}
		return h >>> 0;
	}

	function colourFor(node) {
		var got = colours.get(node.id);
		if (got) return got;

		var i = kindIndex.has(node.kind) ? kindIndex.get(node.kind) : 0;
		var anchor = HUES[i % HUES.length];
		var c;
		if (!node.hash) {
			c = { h: anchor, s: 60, l: 56 };
		} else {
			var seed = hashSeed(node.hash);
			c = {
				h: (anchor + ((seed & 0xff) / 255) * 46 - 23 + 360) % 360,
				s: 50 + (((seed >>> 8) & 0x3f) / 63) * 30,
				l: 44 + (((seed >>> 16) & 0x3f) / 63) * 24,
			};
		}
		colours.set(node.id, c);
		return c;
	}

	function shade(c, alpha) {
		return "hsla(" + c.h.toFixed(1) + "," + c.s.toFixed(1) + "%," + c.l.toFixed(1) + "%," + alpha + ")";
	}

	paintLegend();
	function paintLegend() {
		document.querySelectorAll(".kinds li").forEach(function (li) {
			li.style.setProperty("--swatch", PALETTE[Number(li.dataset.kind) % PALETTE.length]);
		});
		document.querySelectorAll(".marks li").forEach(function (li) {
			li.style.setProperty("--swatch", MARKS[li.dataset.mark] || "#888");
		});
	}

	// View transform: screen = (world - centre) * scale + viewport centre.
	var view = { x: 0, y: 0, scale: 1 };
	var width = 0, height = 0, dpr = 1;
	var animation = null;
	var hovered = null;
	var pointer = null;

	function resize() {
		dpr = window.devicePixelRatio || 1;
		width = canvas.clientWidth;
		height = canvas.clientHeight;
		canvas.width = Math.round(width * dpr);
		canvas.height = Math.round(height * dpr);
		draw();
	}

	function fitTo(cx, cy, r, animate) {
		var scale = Math.min(width, height) / (2 * r * 1.06);
		var target = { x: cx, y: cy, scale: scale };
		if (!animate) {
			view = target;
			draw();
			return;
		}
		animateTo(target);
	}

	function fitAll(animate) {
		var r = scene.bounds && scene.bounds.r ? scene.bounds.r : 1;
		fitTo(scene.bounds.x || 0, scene.bounds.y || 0, r, animate);
	}

	function animateTo(target) {
		var from = { x: view.x, y: view.y, scale: view.scale };
		var start = performance.now();
		var ms = 420;
		if (animation) cancelAnimationFrame(animation);
		function step(now) {
			var t = Math.min(1, (now - start) / ms);
			var e = t < 0.5 ? 4 * t * t * t : 1 - Math.pow(-2 * t + 2, 3) / 2;
			view.x = from.x + (target.x - from.x) * e;
			view.y = from.y + (target.y - from.y) * e;
			// Interpolating the logarithm keeps the apparent speed even across
			// large changes of scale.
			view.scale = Math.exp(Math.log(from.scale) + (Math.log(target.scale) - Math.log(from.scale)) * e);
			draw();
			if (t < 1) animation = requestAnimationFrame(step);
			else animation = null;
		}
		animation = requestAnimationFrame(step);
	}

	function toScreenX(x) { return (x - view.x) * view.scale + width / 2; }
	function toScreenY(y) { return (y - view.y) * view.scale + height / 2; }
	function toWorldX(sx) { return (sx - width / 2) / view.scale + view.x; }
	function toWorldY(sy) { return (sy - height / 2) / view.scale + view.y; }

	function draw() {
		ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
		ctx.clearRect(0, 0, width, height);

		var visible = [];
		roots.forEach(function (n) { drawNode(n, visible); });
		drawLinks(visible);
		drawLabels(visible);
	}

	function drawNode(node, visible) {
		var r = node.r * view.scale;
		if (r < MIN_DRAW) return;

		var sx = toScreenX(node.x);
		var sy = toScreenY(node.y);
		if (sx + r < 0 || sx - r > width || sy + r < 0 || sy - r > height) return;

		var colour = colourFor(node);
		var mark = marked.get(node.id);

		ctx.beginPath();
		ctx.arc(sx, sy, r, 0, Math.PI * 2);
		ctx.fillStyle = shade(colour, node.leaf ? 0.32 : 0.09);
		ctx.fill();

		ctx.lineWidth = Math.min(2, Math.max(0.4, r * 0.02));
		ctx.strokeStyle = shade(colour, 0.78);
		ctx.stroke();

		if (node.shared) {
			// A shared node is drawn with a second ring: it is nested here, but it
			// is reachable from elsewhere too.
			ctx.beginPath();
			ctx.arc(sx, sy, Math.max(1, r - ctx.lineWidth * 2.5), 0, Math.PI * 2);
			ctx.setLineDash([r * 0.12, r * 0.09]);
			ctx.strokeStyle = shade(colour, 0.5);
			ctx.stroke();
			ctx.setLineDash([]);
		}

		if (mark) {
			ctx.beginPath();
			ctx.arc(sx, sy, r + 2, 0, Math.PI * 2);
			ctx.lineWidth = Math.min(4, Math.max(1.5, r * 0.06));
			ctx.strokeStyle = MARKS[mark] || "#fff";
			ctx.stroke();
		}

		var entry = { node: node, sx: sx, sy: sy, r: r };
		visible.push(entry);

		if (r >= MIN_RIBBON) drawRibbon(node, sx, sy, r, colour);

		// Fields all share a radius, so one test decides the whole payload.
		var hasFields = node.fields && node.fields.length;
		if (hasFields && node.fields[0].r * r >= MIN_FIELD) {
			drawFields(node, sx, sy, r);
		} else if (!hasFields && node.leaf && deepDetail && r >= MIN_DERIVED) {
			// A leaf with nothing declared inside it still has its digest, so the
			// descent continues into structure derived from that.
			derive(sx, sy, r * 0.82, hashSeed(node.hash || node.id), 0);
		}
		if (r >= MIN_RECURSE) {
			var kids = children.get(node.id);
			if (kids) {
				// The spokes go down first so the children paint over their ends,
				// leaving the lines emerging from under each disc.
				// Spokes are drawn only for nodes at roughly screen scale or below.
				// A node far larger than the viewport has its children spread beyond
				// the edges, and its spokes would be long lines crossing everything
				// rather than a readable tree.
				if (r >= MIN_SPOKES && r <= Math.min(width, height) * 0.6) {
					drawSpokes(node, sx, sy, r, colour, kids);
				}
				for (var i = 0; i < kids.length; i++) drawNode(kids[i], visible);
			}
		}
	}

	function drawFields(node, sx, sy, r) {
		var colour = colourFor(node);
		for (var i = 0; i < node.fields.length; i++) {
			var f = node.fields[i];
			var fx = sx + f.x * r;
			var fy = sy + f.y * r;
			var fr = f.r * r;

			ctx.beginPath();
			ctx.arc(fx, fy, fr, 0, Math.PI * 2);
			ctx.fillStyle = shade(colour, 0.16);
			ctx.fill();
			ctx.lineWidth = 0.8;
			ctx.strokeStyle = shade(colour, 0.45);
			ctx.stroke();

			if (deepDetail && fr >= MIN_DERIVED) {
				derive(fx, fy, fr * 0.8, hashSeed((node.hash || node.id) + ":" + i), 0);
			}
			if (fr < MIN_FIELD_TEXT) continue;
			ctx.fillStyle = ink(0.55);
			ctx.textAlign = "center";
			ctx.font = fontOf(Math.min(13, fr * 0.24));
			wrapInto(f.key, fx, fy - fr * 0.16, fr * 1.5, Math.min(13, fr * 0.24) * 1.2, 2);
			ctx.fillStyle = ink(0.95);
			ctx.font = "600 " + fontOf(Math.min(16, fr * 0.3));
			wrapInto(f.value, fx, fy + fr * 0.28, fr * 1.55, Math.min(16, fr * 0.3) * 1.2, 2);
		}
	}

	// drawRibbon renders the digest as an arc of per-byte segments. Two nodes with
	// the same hash produce the same ribbon, so shared content is recognisable by
	// eye without reading a single hex character.
	// drawSpokes renders the Merkle relationship one level deep: a hub carrying the
	// node's own digest, joined to each child it commits to. Containment alone says
	// only that these things are nested; the spokes say the parent's hash is made
	// of theirs, which is what distinguishes a Merkle DAG from any other hierarchy.
	function drawSpokes(node, sx, sy, r, colour, kids) {
		// A child nearly as large as its parent is a chain link — the previous
		// commit or transaction nested inside this one. Containment already reads
		// clearly there, and a spoke to its centre is just a long line across
		// everything, so only the genuinely contained parts get one.
		var parts = [];
		for (var n = 0; n < kids.length; n++) {
			if (kids[n].r <= node.r * 0.55) parts.push(kids[n]);
		}
		if (!parts.length) return;

		kids = parts;
		var hub = hubOf(node, sx, sy, r, kids);
		var hubR = Math.max(2.5, Math.min(9, r * 0.035));

		ctx.save();
		ctx.lineWidth = Math.max(0.6, Math.min(2, r * 0.006));
		ctx.strokeStyle = shade(colour, 0.34);
		for (var i = 0; i < kids.length; i++) {
			var k = kids[i];
			var kx = toScreenX(k.x), ky = toScreenY(k.y);
			ctx.beginPath();
			ctx.moveTo(hub.x, hub.y);
			ctx.lineTo(kx, ky);
			ctx.stroke();
		}
		ctx.restore();

		ctx.beginPath();
		ctx.arc(hub.x, hub.y, hubR, 0, Math.PI * 2);
		ctx.fillStyle = shade(colour, 0.95);
		ctx.fill();
		ctx.lineWidth = Math.max(0.6, hubR * 0.25);
		ctx.strokeStyle = ink(0.35);
		ctx.stroke();
	}

	// hubOf parks the digest hub in the widest gap along the node'\''s vertical axis,
	// so it lands in open space rather than on top of a child.
	function hubOf(node, sx, sy, r, kids) {
		var candidates = [
			{ x: sx, y: sy },
			{ x: sx, y: sy - r * 0.74 },
			{ x: sx, y: sy + r * 0.74 },
			{ x: sx - r * 0.74, y: sy },
			{ x: sx + r * 0.74, y: sy },
		];
		var best = candidates[0], bestGap = -Infinity;
		for (var c = 0; c < candidates.length; c++) {
			var gap = Infinity;
			for (var i = 0; i < kids.length; i++) {
				var k = kids[i];
				var d = Math.hypot(candidates[c].x - toScreenX(k.x), candidates[c].y - toScreenY(k.y)) - k.r * view.scale;
				if (d < gap) gap = d;
			}
			if (gap > bestGap) { bestGap = gap; best = candidates[c]; }
		}
		return best;
	}

	function drawRibbon(node, sx, sy, r, colour) {
		var hex = node.hash;
		if (!hex) return;

		var count = Math.min(14, Math.floor(hex.length / 2));
		if (count < 2) return;

		// Thickness is capped so the ribbon stays a texture on the rim rather than
		// growing into the loudest thing on screen, and it sits on the lower arc so
		// it never competes with the label along the top.
		var thickness = Math.min(5, Math.max(1.4, r * 0.03));
		var radius = r - thickness * 1.7;
		if (radius <= 0) return;

		var span = Math.PI * 0.6;
		var from = Math.PI / 2 - span / 2;

		ctx.lineWidth = thickness;
		for (var i = 0; i < count; i++) {
			var v = parseInt(hex.substr(i * 2, 2), 16);
			if (isNaN(v)) return;
			var a0 = from + (span * i) / count;
			var a1 = from + (span * (i + 0.74)) / count;
			ctx.beginPath();
			ctx.arc(sx, sy, radius, a0, a1);
			ctx.strokeStyle = "hsla(" + ((colour.h + v * 0.7 - 90) % 360).toFixed(1) +
				",48%," + (40 + (v / 255) * 34).toFixed(1) + "%,0.75)";
			ctx.stroke();
		}
	}

	// sfc32 with a hash-derived seed: the same digest always grows the same shape,
	// so this is a deterministic picture of the hash rather than decoration.
	function rng(seed) {
		var a = seed >>> 0, b = (seed ^ 0x9e3779b9) >>> 0;
		var c = (seed ^ 0x85ebca6b) >>> 0, d = (seed ^ 0xc2b2ae35) >>> 0;
		return function () {
			a >>>= 0; b >>>= 0; c >>>= 0; d >>>= 0;
			var t = (a + b) >>> 0;
			a = b ^ (b >>> 9);
			b = (c + (c << 3)) >>> 0;
			c = (c << 21) | (c >>> 11);
			c = (c + t) >>> 0;
			d = (d + 1) >>> 0;
			t = (t + d) >>> 0;
			return (t >>> 0) / 4294967296;
		};
	}

	// derive draws self-similar structure grown from a digest. Each disc holds a
	// ring of smaller discs and one at its centre, so whichever way the view
	// descends there is always more of it: the zoom never reaches a floor.
	function derive(sx, sy, r, seed, depth) {
		if (r < 4 || depth > 40) return;
		// Culling the whole disc at entry keeps the work proportional to what is on
		// screen, however deep the view has gone.
		if (sx + r < 0 || sx - r > width || sy + r < 0 || sy - r > height) return;

		var next = rng(seed);
		var arms = 3 + Math.floor(next() * 4);
		var turn = next() * Math.PI * 2;
		var childR = r * (0.26 + next() * 0.1);
		var ring = r - childR - r * 0.04;
		var hue = (seed % 360 + 360) % 360;

		for (var i = 0; i < arms; i++) {
			var angle = turn + (i * Math.PI * 2) / arms;
			paintDerived(sx + Math.cos(angle) * ring, sy + Math.sin(angle) * ring, childR, hue + i * 22);
			// Mixing the arm index back into the seed keeps every branch distinct
			// while the whole tree stays a pure function of the original digest.
			derive(sx + Math.cos(angle) * ring, sy + Math.sin(angle) * ring, childR,
				Math.imul(seed ^ (i + 1), 2246822519) >>> 0, depth + 1);
		}

		// The centre child is what makes the descent bottomless: zooming at the
		// middle of any disc always lands inside another one.
		var coreR = r * 0.36;
		paintDerived(sx, sy, coreR, hue + 180);
		derive(sx, sy, coreR, Math.imul(seed ^ 0x5bf03635, 2654435761) >>> 0, depth + 1);
	}

	function paintDerived(cx, cy, r, hue) {
		if (cx + r < 0 || cx - r > width || cy + r < 0 || cy - r > height) return;
		var h = ((hue % 360) + 360) % 360;
		ctx.beginPath();
		ctx.arc(cx, cy, r, 0, Math.PI * 2);
		// Below a few pixels the fill costs as much as the outline and adds nothing,
		// so the smallest discs are drawn as outline only.
		if (r > 7) {
			ctx.fillStyle = "hsla(" + h.toFixed(1) + ",58%,50%,0.07)";
			ctx.fill();
		}
		ctx.lineWidth = Math.max(0.4, Math.min(1.6, r * 0.03));
		ctx.strokeStyle = "hsla(" + h.toFixed(1) + ",62%,62%,0.42)";
		ctx.stroke();
	}

	function drawLinks(visible) {
		if (!scene.links.length) return;
		var shown = new Map();
		visible.forEach(function (v) { shown.set(v.node.id, v); });

		ctx.save();
		for (var i = 0; i < scene.links.length; i++) {
			var l = scene.links[i];
			var a = shown.get(l.from);
			var b = shown.get(l.to);
			if (!a || !b) continue;
			if (a.r < MIN_LINK || b.r < MIN_LINK) continue;

			var mark = markedLinks.get(l.from + " " + l.to);
			var dx = b.sx - a.sx, dy = b.sy - a.sy;
			var dist = Math.hypot(dx, dy);
			if (dist < 1) continue;

			// Bow the edge away from the straight line so it reads as a reference
			// rather than as containment.
			var mx = (a.sx + b.sx) / 2 - dy * 0.16;
			var my = (a.sy + b.sy) / 2 + dx * 0.16;

			ctx.beginPath();
			ctx.moveTo(a.sx, a.sy);
			ctx.quadraticCurveTo(mx, my, b.sx, b.sy);
			ctx.lineWidth = mark ? 2 : 0.8;
			ctx.setLineDash(mark ? [] : [3, 5]);
			ctx.strokeStyle = mark ? MARKS[mark] : ink(0.16);
			ctx.stroke();
		}
		ctx.restore();
		ctx.setLineDash([]);
	}

	function drawLabels(visible) {
		ctx.textAlign = "center";
		ctx.textBaseline = "middle";
		for (var i = 0; i < visible.length; i++) {
			var v = visible[i];
			if (v.r < MIN_LABEL) continue;
			var label = v.node.label || v.node.id;
			if (!label) continue;

			var kids = children.get(v.node.id);
			var hasVisibleKids = kids && kids.length && v.r >= MIN_RECURSE * 3;
			var reach = hasVisibleKids ? contentReach(v.node, v.r) : fieldsOnlyReach(v);
			if (reach <= 0) {
				// Nothing inside, so the label takes the middle.
				var size = Math.min(22, v.r * 0.3);
				if (size < 8) continue;
				ctx.font = fontOf(size);
				ctx.fillStyle = ink(0.9);
				wrapInto(label, v.sx, v.sy, v.r * 1.7, size * 1.2, 3);
				continue;
			}

			// A node with contents keeps its label in the ring between its own edge
			// and whatever it holds. Where that ring is too thin — a long chain of
			// nodes each barely larger than the last — the label is dropped rather
			// than stacked on top of its neighbours'.
			var ring = v.r - reach;
			var size = Math.min(18, v.r * 0.16, ring * 0.62);
			if (size < 9) continue;
			ctx.font = fontOf(size);
			ctx.fillStyle = ink(0.72);
			wrapInto(label, v.sx, v.sy - v.r + ring / 2, v.r * 1.5, size * 1.2, 1);
		}
	}

	var largestChild = new Map();
	var fieldReach = new Map();

	function largestChildRadius(node) {
		if (largestChild.has(node.id)) return largestChild.get(node.id);
		var kids = children.get(node.id) || [];
		var biggest = 0;
		for (var i = 0; i < kids.length; i++) biggest = Math.max(biggest, kids[i].r);
		largestChild.set(node.id, biggest);
		return biggest;
	}

	// How far the node's own contents reach, in screen pixels: whichever of its
	// children or its payload discs extends furthest from the centre.
	function contentReach(node, screenR) {
		var reach = largestChildRadius(node) * view.scale;
		if (!node.fields || !node.fields.length) return reach;
		if (node.fields[0].r * screenR < MIN_FIELD) return reach;

		if (!fieldReach.has(node.id)) {
			var far = 0;
			for (var i = 0; i < node.fields.length; i++) {
				var f = node.fields[i];
				far = Math.max(far, Math.hypot(f.x, f.y) + f.r);
			}
			fieldReach.set(node.id, far);
		}
		return Math.max(reach, fieldReach.get(node.id) * screenR);
	}

	function fieldsOnlyReach(v) {
		if (!v.node.fields || !v.node.fields.length) return 0;
		if (v.node.fields[0].r * v.r < MIN_FIELD) return 0;
		return contentReach(v.node, v.r);
	}

	function wrapInto(text, cx, cy, maxWidth, lineHeight, maxLines) {
		var words = String(text).split(/\s+/);
		var lines = [];
		var line = "";
		for (var i = 0; i < words.length; i++) {
			var candidate = line ? line + " " + words[i] : words[i];
			if (ctx.measureText(candidate).width > maxWidth && line) {
				lines.push(line);
				line = words[i];
				if (lines.length === maxLines) break;
			} else {
				line = candidate;
			}
		}
		if (lines.length < maxLines && line) lines.push(line);
		if (!lines.length) return;

		var last = lines.length - 1;
		while (ctx.measureText(lines[last]).width > maxWidth && lines[last].length > 1) {
			lines[last] = lines[last].slice(0, -2) + "…";
		}
		var top = cy - ((lines.length - 1) * lineHeight) / 2;
		for (var j = 0; j < lines.length; j++) {
			ctx.fillText(lines[j], cx, top + j * lineHeight);
		}
	}

	function fontOf(px) {
		return px.toFixed(1) + "px ui-sans-serif, system-ui, -apple-system, sans-serif";
	}

	function ink(alpha) {
		return dark.matches
			? "rgba(232, 237, 244, " + alpha + ")"
			: "rgba(16, 21, 28, " + alpha + ")";
	}

	function withAlpha(hex, alpha) {
		var n = parseInt(hex.slice(1), 16);
		return "rgba(" + ((n >> 16) & 255) + "," + ((n >> 8) & 255) + "," + (n & 255) + "," + alpha + ")";
	}

	// The deepest node containing the point wins, which matches what the eye
	// picks out: the innermost disc under the cursor.
	function hitTest(wx, wy) {
		var best = null;
		for (var i = 0; i < scene.nodes.length; i++) {
			var n = scene.nodes[i];
			if (n.r * view.scale < MIN_DRAW) continue;
			var dx = wx - n.x, dy = wy - n.y;
			if (dx * dx + dy * dy <= n.r * n.r) {
				if (!best || n.depth > best.depth) best = n;
			}
		}
		return best;
	}

	function ancestry(node) {
		var chain = [];
		var cur = node;
		while (cur) {
			chain.unshift(cur);
			cur = cur.parent ? byId.get(cur.parent) : null;
		}
		return chain;
	}

	function showTip(node, sx, sy) {
		var parts = ['<span class="kind">' + escapeHTML(node.kind || "node") + "</span>"];
		parts.push("<h2>" + escapeHTML(node.label || node.id) + "</h2>");
		if (node.hash) parts.push("<code>" + escapeHTML(node.hash.slice(0, 32)) + "</code>");
		if (node.fields && node.fields.length) {
			var rows = node.fields.map(function (f) {
				return "<tr><th>" + escapeHTML(f.key) + "</th><td>" + escapeHTML(f.value) + "</td></tr>";
			});
			parts.push("<table>" + rows.join("") + "</table>");
		}
		tip.innerHTML = parts.join("");
		tip.hidden = false;

		var box = tip.getBoundingClientRect();
		var x = Math.min(sx + 16, width - box.width - 10);
		var y = Math.min(sy + 16, height - box.height - 10);
		tip.style.left = Math.max(10, x) + "px";
		tip.style.top = Math.max(10, y) + "px";
	}

	function showCrumb(node) {
		if (!node) {
			crumb.hidden = true;
			return;
		}
		crumb.innerHTML = ancestry(node)
			.map(function (n, i, all) {
				var text = escapeHTML(n.label || n.id);
				return i === all.length - 1 ? "<b>" + text + "</b>" : text;
			})
			.join(' <span>›</span> ');
		crumb.hidden = false;
	}

	function escapeHTML(s) {
		return String(s).replace(/[&<>"']/g, function (c) {
			return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
		});
	}

	var focused = null;

	function focus(node) {
		focused = node;
		showCrumb(node);
		if (node) fitTo(node.x, node.y, node.r, true);
		else fitAll(true);
	}

	canvas.addEventListener("wheel", function (e) {
		e.preventDefault();
		if (animation) { cancelAnimationFrame(animation); animation = null; }

		// Zoom about the cursor: the world point under it must not move.
		var wx = toWorldX(e.offsetX);
		var wy = toWorldY(e.offsetY);
		var factor = Math.exp(-e.deltaY * (e.deltaMode === 1 ? 0.05 : 0.0016));
		view.scale = Math.max(1e-4, Math.min(1e9, view.scale * factor));
		view.x = wx - (e.offsetX - width / 2) / view.scale;
		view.y = wy - (e.offsetY - height / 2) / view.scale;
		draw();
	}, { passive: false });

	canvas.addEventListener("pointerdown", function (e) {
		canvas.setPointerCapture(e.pointerId);
		canvas.classList.add("dragging");
		pointer = { x: e.offsetX, y: e.offsetY, moved: false };
	});

	canvas.addEventListener("pointermove", function (e) {
		if (pointer) {
			var dx = e.offsetX - pointer.x;
			var dy = e.offsetY - pointer.y;
			if (Math.abs(dx) + Math.abs(dy) > 2) pointer.moved = true;
			view.x -= dx / view.scale;
			view.y -= dy / view.scale;
			pointer.x = e.offsetX;
			pointer.y = e.offsetY;
			tip.hidden = true;
			draw();
			return;
		}
		var hit = hitTest(toWorldX(e.offsetX), toWorldY(e.offsetY));
		if (hit) showTip(hit, e.offsetX, e.offsetY);
		else tip.hidden = true;
		if (hit !== hovered) {
			hovered = hit;
			if (!focused) showCrumb(hit);
		}
	});

	function endPointer(e) {
		if (!pointer) return;
		var wasClick = !pointer.moved;
		pointer = null;
		canvas.classList.remove("dragging");
		if (!wasClick) return;
		var hit = hitTest(toWorldX(e.offsetX), toWorldY(e.offsetY));
		if (hit) focus(hit);
	}
	canvas.addEventListener("pointerup", endPointer);
	canvas.addEventListener("pointercancel", function () { pointer = null; canvas.classList.remove("dragging"); });
	canvas.addEventListener("pointerleave", function () { tip.hidden = true; });

	window.addEventListener("keydown", function (e) {
		switch (e.key) {
			case "Escape":
			case "Backspace":
				e.preventDefault();
				focus(focused && focused.parent ? byId.get(focused.parent) : null);
				break;
			case "f":
				focused = null;
				showCrumb(null);
				fitAll(true);
				break;
			case "d":
				deepDetail = !deepDetail;
				draw();
				break;
			case "+":
			case "=":
				animateTo({ x: view.x, y: view.y, scale: view.scale * 1.8 });
				break;
			case "-":
				animateTo({ x: view.x, y: view.y, scale: view.scale / 1.8 });
				break;
		}
	});

	window.addEventListener("resize", resize);
	if (dark.addEventListener) dark.addEventListener("change", draw);

	// A small hook for embedding and automation: drive the camera directly, focus a
	// node by ID, refit the whole graph, or read the scene back.
	window.merkelbrot = {
		scene: scene,
		view: function (x, y, r) {
			if (animation) { cancelAnimationFrame(animation); animation = null; }
			fitTo(x, y, r, false);
		},
		focus: function (id, animate) {
			var node = byId.get(id);
			if (!node) return false;
			focused = node;
			showCrumb(node);
			fitTo(node.x, node.y, node.r, animate !== false);
			return true;
		},
		fit: function (animate) {
			focused = null;
			showCrumb(null);
			fitAll(animate !== false);
		},
	};

	resize();
	fitAll(false);
})();
