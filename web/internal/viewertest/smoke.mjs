// Smoke test for the viewer script.
//
// Everything else about the page is covered by Go tests, which check that the
// markup, the styles, the script and the scene all arrive in one file. None of
// them run the script, so a syntax error or a null dereference in the viewer
// ships silently — the page loads, the canvas stays blank, and no test fails.
// This exports a page for each example source, opens it in a real browser and
// drives it through the range of zoom the viewer is built for, failing on any
// error the page reports along the way.
//
// Run it with: node smoke.mjs

import { execFileSync } from "node:child_process";
import { mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, dirname, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium, devices } from "playwright";

const webDir = resolve(dirname(fileURLToPath(import.meta.url)), "../..");
const repoDir = resolve(webDir, "..");
const out = mkdtempSync(join(tmpdir(), "merkelbrot-smoke-"));

const failures = [];
function check(name, ok, detail) {
	if (!ok) failures.push(detail ? `${name}: ${detail}` : name);
}

// Each source exercises a different shape: a generated DAG with merges, a
// transaction chain, and a real repository read off disk.
const sources = [
	{ name: "synthetic", args: ["--source", "synthetic", "-n", "14"] },
	{ name: "ledger", args: ["--source", "ledger", "-n", "12"] },
	{ name: "git", args: ["--source", "git", "--repo", repoDir] },
];

function exportPage(source) {
	const html = execFileSync(
		"go",
		["run", "./cmd/merkelbrot", "export", ...source.args],
		{ cwd: webDir, maxBuffer: 64 * 1024 * 1024, encoding: "utf8" },
	);
	const path = join(out, source.name + ".html");
	writeFileSync(path, html);
	return path;
}

// A frame that drew nothing looks exactly like a frame that crashed before it
// started, so the canvas is sampled rather than trusted.
async function painted(page) {
	return page.evaluate(() => {
		const c = document.getElementById("view");
		const ctx = c.getContext("2d");
		const { data } = ctx.getImageData(0, 0, c.width, c.height);
		const first = [data[0], data[1], data[2]];
		for (let i = 4; i < data.length; i += 4) {
			if (data[i] !== first[0] || data[i + 1] !== first[1] || data[i + 2] !== first[2]) {
				return true;
			}
		}
		return false;
	});
}

const browser = await chromium.launch({
	executablePath: process.env.CHROMIUM_PATH || undefined,
	args: ["--no-sandbox"],
});

for (const source of sources) {
	try {
		await run(source);
	} catch (e) {
		// A page broken badly enough stops answering evaluate calls at all, which
		// is a failure of this source rather than of the run.
		check(source.name, false, String(e && e.message ? e.message : e));
	}
}

async function run(source) {
	const path = exportPage(source);
	const page = await browser.newPage({ viewport: { width: 1100, height: 660 } });
	const errors = [];
	page.on("pageerror", (e) => errors.push(String(e.message)));
	page.on("console", (m) => {
		if (m.type() === "error") errors.push("console: " + m.text());
	});

	await page.goto("file://" + path);
	await page.waitForTimeout(500);

	const scene = await page.evaluate(() => {
		const api = window.merkelbrot;
		if (!api || !api.scene) return null;
		return { nodes: api.scene.nodes.length, links: api.scene.links.length };
	});
	check(source.name, scene !== null, "the page exposes no viewer API");
	check(source.name, scene && scene.nodes > 0, "the scene reached the page empty");
	check(source.name, await painted(page), "the first frame drew nothing");

	// The descent the viewer exists for: whole graph, into a node, into a field,
	// and past the data floor into derived detail.
	const stops = await page.evaluate(() => {
		const s = window.merkelbrot.scene;
		const leaves = s.nodes.filter((n) => n.leaf && n.fields && n.fields.length);
		if (!leaves.length) return null;
		const deepest = Math.max(...leaves.map((n) => n.depth));
		const leaf = leaves.filter((n) => n.depth === deepest).sort((a, b) => b.r - a.r)[0];
		const f = leaf.fields[0];
		const fx = leaf.x + f.x * leaf.r, fy = leaf.y + f.y * leaf.r, fr = f.r * leaf.r;
		return [
			{ x: leaf.x, y: leaf.y, r: leaf.r },
			{ x: fx, y: fy, r: fr },
			{ x: fx, y: fy, r: fr * 1e-2 },
			{ x: fx, y: fy, r: fr * 1e-4 },
		];
	});
	check(source.name, stops !== null, "no leaf carries a payload to zoom into");
	for (const stop of stops || []) {
		await page.evaluate(([x, y, r]) => window.merkelbrot.view(x, y, r), [stop.x, stop.y, stop.r]);
		await page.waitForTimeout(120);
		check(source.name, await painted(page), `drew nothing at radius ${stop.r}`);
	}

	// Pointing at a node redraws with its own references picked out, which is a
	// path through the drawing code that plain zooming never takes.
	await page.evaluate(() => window.merkelbrot.fit());
	await page.waitForTimeout(120);
	await page.mouse.move(550, 330);
	await page.mouse.move(551, 331);
	await page.waitForTimeout(120);
	check(source.name, await painted(page), "drew nothing after a hover");

	// Toggling derived detail off and on again exercises the deepest branch.
	await page.keyboard.press("d");
	await page.keyboard.press("d");
	await page.waitForTimeout(120);

	// Find a node by its label and fly to it, which is the one part of the page
	// that takes typed input.
	const target = await page.evaluate(() => {
		const n = window.merkelbrot.scene.nodes.find((x) => x.label && x.label.length > 2);
		return n ? n.label : null;
	});
	check(source.name, target !== null, "no node carries a label to search for");
	if (target) {
		await page.keyboard.press("/");
		await page.waitForTimeout(80);
		await page.keyboard.type(target.slice(0, 6));
		await page.waitForTimeout(150);
		const hits = await page.evaluate(() => document.querySelectorAll("#find-list li").length);
		check(source.name, hits > 0, `searching for ${JSON.stringify(target.slice(0, 6))} found nothing`);
		await page.keyboard.press("Enter");
		await page.waitForTimeout(400);
		const closed = await page.evaluate(() => document.getElementById("find").hidden);
		check(source.name, closed, "the find box stayed open after a choice");
		check(source.name, await painted(page), "drew nothing after flying to a match");
	}

	// The arrows are the only way through the graph without a pointer.
	await page.evaluate(() => window.merkelbrot.fit(false));
	await page.waitForTimeout(120);
	const trail = [];
	for (const key of ["ArrowDown", "ArrowDown", "ArrowRight", "ArrowUp"]) {
		await page.keyboard.press(key);
		await page.waitForTimeout(180);
		trail.push(await page.evaluate(() => {
			const c = document.getElementById("crumb");
			return c.hidden ? null : c.textContent.trim();
		}));
	}
	check(source.name, trail[0] !== null, "the first arrow selected nothing");
	check(source.name, new Set(trail).size > 1, `the arrows never moved: ${JSON.stringify(trail)}`);
	check(source.name, (await page.evaluate(() => window.scrollY)) === 0, "an arrow scrolled the page");

	// The picture is a canvas, so without a description a screen reader has nothing
	// at all to go on.
	const described = await page.evaluate(() => {
		const c = document.getElementById("view");
		return { role: c.getAttribute("role"), label: c.getAttribute("aria-label") || "" };
	});
	check(source.name, described.role === "img", "the canvas has no role");
	check(source.name, /\d+ nodes/.test(described.label), `the canvas description says nothing useful: ${JSON.stringify(described.label)}`);

	check(source.name, errors.length === 0, errors.join(" | "));
	await page.close();
}

// Flying across the graph is animated, which a viewer who has asked for less
// motion should not be given.
try {
	await stillness();
} catch (e) {
	check("reduced motion", false, String(e && e.message ? e.message : e));
}

async function stillness() {
	const path = exportPage(sources[0]);
	const settles = async (reduce) => {
		const context = await browser.newContext({ reducedMotion: reduce ? "reduce" : "no-preference" });
		const page = await context.newPage();
		await page.goto("file://" + path);
		await page.waitForTimeout(400);
		const still = await page.evaluate(() => {
			const c = document.getElementById("view");
			const sample = () => {
				const d = c.getContext("2d").getImageData(0, 0, c.width, c.height).data;
				let h = 0;
				for (let i = 0; i < d.length; i += 997) h = (h * 31 + d[i]) >>> 0;
				return h;
			};
			const s = window.merkelbrot.scene;
			const node = s.nodes.find((n) => n.depth === 3) || s.nodes[1];
			window.merkelbrot.focus(node.id, true);
			const before = sample();
			return new Promise((res) => setTimeout(() => res(before === sample()), 80));
		});
		await context.close();
		return still;
	};

	check("reduced motion", (await settles(true)) === true, "a flight was still animating despite the preference");
	check("reduced motion", (await settles(false)) === false, "nothing animated even without the preference, so the check proves nothing");
}

// Touch has no wheel, and the page turns the browser's own gestures off, so a
// pinch is the only way to zoom on a phone. It is worth its own check because
// nothing else on the page exercises a second pointer.
try {
	await pinches();
} catch (e) {
	check("pinch", false, String(e && e.message ? e.message : e));
}

async function pinches() {
	const path = exportPage(sources[0]);
	const context = await browser.newContext({ ...devices["Pixel 7"] });
	const page = await context.newPage();
	const errors = [];
	page.on("pageerror", (e) => errors.push(String(e.message)));

	await page.goto("file://" + path);
	await page.waitForTimeout(500);

	// How much of the canvas is painted stands in for how far in the view is.
	const painting = () =>
		page.evaluate(() => {
			const c = document.getElementById("view");
			const { data } = c.getContext("2d").getImageData(0, 0, c.width, c.height);
			let lit = 0;
			for (let i = 0; i < data.length; i += 4) {
				if (data[i] + data[i + 1] + data[i + 2] > 90) lit++;
			}
			return lit;
		});

	const cdp = await context.newCDPSession(page);
	const touch = (type, points) =>
		cdp.send("Input.dispatchTouchEvent", {
			type,
			touchPoints: points.map((p, id) => ({ x: p.x, y: p.y, id })),
		});

	const before = await painting();
	await touch("touchStart", [{ x: 150, y: 300 }, { x: 250, y: 400 }]);
	for (let k = 1; k <= 6; k++) {
		await touch("touchMove", [
			{ x: 150 - k * 12, y: 300 - k * 12 },
			{ x: 250 + k * 12, y: 400 + k * 12 },
		]);
		await page.waitForTimeout(50);
	}
	await touch("touchEnd", []);
	await page.waitForTimeout(250);
	const after = await painting();

	check("pinch", after > before * 1.2, `spreading two fingers did not zoom in (${before} then ${after})`);
	check("pinch", errors.length === 0, errors.join(" | "));
	await context.close();
}

await browser.close();

if (failures.length) {
	console.error("viewer smoke test failed:");
	for (const f of failures) console.error("  " + f);
	process.exit(1);
}
console.log(`viewer smoke test passed across ${sources.length} sources`);
