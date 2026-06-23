function parseArrayEnv(name) {
  const raw = process.env[name];
  if (!raw) return [];
  try {
    const parsed = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed : [];
  } catch {
    return [];
  }
}

function normalizeStory(story) {
  if (!story || typeof story !== "object") return {};
  return {
    ...story,
    id: typeof story.id === "string" ? story.id : String(story.id || ""),
    title: typeof story.title === "string" ? story.title : "",
    status: typeof story.status === "string" ? story.status : "",
    scope: typeof story.scope === "string" ? story.scope : "",
    lifecycle: typeof story.lifecycle === "string" ? story.lifecycle : "",
  };
}

function dedupeById(stories) {
  const seen = new Set();
  const out = [];
  for (const raw of stories) {
    const story = normalizeStory(raw);
    if (!story.id) continue;
    if (seen.has(story.id)) continue;
    seen.add(story.id);
    out.push(story);
  }
  return out;
}

function isImplemented(story) {
  const status = String(story.status || "").toLowerCase();
  return status === "implemented" || status === "done" || status === "complete" || status === "completed";
}

function isContext(story) {
  const scope = String(story.scope || "").toLowerCase();
  return scope === "context";
}

const provided = parseArrayEnv("STORIES");
const providedContext = parseArrayEnv("STORIES_CONTEXT");

const inScope = [];
const context = [...providedContext];

for (const raw of provided) {
  const story = normalizeStory(raw);
  if (!story.id) continue;
  if (isContext(story) || isImplemented(story)) {
    context.push(story);
  } else {
    inScope.push(story);
  }
}

const stories = dedupeById(inScope);
const stories_context = dedupeById(context.filter((s) => !stories.find((x) => x.id === s.id)));
const relevant = stories.map((s) => s.id);

const specDriven = stories.length === 0;

const components = [
  {
    id: "implementation",
    name: specDriven ? "Spec Implementation" : "Sprint Implementation",
    spec_slice: specDriven
      ? "Implement everything described in the spec."
      : "Implements in-scope stories for this sprint.",
    relevant_stories: relevant,
    depends_on: [],
    interfaces: { consumes: [], exposes: [] },
  },
];

const message = specDriven
  ? "Scoped components: spec-driven (no stories)."
  : `Scoped components: ${stories.length} in-scope stories, ${stories_context.length} context stories.`;

const output = {
  decision: "scoped",
  message,
  data: { components, stories, stories_context },
  artifacts: [],
};
process.stdout.write(JSON.stringify(output));
