// apply_patch renderer — unified-diff envelope provided as a single string arg.
// Body shows the first N diff lines inline; the rest fold behind a
// "Show N more lines" button (consistent with other tool cards). No
// embedded scroll container.
(function () {
  const TR = window.ToolRenderers;
  const DIFF_CAP = 10;

  function buildDiffLine(line) {
    if (line.startsWith("*** ")) {
      const hd = TR.el("div", "tc-diff-header");
      hd.textContent = line;
      return { node: hd, kind: "header" };
    }
    const sign = line.charAt(0);
    const cls = sign === "+" ? "add" : sign === "-" ? "del" : "ctx";
    const ln = TR.el("div", "tc-diff-line tc-diff-" + cls);
    ln.textContent = line;
    return { node: ln, kind: sign };
  }

  TR.register("apply_patch", function (tc) {
    const patch = (tc.args && tc.args.patch) || "";
    const summary = TR.summarySpan([
      TR.statusBadge(tc),
      TR.badge("✎ Patch"),
    ]);
    const fileMatch = /\*\*\* (?:Update|Create|Delete) File:\s*([^\n]+)|--- a\/([^\n]+)/.exec(patch);
    if (fileMatch) {
      const path = fileMatch[1] || fileMatch[2];
      const pathSpan = TR.el("span", "tc-path");
      pathSpan.textContent = " " + path;
      pathSpan.title = path;
      summary.appendChild(pathSpan);
    }

    let body = null;
    let added = 0;
    let removed = 0;
    if (patch) {
      body = TR.el("div", "tc-diff");
      const lines = patch.split("\n");
      const built = lines.map(buildDiffLine);
      for (const { kind } of built) {
        if (kind === "+") added++;
        else if (kind === "-") removed++;
      }
      if (built.length <= DIFF_CAP) {
        for (const { node } of built) body.appendChild(node);
      } else {
        for (let i = 0; i < DIFF_CAP; i++) body.appendChild(built[i].node);
        const hidden = TR.el("div", "tc-diff-hidden");
        hidden.style.display = "none";
        for (let i = DIFF_CAP; i < built.length; i++) hidden.appendChild(built[i].node);
        body.appendChild(hidden);
        const more = TR.el("button", "show-more");
        more.type = "button";
        more.textContent = "Show " + (built.length - DIFF_CAP) + " more lines";
        more.addEventListener("click", () => {
          hidden.style.display = "";
          more.remove();
        });
        body.appendChild(more);
      }
    }

    if (added) {
      summary.appendChild(document.createTextNode(" "));
      summary.appendChild(TR.badge("+" + added, "tc-badge-add"));
    }
    if (removed) {
      summary.appendChild(document.createTextNode(" "));
      summary.appendChild(TR.badge("-" + removed, "tc-badge-del"));
    }
    const isError = !!(tc.result && tc.result.is_error);
    return TR.makeCard("patch", summary, body, { isError });
  });
})();
