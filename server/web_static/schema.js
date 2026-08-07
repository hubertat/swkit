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
        data: null,       // last /api/schema response (or, in edit mode, the client-built edit graph)
        showIo: true,     // "driver I/O" toolbar toggle
        selectedId: null, // clicked node id, drives the detail panel
        loading: false,
        error: null,
        listenersBound: false,

        // ---- Stage 2: edit mode ----
        editAvailable: null, // null = not probed yet; true/false once known (503 -> false)
        editMode: false,     // whether the edit UI is currently active
        editData: null,      // last GET /api/config/edit response ({config, meta})
        working: null,       // deep working copy of editData.config, being edited
        dirty: false,        // true once the working copy diverges from editData.config
        selectedEdit: null,  // {kind, editId} for an editable device, or {kind:'color_light', label} - drives the edit form
        saveErrors: null,    // string[] from the last failed POST /api/config/edit
        colorLightNodes: [], // color light nodes snapshotted from the live graph on entering edit mode (read-only in the edit graph)
    };

    let editIdSeq = 1;
    function nextEditId() { return 'e' + (editIdSeq++); }

    // ---- Helpers ----

    function escHtml(s) {
        if (!s) return '';
        return String(s).replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
    }

    // mkEl creates a plain HTML element with attrs and (safe, textContent-only)
    // text. Used throughout the edit-mode forms so user-controlled strings
    // (device names, io ids, action text) are never passed through innerHTML.
    // Named mkEl (not el) to avoid shadowing buildChrome's "el" container param.
    function mkEl(tag, attrs, text) {
        const e = document.createElement(tag);
        if (attrs) {
            for (const k in attrs) {
                if (!Object.prototype.hasOwnProperty.call(attrs, k)) continue;
                if (k === 'class') e.className = attrs[k];
                else e.setAttribute(k, attrs[k]);
            }
        }
        if (text !== undefined && text !== null) e.textContent = text;
        return e;
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

    // truncateLabel shortens a display string with an ellipsis so long device
    // or IO names don't overflow their fixed-width node/pill rect. The full,
    // untruncated name is always still available: via the native <title>
    // tooltip appended alongside the truncated text, and in the click detail
    // panel/edit form, which never truncate.
    function truncateLabel(s, maxChars) {
        s = s || '';
        if (s.length <= maxChars) return s;
        return s.slice(0, Math.max(0, maxChars - 1)) + '…';
    }

    const NODE_LABEL_MAX_CHARS = 22;
    const IO_PILL_LABEL_MAX_CHARS = 20;

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
        label.textContent = icon + ' ' + truncateLabel(n.label || '', NODE_LABEL_MAX_CHARS);
        if (n.label) {
            const title = svgEl('title');
            title.textContent = n.label;
            label.appendChild(title);
        }
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
        if (n.unsaved) {
            g.appendChild(svgEl('circle', { cx: item.x + 8, cy: item.y + NODE_H - 8, r: 4, class: 'schema-unsaved-dot' }));
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
            text.textContent = truncateLabel(label, IO_PILL_LABEL_MAX_CHARS);
            const title = svgEl('title');
            title.textContent = label;
            text.appendChild(title);
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
        if (S.editMode) {
            S.selectedEdit = id ? nodeIdToEditSelection(id) : null;
        }
        renderActivePanel();
    }

    // nodeIdToEditSelection maps a clicked "device:<kind>:<name>" node id to
    // an edit-panel selection. Returns null for driver/io nodes (not
    // editable), missing placeholders (nothing to edit), and names that no
    // longer resolve to a working-copy entry.
    function nodeIdToEditSelection(id) {
        if (!id || id.indexOf('device:') !== 0) return null;
        const rest = id.slice('device:'.length);
        const sep = rest.indexOf(':');
        if (sep < 0) return null;
        const kind = rest.slice(0, sep);
        const name = rest.slice(sep + 1);
        if (kind === 'missing') return null;
        if (kind === 'color_light') return { kind: 'color_light', label: name };
        const list = workingListFor(kind);
        if (!list) return null;
        const entry = list.find(function(e) { return e.Name === name; });
        if (!entry) return null;
        return { kind: kind, editId: entry._editId };
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
            renderActivePanel();
            return;
        }
        diagramEl.style.display = '';
        if (emptyEl) emptyEl.style.display = 'none';

        const layout = buildLayout(S.data, S.showIo);
        renderDiagram(diagramEl, S.data, layout);
        renderActivePanel();
    }

    // renderActivePanel picks the right side-panel renderer for the current
    // mode: the stage 1 read-only detail panel outside edit mode (or for
    // driver/io nodes even inside edit mode, since those aren't editable),
    // and the stage 2 edit form otherwise.
    function renderActivePanel() {
        if (!S.editMode) {
            renderDetailPanel();
            return;
        }
        if (!S.selectedEdit && S.selectedId && S.data) {
            const node = S.data.nodes.find(function(n) { return n.id === S.selectedId; });
            if (node && (node.kind === 'driver' || node.kind === 'io')) {
                renderDetailPanel();
                return;
            }
        }
        renderEditPanel();
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
                    '<button class="io-filter-btn" id="schema-edit-btn">✏️ Edit</button>' +
                    '<div class="schema-add-wrap" id="schema-add-wrap" style="display:none">' +
                        '<button class="io-filter-btn" id="schema-add-btn">+ Add ▾</button>' +
                        '<div class="schema-add-menu" id="schema-add-menu" style="display:none"></div>' +
                    '</div>' +
                    '<button class="io-filter-btn" id="schema-save-btn" style="display:none">💾 Save</button>' +
                    '<button class="io-filter-btn" id="schema-discard-btn" style="display:none">↩ Discard</button>' +
                    '<span id="schema-unsaved-badge" class="badge badge-not-ready" style="display:none">unsaved changes</span>' +
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
        document.getElementById('schema-edit-btn').addEventListener('click', toggleEditMode);
        document.getElementById('schema-save-btn').addEventListener('click', saveChanges);
        document.getElementById('schema-discard-btn').addEventListener('click', discardChanges);
        document.getElementById('schema-add-btn').addEventListener('click', function(ev) {
            ev.stopPropagation();
            const menu = document.getElementById('schema-add-menu');
            if (menu) menu.style.display = (menu.style.display === 'none') ? '' : 'none';
        });
        document.getElementById('schema-io-toggle').checked = S.showIo;
        buildAddMenu();
        updateEditButtonState();
        probeEditAvailability();
        // Defensive: buildChrome always starts from the static toolbar HTML
        // (edit-mode buttons hidden), so if S.editMode is already true when
        // the chrome is (re)built - e.g. returning to /schema after
        // navigating away without discarding edit mode - the toolbar must be
        // brought back in sync with it rather than silently reverting to the
        // read-only look while S.editMode still reports true underneath.
        updateToolbarMode();

        if (!S.listenersBound) {
            S.listenersBound = true;
            document.addEventListener('keydown', function(e) {
                if (e.key === 'Escape' && (S.selectedId || S.selectedEdit)) selectNode(null);
            });
            document.addEventListener('click', function() {
                const menu = document.getElementById('schema-add-menu');
                if (menu) menu.style.display = 'none';
            });
            window.addEventListener('beforeunload', function(e) {
                if (S.editMode && S.dirty) {
                    e.preventDefault();
                    e.returnValue = '';
                }
            });
        }
    }

    // ==================================================================
    // Stage 2: edit mode
    // ==================================================================

    // ---- Availability probing / mode switching ----

    async function probeEditAvailability() {
        try {
            const resp = await fetch('/api/config/edit');
            S.editAvailable = resp.status !== 503;
        } catch (e) {
            S.editAvailable = false;
        }
        updateEditButtonState();
    }

    function updateEditButtonState() {
        const btn = document.getElementById('schema-edit-btn');
        if (!btn) return;
        if (S.editAvailable === false) {
            btn.disabled = true;
            btn.title = 'Config editing is not available (no config provider configured on the server)';
        } else {
            btn.disabled = false;
            btn.title = '';
        }
    }

    function updateToolbarMode() {
        const editBtn = document.getElementById('schema-edit-btn');
        const saveBtn = document.getElementById('schema-save-btn');
        const discardBtn = document.getElementById('schema-discard-btn');
        const addWrap = document.getElementById('schema-add-wrap');
        const unsavedBadge = document.getElementById('schema-unsaved-badge');
        const refreshBtn = document.getElementById('schema-refresh-btn');
        if (editBtn) editBtn.textContent = S.editMode ? '✕ Exit edit' : '✏️ Edit';
        if (saveBtn) saveBtn.style.display = S.editMode ? '' : 'none';
        if (discardBtn) discardBtn.style.display = S.editMode ? '' : 'none';
        if (addWrap) addWrap.style.display = S.editMode ? '' : 'none';
        if (unsavedBadge) unsavedBadge.style.display = (S.editMode && S.dirty) ? '' : 'none';
        // Refreshing mid-edit would overwrite S.data with the live server
        // graph out from under the working copy (redrawEdit rebuilds S.data
        // from S.working; a stray refresh would immediately clobber that).
        if (refreshBtn) {
            refreshBtn.disabled = S.editMode;
            refreshBtn.title = S.editMode ? 'Refresh is disabled while editing' : '';
        }
    }

    function toggleEditMode() {
        if (S.editMode) {
            if (S.dirty && !confirm('Discard unsaved changes and exit edit mode?')) return;
            exitEditMode({ refetch: true });
        } else {
            enterEditMode();
        }
    }

    async function enterEditMode() {
        setStatus('Loading config…');
        try {
            const resp = await fetch('/api/config/edit');
            if (resp.status === 503) {
                S.editAvailable = false;
                updateEditButtonState();
                setStatus('Config editing not available');
                return;
            }
            if (!resp.ok) throw new Error('HTTP ' + resp.status);
            S.editData = await resp.json();
            S.editAvailable = true;
            // Color lights aren't part of EditableConfig, so the working copy
            // has no notion of them; snapshot them from the live graph (still
            // in S.data at this point) so the edit-mode diagram can still
            // show them as read-only targets instead of bogus "missing"
            // placeholders when a control relation or scene action names one.
            S.colorLightNodes = (S.data && S.data.nodes || []).filter(function(n) {
                return n.kind === 'device' && n.device_type === 'color_light';
            });
            S.working = buildWorkingCopy(S.editData.config);
            S.dirty = false;
            S.editMode = true;
            S.selectedEdit = null;
            S.selectedId = null;
            S.saveErrors = null;
            setStatus('');
            updateToolbarMode();
            redrawEdit();
        } catch (e) {
            setStatus('Failed to load config for editing: ' + (e && e.message || e));
        }
    }

    // exitEditMode leaves edit mode. opts.refetch (default true) controls
    // whether the live /api/schema view is reloaded immediately - saveChanges
    // passes false so it can show a toast first and delay the refetch itself.
    function exitEditMode(opts) {
        opts = opts || {};
        S.editMode = false;
        S.working = null;
        S.selectedEdit = null;
        S.saveErrors = null;
        S.dirty = false;
        updateToolbarMode();
        if (opts.refetch !== false) loadAndDraw();
    }

    function discardChanges() {
        if (!S.dirty) return;
        if (!confirm('Discard all unsaved changes?')) return;
        S.working = buildWorkingCopy(S.editData.config);
        S.dirty = false;
        S.selectedEdit = null;
        S.saveErrors = null;
        updateToolbarMode();
        redrawEdit();
    }

    function markDirty() {
        S.dirty = true;
        updateToolbarMode();
    }

    // redrawEdit rebuilds the client-side graph from the working copy and
    // re-runs the normal (pure) layout/render pipeline against it, so hover
    // adjacency, tooltips and the detail panel all keep working unmodified.
    function redrawEdit() {
        S.data = buildEditGraph(S.working, S.editData && S.editData.meta, S.colorLightNodes);
        redraw();
    }

    // scheduleRedrawEdit defers a redrawEdit() to a macrotask (setTimeout 0)
    // instead of running it synchronously. Field commits happen on "blur",
    // which fires *before* the click that caused it (e.g. clicking a
    // different field, a delete/add button, or another node) is dispatched.
    // redrawEdit() replaces the entire panel/diagram DOM via
    // renderEditPanel()/renderDiagram(), so an immediate synchronous redraw
    // there destroys the very element the in-flight click is targeting - the
    // click event never reaches it (the "two-click bug": the first click
    // only commits+redraws, the second click is needed to actually hit the
    // button). Deferring lets the current click finish dispatching against
    // the still-live DOM first; the (possibly now-redundant) redraw runs
    // right after on the next macrotask.
    let redrawEditScheduled = false;
    function scheduleRedrawEdit() {
        if (redrawEditScheduled) return;
        redrawEditScheduled = true;
        setTimeout(function() {
            redrawEditScheduled = false;
            redrawEdit();
        }, 0);
    }

    // ---- Working copy ----

    function withEditId(obj) {
        obj._editId = nextEditId();
        return obj;
    }

    function buildWorkingCopy(cfg) {
        cfg = cfg || {};
        return {
            Lights: (cfg.Lights || []).map(function(l) { return withEditId(Object.assign({}, l)); }),
            DimmableLights: (cfg.DimmableLights || []).map(function(d) { return withEditId(Object.assign({}, d)); }),
            Outlets: (cfg.Outlets || []).map(function(o) { return withEditId(Object.assign({}, o)); }),
            Buttons: (cfg.Buttons || []).map(function(b) {
                return withEditId(Object.assign({}, b, {
                    ControlDevices: (b.ControlDevices || []).map(function(cd) { return Object.assign({}, cd); }),
                }));
            }),
            Scenes: (cfg.Scenes || []).map(function(s) {
                return withEditId(Object.assign({}, s, {
                    States: (s.States || []).map(function(st) {
                        return Object.assign({}, st, { Actions: (st.Actions || []).slice() });
                    }),
                }));
            }),
        };
    }

    function workingListFor(kind) {
        switch (kind) {
            case 'light': return S.working.Lights;
            case 'dimmable_light': return S.working.DimmableLights;
            case 'outlet': return S.working.Outlets;
            case 'button': return S.working.Buttons;
            case 'scene': return S.working.Scenes;
            default: return null;
        }
    }

    function findWorkingEntry(kind, editId) {
        const list = workingListFor(kind);
        if (!list) return null;
        return list.find(function(e) { return e._editId === editId; }) || null;
    }

    function deleteWorkingEntry(kind, editId) {
        const list = workingListFor(kind);
        if (!list) return;
        const idx = list.findIndex(function(e) { return e._editId === editId; });
        if (idx >= 0) list.splice(idx, 1);
    }

    function allWorkingNames() {
        const names = new Set();
        ['Lights', 'DimmableLights', 'Outlets', 'Buttons', 'Scenes'].forEach(function(key) {
            (S.working[key] || []).forEach(function(e) { names.add(e.Name); });
        });
        return names;
    }

    function kindLabel(kind) {
        switch (kind) {
            case 'light': return 'Light';
            case 'dimmable_light': return 'Dimmable Light';
            case 'outlet': return 'Outlet';
            case 'button': return 'Button';
            case 'scene': return 'Scene';
            case 'color_light': return 'Color Light';
            default: return kind;
        }
    }

    function uniquePlaceholderName(base) {
        const names = allWorkingNames();
        let candidate = 'New ' + base;
        let n = 1;
        while (names.has(candidate)) {
            n++;
            candidate = 'New ' + base + ' ' + n;
        }
        return candidate;
    }

    // propagateRename rewrites every reference to oldName in the working copy
    // (button control-relation targets, scene action device names) to
    // newName. Called once, at rename commit (blur), for controllable kinds.
    function propagateRename(oldName, newName) {
        (S.working.Buttons || []).forEach(function(b) {
            (b.ControlDevices || []).forEach(function(cd) {
                if (cd.DeviceName === oldName) cd.DeviceName = newName;
            });
        });
        (S.working.Scenes || []).forEach(function(s) {
            (s.States || []).forEach(function(st) {
                st.Actions = (st.Actions || []).map(function(actionStr) {
                    const parsed = parseActionString(actionStr);
                    if (parsed && parsed.device === oldName) {
                        parsed.device = newName;
                        return formatActionString(parsed);
                    }
                    return actionStr;
                });
            });
        });
    }

    function isBrightnessVerb(verb) {
        return verb === 'brightness' || verb === 'brightness_up' || verb === 'brightness_down';
    }

    // parseActionString / formatActionString mirror app.ParseAction / Action.String
    // (action.go): "<verb>:<device>" for on/off/toggle, "<verb>:<level>:<device>"
    // for the brightness family. Used client-side for rename propagation and
    // for building the live edit-mode diagram's scene_action edges.
    function parseActionString(s) {
        if (!s) return null;
        const parts = String(s).split(':');
        const verb = (parts[0] || '').toLowerCase();
        if (verb === 'on' || verb === 'off' || verb === 'toggle') {
            if (parts.length !== 2) return null;
            return { verb: verb, level: 0, device: parts[1] };
        }
        if (isBrightnessVerb(verb)) {
            if (parts.length !== 3) return null;
            const level = parseInt(parts[1], 10);
            if (isNaN(level)) return null;
            return { verb: verb, level: level, device: parts[2] };
        }
        return null;
    }

    function formatActionString(a) {
        if (isBrightnessVerb(a.verb)) return a.verb + ':' + a.level + ':' + a.device;
        return a.verb + ':' + a.device;
    }

    // sanitizeWorkingCopy strips the internal-only _editId/_isNew bookkeeping
    // fields before the working copy is sent to the server.
    function sanitizeWorkingCopy(working) {
        return JSON.parse(JSON.stringify(working, function(key, value) {
            if (key.indexOf('_') === 0) return undefined;
            return value;
        }));
    }

    // ---- Add / delete devices ----

    function buildAddMenu() {
        const menu = document.getElementById('schema-add-menu');
        if (!menu) return;
        menu.textContent = '';
        [
            { kind: 'light', icon: '\u{1F4A1}' },
            { kind: 'dimmable_light', icon: '\u{1F506}' },
            { kind: 'outlet', icon: '\u{1F50C}' },
            { kind: 'button', icon: '\u{1F446}' },
            { kind: 'scene', icon: '\u{1F3AC}' },
        ].forEach(function(it) {
            const btn = mkEl('button', { class: 'schema-add-menu-item' }, it.icon + ' ' + kindLabel(it.kind));
            btn.addEventListener('click', function() {
                addDevice(it.kind);
                menu.style.display = 'none';
            });
            menu.appendChild(btn);
        });
    }

    function addDevice(kind) {
        if (!S.working) return;
        const name = uniquePlaceholderName(kindLabel(kind));
        let entry;
        switch (kind) {
            case 'light':
                entry = withEditId({ Name: name, DigitalOutName: '', DisableHomekit: false });
                S.working.Lights.push(entry);
                break;
            case 'dimmable_light':
                entry = withEditId({ Name: name, DigitalOutName: '', AnalogOutName: '', DefaultSetpoint: 0, DisableHomekit: false });
                S.working.DimmableLights.push(entry);
                break;
            case 'outlet':
                entry = withEditId({ Name: name, DigitalOutName: '', DisableHomekit: false });
                S.working.Outlets.push(entry);
                break;
            case 'button':
                entry = withEditId({ Name: name, EventInputName: '', DisableHomekit: false, ControlDevices: [] });
                S.working.Buttons.push(entry);
                break;
            case 'scene':
                entry = withEditId({ Name: name, States: [] });
                S.working.Scenes.push(entry);
                break;
            default:
                return;
        }
        entry._isNew = true;
        S.selectedEdit = { kind: kind, editId: entry._editId };
        S.selectedId = 'device:' + kind + ':' + name;
        markDirty();
        redrawEdit();
    }

    // ---- Client-side edit graph (mirrors buildSchemaGraph/schema.go's shape) ----

    function buildEditGraph(working, meta, colorLights) {
        const nodes = [];
        const edges = [];
        const targetIndex = new Map();  // controllable device name -> node id
        const missingNodes = new Map(); // dangling target name -> placeholder node id
        const ioNodesSeen = new Set();
        const driverNodesSeen = new Set();
        const knownDrivers = new Set((meta && meta.drivers) || []);

        // Color lights aren't part of EditableConfig (read-only, see
        // buildColorLightReadOnly/renderEditPanel); include them verbatim
        // from the live snapshot so they resolve as valid targets instead of
        // dangling-target placeholders.
        (colorLights || []).forEach(function(n) {
            nodes.push(n);
            targetIndex.set(n.label, n.id);
        });

        function addDeviceNode(kind, entry, detail) {
            const id = 'device:' + kind + ':' + entry.Name;
            nodes.push({
                id: id, kind: 'device', device_type: kind, label: entry.Name,
                homekit: kind === 'scene' ? false : !entry.DisableHomekit,
                healthy: true, faulty: false, is_on: false,
                unsaved: !!entry._isNew,
                detail: detail || null,
            });
            if (kind !== 'button') targetIndex.set(entry.Name, id);
            return id;
        }

        function addIoEdge(fromId, ioId, role) {
            if (!ioId) return;
            const nodeId = 'io:' + ioId;
            if (!ioNodesSeen.has(nodeId)) {
                ioNodesSeen.add(nodeId);
                const parts = String(ioId).split('|');
                if (parts.length !== 3) {
                    nodes.push({ id: nodeId, kind: 'io', label: ioId, invalid: true });
                } else {
                    const driver = parts[0], ioType = parts[1], name = parts[2];
                    nodes.push({ id: nodeId, kind: 'io', driver: driver, io_type: ioType, label: name });
                    if (!driverNodesSeen.has(driver)) {
                        driverNodesSeen.add(driver);
                        const known = knownDrivers.has(driver);
                        nodes.push({ id: 'driver:' + driver, kind: 'driver', label: driver, ready: known, missing: !known });
                    }
                }
            }
            edges.push({ kind: 'io', from: fromId, to: nodeId, role: role });
        }

        function resolveTarget(name) {
            if (targetIndex.has(name)) return targetIndex.get(name);
            if (missingNodes.has(name)) return missingNodes.get(name);
            const id = 'device:missing:' + name;
            missingNodes.set(name, id);
            nodes.push({ id: id, kind: 'device', device_type: 'missing', label: name, missing: true });
            return id;
        }

        (working.Lights || []).forEach(function(l) {
            const id = addDeviceNode('light', l, { output_io_id: l.DigitalOutName });
            addIoEdge(id, l.DigitalOutName, 'output');
        });
        (working.Outlets || []).forEach(function(o) {
            const id = addDeviceNode('outlet', o, { output_io_id: o.DigitalOutName });
            addIoEdge(id, o.DigitalOutName, 'output');
        });
        (working.DimmableLights || []).forEach(function(dl) {
            const id = addDeviceNode('dimmable_light', dl, { output_io_id: dl.DigitalOutName, analog_io_id: dl.AnalogOutName });
            addIoEdge(id, dl.DigitalOutName, 'output');
            addIoEdge(id, dl.AnalogOutName, 'analog');
        });
        (working.Scenes || []).forEach(function(s) {
            addDeviceNode('scene', s, { state_names: (s.States || []).map(function(st) { return st.Name; }) });
        });
        (working.Buttons || []).forEach(function(b) {
            const id = addDeviceNode('button', b, { event_input_id: b.EventInputName });
            addIoEdge(id, b.EventInputName, 'event_input');
        });

        (working.Buttons || []).forEach(function(b) {
            const fromId = 'device:button:' + b.Name;
            (b.ControlDevices || []).forEach(function(cd) {
                if (!cd.DeviceName) return;
                const toId = resolveTarget(cd.DeviceName);
                edges.push({ kind: 'control', from: fromId, to: toId, event: cd.EventType, action: cd.Action, level: cd.Level || 0 });
            });
        });
        (working.Scenes || []).forEach(function(s) {
            const fromId = 'device:scene:' + s.Name;
            (s.States || []).forEach(function(st) {
                (st.Actions || []).forEach(function(actionStr) {
                    const parsed = parseActionString(actionStr);
                    if (!parsed) return;
                    const toId = resolveTarget(parsed.device);
                    edges.push({ kind: 'scene_action', from: fromId, to: toId, state: st.Name, action: parsed.verb, level: parsed.level || 0 });
                });
            });
        });

        return { nodes: nodes, edges: edges };
    }

    // ---- Form field builders ----

    function labeledField(labelText, inputEl) {
        const wrap = mkEl('div', { class: 'edit-field' });
        wrap.appendChild(mkEl('label', { class: 'edit-label' }, labelText));
        wrap.appendChild(inputEl);
        return wrap;
    }

    // textInputEl commits on blur, but only calls onCommit when the value
    // actually changed - a blur that leaves the value untouched (e.g. tab-out
    // without editing) must not markDirty/redraw (see call sites, which do
    // both inside onCommit).
    function textInputEl(value, onCommit) {
        const initial = value || '';
        const input = mkEl('input', { type: 'text', class: 'edit-input' });
        input.value = initial;
        input.addEventListener('blur', function() {
            if (input.value === initial) return; // unchanged: no-op
            onCommit(input.value);
        });
        input.addEventListener('keydown', function(e) { if (e.key === 'Enter') input.blur(); });
        return input;
    }

    // numberInputEl: see textInputEl for the unchanged-value no-op rationale.
    function numberInputEl(value, min, max, onCommit) {
        const initial = (value != null) ? value : 0;
        const input = mkEl('input', { type: 'number', class: 'edit-input', min: min, max: max });
        input.value = initial;
        input.addEventListener('blur', function() {
            const parsed = parseInt(input.value, 10) || 0;
            if (parsed === initial) return; // unchanged: no-op
            onCommit(parsed);
        });
        input.addEventListener('keydown', function(e) { if (e.key === 'Enter') input.blur(); });
        return input;
    }

    function checkboxFieldEl(labelText, checked, onChange) {
        const wrap = mkEl('label', { class: 'edit-checkbox-label' });
        const cb = mkEl('input', { type: 'checkbox' });
        cb.checked = !!checked;
        cb.addEventListener('change', function() { onChange(cb.checked); });
        wrap.appendChild(cb);
        wrap.appendChild(document.createTextNode(' ' + labelText));
        return wrap;
    }

    // selectFieldEl builds a <select> from options. If value is set but not
    // among options (e.g. a control-relation target that was renamed/deleted
    // elsewhere in the working copy, or an event/verb the meta vocab doesn't
    // list), a synthetic "(missing) <value>" entry is prepended and selected
    // so the select's displayed state doesn't silently disagree with the
    // working copy - the option's actual value is still the real (unchanged)
    // value, so leaving it alone commits nothing new.
    function selectFieldEl(options, value, onChange) {
        const sel = mkEl('select', { class: 'edit-select' });
        const hasValue = value !== undefined && value !== null && value !== '';
        const missing = hasValue && options.indexOf(value) === -1;
        if (missing) {
            const missingOpt = mkEl('option', { value: value }, '(missing) ' + value);
            missingOpt.selected = true;
            sel.appendChild(missingOpt);
        }
        options.forEach(function(opt) {
            const o = mkEl('option', { value: opt }, opt);
            if (!missing && opt === value) o.selected = true;
            sel.appendChild(o);
        });
        sel.addEventListener('change', function() { onChange(sel.value); });
        return sel;
    }

    let ioListSeq = 0;

    function ioFieldEl(labelText, value, ioType, onCommit) {
        const wrap = mkEl('div', { class: 'edit-field' });
        wrap.appendChild(mkEl('label', { class: 'edit-label' }, labelText));
        const listId = 'schema-io-list-' + (ioListSeq++);
        const input = mkEl('input', { type: 'text', class: 'edit-input mono', list: listId, placeholder: 'driver|type|name' });
        const initial = value || '';
        input.value = initial;
        input.addEventListener('blur', function() {
            const trimmed = input.value.trim();
            if (trimmed === initial) return; // unchanged: no-op
            onCommit(trimmed);
        });
        input.addEventListener('keydown', function(e) { if (e.key === 'Enter') input.blur(); });
        wrap.appendChild(input);

        const datalist = mkEl('datalist', { id: listId });
        const suggestions = (S.editData && S.editData.meta && S.editData.meta.io_suggestions && S.editData.meta.io_suggestions[ioType]) || [];
        suggestions.forEach(function(s) {
            const opt = mkEl('option', { value: s.id });
            opt.textContent = s.configured_as ? (s.label + ' (taken: ' + s.configured_as + ')') : s.label;
            datalist.appendChild(opt);
        });
        wrap.appendChild(datalist);
        return wrap;
    }

    // targetNameOptions lists the valid control-relation/scene-action target
    // names. It must derive only from the live working copy (+ the
    // color-light snapshot taken on entering edit mode) - never from
    // S.editData.meta.output_device_names, which is a snapshot fixed at the
    // moment edit mode was entered and goes stale the instant a device is
    // renamed, added or deleted in the working copy.
    function targetNameOptions() {
        const set = new Set();
        ['Lights', 'DimmableLights', 'Outlets', 'Scenes'].forEach(function(key) {
            (S.working[key] || []).forEach(function(e) { set.add(e.Name); });
        });
        (S.colorLightNodes || []).forEach(function(n) { set.add(n.label); });
        return Array.from(set).sort();
    }

    // buildNameField builds its own input (rather than reusing textInputEl)
    // so it can show an inline validation error without rebuilding the panel
    // - a rename that fails validation (empty, or contains ':') must not
    // trigger a redraw, both per the "no-op → no redraw" rule (finding 9) and
    // so the offending text the user typed stays visible next to the error
    // instead of silently reverting.
    function buildNameField(panel, entry, kind) {
        const wrap = mkEl('div', { class: 'edit-field' });
        wrap.appendChild(mkEl('label', { class: 'edit-label' }, 'Name'));
        const initial = entry.Name || '';
        const input = mkEl('input', { type: 'text', class: 'edit-input' });
        input.value = initial;
        const errEl = mkEl('div', { class: 'edit-field-error' });
        errEl.style.display = 'none';

        input.addEventListener('blur', function() {
            const trimmed = input.value.trim();
            if (trimmed === initial) {
                errEl.style.display = 'none';
                return; // unchanged: no-op, no markDirty/no redraw
            }
            if (!trimmed) {
                errEl.textContent = 'Name must not be empty';
                errEl.style.display = '';
                return;
            }
            if (trimmed.indexOf(':') !== -1) {
                errEl.textContent = "Name must not contain ':' (colons delimit the control-relation/action grammar)";
                errEl.style.display = '';
                return;
            }
            errEl.style.display = 'none';
            const old = entry.Name;
            entry.Name = trimmed;
            if (kind === 'light' || kind === 'dimmable_light' || kind === 'outlet' || kind === 'scene') {
                propagateRename(old, trimmed);
            }
            S.selectedId = 'device:' + kind + ':' + trimmed;
            markDirty();
            scheduleRedrawEdit();
        });
        input.addEventListener('keydown', function(e) { if (e.key === 'Enter') input.blur(); });

        wrap.appendChild(input);
        wrap.appendChild(errEl);
        panel.appendChild(wrap);
    }

    function buildLightForm(panel, entry) {
        buildNameField(panel, entry, 'light');
        panel.appendChild(ioFieldEl('Digital Out', entry.DigitalOutName, 'd_out', function(v) {
            entry.DigitalOutName = v; markDirty(); scheduleRedrawEdit();
        }));
        panel.appendChild(checkboxFieldEl('Disable HomeKit', entry.DisableHomekit, function(v) {
            entry.DisableHomekit = v; markDirty(); redrawEdit();
        }));
    }

    function buildOutletForm(panel, entry) {
        buildNameField(panel, entry, 'outlet');
        panel.appendChild(ioFieldEl('Digital Out', entry.DigitalOutName, 'd_out', function(v) {
            entry.DigitalOutName = v; markDirty(); scheduleRedrawEdit();
        }));
        panel.appendChild(checkboxFieldEl('Disable HomeKit', entry.DisableHomekit, function(v) {
            entry.DisableHomekit = v; markDirty(); redrawEdit();
        }));
    }

    function buildDimmableForm(panel, entry) {
        buildNameField(panel, entry, 'dimmable_light');
        panel.appendChild(ioFieldEl('Digital Out', entry.DigitalOutName, 'd_out', function(v) {
            entry.DigitalOutName = v; markDirty(); scheduleRedrawEdit();
        }));
        panel.appendChild(ioFieldEl('Analog Out', entry.AnalogOutName, 'a_out', function(v) {
            entry.AnalogOutName = v; markDirty(); scheduleRedrawEdit();
        }));
        panel.appendChild(labeledField('Default Setpoint (0-100)', numberInputEl(entry.DefaultSetpoint, 0, 100, function(v) {
            entry.DefaultSetpoint = Math.max(0, Math.min(100, v)); markDirty(); scheduleRedrawEdit();
        })));
        panel.appendChild(checkboxFieldEl('Disable HomeKit', entry.DisableHomekit, function(v) {
            entry.DisableHomekit = v; markDirty(); redrawEdit();
        }));
    }

    function buildButtonForm(panel, entry) {
        buildNameField(panel, entry, 'button');
        panel.appendChild(ioFieldEl('Event Input', entry.EventInputName, 'push_event', function(v) {
            entry.EventInputName = v; markDirty(); scheduleRedrawEdit();
        }));
        panel.appendChild(checkboxFieldEl('Disable HomeKit', entry.DisableHomekit, function(v) {
            entry.DisableHomekit = v; markDirty(); redrawEdit();
        }));

        panel.appendChild(mkEl('div', { class: 'schema-panel-section' }, 'Control relations'));
        entry.ControlDevices = entry.ControlDevices || [];
        const list = mkEl('div', { class: 'edit-relation-list' });
        entry.ControlDevices.forEach(function(cd, idx) {
            list.appendChild(buildControlRelationRow(entry, cd, idx));
        });
        panel.appendChild(list);

        const addBtn = mkEl('button', { class: 'io-filter-btn' }, '+ Add relation');
        addBtn.addEventListener('click', function() {
            const targets = targetNameOptions();
            entry.ControlDevices.push({ EventType: 'single_press', Action: 'toggle', Level: 0, DeviceName: targets[0] || '' });
            markDirty();
            redrawEdit();
        });
        panel.appendChild(addBtn);
    }

    function buildControlRelationRow(button, cd, idx) {
        const meta = (S.editData && S.editData.meta) || {};
        const eventTypes = meta.event_types || ['single_press', 'double_press', 'triple_press', 'long_press'];
        const verbs = meta.action_verbs || ['on', 'off', 'toggle', 'brightness', 'brightness_up', 'brightness_down'];

        const row = mkEl('div', { class: 'edit-relation-row' });
        row.appendChild(selectFieldEl(eventTypes, cd.EventType, function(v) { cd.EventType = v; markDirty(); redrawEdit(); }));
        row.appendChild(selectFieldEl(verbs, cd.Action, function(v) { cd.Action = v; markDirty(); redrawEdit(); }));
        if (isBrightnessVerb(cd.Action)) {
            row.appendChild(numberInputEl(cd.Level, 0, 100, function(v) { cd.Level = Math.max(0, Math.min(100, v)); markDirty(); scheduleRedrawEdit(); }));
        }
        row.appendChild(selectFieldEl(targetNameOptions(), cd.DeviceName, function(v) { cd.DeviceName = v; markDirty(); redrawEdit(); }));
        const rmBtn = mkEl('button', { class: 'io-filter-btn schema-row-remove' }, '✕');
        rmBtn.addEventListener('click', function() {
            button.ControlDevices.splice(idx, 1);
            markDirty();
            redrawEdit();
        });
        row.appendChild(rmBtn);
        return row;
    }

    function buildSceneForm(panel, entry) {
        buildNameField(panel, entry, 'scene');
        panel.appendChild(mkEl('div', { class: 'schema-panel-section' }, 'States'));
        entry.States = entry.States || [];
        const list = mkEl('div', { class: 'edit-state-list' });
        entry.States.forEach(function(st, idx) {
            list.appendChild(buildSceneStateRow(entry, st, idx));
        });
        panel.appendChild(list);

        const addBtn = mkEl('button', { class: 'io-filter-btn' }, '+ Add state');
        addBtn.addEventListener('click', function() {
            entry.States.push({ Name: 'state' + (entry.States.length + 1), Actions: [] });
            markDirty();
            redrawEdit();
        });
        panel.appendChild(addBtn);
    }

    function buildSceneStateRow(scene, st, idx) {
        const wrap = mkEl('div', { class: 'edit-state-row' });
        wrap.appendChild(labeledField('State name', textInputEl(st.Name, function(v) {
            const trimmed = v.trim();
            if (!trimmed || trimmed === st.Name) return; // invalid or unchanged: no-op
            st.Name = trimmed;
            markDirty();
            scheduleRedrawEdit();
        })));

        const ta = mkEl('textarea', { class: 'edit-textarea', rows: 4 });
        const initialActions = (st.Actions || []).join('\n');
        ta.value = initialActions;
        ta.addEventListener('blur', function() {
            if (ta.value === initialActions) return; // unchanged: no-op
            st.Actions = ta.value.split('\n').map(function(s) { return s.trim(); }).filter(function(s) { return s !== ''; });
            markDirty();
            scheduleRedrawEdit();
        });
        wrap.appendChild(labeledField('Actions (one per line)', ta));

        const rmBtn = mkEl('button', { class: 'io-filter-btn schema-row-remove' }, '✕ Remove state');
        rmBtn.addEventListener('click', function() {
            scene.States.splice(idx, 1);
            markDirty();
            redrawEdit();
        });
        wrap.appendChild(rmBtn);
        return wrap;
    }

    function buildErrorsBlock(errors) {
        const wrap = mkEl('div', { class: 'schema-panel-errors' });
        wrap.appendChild(mkEl('div', { class: 'schema-panel-section text-error' }, 'Could not save'));
        const ul = mkEl('ul', { class: 'schema-panel-list' });
        errors.forEach(function(e) {
            ul.appendChild(mkEl('li', { class: 'text-error' }, e));
        });
        wrap.appendChild(ul);
        return wrap;
    }

    // renderEditPanel builds the stage 2 edit form for S.selectedEdit into
    // #schema-detail-panel via createElement/textContent only - never
    // innerHTML with a user-controlled string (device names, io ids, scene
    // action text are all attacker-controllable in principle).
    function renderEditPanel() {
        const panel = document.getElementById('schema-detail-panel');
        if (!panel) return;
        panel.textContent = '';

        if (!S.selectedEdit) {
            panel.classList.remove('open');
            return;
        }

        if (S.selectedEdit.kind === 'color_light') {
            const closeBtn = mkEl('button', { class: 'schema-panel-close', 'aria-label': 'Close' }, '✕');
            closeBtn.addEventListener('click', function() { selectNode(null); });
            panel.appendChild(closeBtn);
            panel.appendChild(mkEl('div', { class: 'schema-panel-title' }, '\u{1F308} ' + S.selectedEdit.label));
            panel.appendChild(mkEl('div', { class: 'text-muted' }, 'Color lights are not editable here yet.'));
            panel.classList.add('open');
            return;
        }

        const entry = findWorkingEntry(S.selectedEdit.kind, S.selectedEdit.editId);
        if (!entry) {
            S.selectedEdit = null;
            panel.classList.remove('open');
            return;
        }

        const closeBtn = mkEl('button', { class: 'schema-panel-close', 'aria-label': 'Close' }, '✕');
        closeBtn.addEventListener('click', function() { selectNode(null); });
        panel.appendChild(closeBtn);
        panel.appendChild(mkEl('div', { class: 'schema-panel-title' }, 'Edit ' + kindLabel(S.selectedEdit.kind)));

        switch (S.selectedEdit.kind) {
            case 'light': buildLightForm(panel, entry); break;
            case 'dimmable_light': buildDimmableForm(panel, entry); break;
            case 'outlet': buildOutletForm(panel, entry); break;
            case 'button': buildButtonForm(panel, entry); break;
            case 'scene': buildSceneForm(panel, entry); break;
        }

        if (S.saveErrors && S.saveErrors.length) {
            panel.appendChild(buildErrorsBlock(S.saveErrors));
        }

        const actions = mkEl('div', { class: 'schema-panel-actions' });
        const delBtn = mkEl('button', { class: 'io-filter-btn schema-delete-btn' }, '🗑 Delete');
        delBtn.addEventListener('click', function() {
            if (!confirm('Delete this ' + kindLabel(S.selectedEdit.kind) + '? This cannot be undone.')) return;
            deleteWorkingEntry(S.selectedEdit.kind, S.selectedEdit.editId);
            S.selectedEdit = null;
            S.selectedId = null;
            markDirty();
            redrawEdit();
        });
        actions.appendChild(delBtn);
        panel.appendChild(actions);

        panel.classList.add('open');
    }

    // ---- Save ----

    let toastEl = null;
    let toastTimer = null;

    function showToast(msg) {
        if (!toastEl) {
            toastEl = document.createElement('div');
            toastEl.className = 'schema-toast';
            document.body.appendChild(toastEl);
        }
        toastEl.textContent = msg;
        toastEl.classList.add('visible');
        if (toastTimer) clearTimeout(toastTimer);
        toastTimer = setTimeout(function() { toastEl.classList.remove('visible'); }, 3000);
    }

    async function saveChanges() {
        const saveBtn = document.getElementById('schema-save-btn');
        if (saveBtn) saveBtn.disabled = true;
        setStatus('Saving…');
        try {
            const payload = sanitizeWorkingCopy(S.working);
            const resp = await fetch('/api/config/edit', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ config: payload }),
            });
            let data;
            try {
                data = await resp.json();
            } catch (e) {
                data = { ok: false, errors: ['invalid response from server'] };
            }
            if (!resp.ok || !data.ok) {
                S.saveErrors = (data.errors && data.errors.length) ? data.errors : ['save failed (HTTP ' + resp.status + ')'];
                setStatus('');
                renderEditPanel();
                return;
            }
            S.saveErrors = null;
            setStatus('');
            showToast('Saved — config reload triggered');
            exitEditMode({ refetch: false });
            setTimeout(loadAndDraw, 1500);
        } catch (e) {
            S.saveErrors = ['network error: ' + (e && e.message || e)];
            renderEditPanel();
        } finally {
            if (saveBtn) saveBtn.disabled = false;
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

    window.swkitSchema = {
        render: render,
        hasUnsavedChanges: function() { return S.editMode && S.dirty; },
        discardSilently: function() {
            S.editMode = false;
            S.working = null;
            S.dirty = false;
            S.selectedEdit = null;
            S.saveErrors = null;
        },
    };
})();
