// communicate renderer — terminating decision/output from serf agents.
// The transcript builder usually consumes communicate to populate the decision
// block; this renderer is a fallback for mid-stream communicate calls.
(function () {
  const TR = window.ToolRenderers;

  TR.register("communicate", function (tc) {
    const msg = (tc.args && tc.args.message) || "";
    const out = (tc.args && tc.args.output) || null;
    // No inline preview in the summary — the body shows the full message, so a
    // truncated copy in the chip would just be redundant.
    const summary = TR.summarySpan([TR.statusBadge(tc), TR.badge("💬 Communicate")]);
    let body = null;
    if (msg || out) {
      body = TR.el("div", "tc-communicate");
      if (msg) {
        body.appendChild(TR.lineCapped(msg, 10, "tc-comm-text"));
      }
      if (out) {
        const details = TR.el("details", "tc-comm-output");
        const summaryEl = document.createElement("summary");
        summaryEl.textContent = "structured output";
        details.appendChild(summaryEl);
        details.appendChild(window.GenericRenderer.renderValue(out));
        body.appendChild(details);
      }
    }
    return TR.makeCard("communicate", summary, body, {});
  });
})();
