// task_list renderer — serf agents' internal task tracking.
//
// Shapes seen in real runs:
//   action: "append" | "update" | "view"
//   tasks:   [{ type, description, prompt, reasoning_effort, depends_on:[ints] }]
//   updates: [{ id, status, notes, reasoning_effort, depends_on:[ints] }]
//
// Render as a human-readable checklist, not a JSON blob.
(function () {
  const TR = window.ToolRenderers;

  function typeChip(type) {
    if (!type) return null;
    return TR.badge(type, "tc-task-type tc-task-type-" + type);
  }

  function effortChip(effort) {
    if (!effort) return null;
    return TR.badge("effort: " + effort, "tc-badge-muted");
  }

  function statusChip(status) {
    if (!status) return null;
    return TR.badge(status, "tc-task-status tc-task-status-" + status);
  }

  function depsLine(deps) {
    if (!deps || !deps.length) return null;
    const span = TR.el("span", "tc-task-deps");
    span.textContent = "depends on " + deps.join(", ");
    return span;
  }

  function renderTask(t, idx) {
    const row = TR.el("li", "tc-task");
    const head = TR.el("div", "tc-task-head");
    const num = TR.el("span", "tc-task-num");
    num.textContent = (idx + 1) + ".";
    head.appendChild(num);
    const tc = typeChip(t.type);
    if (tc) head.appendChild(tc);
    const desc = TR.el("span", "tc-task-desc");
    desc.textContent = t.description || "(no description)";
    head.appendChild(desc);
    row.appendChild(head);

    const meta = TR.el("div", "tc-task-meta");
    const ec = effortChip(t.reasoning_effort);
    if (ec) meta.appendChild(ec);
    const dl = depsLine(t.depends_on);
    if (dl) {
      if (ec) meta.appendChild(document.createTextNode(" "));
      meta.appendChild(dl);
    }
    if (meta.childNodes.length) row.appendChild(meta);

    if (t.prompt) {
      const body = TR.el("div", "tc-task-prompt");
      body.textContent = t.prompt;
      row.appendChild(body);
    }
    return row;
  }

  function renderUpdate(u) {
    const row = TR.el("li", "tc-task tc-task-update");
    const head = TR.el("div", "tc-task-head");
    const num = TR.el("span", "tc-task-num");
    num.textContent = "#" + (u.id != null ? u.id : "?");
    head.appendChild(num);
    const sc = statusChip(u.status);
    if (sc) head.appendChild(sc);
    // Prefer the resolved description from the original append; fall back to
    // an inline description if present on the update itself.
    const resolved = u._resolved;
    const tc = typeChip(resolved && resolved.type);
    if (tc) head.appendChild(tc);
    const descText = (resolved && resolved.description) || u.description;
    if (descText) {
      const desc = TR.el("span", "tc-task-desc");
      desc.textContent = descText;
      head.appendChild(desc);
    }
    row.appendChild(head);

    const meta = TR.el("div", "tc-task-meta");
    const ec = effortChip(u.reasoning_effort);
    if (ec) meta.appendChild(ec);
    const dl = depsLine(u.depends_on);
    if (dl) {
      if (ec) meta.appendChild(document.createTextNode(" "));
      meta.appendChild(dl);
    }
    if (meta.childNodes.length) row.appendChild(meta);

    if (u.notes) {
      const body = TR.el("div", "tc-task-prompt");
      body.textContent = u.notes;
      row.appendChild(body);
    }
    return row;
  }

  TR.register("task_list", function (tc) {
    const action = (tc.args && tc.args.action) || "(action)";
    const tasks = (tc.args && tc.args.tasks) || [];
    const updates = (tc.args && tc.args.updates) || [];
    const count = tasks.length + updates.length;

    const summary = TR.summarySpan([
      TR.statusBadge(tc),
      TR.badge("📋 Task list"),
      " " + action,
    ]);
    if (count) {
      summary.appendChild(document.createTextNode(" "));
      summary.appendChild(TR.badge(count + (count === 1 ? " item" : " items"), "tc-badge-muted"));
    }

    let body = null;
    if (tasks.length || updates.length) {
      body = TR.el("ol", "tc-tasklist");
      tasks.forEach((t, i) => body.appendChild(renderTask(t, i)));
      updates.forEach((u) => body.appendChild(renderUpdate(u)));
    }
    return TR.makeCard("tasklist", summary, body, {});
  });
})();
