// edit_file renderer — unified-diff hunks. Lace-inspired:
//   - summary shows path with full-path tooltip
//   - separate +N/-M counts in the summary instead of an aggregate
(function () {
  const TR = window.ToolRenderers;

  TR.register("edit_file", function (tc) {
    const path = (tc.args && tc.args.file_path) || "(unknown path)";
    const pathSpan = TR.el("span", "tc-path");
    pathSpan.textContent = path;
    pathSpan.title = path;
    const summary = TR.summarySpan([
      TR.statusBadge(tc),
      TR.badge("✎ Edit"),
      " ",
      pathSpan,
    ]);
    let body = null;
    if (tc.diff && tc.diff.hunks && tc.diff.hunks.length > 0) {
      body = TR.el("div", "tc-diff");
      let added = 0;
      let removed = 0;
      for (const h of tc.diff.hunks) {
        const hd = TR.el("div", "tc-diff-header");
        hd.textContent = "@@ -" + h.old_start + "," + h.old_lines + " +" + h.new_start + "," + h.new_lines + " @@";
        body.appendChild(hd);
        for (const line of h.lines) {
          const sign = line.charAt(0);
          const cls = (sign === "+" ? "add" : sign === "-" ? "del" : "ctx");
          const ln = TR.el("div", "tc-diff-line tc-diff-" + cls);
          ln.textContent = line;
          body.appendChild(ln);
          if (sign === "+") added++;
          else if (sign === "-") removed++;
        }
      }
      if (added) {
        summary.appendChild(document.createTextNode(" "));
        const plus = TR.badge("+" + added, "tc-badge-add");
        summary.appendChild(plus);
      }
      if (removed) {
        summary.appendChild(document.createTextNode(" "));
        summary.appendChild(TR.badge("-" + removed, "tc-badge-del"));
      }
    } else if (tc.result && tc.result.is_error) {
      body = TR.el("pre", "tc-error-content");
      body.textContent = TR.decodeContent(tc.result.content);
    }
    const isError = !!(tc.result && tc.result.is_error);
    return TR.makeCard("edit", summary, body, { isError });
  });
})();
