// swkit web ui - config schema diagram (Stage 1: read-only)
//
// Layout: a 3-column layered SVG diagram (Controls | Devices | Drivers & IO)
// built from GET /api/schema. All mutable UI state lives in one module-level
// object (S) so a future edit mode can extend it without restructuring.
// Graph layout (pure, no DOM) is kept separate from SVG rendering (buildLayout
// vs renderDiagram) for the same reason.

(function() {
    'use strict';

    const SVG_NS = 'http://www.w3.org/2000/svg';

    // ---- Layout constants ----

    const NODE_W = 190;
    const NODE_H = 46;
    const ROW_GAP = 14;
    const COL_GAP = 110;
    const IO_PILL_W = 150;
    const IO_PILL_H = 22;
    const IO_PILL_GAP = 6;
    const GROUP_PAD = 10;
    const GROUP_HEADER_H = 24;
    const GROUP_GAP = 18;
    const MARGIN = 24;

    const EVENT_COLORS = {
        single_press: 'var(--accent)',
        double_press: 'var(--info)',
        triple_press: '#bb9af7',
        long_press: 'var(--event)',
    };
    const SCENE_EDGE_COLOR = 'var(--success)';
    const IO_EDGE_COLOR = 'var(--text-muted)';

    const DEVICE_ICONS = {
        light: '\u{1F4A1}',
        dimmable_light: '\u{1F506}',
        color_light: '\u{1F308}',
        outlet: '\u{1F50C}',
        button: '\u{1F446}',
        scene: '\u{1F3AC}',
        missing: '❓',
    };

    const DEVICE_ACCENTS = {
        light: 'var(--warning)',
        dimmable_light: '#ff9e64',
        color_light: '#bb9af7',
        outlet: 'var(--success)',
        button: 'var(--accent)',
    };

    // ---- Module state (single object; stage 2 edit-mode fields go here too) ----

    const S = {
        data: null,       // last /api/schema response
        showIo: true,     // "driver I/O" toolbar toggle
        selectedId: null, // clicked node id, drives the detail panel
        loading: false,
        error: null,
        listenersBound: false,
    };

    // ---- Helpers ----

    function escHtml(s) {
        if (!s) return '';
        return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
    }

    function svgEl(tag, attrs) {
        const el = document.createElementNS(SVG_NS, tag);
        if (attrs) {
            for (const k in attrs) {
                if (Object.prototype.hasOwnProperty.call(attrs, k)) {
                    el.setAttribute(k, attrs[k]);
                }
            }
        }
        return el;
    }

    function edgeColor(edge) {
        if (edge.kind === 'control') return EVENT_COLORS[edge.event] || 'var(--accent)';
        if (edge.kind === 'scene_action') return SCENE_EDGE_COLOR;
        return IO_EDGE_COLOR;
    }

    function nodeMap(data) {
        const m = new Map();
        for (const n of data.nodes) m.set(n.id, n);
        return m;
    }

    // ==================================================================
    // Graph build (pure): data -> positioned layout. No DOM access here.
    // ==================================================================

    function buildLayout(data, showIo) {
        const nodes = data.nodes || [];
        const edges = data.edges || [];
        const nMap = nodeMap(data);

        const controls = nodes.filter(n => n.kind === 'device' && (n.device_type === 'button' || n.device_type === 'scene'));
        const devices = nodes.filter(n => n.kind === 'device' && n.device_type !== 'button' && n.device_type !== 'scene');
        const drivers = nodes.filter(n => n.kind === 'driver');
        const ios = nodes.filter(n => n.kind === 'io');

        const colX = {
            controls: MARGIN,
            devices: MARGIN + NODE_W + COL_GAP,
            drivers: MARGIN + (NODE_W + COL_GAP) * 2,
        };

        // ---- Column 1: Controls, kept in backend (config) order ----
        const controlPos = new Map();
        controls.forEach((n, i) => {
            controlPos.set(n.id, { x: colX.controls, y: MARGIN + i * (NODE_H + ROW_GAP) });
        });

        // ---- Column 2: Devices, barycenter-ordered by connected controls ----
        const relEdges = edges.filter(e => e.kind === 'control' || e.kind === 'scene_action');
        const incomingByDevice = new Map();
        relEdges.forEach(e => {
            if (!incomingByDevice.has(e.to)) incomingByDevice.set(e.to, []);
            const p = controlPos.get(e.from);
            if (p) incomingByDevice.get(e.to).push(p.y + NODE_H / 2);
        });
        const deviceKey = devices.map((n, i) => {
            const ys = incomingByDevice.get(n.id);
            const avg = ys && ys.length ? ys.reduce((a, b) => a + b, 0) / ys.length : i * (NODE_H + ROW_GAP);
            return { n, avg, i };
        });
        deviceKey.sort((a, b) => a.avg - b.avg || a.i - b.i);
        const devicePos = new Map();
        deviceKey.forEach((d, i) => {
            devicePos.set(d.n.id, { x: colX.devices, y: MARGIN + i * (NODE_H + ROW_GAP) });
        });

        // ---- Column 3: Drivers & IO (only when showIo) ----
        const groups = [];
        const ioPos = new Map();
        let driversHeight = 0;
        if (showIo) {
            const ioEdges = edges.filter(e => e.kind === 'io');
            const incomingByIo = new Map();
            ioEdges.forEach(e => {
                if (!incomingByIo.has(e.to)) incomingByIo.set(e.to, []);
                const p = devicePos.get(e.from);
                if (p) incomingByIo.get(e.to).push(p.y + NODE_H / 2);
            });

            // Real + placeholder driver nodes, in backend order.
            const byDriverName = new Map();
            drivers.forEach(d => byDriverName.set(d.label, { driverNode: d, pills: [] }));
            const invalidPills = [];
            ios.forEach((io, i) => {
                if (io.invalid || !io.driver) {
                    invalidPills.push({ n: io, i });
                    return;
                }
                if (!byDriverName.has(io.driver)) {
                    byDriverName.set(io.driver, { driverNode: null, pills: [] });
                }
                byDriverName.get(io.driver).pills.push({ n: io, i });
            });

            let gy = MARGIN;
            byDriverName.forEach((group, driverName) => {
                const keyed = group.pills.map(p => {
                    const ys = incomingByIo.get(p.n.id);
                    const avg = ys && ys.length ? ys.reduce((a, b) => a + b, 0) / ys.length : p.i * (IO_PILL_H + IO_PILL_GAP);
                    return { n: p.n, avg, i: p.i };
                });
                keyed.sort((a, b) => a.avg - b.avg || a.i - b.i);

                const boxH = GROUP_HEADER_H + GROUP_PAD * 2 + Math.max(keyed.length, 1) * (IO_PILL_H + IO_PILL_GAP) - (keyed.length ? IO_PILL_GAP : 0);
                const boxY = gy;
                keyed.forEach((k, idx) => {
                    ioPos.set(k.n.id, {
                        x: colX.drivers + GROUP_PAD,
                        y: boxY + GROUP_HEADER_H + GROUP_PAD + idx * (IO_PILL_H + IO_PILL_GAP),
                    });
                });
                groups.push({
                    driverNode: group.driverNode,
                    name: driverName,
                    x: colX.drivers,
                    y: boxY,
                    w: IO_PILL_W + GROUP_PAD * 2,
                    h: boxH,
                    pills: keyed.map(k => k.n),
                });
                gy += boxH + GROUP_GAP;
            });

            if (invalidPills.length) {
                const boxH = GROUP_HEADER_H + GROUP_PAD * 2 + invalidPills.length * (IO_PILL_H + IO_PILL_GAP) - IO_PILL_GAP;
                const boxY = gy;
                invalidPills.forEach((p, idx) => {
                    ioPos.set(p.n.id, {
                        x: colX.drivers + GROUP_PAD,
                        y: boxY + GROUP_HEADER_H + GROUP_PAD + idx * (IO_PILL_H + IO_PILL_GAP),
                    });
                });
                groups.push({
                    driverNode: null,
                    invalid: true,
                    name: 'invalid ids',
                    x: colX.drivers,
                    y: boxY,
                    w: IO_PILL_W + GROUP_PAD * 2,
                    h: boxH,
                    pills: invalidPills.map(p => p.n),
                });
                gy += boxH + GROUP_GAP;
            }
            driversHeight = gy - GROUP_GAP;
        }

        const controlsHeight = controls.length ? controls.length * (NODE_H + ROW_GAP) - ROW_GAP : 0;
        const devicesHeight = devices.length ? devices.length * (NODE_H + ROW_GAP) - ROW_GAP : 0;
        const contentHeight = Math.max(controlsHeight, devicesHeight, driversHeight, NODE_H);

        const width = (showIo ? colX.drivers + IO_PILL_W + GROUP_PAD * 2 : colX.devices + NODE_W) + MARGIN;
        const height = contentHeight + MARGIN * 2;

        // Edges to actually draw: hide io-kind edges (and anything touching a
        // hidden io node) when the driver/IO column is collapsed.
        const drawEdges = edges.filter(e => showIo || e.kind !== 'io');

        return {
            width, height,
            controls: controls.map(n => Object.assign({ node: n }, controlPos.get(n.id))),
            devices: devices.map(n => Object.assign({ node: n }, devicePos.get(n.id))),
            groups,
            ioPos,
            edges: drawEdges,
            nodeMap: nMap,
            portFor(id) {
                if (controlPos.has(id)) return { x: controlPos.get(id).x, xRight: controlPos.get(id).x + NODE_W, y: controlPos.get(id).y + NODE_H / 2 };
                if (devicePos.has(id)) return { x: devicePos.get(id).x, xRight: devicePos.get(id).x + NODE_W, y: devicePos.get(id).y + NODE_H / 2 };
                if (ioPos.has(id)) { const p = ioPos.get(id); return { x: p.x, xRight: p.x + IO_PILL_W, y: p.y + IO_PILL_H / 2 }; }
                return null;
            },
        };
    }

    // ==================================================================
    // Render (DOM/SVG only, consumes a layout built above)
    // ==================================================================

    function bezierPath(x1, y1, x2, y2) {
        const midX = (x1 + x2) / 2;
        return 'M ' + x1 + ',' + y1 + ' C ' + midX + ',' + y1 + ' ' + midX + ',' + y2 + ' ' + x2 + ',' + y2;
    }

    function renderDiagram(container, data, layout) {
        container.innerHTML = '';

        const svg = svgEl('svg', {
            class: 'schema-svg',
            viewBox: '0 0 ' + layout.width + ' ' + layout.height,
            width: layout.width,
            height: layout.height,
        });

        // Background rect: click here clears the selection.
        const bg = svgEl('rect', { x: 0, y: 0, width: layout.width, height: layout.height, class: 'schema-bg', fill: 'transparent' });
        bg.addEventListener('click', function() { selectNode(null); });
        svg.appendChild(bg);

        const defs = svgEl('defs');
        const usedColors = new Set();
        layout.edges.forEach(e => usedColors.add(edgeColor(e)));
        usedColors.forEach(color => defs.appendChild(buildMarker(color)));
        svg.appendChild(defs);

        const edgeLayer = svgEl('g', { class: 'schema-edges' });
        const nodeLayer = svgEl('g', { class: 'schema-nodes' });
        svg.appendChild(edgeLayer);
        svg.appendChild(nodeLayer);

        // ---- Edges ----
        layout.edges.forEach(edge => {
            const from = layout.portFor(edge.from);
            const to = layout.portFor(edge.to);
            if (!from || !to) return;
            const color = edgeColor(edge);
            const isIo = edge.kind === 'io';
            const path = svgEl('path', {
                d: bezierPath(from.xRight, from.y, to.x, to.y),
                class: 'schema-edge' + (isIo ? ' schema-edge-io' : ' schema-edge-rel'),
                stroke: color,
                fill: 'none',
            });
            path.dataset.from = edge.from;
            path.dataset.to = edge.to;
            if (!isIo) {
                path.setAttribute('marker-end', 'url(#arrow-' + colorId(color) + ')');
            }
            if (edge.kind === 'scene_action') {
                path.setAttribute('stroke-dasharray', '5,4');
            }
            path.addEventListener('mouseenter', function(ev) { showTooltip(ev, edge, layout.nodeMap); });
            path.addEventListener('mousemove', moveTooltip);
            path.addEventListener('mouseleave', hideTooltip);
            edgeLayer.appendChild(path);
        });

        // ---- Control / Device nodes ----
        layout.controls.forEach(item => nodeLayer.appendChild(buildDeviceNode(item)));
        layout.devices.forEach(item => nodeLayer.appendChild(buildDeviceNode(item)));

        // ---- Driver groups + IO pills ----
        layout.groups.forEach(group => nodeLayer.appendChild(buildDriverGroup(group)));

        // Delegate hover + click for adjacency dim/highlight and selection.
        svg.addEventListener('mouseover', function(ev) {
            const el = ev.target.closest('[data-node-id]');
            if (el) applyAdjacency(svg, el.dataset.nodeId);
        });
        svg.addEventListener('mouseout', function(ev) {
            const el = ev.target.closest('[data-node-id]');
            const to = ev.relatedTarget && ev.relatedTarget.closest ? ev.relatedTarget.closest('[data-node-id]') : null;
            if (el && (!to || to.dataset.nodeId !== el.dataset.nodeId)) clearAdjacency(svg);
        });
        svg.addEventListener('click', function(ev) {
            const el = ev.target.closest('[data-node-id]');
            if (el) selectNode(el.dataset.nodeId);
        });

        container.appendChild(svg);
        if (S.selectedId) applyAdjacency(svg, S.selectedId);
    }

    function colorId(color) {
        return color.replace(/[^a-zA-Z0-9]/g, '');
    }

    function buildMarker(color) {
        const marker = svgEl('marker', {
            id: 'arrow-' + colorId(color),
            viewBox: '0 0 10 10',
            refX: 8, refY: 5,
            markerWidth: 7, markerHeight: 7,
            orient: 'auto-start-reverse',
        });
        marker.appendChild(svgEl('path', { d: 'M 0 0 L 10 5 L 0 10 z', fill: color }));
        return marker;
    }

    function buildDeviceNode(item) {
        const n = item.node;
        const g = svgEl('g', { class: 'schema-node schema-node-device', 'data-node-id': n.id });
        const dashed = n.device_type === 'scene' || n.missing;
        let extraClass = '';
        if (n.missing) extraClass = ' schema-node-missing';
        else if (n.device_type === 'scene') extraClass = ' schema-node-scene';
        const rectAttrs = {
            x: item.x, y: item.y, width: NODE_W, height: NODE_H, rx: 8,
            class: 'schema-node-rect' + (dashed ? ' schema-node-dashed' : '') + extraClass,
        };
        g.appendChild(svgEl('rect', rectAttrs));

        const accent = DEVICE_ACCENTS[n.device_type];
        if (!dashed && accent) {
            g.appendChild(svgEl('rect', {
                x: item.x, y: item.y + 2, width: 3, height: NODE_H - 4, fill: accent, class: 'schema-node-accent',
            }));
        }

        const icon = DEVICE_ICONS[n.device_type] || '•';
        const label = svgEl('text', { x: item.x + 14, y: item.y + NODE_H / 2 + 5, class: 'schema-node-label' });
        label.textContent = icon + ' ' + (n.label || '');
        g.appendChild(label);

        let markerX = item.x + NODE_W - 14;
        if (n.homekit) {
            const hk = svgEl('text', { x: markerX, y: item.y + 15, class: 'schema-node-marker', 'text-anchor': 'end' });
            hk.textContent = '\u{1F34E}';
            g.appendChild(hk);
            markerX -= 16;
        }
        if (n.faulty) {
            g.appendChild(svgEl('circle', { cx: item.x + NODE_W - 8, cy: item.y + 8, r: 4, class: 'schema-fault-dot' }));
        }

        return g;
    }

    function buildDriverGroup(group) {
        const g = svgEl('g', { class: 'schema-group' });
        const missing = group.driverNode && group.driverNode.missing;
        let groupExtra = '';
        if (missing) groupExtra = ' schema-node-dashed schema-node-missing';
        else if (group.invalid) groupExtra = ' schema-node-dashed schema-group-invalid';
        g.appendChild(svgEl('rect', {
            x: group.x, y: group.y, width: group.w, height: group.h, rx: 8,
            class: 'schema-group-rect' + groupExtra,
        }));

        const headerId = group.driverNode ? group.driverNode.id : null;
        const headerG = svgEl('g', headerId ? { class: 'schema-node schema-group-header', 'data-node-id': headerId } : { class: 'schema-group-header' });
        const headerText = svgEl('text', { x: group.x + GROUP_PAD, y: group.y + 16, class: 'schema-group-title' });
        headerText.textContent = (group.invalid ? '⚠️ ' : '⚡ ') + group.name;
        headerG.appendChild(headerText);
        if (!group.invalid) {
            const ready = group.driverNode && group.driverNode.ready;
            headerG.appendChild(svgEl('circle', {
                cx: group.x + group.w - 34, cy: group.y + 12, r: 4,
                class: ready ? 'schema-ready-dot-on' : 'schema-ready-dot-off',
            }));
            const count = svgEl('text', { x: group.x + group.w - 8, y: group.y + 16, class: 'schema-group-count', 'text-anchor': 'end' });
            count.textContent = (group.driverNode && group.driverNode.io_total != null ? group.driverNode.io_total : group.pills.length) + ' IOs';
            headerG.appendChild(count);
        }
        g.appendChild(headerG);

        group.pills.forEach((io, idx) => {
            const y = group.y + GROUP_HEADER_H + GROUP_PAD + idx * (IO_PILL_H + IO_PILL_GAP);
            const pg = svgEl('g', { class: 'schema-node schema-io-pill', 'data-node-id': io.id });
            pg.appendChild(svgEl('rect', {
                x: group.x + GROUP_PAD, y: y, width: IO_PILL_W, height: IO_PILL_H, rx: 4,
                class: 'schema-io-pill-rect' + (io.invalid ? ' schema-node-dashed schema-node-missing' : ''),
            }));
            const text = svgEl('text', { x: group.x + GROUP_PAD + 6, y: y + 15, class: 'schema-io-pill-label' });
            const label = io.custom_name ? (io.custom_name + ' [' + (io.io_type || '?') + ' ' + io.label + ']') : ((io.io_type || '?') + ' ' + (io.label || ''));
            text.textContent = label;
            pg.appendChild(text);
            g.appendChild(pg);
        });

        return g;
    }

    // ---- Hover adjacency ----

    function adjacentSet(nodeId, data) {
        const ids = new Set([nodeId]);
        (data.edges || []).forEach(e => {
            if (e.from === nodeId) ids.add(e.to);
            if (e.to === nodeId) ids.add(e.from);
        });
        return ids;
    }

    function applyAdjacency(svg, nodeId) {
        const adj = adjacentSet(nodeId, S.data);
        svg.querySelectorAll('.schema-node').forEach(el => {
            el.classList.toggle('dim', !adj.has(el.dataset.nodeId));
        });
        svg.querySelectorAll('.schema-edge').forEach(el => {
            const touches = adj.has(el.dataset.from) && adj.has(el.dataset.to) && (el.dataset.from === nodeId || el.dataset.to === nodeId);
            el.classList.toggle('dim', !touches);
            el.classList.toggle('schema-edge-active', touches);
        });
    }

    function clearAdjacency(svg) {
        svg.querySelectorAll('.dim').forEach(el => el.classList.remove('dim'));
        svg.querySelectorAll('.schema-edge-active').forEach(el => el.classList.remove('schema-edge-active'));
        if (S.selectedId) applyAdjacency(svg, S.selectedId);
    }

    // ---- Tooltip (single floating div, reused for all edges) ----

    let tooltipEl = null;

    function ensureTooltip() {
        if (!tooltipEl) {
            tooltipEl = document.createElement('div');
            tooltipEl.className = 'schema-tooltip';
            document.body.appendChild(tooltipEl);
        }
        return tooltipEl;
    }

    function edgeTooltipText(edge, nMap) {
        const toNode = nMap.get(edge.to);
        const toLabel = toNode ? toNode.label : edge.to;
        if (edge.kind === 'control') {
            const lvl = edge.level ? (' ' + edge.level) : '';
            return edge.event + ' → ' + edge.action + lvl + ' → ' + toLabel;
        }
        if (edge.kind === 'scene_action') {
            const lvl = edge.level ? (' ' + edge.level) : '';
            return edge.state + ': ' + edge.action + lvl + ' → ' + toLabel;
        }
        return edge.role || 'io';
    }

    function showTooltip(ev, edge, nMap) {
        const el = ensureTooltip();
        el.textContent = edgeTooltipText(edge, nMap);
        el.classList.add('visible');
        moveTooltip(ev);
    }

    function moveTooltip(ev) {
        if (!tooltipEl) return;
        tooltipEl.style.left = (ev.clientX + 14) + 'px';
        tooltipEl.style.top = (ev.clientY + 14) + 'px';
    }

    function hideTooltip() {
        if (tooltipEl) tooltipEl.classList.remove('visible');
    }

    // ---- Selection / detail panel ----

    function selectNode(id) {
        S.selectedId = id;
        const svg = document.querySelector('.schema-svg');
        if (svg) {
            if (id) applyAdjacency(svg, id); else clearAllDim(svg);
        }
        renderDetailPanel();
    }

    function clearAllDim(svg) {
        svg.querySelectorAll('.dim').forEach(el => el.classList.remove('dim'));
        svg.querySelectorAll('.schema-edge-active').forEach(el => el.classList.remove('schema-edge-active'));
    }

    function renderDetailPanel() {
        const panel = document.getElementById('schema-detail-panel');
        if (!panel) return;
        if (!S.selectedId || !S.data) {
            panel.classList.remove('open');
            panel.innerHTML = '';
            return;
        }
        const node = S.data.nodes.find(n => n.id === S.selectedId);
        if (!node) {
            panel.classList.remove('open');
            panel.innerHTML = '';
            return;
        }

        let html = '<button class="schema-panel-close" aria-label="Close">✕</button>';
        html += '<div class="schema-panel-title">' + (DEVICE_ICONS[node.device_type] || (node.kind === 'driver' ? '⚡' : '\u{1F50C}')) + ' ' + escHtml(node.label || node.id) + '</div>';

        if (node.kind === 'driver') {
            html += '<div class="row"><dt>Kind:</dt><dd>Driver</dd></div>';
            html += '<div class="row"><dt>Ready:</dt><dd>' + (node.ready ? 'yes' : 'no') + (node.missing ? ' (referenced, not configured)' : '') + '</dd></div>';
            if (node.io_total != null) html += '<div class="row"><dt>IO points:</dt><dd>' + node.io_total + '</dd></div>';
            const ios = S.data.nodes.filter(n => n.kind === 'io' && n.driver === node.label);
            if (ios.length) {
                html += '<div class="schema-panel-section">IO points</div><ul class="schema-panel-list">';
                ios.forEach(io => { html += '<li class="mono">' + escHtml((io.io_type || '?') + ' ' + io.label) + '</li>'; });
                html += '</ul>';
            }
        } else if (node.kind === 'io') {
            html += '<div class="row"><dt>Driver:</dt><dd>' + escHtml(node.driver || '-') + '</dd></div>';
            html += '<div class="row"><dt>Type:</dt><dd>' + escHtml(node.io_type || '-') + '</dd></div>';
            if (node.custom_name) html += '<div class="row"><dt>Name:</dt><dd>' + escHtml(node.custom_name) + '</dd></div>';
            if (node.invalid) html += '<div class="row"><dt class="text-error">Invalid io id</dt></div>';
            const users = (S.data.edges || []).filter(e => e.kind === 'io' && e.to === node.id);
            if (users.length) {
                html += '<div class="schema-panel-section">Used by</div><ul class="schema-panel-list">';
                users.forEach(e => {
                    const from = S.data.nodes.find(n => n.id === e.from);
                    html += '<li>' + escHtml(from ? from.label : e.from) + ' <span class="text-muted">(' + escHtml(e.role) + ')</span></li>';
                });
                html += '</ul>';
            }
        } else {
            // device (incl. missing placeholder)
            html += '<div class="row"><dt>Type:</dt><dd>' + escHtml(node.device_type) + '</dd></div>';
            if (node.missing) {
                html += '<div class="row"><dt class="text-error">Not configured</dt><dd>referenced by name only</dd></div>';
            } else {
                html += '<div class="row"><dt>HomeKit:</dt><dd>' + (node.homekit ? 'enabled' : 'disabled') + '</dd></div>';
                html += '<div class="row"><dt>Healthy:</dt><dd>' + (node.healthy ? 'yes' : 'no') + '</dd></div>';
                if (node.faulty) html += '<div class="row"><dt class="text-error">Faulty</dt></div>';
                html += '<div class="row"><dt>State:</dt><dd>' + (node.is_on ? 'on' : 'off') + '</dd></div>';
            }
            if (node.detail) {
                for (const k in node.detail) {
                    if (!Object.prototype.hasOwnProperty.call(node.detail, k)) continue;
                    html += '<div class="row"><dt>' + escHtml(k) + ':</dt><dd class="mono">' + escHtml(JSON.stringify(node.detail[k])) + '</dd></div>';
                }
            }

            const incoming = (S.data.edges || []).filter(e => (e.kind === 'control' || e.kind === 'scene_action') && e.to === node.id);
            if (incoming.length) {
                html += '<div class="schema-panel-section">Driven by</div><ul class="schema-panel-list">';
                incoming.forEach(e => {
                    const from = S.data.nodes.find(n => n.id === e.from);
                    const via = e.kind === 'control' ? (e.event + ' → ' + e.action) : (e.state + ': ' + e.action);
                    html += '<li>' + escHtml(from ? from.label : e.from) + ' <span class="text-muted">(' + escHtml(via) + (e.level ? ' ' + e.level : '') + ')</span></li>';
                });
                html += '</ul>';
            }
            const outgoing = (S.data.edges || []).filter(e => (e.kind === 'control' || e.kind === 'scene_action') && e.from === node.id);
            if (outgoing.length) {
                html += '<div class="schema-panel-section">Controls</div><ul class="schema-panel-list">';
                outgoing.forEach(e => {
                    const to = S.data.nodes.find(n => n.id === e.to);
                    const via = e.kind === 'control' ? (e.event + ' → ' + e.action) : (e.state + ': ' + e.action);
                    html += '<li>' + escHtml(to ? to.label : e.to) + ' <span class="text-muted">(' + escHtml(via) + (e.level ? ' ' + e.level : '') + ')</span></li>';
                });
                html += '</ul>';
            }
        }

        panel.innerHTML = html;
        panel.classList.add('open');
        const closeBtn = panel.querySelector('.schema-panel-close');
        if (closeBtn) closeBtn.addEventListener('click', function() { selectNode(null); });
    }

    // ---- Toolbar / chrome / data loading ----

    function redraw() {
        const diagramEl = document.getElementById('schema-diagram');
        const emptyEl = document.getElementById('schema-empty');
        if (!diagramEl || !S.data) return;

        const deviceCount = (S.data.nodes || []).filter(n => n.kind === 'device').length;
        if (deviceCount === 0) {
            diagramEl.style.display = 'none';
            if (emptyEl) emptyEl.style.display = '';
            return;
        }
        diagramEl.style.display = '';
        if (emptyEl) emptyEl.style.display = 'none';

        const layout = buildLayout(S.data, S.showIo);
        renderDiagram(diagramEl, S.data, layout);
        renderDetailPanel();
    }

    async function loadAndDraw() {
        S.loading = true;
        S.error = null;
        setStatus('Loading…');
        try {
            const resp = await fetch('/api/schema');
            if (!resp.ok) throw new Error('HTTP ' + resp.status);
            S.data = await resp.json();
            S.loading = false;
            setStatus('');
            redraw();
        } catch (e) {
            S.loading = false;
            S.error = String(e && e.message || e);
            setStatus('Failed to load schema: ' + S.error);
        }
    }

    function setStatus(text) {
        const el = document.getElementById('schema-status');
        if (el) el.textContent = text;
    }

    function buildLegend() {
        const items = [
            ['\u{1F4A1}', 'Light'], ['\u{1F506}', 'Dimmable'], ['\u{1F308}', 'Color light'],
            ['\u{1F50C}', 'Outlet'], ['\u{1F446}', 'Button'], ['\u{1F3AC}', 'Scene'],
            ['⚡', 'Driver'], ['❓', 'Missing target'],
        ];
        let html = '<div class="schema-legend">';
        items.forEach(([icon, label]) => {
            html += '<span class="schema-legend-item">' + icon + ' ' + escHtml(label) + '</span>';
        });
        const edgeItems = [
            ['var(--accent)', 'single press'], ['var(--info)', 'double press'],
            ['#bb9af7', 'triple press'], ['var(--event)', 'long press'],
            ['var(--success)', 'scene action'], ['var(--text-muted)', 'IO wiring'],
        ];
        edgeItems.forEach(([color, label]) => {
            html += '<span class="schema-legend-item"><span class="schema-legend-swatch" style="background:' + color + '"></span>' + escHtml(label) + '</span>';
        });
        html += '</div>';
        return html;
    }

    function buildChrome(el) {
        el.innerHTML =
            '<div class="card" id="schema-root">' +
                '<div class="card-title">\u{1F5FA}️ Configuration Schema</div>' +
                '<div class="schema-toolbar">' +
                    '<label class="schema-toggle"><input type="checkbox" id="schema-io-toggle" checked> ⚡ driver I/O</label>' +
                    '<button class="io-filter-btn" id="schema-refresh-btn">⟳ Refresh</button>' +
                    '<span id="schema-status" class="text-muted"></span>' +
                '</div>' +
                buildLegend() +
            '</div>' +
            '<div class="schema-diagram-wrap"><div id="schema-diagram"></div></div>' +
            '<div id="schema-empty" class="empty-state" style="display:none">No devices configured</div>' +
            '<div id="schema-detail-panel" class="schema-detail-panel"></div>';

        document.getElementById('schema-io-toggle').addEventListener('change', function() {
            S.showIo = this.checked;
            redraw();
        });
        document.getElementById('schema-refresh-btn').addEventListener('click', function() {
            loadAndDraw();
        });

        if (!S.listenersBound) {
            S.listenersBound = true;
            document.addEventListener('keydown', function(e) {
                if (e.key === 'Escape' && S.selectedId) selectNode(null);
            });
        }
    }

    // ---- Public entry point ----
    // render() is called from app.js's renderPage() on every 1s poll tick
    // while the schema tab is active, but must not rebuild/refetch each
    // time (see app.js: the 'schema' case returns immediately after this
    // call). We rely on #schema-root only existing once we've built the
    // chrome; other tabs replace #page-content wholesale when navigated to,
    // so returning to /schema naturally finds no #schema-root and rebuilds.
    function render() {
        const el = document.getElementById('page-content');
        if (!el) return;
        if (document.getElementById('schema-root')) return; // already built, no-op

        buildChrome(el);
        loadAndDraw();
    }

    window.swkitSchema = { render };
})();
