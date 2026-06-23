// write_file renderer — full file contents being written. Lace-inspired:
//   - summary path has full-path tooltip
//   - line count chip stays — it's the most informative size signal we have
(function () {
  const TR = window.ToolRenderers;

  TR.register("write_file", function (tc) {
    const path = (tc.args && tc.args.file_path) || "(unknown path)";
    const content = (tc.args && tc.args.content) || "";
    const lineCount = content ? content.split("\n").length : 0;
    const pathSpan = TR.el("span", "tc-path");
    pathSpan.textContent = path;
    pathSpan.title = path;
    const summary = TR.summarySpan([
      TR.statusBadge(tc),
      TR.badge("✎ Write"),
      " ",
      pathSpan,
      " ",
      TR.badge(lineCount + " lines", "tc-badge-muted"),
    ]);
    let body = null;
    if (content) {
      body = TR.lineCapped(content, 10, "tc-write-content language-" + TR.langFromExt(path));
    } else if (tc.result && tc.result.is_error) {
      body = TR.el("pre", "tc-error-content");
      body.textContent = TR.decodeContent(tc.result.content);
    }
    const isError = !!(tc.result && tc.result.is_error);
    return TR.makeCard("write", summary, body, { isError });
  });
})();
