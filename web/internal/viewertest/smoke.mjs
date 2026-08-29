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
import { chromium } from "playwright";

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

	check(source.name, errors.length === 0, errors.join(" | "));
	await page.close();
}

await browser.close();

if (failures.length) {
	console.error("viewer smoke test failed:");
	for (const f of failures) console.error("  " + f);
	process.exit(1);
}
console.log(`viewer smoke test passed across ${sources.length} sources`);
