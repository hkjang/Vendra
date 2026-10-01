// Runs vitest on an interpreter this script names, instead of whichever `node`
// the PATH happens to hand it.
//
// `node` is not a safe thing to inherit inside an npm script. Before npm runs a
// script it walks from the package directory up to the filesystem root and
// unshifts *every* node_modules/.bin it finds onto the front of PATH
// (@npmcli/run-script/lib/set-path.js). A machine that has ever installed a
// package depending on `node-bin-gen` — playwright wrappers and several CLI
// tools do — therefore has a node_modules/.bin/node somewhere above the
// checkout, and that symlink shadows the node the developer actually chose.
// vitest's bin starts with `#!/usr/bin/env node`, so the whole suite starts on
// the shadowing copy.
//
// What that looks like when it happens is not a version complaint. jsdom
// declares `engines: ^22.22.2 || ...` and reaches undici, which calls
// `worker_threads.markAsUncloneable` — absent before Node 22. So every test
// file dies in its own fork worker with `webidl.util.markAsUncloneable is not a
// function`, and the run ends with "Test Files no tests / Errors 22 errors": a
// hundred lines of stack naming undici and jsdom, none of them naming the
// interpreter that is the only thing wrong. The dependency tree is fine — the
// same lockfile passes 22/22 the moment the interpreter is named.
//
// npm itself knows which node the developer chose: it exports its own execPath
// as both npm_node_execpath and NODE for every script it runs
// (@npmcli/config/lib/set-envs.js). That is the value to trust, not PATH. If it
// is too old we say so in one line carrying the path and the version, rather
// than starting a run that cannot work.
//
// This wrapper is loaded by the shadowing node too, so it has to stay on ground
// that the old node covers — it touches no jsdom and no undici, only
// child_process.

import { spawnSync } from "node:child_process";
import { createRequire } from "node:module";
import { readFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const packageRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");

// Thrown rather than exited on, so the one line we have to say is flushed before
// the process goes away. process.exit() drops whatever is still buffered when
// stderr is a pipe, which is exactly how CI reads us.
class Unrunnable extends Error {}

function resolveInterpreter(minimumMajor) {
  // process.execPath is the last resort rather than the first choice: when npm
  // is the caller it is the shadowing node, the very value we are here to stop
  // trusting. It only applies to someone invoking this script by hand.
  const interpreter = process.env.npm_node_execpath || process.env.NODE || process.execPath;

  const reported = spawnSync(interpreter, ["-v"], { encoding: "utf8" });
  if (reported.error || reported.status !== 0) {
    throw new Unrunnable(
      `web tests need Node >= ${minimumMajor}; resolved ${interpreter}, which did not answer -v`,
    );
  }
  const version = reported.stdout.trim();
  const major = /^v?(\d+)\./.exec(version);
  if (!major || Number(major[1]) < minimumMajor) {
    throw new Unrunnable(
      `web tests need Node >= ${minimumMajor}; resolved ${interpreter} (${version})`,
    );
  }
  return interpreter;
}

function resolveMinimumMajor() {
  // engines.node stays the single place the floor is written down, so bumping it
  // there moves this guard with it instead of leaving the two to disagree.
  const manifest = JSON.parse(readFileSync(path.join(packageRoot, "package.json"), "utf8"));
  const declared = manifest.engines?.node ?? "";
  const found = /(\d+)/.exec(declared);
  if (!found) {
    throw new Unrunnable(
      'web/package.json must declare engines.node (e.g. ">=22") for the test runner to check against',
    );
  }
  return Number(found[1]);
}

function resolveVitestBin() {
  // Resolved through vitest's own manifest so the path follows the installed
  // package instead of being spelled out here. The bin is handed to node as an
  // argument, which turns its `#!/usr/bin/env node` line into a comment — the
  // whole point, since that line is what picked the wrong interpreter.
  const require = createRequire(import.meta.url);
  let manifestPath;
  try {
    manifestPath = require.resolve("vitest/package.json");
  } catch {
    throw new Unrunnable("vitest is not installed; run `npm ci` in web/");
  }
  const bin = JSON.parse(readFileSync(manifestPath, "utf8")).bin;
  const entry = typeof bin === "string" ? bin : bin?.vitest;
  if (!entry) {
    throw new Unrunnable("vitest is installed but publishes no vitest bin; run `npm ci` in web/");
  }
  return path.resolve(path.dirname(manifestPath), entry);
}

try {
  const interpreter = resolveInterpreter(resolveMinimumMajor());
  const vitestBin = resolveVitestBin();

  // Arguments pass straight through, so `npm test -- --coverage` and
  // `npm run test:watch -- src/session.test.ts` keep working unchanged. cwd is
  // pinned to the package so vitest finds the same config whether npm invoked
  // us from web/ or a person invoked us from the repository root.
  const run = spawnSync(interpreter, [vitestBin, ...process.argv.slice(2)], {
    stdio: "inherit",
    cwd: packageRoot,
  });
  if (run.error) {
    throw new Unrunnable(`could not start vitest on ${interpreter}: ${run.error.message}`);
  }
  process.exitCode = run.status ?? 1;
} catch (err) {
  if (!(err instanceof Unrunnable)) {
    throw err;
  }
  console.error(err.message);
  process.exitCode = 1;
}
