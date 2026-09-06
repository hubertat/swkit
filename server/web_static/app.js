// swkit web ui - auto-refresh and interactions

(function() {
    'use strict';

    const REFRESH_INTERVAL = 1000; // ms
    let refreshTimer = null;
    let currentTab = '';
    let ioFilter = 'all';
    let ioSort = 'recent'; // 'recent' | 'default'
    let expandedDrivers = new Set(); // tracks which driver names are expanded

    // ---- API ----

    async function fetchState() {
        const indicator = document.getElementById('refresh-indicator');
        if (indicator) indicator.classList.add('fetching');
        try {
            const resp = await fetch('/api/state');
            if (!resp.ok) throw new Error('fetch failed');
            return await resp.json();
        } finally {
            if (indicator) {
                setTimeout(() => indicator.classList.remove('fetching'), 200);
            }
        }
    }

    // ---- Rendering helpers ----

    function badge(text, cls) {
        return '<span class="badge badge-' + cls + '">' + escHtml(text) + '</span>';
    }

    function getLastActive(pt) {
        let t = 0;
        if (pt.last_changed) { const d = new Date(pt.last_changed).getTime(); if (d > t) t = d; }
        if (pt.last_event)   { const d = new Date(pt.last_event).getTime();   if (d > t) t = d; }
        return t; // 0 if never active
    }

    function stateIndicator(state, lastEvent, lastChanged, now) {
        let cls = state ? 'io-state-on' : 'io-state-off';
        if (lastEvent && (now - new Date(lastEvent).getTime()) < 2000) {
            cls = 'io-state-event';
        }
        const lastActiveMs = Math.max(
            lastEvent   ? new Date(lastEvent).getTime()   : 0,
            lastChanged ? new Date(lastChanged).getTime() : 0
        );
        let extra = '';
        if (lastActiveMs > 0 && (now - lastActiveMs) < 200000) {
            extra = ' io-recently-active';
        }
        return '<span class="io-state-indicator ' + cls + extra + '"></span>';
    }

    function timeSince(ts, now) {
        if (!ts) return '';
        const d = new Date(ts);
        if (d.getTime() === 0) return '';
        const secs = Math.floor((now - d.getTime()) / 1000);
        if (secs < 0) return '';
        if (secs >= 200) return '';
        if (secs < 60) return secs + 's ago';
        const mins = Math.floor(secs / 60);
        return mins + 'm' + (secs % 60) + 's ago';
    }

    function escHtml(s) {
        if (!s) return '';
        return s.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;').replace(/"/g,'&quot;');
    }

    function deviceIcon(type) {
        // Kept in sync with schema.js's DEVICE_ICONS so the same device type
        // reads the same everywhere in the UI.
        switch(type) {
            case 'light': return '\u{1F4A1}';
            case 'color_light': return '\u{1F308}';
            case 'dimmable_light': return '\u{1F506}';
            case 'outlet': return '\u{1F50C}';
            case 'button': return '\u{1F446}';
            case 'scene': return '\u{1F3AC}';
            default: return '?';
        }
    }

    // ---- Dashboard ----

    function renderDashboard(state) {
        const el = document.getElementById('page-content');
        if (!el) return;
        const s = state.summary;

        let driversStatus = s.drivers_ready === s.drivers_total
            ? badge('Ready', 'ready')
            : badge(s.drivers_ready + '/' + s.drivers_total + ' Ready', 'not-ready');

        let hkStatus = s.homekit_enabled
            ? badge('Enabled', 'ready') + ' <span class="text-muted">' + s.homekit_devices + ' accessories</span>'
            : badge('Disabled', 'off');

        const svc = state.services || {};
        const sshStatus = svc.ssh_enabled
            ? ':' + (svc.ssh_port || 2222)
            : '<span class="text-muted">disabled</span>';
        const webStatus = ':' + (svc.web_port || 8080);
        const agentStatus = svc.agent_enabled
            ? '<span class="text-muted">' + escHtml(svc.agent_model || 'enabled') + '</span>'
            : '<span class="text-muted">disabled</span>';

        el.innerHTML = '<div class="dashboard-grid">' +
            '<div class="card">' +
                '<div class="card-title">\u26A1 Drivers</div>' +
                '<div class="stat-value">' + s.drivers_total + '</div>' +
                '<div class="stat-label">configured drivers</div>' +
                '<div class="mt-8">' + driversStatus + '</div>' +
            '</div>' +
            '<div class="card">' +
                '<div class="card-title">\u{1F3E0} Devices</div>' +
                '<div class="stat-row"><span class="label">\u{1F4A1} Lights</span><span class="value">' + s.lights_count + '</span></div>' +
                (s.color_lights_count > 0 ? '<div class="stat-row"><span class="label">\u{1F308} Color Lights</span><span class="value">' + s.color_lights_count + '</span></div>' : '') +
                (s.dimmable_lights_count > 0 ? '<div class="stat-row"><span class="label">\u{1F506} Dimmable Lights</span><span class="value">' + s.dimmable_lights_count + '</span></div>' : '') +
                '<div class="stat-row"><span class="label">\u{1F50C} Outlets</span><span class="value">' + s.outlets_count + '</span></div>' +
                '<div class="stat-row"><span class="label">\u{1F446} Buttons</span><span class="value">' + s.buttons_count + '</span></div>' +
                (s.scenes_count > 0 ? '<div class="stat-row"><span class="label">\u{1F3AC} Scenes</span><span class="value">' + s.scenes_count + '</span></div>' : '') +
            '</div>' +
            '<div class="card">' +
                '<div class="card-title">\u{1F34E} HomeKit</div>' +
                '<div class="mt-8">' + hkStatus + '</div>' +
                (s.homekit_enabled && state.homekit && state.homekit.pin
                    ? '<div class="stat-row mt-8"><span class="label">PIN</span><span class="value mono">' + escHtml(formatPin(state.homekit.pin)) + '</span></div>'
                    : '') +
            '</div>' +
            '<div class="card">' +
                '<div class="card-title">\u2699\uFE0F Services</div>' +
                '<div class="stat-row"><span class="label">SSH</span><span class="value mono">' + sshStatus + '</span></div>' +
                '<div class="stat-row"><span class="label">Web UI</span><span class="value mono">' + webStatus + '</span></div>' +
                '<div class="stat-row"><span class="label">AI Agent</span><span class="value">' + agentStatus + '</span></div>' +
            '</div>' +
        '</div>';
    }

    function formatPin(pin) {
        if (pin && pin.length === 8) {
            return pin.substring(0, 4) + '-' + pin.substring(4);
        }
        return pin || '';
    }

    // ---- Drivers ----

    function formatStatusInfo(info) {
        if (!info) return '';
        // Escape the WHOLE string first, then pick out key:value fragments to
        // wrap in pill markup. The previous version ran the regex over the
        // raw string and only escHtml()'d the matched k/v pieces - any text
        // that didn't match `\w+:\S+` (e.g. a bracketed list with no colon,
        // or anything after a space inside a value) passed through into
        // innerHTML completely unescaped. That text can be config-supplied
        // (e.g. WagoIO.Status()'s "[%s]" module list, or a gate/broker
        // address with a space in it), making this a stored-XSS path - see
        // drivers/wago_driver.go's Status(). Escaping first closes that off:
        // entities like &lt; never contain ":" so they can't spuriously
        // create/break a match, and since escHtml is a no-op on plain
        // alphanumeric status text, benign output is byte-for-byte the same
        // as before.
        const escaped = escHtml(info);
        return escaped.replace(/(\w+):(\S+)/g, function(_, k, v) {
            return '<span class="stat-pill"><span class="pill-key">' + k + '</span><span class="pill-val">' + v + '</span></span>';
        });
    }

    function renderDriverDetailPanel(d) {
        if (!d.details) return '';
        const det = d.details;

        // Shelly: has "devices" array
        if (det.devices !== undefined) {
            if (!det.devices || det.devices.length === 0) {
                return '<div class="driver-detail-panel"><span class="text-muted">No devices discovered yet</span></div>';
            }
            let rows = '';
            for (const dev of det.devices) {
                const healthIcon = dev.healthy
                    ? '<span class="text-success">\u2713</span>'
                    : '<span class="text-error">\u2717</span>';
                rows += '<tr>' +
                    '<td class="mono">' + escHtml(dev.id) + '</td>' +
                    '<td>' + escHtml(dev.model || '-') + '</td>' +
                    '<td class="mono">' + escHtml(dev.network || '-') + '</td>' +
                    '<td>' + dev.switches + '</td>' +
                    '<td>' + dev.inputs + '</td>' +
                    '<td>' + healthIcon + '</td>' +
                    '</tr>';
            }
            const brokerInfo = det.broker
                ? '<div class="driver-detail-meta">Broker: <span class="mono">' + escHtml(det.broker) + '</span></div>'
                : '';
            return '<div class="driver-detail-panel">' + brokerInfo +
                '<div class="table-wrap"><table>' +
                '<thead><tr><th>Device ID</th><th>Model</th><th>Network</th><th>Switches</th><th>Inputs</th><th>Health</th></tr></thead>' +
                '<tbody>' + rows + '</tbody>' +
                '</table></div></div>';
        }

        // WAGO: has "modules" array
        if (det.modules !== undefined) {
            if (!det.modules || det.modules.length === 0) {
                return '<div class="driver-detail-panel"><span class="text-muted">No modules configured</span></div>';
            }
            let rows = '';
            for (const mod of det.modules) {
                rows += '<tr>' +
                    '<td>' + mod.index + '</td>' +
                    '<td class="mono">' + escHtml(mod.part_number || '-') + '</td>' +
                    '<td>' + escHtml(mod.description || '-') + '</td>' +
                    '<td>' + mod.di + '</td>' +
                    '<td>' + mod.do + '</td>' +
                    '</tr>';
            }
            const addrInfo = det.address
                ? '<div class="driver-detail-meta">Address: <span class="mono">' + escHtml(det.address) + ':' + det.port + '</span></div>'
                : '';
            return '<div class="driver-detail-panel">' + addrInfo +
                '<div class="table-wrap"><table>' +
                '<thead><tr><th>#</th><th>Part Number</th><th>Description</th><th>DI</th><th>DO</th></tr></thead>' +
                '<tbody>' + rows + '</tbody>' +
                '</table></div></div>';
        }

        // Fallback: raw JSON
        return '<div class="driver-detail-panel"><pre class="driver-detail-raw">' + escHtml(JSON.stringify(det, null, 2)) + '</pre></div>';
    }

    function renderDrivers(state) {
        const el = document.getElementById('page-content');
        if (!el) return;

        if (!state.drivers || state.drivers.length === 0) {
            el.innerHTML = '<div class="empty-state">No drivers configured</div>';
            return;
        }

        // Count IO points per driver
        const ioCounts = {};
        for (const pt of (state.io_debug || [])) {
            ioCounts[pt.driver_name] = (ioCounts[pt.driver_name] || 0) + 1;
        }

        let rows = '';
        for (const d of state.drivers) {
            const ioCount = ioCounts[d.name];
            const ioBadge = ioCount > 0 ? ' ' + badge(ioCount + ' IOs', 'type') : '';
            const hasDetails = !!d.details;
            const isExpanded = expandedDrivers.has(d.name);
            // JSON.stringify(d.name) is embedded inside a double-quoted HTML
            // attribute; JSON strings are themselves double-quoted, so a raw
            // `"` in d.name would close the attribute early and let anything
            // after it be parsed as new attributes (e.g. onmouseover=...).
            // escHtml() turns those quotes into &quot;, which the HTML parser
            // resolves back to `"` only *after* correctly finding the
            // attribute boundary - so the value the inline handler receives
            // is unchanged, but a crafted driver name can no longer break out
            // of the attribute. Driver names come from IoDriver.String() and
            // aren't attacker-controlled today, but this keeps the pattern
            // safe regardless.
            const expandBtn = hasDetails
                ? '<button class="driver-expand-btn" onclick="swkit.toggleDriver(' + escHtml(JSON.stringify(d.name)) + ')">' +
                    (isExpanded ? '\u25BC' : '\u25B6') + '</button> '
                : '';
            rows += '<tr>' +
                '<td class="fw-600">' + expandBtn + escHtml(d.name) + ioBadge + '</td>' +
                '<td>' + (d.ready ? badge('Ready', 'ready') : badge('Not Ready', 'not-ready')) + '</td>' +
                '<td>' + formatStatusInfo(d.status_info) + '</td>' +
                '</tr>';
            if (hasDetails && isExpanded) {
                rows += '<tr class="driver-detail-row"><td colspan="3">' + renderDriverDetailPanel(d) + '</td></tr>';
            }
        }

        el.innerHTML = '<div class="card">' +
            '<div class="card-title">\u26A1 IO Drivers</div>' +
            '<div class="table-wrap"><table>' +
            '<thead><tr><th>Driver</th><th>Status</th><th>Info</th></tr></thead>' +
            '<tbody>' + rows + '</tbody>' +
            '</table></div></div>';
    }

    // ---- Devices ----

    function renderDevices(state) {
        const el = document.getElementById('page-content');
        if (!el) return;

        if (!state.devices || state.devices.length === 0) {
            el.innerHTML = '<div class="empty-state">No devices configured</div>';
            return;
        }

        const now = Date.now();
        let cards = '';
        for (const d of state.devices) {
            let statusBadge = '';
            if (d.type === 'button') {
                if (d.last_event_time && (now - new Date(d.last_event_time).getTime()) < 2000) {
                    statusBadge = badge(d.last_event_type || 'event', 'event');
                } else if (d.last_event_time && (now - new Date(d.last_event_time).getTime()) < 200000) {
                    statusBadge = badge(d.last_event_type || 'idle', 'on');
                } else {
                    statusBadge = '<span class="text-muted">-</span>';
                }
            } else if (d.type === 'scene') {
                // A scene has N named states, not an on/off pair, so an
                // ON/OFF badge doesn't fit. scene_state_index is omitempty
                // (0 = the conventional "off" state), so it's absent on the
                // wire for a scene that's currently at state 0 - `|| 0`
                // treats that the same as an explicit 0, which is correct
                // here since both mean "index 0".
                const names = d.scene_state_names || [];
                const idx = d.scene_state_index || 0;
                const label = names[idx] || ('state ' + idx);
                statusBadge = badge(label, idx === 0 ? 'off' : 'on');
            } else {
                statusBadge = d.is_on ? badge('ON', 'on') : badge('OFF', 'off');
            }

            // Brightness is omitempty too (0% is absent on the wire), so
            // `|| 0` is again just "no value means 0", not an ambiguity.
            let brightnessBadge = '';
            if (d.type === 'dimmable_light') {
                brightnessBadge = badge((d.brightness || 0) + '%', 'type');
            }

            let healthBadge = '';
            if (d.is_faulty) {
                healthBadge = badge('Faulty', 'faulty');
            } else if (d.is_healthy) {
                healthBadge = badge('Healthy', 'healthy');
            }

            let hkBadge = d.homekit_enabled ? '' : '<span class="text-muted" style="font-size:0.75rem">no HK</span>';

            let details = '';
            if (d.output_io_id) {
                details += '<div class="row"><dt>Output:</dt><dd>' + escHtml(d.output_io_id) + '</dd></div>';
            }
            if (d.rgbw_io_id) {
                details += '<div class="row"><dt>RGBW:</dt><dd>' + escHtml(d.rgbw_io_id) + '</dd></div>';
            }
            if (d.event_input_id) {
                details += '<div class="row"><dt>Input:</dt><dd>' + escHtml(d.event_input_id) + '</dd></div>';
            }
            if (d.type === 'scene' && d.scene_state_names && d.scene_state_names.length > 0) {
                details += '<div class="row"><dt>States:</dt><dd>' + escHtml(d.scene_state_names.join(', ')) + '</dd></div>';
            }

            // Last event for buttons
            if (d.type === 'button' && d.last_event_time) {
                const ago = timeSince(d.last_event_time, now);
                if (ago) {
                    details += '<div class="row"><dt>Last event:</dt><dd>' + escHtml(d.last_event_type) + ' <span class="text-muted">' + ago + '</span></dd></div>';
                }
            }

            let controls = '';
            if (d.control_relations && d.control_relations.length > 0) {
                controls = '<div class="control-relations">';
                for (const rel of d.control_relations) {
                    controls += '<div class="control-rel">' +
                        '<span class="event-type">' + escHtml(rel.event_type) + '</span>' +
                        '<span class="action">' + escHtml(rel.action) + '</span>' +
                        '<span class="target">' + escHtml(rel.device_name) + '</span>' +
                        '</div>';
                }
                controls += '</div>';
            }

            cards += '<div class="device-card">' +
                '<div class="device-header">' +
                    '<span class="device-name">' + deviceIcon(d.type) + ' ' + escHtml(d.name) + '</span>' +
                    '<span>' + statusBadge + ' ' + brightnessBadge + ' ' + healthBadge + ' ' + hkBadge + '</span>' +
                '</div>' +
                '<div style="margin-top:2px">' + badge(d.type, 'type') + '</div>' +
                (details ? '<div class="device-detail">' + details + '</div>' : '') +
                controls +
            '</div>';
        }

        el.innerHTML = '<div class="card"><div class="card-title">\u{1F3E0} Devices</div></div>' +
            '<div class="device-grid">' + cards + '</div>';
    }

    // ---- IO Debug ----

    function renderIoDebug(state) {
        const el = document.getElementById('page-content');
        if (!el) return;

        if (!state.io_debug || state.io_debug.length === 0) {
            el.innerHTML = '<div class="empty-state">No IO debug data available</div>';
            return;
        }

        const now = Date.now();
        const inputs = state.io_debug.filter(p => p.type === 'input');
        const outputs = state.io_debug.filter(p => p.type === 'output');
        const analog = state.io_debug.filter(p => p.type === 'analog_output');

        function applySortIfNeeded(pts) {
            if (ioSort !== 'recent') return pts;
            return [...pts].sort((a, b) => getLastActive(b) - getLastActive(a));
        }

        let filtered;
        if (ioFilter === 'inputs') filtered = applySortIfNeeded(inputs);
        else if (ioFilter === 'outputs') filtered = applySortIfNeeded(outputs);
        else if (ioFilter === 'analog') filtered = applySortIfNeeded(analog);
        else filtered = null; // show columns

        // Filter + sort buttons
        const analogBtn = analog.length > 0
            ? '<button class="io-filter-btn' + (ioFilter === 'analog' ? ' active' : '') + '" onclick="swkit.setIoFilter(\'analog\')">Analog (' + analog.length + ')</button>'
            : '';
        let filters = '<div class="io-filters">' +
            '<button class="io-filter-btn' + (ioFilter === 'all' ? ' active' : '') + '" onclick="swkit.setIoFilter(\'all\')">All</button>' +
            '<button class="io-filter-btn' + (ioFilter === 'inputs' ? ' active' : '') + '" onclick="swkit.setIoFilter(\'inputs\')">Inputs (' + inputs.length + ')</button>' +
            '<button class="io-filter-btn' + (ioFilter === 'outputs' ? ' active' : '') + '" onclick="swkit.setIoFilter(\'outputs\')">Outputs (' + outputs.length + ')</button>' +
            analogBtn +
            '<div style="margin-left:auto;display:flex;gap:4px">' +
            '<button class="io-filter-btn' + (ioSort === 'recent' ? ' active' : '') + '" onclick="swkit.setIoSort(\'recent\')">\u2193 Recent</button>' +
            '<button class="io-filter-btn' + (ioSort === 'default' ? ' active' : '') + '" onclick="swkit.setIoSort(\'default\')">Default</button>' +
            '</div>' +
            '</div>';

        let content;
        if (filtered) {
            content = renderIoTable(filtered, now);
        } else {
            const analogCol = analog.length > 0
                ? '<div class="card"><div class="card-title">Analog (' + analog.length + ')</div>' + renderIoTable(applySortIfNeeded(analog), now) + '</div>'
                : '';
            content = '<div class="io-columns">' +
                '<div class="card"><div class="card-title">Inputs (' + inputs.length + ')</div>' + renderIoTable(applySortIfNeeded(inputs), now) + '</div>' +
                '<div class="card"><div class="card-title">Outputs (' + outputs.length + ')</div>' + renderIoTable(applySortIfNeeded(outputs), now) + '</div>' +
                analogCol +
                '</div>';
        }

        el.innerHTML = '<div class="card"><div class="card-title">\u{1F50D} IO Debug</div>' + filters + '</div>' + content;
    }

    function renderIoTable(points, now) {
        if (!points || points.length === 0) {
            return '<div class="text-muted" style="padding:8px">none</div>';
        }

        let rows = '';
        for (const pt of points) {
            const ind = stateIndicator(pt.state, pt.last_event, pt.last_changed, now);
            const healthBadge = pt.healthy ? '<span class="text-success">\u2713</span>' : '<span class="text-error">\u2717</span>';
            const changed = timeSince(pt.last_changed, now);
            const evtTime = timeSince(pt.last_event, now);
            const configured = pt.configured_as ? '<span class="text-muted">&lt;' + escHtml(pt.configured_as) + '&gt;</span>' : '';
            const label = pt.custom_name
                ? '<span class="fw-600">' + escHtml(pt.custom_name) + '</span> <span class="text-muted mono" style="font-size:0.75rem">' + escHtml(pt.name) + '</span>'
                : '<span class="mono">' + escHtml(pt.name) + '</span>';

            let stateCell;
            if (pt.type === 'analog_output') {
                const max = pt.max || 0;
                const pct = max > 0 ? Math.round((pt.value || 0) * 100 / max) : 0;
                stateCell = '<span class="mono">' + (pt.value || 0) + '</span> <span class="text-muted">(' + pct + '%)</span>';
            } else {
                stateCell = ind + (pt.state ? ' ON' : ' OFF');
            }

            rows += '<tr>' +
                '<td>' + escHtml(pt.driver_name) + '</td>' +
                '<td>' + label + '</td>' +
                '<td>' + stateCell + '</td>' +
                '<td>' + healthBadge + '</td>' +
                '<td class="text-muted">' + (changed || evtTime || '-') + '</td>' +
                '<td>' + configured + '</td>' +
                '</tr>';
        }

        return '<div class="table-wrap"><table>' +
            '<thead><tr><th>Driver</th><th>Name</th><th>State</th><th>Health</th><th>Activity</th><th>Device</th></tr></thead>' +
            '<tbody>' + rows + '</tbody>' +
            '</table></div>';
    }

    // ---- Logs ----

    let logsEventSource = null;
    let logsLines = [];
    const LOGS_MAX_LINES = 500;
    let logsAutoScroll = true;
    let logsLevelFilter = 'all'; // 'all' | 'info' | 'warn' | 'error' | 'debug'
    let logsTextFilter = '';

    const LOG_LEVEL_KEYWORDS = {
        debug: ['DEBU', 'DEBG', 'debug', 'DEBUG'],
        info:  ['INFO', 'info'],
        warn:  ['WARN', 'warn', 'WARNING'],
        error: ['ERRO', 'error', 'ERROR'],
    };

    function lineMatchesLevel(line, level) {
        if (level === 'all') return true;
        const keywords = LOG_LEVEL_KEYWORDS[level] || [];
        return keywords.some(kw => line.includes(kw));
    }

    function getFilteredLines() {
        return logsLines.filter(line => {
            if (!lineMatchesLevel(line, logsLevelFilter)) return false;
            if (logsTextFilter && !line.toLowerCase().includes(logsTextFilter.toLowerCase())) return false;
            return true;
        });
    }

    function renderLogs() {
        const el = document.getElementById('page-content');
        if (!el) return;

        // Only create DOM once
        if (!el.querySelector('#logs-output')) {
            el.innerHTML = '<div class="card">' +
                '<div class="card-title">\u{1F4DC} Logs</div>' +
                '<div class="logs-toolbar">' +
                    '<div class="logs-level-filters">' +
                        '<button class="io-filter-btn logs-level-btn active" data-level="all">All</button>' +
                        '<button class="io-filter-btn logs-level-btn" data-level="debug">Debug</button>' +
                        '<button class="io-filter-btn logs-level-btn" data-level="info">Info</button>' +
                        '<button class="io-filter-btn logs-level-btn" data-level="warn">Warn</button>' +
                        '<button class="io-filter-btn logs-level-btn" data-level="error">Error</button>' +
                    '</div>' +
                    '<input id="logs-text-filter" class="logs-text-filter" type="text" placeholder="Filter text..." value="">' +
                    '<button id="logs-clear-btn" class="io-filter-btn">Clear</button>' +
                    '<button id="logs-autoscroll-btn" class="io-filter-btn active">Auto-scroll</button>' +
                    '<span id="logs-count" class="text-muted"></span>' +
                '</div>' +
            '</div>' +
            '<div class="logs-container"><pre id="logs-output"></pre></div>';

            // Level filter buttons
            el.querySelectorAll('.logs-level-btn').forEach(function(btn) {
                btn.addEventListener('click', function() {
                    logsLevelFilter = this.getAttribute('data-level');
                    el.querySelectorAll('.logs-level-btn').forEach(b => b.classList.remove('active'));
                    this.classList.add('active');
                    updateLogsOutput();
                });
            });

            // Text filter input
            document.getElementById('logs-text-filter').addEventListener('input', function() {
                logsTextFilter = this.value;
                updateLogsOutput();
            });

            document.getElementById('logs-clear-btn').addEventListener('click', function() {
                logsLines = [];
                updateLogsOutput();
            });
            document.getElementById('logs-autoscroll-btn').addEventListener('click', function() {
                logsAutoScroll = !logsAutoScroll;
                this.classList.toggle('active', logsAutoScroll);
                if (logsAutoScroll) scrollLogsToBottom();
            });

            // Disable auto-scroll when user scrolls up manually
            el.querySelector('.logs-container').addEventListener('scroll', function() {
                const atBottom = this.scrollHeight - this.scrollTop - this.clientHeight < 8;
                if (!atBottom && logsAutoScroll) {
                    logsAutoScroll = false;
                    const btn = document.getElementById('logs-autoscroll-btn');
                    if (btn) btn.classList.remove('active');
                } else if (atBottom && !logsAutoScroll) {
                    logsAutoScroll = true;
                    const btn = document.getElementById('logs-autoscroll-btn');
                    if (btn) btn.classList.add('active');
                }
            });
        }

        // Start SSE if not connected
        if (!logsEventSource) {
            logsEventSource = new EventSource('/api/logs/stream');
            logsEventSource.onmessage = function(e) {
                logsLines.push(e.data);
                if (logsLines.length > LOGS_MAX_LINES) {
                    logsLines = logsLines.slice(logsLines.length - LOGS_MAX_LINES);
                }
                updateLogsOutput();
            };
            logsEventSource.onerror = function() {
                // Will auto-reconnect
            };
        }

        updateLogsOutput();
    }

    function updateLogsOutput() {
        const out = document.getElementById('logs-output');
        if (!out) return;
        const filtered = getFilteredLines();
        out.textContent = filtered.join('\n');
        const countEl = document.getElementById('logs-count');
        if (countEl) {
            const filterDesc = logsLevelFilter !== 'all' || logsTextFilter
                ? filtered.length + ' of ' + logsLines.length
                : logsLines.length + '/' + LOGS_MAX_LINES;
            countEl.textContent = filterDesc + ' lines';
        }
        if (logsAutoScroll) scrollLogsToBottom();
    }

    function scrollLogsToBottom() {
        const container = document.querySelector('.logs-container');
        if (container) container.scrollTop = container.scrollHeight;
    }

    function closeLogsStream() {
        if (logsEventSource) {
            logsEventSource.close();
            logsEventSource = null;
        }
    }

    // ---- Config ----

    function renderConfig(state) {
        const el = document.getElementById('page-content');
        if (!el) return;

        if (!state.config_json) {
            el.innerHTML = '<div class="empty-state">No configuration data available</div>';
            return;
        }

        el.innerHTML = '<div class="card"><div class="card-title">\u2699\uFE0F Configuration</div></div>' +
            '<div class="config-display"><pre>' + syntaxHighlight(state.config_json) + '</pre></div>';
    }

    function syntaxHighlight(json) {
        if (typeof json !== 'string') {
            json = JSON.stringify(json, null, 2);
        }
        json = escHtml(json);
        return json.replace(
            /("(\\u[a-fA-F0-9]{4}|\\[^u]|[^\\"])*"(\s*:)?|\b(true|false|null)\b|-?\d+(?:\.\d*)?(?:[eE][+\-]?\d+)?)/g,
            function(match) {
                let cls = 'number';
                if (/^"/.test(match)) {
                    if (/:$/.test(match)) {
                        cls = 'key';
                        // Remove trailing colon for wrapping, add it back
                        return '<span class="' + cls + '">' + match.slice(0, -1) + '</span>:';
                    } else {
                        cls = 'string';
                    }
                } else if (/true|false/.test(match)) {
                    cls = 'bool';
                } else if (/null/.test(match)) {
                    cls = 'null';
                }
                return '<span class="' + cls + '">' + match + '</span>';
            }
        );
    }

    // ---- Page routing ----

    function getTab() {
        const path = window.location.pathname;
        if (path === '/io-debug') return 'io-debug';
        if (path === '/devices') return 'devices';
        if (path === '/drivers') return 'drivers';
        if (path === '/config') return 'config';
        if (path === '/logs') return 'logs';
        if (path === '/schema') return 'schema';
        return 'dashboard';
    }

    function renderPage(state) {
        const tab = getTab();

        // Leaving the schema tab while edit mode is active needs to tear down
        // that mode, since the whole schema chrome (including the working
        // copy) lives only in schema.js's module state and the DOM backing it
        // gets discarded the moment another tab's render*() replaces
        // #page-content wholesale - but S.editMode itself is untouched by
        // that DOM replacement. Left set, it causes stale-state bugs on
        // return to /schema (buildChrome rebuilds fresh chrome while editMode
        // still reports true, and clicking a node reopens an edit form bound
        // to the old working copy). Confirm first only when there are
        // unsaved changes to actually lose; otherwise discard silently.
        if (currentTab === 'schema' && tab !== 'schema' && window.swkitSchema) {
            if (window.swkitSchema.hasUnsavedChanges && window.swkitSchema.hasUnsavedChanges()) {
                if (!confirm('You have unsaved schema changes. Leave without saving?')) {
                    history.replaceState(null, '', '/schema');
                    return; // stay put; #page-content/schema chrome is untouched
                }
            }
            if (window.swkitSchema.discardSilently) window.swkitSchema.discardSilently();
        }

        currentTab = tab;

        // Update nav active state
        document.querySelectorAll('.nav a').forEach(a => {
            a.classList.toggle('active', a.getAttribute('data-tab') === tab);
        });

        // Update timestamp
        const tsEl = document.getElementById('timestamp');
        if (tsEl && state.timestamp) {
            const d = new Date(state.timestamp);
            tsEl.textContent = d.toLocaleTimeString();
        }

        // Close logs SSE when navigating away
        if (tab !== 'logs') {
            closeLogsStream();
        }

        switch(tab) {
            case 'dashboard': renderDashboard(state); break;
            case 'drivers': renderDrivers(state); break;
            case 'devices': renderDevices(state); break;
            case 'io-debug': renderIoDebug(state); break;
            case 'config': renderConfig(state); break;
            case 'logs': renderLogs(); return; // Logs doesn't need state polling
            case 'schema': if (window.swkitSchema) window.swkitSchema.render(state); return; // no diagram re-render on poll; state feeds the live overlay only
        }
    }

    // ---- Refresh loop ----
    //
    // A plain setInterval(refresh, ...) with an async callback can overlap:
    // if /api/state is slow, a second (or third) fetch can start before the
    // first resolves, and there's no guarantee they land in request order -
    // a slow response for an older tick can render after a newer one,
    // showing stale data. Fixed by self-scheduling: the next tick is only
    // queued via setTimeout once the current one has fully completed
    // (fetch + render, or the caught error), so at most one refresh cycle is
    // ever in flight from the timer alone.
    //
    // refreshInFlight/refreshQueued additionally guard the one place a
    // refresh can be triggered from outside the timer loop: navigate() and
    // the popstate handler both call refresh() directly for an immediate
    // update. If that lands while a timer-driven refresh is already
    // in-flight, it's coalesced into a single follow-up run right after the
    // current one finishes, rather than firing a second concurrent fetch.
    let refreshActive = false;
    let refreshInFlight = false;
    let refreshQueued = false;

    async function refresh() {
        if (refreshInFlight) {
            refreshQueued = true;
            return;
        }
        refreshInFlight = true;
        try {
            const tab = getTab();
            if (tab === 'logs') {
                // Logs is driven entirely by its own SSE stream (see
                // renderLogs/logsEventSource) and never reads `state` -
                // renderPage's 'logs' case ignores its argument. Skip the
                // /api/state fetch, but still run renderPage so nav
                // highlighting and the logs SSE connect/close still happen
                // on navigation into/out of this tab. The header timestamp
                // simply stops advancing while on this tab, since it has
                // nothing to do with logs.
                renderPage({});
            } else {
                const state = await fetchState();
                renderPage(state);
            }
        } catch(e) {
            // silently retry on next tick
        } finally {
            refreshInFlight = false;
        }
        if (refreshQueued) {
            refreshQueued = false;
            refresh();
        }
    }

    function scheduleRefresh() {
        if (!refreshActive) return;
        // Clear any timer that's still pending before replacing the handle.
        // stopRefresh()/startRefresh() (visibilitychange) landing while a
        // refresh is awaiting its fetch leaves the old timer callback still
        // suspended: when it resumes it calls scheduleRefresh() again, so
        // without this clear two self-scheduling chains would both stay
        // alive - doubling the poll rate and leaving one of them untracked
        // by refreshTimer, where stopRefresh() can no longer cancel it.
        if (refreshTimer) clearTimeout(refreshTimer);
        refreshTimer = setTimeout(async function() {
            await refresh();
            scheduleRefresh();
        }, REFRESH_INTERVAL);
    }

    function startRefresh() {
        if (refreshActive) return; // already running
        refreshActive = true;
        refresh().then(scheduleRefresh);
    }

    function stopRefresh() {
        refreshActive = false;
        if (refreshTimer) {
            clearTimeout(refreshTimer);
            refreshTimer = null;
        }
    }

    // ---- Navigation (SPA-like) ----

    function navigate(path) {
        history.pushState(null, '', path);
        refresh();
    }

    // ---- Init ----

    document.addEventListener('DOMContentLoaded', function() {
        // Set up nav clicks
        document.querySelectorAll('.nav a').forEach(a => {
            a.addEventListener('click', function(e) {
                e.preventDefault();
                navigate(this.getAttribute('href'));
            });
        });

        // Handle browser back/forward
        window.addEventListener('popstate', function() {
            refresh();
        });

        // Keyboard tab navigation: 1-7 switches tabs
        document.addEventListener('keydown', function(e) {
            if (document.activeElement && document.activeElement !== document.body) return;
            const tabs = ['/', '/drivers', '/devices', '/io-debug', '/config', '/logs', '/schema'];
            const n = parseInt(e.key);
            if (n >= 1 && n <= 7) { e.preventDefault(); navigate(tabs[n - 1]); }
        });

        startRefresh();
    });

    // Pause refresh when tab is hidden
    document.addEventListener('visibilitychange', function() {
        if (document.hidden) {
            stopRefresh();
        } else {
            startRefresh();
        }
    });

    // Expose for filter/sort buttons and driver expand toggle
    window.swkit = {
        setIoFilter: function(f) { ioFilter = f; refresh(); },
        setIoSort: function(s) { ioSort = s; refresh(); },
        toggleDriver: function(name) {
            if (expandedDrivers.has(name)) {
                expandedDrivers.delete(name);
            } else {
                expandedDrivers.add(name);
            }
            refresh();
        }
    };
})();
