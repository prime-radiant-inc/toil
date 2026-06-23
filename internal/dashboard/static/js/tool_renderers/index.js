// tool_renderers/index.js — registry mapping tool_name → renderer fn and the
// shared helpers used by per-tool renderer files.
//
// Each per-tool file in this directory calls window.ToolRenderers.register(name, fn)
// at module top level. Helpers (makeCard, summarySpan, badge, etc.) are exposed on
// window.ToolRenderers so per-tool files can use them without re-imports.

(function (global) {
  const registry = {};

  function register(name, fn) {
    registry[name] = fn;
  }

  function render(toolCall) {
    const fn = registry[toolCall.tool_name] || renderUnknown;
    return fn(toolCall);
  }

  function makeCard(family, summary, body, options) {
    options = options || {};
    const card = el("div", "tc tc-" + family + (options.isError ? " tc-error" : ""));
    const sum = el("div", "tc-summary");
    sum.appendChild(summary);
    card.appendChild(sum);
    if (body) {
      const bodyWrap = el("div", "tc-body");
      bodyWrap.appendChild(body);
      card.appendChild(bodyWrap);
    }
    return card;
  }

  function summarySpan(parts) {
    const wrap = el("span", "tc-summary-text");
    for (const p of parts) {
      if (typeof p === "string") {
        const t = document.createElement("span");
        t.textContent = p;
        wrap.appendChild(t);
      } else {
        wrap.appendChild(p);
      }
    }
    return wrap;
  }

  function badge(text, cls) {
    const b = el("span", "tc-badge " + (cls || ""));
    b.textContent = text;
    return b;
  }

  // Success / failure / in-flight indicator. Returns an HTMLElement to append
  // to the card summary. Renderers should append this before any tool-specific
  // summary content so the status sits at a consistent position.
  function statusBadge(tc) {
    if (!tc.result) {
      const dim = el("span", "tc-status tc-status-pending");
      dim.title = "in flight";
      dim.textContent = "…";
      return dim;
    }
    if (tc.result.is_error) {
      const b = el("span", "tc-status tc-status-fail");
      b.textContent = "✗";
      b.title = "failed";
      return b;
    }
    const ok = el("span", "tc-status tc-status-ok");
    ok.textContent = "✓";
    ok.title = "completed";
    return ok;
  }

  function renderUnknown(tc) {
    const summary = summarySpan([statusBadge(tc), badge(tc.tool_name || "tool"), " call"]);
    let body = null;
    if (tc.args || tc.result) {
      body = el("div", "tc-unknown");
      if (tc.args) {
        const a = el("div", "tc-args");
        a.appendChild(global.GenericRenderer.renderValue(tc.args));
        body.appendChild(a);
      }
      if (tc.result) {
        const r = el("div", "tc-result");
        r.appendChild(global.GenericRenderer.renderValue(decodeContent(tc.result.content)));
        body.appendChild(r);
      }
    }
    return makeCard("unknown", summary, body, {});
  }

  function decodeContent(c) {
    if (c == null) return "";
    if (typeof c === "string") {
      // Server marshals to JSON; may be a JSON-encoded string.
      try {
        const parsed = JSON.parse(c);
        return typeof parsed === "string" ? parsed : c;
      } catch { return c; }
    }
    return JSON.stringify(c, null, 2);
  }

  function lineCapped(text, cap, className) {
    const lines = text.split("\n");
    const wrap = el("div", "capped" + (className ? " " + className : ""));
    const visible = el("pre", "capped-pre" + (className ? " " + className : ""));
    if (lines.length <= cap) {
      visible.textContent = text;
      wrap.appendChild(visible);
      return wrap;
    }
    visible.textContent = lines.slice(0, cap).join("\n");
    wrap.appendChild(visible);
    const remaining = lines.length - cap;
    const btn = el("button", "show-more");
    btn.type = "button";
    btn.textContent = "Show " + remaining + " more lines";
    btn.addEventListener("click", () => {
      visible.textContent = text;
      btn.remove();
    });
    wrap.appendChild(btn);
    return wrap;
  }

  // visualClamp caps to N *visual* lines (after word-wrap) via CSS line-clamp.
  // A "Show full" button is appended only when the content overflows.
  function visualClamp(text, lines, className) {
    const wrap = el("div", "vclamp-wrap");
    const body = el("pre", "vclamp" + (className ? " " + className : ""));
    body.style.setProperty("--vlc-lines", String(lines));
    body.textContent = text;
    wrap.appendChild(body);
    requestAnimationFrame(() => {
      if (body.scrollHeight - body.clientHeight > 1) {
        const btn = el("button", "show-more");
        btn.type = "button";
        btn.textContent = "Show full";
        btn.addEventListener("click", () => {
          body.classList.add("expanded");
          btn.remove();
        });
        wrap.appendChild(btn);
      }
    });
    return wrap;
  }

  // capLines kept for any external callers; internally prefer lineCapped.
  function capLines(text, max) {
    const lines = text.split("\n");
    if (lines.length <= max) return text;
    return lines.slice(0, max).join("\n") + "\n… " + (lines.length - max) + " more lines";
  }

  function el(tag, cls) {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    return e;
  }

  function langFromExt(path) {
    const m = /\.(\w+)$/.exec(path || "");
    if (!m) return "text";
    const ext = m[1].toLowerCase();
    const map = {
      go: "go", js: "javascript", ts: "typescript", py: "python",
      md: "markdown", yaml: "yaml", yml: "yaml", json: "json",
      html: "html", css: "css", sh: "bash",
    };
    return map[ext] || "text";
  }

  global.ToolRenderers = {
    register, render,
    makeCard, summarySpan, badge, statusBadge,
    decodeContent, capLines, lineCapped, visualClamp, el, langFromExt,
  };
})(window);
