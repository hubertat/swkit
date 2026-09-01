// swkit web ui - config schema diagram (Stage 1: read-only)
//
// Layout: a layered SVG diagram, columns [Driver inputs] [Buttons] [Scenes]
// [Devices] [Driver outputs] (the driver-IO columns only appear when the
// "driver I/O" toggle is on), built from GET /api/schema. All mutable UI
// state lives in one module-level object (S) so a future edit mode can
// extend it without restructuring. Graph layout (pure, no DOM) is kept
// separate from SVG rendering (buildLayout vs renderDiagram) for the same
// reason.

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
    const IO_COL_W = IO_PILL_W + GROUP_PAD * 2;
    const HEADER_H = 22; // vertical band above each column for its header label (item 6); content stacking starts at MARGIN + HEADER_H, not MARGIN alone.
    const EDGE_FAN_GAP = 6; // per-member y offset for parallel edges sharing one from|to|kind (item 4)
    const SCENE_STATE_LABEL_MAX_CHARS = 24; // a scene state row is a bit wider than an IO pill (NODE_W - GROUP_PAD*2 vs IO_PILL_W)

    // ---- Live overlay constants (Stage 3: round-2 feedback item 3) ----

    const RECENCY_STRONG_MS = 10000;
    const RECENCY_MEDIUM_MS = 30000;
    const RECENCY_FADE_MS = 90000;
    const EVENT_FLASH_MS = 2000;
    const OVERLAY_GAP_RESEED_MS = 3000;

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

        // ---- Live overlay (round-2 item 3): fed by app.js's 1s /api/state
        // poll via render(state). Overlay-only - never touched by/touches
        // layout or node creation. Skipped entirely while S.editMode.
        liveTrack: new Map(), // nodeId -> {sig, changedAt}; recency bookkeeping across polls
        lastOverlayAt: 0,     // Date.now() of the last applied tick; detects overlay gaps (tab backgrounding, edit sessions)
        pendingReseed: false, // set on leaving edit mode; forces the next tick to re-seed instead of glowing
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

    // resolveScenePortId computes the geometry port id a scene_action edge's
    // source actually leaves from: the owning state's sub-row (item 2) when
    // edge.state unambiguously matches one of the scene's configured state
    // names, or the scene's own (header) node id as a safe fallback
    // otherwise - e.g. a state renamed/removed since this schema snapshot
    // was taken (unmatched), or a hand-edited config.json with two states
    // sharing a name (ambiguous: nothing server-side rejects a duplicate
    // state name reaching the client - see scene.go's NewScene - and
    // edge.state is a bare name, not an index, so there is no way to tell
    // which of the two rows actually owns a given action). Falling back to
    // the header in the ambiguous case is deliberate (finding F5): picking
    // "the first one anyway" would render a specific, plausible-looking, but
    // possibly wrong state->action mapping - exactly what per-state ports
    // exist to prevent - whereas the header fallback is honestly neutral.
    // Ports are keyed by state INDEX below ("#state@<idx>"), not name, so
    // two same-named states still get two distinct, non-colliding rows even
    // though an edge naming them can't itself be attributed to one. Pure
    // (node-map lookup only, no DOM), so it is shared by buildLayout's device
    // barycentring and renderDiagram's edge routing/fanning - both need to
    // agree on where a scene_action edge "really" starts.
    function resolveScenePortId(edge, nMap) {
        if (edge.kind !== 'scene_action') return edge.from;
        const node = nMap.get(edge.from);
        if (!node || node.device_type !== 'scene') return edge.from;
        const states = (node.detail && node.detail.state_names) || [];
        const firstIdx = states.indexOf(edge.state);
        if (firstIdx === -1) return edge.from; // unmatched
        if (states.lastIndexOf(edge.state) !== firstIdx) return edge.from; // ambiguous: duplicate state name
        return edge.from + '#state@' + firstIdx;
    }

    // ==================================================================
    // Graph build (pure): data -> positioned layout. No DOM access here.
    // ==================================================================

    // ioSide classifies an io node as belonging to the left (input) or right
    // (output) driver/IO column: push_event/d_in are inputs (wired from
    // Controls), d_out/a_out/rgbw_out are outputs (wired from Devices).
    // Invalid ids and any other/unknown io_type default to the right/output
    // side, per the round-2 spec ("unknown/invalid ids default to output
    // side, right") - the /api/schema payload itself is unchanged, this is
    // purely a client-side layout classification of the same nodes.
    function ioSide(io) {
        if (io.invalid || !io.io_type) return 'right';
        return (io.io_type === 'push_event' || io.io_type === 'd_in') ? 'left' : 'right';
    }

    function buildLayout(data, showIo) {
        const nodes = data.nodes || [];
        const edges = data.edges || [];
        const nMap = nodeMap(data);

        // Column split (item 1): Controls used to hold both buttons and
        // scenes at the same x, so a button->scene or scene->scene edge
        // computed leftToRight = from.x <= to.x as true even though it ran
        // backwards through both node bodies (P1) - equal x is never really
        // "left to right". Splitting Scenes into its own column, to the
        // RIGHT of Buttons, makes button->scene and scene->device both
        // genuinely flow left-to-right; only scene->scene remains
        // same-column, handled below by sameColumnPath (item 3).
        const buttons = nodes.filter(n => n.kind === 'device' && n.device_type === 'button');
        const scenes = nodes.filter(n => n.kind === 'device' && n.device_type === 'scene');
        const devices = nodes.filter(n => n.kind === 'device' && n.device_type !== 'button' && n.device_type !== 'scene');
        const drivers = nodes.filter(n => n.kind === 'driver');
        const ios = nodes.filter(n => n.kind === 'io');

        // Column order: [Driver inputs] [Buttons] [Scenes] [Devices] [Driver
        // outputs]. The two IO columns only take up space when showIo is on;
        // collapsed, Buttons/Scenes/Devices sit exactly where they would with
        // no IO columns at all. Scenes gets the same treatment (finding F6):
        // most existing configs have no scenes at all, and reserving a full
        // NODE_W+COL_GAP column - plus its own header - for a column that
        // will render nothing widened every scene-less diagram by ~300px for
        // no reason.
        const ioColSpan = showIo ? IO_COL_W + COL_GAP : 0;
        const scenesColSpan = scenes.length ? NODE_W + COL_GAP : 0;
        const colX = {
            driversLeft: MARGIN,
            controls: MARGIN + ioColSpan,
            scenes: MARGIN + ioColSpan + NODE_W + COL_GAP,
            devices: MARGIN + ioColSpan + NODE_W + COL_GAP + scenesColSpan,
            driversRight: MARGIN + ioColSpan + NODE_W + COL_GAP + scenesColSpan + NODE_W + COL_GAP,
        };
        // Content rows (buttons/scenes/devices/driver groups) start below a
        // header band (item 6) instead of flush against MARGIN.
        const CONTENT_TOP = MARGIN + HEADER_H;

        // ---- Buttons, kept in backend (config) order ----
        const buttonPos = new Map();
        buttons.forEach((n, i) => {
            buttonPos.set(n.id, { x: colX.controls, y: CONTENT_TOP + i * (NODE_H + ROW_GAP) });
        });

        // ---- Scenes: dedicated column, group boxes with per-state sub-rows
        // (item 2). A scene's height depends on its state count, so scenes
        // stack with dynamic heights - the same idiom as the driver group
        // boxes below (buildDriverGroup is the DOM-side model). Positioned
        // BEFORE devices, ordered by the average y of incoming *button*
        // control edges only (scenes have no IO of their own to barycenter
        // against - P4 - and this ordering is what lets the devices pass
        // below pool both buttons' and scenes' positions in one go).
        function sceneStateNames(n) {
            return (n.detail && n.detail.state_names) || [];
        }
        function sceneBoxHeight(states) {
            // A 0-state (legacy inert) scene still reserves one row's worth
            // of body, so it never collapses to header-only/zero height.
            const n = Math.max(states.length, 1);
            return GROUP_HEADER_H + GROUP_PAD * 2 + n * (IO_PILL_H + IO_PILL_GAP) - IO_PILL_GAP;
        }

        const incomingByScene = new Map();
        edges.filter(e => e.kind === 'control').forEach(e => {
            const p = buttonPos.get(e.from);
            if (!p) return;
            if (!incomingByScene.has(e.to)) incomingByScene.set(e.to, []);
            incomingByScene.get(e.to).push(p.y + NODE_H / 2);
        });
        const sceneKey = scenes.map((n, i) => {
            const ys = incomingByScene.get(n.id);
            const avg = ys && ys.length ? ys.reduce((a, b) => a + b, 0) / ys.length : i * (NODE_H + ROW_GAP);
            return { n, avg, i };
        });
        // Finding 11 tiebreaker (see layoutSide's comment below): keeps this
        // sort deterministic across equal/fallback avgs, same as its siblings.
        sceneKey.sort((a, b) => a.avg - b.avg || a.i - b.i);

        const scenePortPos = new Map(); // bare scene id -> header port; "id#state@idx" -> that state row's port
        const sceneList = [];
        let sceneY = CONTENT_TOP;
        sceneKey.forEach(({ n }) => {
            const states = sceneStateNames(n);
            const h = sceneBoxHeight(states);
            const x = colX.scenes;
            const headerY = sceneY + GROUP_HEADER_H / 2;
            scenePortPos.set(n.id, { x: x, xRight: x + NODE_W, y: headerY, halfH: GROUP_HEADER_H / 2 });
            const rowItems = states.map((name, idx) => {
                const y = sceneY + GROUP_HEADER_H + GROUP_PAD + idx * (IO_PILL_H + IO_PILL_GAP) + IO_PILL_H / 2;
                // Port ids for state rows are geometry-only (D6): the node id
                // itself ("device:scene:<name>") never changes, so
                // selection/adjacency/edit-mode lookups keyed on it keep
                // working untouched - only portFor and resolveScenePortId
                // ever see this "#state@" suffix. Keyed by INDEX, not name
                // (finding F5): a hand-edited config.json can carry two
                // states with the same name (nothing server-side rejects
                // it), and keying by name here would let the second row's
                // Map.set silently overwrite the first row's port, corrupting
                // both rows' edge geometry (an edge would appear to leave
                // from the wrong row's y).
                scenePortPos.set(n.id + '#state@' + idx, { x: x, xRight: x + NODE_W, y: y, halfH: IO_PILL_H / 2 });
                return { name: name, index: idx, y: y };
            });
            sceneList.push({ node: n, x: x, y: sceneY, w: NODE_W, h: h, headerY: headerY, states: rowItems });
            sceneY += h + ROW_GAP;
        });
        const scenesHeight = sceneList.length ? sceneY - ROW_GAP - CONTENT_TOP : 0;

        // ---- Devices, barycenter-ordered by ALL connected buttons+scenes ----
        // Pools control edges (button -> target) and scene_action edges
        // (scene -> target) into one incoming-y map per device, same as
        // before the column split - the only change is that a scene_action
        // edge now contributes the y of the specific STATE ROW that owns it
        // (falling back to the scene's header row - see resolveScenePortId)
        // instead of one scene-wide y, which only works because scenePortPos
        // above is already fully built.
        const relEdges = edges.filter(e => e.kind === 'control' || e.kind === 'scene_action');
        const incomingByDevice = new Map();
        relEdges.forEach(e => {
            let centerY;
            if (e.kind === 'control') {
                const p = buttonPos.get(e.from);
                if (p) centerY = p.y + NODE_H / 2;
            } else {
                const p = scenePortPos.get(resolveScenePortId(e, nMap));
                if (p) centerY = p.y; // scenePortPos y values are already row/header centers
            }
            if (centerY === undefined) return;
            if (!incomingByDevice.has(e.to)) incomingByDevice.set(e.to, []);
            incomingByDevice.get(e.to).push(centerY);
        });
        const deviceKey = devices.map((n, i) => {
            const ys = incomingByDevice.get(n.id);
            const avg = ys && ys.length ? ys.reduce((a, b) => a + b, 0) / ys.length : i * (NODE_H + ROW_GAP);
            return { n, avg, i };
        });
        deviceKey.sort((a, b) => a.avg - b.avg || a.i - b.i);
        const devicePos = new Map();
        deviceKey.forEach((d, i) => {
            devicePos.set(d.n.id, { x: colX.devices, y: CONTENT_TOP + i * (NODE_H + ROW_GAP) });
        });

        // ---- Driver inputs (left) & Driver outputs (right) (only when showIo) ----
        let leftGroups = [], rightGroups = [];
        let leftHeight = 0, rightHeight = 0;
        const ioPos = new Map();
        if (showIo) {
            const ioEdges = edges.filter(e => e.kind === 'io');
            // Two separate incoming-y maps: left-side pills are barycentered
            // against the Buttons column (buttons feeding event_input edges -
            // scenes have no IO of their own, see P4), right-side pills
            // against the Devices column (output/analog/rgbw edges).
            const fromControlY = new Map();
            const fromDeviceY = new Map();
            ioEdges.forEach(e => {
                const cp = buttonPos.get(e.from);
                const dp = cp ? null : devicePos.get(e.from);
                const p = cp || dp;
                if (!p) return;
                const bucket = cp ? fromControlY : fromDeviceY;
                if (!bucket.has(e.to)) bucket.set(e.to, []);
                bucket.get(e.to).push(p.y + NODE_H / 2);
            });

            // Real + placeholder driver nodes, in backend order; pills split
            // per-driver into left/right buckets by io type.
            const byDriverName = new Map();
            drivers.forEach(d => byDriverName.set(d.label, { driverNode: d, left: [], right: [] }));
            const invalidPills = [];
            ios.forEach((io, i) => {
                if (io.invalid || !io.driver) {
                    invalidPills.push({ n: io, i });
                    return;
                }
                if (!byDriverName.has(io.driver)) {
                    byDriverName.set(io.driver, { driverNode: null, left: [], right: [] });
                }
                byDriverName.get(io.driver)[ioSide(io)].push({ n: io, i });
            });

            // orderPills: barycenter-sort one driver box's pills by the avg y
            // of whichever column feeds them, falling back to config order.
            function orderPills(list, incomingMap) {
                const keyed = list.map(p => {
                    const ys = incomingMap.get(p.n.id);
                    const avg = ys && ys.length ? ys.reduce((a, b) => a + b, 0) / ys.length : p.i * (IO_PILL_H + IO_PILL_GAP);
                    return { n: p.n, avg, i: p.i };
                });
                keyed.sort((a, b) => a.avg - b.avg || a.i - b.i);
                return keyed;
            }

            // groupAvgY: barycenter for an entire driver box (pooling the y's
            // of every edge feeding any of its pills), so driver boxes
            // themselves - not just the pills inside them - are ordered by
            // their connected buttons (left) / devices (right).
            function groupAvgY(list, incomingMap, fallbackIndex) {
                const ys = [];
                list.forEach(p => {
                    const arr = incomingMap.get(p.n.id);
                    if (arr) ys.push.apply(ys, arr);
                });
                return ys.length ? ys.reduce((a, b) => a + b, 0) / ys.length : fallbackIndex * (IO_PILL_H + IO_PILL_GAP);
            }

            // layoutSide stacks one column's driver boxes top-to-bottom in
            // barycenter order, writing pill positions into the shared ioPos
            // map, and returns the built groups plus the column's height.
            function layoutSide(entries, incomingMap, x) {
                // Finding 11: index tiebreaker to match the deterministic
                // (stable, config-order-preserving) sort used by
                // deviceKey.sort and orderPills above - without it, two
                // driver boxes with an equal (or both-fallback) avg y could
                // swap order between renders/engines since Array#sort's
                // stability isn't guaranteed to be visited in insertion order
                // for equal keys pre-ES2019, and even where it is, relying on
                // that implicitly here would be inconsistent with its
                // siblings.
                const ordered = entries.map((e, i) => ({ e, avg: groupAvgY(e.rawPills, incomingMap, i), i }));
                ordered.sort((a, b) => a.avg - b.avg || a.i - b.i);
                const built = [];
                let gy = CONTENT_TOP;
                ordered.forEach(({ e }) => {
                    const keyed = orderPills(e.rawPills, incomingMap);
                    const boxH = GROUP_HEADER_H + GROUP_PAD * 2 + Math.max(keyed.length, 1) * (IO_PILL_H + IO_PILL_GAP) - (keyed.length ? IO_PILL_GAP : 0);
                    const boxY = gy;
                    keyed.forEach((k, idx) => {
                        ioPos.set(k.n.id, {
                            x: x + GROUP_PAD,
                            y: boxY + GROUP_HEADER_H + GROUP_PAD + idx * (IO_PILL_H + IO_PILL_GAP),
                        });
                    });
                    built.push({
                        driverNode: e.driverNode,
                        name: e.name,
                        x: x, y: boxY, w: IO_COL_W, h: boxH,
                        pills: keyed.map(k => k.n),
                        // Finding 5: carried through so buildDriverGroup can
                        // tell a dual-side box (one of a matched left/right
                        // pair for the same driver) from a single-box driver.
                        dual: !!e.dual,
                    });
                    gy += boxH + GROUP_GAP;
                });
                return { groups: built, height: built.length ? gy - GROUP_GAP : 0 };
            }

            const leftEntries = [];
            const rightEntries = [];
            byDriverName.forEach((group, driverName) => {
                // A driver with pills on both sides gets a box on each side,
                // same name+icon, disambiguated with an "(in)"/"(out)" suffix.
                const dual = group.left.length > 0 && group.right.length > 0;
                if (group.left.length) leftEntries.push({ name: dual ? driverName + ' (in)' : driverName, driverNode: group.driverNode, rawPills: group.left, dual: dual });
                if (group.right.length) rightEntries.push({ name: dual ? driverName + ' (out)' : driverName, driverNode: group.driverNode, rawPills: group.right, dual: dual });
                if (!group.left.length && !group.right.length && group.driverNode) {
                    // A known driver with nothing currently wired to it: keep
                    // it visible (readiness/IO-count still useful) on the
                    // right - the same default side as unknown/invalid ids.
                    rightEntries.push({ name: driverName, driverNode: group.driverNode, rawPills: [], dual: false });
                }
            });

            const leftLayout = layoutSide(leftEntries, fromControlY, colX.driversLeft);
            const rightLayout = layoutSide(rightEntries, fromDeviceY, colX.driversRight);
            leftGroups = leftLayout.groups;
            rightGroups = rightLayout.groups;
            leftHeight = leftLayout.height;
            rightHeight = rightLayout.height;

            if (invalidPills.length) {
                // Unsorted (no driver to barycenter against, same as before
                // the split): appended below the real driver boxes on the
                // right, their default side.
                const boxH = GROUP_HEADER_H + GROUP_PAD * 2 + invalidPills.length * (IO_PILL_H + IO_PILL_GAP) - IO_PILL_GAP;
                const boxY = rightGroups.length ? rightHeight + GROUP_GAP : CONTENT_TOP;
                invalidPills.forEach((p, idx) => {
                    ioPos.set(p.n.id, {
                        x: colX.driversRight + GROUP_PAD,
                        y: boxY + GROUP_HEADER_H + GROUP_PAD + idx * (IO_PILL_H + IO_PILL_GAP),
                    });
                });
                rightGroups.push({
                    driverNode: null,
                    invalid: true,
                    name: 'invalid ids',
                    x: colX.driversRight, y: boxY, w: IO_COL_W, h: boxH,
                    pills: invalidPills.map(p => p.n),
                });
                rightHeight = boxY + boxH;
            }
        }

        const buttonsHeight = buttons.length ? buttons.length * (NODE_H + ROW_GAP) - ROW_GAP : 0;
        const devicesHeight = devices.length ? devices.length * (NODE_H + ROW_GAP) - ROW_GAP : 0;
        const contentHeight = Math.max(buttonsHeight, scenesHeight, devicesHeight, leftHeight, rightHeight, NODE_H);

        const width = (showIo ? colX.driversRight + IO_COL_W : colX.devices + NODE_W) + MARGIN;
        const height = contentHeight + MARGIN * 2 + HEADER_H;

        // Edges to actually draw: hide io-kind edges (and anything touching a
        // hidden io node) when the driver/IO columns are collapsed.
        const drawEdges = edges.filter(e => showIo || e.kind !== 'io');

        // Column headers (item 6): plain data, no DOM - rendered as SVG text
        // by renderDiagram. Every column's header is conditional on that
        // column actually having content (finding F6) - an empty column
        // still labelled (e.g. "SCENES" over nothing) reads as a diagram bug,
        // not an intentionally empty section.
        const headers = [];
        if (showIo) headers.push({ x: colX.driversLeft, w: IO_COL_W, label: 'Inputs' });
        if (buttons.length) headers.push({ x: colX.controls, w: NODE_W, label: 'Buttons' });
        if (scenes.length) headers.push({ x: colX.scenes, w: NODE_W, label: 'Scenes' });
        if (devices.length) headers.push({ x: colX.devices, w: NODE_W, label: 'Devices' });
        if (showIo) headers.push({ x: colX.driversRight, w: IO_COL_W, label: 'Outputs' });

        return {
            width, height,
            headerY: MARGIN + HEADER_H / 2 + 4,
            headers: headers,
            buttons: buttons.map(n => Object.assign({ node: n }, buttonPos.get(n.id))),
            scenes: sceneList,
            devices: devices.map(n => Object.assign({ node: n }, devicePos.get(n.id))),
            groups: leftGroups.concat(rightGroups),
            ioPos,
            edges: drawEdges,
            nodeMap: nMap,
            portFor(id) {
                if (buttonPos.has(id)) { const p = buttonPos.get(id); return { x: p.x, xRight: p.x + NODE_W, y: p.y + NODE_H / 2, halfH: NODE_H / 2 }; }
                if (devicePos.has(id)) { const p = devicePos.get(id); return { x: p.x, xRight: p.x + NODE_W, y: p.y + NODE_H / 2, halfH: NODE_H / 2 }; }
                if (scenePortPos.has(id)) return scenePortPos.get(id);
                if (ioPos.has(id)) { const p = ioPos.get(id); return { x: p.x, xRight: p.x + IO_PILL_W, y: p.y + IO_PILL_H / 2, halfH: IO_PILL_H / 2 }; }
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

    // sameColumnPath (item 3, residual P1: scene->scene) routes an edge whose
    // endpoints share one x - today only possible for a scene targeting an
    // earlier scene, since Scenes is the only column that is both a source
    // and a target of rel edges - via each shape's LEFT edge, bulging out
    // into the gutter to their left, so the curve never crosses either node
    // body the way the old from.xRight->to.x fallback did (P1: leftToRight's
    // "<=" treated equal x as left-to-right). Guards y1 === y2 so a
    // degenerate same-point edge (a scene can never legally target itself -
    // see sceneTargetNameOptions/resolveControllable - but a stale/malformed
    // graph must still not hand the bezier a zero-length control vector).
    function sameColumnPath(x, y1, y2) {
        if (y1 === y2) y2 += 0.01;
        const bulge = Math.max(40, COL_GAP / 2);
        const gx = x - bulge;
        return 'M ' + x + ',' + y1 + ' C ' + gx + ',' + y1 + ' ' + gx + ',' + y2 + ' ' + x + ',' + y2;
    }

    // clampFan (item 4) turns a fan index (…, -1, 0, 1, …) into a y offset,
    // never exceeding either endpoint's own half-height - so a fanned edge's
    // shifted endpoint can never poke outside the shape it leaves from/
    // arrives at. fanMax is the group's own largest |fanIndex| (always
    // (memberCount-1)/2, since fanIndex is centered on 0); scaling the gap
    // down so the outermost member lands exactly on the limit - rather than
    // independently clamping each member's already-computed offset to that
    // same limit - keeps every member's offset distinct at any group size
    // (finding F12: pure clamping collapsed multiple outer members onto the
    // same clamped value once (memberCount-1)*EDGE_FAN_GAP/2 exceeded the
    // limit, e.g. 7 parallel edges into an IO pill's IO_PILL_H/2=11 limit
    // clamped to -11,-11,-6,0,6,11,11 - two pairs pixel-identical again,
    // re-creating the overlap the fanning exists to fix).
    function clampFan(fanIndex, fanMax, halfHFrom, halfHTo) {
        if (fanMax <= 0) return 0;
        const limit = Math.min(halfHFrom, halfHTo);
        const gap = Math.min(EDGE_FAN_GAP, limit / fanMax);
        return fanIndex * gap;
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
        const headerLayer = svgEl('g', { class: 'schema-headers' });
        // Edges render behind nodes (edgeLayer appended first) - this is what
        // already made IO edges pass behind driver group boxes, and now also
        // covers a button->device control edge that spans past the whole
        // Scenes column: its bezier midpoint arcs near/through scene boxes,
        // but sits underneath them, same as any other cross-column edge.
        svg.appendChild(edgeLayer);
        svg.appendChild(nodeLayer);
        svg.appendChild(headerLayer);

        // ---- Column headers (item 6) ----
        layout.headers.forEach(h => {
            const text = svgEl('text', { x: h.x + 4, y: layout.headerY, class: 'schema-col-header' });
            text.textContent = h.label;
            headerLayer.appendChild(text);
        });

        // ---- Edges ----
        // Parallel-edge fanning (item 4): group by from|to|kind using the
        // *resolved* port id, so a scene's own per-state ports (already
        // distinct - item 2) never get fanned redundantly on top of that. A
        // group with more than one member offsets each member's endpoint y
        // so no two edges between the same pair of ports render identically
        // (this covers e.g. one button with several event types all
        // targeting the same light).
        const edgeItems = layout.edges.map(edge => ({ edge: edge, fromId: resolveScenePortId(edge, layout.nodeMap) }));
        const fanGroups = new Map();
        edgeItems.forEach(item => {
            const key = item.fromId + '|' + item.edge.to + '|' + item.edge.kind;
            if (!fanGroups.has(key)) fanGroups.set(key, []);
            fanGroups.get(key).push(item);
        });
        fanGroups.forEach(group => {
            const fanMax = (group.length - 1) / 2;
            group.forEach((item, i) => { item.fanIndex = i - fanMax; item.fanMax = fanMax; });
        });

        edgeItems.forEach(item => {
            const edge = item.edge;
            const from = layout.portFor(item.fromId);
            const to = layout.portFor(edge.to);
            if (!from || !to) return;
            const color = edgeColor(edge);
            const isIo = edge.kind === 'io';
            const fanOffset = clampFan(item.fanIndex, item.fanMax, from.halfH, to.halfH);

            let d;
            if (from.x === to.x) {
                // Item 3: same-column edges (scene->scene) never use the
                // left-to-right bezier fallback below - see sameColumnPath.
                d = sameColumnPath(from.x, from.y + fanOffset, to.y + fanOffset);
            } else {
                // Direction-aware endpoints: with the driver I/O split, an io
                // edge's "to" (an input-side pill in the left column) can sit
                // to the LEFT of its "from" (a button in Buttons) - in that
                // case draw from from's LEFT edge to to's RIGHT edge so the
                // curve still runs between the two shapes' nearest edges,
                // instead of reaching backward across both node widths.
                // Everything else (left-to-right) is unchanged. Equal x is
                // handled above, so this comparison never needs "<=".
                const leftToRight = from.x < to.x;
                const x1 = leftToRight ? from.xRight : from.x;
                const x2 = leftToRight ? to.x : to.xRight;
                d = bezierPath(x1, from.y + fanOffset, x2, to.y + fanOffset);
            }

            const path = svgEl('path', {
                d: d,
                class: 'schema-edge' + (isIo ? ' schema-edge-io' : ' schema-edge-rel'),
                stroke: color,
                fill: 'none',
            });
            // D6: dataset.from carries the *resolved* port id (possibly a
            // scene's "id#state@N" sub-row) for debugging/inspection -
            // exactly which row an edge geometrically leaves from. Adjacency
            // comparisons must never derive the owning node id by string-
            // splitting this value: a device name is free to contain a
            // literal '#' (nothing server-side forbids it - config_edit.go's
            // name validation only rejects ':'), so a fold-at-'#' approach
            // corrupted any node whose real name contained one (finding F2 -
            // e.g. a light named "Lamp#1" got folded to "Lamp"). dataset.
            // fromNode instead carries the always-bare edge.from straight
            // from the schema/edit graph, so applyAdjacency below compares
            // like-for-like without parsing anything.
            path.dataset.from = item.fromId;
            path.dataset.fromNode = edge.from;
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

        // ---- Button / Scene / Device nodes ----
        layout.buttons.forEach(item => nodeLayer.appendChild(buildDeviceNode(item)));
        layout.scenes.forEach(item => nodeLayer.appendChild(buildSceneGroupNode(item)));
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
        // Iterate by code point (not UTF-16 code unit) so multi-unit
        // characters - e.g. emoji using surrogate pairs - are never split in
        // the middle, which would otherwise render as a broken glyph.
        var cps = Array.from(s);
        if (cps.length <= maxChars) return s;
        return cps.slice(0, Math.max(0, maxChars - 1)).join('') + '…';
    }

    const NODE_LABEL_MAX_CHARS = 22;
    const IO_PILL_LABEL_MAX_CHARS = 20;
    // A scene header's 24px band also carries the right-anchored live
    // "extra" state label (see buildSceneGroupNode) - something no other
    // node type shares its label row with - so it gets a tighter truncation
    // budget than NODE_LABEL_MAX_CHARS to leave that label room (finding
    // F13: at the full budget the two routinely overlapped).
    const SCENE_HEADER_LABEL_MAX_CHARS = 13;

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

    // buildDeviceNode renders a fixed-height NODE_H rect for a button or an
    // "ordinary" device (light/outlet/etc). Scenes are rendered by
    // buildSceneGroupNode instead (item 2's dynamic-height container box) -
    // layout.buttons/layout.devices never include a scene node, so the
    // device_type checks below only ever see 'missing' as a dashed variant.
    function buildDeviceNode(item) {
        const n = item.node;
        const g = svgEl('g', { class: 'schema-node schema-node-device', 'data-node-id': n.id });
        const dashed = n.missing;
        const rectAttrs = {
            x: item.x, y: item.y, width: NODE_W, height: NODE_H, rx: 8,
            class: 'schema-node-rect' + (dashed ? ' schema-node-dashed schema-node-missing' : ''),
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

        // Live-state overlay hooks (round-2 item 3): a state dot + a small
        // "extra" label (brightness %, active scene state, event flash),
        // seeded here from the schema node's own snapshot fields so the node
        // looks right before the first /api/state poll ever lands, then only
        // ever updated in place (class/text) by applyLiveOverlay - never
        // recreated. Skipped for "missing" placeholders (nothing to show)
        // and entirely in edit mode (the edit-graph carries no live state,
        // and the overlay itself bails out while S.editMode - see
        // applyLiveOverlay - so a dot here would just sit inert; simplest to
        // not build it at all).
        if (!n.missing && !S.editMode) {
            const dotCx = item.x + NODE_W - 10;
            const dotCy = item.y + NODE_H - 10;
            const dot = svgEl('circle', { cx: dotCx, cy: dotCy, r: 4.5, class: 'schema-state-dot' });
            const extra = svgEl('text', { x: dotCx - 10, y: dotCy + 3, class: 'schema-state-extra', 'text-anchor': 'end' });
            g.appendChild(dot);
            g.appendChild(extra);
            seedStateOverlay(dot, extra, n);
        }

        return g;
    }

    // buildSceneGroupNode renders a scene as a container box (item 2): a
    // header row (name + the same HomeKit/fault/unsaved/live-state markers
    // buildDeviceNode above uses) plus one sub-row per configured state, in
    // the idiom of buildDriverGroup below. scene_action edges leave from the
    // owning state's row (see resolveScenePortId/portFor) instead of the
    // scene's centre, so different states never draw identical, overlapping
    // curves - this is what structurally fixes P2 for scenes.
    function buildSceneGroupNode(item) {
        const n = item.node;
        // schema-node-device (not just schema-node-scene-group) is required
        // here (finding F1): applyLiveOverlay (Stage 3) walks the DOM for
        // '.schema-node-device' to find every node it should keep in sync on
        // each /api/state poll - a scene's state dot, "extra" label and
        // per-state active-row highlight are all wired through that same
        // class (see applyLiveOverlay below). It carries no CSS rule of its
        // own (checked style.css - it's a pure JS selector hook), so adding
        // it here changes no styling; without it a scene's live overlay was
        // never applied at all and froze at whatever seedStateOverlay
        // painted on page load.
        const g = svgEl('g', { class: 'schema-node schema-node-device schema-node-scene-group', 'data-node-id': n.id });

        g.appendChild(svgEl('rect', {
            x: item.x, y: item.y, width: item.w, height: item.h, rx: 8,
            class: 'schema-group-rect schema-scene-group-rect',
        }));

        const headerText = svgEl('text', { x: item.x + 14, y: item.y + GROUP_HEADER_H / 2 + 5, class: 'schema-node-label' });
        // SCENE_HEADER_LABEL_MAX_CHARS, not NODE_LABEL_MAX_CHARS (finding
        // F13) - see its own comment. The untruncated name is still always
        // available via the <title> child below, same as everywhere else.
        headerText.textContent = DEVICE_ICONS.scene + ' ' + truncateLabel(n.label || '', SCENE_HEADER_LABEL_MAX_CHARS);
        if (n.label) {
            const title = svgEl('title');
            title.textContent = n.label;
            headerText.appendChild(title);
        }
        g.appendChild(headerText);

        let markerX = item.x + item.w - 14;
        if (n.homekit) {
            const hk = svgEl('text', { x: markerX, y: item.y + 15, class: 'schema-node-marker', 'text-anchor': 'end' });
            hk.textContent = '\u{1F34E}';
            g.appendChild(hk);
            markerX -= 16;
        }
        if (n.faulty) {
            // Chained onto markerX rather than a fixed item.x+item.w-8
            // (finding F13): at that fixed position the fault dot sat only
            // ~2px from the live-state dot below (cx item.x+item.w-10) -
            // two same-radius circles close enough to visually merge.
            // Chaining after the HomeKit marker (when present), or the
            // header's own right margin (when not), keeps it clear of the
            // state dot regardless of which markers this scene has.
            g.appendChild(svgEl('circle', { cx: markerX - 4, cy: item.y + 8, r: 4, class: 'schema-fault-dot' }));
            markerX -= 14;
        }
        if (n.unsaved) {
            g.appendChild(svgEl('circle', { cx: item.x + 8, cy: item.y + item.h - 8, r: 4, class: 'schema-unsaved-dot' }));
        }

        // Live-state overlay hooks: same contract as buildDeviceNode's dot/
        // extra above - seeded here, then only ever updated in place by
        // updateDeviceOverlay, never recreated. Placed in the header band
        // (rather than the node's bottom-right corner, since a scene box's
        // total height varies with its state count).
        if (!S.editMode) {
            const dotCx = item.x + item.w - 10;
            const dotCy = item.y + GROUP_HEADER_H - 7;
            const dot = svgEl('circle', { cx: dotCx, cy: dotCy, r: 4.5, class: 'schema-state-dot' });
            const extra = svgEl('text', { x: dotCx - 10, y: dotCy + 3, class: 'schema-state-extra', 'text-anchor': 'end' });
            g.appendChild(dot);
            g.appendChild(extra);
            seedStateOverlay(dot, extra, n);
        }

        // ---- per-state sub-rows ----
        // activeIdx falls back to 0 whenever detail.state_index is absent -
        // which is exactly what buildEditGraph supplies for a scene (only
        // state_names, no state_index: the working copy has no notion of a
        // "current" state). Outside edit mode that fallback only shows
        // briefly (until the first /api/state poll seeds the real value);
        // in edit mode it never gets overtaken by a poll, so without the
        // !S.editMode guard below, state row 0 would render permanently -
        // and wrongly - "active" for the whole editing session (finding F9).
        // Same rule buildSceneGroupNode's dot/extra pair above already
        // follows.
        const activeIdx = (n.detail && n.detail.state_index) || 0;
        if (!item.states.length) {
            // A legacy 0-state scene: still show the reserved row's worth of
            // body (sceneBoxHeight in buildLayout), rather than an
            // unexplained blank box.
            const empty = svgEl('text', {
                x: item.x + GROUP_PAD + 6, y: item.y + GROUP_HEADER_H + GROUP_PAD + IO_PILL_H / 2 + 4,
                class: 'schema-scene-state-empty',
            });
            empty.textContent = 'no states configured';
            g.appendChild(empty);
        } else {
            item.states.forEach(st => {
                const active = !S.editMode && st.index === activeIdx;
                const rowTop = st.y - IO_PILL_H / 2;
                // data-state-index (not data-node-id: state rows are not
                // independently selectable/hoverable, they're decoration
                // inside the scene's own node) is how the live overlay
                // (updateDeviceOverlay) finds this row again on every poll to
                // toggle the active-state class in place - item 6.
                const rowG = svgEl('g', {
                    class: 'schema-scene-state-row' + (active ? ' schema-scene-state-active' : ''),
                    'data-state-index': st.index,
                });
                rowG.appendChild(svgEl('rect', {
                    x: item.x + GROUP_PAD, y: rowTop, width: item.w - GROUP_PAD * 2, height: IO_PILL_H, rx: 4,
                    class: 'schema-scene-state-rect',
                }));
                const label = svgEl('text', { x: item.x + GROUP_PAD + 6, y: st.y + 4, class: 'schema-scene-state-label' });
                const full = st.index + ' ' + st.name;
                label.textContent = truncateLabel(full, SCENE_STATE_LABEL_MAX_CHARS);
                const title = svgEl('title');
                title.textContent = full;
                label.appendChild(title);
                rowG.appendChild(label);
                g.appendChild(rowG);
            });
        }

        return g;
    }

    // seedStateOverlay sets the initial dot/extra-label appearance from the
    // schema node's own snapshot fields (is_on / detail.*, as built by
    // schema.go's buildDeviceDetail). This is what a freshly opened tab shows
    // before the first live /api/state poll arrives; applyLiveOverlay takes
    // over from there and only ever updates these same two elements in place.
    function seedStateOverlay(dot, extra, n) {
        const detail = n.detail || {};
        if (n.device_type === 'scene') {
            const idx = detail.state_index || 0;
            const names = detail.state_names || [];
            setDotState(dot, extra, idx > 0 ? 'on' : 'off', idx > 0 ? (names[idx] || '') : '');
        } else if (n.device_type === 'button') {
            // Buttons have no persisted on/off state to seed; the event
            // flash is purely poll-driven (last_event_time isn't on the
            // schema node), so start neutral.
            setDotState(dot, extra, 'off', '');
        } else if (n.device_type === 'dimmable_light') {
            setDotState(dot, extra, n.is_on ? 'on' : 'off', n.is_on ? ((detail.brightness || 0) + '%') : '');
        } else {
            setDotState(dot, extra, n.is_on ? 'on' : 'off', '');
        }
    }

    // setDotState applies a dot class ('on'/'off'/'event-flash') and the
    // extra label's text in one place, shared by the seed path above and the
    // live-overlay update path below - both only ever touch these two
    // elements' class/text, never recreate them.
    function setDotState(dot, extra, dotClass, extraText) {
        dot.setAttribute('class', 'schema-state-dot' + (dotClass ? ' ' + dotClass : ''));
        if (!extra) return;
        if (extraText) {
            extra.textContent = extraText;
            extra.classList.add('visible');
        } else {
            extra.textContent = '';
            extra.classList.remove('visible');
        }
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
            // Finding 5: a driver split across two boxes (one per side) used
            // to show the same driver-wide io_total on both, reading as if
            // there were twice as many IOs as actually configured. A
            // dual-side box shows only what's wired into *this* box instead;
            // io_total (the driver-wide count, independent of what's wired)
            // stays reserved for the single-box case.
            count.textContent = group.dual
                ? group.pills.length + ' wired'
                : (group.driverNode && group.driverNode.io_total != null ? group.driverNode.io_total : group.pills.length) + ' IOs';
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

    // Node ids are bare by construction (finding F2): every graph node
    // (button, device, scene, driver, io) is keyed by its own real id, and
    // S.data's edges always carry bare e.from/e.to - schema.go and
    // buildEditGraph never emit a "#"-suffixed id; that suffix only ever
    // appears in the DOM, written by renderDiagram's edge loop above as the
    // *resolved* geometry port for a scene's per-state sub-row (dataset.
    // from/fromNode). So adjacency here never needs to parse or fold
    // anything: it compares nodeId (from a clicked/hovered element's bare
    // data-node-id) against dataset.fromNode/dataset.to (also always bare)
    // directly. A prior version instead folded at the first '#' to recover
    // the owning node id from an edge's *resolved* dataset.from - which
    // broke on any node whose real name legitimately contains a '#' (again,
    // nothing server-side forbids it), truncating e.g. "Lamp#1" to "Lamp"
    // and silently failing to dim/highlight anything connected to it.
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
            const from = el.dataset.fromNode;
            const to = el.dataset.to;
            const touches = adj.has(from) && adj.has(to) && (from === nodeId || to === nodeId);
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

    // ---- Detail dock fact-group helpers ----
    // Small string builders shared by renderDetailPanel below. Kept as plain
    // string concatenation (matching the pre-existing style) - all
    // user-derived values still go through escHtml exactly as before; only
    // the grouping/layout changed (round-2 item 2: docked, horizontal fact
    // groups instead of a tall vertical list).

    function factRow(label, valueHtml, opts) {
        opts = opts || {};
        if (valueHtml === undefined) {
            return '<div class="row"><dt class="text-error">' + label + '</dt></div>';
        }
        const cls = opts.cls ? ' class="' + opts.cls + '"' : '';
        const live = opts.live ? ' data-live="' + opts.live + '"' : '';
        return '<div class="row"><dt>' + label + ':</dt><dd' + cls + live + '>' + valueHtml + '</dd></div>';
    }

    function factGroup(title, bodyHtml) {
        if (!bodyHtml) return '';
        return '<div class="schema-dock-group"><div class="schema-panel-section">' + title + '</div>' + bodyHtml + '</div>';
    }

    function ioDetailLabel(key) {
        switch (key) {
            case 'output_io_id': return 'Output';
            case 'rgbw_io_id': return 'RGBW';
            case 'analog_io_id': return 'Analog';
            case 'event_input_id': return 'Event input';
            default: return key;
        }
    }

    const IO_DETAIL_KEYS = ['output_io_id', 'rgbw_io_id', 'analog_io_id', 'event_input_id'];

    // relTimeAgo/formatEventFact/formatSceneFact format the same "State"
    // facts both at initial render (from the /api/schema node snapshot, via
    // node.detail) and on every live poll thereafter (from /api/state, via
    // updateDetailDockLive) - kept as pure functions so both call sites stay
    // in sync without duplicating the formatting logic.
    function relTimeAgo(ms) {
        if (ms < 1500) return 'just now';
        if (ms < 60000) return Math.round(ms / 1000) + 's ago';
        if (ms < 3600000) return Math.round(ms / 60000) + 'm ago';
        return Math.round(ms / 3600000) + 'h ago';
    }

    function formatEventFact(eventType, eventTime, now) {
        if (!eventType) return '—';
        if (!eventTime) return eventType;
        const t = (eventTime instanceof Date) ? eventTime.getTime() : new Date(eventTime).getTime();
        if (isNaN(t)) return eventType;
        return eventType + ' (' + relTimeAgo(Math.max(0, now - t)) + ')';
    }

    function formatSceneFact(stateIndex, stateNames) {
        if (stateIndex > 0 && stateNames && stateNames[stateIndex]) return stateNames[stateIndex];
        return 'off';
    }

    // sceneStateListItemText/buildSceneStateNamesList (finding 9): the
    // configured state names for a scene used to be visible nowhere
    // read-only once round-2's docked panel replaced the old vertical list -
    // only the single active-state name showed. Lists all configured states
    // with the active one marked, alongside the existing "Active state" row;
    // shared by the initial render (here) and the live-poll refresh
    // (updateDetailDockLive) via the 'scene_state_list' data-live hook so it
    // stays correct if the active state changes while the dock is open.
    function sceneStateListItemText(name, idx, activeIdx) {
        return name + (idx === activeIdx ? ' ✓' : '');
    }

    function buildSceneStateNamesList(stateNames, activeIdx) {
        const names = stateNames || [];
        if (!names.length) return '';
        let list = '<ul class="schema-panel-list" data-live="scene_state_list">';
        names.forEach(function(name, idx) {
            const cls = idx === activeIdx ? ' class="text-success"' : '';
            list += '<li' + cls + '>' + escHtml(sceneStateListItemText(name, idx, activeIdx)) + '</li>';
        });
        list += '</ul>';
        return list;
    }

    // closeDock/closeModal (finding 2): the detail dock and the edit modal
    // must be mutually exclusive - at most one of them ever carries the
    // 'open' class at a time. renderActivePanel calls whichever of these
    // corresponds to the panel it is NOT about to (re)render, before
    // rendering the other, so a stale dock/modal from a previous selection
    // can never linger alongside (or block the close button of) the one the
    // user is now interacting with. Both are also safe/cheap to call when
    // already closed.
    function closeDock() {
        const panel = document.getElementById('schema-detail-dock');
        if (!panel) return;
        panel.classList.remove('open');
        panel.innerHTML = '';
    }

    function closeModal() {
        const backdrop = document.getElementById('schema-edit-backdrop');
        const panel = document.getElementById('schema-edit-modal');
        if (backdrop) backdrop.classList.remove('open');
        if (panel) panel.textContent = '';
    }

    function renderDetailPanel() {
        const panel = document.getElementById('schema-detail-dock');
        if (!panel) return;
        if (!S.selectedId || !S.data) {
            closeDock();
            return;
        }
        const node = S.data.nodes.find(n => n.id === S.selectedId);
        if (!node) {
            closeDock();
            return;
        }

        const icon = DEVICE_ICONS[node.device_type] || (node.kind === 'driver' ? '⚡' : '\u{1F50C}');
        let html = '<div class="schema-dock-header">';
        html += '<button class="schema-panel-close" aria-label="Close">✕</button>';
        html += '<div class="schema-panel-title">' + icon + ' ' + escHtml(node.label || node.id) + '</div>';
        html += '</div><div class="schema-dock-grid">';

        if (node.kind === 'driver') {
            let identity = factRow('Kind', 'Driver');
            identity += factRow('Ready', (node.ready ? 'yes' : 'no') + (node.missing ? ' (referenced, not configured)' : ''));
            if (node.io_total != null) identity += factRow('IO points', node.io_total);
            html += factGroup('Identity', identity);

            const ios = S.data.nodes.filter(n => n.kind === 'io' && n.driver === node.label);
            if (ios.length) {
                let list = '<ul class="schema-panel-list">';
                ios.forEach(io => { list += '<li class="mono">' + escHtml((io.io_type || '?') + ' ' + io.label) + '</li>'; });
                list += '</ul>';
                html += factGroup('IO points', list);
            }
        } else if (node.kind === 'io') {
            let identity = factRow('Driver', escHtml(node.driver || '-'));
            identity += factRow('Type', escHtml(node.io_type || '-'));
            if (node.custom_name) identity += factRow('Name', escHtml(node.custom_name));
            if (node.invalid) identity += factRow('Invalid io id');
            html += factGroup('Identity', identity);

            let stateRows = factRow('State', '—', { live: 'io_state' });
            html += factGroup('State', stateRows);

            const users = (S.data.edges || []).filter(e => e.kind === 'io' && e.to === node.id);
            if (users.length) {
                let list = '<ul class="schema-panel-list">';
                users.forEach(e => {
                    const from = S.data.nodes.find(n => n.id === e.from);
                    list += '<li>' + escHtml(from ? from.label : e.from) + ' <span class="text-muted">(' + escHtml(e.role) + ')</span></li>';
                });
                list += '</ul>';
                html += factGroup('Used by', list);
            }
        } else {
            // device (incl. missing placeholder)
            let identity = factRow('Type', escHtml(node.device_type));
            if (node.missing) {
                identity += '<div class="row"><dt class="text-error">Not configured</dt><dd>referenced by name only</dd></div>';
            } else {
                identity += factRow('HomeKit', node.homekit ? 'enabled' : 'disabled');
                identity += factRow('Healthy', node.healthy ? 'yes' : 'no');
                if (node.faulty) identity += factRow('Faulty');
            }
            html += factGroup('Identity', identity);

            const detail = node.detail || {};
            let ioRows = '';
            IO_DETAIL_KEYS.forEach(function(k) {
                if (detail[k]) ioRows += factRow(ioDetailLabel(k), '<span class="mono">' + escHtml(String(detail[k])) + '</span>');
            });
            html += factGroup('IO bindings', ioRows);

            if (!node.missing) {
                let stateRows = '';
                if (node.device_type === 'button') {
                    stateRows += factRow('Last event', escHtml(formatEventFact(detail.last_event_type, detail.last_event_time, Date.now())), { live: 'last_event' });
                } else if (node.device_type === 'scene') {
                    const activeIdx = detail.state_index || 0;
                    stateRows += factRow('Active state', escHtml(formatSceneFact(activeIdx, detail.state_names)), { live: 'scene_state' });
                    stateRows += buildSceneStateNamesList(detail.state_names, activeIdx);
                } else {
                    stateRows += factRow('State', node.is_on ? 'on' : 'off', { live: 'is_on' });
                    if (node.device_type === 'dimmable_light') {
                        stateRows += factRow('Brightness', (detail.brightness || 0) + '%', { live: 'brightness' });
                    }
                }
                html += factGroup('State', stateRows);
            }

            let relHtml = '';
            const incoming = (S.data.edges || []).filter(e => (e.kind === 'control' || e.kind === 'scene_action') && e.to === node.id);
            if (incoming.length) {
                relHtml += '<div class="schema-dock-subhead">Driven by</div><ul class="schema-panel-list">';
                incoming.forEach(e => {
                    const from = S.data.nodes.find(n => n.id === e.from);
                    const via = e.kind === 'control' ? (e.event + ' → ' + e.action) : (e.state + ': ' + e.action);
                    relHtml += '<li>' + escHtml(from ? from.label : e.from) + ' <span class="text-muted">(' + escHtml(via) + (e.level ? ' ' + e.level : '') + ')</span></li>';
                });
                relHtml += '</ul>';
            }
            const outgoing = (S.data.edges || []).filter(e => (e.kind === 'control' || e.kind === 'scene_action') && e.from === node.id);
            if (outgoing.length) {
                relHtml += '<div class="schema-dock-subhead">Controls</div><ul class="schema-panel-list">';
                outgoing.forEach(e => {
                    const to = S.data.nodes.find(n => n.id === e.to);
                    const via = e.kind === 'control' ? (e.event + ' → ' + e.action) : (e.state + ': ' + e.action);
                    relHtml += '<li>' + escHtml(to ? to.label : e.to) + ' <span class="text-muted">(' + escHtml(via) + (e.level ? ' ' + e.level : '') + ')</span></li>';
                });
                relHtml += '</ul>';
            }
            html += factGroup('Relations', relHtml);
        }

        html += '</div>'; // .schema-dock-grid
        panel.innerHTML = html;
        panel.classList.add('open');
        const closeBtn = panel.querySelector('.schema-panel-close');
        if (closeBtn) closeBtn.addEventListener('click', function() { selectNode(null); });
        // Item 2: bring the dock into view when the clicked node is
        // off-screen (e.g. a tall diagram scrolled down); 'nearest' avoids
        // yanking the viewport when the dock is already fully visible.
        if (typeof panel.scrollIntoView === 'function') panel.scrollIntoView({ block: 'nearest' });
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
    // and the stage 2 edit form otherwise. Finding 2: the dock and the modal
    // must be mutually exclusive, so whichever one this call is NOT about to
    // render is explicitly closed first - this is what makes the dock's own
    // close button work while in edit mode (it routes through selectNode(null)
    // -> here -> the renderEditPanel branch below, which used to leave the
    // dock's stale 'open' markup untouched) and stops a driver/io dock left
    // open from a click before entering edit mode, or before switching to a
    // device selection within edit mode, from lingering alongside the modal.
    function renderActivePanel() {
        if (!S.editMode) {
            closeModal();
            renderDetailPanel();
            return;
        }
        if (!S.selectedEdit && S.selectedId && S.data) {
            const node = S.data.nodes.find(function(n) { return n.id === S.selectedId; });
            if (node && (node.kind === 'driver' || node.kind === 'io')) {
                closeModal();
                renderDetailPanel();
                return;
            }
        }
        closeDock();
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

    // renderErrorBanner/clearErrorBanner (finding 1): save-validation errors
    // must be visible independent of the edit modal's open/closed state - the
    // modal backdrop covers the toolbar, so Save is only reachable with the
    // modal already closed, and a rejected save must not go silent just
    // because renderEditPanel() (called from the same failure path, for the
    // case the modal happens to be open) tears down the modal's own content
    // when S.selectedEdit is null. Lives in the card's normal document flow,
    // directly under the toolbar - never covered by the backdrop (z-index 70)
    // and unaffected by whether the modal is open or closed. Built via
    // createElement/textContent only, same as buildErrorsBlock, since save
    // error strings can embed user-controlled text (device/io names).
    function renderErrorBanner(errors) {
        const banner = document.getElementById('schema-error-banner');
        if (!banner) return;
        if (!errors || !errors.length) {
            clearErrorBanner();
            return;
        }
        banner.textContent = '';
        const closeBtn = mkEl('button', { class: 'schema-panel-close', 'aria-label': 'Dismiss' }, '✕');
        closeBtn.addEventListener('click', clearErrorBanner);
        banner.appendChild(closeBtn);
        const count = errors.length;
        banner.appendChild(mkEl('div', { class: 'schema-panel-section text-error' },
            'Save failed — ' + count + ' error' + (count === 1 ? '' : 's')));
        const ul = mkEl('ul', { class: 'schema-panel-list' });
        errors.forEach(function(e) { ul.appendChild(mkEl('li', { class: 'text-error' }, e)); });
        banner.appendChild(ul);
        banner.style.display = '';
    }

    function clearErrorBanner() {
        const banner = document.getElementById('schema-error-banner');
        if (!banner) return;
        banner.style.display = 'none';
        banner.textContent = '';
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
        html += '<span class="schema-legend-item">Columns: Inputs · Buttons · Scenes · Devices · Outputs</span>';
        html += '<span class="schema-legend-item">Scene edges leave from the state row that fires them</span>';
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
            '<div id="schema-error-banner" class="schema-error-banner" style="display:none"></div>' +
            '<div class="schema-diagram-wrap"><div id="schema-diagram"></div></div>' +
            '<div id="schema-empty" class="empty-state" style="display:none">No devices configured</div>' +
            '<div id="schema-detail-dock" class="schema-detail-dock"></div>' +
            '<div id="schema-edit-backdrop" class="schema-modal-backdrop">' +
                '<div id="schema-edit-modal" class="schema-modal" role="dialog" aria-modal="true" aria-label="Edit device"></div>' +
            '</div>';

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

        // Item 1: clicking the dimmed backdrop (but not the dialog itself)
        // closes the edit modal with the same semantics as the close
        // button/Esc - selectNode(null), which only clears the selection;
        // any focused field's blur-commit has already run (blur fires before
        // this click, since it fires on mousedown) so nothing is lost.
        //
        // Finding 10: ev.target === backdrop alone is not enough - the click
        // event's target is computed from the mousedown/mouseup pair (the
        // nearest common ancestor of the two, per the UI Events spec, since
        // mousedown fires on whatever was pressed and mouseup on whatever the
        // pointer is over on release), not from where the press originated.
        // So starting a text-selection drag inside the modal (mousedown on
        // an input/label inside .schema-modal) and releasing over the
        // backdrop (mouseup outside the dialog) yields a click whose target
        // resolves to the backdrop - indistinguishable, by target alone, from
        // an actual click on the empty backdrop - and would wrongly discard
        // the in-progress edit. Track where the mousedown itself landed and
        // only close when *that* was the backdrop too.
        const backdrop = document.getElementById('schema-edit-backdrop');
        if (backdrop) {
            let mousedownOnBackdrop = false;
            backdrop.addEventListener('mousedown', function(ev) {
                mousedownOnBackdrop = (ev.target === backdrop);
            });
            backdrop.addEventListener('click', function(ev) {
                if (ev.target === backdrop && mousedownOnBackdrop) selectNode(null);
            });
        }
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
            clearErrorBanner();
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
        // Clear the stale selection too: leaving edit mode with a device
        // still "selected" (e.g. via the modal's own close button, which only
        // clears S.selectedEdit through selectNode) would otherwise make the
        // read-only detail dock spontaneously reopen on that same node the
        // instant renderActivePanel next runs, even though the user never
        // clicked anything in read-only mode.
        S.selectedId = null;
        S.saveErrors = null;
        clearErrorBanner();
        S.dirty = false;
        // Finding 3: an edit session (of any length) is exactly the kind of
        // overlay gap applyLiveOverlay must not mistake for real, simultaneous
        // device changes - force the next tick to re-seed silently rather
        // than glow, regardless of how long the session lasted.
        S.pendingReseed = true;
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
        clearErrorBanner();
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
    // Used for structural, click-driven commits (add/remove relation rows,
    // add/remove states, delete device, checkbox/select commits) where the
    // panel genuinely needs to be rebuilt and there is no in-flight blur to
    // race against. The guard defends against exitEditMode()/discardSilently()
    // having torn down edit mode (nulled S.working) by the time this runs.
    function redrawEdit() {
        if (!S.editMode || !S.working) return;
        S.data = buildEditGraph(S.working, S.editData && S.editData.meta, S.colorLightNodes);
        redraw();
    }

    // redrawEditDiagramOnly rebuilds S.data from the working copy (so
    // subsequent clicks/hover see committed values) and re-renders only the
    // SVG diagram - it deliberately leaves the edit panel untouched. See
    // scheduleRedrawEdit() below for why this matters for blur-triggered
    // commits.
    function redrawEditDiagramOnly() {
        if (!S.editMode || !S.working) return;
        S.data = buildEditGraph(S.working, S.editData && S.editData.meta, S.colorLightNodes);
        const diagramEl = document.getElementById('schema-diagram');
        const emptyEl = document.getElementById('schema-empty');
        if (!diagramEl || !S.data) return;
        const deviceCount = (S.data.nodes || []).filter(function(n) { return n.kind === 'device'; }).length;
        if (deviceCount === 0) {
            diagramEl.style.display = 'none';
            if (emptyEl) emptyEl.style.display = '';
            return;
        }
        diagramEl.style.display = '';
        if (emptyEl) emptyEl.style.display = 'none';
        const layout = buildLayout(S.data, S.showIo);
        renderDiagram(diagramEl, S.data, layout);
    }

    // scheduleRedrawEdit defers a diagram-only redraw to a macrotask
    // (setTimeout 0) for blur-triggered field commits (name, IO fields,
    // DefaultSetpoint, relation Level, scene state name, scene actions
    // textarea). Two things make this tricky:
    //
    //   1. blur fires *before* the click that caused it (e.g. clicking a
    //      different field, a delete/add button, or another node) is
    //      dispatched, and in fact fires during mousedown - well before the
    //      matching mouseup/click - so even a same-tick synchronous redraw
    //      does not reliably outlast the physical click that is still in
    //      flight.
    //   2. by the time the deferred callback runs, the user may have left
    //      edit mode entirely (exitEditMode()/discardSilently() null out
    //      S.working), so the callback must re-check before touching
    //      anything - hence the guard below (also duplicated defensively
    //      inside redrawEditDiagramOnly itself).
    //
    // Crucially, the deferred work here is diagram-only, NOT a full
    // redrawEdit(). Rebuilding the edit panel would replace
    // #schema-edit-modal's entire subtree (renderEditPanel() does
    // `panel.textContent = ''` then rebuilds from scratch), detaching
    // whatever the in-flight click is targeting before it is dispatched -
    // reintroducing the two-click bug - and, if the click landed on a
    // different field, destroying the very input the user just focused. The
    // panel's own inputs already display the value that was just committed
    // (the user typed it), so nothing there needs rebuilding; only the
    // diagram (labels, colors, edges) can be stale. Do NOT change this back
    // to a full redrawEdit() without re-solving that DOM-detachment problem.
    let redrawEditScheduled = false;
    function scheduleRedrawEdit() {
        if (redrawEditScheduled) return;
        redrawEditScheduled = true;
        setTimeout(function() {
            redrawEditScheduled = false;
            if (!S.editMode || !S.working) return;
            redrawEditDiagramOnly();
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

    // uniqueStateName mirrors uniquePlaceholderName's idiom (probe, bump,
    // retry) but scoped to one scene's own States list instead of the
    // cross-kind device-name set - state names only need to be unique within
    // their own scene (validateEditableConfig's seenStates check, mirrored
    // client-side by buildSceneStateNameField). Finding F4: deriving the
    // candidate purely from states.length (as this used to) breaks the
    // instant a state is removed from the middle/end of the list, e.g.
    // [off, on] -> +Add -> [off, on, state3] -> remove "on" -> [off, state3]
    // -> +Add derives 'state' + (2+1) = 'state3' again, colliding with the
    // state3 still present and failing to save server-side with "duplicate
    // state name".
    function uniqueStateName(states) {
        const names = new Set((states || []).map(function(s) { return s.Name; }));
        let n = states.length + 1;
        let candidate = 'state' + n;
        while (names.has(candidate)) {
            n++;
            candidate = 'state' + n;
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
                // Seed off/on states rather than an empty list: a scene with
                // zero states is completely inert at runtime (Toggle() no-ops
                // with len(states)==0, SetValue(true) no-ops with fewer than
                // 2 - see scene.go), and server-side validation never flags
                // that as an error, so a freshly added scene would otherwise
                // silently do nothing until the user happened to notice.
                entry = withEditId({ Name: name, States: [{ Name: 'off', Actions: [] }, { Name: 'on', Actions: [] }] });
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
            // Item 5: dedup on the same key the server uses (schema.go's
            // buildSchemaGraph, ~line 219: fromID|toID|state|verb|level) so
            // the edit-mode diagram never fans/overlaps an edge the
            // server-side view never would (e.g. a duplicated hand-edited
            // action string within one state). Scoped per-scene rather than
            // one map across the whole loop, like the server's single `seen`
            // - since fromId is always this scene, a shared map could never
            // dedup across different scenes anyway.
            const seen = new Set();
            (s.States || []).forEach(function(st) {
                (st.Actions || []).forEach(function(actionStr) {
                    const parsed = parseActionString(actionStr);
                    if (!parsed) return;
                    const toId = resolveTarget(parsed.device);
                    const level = parsed.level || 0;
                    const key = fromId + '|' + toId + '|' + st.Name + '|' + parsed.verb + '|' + level;
                    if (seen.has(key)) return;
                    seen.add(key);
                    edges.push({ kind: 'scene_action', from: fromId, to: toId, state: st.Name, action: parsed.verb, level: level });
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
            const raw = parseInt(input.value, 10);
            const clamped = isNaN(raw) ? initial : Math.max(min, Math.min(max, raw));
            if (clamped === initial) {
                input.value = initial; // invalid/out-of-range/no-op: revert the displayed value, no markDirty/no redraw
                return;
            }
            input.value = clamped;
            onCommit(clamped);
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
    //
    // Finding F8: a ':'-containing name can only reach the working copy via
    // a hand-edited config.json (buildNameField already refuses ':' on
    // rename - see its inline validator), but nothing stops such a name from
    // being *offered* here and picked, producing an action string like
    // "on:Strip:1" that parseActionString then rejects as malformed (3 parts
    // for an on/off/toggle verb) - degrading the row the user just built
    // with the picker straight into the raw-text error row (see F7). An
    // already-selected such value still displays via selectFieldEl's
    // "(missing)" fallback path (checked: options.indexOf(value) === -1
    // once filtered out here, so hasValue && missing both go true), so
    // filtering it out of the offered list loses no information, just stops
    // it from being freshly chosen. Filtering here also keeps the client
    // self-consistent with buildNameField's own rule: ':' delimits the
    // action-string grammar, so no target name may contain one.
    function targetNameOptions() {
        const set = new Set();
        ['Lights', 'DimmableLights', 'Outlets', 'Scenes'].forEach(function(key) {
            (S.working[key] || []).forEach(function(e) { set.add(e.Name); });
        });
        (S.colorLightNodes || []).forEach(function(n) { set.add(n.label); });
        return Array.from(set).filter(function(name) { return name.indexOf(':') === -1; }).sort();
    }

    // sceneTargetNameOptions restricts targetNameOptions() to the targets a
    // scene action on `scene` may legally reference. SwKit.Setup builds
    // scenes sequentially and resolves each action's target via
    // resolveControllable, which only ever sees scenes already appended - so
    // an action may target an earlier scene, never itself or a later one
    // (mirrors the sceneIndex check in config_edit.go's validateEditableConfig).
    // Non-scene targets (lights, dimmable lights, outlets, color lights) carry
    // no such ordering constraint. Button control relations have no ordering
    // constraint at all (buttons aren't built in dependency order relative to
    // their targets), so they keep the unfiltered targetNameOptions() list.
    function sceneTargetNameOptions(scene) {
        const idx = S.working.Scenes.indexOf(scene);
        return targetNameOptions().filter(function(name) {
            const sceneIdx = S.working.Scenes.findIndex(function(s) { return s.Name === name; });
            return sceneIdx === -1 || sceneIdx < idx;
        });
    }

    // verbOptionsForTarget filters the verb vocabulary down to the
    // non-brightness subset (on/off/toggle) unless `name` resolves to a
    // dimmable light - mirrors dimmableTargetTypes in config_edit.go
    // (DimmableLight is the only runtime type that implements app.Dimmable;
    // ColorLight and Scene do not implement SetBrightness). Used for both
    // button control-relation rows and scene action rows, since the server
    // enforces the same rule for both (see validateEditableConfig's two call
    // sites of dimmableTargetTypes).
    function verbOptionsForTarget(name) {
        const meta = (S.editData && S.editData.meta) || {};
        const allVerbs = meta.action_verbs || ['on', 'off', 'toggle', 'brightness', 'brightness_up', 'brightness_down'];
        const isDimmable = (S.working.DimmableLights || []).some(function(d) { return d.Name === name; });
        if (isDimmable) return allVerbs;
        return allVerbs.filter(function(v) { return !isBrightnessVerb(v); });
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
        const relationTargets = targetNameOptions();
        if (!relationTargets.length) {
            // Finding F11: mirrors the scene "+ Add action" guard just below
            // in buildSceneStateRow - without it, staging DeviceName: ''
            // fails validation only on save, with a confusing "target
            // device "" does not exist" error, instead of being caught here
            // where the user can see why the button is disabled.
            addBtn.disabled = true;
            addBtn.title = 'No valid target exists yet - add a light, outlet, dimmable light, or scene first';
        } else {
            addBtn.addEventListener('click', function() {
                const targets = targetNameOptions();
                entry.ControlDevices.push({ EventType: 'single_press', Action: 'toggle', Level: 0, DeviceName: targets[0] || '' });
                markDirty();
                redrawEdit();
            });
        }
        panel.appendChild(addBtn);
    }

    function buildControlRelationRow(button, cd, idx) {
        const meta = (S.editData && S.editData.meta) || {};
        const eventTypes = meta.event_types || ['single_press', 'double_press', 'triple_press', 'long_press'];

        const row = mkEl('div', { class: 'edit-relation-row' });
        row.appendChild(selectFieldEl(eventTypes, cd.EventType, function(v) { cd.EventType = v; markDirty(); redrawEdit(); }));
        row.appendChild(selectFieldEl(verbOptionsForTarget(cd.DeviceName), cd.Action, function(v) { cd.Action = v; markDirty(); redrawEdit(); }));
        if (isBrightnessVerb(cd.Action)) {
            row.appendChild(numberInputEl(cd.Level, 0, 100, function(v) { cd.Level = Math.max(0, Math.min(100, v)); markDirty(); scheduleRedrawEdit(); }));
        }
        row.appendChild(selectFieldEl(targetNameOptions(), cd.DeviceName, function(v) {
            cd.DeviceName = v;
            // Item 4: switching the target can make the currently-selected
            // verb illegal (brightness requires a dimmable target) - coerce
            // to toggle rather than leave an invalid verb/target combination
            // staged in the working copy.
            if (isBrightnessVerb(cd.Action) && verbOptionsForTarget(v).indexOf(cd.Action) === -1) {
                cd.Action = 'toggle';
            }
            markDirty();
            redrawEdit();
        }));
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
        if (entry.States.length < 2) {
            // A scene with fewer than 2 states can never be toggled to
            // anything: Toggle() no-ops with zero states and SetValue(true)
            // no-ops with fewer than two (scene.go) - state index 0 is the
            // "off" state by convention, so a usable scene needs at least one
            // more state beyond it. Server-side validation doesn't reject
            // this (it's a usability trap, not a data error), so flag it here.
            panel.appendChild(mkEl('div', { class: 'text-muted' },
                'A scene needs at least two states to be toggleable. State 0 is the "off" state by convention.'));
        }
        const list = mkEl('div', { class: 'edit-state-list' });
        entry.States.forEach(function(st, idx) {
            list.appendChild(buildSceneStateRow(entry, st, idx));
        });
        panel.appendChild(list);

        const addBtn = mkEl('button', { class: 'io-filter-btn' }, '+ Add state');
        addBtn.addEventListener('click', function() {
            entry.States.push({ Name: uniqueStateName(entry.States), Actions: [] });
            markDirty();
            redrawEdit();
        });
        panel.appendChild(addBtn);
    }

    // buildSceneStateNameField mirrors buildNameField's inline-error pattern
    // (no markDirty/redraw on an invalid edit, offending text stays visible)
    // but validates against the server's actual state-name rules (empty, or
    // duplicating another state's name within the same scene - see
    // validateEditableConfig's seenStates check), instead of buildNameField's
    // device-name rules (empty, or containing ':').
    function buildSceneStateNameField(scene, st) {
        const wrap = mkEl('div', { class: 'edit-field' });
        wrap.appendChild(mkEl('label', { class: 'edit-label' }, 'State name'));
        const initial = st.Name || '';
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
                errEl.textContent = 'State name must not be empty';
                errEl.style.display = '';
                return;
            }
            const dup = (scene.States || []).some(function(other) { return other !== st && other.Name === trimmed; });
            if (dup) {
                errEl.textContent = 'Another state in this scene is already named "' + trimmed + '"';
                errEl.style.display = '';
                return;
            }
            errEl.style.display = 'none';
            st.Name = trimmed;
            markDirty();
            scheduleRedrawEdit();
        });
        input.addEventListener('keydown', function(e) { if (e.key === 'Enter') input.blur(); });

        wrap.appendChild(input);
        wrap.appendChild(errEl);
        return wrap;
    }

    function buildSceneStateRow(scene, st, idx) {
        const wrap = mkEl('div', { class: 'edit-state-row' });
        wrap.appendChild(buildSceneStateNameField(scene, st));

        const upBtn = mkEl('button', { class: 'io-filter-btn' }, '↑ Move up');
        upBtn.disabled = idx === 0;
        upBtn.addEventListener('click', function() {
            if (idx === 0) return;
            // State order is semantic (index 0 = off, Toggle cycles states in
            // array order) - this is a click-driven structural commit, so a
            // full redrawEdit() (not scheduleRedrawEdit) is correct here.
            const arr = scene.States;
            const tmp = arr[idx - 1]; arr[idx - 1] = arr[idx]; arr[idx] = tmp;
            markDirty();
            redrawEdit();
        });
        wrap.appendChild(upBtn);

        const downBtn = mkEl('button', { class: 'io-filter-btn' }, '↓ Move down');
        downBtn.disabled = idx === scene.States.length - 1;
        downBtn.addEventListener('click', function() {
            if (idx === scene.States.length - 1) return;
            const arr = scene.States;
            const tmp = arr[idx + 1]; arr[idx + 1] = arr[idx]; arr[idx] = tmp;
            markDirty();
            redrawEdit();
        });
        wrap.appendChild(downBtn);

        wrap.appendChild(mkEl('div', { class: 'schema-panel-section' }, 'Actions'));
        st.Actions = st.Actions || [];
        const actionList = mkEl('div', { class: 'edit-relation-list' });
        st.Actions.forEach(function(actionStr, actionIdx) {
            actionList.appendChild(buildSceneActionRow(scene, st, actionIdx));
        });
        wrap.appendChild(actionList);

        const addActionBtn = mkEl('button', { class: 'io-filter-btn' }, '+ Add action');
        const defaultTargets = sceneTargetNameOptions(scene);
        if (!defaultTargets.length) {
            // No legal target exists yet (no lights/dimmable lights/outlets/
            // color lights configured, and no earlier scene to reference) -
            // disable instead of staging an "on:" action with an empty
            // device, which would just fail validation on save with a
            // confusing "target device "" does not exist" error.
            addActionBtn.disabled = true;
            addActionBtn.title = 'No valid target exists yet - add a light, outlet, dimmable light, or an earlier scene first';
        } else {
            addActionBtn.addEventListener('click', function() {
                st.Actions.push(formatActionString({ verb: 'on', level: 0, device: defaultTargets[0] }));
                markDirty();
                redrawEdit();
            });
        }
        wrap.appendChild(addActionBtn);

        const rmBtn = mkEl('button', { class: 'io-filter-btn schema-row-remove' }, '✕ Remove state');
        rmBtn.addEventListener('click', function() {
            scene.States.splice(idx, 1);
            markDirty();
            redrawEdit();
        });
        wrap.appendChild(rmBtn);
        return wrap;
    }

    // buildSceneActionRow renders one scene-state action string as a
    // structured verb/level/target picker (mirroring buildControlRelationRow),
    // when the string parses via parseActionString. When it doesn't - a
    // hand-edited or legacy action string the grammar can't represent - it
    // falls back to a raw text input pre-filled with the original string plus
    // an inline error, and leaves that string completely untouched until the
    // user edits it: the picker must never silently drop or rewrite an
    // unparseable action.
    function buildSceneActionRow(scene, st, idx) {
        const row = mkEl('div', { class: 'edit-relation-row' });

        // buildRowContent renders the verb/level/target pickers, or the raw-
        // fallback input+error, for whatever action string idx *currently*
        // holds. Shared by the row's initial build below and by
        // replaceContent (finding F7): this row's whole structure - raw
        // input+error vs. structured pickers - is a function of the
        // committed value, unlike every other blur commit in this file
        // (where the panel's own inputs already display the value that was
        // just committed, and only the diagram needs refreshing - see
        // scheduleRedrawEdit's doc comment). Without re-running this after a
        // repair, the row stayed stuck showing the raw box and a stale
        // "Not a recognised action" error even once the string was fixed.
        function buildRowContent() {
            const raw = st.Actions[idx];
            const parsed = parseActionString(raw);
            const nodes = [];
            if (!parsed) {
                const wrap = mkEl('div', { class: 'edit-field' });
                const input = mkEl('input', { type: 'text', class: 'edit-input mono' });
                input.value = raw;
                input.addEventListener('blur', function() {
                    if (input.value === raw) return; // unchanged: no-op
                    st.Actions[idx] = input.value;
                    markDirty();
                    // Replace only this row's own content in place - do NOT
                    // call the full redrawEdit() from here; see
                    // scheduleRedrawEdit's doc comment for why rebuilding the
                    // whole edit panel from a blur handler detaches whatever
                    // click is still in flight (blur fires on mousedown,
                    // before the matching click is dispatched).
                    replaceContent();
                    scheduleRedrawEdit();
                });
                input.addEventListener('keydown', function(e) { if (e.key === 'Enter') input.blur(); });
                wrap.appendChild(input);
                wrap.appendChild(mkEl('div', { class: 'edit-field-error' },
                    'Not a recognised action (expected on/off/toggle:<device> or brightness[_up|_down]:<level>:<device>)'));
                nodes.push(wrap);
            } else {
                const targets = sceneTargetNameOptions(scene);
                nodes.push(selectFieldEl(verbOptionsForTarget(parsed.device), parsed.verb, function(v) {
                    parsed.verb = v;
                    st.Actions[idx] = formatActionString(parsed);
                    markDirty();
                    redrawEdit();
                }));
                if (isBrightnessVerb(parsed.verb)) {
                    nodes.push(numberInputEl(parsed.level, 0, 100, function(v) {
                        parsed.level = Math.max(0, Math.min(100, v));
                        st.Actions[idx] = formatActionString(parsed);
                        markDirty();
                        scheduleRedrawEdit();
                    }));
                }
                nodes.push(selectFieldEl(targets, parsed.device, function(v) {
                    parsed.device = v;
                    // Item 4: switching the target can make the currently-selected
                    // verb illegal (brightness requires a dimmable target) -
                    // coerce to toggle rather than leave an invalid combination
                    // staged in the working copy.
                    if (isBrightnessVerb(parsed.verb) && verbOptionsForTarget(v).indexOf(parsed.verb) === -1) {
                        parsed.verb = 'toggle';
                    }
                    st.Actions[idx] = formatActionString(parsed);
                    markDirty();
                    redrawEdit();
                }));
            }
            return nodes;
        }

        // contentNodes tracks whichever DOM nodes buildRowContent last
        // produced, so replaceContent can remove exactly those (and only
        // those) before inserting the freshly rebuilt ones in their place,
        // ahead of the row's own up/down/remove buttons (upBtn, defined
        // below - always still in the DOM by the time replaceContent can
        // possibly run, since that only happens from an async blur handler
        // fired well after this row finished building).
        let contentNodes = [];
        function replaceContent() {
            contentNodes.forEach(function(n) { row.removeChild(n); });
            contentNodes = buildRowContent();
            contentNodes.forEach(function(n) { row.insertBefore(n, upBtn); });
        }

        const upBtn = mkEl('button', { class: 'io-filter-btn' }, '↑');
        upBtn.disabled = idx === 0;
        upBtn.addEventListener('click', function() {
            if (idx === 0) return;
            // Action order is the apply order within a state (Activate walks
            // state.actions in order) - a click-driven structural commit.
            const arr = st.Actions;
            const tmp = arr[idx - 1]; arr[idx - 1] = arr[idx]; arr[idx] = tmp;
            markDirty();
            redrawEdit();
        });

        const downBtn = mkEl('button', { class: 'io-filter-btn' }, '↓');
        downBtn.disabled = idx === st.Actions.length - 1;
        downBtn.addEventListener('click', function() {
            if (idx === st.Actions.length - 1) return;
            const arr = st.Actions;
            const tmp = arr[idx + 1]; arr[idx + 1] = arr[idx]; arr[idx] = tmp;
            markDirty();
            redrawEdit();
        });

        const rmBtn = mkEl('button', { class: 'io-filter-btn schema-row-remove' }, '✕');
        rmBtn.addEventListener('click', function() {
            st.Actions.splice(idx, 1);
            markDirty();
            redrawEdit();
        });

        contentNodes = buildRowContent();
        contentNodes.forEach(function(n) { row.appendChild(n); });
        row.appendChild(upBtn);
        row.appendChild(downBtn);
        row.appendChild(rmBtn);

        return row;
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
    // #schema-edit-modal (the centered dialog, round-2 item 1) via
    // createElement/textContent only - never innerHTML with a
    // user-controlled string (device names, io ids, scene action text are
    // all attacker-controllable in principle). The dialog's own open/closed
    // state is driven off #schema-edit-backdrop's 'open' class (opacity +
    // pointer-events; see style.css) rather than the panel itself, since the
    // panel is just the inner box - the backdrop is what dims the page and
    // must be inert while closed so it never eats clicks meant for the
    // diagram underneath.
    function renderEditPanel() {
        const panel = document.getElementById('schema-edit-modal');
        const backdrop = document.getElementById('schema-edit-backdrop');
        if (!panel) return;
        panel.textContent = '';

        if (!S.selectedEdit) {
            if (backdrop) backdrop.classList.remove('open');
            return;
        }

        if (S.selectedEdit.kind === 'color_light') {
            const closeBtn = mkEl('button', { class: 'schema-panel-close', 'aria-label': 'Close' }, '✕');
            closeBtn.addEventListener('click', function() { selectNode(null); });
            panel.appendChild(closeBtn);
            panel.appendChild(mkEl('div', { class: 'schema-panel-title' }, '\u{1F308} ' + S.selectedEdit.label));
            panel.appendChild(mkEl('div', { class: 'text-muted' }, 'Color lights are not editable here yet.'));
            if (backdrop) backdrop.classList.add('open');
            return;
        }

        const entry = findWorkingEntry(S.selectedEdit.kind, S.selectedEdit.editId);
        if (!entry) {
            S.selectedEdit = null;
            if (backdrop) backdrop.classList.remove('open');
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

        if (backdrop) backdrop.classList.add('open');
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
                // Finding 1: setStatus('') here used to silently wipe
                // 'Saving…' with nothing to replace it, and the errors below
                // only ever reached the modal - which is necessarily closed
                // right now (the backdrop covers the toolbar, so this click
                // could only have fired while it was shut). The banner is the
                // only feedback guaranteed visible at this exact moment;
                // still also populate the modal's own error block (via
                // renderEditPanel) for the case a *different* selection is
                // open when the response comes back.
                setStatus('Save failed — ' + S.saveErrors.length + ' error' + (S.saveErrors.length === 1 ? '' : 's'));
                renderErrorBanner(S.saveErrors);
                renderEditPanel();
                return;
            }
            S.saveErrors = null;
            clearErrorBanner();
            setStatus('');
            showToast('Saved — config reload triggered');
            exitEditMode({ refetch: false });
            setTimeout(loadAndDraw, 1500);
        } catch (e) {
            S.saveErrors = ['network error: ' + (e && e.message || e)];
            setStatus('Save failed — network error');
            renderErrorBanner(S.saveErrors);
            renderEditPanel();
        } finally {
            if (saveBtn) saveBtn.disabled = false;
        }
    }

    // ==================================================================
    // Stage 3: live overlay (round-2 item 3)
    // ==================================================================
    //
    // Fed by app.js's existing 1s /api/state poll: app.js passes the freshly
    // fetched state into render(state) every tick while the schema tab is
    // active (see the public entry point below). This section only ever
    // mutates attributes/classes/text of SVG/DOM elements the diagram
    // already built - it never re-layouts, rebuilds nodes, or touches
    // S.data. Skipped entirely in edit mode: the edit-mode graph carries no
    // live state, and overlaying live values onto a working copy the user
    // is mid-edit on would just be wrong.

    // liveSignature reduces a polled device to whatever recency tracking
    // cares about for its type - a signature change is what "counts" as a
    // state change for the fading accent ring, independent of which
    // specific field(s) actually changed.
    function liveSignature(d) {
        switch (d.type) {
            case 'button': return d.last_event_time || '';
            case 'scene': return String(d.scene_state_index || 0);
            case 'dimmable_light': return (d.is_on ? '1' : '0') + ':' + (d.brightness || 0);
            default: return d.is_on ? '1' : '0';
        }
    }

    // shortEventLabel turns "single_press" into "single" etc., for the
    // brief fading label shown next to a button's flashing state dot.
    function shortEventLabel(eventType) {
        if (!eventType) return '';
        return String(eventType).replace(/_press$/, '');
    }

    // buildIoStateIndex maps every canonical io id a debug point resolves to
    // (io_ids, computed server-side via app.IoPointToId/IoPointToIdWithType -
    // see web_server.go) to that point, so schema io pills (keyed the same
    // way by schema.go's ioCustomNames/appendIoNode) can be matched without
    // re-implementing the shelly/wago id translation client-side.
    function buildIoStateIndex(ioDebug) {
        const idx = new Map();
        (ioDebug || []).forEach(function(pt) {
            (pt.io_ids || []).forEach(function(id) { idx.set(id, pt); });
        });
        return idx;
    }

    // applyLiveOverlay is the per-poll entry point (called from render()
    // below on every tick the schema tab is already built). Guarded so it is
    // a cheap no-op whenever there's nothing sensible to overlay onto.
    function applyLiveOverlay(state) {
        if (S.editMode) return;
        if (!S.data || !state || !Array.isArray(state.devices)) return;
        const svg = document.querySelector('.schema-svg');
        if (!svg) return;

        const now = Date.now();

        // Finding 3: overlay ticks stop entirely while the tab is
        // backgrounded (app.js's stopRefresh) and for the whole duration of
        // an edit session (this function bails out above while S.editMode).
        // On resume, any device whose signature changed during that gap
        // would otherwise be stamped changedAt=now right below, producing a
        // false "just changed" strongest-glow burst across every node that
        // happened to change while nobody was polling. A tick following a
        // >3s gap, or the first tick right after leaving edit mode
        // (S.pendingReseed, set by exitEditMode - a short edit session might
        // not clear the 3s gap check on its own), is treated as a silent
        // re-seed instead: updateDeviceOverlay stamps changedAt=0 for any
        // signature change this tick, exactly like a never-before-seen node.
        const reseed = S.pendingReseed || (S.lastOverlayAt !== 0 && (now - S.lastOverlayAt) > OVERLAY_GAP_RESEED_MS);
        S.pendingReseed = false;

        // Build id->element maps once per tick by walking the DOM directly,
        // rather than one querySelector('[data-node-id="..."]') per device -
        // both for performance and because device/io names are arbitrary
        // strings that could contain characters (e.g. a stray '"') unsafe to
        // interpolate into a CSS attribute-selector string.
        const deviceEls = new Map();
        svg.querySelectorAll('.schema-node-device').forEach(function(g) {
            if (g.dataset.nodeId) deviceEls.set(g.dataset.nodeId, g);
        });
        const pillEls = new Map();
        svg.querySelectorAll('.schema-io-pill').forEach(function(g) {
            if (g.dataset.nodeId) pillEls.set(g.dataset.nodeId, g);
        });

        state.devices.forEach(function(d) {
            const id = 'device:' + d.type + ':' + d.name;
            const g = deviceEls.get(id);
            if (!g) return; // not in the current diagram (renamed/removed elsewhere) - skip silently
            updateDeviceOverlay(g, id, d, now, reseed);
        });

        // Finding 7: built once here and threaded through to
        // updateDetailDockLive below, instead of each rebuilding its own
        // copy from the same state.io_debug every tick.
        const ioStateById = buildIoStateIndex(state.io_debug);
        pillEls.forEach(function(g, nodeId) {
            const ioId = nodeId.indexOf('io:') === 0 ? nodeId.slice(3) : null;
            const pt = ioId ? ioStateById.get(ioId) : null;
            const rect = g.querySelector('.schema-io-pill-rect');
            if (!rect) return;
            if (!pt) { rect.classList.remove('schema-io-on', 'schema-io-off'); return; }
            rect.classList.toggle('schema-io-on', !!pt.state);
            rect.classList.toggle('schema-io-off', !pt.state);
        });

        updateDetailDockLive(state, now, ioStateById);

        S.lastOverlayAt = now;
    }

    // updateDeviceOverlay updates one device node's recency-ring class (on
    // the outer <g>) and its state dot/extra-label (see buildDeviceNode /
    // setDotState) from one polled device entry. Never creates or removes
    // SVG elements - only classes and text on what buildDeviceNode already
    // built.
    function updateDeviceOverlay(g, id, d, now, reseed) {
        const sig = liveSignature(d);
        let track = S.liveTrack.get(id);
        if (!track) {
            // First time this node id has ever been seen since the tab was
            // (re)opened - seed silently (changedAt 0 => effectively
            // infinite age => no glow), so opening the tab never triggers a
            // glow storm across every device at once.
            track = { sig: sig, changedAt: 0 };
            S.liveTrack.set(id, track);
        } else if (track.sig !== sig) {
            track.sig = sig;
            // Finding 3: a re-seed tick (overlay gap, or just having left
            // edit mode) must not treat a signature change accumulated
            // during the gap as "just happened" - stamp 0 (no glow), same as
            // a brand-new track entry above.
            track.changedAt = reseed ? 0 : now;
        }
        const age = track.changedAt ? (now - track.changedAt) : Infinity;
        g.classList.remove('schema-recency-strong', 'schema-recency-medium', 'schema-recency-faint');
        if (age < RECENCY_STRONG_MS) g.classList.add('schema-recency-strong');
        else if (age < RECENCY_MEDIUM_MS) g.classList.add('schema-recency-medium');
        else if (age < RECENCY_FADE_MS) g.classList.add('schema-recency-faint');

        const dot = g.querySelector('.schema-state-dot');
        if (!dot) return; // edit mode / missing placeholder never got one (see buildDeviceNode)
        const extra = g.querySelector('.schema-state-extra');

        if (d.type === 'button') {
            // Finding 4: driven off the signature-change bookkeeping above
            // (liveSignature includes last_event_time for buttons) instead
            // of comparing d.last_event_time to the browser's Date.now() -
            // clock skew between this client and the server (easily >2s on
            // an unsynced Pi) used to kill the flash outright. track.changedAt
            // is stamped from the same clock (now, this function's own
            // Date.now() from applyLiveOverlay) that age is compared against
            // below, so they can never disagree. changedAt===0 still means
            // "no real recent change" (a brand-new track, or a re-seeded one -
            // see updateDeviceOverlay/applyLiveOverlay above) so a stale
            // event already on the schema at page load correctly never
            // flashes, same as before.
            if (track.changedAt !== 0 && (now - track.changedAt) < EVENT_FLASH_MS) {
                setDotState(dot, extra, 'event-flash', shortEventLabel(d.last_event_type));
            } else {
                setDotState(dot, extra, 'off', '');
            }
        } else if (d.type === 'scene') {
            const active = (d.scene_state_index || 0) > 0;
            const name = active && d.scene_state_names ? d.scene_state_names[d.scene_state_index] : '';
            setDotState(dot, extra, active ? 'on' : 'off', name || '');
            // Item 6: toggle the active-state sub-row's highlight class only
            // - buildSceneGroupNode already created one <g class="schema-
            // scene-state-row" data-state-index="N"> per configured state at
            // build time, this never creates/removes rows, same rule as the
            // dot/extra above.
            const activeIdx = d.scene_state_index || 0;
            g.querySelectorAll('.schema-scene-state-row').forEach(function(row) {
                row.classList.toggle('schema-scene-state-active', Number(row.dataset.stateIndex) === activeIdx);
            });
        } else if (d.type === 'dimmable_light') {
            setDotState(dot, extra, d.is_on ? 'on' : 'off', d.is_on ? ((d.brightness || 0) + '%') : '');
        } else {
            setDotState(dot, extra, d.is_on ? 'on' : 'off', '');
        }
    }

    // updateDetailDockLive refreshes the docked detail panel's live "State"
    // facts (textContent only - see the data-live hooks written by
    // renderDetailPanel) when the currently open dock is showing a node this
    // poll has fresh data for. ioStateById is built once per tick by the
    // caller (finding 7) and passed in rather than rebuilt here.
    function updateDetailDockLive(state, now, ioStateById) {
        if (!S.selectedId) return;
        const dock = document.getElementById('schema-detail-dock');
        if (!dock || !dock.classList.contains('open')) return;

        if (S.selectedId.indexOf('io:') === 0) {
            const ioEl = dock.querySelector('[data-live="io_state"]');
            if (!ioEl) return;
            const ioId = S.selectedId.slice(3);
            const pt = ioStateById.get(ioId);
            ioEl.textContent = pt ? (pt.state ? 'on' : 'off') : '—';
            return;
        }

        const d = (state.devices || []).find(function(x) { return ('device:' + x.type + ':' + x.name) === S.selectedId; });
        if (!d) return;

        const isOnEl = dock.querySelector('[data-live="is_on"]');
        if (isOnEl) isOnEl.textContent = d.is_on ? 'on' : 'off';

        const briEl = dock.querySelector('[data-live="brightness"]');
        if (briEl) briEl.textContent = (d.brightness || 0) + '%';

        const evEl = dock.querySelector('[data-live="last_event"]');
        if (evEl) evEl.textContent = formatEventFact(d.last_event_type, d.last_event_time, now);

        const sceneEl = dock.querySelector('[data-live="scene_state"]');
        if (sceneEl) sceneEl.textContent = formatSceneFact(d.scene_state_index || 0, d.scene_state_names);

        // Finding 9 (live half): keep the state-names list's active marker in
        // sync while the dock stays open across a scene state change.
        const listEl = dock.querySelector('[data-live="scene_state_list"]');
        if (listEl && d.scene_state_names) {
            const activeIdx = d.scene_state_index || 0;
            Array.prototype.forEach.call(listEl.children, function(li, idx) {
                li.classList.toggle('text-success', idx === activeIdx);
                li.textContent = sceneStateListItemText(d.scene_state_names[idx] || '', idx, activeIdx);
            });
        }
    }

    // ---- Public entry point ----
    // render(state) is called from app.js's renderPage() on every 1s poll
    // tick while the schema tab is active, passing the freshly polled
    // /api/state. The diagram itself is only built/refetched once per tab
    // activation (see app.js: the 'schema' case returns immediately after
    // this call, same as the pre-round-2 contract) - every subsequent tick
    // just feeds the live overlay (round-2 item 3). We rely on #schema-root
    // only existing once we've built the chrome; other tabs replace
    // #page-content wholesale when navigated to, so returning to /schema
    // naturally finds no #schema-root and rebuilds.
    function render(state) {
        const el = document.getElementById('page-content');
        if (!el) return;
        if (document.getElementById('schema-root')) {
            applyLiveOverlay(state);
            return;
        }

        // Fresh tab activation: reset recency tracking so the very first
        // poll after loadAndDraw() finishes seeds silently instead of
        // comparing against stale values from a previous visit.
        S.liveTrack.clear();
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
            S.selectedId = null;
            S.saveErrors = null;
        },
    };
})();
