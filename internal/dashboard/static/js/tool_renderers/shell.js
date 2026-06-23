// shell renderer — terminal command + output. Lace-inspired:
//   - summary uses `$ <command>` instead of a verbose "Shell" badge
//   - exit-code chip when the result carries a non-zero exit code
//   - "(no output)" indicator when the command succeeded silently
(function () {
  const TR = window.ToolRenderers;

  // Best-effort exit-code extraction. Different runners report this in
  // different shapes; we look at the parsed result content if it happens to be
  // structured JSON with an exit_code / exitCode field. Returns null when not
  // discoverable — non-zero exits will already show via the status badge.
  function extractExitCode(rawContent) {
    if (rawContent == null) return null;
    try {
      const parsed = typeof rawContent === "string" ? JSON.parse(rawContent) : rawContent;
      if (parsed && typeof parsed === "object") {
        if (typeof parsed.exit_code === "number") return parsed.exit_code;
        if (typeof parsed.exitCode === "number") return parsed.exitCode;
      }
    } catch { /* not structured — fall through */ }
    return null;
  }

  TR.register("shell", function (tc) {
    const cmd = (tc.args && tc.args.command) || "(no command)";
    const cmdCode = TR.el("code", "tc-cmd");
    cmdCode.textContent = "$ " + cmd;
    cmdCode.title = cmd;
    const summary = TR.summarySpan([
      TR.statusBadge(tc),
      cmdCode,
    ]);
    const exitCode = tc.result ? extractExitCode(tc.result.content) : null;
    if (exitCode != null && exitCode !== 0) {
      summary.appendChild(document.createTextNode(" "));
      summary.appendChild(TR.badge("exit " + exitCode, "tc-badge-error"));
    }
    let body = null;
    if (tc.result) {
      const out = TR.decodeContent(tc.result.content);
      if (out.length === 0) {
        body = TR.el("span", "tc-empty");
        body.textContent = "(no output)";
      } else {
        body = TR.el("div", "tc-terminal");
        body.appendChild(TR.lineCapped(out, 10, "tc-stdout"));
      }
    }
    return TR.makeCard("shell", summary, body, {
      isError: !!(tc.result && tc.result.is_error),
    });
  });
})();
