// read_file renderer — file contents preview. Lace-inspired:
//   - summary shows path with full-path tooltip for hover (path may be long)
//   - line-range form "(lines start-end)" when the call carries offset/limit
//   - whole-file reads get a muted "N lines" chip once the result arrives
(function () {
  const TR = window.ToolRenderers;

  TR.register("read_file", function (tc) {
    const path = (tc.args && tc.args.file_path) || "(unknown path)";
    const offset = tc.args && tc.args.offset;
    const limit = tc.args && tc.args.limit;
    const hasRange = !!(offset || limit);

    const pathSpan = TR.el("span", "tc-path");
    pathSpan.textContent = path;
    pathSpan.title = path; // hover reveals full path when truncated by container

    const summary = TR.summarySpan([
      TR.statusBadge(tc),
      TR.badge("📖 Read"),
      " ",
      pathSpan,
    ]);
    if (hasRange) {
      const start = offset || 1;
      const end = start + (limit || 0) - 1;
      summary.appendChild(document.createTextNode(" "));
      summary.appendChild(TR.badge("lines " + start + "–" + end, "tc-badge-muted"));
    }

    let body = null;
    if (tc.result && !tc.result.is_error) {
      const content = TR.decodeContent(tc.result.content);
      body = TR.visualClamp(content, 3, "tc-read-content language-" + TR.langFromExt(path));
      if (!hasRange && content) {
        const lineCount = content.split("\n").length;
        summary.appendChild(document.createTextNode(" "));
        summary.appendChild(TR.badge(lineCount + " lines", "tc-badge-muted"));
      }
    } else if (tc.result && tc.result.is_error) {
      body = TR.el("pre", "tc-error-content");
      body.textContent = TR.decodeContent(tc.result.content);
    }
    return TR.makeCard("read", summary, body, {
      isError: !!(tc.result && tc.result.is_error),
    });
  });
})();
