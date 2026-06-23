// grep renderer — pattern + path + match listing. Lace-inspired:
//   - empty result renders "(no matches)" instead of a silently empty body
//   - path has full-path tooltip
(function () {
  const TR = window.ToolRenderers;

  TR.register("grep", function (tc) {
    const pattern = (tc.args && tc.args.pattern) || "";
    const path = (tc.args && tc.args.path) || ".";
    const summary = TR.summarySpan([
      TR.statusBadge(tc),
      TR.badge("🔍 Grep"),
      " ",
    ]);
    const patSpan = TR.el("code", "tc-grep-pattern");
    patSpan.textContent = '"' + pattern + '"';
    summary.appendChild(patSpan);
    const inSpan = TR.el("span", "tc-path");
    inSpan.textContent = " in " + path;
    inSpan.title = path;
    summary.appendChild(inSpan);

    let body = null;
    if (tc.result) {
      const content = TR.decodeContent(tc.result.content);
      const lineCount = content ? content.split("\n").filter(l => l.length > 0).length : 0;
      summary.appendChild(document.createTextNode(" "));
      summary.appendChild(TR.badge(lineCount + " matches", "tc-badge-muted"));
      if (lineCount === 0) {
        body = TR.el("span", "tc-empty");
        body.textContent = "(no matches)";
      } else {
        body = TR.lineCapped(content, 10, "tc-grep-results");
      }
    }
    return TR.makeCard("grep", summary, body, {
      isError: !!(tc.result && tc.result.is_error),
    });
  });
})();
