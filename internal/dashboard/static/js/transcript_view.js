// transcript_view.js — builds the DOM for a row's disclosure body.
// Consumes the Transcript JSON shape: { attempts: [{ ordinal, outcome, failure_reason, messages: [...] }] }.
// Each Row in the document represents a single execution, so attempts will always be a 1-element list.
// The attempt header is omitted; messages render directly in the transcript wrapper.

(function (global) {
  function render(transcript) {
    const wrap = el("div", "transcript");
    if (!transcript || !transcript.attempts) return wrap;
    // Each row is one execution; attempts is always a 1-element list.
    transcript.attempts.forEach((attempt) => {
      wrap.appendChild(renderAttempt(attempt));
    });
    return wrap;
  }

  function renderAttempt(attempt) {
    const outcome = attempt.outcome || "running";
    const sec = el("section", "attempt attempt-" + outcome);
    // No attempt-header: each Row already represents a single execution, so
    // "Attempt N of M" is redundant. Failed attempts still show failure reason.
    if (attempt.outcome === "failed" && attempt.failure_reason) {
      const failNote = el("div", "attempt-failure-note");
      failNote.textContent = "failed: " + attempt.failure_reason;
      sec.appendChild(failNote);
    }

    // Pre-pass: task_list updates reference task ids assigned by earlier
    // append calls. Resolve them so the renderer can surface descriptions.
    annotateTaskListUpdates(attempt.messages || []);

    const body = el("div", "attempt-body");
    (attempt.messages || []).forEach((m) => {
      body.appendChild(renderMessage(m));
    });
    sec.appendChild(body);
    return sec;
  }

  // Walk task_list calls in order. Appends assign sequential ids starting at 1;
  // updates pick up the matching task and gain a _resolved field with the
  // original description/type/prompt so the renderer can show context.
  function annotateTaskListUpdates(messages) {
    const registry = new Map();
    let nextId = 1;
    for (const m of messages) {
      if (m.kind !== "tool_call" || !m.tool_call) continue;
      if (m.tool_call.tool_name !== "task_list") continue;
      const args = m.tool_call.args || {};
      if (args.action === "append" && Array.isArray(args.tasks)) {
        for (const t of args.tasks) {
          registry.set(nextId, {
            description: t.description || "",
            type: t.type || "",
            prompt: t.prompt || "",
          });
          nextId++;
        }
      } else if (args.action === "update" && Array.isArray(args.updates)) {
        for (const u of args.updates) {
          const original = registry.get(u.id);
          if (original) u._resolved = original;
        }
      }
    }
  }

  function renderMessage(m) {
    switch (m.kind) {
      case "system_prompt":  return renderSystemPrompt(m);
      case "user_prompt":    return renderAssistantOrUser(m, "msg-user", "user");
      case "assistant":      return renderAssistantOrUser(m, "msg-assistant", "agent");
      case "tool_call":      return window.ToolRenderers.render(m.tool_call);
      case "decision":       return renderDecision(m.decision);
      default: {
        const u = el("div", "msg-unknown");
        u.textContent = "[unknown message kind: " + m.kind + "]";
        return u;
      }
    }
  }

  function renderSystemPrompt(m) {
    const wrap = el("details", "msg msg-system");
    const summary = el("summary", "msg-system-summary");
    summary.textContent = "▸ role boilerplate · " + (m.text ? m.text.length : 0) + " chars";
    wrap.appendChild(summary);
    const body = el("pre", "msg-system-body");
    body.textContent = m.text || "";
    wrap.appendChild(body);
    return wrap;
  }

  function renderAssistantOrUser(m, klass, labelText) {
    const CAP = 10;
    const wrap = el("div", "msg " + klass);
    const label = el("div", "msg-label");
    label.textContent = labelText;
    wrap.appendChild(label);
    const body = el("div", "msg-body");
    const text = m.text || "";
    const lines = text.split("\n");
    if (lines.length > CAP) {
      // Truncate: render first CAP lines with fallback markdown, full view on expand.
      const truncated = lines.slice(0, CAP).join("\n");
      body.innerHTML = renderMarkdownSafe(truncated);
      wrap.appendChild(body);
      const btn = el("button", "show-more");
      btn.type = "button";
      btn.textContent = "Show " + (lines.length - CAP) + " more lines";
      btn.addEventListener("click", () => {
        // Restore full server HTML if available, else render full markdown.
        if (m.html) {
          body.innerHTML = m.html;
        } else {
          body.innerHTML = renderMarkdownSafe(text);
        }
        btn.remove();
      });
      wrap.appendChild(btn);
    } else {
      if (m.html) {
        // Server has pre-rendered HTML (Task 23) — trust it (server sanitizes via bluemonday).
        body.innerHTML = m.html;
      } else {
        // Fallback: minimal markdown for newlines/code/bold/italic.
        body.innerHTML = renderMarkdownSafe(text);
      }
      wrap.appendChild(body);
    }
    return wrap;
  }

  function renderDecision(d) {
    const wrap = el("div", "msg msg-decision msg-decision-" + (d.family || "neutral"));
    const headline = el("div", "decision-headline");
    headline.textContent = "✓ Decision: " + d.id;
    wrap.appendChild(headline);
    if (d.description) {
      const desc = el("div", "decision-desc");
      desc.textContent = d.description;
      wrap.appendChild(desc);
    }
    if (d.tags && d.tags.length) {
      const tags = el("div", "decision-tags");
      d.tags.forEach((t) => {
        const pill = el("span", "decision-tag");
        pill.textContent = t;
        tags.appendChild(pill);
      });
      wrap.appendChild(tags);
    }
    return wrap;
  }

  function escapeHTML(s) {
    return String(s)
      .replace(/&/g, "&amp;")
      .replace(/</g, "&lt;")
      .replace(/>/g, "&gt;");
  }

  // Minimal markdown for the fallback path: escape HTML, then bold/italic/code/newlines.
  function renderMarkdownSafe(text) {
    const esc = text
      .replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;");
    return esc
      .replace(/`([^`]+)`/g, "<code>$1</code>")
      .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
      .replace(/\*([^*]+)\*/g, "<em>$1</em>")
      .replace(/\n/g, "<br>");
  }

  function el(tag, cls) {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    return e;
  }

  global.TranscriptView = { render };
})(window);
