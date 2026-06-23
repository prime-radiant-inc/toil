(function () {
  // ---- Row prompt truncation -----------------------------------------------

  function applyRowPromptCaps(root) {
    const cap = 10;
    const prompts = (root || document).querySelectorAll('.row .prompt');
    prompts.forEach(p => {
      if (p.dataset.capped) return;
      const full = p.textContent;
      const lines = full.split('\n');
      if (lines.length <= cap) return;
      p.dataset.capped = '1';
      p.dataset.fullText = full;
      p.textContent = lines.slice(0, cap).join('\n');
      const btn = document.createElement('button');
      btn.type = 'button';
      btn.className = 'show-more';
      btn.textContent = `Show ${lines.length - cap} more lines`;
      btn.addEventListener('click', () => {
        p.textContent = full;
        btn.remove();
      });
      p.after(btn);
    });
  }

  document.addEventListener('DOMContentLoaded', () => applyRowPromptCaps());

  const runDocEl = document.getElementById('run-doc');
  if (!runDocEl) return;
  const runID = runDocEl.dataset.runId;
  const basePath = runDocEl.dataset.basePath || '';

  const storageKey = (kind, id) => `toil:doc:${runID}:${kind}:${id}`;

  // transcripts: runID::nodeID::attempt → live Transcript object (the same
  // object passed to TranscriptView.render). Mutations on this object reflect
  // on re-render. attempt defaults to 1 for rows without AttemptOrdinal.
  const liveTranscripts = new Map();

  function transcriptKey(txRunID, nodeID, attempt) {
    return txRunID + '::' + nodeID + '::' + (attempt || 1);
  }

  // Restore prior fold state from localStorage for every .run-node on initial render.
  document.querySelectorAll('.run-node').forEach((section) => {
    const subID = section.dataset.runId;
    if (!subID) return;
    const key = storageKey('runfold', subID);
    const val = localStorage.getItem(key);
    if (val === 'compact') section.classList.add('compact');
    else if (val === 'expanded') section.classList.remove('compact');
  });

  // Restore prior row-disclosure state.
  document.querySelectorAll('.row').forEach((row) => {
    const nodeID = row.dataset.nodeId;
    const rowRunID = row.dataset.runId;
    const attempt = row.dataset.attempt || '1';
    if (!nodeID || !rowRunID) return;
    const key = storageKey('row', `${rowRunID}/${nodeID}/${attempt}`);
    if (localStorage.getItem(key) === 'open') {
      const disclosure = row.querySelector('.disclosure');
      if (disclosure) openRow(row, disclosure);
    }
  });

  // One delegated click handler covers run-node toggles, row disclosures,
  // and session-id copy buttons, for both server-rendered and SSE-injected
  // elements.
  runDocEl.addEventListener('click', (ev) => {
    const copyBtn = ev.target.closest('.copy-btn');
    if (copyBtn) {
      ev.preventDefault();
      ev.stopPropagation();
      const text = copyBtn.dataset.copy || '';
      if (!text) return;
      navigator.clipboard.writeText(text).then(() => {
        copyBtn.classList.add('copied');
        setTimeout(() => copyBtn.classList.remove('copied'), 1200);
      }).catch(() => {});
      return;
    }
    const toggle = ev.target.closest('.run-node-toggle');
    if (toggle) {
      ev.preventDefault();
      ev.stopPropagation();
      const section = toggle.closest('.run-node');
      if (!section) return;
      const subID = section.dataset.runId;
      const nowCompact = !section.classList.contains('compact');
      section.classList.toggle('compact', nowCompact);
      // Swap the glyph text node to match.
      const glyph = toggle.querySelector('.glyph');
      if (glyph) glyph.textContent = nowCompact ? '▸' : '▾';
      if (subID) localStorage.setItem(storageKey('runfold', subID), nowCompact ? 'compact' : 'expanded');
      return;
    }
    const disc = ev.target.closest('.disclosure');
    if (disc) {
      ev.preventDefault();
      ev.stopPropagation();
      const row = disc.closest('.row');
      if (!row) return;
      const nodeID = row.dataset.nodeId;
      const rowRunID = row.dataset.runId;
      const attempt = row.dataset.attempt || '1';
      if (!nodeID || !rowRunID) return;
      const key = storageKey('row', `${rowRunID}/${nodeID}/${attempt}`);
      if (disc.classList.contains('open')) {
        closeRow(row, disc);
        localStorage.removeItem(key);
      } else {
        openRow(row, disc);
        localStorage.setItem(key, 'open');
      }
      return;
    }
  });

  function ensureDisclosureBody(row) {
    let body = row.querySelector('.disclosure-body');
    if (!body) {
      body = document.createElement('div');
      body.className = 'disclosure-body';
      body.dataset.loaded = '';
      body.textContent = 'Loading…';
      row.querySelector('.disclosure').after(body);
    }
    return body;
  }

  function openRow(row, disclosure) {
    const body = ensureDisclosureBody(row);
    body.classList.remove('hidden');
    disclosure.classList.add('open');
    const label = disclosure.querySelector('.disc-label');
    if (label) label.textContent = 'hide details';

    if (body.dataset.loaded !== '1') {
      const rowRunID = row.dataset.runId;
      const nodeID = row.dataset.nodeId;
      const attempt = parseInt(row.dataset.attempt || '1', 10);
      fetch(`/runs/${encodeURIComponent(rowRunID)}/document/row/${encodeURIComponent(nodeID)}?attempt=${attempt}`)
        .then(r => r.json())
        .then(data => {
          renderDisclosureBody(body, data);
          if (data.transcript) {
            liveTranscripts.set(transcriptKey(rowRunID, nodeID, attempt), data.transcript);
          }
          body.dataset.loaded = '1';
        })
        .catch(err => { body.textContent = `Error: ${err.message}`; });
    }
  }

  function closeRow(row, disclosure) {
    const body = row.querySelector('.disclosure-body');
    if (body) body.classList.add('hidden');
    disclosure.classList.remove('open');
    const label = disclosure.querySelector('.disc-label');
    if (label) label.textContent = 'show details';
    const rowRunID = row.dataset.runId;
    const nodeID = row.dataset.nodeId;
    const attempt = parseInt(row.dataset.attempt || '1', 10);
    if (rowRunID && nodeID) {
      liveTranscripts.delete(transcriptKey(rowRunID, nodeID, attempt));
    }
  }


  function renderDisclosureBody(body, data) {
    body.textContent = '';

    // Transcript (the new chronological view) — the system_prompt at the top
    // of each attempt exposes the boilerplate via a collapsed <details> element.
    if (data.transcript && window.TranscriptView) {
      body.appendChild(window.TranscriptView.render(data.transcript));
    }
  }

  function renderFullSession(body, session, linkEl) {
    const panel = document.createElement('div');
    panel.className = 'full-session-panel';
    const count = session.parts ? session.parts.length : 0;
    const labelEl = document.createElement('div');
    labelEl.className = 'sub-label';
    labelEl.innerHTML = `full session ${escapeHtml(session.session_id || '')} <span class="count">· ${count} parts</span>`;
    panel.appendChild(labelEl);
    for (const p of (session.parts || [])) {
      const part = document.createElement('div');
      part.className = 't-line full-session-part';
      part.innerHTML = `<b>${escapeHtml(p.node || '')}</b> · ${escapeHtml(p.decision || '')}` +
        (p.message ? `<br><span class="result">${escapeHtml(p.message)}</span>` : '');
      panel.appendChild(part);
    }
    if (linkEl) linkEl.remove();
    body.appendChild(panel);
  }

  function appendSubLabel(body, name, count) {
    const el = document.createElement('div');
    el.className = 'sub-label';
    if (count === 0 || count === '' || count === undefined || count === null) {
      el.textContent = name;
    } else {
      el.innerHTML = `${name} <span class="count">· ${escapeHtml(String(count))}</span>`;
    }
    body.appendChild(el);
  }

  function artifactRow(a, depth) {
    depth = depth || 0;
    const div = document.createElement('div');
    div.className = 'artifact';
    if (depth > 0) div.style.marginLeft = `${depth * 12}px`;
    const header = document.createElement('div');
    header.className = 'artifact-header';
    const desc = a.desc || '';
    const displayDesc = desc.length > 200 ? desc.slice(0, 197) + '…' : desc;
    header.innerHTML = `<span class="name">${escapeHtml(a.name || '')}</span>` +
      (a.size ? ` <span class="size">${escapeHtml(a.size)}</span>` : '') +
      ` <span class="desc">${escapeHtml(displayDesc)}</span>`;
    header.title = a.value || desc;
    div.appendChild(header);
    if (Array.isArray(a.nested) && a.nested.length > 0) {
      const nestedContainer = document.createElement('div');
      nestedContainer.className = 'artifact-nested';
      for (const child of a.nested) {
        nestedContainer.appendChild(artifactRow(child, depth + 1));
      }
      div.appendChild(nestedContainer);
    }
    return div;
  }

  function escapeHtml(s) {
    if (s == null) return '';
    return String(s)
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;');
  }

  // ---- Live SSE subscription ----------------------------------------------

  let eventSource = null;
  let reconnectAttempts = 0;
  let reconnectTimer = null;
  let reconnectBanner = null;

  const liveEventTypes = [
    'run_started',
    'run_completed',
    'wave_started',
    'wave_completed',
    'node_started',
    'node_completed',
    'node_attempt_started',
    'node_attempt_failed',
    'node_inputs_resolved',
    'node_prompt',
    'node_output',
    'tool_call',
    'tool_result',
    'subworkflow_started',
    'subworkflow_completed',
  ];

  function connectLiveStream() {
    if (eventSource) {
      eventSource.close();
      eventSource = null;
    }
    if (reconnectTimer) {
      clearTimeout(reconnectTimer);
      reconnectTimer = null;
    }

    const url = `/runs/${encodeURIComponent(runID)}/events/stream`;
    eventSource = new EventSource(url);

    // Named handler for each event type we care about.
    for (const evType of liveEventTypes) {
      eventSource.addEventListener(evType, (msg) => {
        reconnectAttempts = 0;
        clearReconnectBanner();
        try {
          const payload = JSON.parse(msg.data);
          handleStreamEvent(evType, payload);
        } catch (e) {
          console.error('bad event payload', evType, msg.data, e);
        }
      });
    }

    // Default message handler (covers events emitted without an `event:` prefix)
    eventSource.addEventListener('message', (msg) => {
      reconnectAttempts = 0;
      clearReconnectBanner();
      try {
        const payload = JSON.parse(msg.data);
        // Use the payload's own .type field as the dispatch key
        handleStreamEvent(payload.type || 'message', payload);
      } catch (e) {
        console.error('bad message payload', msg.data, e);
      }
    });

    eventSource.onerror = () => {
      reconnectAttempts++;
      eventSource.close();
      eventSource = null;
      if (reconnectAttempts > 12) {  // ~60s of failures at 5s cap
        showReconnectBanner();
        return;
      }
      const delay = Math.min(5000, 500 * reconnectAttempts);
      reconnectTimer = setTimeout(connectLiveStream, delay);
    };
  }

  // ---- Live transcript mutation helpers -----------------------------------

  function currentAttempt(transcript) {
    if (!transcript.attempts || !transcript.attempts.length) {
      if (!transcript.attempts) transcript.attempts = [];
      transcript.attempts.push({ ordinal: 1, outcome: '', messages: [] });
    }
    return transcript.attempts[transcript.attempts.length - 1];
  }

  // Find the attempt ordinal for the open/running row of a node.
  // For live events, a node may have multiple rows (past executions are closed,
  // the current execution is running). We want the row that is currently open
  // or the most recently created one.
  function liveAttemptForNode(txRunID, nodeID) {
    // Find all rows for this run+node, pick the one that is running or the last one.
    const rows = Array.from(document.querySelectorAll(
      `.row[data-run-id="${CSS.escape(txRunID)}"][data-node-id="${CSS.escape(nodeID)}"]`));
    if (!rows.length) return 1;
    // Prefer a running row; fall back to the last in DOM order.
    const running = rows.find(r => r.classList.contains('running'));
    const target = running || rows[rows.length - 1];
    return parseInt(target.dataset.attempt || '1', 10);
  }

  // Mutates the cached Transcript object to incorporate a live event.
  // Returns true if the transcript was found and mutated (caller should re-render).
  // Returns false if no open transcript matched (caller continues with other handlers).
  function applyLiveEventToTranscript(evType, payload) {
    const attempt = liveAttemptForNode(payload.run_id, payload.node_id);
    const key = transcriptKey(payload.run_id, payload.node_id, attempt);
    const transcript = liveTranscripts.get(key);
    if (!transcript) return false;
    if (!transcript.attempts) transcript.attempts = [];

    switch (evType) {
      case 'node_attempt_started': {
        const ord = (payload.data && payload.data.attempt) || (transcript.attempts.length + 1);
        if (!transcript.attempts.some(a => a.ordinal === ord)) {
          transcript.attempts.push({ ordinal: ord, outcome: '', messages: [] });
        }
        break;
      }
      case 'node_attempt_failed': {
        const ord = (payload.data && payload.data.attempt) || 1;
        const att = transcript.attempts.find(a => a.ordinal === ord);
        if (att) {
          att.outcome = 'failed';
          att.failure_reason = (payload.data && payload.data.reason) || '';
        }
        break;
      }
      case 'node_output': {
        if (!payload.text) return false;
        const att = currentAttempt(transcript);
        const last = att.messages[att.messages.length - 1];
        if (last && last.kind === 'assistant') {
          last.text = (last.text || '') + payload.text;
          last.html = ''; // invalidate cached server-side rendering
        } else {
          att.messages.push({ kind: 'assistant', text: payload.text });
        }
        break;
      }
      case 'tool_call': {
        const att = currentAttempt(transcript);
        att.messages.push({
          kind: 'tool_call',
          tool_call: {
            tool_id: payload.data && payload.data.tool_id,
            tool_name: payload.data && payload.data.tool_name,
            args: (payload.data && payload.data.args) || {},
          },
        });
        break;
      }
      case 'tool_result': {
        const att = currentAttempt(transcript);
        const targetID = payload.data && payload.data.tool_id;
        const target = att.messages.find(m =>
          m.kind === 'tool_call' && m.tool_call && m.tool_call.tool_id === targetID);
        if (target) {
          target.tool_call.result = {
            is_error: !!(payload.data && payload.data.is_error),
            content: payload.data && payload.data.content,
          };
        }
        break;
      }
      case 'node_completed': {
        const att = transcript.attempts[transcript.attempts.length - 1];
        if (att) {
          att.outcome = 'succeeded';
          if (payload.data && payload.data.decision) {
            att.messages.push({
              kind: 'decision',
              decision: { id: payload.data.decision },
            });
          }
        }
        break;
      }
      default:
        return false;
    }
    return true;
  }

  // Re-renders the transcript portion of an open disclosure body.
  // Finds the running or last row for the nodeID, then re-renders its transcript.
  function reRenderOpenDisclosure(txRunID, nodeID) {
    const attempt = liveAttemptForNode(txRunID, nodeID);
    const rowEl = document.querySelector(
      `.row[data-run-id="${CSS.escape(txRunID)}"][data-node-id="${CSS.escape(nodeID)}"][data-attempt="${attempt}"]`);
    if (!rowEl) return;
    const body = rowEl.querySelector('.disclosure-body');
    if (!body || body.classList.contains('hidden')) return;
    const transcript = liveTranscripts.get(transcriptKey(txRunID, nodeID, attempt));
    if (!transcript || !window.TranscriptView) return;
    // Preserve the prompt block at top; only replace the .transcript element.
    const txWrap = body.querySelector('.transcript');
    const fresh = window.TranscriptView.render(transcript);
    if (txWrap) {
      txWrap.replaceWith(fresh);
    } else {
      body.appendChild(fresh);
    }
  }

  function handleStreamEvent(evType, payload) {
    console.debug('[live]', evType, payload);

    // Attempt to update any open transcript first; re-render if we mutated it.
    // We still fall through to the existing row-level handlers below for events
    // that also affect non-transcript DOM (node_output, node_completed).
    if (applyLiveEventToTranscript(evType, payload)) {
      reRenderOpenDisclosure(payload.run_id, payload.node_id);
    }

    switch (evType) {
      case 'node_started':          return onNodeStarted(payload);
      case 'node_completed':        return onNodeCompleted(payload);
      case 'node_output':           return onNodeOutput(payload);
      case 'subworkflow_started':   return onSubworkflowStarted(payload);
      case 'subworkflow_completed': return onSubworkflowCompleted(payload);
      case 'run_completed':         return onRunCompleted(payload);
      // wave_*, node_prompt, node_inputs_resolved, run_started,
      // node_attempt_started, node_attempt_failed, tool_call, tool_result:
      // transcript mutation handled above; no further row-level DOM action needed.
    }
  }

  function rowID(rowRunID, nodeID, attempt) {
    return `node-${rowRunID}-${nodeID}-attempt-${attempt || 1}`;
  }

  // Find an existing running row for a node, or create a new placeholder row
  // inside the parent run-node's .run-node-children container. Each node_started
  // event for a node creates a new execution row (attempt N). Completed rows are
  // not mutated by live events for a new execution.
  function findOrCreateRow(payload) {
    const rowRunID = payload.run_id;
    const nodeID = payload.node_id;
    if (!rowRunID || !nodeID) return null;
    // ForEach iteration node ids ("template::N") are handled by onSubworkflowStarted.
    if (nodeID.includes('::')) return null;

    // Find the parent run-node's children container.
    const parentSection = document.querySelector(
      `.run-node[data-run-id="${CSS.escape(rowRunID)}"]`);
    if (!parentSection) return null;
    const childrenContainer = parentSection.querySelector(':scope > .run-node-children');
    if (!childrenContainer) return null;

    // Find an existing running row for this node. Live events mutate the currently
    // running row, not any previously completed row.
    const existingRows = Array.from(childrenContainer.querySelectorAll(
      `:scope > .row[data-node-id="${CSS.escape(nodeID)}"]`));
    const runningRow = existingRows.find(r => r.classList.contains('running'));
    if (runningRow) return runningRow;

    // No running row — create a new one for the next attempt ordinal.
    const nextAttempt = existingRows.length + 1;
    const id = rowID(rowRunID, nodeID, nextAttempt);
    const already = document.getElementById(id);
    if (already) return already;

    const role = (payload && payload.role) || nodeID;
    const workflowID = parentSection.dataset.workflowId || '';
    const avBg = (typeof window !== 'undefined' && window.roleColor)
      ? window.roleColor(role, 'bg')
      : '';
    const stepColor = (typeof window !== 'undefined' && window.roleColor)
      ? window.roleColor(role, 'text')
      : '';
    const row = document.createElement('div');
    row.className = 'row running';
    row.id = id;
    row.dataset.runId = rowRunID;
    row.dataset.nodeId = nodeID;
    row.dataset.attempt = String(nextAttempt);
    // The meta-line is only emitted once we have an attempt > 1 or a session
    // id; for fresh rows the running indicator lives in who-right. The
    // structure mirrors the row_child template exactly so live-injected rows
    // pick up the same CSS.
    const wfWidget = workflowID
      ? `<span class="wf-widget"><span class="wf-name">${escapeHtml(workflowID)}</span><span class="wf-sep">·</span><span class="wf-runid">${escapeHtml(rowRunID)}</span></span><span class="sep">/</span>`
      : '';
    const attemptMeta = nextAttempt > 1
      ? `<div class="meta-line"><span class="attempt">attempt ${nextAttempt} of ${nextAttempt}</span></div>`
      : '';
    row.innerHTML = `
      <div class="av" style="background:${escapeHtml(avBg)}; color:#fff">${escapeHtml(firstLetter(role))}</div>
      <div class="row-body">
        <div class="who-line">
          <div class="who-left">
            <span class="path">${wfWidget}<span class="step" style="color:${escapeHtml(stepColor)}">${escapeHtml(role)}</span></span>
          </div>
          <div class="who-right"><span class="running-strip">running…</span></div>
        </div>
        ${attemptMeta}
        <div class="details-line">
          <a class="disclosure" data-disclosure="closed" href="#" role="button"><span class="disc-label">show details</span></a>
        </div>
        <div class="result running-result"></div>
      </div>
    `;

    childrenContainer.appendChild(row);
    return row;
  }

  function firstLetter(s) {
    if (!s) return '·';
    return s.charAt(0).toUpperCase();
  }

  function onNodeStarted(payload) {
    const row = findOrCreateRow(payload);
    if (row) {
      row.classList.add('running');
      onAnyDomMutation();
    }
  }

  function onNodeOutput(payload) {
    if (payload.stream !== 'stdout' || !payload.text) return;

    // Try to parse as structured decision; if it parses and has a decision,
    // fall through to the decision-update path.
    let parsed = null;
    try { parsed = JSON.parse(payload.text); } catch (e) { /* not JSON */ }

    const row = findOrCreateRow(payload);
    if (!row) return;

    if (parsed && parsed.decision) {
      // Decision payload: replace running indicator with pill.
      const who = row.querySelector('.who-line');
      if (!who) return;
      const dot = who.querySelector('.running-dot');
      if (dot) dot.remove();
      const strip = who.querySelector('.running-strip');
      if (strip) strip.remove();
      if (!who.querySelector('.dec')) {
        const pill = document.createElement('span');
        pill.className = `dec ${classifyDecisionFamily(parsed.decision)}`;
        pill.textContent = parsed.decision;
        who.appendChild(pill);
      }
      const result = row.querySelector('.result');
      if (result && parsed.message) {
        result.textContent = parsed.message;
        result.classList.remove('running-result');
      }
      onAnyDomMutation();
      return;
    }

    // Non-decision stdout: stream the text into the result block, line by line,
    // showing only the last few lines while the node is still running.
    const result = row.querySelector('.result');
    if (!result) return;
    result.classList.add('running-result');
    // Append chunk, cap at last 8 lines.
    const existing = result.textContent || '';
    const combined = (existing + payload.text).split('\n');
    const tail = combined.slice(-8).join('\n');
    result.textContent = tail.trim();
    onAnyDomMutation();
  }

  function onNodeCompleted(payload) {
    const runID = payload.run_id;
    const nodeID = payload.node_id;
    const decision = (payload.data && payload.data.decision) || '';
    if (!runID || !nodeID) return;
    // Find the currently running row for this node; fall back to the most recent.
    const rows = Array.from(document.querySelectorAll(
      `.row[data-run-id="${CSS.escape(runID)}"][data-node-id="${CSS.escape(nodeID)}"]`));
    if (rows.length === 0) return;
    const row = rows.find(r => r.classList.contains('running')) || rows[rows.length - 1];
    row.classList.remove('running');
    // Strip the "running…" indicator from who-right and tear down any legacy
    // running-dot/strip nodes that might still be present from older builds.
    const who = row.querySelector('.who-line');
    if (who) {
      who.querySelectorAll('.running-dot, .running-strip').forEach(n => n.remove());
    }
    // Emit the result-target pill matching the row_child template. We don't
    // have the workflow edges client-side, so we leave the target empty —
    // a subsequent page reload (or the document API) fills it in.
    if (decision) {
      const left = row.querySelector('.who-left');
      if (left) {
        left.querySelectorAll('.result-target').forEach(n => n.remove());
        const pill = document.createElement('span');
        pill.className = `result-target ${classifyDecisionFamily(decision)}`;
        pill.textContent = decision;
        left.appendChild(pill);
      }
    }
    onAnyDomMutation();
  }

  function onSubworkflowStarted(payload) {
    // Real event shape: data.child_run, data.child_workflow
    const parentRunID = payload.run_id;
    const nodeID = payload.node_id || '';
    const childRunID = payload.data && payload.data.child_run;
    const workflowID = (payload.data && payload.data.child_workflow) || '';
    if (!parentRunID || !childRunID) return;

    // Dedup: if the child .run-node already exists anywhere, do nothing.
    if (document.querySelector(`.run-node[data-run-id="${CSS.escape(childRunID)}"]`)) return;

    const parentSection = document.querySelector(
      `.run-node[data-run-id="${CSS.escape(parentRunID)}"]`);
    if (!parentSection) return;
    const childrenContainer = parentSection.querySelector(':scope > .run-node-children');
    if (!childrenContainer) return;

    const isIteration = nodeID.includes('::');
    let target = childrenContainer;
    if (isIteration) {
      const templateName = nodeID.split('::')[0];
      target = getOrCreateParallelChild(childrenContainer, templateName);
    }
    target.appendChild(buildRunningRunNode(childRunID, workflowID));
    onAnyDomMutation();
  }

  function getOrCreateParallelChild(container, templateName) {
    let pc = container.querySelector(
      `:scope > .parallel-child[data-iteration-prefix="${CSS.escape(templateName)}"]`);
    if (!pc) {
      pc = document.createElement('div');
      pc.className = 'parallel-child';
      pc.dataset.iterationPrefix = templateName;
      container.appendChild(pc);
    }
    return pc;
  }

  function buildRunningRunNode(childRunID, workflowID) {
    const section = document.createElement('section');
    section.className = 'run-node';
    section.dataset.runId = childRunID;
    if (workflowID) section.dataset.workflowId = workflowID;
    // Markup matches the wf_widget template define in run_node.html so SSE-
    // injected sub-runs look identical to server-rendered ones.
    section.innerHTML = `
      <header class="run-node-head">
        <a class="run-node-toggle" href="#" role="button"><span class="glyph">▾</span></a>
        <span class="wf-widget">
          <span class="wf-name">${escapeHtml(workflowID || '…')}</span>
          <span class="wf-sep">·</span>
          <span class="wf-runid">${escapeHtml(childRunID)}</span>
        </span>
      </header>
      <div class="run-node-children"></div>
    `;
    return section;
  }

  function onSubworkflowCompleted(payload) {
    // No structural mutation needed — completion will be reflected by the next
    // page render. We still trigger the scroll/activity nudge.
    onAnyDomMutation();
  }

  function onRunCompleted(payload) {
    if (payload.run_id === runID) {
      runDocEl.dataset.runStatus = 'completed';
      if (eventSource) {
        eventSource.close();
        eventSource = null;
      }
    }
    onAnyDomMutation();
  }

  // ---- Don't-yank scroll behavior ----------------------------------------

  let stickToBottom = false;
  let activityPill = null;
  const stickThreshold = 100; // px from bottom counts as "near bottom"

  function updateStickState() {
    const doc = document.documentElement;
    const distFromBottom = doc.scrollHeight - (window.scrollY + window.innerHeight);
    stickToBottom = distFromBottom < stickThreshold;
    if (stickToBottom && activityPill) {
      activityPill.remove();
      activityPill = null;
    }
  }

  window.addEventListener('scroll', updateStickState, { passive: true });
  updateStickState();

  function onAnyDomMutation() {
    // Called after each SSE-driven DOM change.
    if (stickToBottom) {
      // Defer to next frame to let the DOM settle before measuring.
      requestAnimationFrame(() => {
        window.scrollTo({ top: document.documentElement.scrollHeight, behavior: 'smooth' });
      });
      return;
    }
    if (activityPill) return; // already showing
    activityPill = document.createElement('div');
    activityPill.className = 'new-activity-pill';
    activityPill.textContent = '↓ new activity';
    activityPill.addEventListener('click', () => {
      window.scrollTo({ top: document.documentElement.scrollHeight, behavior: 'smooth' });
      if (activityPill) { activityPill.remove(); activityPill = null; }
    });
    document.body.appendChild(activityPill);
  }

  // Mirror server-side classifyDecision in build.go.
  function classifyDecisionFamily(decision) {
    const ok = ['approved', 'tests_pass', 'correct_failure', 'default', 'all_succeeded',
                'merged', 'ready', 'prepared', 'skip', 'force_approve', 'succeeded',
                'resolved', 'completed', 'tests_already_passing', 'learnings_synthesized'];
    const bad = ['changes_requested', 'tests_fail', 'fix_failed', 'failed',
                 'failed_handled', 'rejected', 'conflict', 'conflict_unresolved',
                 'give_up', 'root_cause_confirmed', 'cancelled', 'spec_issue'];
    const plan = ['ready_for_review', 'ready_for_plan', 'planned'];
    if (ok.includes(decision)) return 'ok';
    if (bad.includes(decision)) return 'bad';
    if (plan.includes(decision)) return 'plan';
    return 'neutral';
  }

  function showReconnectBanner() {
    if (reconnectBanner) return;
    reconnectBanner = document.createElement('div');
    reconnectBanner.className = 'banner-error';
    reconnectBanner.innerHTML = 'Live connection lost. <a href="">Refresh</a> to reconnect.';
    runDocEl.prepend(reconnectBanner);
  }

  function clearReconnectBanner() {
    if (reconnectBanner) {
      reconnectBanner.remove();
      reconnectBanner = null;
    }
  }

  // Subscribe if the run is currently running, or if ?live=1 is set (debug override).
  const shouldSubscribe = runDocEl.dataset.runStatus === 'running'
    || new URLSearchParams(window.location.search).get('live') === '1';
  if (shouldSubscribe) {
    connectLiveStream();
  }
})();
