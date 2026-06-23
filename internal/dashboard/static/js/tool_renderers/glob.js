// glob renderer — pattern + matched filenames. Lace-inspired:
//   - empty result renders "(no matches)" instead of a silently empty body
(function () {
  const TR = window.ToolRenderers;

  TR.register("glob", function (tc) {
    const pattern = (tc.args && tc.args.pattern) || "";
    const summary = TR.summarySpan([
      TR.statusBadge(tc),
      TR.badge("🔍 Glob"),
      " ",
    ]);
    const patSpan = TR.el("code", "tc-grep-pattern");
    patSpan.textContent = '"' + pattern + '"';
    summary.appendChild(patSpan);

    let body = null;
    if (tc.result) {
      const content = TR.decodeContent(tc.result.content);
      const fileCount = content ? content.split("\n").filter(l => l.length > 0).length : 0;
      summary.appendChild(document.createTextNode(" "));
      summary.appendChild(TR.badge(fileCount + " files", "tc-badge-muted"));
      if (fileCount === 0) {
        body = TR.el("span", "tc-empty");
        body.textContent = "(no matches)";
      } else {
        body = TR.lineCapped(content, 10, "tc-glob-results");
      }
    }
    return TR.makeCard("glob", summary, body, {
      isError: !!(tc.result && tc.result.is_error),
    });
  });
})();
