#!/usr/bin/env node
// Test scope_components.js output when stories are absent or empty.
//
// Usage: node scope_components_test.js

const { execFileSync } = require("child_process");
const path = require("path");

const script = path.join(__dirname, "scope_components.js");
let failures = 0;

function run(env) {
  const result = execFileSync("node", [script], {
    env: { ...process.env, ...env },
    encoding: "utf-8",
  });
  return JSON.parse(result);
}

function assert(label, actual, expected) {
  if (actual !== expected) {
    console.error(`FAIL: ${label}`);
    console.error(`  expected: ${JSON.stringify(expected)}`);
    console.error(`  actual:   ${JSON.stringify(actual)}`);
    failures++;
  } else {
    console.log(`PASS: ${label}`);
  }
}

// Test 1: No STORIES env var at all
{
  const out = run({ STORIES: undefined, STORIES_CONTEXT: undefined });
  assert(
    "no stories — components has one entry",
    out.data.components.length,
    1
  );
  assert(
    "no stories — relevant_stories is empty",
    out.data.components[0].relevant_stories.length,
    0
  );
  assert(
    "no stories — spec_slice reflects spec-driven mode",
    out.data.components[0].spec_slice,
    "Implement everything described in the spec."
  );
  assert(
    "no stories — message reflects no stories",
    out.message,
    "Scoped components: spec-driven (no stories)."
  );
}

// Test 2: STORIES is JSON null (how buildShellEnv encodes nil)
{
  const out = run({ STORIES: "null", STORIES_CONTEXT: undefined });
  assert(
    "null stories — spec_slice reflects spec-driven mode",
    out.data.components[0].spec_slice,
    "Implement everything described in the spec."
  );
}

// Test 3: STORIES is empty array
{
  const out = run({ STORIES: "[]", STORIES_CONTEXT: undefined });
  assert(
    "empty stories — spec_slice reflects spec-driven mode",
    out.data.components[0].spec_slice,
    "Implement everything described in the spec."
  );
}

// Test 4: With actual stories — should keep existing behavior
{
  const stories = JSON.stringify([
    { id: "s1", title: "Story 1", status: "todo" },
    { id: "s2", title: "Story 2", status: "todo" },
  ]);
  const out = run({ STORIES: stories, STORIES_CONTEXT: undefined });
  assert(
    "with stories — relevant_stories populated",
    out.data.components[0].relevant_stories.length,
    2
  );
  assert(
    "with stories — spec_slice is story-driven",
    out.data.components[0].spec_slice,
    "Implements in-scope stories for this sprint."
  );
  assert(
    "with stories — message shows story count",
    out.message,
    "Scoped components: 2 in-scope stories, 0 context stories."
  );
}

if (failures > 0) {
  console.error(`\n${failures} test(s) failed`);
  process.exit(1);
} else {
  console.log(`\nAll tests passed`);
}
