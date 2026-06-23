// spawn_agent renderer — subagent dispatch (agent_type, model, task description).
(function () {
  const TR = window.ToolRenderers;

  TR.register("spawn_agent", function (tc) {
    const desc = (tc.args && tc.args.task) || "(no task)";
    const agent = (tc.args && tc.args.agent_type) || "agent";
    const model = (tc.args && tc.args.model) || "";
    const summary = TR.summarySpan([
      TR.statusBadge(tc),
      TR.badge("🤖 Dispatch"),
      " " + agent + (model ? " (" + model + ")" : "") + ": ",
    ]);
    const descSpan = TR.el("span", "tc-task-desc");
    descSpan.textContent = '"' + (desc.length > 120 ? desc.slice(0, 120) + "…" : desc) + '"';
    summary.appendChild(descSpan);

    let body = null;
    if (tc.result) {
      body = TR.el("div", "tc-task-result");
      const content = TR.decodeContent(tc.result.content);
      try {
        const parsed = JSON.parse(content);
        body.appendChild(window.GenericRenderer.renderValue(parsed));
      } catch {
        body.appendChild(TR.lineCapped(content, 10, "tc-task-text"));
      }
    }
    return TR.makeCard("spawn", summary, body, {
      isError: !!(tc.result && tc.result.is_error),
    });
  });
})();
