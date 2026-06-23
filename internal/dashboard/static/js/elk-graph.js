(() => {
    'use strict';

    // Design tokens
    const ACCENT = '#1a6b5a';
    const INK = '#1b2631';
    const MUTED = '#6b7c8f';
    const SURFACE = '#f6f8fa';
    const EDGE_COLOR = '#374a5e';
    const ESCAPE_COLOR = '#d97706';
    const FONT = "'DM Sans', sans-serif";

    const STATUS_STYLES = {
        pending:            { fill: '#f6f8fa', stroke: '#e1e4e8', text: '#1b2631' },
        running:            { fill: '#dbeafe', stroke: '#3b82f6', text: '#1e40af' },
        completed:          { fill: '#d1fae5', stroke: '#10b981', text: '#065f46' },
        failed:             { fill: '#fee2e2', stroke: '#ef4444', text: '#991b1b' },
        paused:             { fill: '#fef3c7', stroke: '#f59e0b', text: '#92400e' },
        awaiting_approval:  { fill: '#fef3c7', stroke: '#f59e0b', text: '#92400e' },
    };

    const DEFAULT_STYLE = STATUS_STYLES.pending;

    const NODE_WIDTH = 200;
    const NODE_HEIGHT = 40;
    const NODE_HEIGHT_WITH_PROMPT = 56;
    const NODE_HEIGHT_WITH_THREE_LINES = 72; // label + description + subtitle
    const NODE_METRICS_EXTRA = 30; // extra height when a node carries metrics
    const PARENT_PADDING = 20;
    const MIN_HEIGHT = 500;

    // Instance storage keyed by container element
    const instances = new Map();

    function statusStyle(status) {
        return STATUS_STYLES[status] || DEFAULT_STYLE;
    }

    // Build subtitle text from node metadata
    function subtitleText(node, isCollapsedParent, childCount) {
        const parts = [];
        if (node.status) parts.push(node.status);
        if (node.decision) parts.push(node.decision);
        else if (node.attempts && node.attempts > 1) parts.push(node.attempts + ' attempts');
        if (isCollapsedParent) parts.push(childCount + ' nodes');
        return parts.join(' \u00b7 ');
    }

    // Convert TopologyGraph to ELK graph format
    function toElkGraph(topology, direction) {
        direction = direction || 'DOWN';
        const nodeMap = new Map();
        const childrenOf = new Map(); // parentId -> [childNodes]

        // Index nodes by id and group by parent
        for (const n of topology.nodes) {
            nodeMap.set(n.id, n);
            const parentId = n.parent || '__root__';
            if (!childrenOf.has(parentId)) childrenOf.set(parentId, []);
            childrenOf.get(parentId).push(n);
        }

        function buildElkNode(node) {
            const children = childrenOf.get(node.id);
            const isParent = children && children.length > 0;

            const elkNode = {
                id: node.id,
                labels: [{ text: node.label || node.id }],
            };

            if (isParent) {
                // Parent node: let ELK size based on children, set internal layout
                elkNode.children = children.map(buildElkNode);
                elkNode.layoutOptions = {
                    'elk.algorithm': 'layered',
                    'elk.direction': direction,
                    'elk.spacing.nodeNode': '40',
                    'elk.layered.spacing.nodeNodeBetweenLayers': '60',
                    'elk.padding': '[top=' + PARENT_PADDING + ',left=' + PARENT_PADDING +
                        ',bottom=' + (node.prompt ? 40 : PARENT_PADDING) + ',right=' + PARENT_PADDING + ']',
                    'elk.hierarchyHandling': 'INCLUDE_CHILDREN',
                };
                // Internal edges (edges where both source and target are children of this node)
                const childIds = new Set(children.map(c => c.id));
                const internalEdges = (topology.edges || []).filter(
                    e => childIds.has(e.source) && childIds.has(e.target)
                );
                if (internalEdges.length > 0) {
                    elkNode.edges = internalEdges.map(edgeToElk);
                }
            } else {
                elkNode.width = NODE_WIDTH;
                const hasDescription = !!node.description;
                const hasSubtitle = !!node.subtitle;
                const hasSecondary = !!(node.prompt || node.child_run_id || node.workflow_name || node.workflow || hasSubtitle);
                if (hasDescription && hasSubtitle) {
                    elkNode.height = NODE_HEIGHT_WITH_THREE_LINES;
                } else if (hasSecondary) {
                    elkNode.height = NODE_HEIGHT_WITH_PROMPT;
                } else {
                    elkNode.height = NODE_HEIGHT;
                }
                if (node.metrics) {
                    elkNode.height += NODE_METRICS_EXTRA;
                }
                // Subworkflow nodes render a stack of four rects offset 3/6/9px
                // down-right; reserve that extra padding in the layout so
                // stack shadows don't collide with neighboring nodes.
                if (node.kind === 'subworkflow' || node.kind === 'subworkflow-foreach') {
                    elkNode.width += 10;
                    elkNode.height += 10;
                }
            }

            return elkNode;
        }

        function edgeToElk(e) {
            const elkEdge = {
                id: e.id || (e.source + '->' + e.target),
                sources: [e.source],
                targets: [e.target],
            };
            if (e.label) {
                elkEdge.labels = [{ text: e.label, layoutOptions: { 'elk.edgeLabels.placement': 'CENTER' } }];
            }
            return elkEdge;
        }

        // Top-level children (nodes without parent)
        const topLevel = childrenOf.get('__root__') || [];

        // Top-level edges (both endpoints are top-level or cross-parent)
        const topLevelIds = new Set(topLevel.map(n => n.id));
        // Collect all internal edge ids to exclude them from top-level
        const internalEdgeIds = new Set();
        for (const [parentId, children] of childrenOf) {
            if (parentId === '__root__') continue;
            const childIds = new Set(children.map(c => c.id));
            for (const e of (topology.edges || [])) {
                if (childIds.has(e.source) && childIds.has(e.target)) {
                    internalEdgeIds.add(e.id || (e.source + '->' + e.target));
                }
            }
        }

        const topLevelEdges = (topology.edges || []).filter(e => {
            const eid = e.id || (e.source + '->' + e.target);
            return !internalEdgeIds.has(eid);
        });

        return {
            id: 'root',
            layoutOptions: {
                'elk.algorithm': 'layered',
                'elk.direction': direction,
                'elk.edgeRouting': 'SPLINES',
                'elk.spacing.nodeNode': '60',
                'elk.layered.spacing.nodeNodeBetweenLayers': '80',
                'elk.edgeLabels.placement': 'CENTER',
                'elk.hierarchyHandling': 'INCLUDE_CHILDREN',
            },
            children: topLevel.map(buildElkNode),
            edges: topLevelEdges.map(edgeToElk),
        };
    }

    // Generate SVG path from ELK edge sections using D3 curveBasis
    function edgePath(edge) {
        const sections = edge.sections || [];
        if (sections.length === 0) return '';
        const allPoints = [];
        for (const section of sections) {
            allPoints.push([section.startPoint.x, section.startPoint.y]);
            if (section.bendPoints) {
                for (const bp of section.bendPoints) {
                    allPoints.push([bp.x, bp.y]);
                }
            }
            allPoints.push([section.endPoint.x, section.endPoint.y]);
        }
        return d3.line().curve(d3.curveBasis)(allPoints);
    }

    // Collect all laid-out nodes recursively with absolute positions
    function collectNodes(elkNode, offsetX, offsetY, topoNodeMap, childrenOf) {
        const results = [];
        const children = elkNode.children || [];
        for (const child of children) {
            const ax = offsetX + (child.x || 0);
            const ay = offsetY + (child.y || 0);
            const topoNode = topoNodeMap.get(child.id) || {};
            const hasTopoChildren = childrenOf.has(child.id);
            const isParent = (child.children && child.children.length > 0);
            results.push({
                id: child.id,
                x: ax,
                y: ay,
                width: child.width || NODE_WIDTH,
                height: child.height || NODE_HEIGHT,
                label: topoNode.label || child.id,
                description: topoNode.description || '',
                subtitle: topoNode.subtitle || '',
                status: topoNode.status || '',
                current: topoNode.current || false,
                decision: topoNode.decision || '',
                attempts: topoNode.attempts || 0,
                prompt: topoNode.prompt || '',
                kind: topoNode.kind || '',
                child_run_id: topoNode.child_run_id || '',
                workflow: topoNode.workflow || '',
                workflow_name: topoNode.workflow_name || '',
                metrics: topoNode.metrics || null,
                isParent: isParent,
                isCollapsedParent: hasTopoChildren && !isParent,
                childCount: (childrenOf.get(child.id) || []).length,
            });
            if (child.children && child.children.length > 0) {
                results.push(...collectNodes(child, ax, ay, topoNodeMap, childrenOf));
            }
        }
        return results;
    }

    // Collect all laid-out edges recursively with absolute offsets
    function collectEdges(elkNode, offsetX, offsetY, topoEdgeMap) {
        const results = [];
        const edges = elkNode.edges || [];
        for (const edge of edges) {
            const topoEdge = topoEdgeMap.get(edge.id) || {};
            results.push({
                id: edge.id,
                sections: (edge.sections || []).map(s => ({
                    startPoint: { x: s.startPoint.x + offsetX, y: s.startPoint.y + offsetY },
                    endPoint: { x: s.endPoint.x + offsetX, y: s.endPoint.y + offsetY },
                    bendPoints: (s.bendPoints || []).map(bp => ({
                        x: bp.x + offsetX, y: bp.y + offsetY,
                    })),
                })),
                label: topoEdge.label || '',
                isEscape: topoEdge.isEscape || false,
                isSelfLoop: topoEdge.source === topoEdge.target,
            });
        }
        for (const child of (elkNode.children || [])) {
            const cx = offsetX + (child.x || 0);
            const cy = offsetY + (child.y || 0);
            results.push(...collectEdges(child, cx, cy, topoEdgeMap));
        }
        return results;
    }

    // Compute bounding box of all nodes
    function boundingBox(nodes) {
        if (nodes.length === 0) return { x: 0, y: 0, width: 400, height: 300 };
        let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
        for (const n of nodes) {
            minX = Math.min(minX, n.x);
            minY = Math.min(minY, n.y);
            maxX = Math.max(maxX, n.x + n.width);
            maxY = Math.max(maxY, n.y + n.height);
        }
        return { x: minX, y: minY, width: maxX - minX, height: maxY - minY };
    }

    // Fit the graph content to the container with padding
    function fitToView(svg, zoomBehavior, nodes, transition) {
        const containerEl = svg.node().parentElement;
        const cw = containerEl.clientWidth;
        const ch = containerEl.clientHeight;
        const bb = boundingBox(nodes);
        const pad = 32;

        const scaleX = (cw - 2 * pad) / bb.width;
        const scaleY = (ch - 2 * pad) / bb.height;
        const scale = Math.min(scaleX, scaleY, 1.5); // don't zoom in too much
        const clampedScale = Math.max(0.2, Math.min(4, scale));

        const tx = (cw - bb.width * clampedScale) / 2 - bb.x * clampedScale;
        const ty = (ch - bb.height * clampedScale) / 2 - bb.y * clampedScale;

        const transform = d3.zoomIdentity.translate(tx, ty).scale(clampedScale);

        if (transition) {
            svg.transition().duration(400).ease(d3.easeCubicOut).call(zoomBehavior.transform, transform);
        } else {
            svg.call(zoomBehavior.transform, transform);
        }
    }

    // Build control bar (zoom +/-, fit)
    function buildControls(container, svg, zoomBehavior, allNodes) {
        const bar = document.createElement('div');
        bar.style.cssText = 'position:absolute;top:8px;right:8px;display:flex;gap:4px;z-index:10;';

        function makeBtn(text, onClick) {
            const btn = document.createElement('button');
            btn.textContent = text;
            btn.style.cssText =
                "padding:2px 8px;border:1px solid #e1e4e8;border-radius:4px;" +
                "background:white;font-size:12px;font-family:'DM Sans',sans-serif;" +
                "cursor:pointer;color:" + INK + ";";
            btn.addEventListener('click', onClick);
            bar.appendChild(btn);
            return btn;
        }

        makeBtn('+', () => svg.transition().duration(200).call(zoomBehavior.scaleBy, 1.3));
        makeBtn('\u2212', () => svg.transition().duration(200).call(zoomBehavior.scaleBy, 0.77));
        makeBtn('Fit', () => fitToView(svg, zoomBehavior, allNodes, true));

        container.style.position = 'relative';
        container.appendChild(bar);
    }

    // Render the full graph SVG
    function renderSVG(container, layoutRoot, topology, options) {
        options = options || {};
        // Build lookup maps
        const topoNodeMap = new Map();
        const childrenOf = new Map();
        for (const n of topology.nodes) {
            topoNodeMap.set(n.id, n);
            const parentId = n.parent || '__root__';
            if (!childrenOf.has(parentId)) childrenOf.set(parentId, []);
            childrenOf.get(parentId).push(n);
        }
        const topoEdgeMap = new Map();
        for (const e of (topology.edges || [])) {
            const eid = e.id || (e.source + '->' + e.target);
            topoEdgeMap.set(eid, e);
        }

        // Collect positioned elements
        const allNodes = collectNodes(layoutRoot, 0, 0, topoNodeMap, childrenOf);
        const allEdges = collectEdges(layoutRoot, 0, 0, topoEdgeMap);

        // Build node position lookup for cross-hierarchy edge fixup
        const nodePositions = new Map();
        for (const n of allNodes) {
            nodePositions.set(n.id, n);
        }

        // Fix cross-hierarchy edges: when source or target is a child node
        // inside a compound, ELK routes from the compound boundary. Override
        // the start/end points to connect directly to the actual child node.
        for (const edge of allEdges) {
            const topoEdge = topoEdgeMap.get(edge.id);
            if (!topoEdge || edge.sections.length === 0) continue;

            const srcNode = nodePositions.get(topoEdge.source);
            const tgtNode = nodePositions.get(topoEdge.target);
            if (!srcNode || !tgtNode) continue;

            const srcTopo = topoNodeMap.get(topoEdge.source);
            const tgtTopo = topoNodeMap.get(topoEdge.target);
            const srcIsChild = srcTopo && srcTopo.parent;
            const tgtIsChild = tgtTopo && tgtTopo.parent;

            // Only fix edges that cross hierarchy (one endpoint is nested)
            if (!srcIsChild && !tgtIsChild) continue;
            // Skip fully internal edges (both in same parent)
            if (srcIsChild && tgtIsChild && srcTopo.parent === tgtTopo.parent) continue;

            const firstSection = edge.sections[0];
            const lastSection = edge.sections[edge.sections.length - 1];

            const isVertical = (options.direction || 'DOWN') === 'DOWN' || options.direction === 'UP';
            if (isVertical) {
                // Override start point to source node's bottom center
                firstSection.startPoint = {
                    x: srcNode.x + srcNode.width / 2,
                    y: srcNode.y + srcNode.height,
                };
                // Override end point to target node's top center
                lastSection.endPoint = {
                    x: tgtNode.x + tgtNode.width / 2,
                    y: tgtNode.y,
                };
            } else {
                // Override start point to source node's right center
                firstSection.startPoint = {
                    x: srcNode.x + srcNode.width,
                    y: srcNode.y + srcNode.height / 2,
                };
                // Override end point to target node's left center
                lastSection.endPoint = {
                    x: tgtNode.x,
                    y: tgtNode.y + tgtNode.height / 2,
                };
            }
            // Clear bend points for clean direct path
            for (const s of edge.sections) {
                s.bendPoints = [];
            }
        }

        // Clear container
        container.innerHTML = '';
        const inline = !!options.inline;

        if (!inline) {
            const minHeight = options.minHeight || MIN_HEIGHT;
            container.style.minHeight = minHeight + 'px';
        }

        const containerWidth = container.clientWidth || 600;

        const svg = d3.select(container).append('svg')
            .attr('width', '100%')
            .style('font-family', FONT);

        // Defs: arrowhead markers
        const defs = svg.append('defs');

        defs.append('marker')
            .attr('id', 'arrowhead')
            .attr('viewBox', '0 0 10 7')
            .attr('refX', 10)
            .attr('refY', 3.5)
            .attr('markerWidth', 8)
            .attr('markerHeight', 6)
            .attr('orient', 'auto')
            .append('path')
            .attr('d', 'M0,0 L10,3.5 L0,7 Z')
            .attr('fill', EDGE_COLOR);

        defs.append('marker')
            .attr('id', 'arrowhead-escape')
            .attr('viewBox', '0 0 10 7')
            .attr('refX', 10)
            .attr('refY', 3.5)
            .attr('markerWidth', 8)
            .attr('markerHeight', 6)
            .attr('orient', 'auto')
            .append('path')
            .attr('d', 'M0,0 L10,3.5 L0,7 Z')
            .attr('fill', ESCAPE_COLOR);

        const g = svg.append('g').attr('class', 'graph-content');

        // Render parent (compound) node backgrounds first so edges draw on top
        const parentGroup = g.append('g').attr('class', 'parent-backgrounds');
        for (const node of allNodes) {
            if (!node.isParent) continue;
            const style = statusStyle(node.status);
            const pg = parentGroup.append('g')
                .attr('class', 'graph-parent')
                .attr('data-node-id', node.id)
                .attr('transform', 'translate(' + node.x + ',' + node.y + ')');

            const isSubworkflow = node.kind === 'subworkflow' || node.kind === 'subworkflow-foreach';

            // Subworkflow compound nodes render as a stack of four to signal
            // "this is a whole nested sub-workflow". Three back-rects peek
            // out from the bottom-right at offsets 3/6/9.
            if (isSubworkflow) {
                for (let i = 3; i >= 1; i--) {
                    const offset = i * 3;
                    pg.append('rect')
                        .attr('width', Math.max(0, node.width - offset))
                        .attr('height', Math.max(0, node.height - offset))
                        .attr('rx', 8)
                        .attr('x', offset)
                        .attr('y', offset)
                        .attr('fill', style.fill)
                        .attr('stroke', style.stroke)
                        .attr('stroke-width', 1)
                        .attr('opacity', 0.3 + (4 - i) * 0.15);
                }
            }

            const bgW = isSubworkflow ? Math.max(0, node.width - 9) : node.width;
            const bgH = isSubworkflow ? Math.max(0, node.height - 9) : node.height;
            pg.append('rect')
                .attr('width', bgW)
                .attr('height', bgH)
                .attr('rx', 8)
                .attr('fill', style.fill)
                .attr('stroke', node.current ? INK : style.stroke)
                .attr('stroke-width', node.current ? 2.5 : 1.5)
                .attr('stroke-dasharray', isSubworkflow ? 'none' : (node.current ? 'none' : '6,3'));

            // Parent label at bottom
            const hasSecondary = !!(node.child_run_id || node.workflow_name || node.prompt);
            const parentLabelY = hasSecondary ? bgH - 22 : bgH - 10;
            pg.append('text')
                .attr('x', bgW / 2)
                .attr('y', parentLabelY)
                .attr('text-anchor', 'middle')
                .attr('font-size', '11px')
                .attr('font-weight', node.current ? '600' : '400')
                .attr('font-family', FONT)
                .attr('fill', node.current ? INK : MUTED)
                .text(node.label);

            // Secondary line — for subworkflow parents prefer child run ID,
            // falling back to workflow name. For non-subworkflow compound
            // parents, show the prompt.
            let secondary = '';
            let secondaryMono = false;
            if (isSubworkflow) {
                secondary = node.child_run_id || node.workflow_name || node.workflow || '';
                secondaryMono = !!node.child_run_id;
            } else {
                secondary = node.prompt;
            }
            if (secondary) {
                pg.append('text')
                    .attr('x', bgW / 2)
                    .attr('y', parentLabelY + 14)
                    .attr('text-anchor', 'middle')
                    .attr('font-size', '10px')
                    .attr('font-family', secondaryMono ? 'ui-monospace, SFMono-Regular, Menlo, monospace' : FONT)
                    .attr('fill', MUTED)
                    .attr('opacity', 0.9)
                    .text(truncateLabel(secondary, bgW - 30));
            }
        }

        // Render edges (above parent backgrounds, below leaf nodes)
        const edgeGroup = g.append('g').attr('class', 'edges');
        for (const edge of allEdges) {
            const eg = edgeGroup.append('g').attr('class', 'edge');

            let visiblePath = null;

            if (edge.isSelfLoop) {
                // Self-loop: small curved arrow on right side
                const node = allNodes.find(n => {
                    const topoEdge = topoEdgeMap.get(edge.id);
                    return topoEdge && n.id === topoEdge.source;
                });
                if (node) {
                    const cx = node.x + node.width;
                    const cy = node.y + node.height / 2;
                    const r = 16;
                    const d = 'M' + cx + ',' + (cy - 10) +
                        ' C' + (cx + r * 2) + ',' + (cy - 10) +
                        ' ' + (cx + r * 2) + ',' + (cy + 10) +
                        ' ' + cx + ',' + (cy + 10);
                    visiblePath = eg.append('path')
                        .attr('d', d)
                        .attr('fill', 'none')
                        .attr('stroke', edge.isEscape ? ESCAPE_COLOR : EDGE_COLOR)
                        .attr('stroke-width', edge.isEscape ? 1.5 : 1.8)
                        .attr('stroke-dasharray', edge.isEscape ? '5,3' : 'none')
                        .attr('marker-end', edge.isEscape ? 'url(#arrowhead-escape)' : 'url(#arrowhead)');
                    // Wide invisible hit area
                    eg.append('path')
                        .attr('d', d)
                        .attr('fill', 'none')
                        .attr('stroke', 'transparent')
                        .attr('stroke-width', 20)
                        .style('cursor', 'pointer');
                }
            } else {
                const pathD = edgePath(edge);
                if (pathD) {
                    visiblePath = eg.append('path')
                        .attr('d', pathD)
                        .attr('fill', 'none')
                        .attr('stroke', edge.isEscape ? ESCAPE_COLOR : EDGE_COLOR)
                        .attr('stroke-width', edge.isEscape ? 1.5 : 1.8)
                        .attr('stroke-dasharray', edge.isEscape ? '5,3' : 'none')
                        .attr('marker-end', edge.isEscape ? 'url(#arrowhead-escape)' : 'url(#arrowhead)');
                    // Wide invisible hit area
                    eg.append('path')
                        .attr('d', pathD)
                        .attr('fill', 'none')
                        .attr('stroke', 'transparent')
                        .attr('stroke-width', 20)
                        .style('cursor', 'pointer');
                }
            }

            // Edge label (hidden by default, shown on hover)
            let labelEl = null;
            if (edge.label) {
                const sections = edge.sections || [];
                let lx = 0, ly = 0;
                if (sections.length > 0) {
                    const s = sections[0];
                    const allPts = [s.startPoint];
                    if (s.bendPoints) allPts.push(...s.bendPoints);
                    allPts.push(s.endPoint);
                    const mid = Math.floor(allPts.length / 2);
                    lx = allPts[mid].x;
                    ly = allPts[mid].y;
                }
                // Label background pill
                const labelBg = eg.append('rect')
                    .attr('x', lx - 40)
                    .attr('y', ly - 18)
                    .attr('width', 80)
                    .attr('height', 18)
                    .attr('rx', 4)
                    .attr('fill', 'white')
                    .attr('stroke', '#e1e4e8')
                    .attr('stroke-width', 0.5)
                    .attr('opacity', 0)
                    .attr('class', 'edge-label-bg')
                    .style('pointer-events', 'none');
                labelEl = eg.append('text')
                    .attr('x', lx)
                    .attr('y', ly - 6)
                    .attr('text-anchor', 'middle')
                    .attr('font-size', '10px')
                    .attr('font-family', FONT)
                    .attr('fill', MUTED)
                    .attr('opacity', 0)
                    .attr('class', 'edge-label')
                    .style('pointer-events', 'none')
                    .text(edge.label);
                // Size the background to fit the text after render
                requestAnimationFrame(() => {
                    const bbox = labelEl.node().getBBox();
                    labelBg
                        .attr('x', bbox.x - 4)
                        .attr('y', bbox.y - 2)
                        .attr('width', bbox.width + 8)
                        .attr('height', bbox.height + 4);
                });
            }

            // Hover: fade label in, highlight source/target nodes
            const topoEdge = topoEdgeMap.get(edge.id);
            eg.on('mouseenter', function () {
                // Show label
                d3.select(this).selectAll('.edge-label, .edge-label-bg')
                    .transition().duration(150).attr('opacity', 1);
                // Highlight edge path
                if (visiblePath) {
                    visiblePath.attr('stroke-width', edge.isEscape ? 2.5 : 3);
                }
                // Highlight source/target nodes
                if (topoEdge) {
                    nodeGroup.selectAll('.graph-node')
                        .filter(function () {
                            const nid = d3.select(this).attr('data-node-id');
                            return nid === topoEdge.source || nid === topoEdge.target;
                        })
                        .select('rect')
                        .attr('stroke-width', 3);
                }
            }).on('mouseleave', function () {
                d3.select(this).selectAll('.edge-label, .edge-label-bg')
                    .transition().duration(150).attr('opacity', 0);
                if (visiblePath) {
                    visiblePath.attr('stroke-width', edge.isEscape ? 1.5 : 1.8);
                }
                if (topoEdge) {
                    nodeGroup.selectAll('.graph-node')
                        .filter(function () {
                            const nid = d3.select(this).attr('data-node-id');
                            return nid === topoEdge.source || nid === topoEdge.target;
                        })
                        .select('rect')
                        .attr('stroke-width', 1.5);
                }
            });
        }

        // Render leaf nodes (above edges, parents already rendered)
        const nodeGroup = g.append('g').attr('class', 'nodes');
        for (const node of allNodes) {
            if (node.isParent) continue; // already rendered in parent-backgrounds
            const style = statusStyle(node.status);
            const ng = nodeGroup.append('g')
                .attr('class', 'graph-node')
                .attr('data-node-id', node.id)
                .attr('data-status', node.status || '')
                .attr('transform', 'translate(' + node.x + ',' + node.y + ')');

            // Tooltip with full label and status
            const tooltipParts = [node.label];
            if (node.status) tooltipParts.push('Status: ' + node.status);
            if (node.workflow_name) tooltipParts.push('Workflow: ' + node.workflow_name + ' (' + node.workflow + ')');
            else if (node.workflow) tooltipParts.push('Workflow: ' + node.workflow);
            if (node.child_run_id) tooltipParts.push('Child run: ' + node.child_run_id);
            if (node.prompt) tooltipParts.push(node.prompt);
            ng.append('title').text(tooltipParts.join('\n'));

            const isSubworkflow = node.kind === 'subworkflow' || node.kind === 'subworkflow-foreach';
            const isIteration = node.kind === 'foreach-iteration';

            // Subworkflow nodes render as a stack of four rects to suggest
            // "this contains a whole sub-workflow of its own". We reserved
            // 10px extra width/height in the ELK layout (see toElkGraph)
            // so the stack shadows fit within the node's allocated space
            // without colliding with neighbors.
            //
            // Layout: main rect takes the top-left NODE_WIDTH × NODE_HEIGHT
            // area; three back-rects peek out from the bottom-right at
            // offsets 3/6/9.
            const mainW = isSubworkflow ? node.width - 9 : node.width;
            const mainH = isSubworkflow ? node.height - 9 : node.height;
            if (isSubworkflow) {
                for (let i = 3; i >= 1; i--) {
                    const offset = i * 3;
                    ng.append('rect')
                        .attr('width', mainW)
                        .attr('height', mainH)
                        .attr('rx', 8)
                        .attr('x', offset)
                        .attr('y', offset)
                        .attr('fill', style.fill)
                        .attr('stroke', style.stroke)
                        .attr('stroke-width', 1)
                        .attr('opacity', 0.3 + (4 - i) * 0.15);
                }
            }

            // Background rect (primary face of the stack)
            const mainRect = ng.append('rect')
                .attr('width', mainW)
                .attr('height', mainH)
                .attr('rx', 8)
                .attr('fill', style.fill)
                .attr('stroke', style.stroke)
                .attr('stroke-width', node.current ? 3 : 1.5);

            // Iteration nodes use a dashed border to suggest "instance of a loop".
            if (isIteration) {
                mainRect.attr('stroke-dasharray', '4 3');
            }

            if (node.current) {
                mainRect.attr('stroke', INK);
            }

            // Description line (e.g. "Spec Implementation" under the workflow
            // name "Plan and Build"). Subtitle line (mono, run id). Either,
            // both, or neither may be present.
            const hasDescription = !!node.description;
            const hasSubtitle = !!node.subtitle;
            // Legacy single-secondary path: when there's no Description, we
            // fall back to the existing subworkflow/iteration/prompt logic so
            // workflow-node graphs still render correctly.
            let legacySecondary = '';
            let legacySecondaryMono = false;
            if (!hasDescription) {
                if (hasSubtitle) {
                    legacySecondary = node.subtitle;
                    legacySecondaryMono = true;
                } else if (isSubworkflow || isIteration) {
                    legacySecondary = node.child_run_id || node.workflow_name || node.workflow || '';
                    legacySecondaryMono = !!node.child_run_id;
                } else {
                    legacySecondary = node.prompt;
                }
            }
            const hasLegacySecondary = !!legacySecondary;
            const threeLines = hasDescription && hasSubtitle;
            const twoLines = !threeLines && (hasDescription || hasLegacySecondary);
            // Stack the lines vertically inside the node.
            let labelY;
            if (threeLines) labelY = 14;
            else if (twoLines) labelY = 16;
            else labelY = mainH / 2 + 1;

            ng.append('text')
                .attr('x', mainW / 2)
                .attr('y', labelY)
                .attr('text-anchor', 'middle')
                .attr('dominant-baseline', 'middle')
                .attr('font-size', '13px')
                .attr('font-weight', '600')
                .attr('font-family', FONT)
                .attr('fill', style.text)
                .attr('class', 'node-label')
                .text(truncateLabel(node.label, mainW - 20));

            let lineY = labelY + 18;
            if (hasDescription) {
                ng.append('text')
                    .attr('x', mainW / 2)
                    .attr('y', lineY)
                    .attr('text-anchor', 'middle')
                    .attr('dominant-baseline', 'middle')
                    .attr('font-size', '11px')
                    .attr('font-family', FONT)
                    .attr('fill', INK)
                    .attr('class', 'node-description')
                    .text(truncateLabel(node.description, mainW - 20));
                lineY += 16;
            }
            if (hasDescription && hasSubtitle) {
                ng.append('text')
                    .attr('x', mainW / 2)
                    .attr('y', lineY)
                    .attr('text-anchor', 'middle')
                    .attr('dominant-baseline', 'middle')
                    .attr('font-size', '10px')
                    .attr('font-family', 'ui-monospace, SFMono-Regular, Menlo, monospace')
                    .attr('fill', MUTED)
                    .attr('class', 'node-subtitle')
                    .text(truncateLabel(node.subtitle, mainW - 20));
            } else if (hasLegacySecondary) {
                ng.append('text')
                    .attr('x', mainW / 2)
                    .attr('y', labelY + 18)
                    .attr('text-anchor', 'middle')
                    .attr('dominant-baseline', 'middle')
                    .attr('font-size', '10px')
                    .attr('font-family', legacySecondaryMono ? 'ui-monospace, SFMono-Regular, Menlo, monospace' : FONT)
                    .attr('fill', MUTED)
                    .attr('class', 'node-prompt')
                    .text(truncateLabel(legacySecondary, mainW - 20));
            }

            // Metric row — only when the topology node carries metrics data.
            if (node.metrics) {
                const metricsHTML = renderNodeMetrics(node.metrics, node.status);
                if (metricsHTML) {
                    const foY = mainH - NODE_METRICS_EXTRA;
                    ng.append('foreignObject')
                        .attr('x', 8)
                        .attr('y', foY)
                        .attr('width', Math.max(0, mainW - 16))
                        .attr('height', NODE_METRICS_EXTRA)
                        .append('xhtml:div')
                        .attr('class', 'node-metrics-foreign')
                        .html(metricsHTML);
                }
            }
        }

        let zoomBehavior = null;

        if (inline) {
            // Inline mode: use viewBox, no zoom/pan — page scrolls naturally.
            // Cap the rendered scale so nodes don't blow up larger than their
            // natural pixel size. If the graph is narrower than the container,
            // center it at 1:1 scale instead of stretching to fill width.
            const bb = boundingBox(allNodes);
            const pad = 20;
            const vx = bb.x - pad;
            const vy = bb.y - pad;
            const vw = bb.width + pad * 2;
            const vh = bb.height + pad * 2;
            const cw = containerWidth;
            const scale = Math.min(cw / vw, 1); // never zoom in past 1:1
            const renderedHeight = vh * scale;

            svg.attr('viewBox', vx + ' ' + vy + ' ' + vw + ' ' + vh)
               .attr('preserveAspectRatio', 'xMidYMin meet')
               .attr('width', Math.min(cw, vw))
               .attr('height', renderedHeight)
               .style('display', 'block')
               .style('margin', '0 auto');
        } else {
            const minHeight = options.minHeight || MIN_HEIGHT;
            const containerHeight = Math.max(container.clientHeight, minHeight);

            // Zoom/pan
            zoomBehavior = d3.zoom()
                .scaleExtent([0.2, 4])
                .on('start', () => svg.style('cursor', 'grabbing'))
                .on('zoom', (event) => {
                    g.attr('transform', event.transform);
                })
                .on('end', () => svg.style('cursor', 'grab'));
            svg.call(zoomBehavior);
            svg.style('cursor', 'grab');

            // Fit to view
            svg.attr('height', containerHeight);
            fitToView(svg, zoomBehavior, allNodes, false);

            // Control bar
            buildControls(container, svg, zoomBehavior, allNodes);

            // Auto-size SVG height
            const bb = boundingBox(allNodes);
            const graphHeight = bb.height + 80;
            const finalHeight = Math.max(containerHeight, minHeight, graphHeight);
            svg.attr('height', finalHeight);

            // Keyboard shortcuts when container is hovered
            container.setAttribute('tabindex', '0');
            container.style.outline = 'none';
            container.addEventListener('keydown', function (e) {
                if (e.key === 'f' || e.key === 'F') {
                    fitToView(svg, zoomBehavior, allNodes, true);
                    e.preventDefault();
                } else if (e.key === '+' || e.key === '=') {
                    svg.transition().duration(200).call(zoomBehavior.scaleBy, 1.3);
                    e.preventDefault();
                } else if (e.key === '-') {
                    svg.transition().duration(200).call(zoomBehavior.scaleBy, 0.77);
                    e.preventDefault();
                } else if (e.key === 'Escape') {
                    const inst = instances.get(container);
                    if (inst && inst.breadcrumbStack && inst.breadcrumbStack.length > 1) {
                        navigateBreadcrumb(container, 0);
                        e.preventDefault();
                    }
                }
            });
        }

        return { svg, zoomBehavior, allNodes };
    }

    // Truncate label text to fit within a given pixel width (approximate)
    function truncateLabel(text, maxWidth) {
        // Rough estimate: ~7px per character at 13px semibold DM Sans
        const maxChars = Math.floor(maxWidth / 7);
        if (text.length <= maxChars) return text;
        return text.slice(0, maxChars - 1) + '\u2026';
    }

    // Breadcrumb management
    function renderBreadcrumb(container, stack, onNavigate) {
        let breadcrumbEl = container.parentElement.querySelector('.elk-breadcrumb');
        if (!breadcrumbEl) {
            breadcrumbEl = document.createElement('div');
            breadcrumbEl.className = 'elk-breadcrumb';
            breadcrumbEl.style.cssText =
                "display:flex;align-items:center;gap:4px;font-size:12px;" +
                "font-family:'DM Sans',sans-serif;color:" + MUTED + ";margin-bottom:8px;";
            container.parentElement.insertBefore(breadcrumbEl, container);
        }
        breadcrumbEl.innerHTML = '';
        if (stack.length <= 1) {
            breadcrumbEl.style.display = 'none';
            return;
        }
        breadcrumbEl.style.display = 'flex';
        stack.forEach((entry, i) => {
            if (i > 0) {
                const sep = document.createElement('span');
                sep.textContent = '/';
                sep.style.color = '#e1e4e8';
                breadcrumbEl.appendChild(sep);
            }
            const link = document.createElement('a');
            link.textContent = entry.label;
            link.style.cssText = 'cursor:pointer;color:' + (i < stack.length - 1 ? ACCENT : INK) + ';';
            link.addEventListener('click', () => onNavigate(i));
            breadcrumbEl.appendChild(link);
        });
    }

    // Wire node click handlers including drill-down for subworkflow nodes
    function wireNodeClicks(container, instance) {
        const options = instance.options || {};
        const topology = instance.topology;

        // Build node lookup for kind/workflow info
        const topoNodeMap = new Map();
        for (const n of topology.nodes) {
            topoNodeMap.set(n.id, n);
        }

        d3.select(container).selectAll('.graph-node')
            .style('cursor', 'pointer')
            .on('click', function (event) {
                event.stopPropagation();
                const nodeId = d3.select(this).attr('data-node-id');

                // Custom handler takes priority (run page)
                if (typeof options.onNodeSelect === 'function') {
                    options.onNodeSelect(nodeId);
                    return;
                }

                // Workflow page: drill into subworkflow nodes
                const topoNode = topoNodeMap.get(nodeId);
                if (topoNode && topoNode.kind === 'subworkflow' && topoNode.workflow) {
                    drillDown(container, topoNode.workflow, topoNode.label || topoNode.workflow);
                }
            });
    }

    async function drillDown(container, workflowId, label) {
        const instance = instances.get(container);
        if (!instance) return;

        // Initialize breadcrumb stack
        if (!instance.breadcrumbStack) {
            const rootLabel = instance.options.selectedWorkflow || 'Root';
            instance.breadcrumbStack = [{ id: '__root__', label: rootLabel, topology: instance.topology }];
        }

        try {
            const response = await fetch('/workflows/' + workflowId + '/graph');
            if (!response.ok) return;
            const topology = await response.json();

            instance.breadcrumbStack.push({ id: workflowId, label: label, topology: topology });

            // Fade out, re-render, fade in
            const svg = d3.select(container).select('svg');
            svg.transition().duration(150).style('opacity', 0).on('end', async () => {
                const elk = new ELK();
                const dir = instance.options && instance.options.direction;
                const elkGraph = toElkGraph(topology, dir);
                const layoutRoot = await elk.layout(elkGraph);
                const newInstance = renderSVG(container, layoutRoot, topology, instance.options);
                newInstance.topology = topology;
                newInstance.options = instance.options;
                newInstance.breadcrumbStack = instance.breadcrumbStack;
                instances.set(container, newInstance);
                wireNodeClicks(container, newInstance);
                renderBreadcrumb(container, newInstance.breadcrumbStack, (idx) => navigateBreadcrumb(container, idx));
                d3.select(container).select('svg').style('opacity', 0)
                    .transition().duration(150).style('opacity', 1);
            });
        } catch (err) {
            console.error('drill-down fetch error', err);
        }
    }

    async function navigateBreadcrumb(container, index) {
        const instance = instances.get(container);
        if (!instance || !instance.breadcrumbStack) return;

        // Truncate stack to the selected level
        instance.breadcrumbStack = instance.breadcrumbStack.slice(0, index + 1);
        const entry = instance.breadcrumbStack[index];

        const elk = new ELK();
        const dir = instance.options && instance.options.direction;
        const elkGraph = toElkGraph(entry.topology, dir);
        const layoutRoot = await elk.layout(elkGraph);
        const newInstance = renderSVG(container, layoutRoot, entry.topology, instance.options);
        newInstance.topology = entry.topology;
        newInstance.options = instance.options;
        newInstance.breadcrumbStack = instance.breadcrumbStack;
        instances.set(container, newInstance);
        wireNodeClicks(container, newInstance);
        renderBreadcrumb(container, newInstance.breadcrumbStack, (idx) => navigateBreadcrumb(container, idx));
    }

    // Public API

    async function render(container, topology, options) {
        options = options || {};
        const elk = new ELK();
        const elkGraph = toElkGraph(topology, options.direction);
        const layoutRoot = await elk.layout(elkGraph);
        const instance = renderSVG(container, layoutRoot, topology, options);
        instance.topology = topology;
        instance.options = options;
        instances.set(container, instance);
        wireNodeClicks(container, instance);
        return instance;
    }

    async function update(container, topology) {
        const prev = instances.get(container);
        const options = (prev && prev.options) || {};

        // Preserve the current zoom/pan transform so the user's view isn't reset
        let savedTransform = null;
        if (prev && prev.svg && prev.zoomBehavior) {
            const svgNode = prev.svg.node();
            if (svgNode) {
                savedTransform = d3.zoomTransform(svgNode);
            }
        }

        const elk = new ELK();
        const elkGraph = toElkGraph(topology, options.direction);
        const layoutRoot = await elk.layout(elkGraph);
        const instance = renderSVG(container, layoutRoot, topology, options);
        instance.topology = topology;
        instance.options = options;

        // Restore the saved zoom/pan transform instead of the default fitToView
        if (savedTransform && instance.svg && instance.zoomBehavior) {
            instance.svg.call(instance.zoomBehavior.transform, savedTransform);
        }

        instances.set(container, instance);
        wireNodeClicks(container, instance);
        return instance;
    }

    function updateNodeStatus(container, nodeId, status) {
        const svg = d3.select(container).select('svg');
        if (svg.empty()) return;

        const node = svg.select('.graph-node[data-node-id="' + nodeId + '"]');
        if (node.empty()) return;

        const oldStatus = node.attr('data-status');
        const style = statusStyle(status);
        node.attr('data-status', status);
        node.select('rect')
            .attr('fill', style.fill)
            .attr('stroke', style.stroke);
        node.select('.node-label')
            .attr('fill', style.text);

        // Trigger flash animation on status transition
        if (oldStatus && oldStatus !== status) {
            node.classed('status-transition', true);
            setTimeout(() => node.classed('status-transition', false), 600);
        }

        // Update subtitle if it contains a status
        const subtitle = node.select('.node-subtitle');
        if (!subtitle.empty()) {
            const oldText = subtitle.text();
            // Replace the status portion (first segment before dot separator)
            const parts = oldText.split(' \u00b7 ');
            if (parts.length > 0) {
                parts[0] = status;
                subtitle.text(parts.join(' \u00b7 '));
            }
        }
    }

    window.ElkGraph = { render, update, updateNodeStatus };
})();
