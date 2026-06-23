// ─── Metric Formatting Helpers ────────────────────────────────────────────────
//
// Kept in lockstep with internal/metrics/format.go. Fixtures in
// internal/metrics/formatter_fixtures.json pin the shared contract — both
// Go's TestFormatters_GoAgainstFixtures and TestFormatters_JSParity assert
// against the same cases. If these formatters drift from Go, that test fails.
function trimTrailingZero(s) {
    const dot = s.indexOf('.');
    if (dot < 0) return s;
    if (dot >= 2) return s.slice(0, dot + 2);
    return s;
}

const MetricsFmt = {
    duration(ms) {
        if (!ms || ms < 0) return '0s';
        const s = Math.floor(ms / 1000);
        if (s < 60) return s + 's';
        if (s < 3600) return Math.floor(s / 60) + 'm ' + String(s % 60).padStart(2, '0') + 's';
        const h = Math.floor(s / 3600);
        const m = Math.floor((s % 3600) / 60);
        return h + 'h ' + String(m).padStart(2, '0') + 'm';
    },
    tokens(n) {
        if (!n || n < 1000) return String(n || 0);
        if (n < 1_000_000) return trimTrailingZero((n / 1000).toFixed(2)) + 'k';
        return trimTrailingZero((n / 1_000_000).toFixed(2)) + 'M';
    },
    // Accepts a number, null, or undefined. null/undefined → "—" (unknown
    // model), 0 → "$0.00" (priced, no tokens), non-zero → formatted by
    // magnitude. Boundary at $0.9995 matches Go's FormatCost: any value
    // that would round up to $1.00 at 3dp is rendered at 2dp instead.
    cost(v) {
        if (v == null) return '—';
        const f = typeof v === 'number' ? v : parseFloat(v);
        if (isNaN(f)) return '—';
        if (f === 0) return '$0.00';
        if (f < 0.001) return '$' + f.toFixed(4);
        if (f >= 0.9995) return '$' + f.toFixed(2);
        return '$' + f.toFixed(3);
    },
};

// unpricedBadgeHTML returns a small amber badge when n > 0, matching the
// Go helper dashboard.unpricedBadge on the run-meta card. Mirror both
// sides when either changes.
function unpricedBadgeHTML(n) {
    if (!n || n <= 0) return '';
    const title = n + ' calls used a model not in the pricing catalog — cost shown is incomplete.';
    return ' <span title="' + title + '" class="inline-block text-[10px] font-medium px-1.5 py-0 rounded bg-amber-100 text-amber-800 align-middle">pricing incomplete</span>';
}

// renderNodeMetrics returns an HTML string for the metric row under a graph
// node. Handles leaf (own) vs compound (rollup with ∑ prefix) vs skipped.
function renderNodeMetrics(m, status) {
    if (!m) return '';
    if (status === 'skipped') {
        return '<div class="node-metrics"><span>—</span></div>';
    }
    const rollup = m.rollup;
    const line = (summary, withSum) => {
        const prefix = withSum ? '<span class="sum-prefix">∑</span>' : '';
        return (
            prefix +
            MetricsFmt.duration(summary.duration_ms) +
            '<span class="sep">·</span>' +
            MetricsFmt.tokens(summary.tokens_total) + ' tok' +
            '<span class="sep">·</span>' +
            MetricsFmt.cost(summary.cost_usd) +
            unpricedBadgeHTML(summary.unpriced_event_count)
        );
    };
    if (rollup) {
        // Compound node. If own has any meaningful activity, show both.
        if (m.duration_ms > 0 || m.tokens_total > 0) {
            return (
                '<div class="node-metrics">' +
                '<div class="node-metrics-row own">' + line(m, false) + '</div>' +
                '<div class="node-metrics-row rollup">' + line(rollup, true) + '</div>' +
                '</div>'
            );
        }
        return '<div class="node-metrics">' + line(rollup, true) + '</div>';
    }
    return '<div class="node-metrics">' + line(m, false) + '</div>';
}

(() => {
    // Using the module-level MetricsFmt defined above. Don't shadow.

    function escapeHtml(s) {
        return s.replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' }[c]));
    }

    function renderRunNodeSubtree(metricsResponse) {
        const entries = Object.entries(metricsResponse.nodes || {});
        if (!entries.length) return '<div class="p-3 text-xs text-muted">No nodes.</div>';
        entries.sort(([a], [b]) => a.localeCompare(b));
        const rows = entries.map(([id, node]) => {
            const m = node.rollup || node.own;
            const metricsHTML = MetricsFmt.duration(m.duration_ms) +
                ' <span class="text-edge">\xb7</span> ' +
                MetricsFmt.tokens(m.tokens.total) + ' tok' +
                ' <span class="text-edge">\xb7</span> ' +
                MetricsFmt.cost(m.cost_usd) +
                unpricedBadgeHTML(m.unpriced_event_count);
            return (
                '<div class="flex items-center justify-between px-3 py-1 text-xs border-t border-edge-light">' +
                '<span class="font-mono text-ink-light">' + escapeHtml(id) + '</span>' +
                '<span class="text-ink" style="font-variant-numeric:tabular-nums">' + metricsHTML + '</span>' +
                '</div>'
            );
        }).join('');
        return rows;
    }

    // ─── Section 1: SSE Dispatcher ─────────────────────────────────────
    const connectToStream = (runID, basePath) => {
        const source = new EventSource(basePath + '/runs/' + runID + '/stream');

        source.addEventListener('transcript-item', (e) => {
            const idStr = e.lastEventId || '';
            const nodeMatch = idStr.match(/nodeId:([^,]+)/);
            if (!nodeMatch) return;
            const nodeId = nodeMatch[1];
            const toolMatch = idStr.match(/toolUseId:(.+)/);
            const toolUseId = toolMatch ? toolMatch[1] : '';

            const container = document.querySelector('[data-node-transcript="' + CSS.escape(nodeId) + '"]');
            if (!container) return;

            // Remove "Waiting for output..." placeholder on first item
            const placeholder = container.querySelector('.text-muted');
            if (placeholder && container.children.length === 1) {
                container.removeChild(placeholder);
            }

            let inserted = null;
            if (toolUseId) {
                const existing = container.querySelector('[data-tool-use-id="' + CSS.escape(toolUseId) + '"]');
                if (existing) {
                    existing.outerHTML = e.data;
                    inserted = container.querySelector('[data-tool-use-id="' + CSS.escape(toolUseId) + '"]');
                }
            }
            if (!inserted) {
                const beforeCount = container.children.length;
                container.insertAdjacentHTML('beforeend', e.data);
                inserted = container.children[beforeCount] || null;
            }

            // Highlight any new code blocks. HTMX afterSwap covers htmx-driven
            // updates, but SSE inserts bypass it — so we do it here.
            if (inserted && typeof hljs !== 'undefined') {
                inserted.querySelectorAll('pre code:not(.hljs)').forEach((el) => hljs.highlightElement(el));
            }

            // Auto-scroll if user is near the bottom of the page
            const scrollBottom = window.innerHeight + window.scrollY;
            const docHeight = document.documentElement.scrollHeight;
            if (docHeight - scrollBottom < 300) {
                const lastChild = container.lastElementChild;
                if (lastChild) {
                    lastChild.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
                }
            }
        });

        let timelineRefreshTimer = null;
        source.addEventListener('timeline-refresh', () => {
            if (timelineRefreshTimer) return;
            timelineRefreshTimer = setTimeout(() => {
                timelineRefreshTimer = null;
                const timeline = document.getElementById('run-timeline');
                if (!timeline || timeline.offsetParent === null) return;
                const wasNearBottom = timeline.scrollHeight - timeline.scrollTop - timeline.clientHeight < 100;
                htmx.ajax('GET', basePath + '/runs/' + runID + '/timeline', {
                    target: timeline,
                    swap: 'innerHTML',
                }).then(() => {
                    if (wasNearBottom) {
                        timeline.scrollTop = timeline.scrollHeight;
                    }
                });
            }, 800);
        });

        // Refetch the drawer body on timeline-refresh while the drawer is open.
        // Child-run progress doesn't stream as transcript-item events for
        // subworkflow / foreach-iteration panels, so we re-render on the
        // same signal that refreshes the tree.
        let drawerRefreshTimer = null;
        source.addEventListener('timeline-refresh', () => {
            const drawer = window.__transcriptDrawer;
            if (!drawer || !drawer.open) return;
            if (drawerRefreshTimer) return;
            drawerRefreshTimer = setTimeout(() => {
                drawerRefreshTimer = null;
                drawer.refetch();
            }, 800);
        });

        source.addEventListener('node-status', (e) => {
            const nodeId = e.lastEventId;
            if (!nodeId) return;
            const badge = document.querySelector('[data-node-status-badge="' + CSS.escape(nodeId) + '"]');
            if (badge) badge.innerHTML = e.data;

            // Auto-expand running or failed nodes in the transcript accordion
            const statusText = e.data.toLowerCase();
            if (statusText.includes('running') || statusText.includes('started')) {
                expandAccordionNode(nodeId);
            }
            // Match genuine failures only — 'failed-handled' is a distinct
            // absorbed state styled separately; don't apply the red-border.
            if (statusText.includes('failed') && !statusText.includes('failed-handled')) {
                expandAccordionNode(nodeId);
                // Add red border to failed accordion section
                const section = document.querySelector('.accordion-section[data-node-id="' + CSS.escape(nodeId) + '"]');
                if (section) {
                    section.classList.add('border-l-4', 'border-l-red-400');
                }
            } else if (statusText.includes('failed-handled')) {
                expandAccordionNode(nodeId);
                // Amber border for absorbed failures.
                const section = document.querySelector('.accordion-section[data-node-id="' + CSS.escape(nodeId) + '"]');
                if (section) {
                    section.classList.add('border-l-4', 'border-l-amber-400');
                }
            }
        });

        source.addEventListener('graph-update', (e) => {
            if (!e.data) return;
            try {
                const data = JSON.parse(e.data);
                if (data.node_id && data.type) {
                    const statusMap = {
                        node_started: 'running',
                        node_completed: 'completed',
                        node_failed: 'failed',
                        node_failed_handled: 'failed-handled',
                        node_skipped: 'skipped',
                    };
                    const status = statusMap[data.type];
                    if (status) {
                        updateGraphNodeStatus(data.node_id, status);
                    }
                }
            } catch (err) {
                // ignore parse errors
            }
            scheduleCompoundDiagramRefresh();
        });

        source.addEventListener('run-status', () => {
            htmx.ajax('GET', basePath + '/runs/' + runID + '/status-bar', {target: '#run-status-bar', swap: 'innerHTML'});
        });

        source.onerror = (err) => {
            console.error('event stream error', err);
        };

        // Second stream — cross-run metric-update events for the whole
        // execution group rooted at this run. Carries its own run_id per
        // event so child-run nodes in a compound graph can patch live.
        const metricsSource = new EventSource('/runs/' + runID + '/execution-group/metrics?follow=true');
        // Track the latest per-run totals so we can compute a live
        // group total for the Execution Group card header.
        const groupRunTotals = {};
        const updateGroupTotal = () => {
            const el = document.querySelector('[data-group-total-for="' + CSS.escape(runID) + '"]');
            if (!el) return;
            let dur = 0, input = 0, output = 0, cacheRead = 0, reasoning = 0, cost = 0;
            let any = false;
            for (const rt of Object.values(groupRunTotals)) {
                if (!rt) continue;
                any = true;
                if (rt.duration_ms > dur) dur = rt.duration_ms;
                const tk = rt.tokens || {};
                input += tk.input || 0;
                output += tk.output || 0;
                cacheRead += tk.cache_read || 0;
                reasoning += tk.reasoning || 0;
                const f = parseFloat(rt.cost_usd);
                if (!isNaN(f)) cost += f;
            }
            if (!any) return;
            const total = input + output + cacheRead + reasoning;
            el.textContent =
                MetricsFmt.duration(dur) + ' · ' +
                MetricsFmt.tokens(total) + ' tok · ' +
                MetricsFmt.cost(cost.toFixed(4));
        };
        // Update the tokens · cost line in the run-meta card. Rolls up
        // THIS run plus any child runs it spawned — the stream serves the
        // whole exec group so we just sum all run totals we've seen.
        const updateSubtreeTotal = () => {
            const el = document.querySelector('[data-subtree-total-for="' + CSS.escape(runID) + '"]');
            if (!el) return;
            let input = 0, output = 0, cacheRead = 0, reasoning = 0, cost = 0;
            let any = false;
            for (const rt of Object.values(groupRunTotals)) {
                if (!rt) continue;
                any = true;
                const tk = rt.tokens || {};
                input += tk.input || 0;
                output += tk.output || 0;
                cacheRead += tk.cache_read || 0;
                reasoning += tk.reasoning || 0;
                const f = parseFloat(rt.cost_usd);
                if (!isNaN(f)) cost += f;
            }
            if (!any) return;
            const total = input + output + cacheRead + reasoning;
            el.textContent =
                MetricsFmt.tokens(total) + ' tok · ' +
                MetricsFmt.cost(cost.toFixed(4));
        };
        metricsSource.addEventListener('metric-update', (ev) => {
            try {
                const payload = JSON.parse(ev.data);
                const payloadRunID = payload.run_id || runID;
                for (const [nodeID, update] of Object.entries(payload.nodes || {})) {
                    // Graph node IDs are qualified as runID::nodeID, using
                    // the source run_id from the payload so child-run nodes
                    // in the compound graph receive live updates.
                    const qualifiedID = payloadRunID + '::' + nodeID;
                    const metricsEl = document.querySelector(
                        '[data-node-id="' + CSS.escape(qualifiedID) + '"] .node-metrics'
                    );
                    if (metricsEl) {
                        const own = update.own || {};
                        const rollup = update.rollup || {};
                        const hasRollup = rollup.duration_ms > 0 || (rollup.tokens && rollup.tokens.total > 0);
                        const summary = {
                            duration_ms: hasRollup ? rollup.duration_ms : own.duration_ms,
                            tokens_total: hasRollup
                                ? (rollup.tokens && rollup.tokens.total)
                                : (own.tokens && own.tokens.total),
                            cost_usd: hasRollup ? rollup.cost_usd : own.cost_usd,
                            unpriced_event_count: hasRollup
                                ? rollup.unpriced_event_count
                                : own.unpriced_event_count,
                            rollup: hasRollup ? {
                                duration_ms: rollup.duration_ms,
                                tokens_total: rollup.tokens && rollup.tokens.total,
                                cost_usd: rollup.cost_usd,
                                unpriced_event_count: rollup.unpriced_event_count,
                            } : null,
                        };
                        metricsEl.outerHTML = renderNodeMetrics(summary, null);
                    }
                }
                if (payload.run_total) {
                    const rt = payload.run_total;
                    const rtEl = document.querySelector(
                        '[data-run-total-for="' + CSS.escape(payloadRunID) + '"]'
                    );
                    if (rtEl) {
                        rtEl.textContent =
                            MetricsFmt.duration(rt.duration_ms) + ' · ' +
                            MetricsFmt.tokens(rt.tokens && rt.tokens.total) + ' tok · ' +
                            MetricsFmt.cost(rt.cost_usd);
                    }
                    groupRunTotals[payloadRunID] = rt;
                    updateGroupTotal();
                    updateSubtreeTotal();
                }
            } catch (err) {
                console.error('metric-update parse failed', err);
            }
        });
        metricsSource.onerror = () => {
            // Suppress — metrics stream closes when every run in the group
            // reaches a terminal state; this is expected.
        };
    };

    // ─── Section 2: Graph Orchestration ────────────────────────────────
    let macroDiagramEl = null;
    let microDiagramEl = null;
    let compoundGraphText = '';
    let compoundDiagramTimer = null;
    let macroInitialized = false;
    let microInitialized = false;
    let runID = '';
    let basePath = '';
    let runView = null;

    // SSE-driven status overrides
    const sseStatusOverrides = new Map();

    const updateGraphNodeStatus = (nodeId, status) => {
        const qualifiedId = runID + '::' + nodeId;
        sseStatusOverrides.set(qualifiedId, status);
        if (microDiagramEl) {
            ElkGraph.updateNodeStatus(microDiagramEl, qualifiedId, status);
        }
    };

    const reapplySSEOverrides = () => {
        if (!microDiagramEl) return;
        for (const [qualId, status] of sseStatusOverrides) {
            ElkGraph.updateNodeStatus(microDiagramEl, qualId, status);
        }
    };

    const expandAccordionNode = (nodeId) => {
        const section = document.querySelector('[data-node-id="' + CSS.escape(nodeId) + '"]');
        if (!section) return;
        const alpineData = Alpine.$data(section);
        if (alpineData) {
            alpineData.expanded = true;
        }
        section.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
    };

    const populateBottomSheet = (nodeID) => {
        const container = document.getElementById('bottom-sheet-content');
        if (!container) return;
        while (container.firstChild) container.removeChild(container.firstChild);
        const transcript = document.querySelector('[data-node-transcript="' + CSS.escape(nodeID) + '"]');
        if (transcript) {
            container.innerHTML = transcript.innerHTML;
        } else {
            const empty = document.createElement('div');
            empty.className = 'text-muted text-sm';
            empty.textContent = 'No transcript yet.';
            container.appendChild(empty);
        }
    };

    // ─── Topology Splitting ────────────────────────────────────────────

    // Split a compound topology into macro (run-level) and micro (current run's nodes).
    function splitTopology(topology, currentRunID) {
        const runNodes = [];
        const microNodes = [];
        const runNodeIds = new Set();

        for (const n of topology.nodes) {
            if (!n.parent) {
                runNodes.push(n);
                runNodeIds.add(n.id);
            } else if (n.parent === currentRunID) {
                // Strip parent so ELK renders flat
                microNodes.push(Object.assign({}, n, { parent: '' }));
            }
        }

        // Macro edges: lift child-spawning edges to run level
        const macroEdgeMap = new Map(); // dedup key -> edge
        const microEdges = [];
        const microNodeIds = new Set(microNodes.map(n => n.id));

        for (const e of (topology.edges || [])) {
            const srcParent = getParent(topology, e.source);
            const tgtParent = getParent(topology, e.target);

            // Both endpoints in the micro run's children
            if (microNodeIds.has(e.source) && microNodeIds.has(e.target)) {
                microEdges.push(e);
                continue;
            }

            // Inter-run edge: lift source/target to their run-level parent
            const macroSrc = runNodeIds.has(e.source) ? e.source : srcParent;
            const macroTgt = runNodeIds.has(e.target) ? e.target : tgtParent;
            if (macroSrc && macroTgt && macroSrc !== macroTgt) {
                const key = macroSrc + '->' + macroTgt;
                if (!macroEdgeMap.has(key)) {
                    macroEdgeMap.set(key, {
                        id: key,
                        source: macroSrc,
                        target: macroTgt,
                        label: e.label || '',
                        isEscape: e.isEscape || false,
                    });
                }
            }
        }

        return {
            macro: { nodes: runNodes, edges: Array.from(macroEdgeMap.values()) },
            micro: { nodes: microNodes, edges: microEdges },
        };
    }

    function getParent(topology, nodeId) {
        for (const n of topology.nodes) {
            if (n.id === nodeId) return n.parent || nodeId;
        }
        return nodeId;
    }

    // ─── Click Handlers ────────────────────────────────────────────────

    // Walk up the DOM, opening any ancestor <details> so the target row
    // becomes visible. Safe for rows not inside a <details> (no-op).
    const expandAncestors = (el) => {
        let cursor = el;
        while (cursor) {
            if (cursor.tagName === 'DETAILS') cursor.open = true;
            cursor = cursor.parentElement;
        }
    };

    // In the unified Run tab, compound-graph clicks behave the same way as
    // micro-graph clicks: scroll to the corresponding row in the tree and,
    // for node-level clicks, open the transcript drawer. Run-container
    // clicks just open that run's <details> and scroll to it — no
    // cross-page navigation needed since everything is on one page.
    const macroClickHandler = (id) => {
        const sep = id.indexOf('::');
        if (sep === -1) {
            // Run container click — expand that run (and all ancestors)
            // in the tree + scroll.
            const target = document.getElementById('run-' + id);
            if (target) {
                expandAncestors(target);
                if (target.tagName === 'DETAILS') target.open = true;
                target.scrollIntoView({ behavior: 'smooth', block: 'center' });
                target.classList.add('flash-highlight');
                setTimeout(() => target.classList.remove('flash-highlight'), 1500);
            }
            return;
        }
        // Node click — defer to the micro-click handler's behaviour.
        microClickHandler(id);
    };

    // The right-panel detail card was removed in the unified Run view;
    // transcripts now open in the slide-over drawer via
    // window.__transcriptDrawer.openFor(runID, nodeID, basePath).
    // This shim remains so older callers don't throw.
    const loadNodeDetail = () => {};

    // Fetch per-node metrics from /runs/{id}/metrics and populate the
    // metrics drawer with the given node's own token/cost breakdown.
    async function openMetricsDrawer(targetRunID, nodeID) {
        try {
            const res = await fetch('/runs/' + encodeURIComponent(targetRunID) + '/metrics');
            if (!res.ok) return;
            const data = await res.json();
            const node = (data.nodes || {})[nodeID];
            if (!node) return;
            const b = (node.own && node.own.tokens) || {};
            document.getElementById('drawer-node-id').textContent = nodeID;
            document.getElementById('drawer-node-status').textContent = node.status || '';
            document.getElementById('drawer-duration').textContent = MetricsFmt.duration(node.own && node.own.duration_ms);
            document.getElementById('drawer-input').textContent = (b.input || 0).toLocaleString();
            document.getElementById('drawer-output').textContent = (b.output || 0).toLocaleString();
            document.getElementById('drawer-cache').textContent = (b.cache_read || 0).toLocaleString();
            document.getElementById('drawer-reasoning').textContent = (b.reasoning || 0).toLocaleString();
            document.getElementById('drawer-total').textContent = (b.total || 0).toLocaleString();
            document.getElementById('drawer-cost').textContent = MetricsFmt.cost(node.own && node.own.cost_usd);
            const unpriced = (node.own && node.own.unpriced_event_count) || 0;
            const row = document.getElementById('drawer-unpriced-row');
            if (unpriced > 0) {
                row.classList.remove('hidden');
                document.getElementById('drawer-unpriced').textContent = unpriced.toLocaleString();
            } else {
                row.classList.add('hidden');
            }
            window.dispatchEvent(new CustomEvent('open-drawer'));
        } catch (err) {
            console.error('openMetricsDrawer error', err);
        }
    }

    const microClickHandler = (qualifiedId) => {
        // qualifiedId is "runID::nodeID" or "runID::nodeID::N" (ForEach
        // iteration). Extract the run prefix and the rest.
        const firstSep = qualifiedId.indexOf('::');
        if (firstSep === -1) return;
        const targetRunID = qualifiedId.substring(0, firstSep);
        const nodeId = qualifiedId.substring(firstSep + 2);

        // Scroll to the corresponding node row in the tree (if present)
        // and flash it briefly. Open any ancestor <details> first so the
        // row is actually visible even when its containing run was
        // collapsed (e.g., clicking a node from another run via the
        // compound graph at the top).
        const escape = (s) => (window.CSS && CSS.escape) ? CSS.escape(s) : s.replace(/"/g, '\\"');
        const row = document.querySelector('[data-node-row="' + escape(qualifiedId) + '"]');
        if (row) {
            expandAncestors(row);
            row.scrollIntoView({ behavior: 'smooth', block: 'center' });
            row.classList.add('flash-highlight');
            setTimeout(() => row.classList.remove('flash-highlight'), 1500);
        }

        // Open the transcript drawer.
        if (window.__transcriptDrawer) {
            window.__transcriptDrawer.openFor(targetRunID, nodeId, basePath);
        }

        // Open the metrics drawer alongside the transcript drawer.
        openMetricsDrawer(targetRunID, nodeId);
    };

    // ─── Init & Refresh ────────────────────────────────────────────────

    let cachedSplit = null;

    const parseTopology = () => {
        const sourceEl = document.getElementById('compound-graph-source');
        if (!sourceEl) return null;
        try {
            const topology = JSON.parse(sourceEl.textContent);
            return splitTopology(topology, runID);
        } catch (err) {
            console.error('failed to parse compound graph', err);
            return null;
        }
    };

    const initMacroGraph = async () => {
        if (macroInitialized || !macroDiagramEl) return;
        if (!cachedSplit) cachedSplit = parseTopology();
        if (!cachedSplit || cachedSplit.macro.nodes.length <= 1) return;

        await ElkGraph.render(macroDiagramEl, cachedSplit.macro, {
            basePath: basePath,
            onNodeSelect: macroClickHandler,
            inline: true,
        });
        macroInitialized = true;
    };

    const initMicroGraph = async () => {
        if (microInitialized || !microDiagramEl) return;
        if (!cachedSplit) cachedSplit = parseTopology();
        if (!cachedSplit) return;

        await ElkGraph.render(microDiagramEl, cachedSplit.micro, {
            basePath: basePath,
            onNodeSelect: microClickHandler,
            inline: true,
        });
        microInitialized = true;
        reapplySSEOverrides();
    };

    const refreshCompoundDiagram = async () => {
        try {
            const response = await fetch('/runs/' + runID + '/compound-graph');
            if (!response.ok) return;
            const text = await response.text();
            if (text === compoundGraphText) return;
            compoundGraphText = text;
            const topology = JSON.parse(text);
            const split = splitTopology(topology, runID);

            cachedSplit = split;

            if (macroDiagramEl && macroInitialized && split.macro.nodes.length > 1) {
                await ElkGraph.update(macroDiagramEl, split.macro);
            }

            if (microDiagramEl && microInitialized) {
                if (microDiagramEl.offsetParent !== null) {
                    await ElkGraph.update(microDiagramEl, split.micro);
                    reapplySSEOverrides();
                } else {
                    microInitialized = false;
                }
            }
        } catch (err) {
            console.error('compound graph fetch error', err);
        }
    };

    const scheduleCompoundDiagramRefresh = () => {
        if (compoundDiagramTimer) return;
        compoundDiagramTimer = setTimeout(() => {
            compoundDiagramTimer = null;
            refreshCompoundDiagram();
        }, 800);
    };

    const observeVisibility = (el, initFn, isInitialized) => {
        if (!el) return;
        const check = () => {
            if (el.offsetParent !== null && !isInitialized()) {
                initFn();
                return;
            }
            if (!isInitialized()) {
                requestAnimationFrame(check);
            }
        };
        requestAnimationFrame(check);
    };

    // ─── Section 3: Swipe-to-Dismiss ──────────────────────────────────
    const initSwipeToDismiss = () => {
        const bottomSheet = document.querySelector('[role="dialog"][aria-modal="true"]');
        if (!bottomSheet) return;
        const panel = bottomSheet.querySelector('.bg-white.rounded-t-2xl');
        if (!panel) return;
        let startY = 0;
        let currentY = 0;
        panel.addEventListener('touchstart', (e) => {
            startY = e.touches[0].clientY;
            currentY = startY;
            panel.style.transition = 'none';
        }, { passive: true });
        panel.addEventListener('touchmove', (e) => {
            currentY = e.touches[0].clientY;
            const deltaY = currentY - startY;
            if (deltaY > 0) {
                panel.style.transform = 'translateY(' + deltaY + 'px)';
            }
        }, { passive: true });
        panel.addEventListener('touchend', () => {
            const deltaY = currentY - startY;
            panel.style.transition = 'transform 0.2s ease-out';
            if (deltaY > 100) {
                panel.style.transform = 'translateY(100%)';
                setTimeout(() => {
                    const tabData = Alpine.$data(runView);
                    if (tabData) tabData.activeNode = null;
                    panel.style.transform = '';
                }, 200);
            } else {
                panel.style.transform = 'translateY(0)';
            }
        });
    };

    // ─── Section 4: Bootstrap ─────────────────────────────────────────
    document.addEventListener('DOMContentLoaded', () => {
        // Click-to-copy for session-id pills. Delegated on document so it
        // covers pills rendered later via SSE (transcript-item events) too.
        document.addEventListener('click', async (ev) => {
            const pill = ev.target.closest && ev.target.closest('.session-pill');
            if (!pill) return;
            const id = pill.dataset.sessionId || pill.textContent.trim();
            if (!id) return;
            const original = pill.dataset.original || pill.textContent;
            pill.dataset.original = original;
            let label = 'Copied!';
            try {
                await navigator.clipboard.writeText(id);
            } catch (_) {
                label = 'Copy failed';
            }
            pill.textContent = label;
            pill.classList.add('session-pill--copied');
            clearTimeout(pill._copyTimer);
            pill._copyTimer = setTimeout(() => {
                pill.textContent = pill.dataset.original;
                pill.classList.remove('session-pill--copied');
            }, 1200);
        });

        // Lazy-load per-node metrics when a run row is expanded
        document.addEventListener('toggle', async (ev) => {
            const details = ev.target;
            if (!(details instanceof HTMLDetailsElement) || !details.open) return;
            const container = details.querySelector('[data-nodes-for]');
            if (!container || container.dataset.loaded === 'true') return;
            container.dataset.loaded = 'true';
            const rowRunID = container.dataset.nodesFor;
            try {
                const res = await fetch('/runs/' + encodeURIComponent(rowRunID) + '/metrics');
                if (!res.ok) throw new Error('HTTP ' + res.status);
                const data = await res.json();
                container.innerHTML = renderRunNodeSubtree(data);
            } catch (err) {
                container.innerHTML = '<div class="p-3 text-xs text-red-600">Failed to load metrics: ' + escapeHtml(err.message) + '</div>';
            }
        }, true);

        // Syntax highlighting for code blocks
        if (typeof hljs !== 'undefined') {
            hljs.highlightAll();
            // Also highlight code blocks loaded via HTMX
            document.body.addEventListener('htmx:afterSwap', (e) => {
                e.detail.target.querySelectorAll('pre code:not(.hljs)').forEach((el) => {
                    hljs.highlightElement(el);
                });
            });
        }

        // Lightbox that shows a single run's per-workflow DAG over the
        // top-of-page run-tree graph. Lazily created on first open;
        // populated by toggleRunNodeGraph below.
        let dagLightbox = null;
        function ensureDagLightbox() {
            if (dagLightbox) return dagLightbox;
            const box = document.createElement('div');
            box.id = 'dag-lightbox';
            box.className = 'dag-lightbox';
            box.hidden = true;
            box.innerHTML = ''
                + '<div class="dag-lightbox-backdrop" data-close="true"></div>'
                + '<div class="dag-lightbox-panel">'
                +   '<header class="dag-lightbox-head">'
                +     '<span class="dag-lightbox-title"></span>'
                +     '<button class="dag-lightbox-close" type="button" aria-label="Close" data-close="true">&times;</button>'
                +   '</header>'
                +   '<div class="dag-lightbox-canvas"></div>'
                + '</div>';
            document.body.appendChild(box);
            box.addEventListener('click', (ev) => {
                const t = ev.target;
                if (t instanceof HTMLElement && t.dataset.close === 'true') {
                    closeDagLightbox();
                }
            });
            dagLightbox = box;
            return box;
        }
        function closeDagLightbox() {
            if (!dagLightbox) return;
            dagLightbox.hidden = true;
            dagLightbox.dataset.runId = '';
            const canvas = dagLightbox.querySelector('.dag-lightbox-canvas');
            if (canvas) canvas.innerHTML = '';
        }
        document.addEventListener('keydown', (ev) => {
            if (ev.key === 'Escape' && dagLightbox && !dagLightbox.hidden) {
                closeDagLightbox();
            }
        });

        // Open (or toggle off) the lightbox for a run id. The topology
        // JSON is embedded in the page as <script id="topo-<runId>">,
        // emitted by the run_node template. Called from the run-tree
        // graph's onNodeSelect.
        function toggleRunNodeGraph(runId) {
            const source = document.getElementById('topo-' + runId);
            if (!source) return;
            const box = ensureDagLightbox();
            if (!box.hidden && box.dataset.runId === runId) {
                closeDagLightbox();
                return;
            }
            const canvas = box.querySelector('.dag-lightbox-canvas');
            const title = box.querySelector('.dag-lightbox-title');
            canvas.innerHTML = '';
            let wfid = '';
            const section = document.querySelector('#run-doc .run-node[data-run-id="' + runId + '"]');
            if (section) wfid = section.dataset.workflowId || '';
            title.textContent = (wfid ? wfid.toUpperCase() + ' · ' : '') + runId;
            box.hidden = false;
            box.dataset.runId = runId;
            if (window.ElkGraph) {
                const topo = JSON.parse(source.textContent);
                window.ElkGraph.render(canvas, topo, { inline: true });
            }
        }

        // Generic ELK graph canvases (workflow detail pages, etc.)
        // Defer rendering until visible (may be in a hidden tab)
        document.querySelectorAll('.elk-graph-canvas').forEach((el) => {
            const sourceEl = document.getElementById(el.dataset.sourceId);
            if (!sourceEl) return;
            let rendered = false;
            const tryRender = async () => {
                if (rendered) return;
                if (el.offsetParent === null) return;
                rendered = true;
                const topology = JSON.parse(sourceEl.textContent);
                const opts = {
                    basePath: el.dataset.basePath || '',
                    selectedWorkflow: el.dataset.selectedWorkflow || '',
                    inline: el.dataset.inline === 'true',
                };
                // The run-view's top-of-page run-tree graph routes node
                // clicks to toggle that run's per-workflow DAG inline in
                // the document body, rather than the default drill-down.
                if (el.id === 'run-compound-diagram') {
                    opts.onNodeSelect = toggleRunNodeGraph;
                }
                await ElkGraph.render(el, topology, opts);
            };
            tryRender();
            if (!rendered) {
                const obs = new IntersectionObserver((entries) => {
                    if (entries[0].isIntersecting) { tryRender(); obs.disconnect(); }
                });
                obs.observe(el);
            }
        });

        // Run detail page
        runView = document.getElementById('run-view');
        if (runView && runView.dataset.runId) {
            runID = runView.dataset.runId;
            basePath = runView.dataset.basePath || '';

            macroDiagramEl = document.getElementById('macro-diagram');
            microDiagramEl = document.getElementById('micro-diagram');
            const compoundGraphSource = document.getElementById('compound-graph-source');
            compoundGraphText = compoundGraphSource ? compoundGraphSource.textContent.trim() : '';

            connectToStream(runID, basePath);
            observeVisibility(macroDiagramEl, initMacroGraph, () => macroInitialized);
            observeVisibility(microDiagramEl, initMicroGraph, () => microInitialized);
            initSwipeToDismiss();

            // Restore selection from hash — open drawer for #node=<runID>::<nodeID>.
            // Legacy hashes with just a bare node ID are treated as nodes in
            // the current run. Bail after ~2s if the drawer never appears.
            const hashNode = new URLSearchParams(location.hash.substring(1)).get('node');
            if (hashNode) {
                let attempts = 0;
                const tryOpen = () => {
                    if (!window.__transcriptDrawer) {
                        if (++attempts > 20) return;
                        setTimeout(tryOpen, 100);
                        return;
                    }
                    let targetRun = runID;
                    let targetNode = hashNode;
                    const sep = hashNode.indexOf('::');
                    if (sep !== -1) {
                        targetRun = hashNode.substring(0, sep);
                        targetNode = hashNode.substring(sep + 2);
                    }
                    window.__transcriptDrawer.openFor(targetRun, targetNode, basePath);
                };
                tryOpen();
            }
        }
    });
})();
