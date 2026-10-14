/* swkit scene studio
 *
 * A visual editor for scenes, at /scenes. It edits the same config as the
 * Schema tab (GET/POST /api/config/edit, with the same revision handshake),
 * but only touches Scenes (and, when a scene is renamed or deleted, the button
 * relations and scene steps that point at it).
 *
 * Model
 * -----
 * A scene state's config is a list of step strings (see app.ParseSceneStep):
 * "on:Kitchen", "brightness:60:Hall", "after:10m:off:Hall". The studio groups
 * them into "moments": one device at one point in time, with an optional
 * power part (on/off/toggle) and an optional brightness part (set/up/down),
 * because a dimmable light's on/off and level are independent outputs and
 * people think of "Hall on at 60%" as one thing. A moment serializes back to
 * at most two steps with the same delay. Strings the studio cannot read are
 * kept verbatim and shown as such, never dropped.
 *
 * Scenes untouched in a session are sent back exactly as they were loaded, so
 * opening and saving never reformats someone's hand-written config.
 */
(function () {
    'use strict';

    // ---------- constants ----------

    // Delay snap points (seconds) for dragging and keyboard nudging.
    const SNAP_S = [0, 1, 2, 3, 5, 10, 15, 20, 30, 45, 60, 90, 120, 180, 300, 420, 600, 900, 1200, 1800,
        2700, 3600, 5400, 7200, 10800, 14400, 21600, 28800, 43200, 64800, 86400];
    // Ruler tick candidates (seconds); thinned to fit the track width.
    const TICK_S = [0, 5, 15, 30, 60, 120, 300, 600, 900, 1800, 3600, 7200, 14400, 28800, 43200, 86400];
    const DELAY_PRESETS_S = [5, 30, 60, 300, 900, 1800, 3600];
    const MAX_DELAY_MS = 24 * 3600 * 1000;
    const SCALE_K = 10; // seconds; knee of the log-like time scale

    const ICON = {
        light: '<svg viewBox="0 0 24 24"><path d="M9 18h6M10 21h4"/><path d="M12 3a6 6 0 0 0-3.6 10.8c.7.6 1.1 1.3 1.1 2.2h5c0-.9.4-1.6 1.1-2.2A6 6 0 0 0 12 3z"/></svg>',
        color_light: '<svg viewBox="0 0 24 24"><path d="M12 3a9 9 0 1 0 0 18c1 0 1.6-.8 1.6-1.6 0-.5-.2-.9-.5-1.2-.3-.3-.5-.7-.5-1.2 0-.9.7-1.6 1.6-1.6H16a5 5 0 0 0 5-5C21 6.5 17 3 12 3z"/><circle cx="7.5" cy="11" r="1.2"/><circle cx="10.5" cy="7" r="1.2"/><circle cx="15.5" cy="7.5" r="1.2"/></svg>',
        dimmable_light: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></svg>',
        outlet: '<svg viewBox="0 0 24 24"><rect x="4" y="4" width="16" height="16" rx="4.5"/><path d="M9.5 9.5v2M14.5 9.5v2M10 15.5h4"/></svg>',
        scene: '<svg viewBox="0 0 24 24"><path d="M11 3l1.7 4.6L17.3 9.3l-4.6 1.7L11 15.6 9.3 11 4.7 9.3l4.6-1.7z"/><path d="M18 14l.8 2.2L21 17l-2.2.8L18 20l-.8-2.2L15 17l2.2-.8z"/></svg>',
        play: '<svg viewBox="0 0 24 24"><path d="M8 5.5v13l10.5-6.5z"/></svg>',
        plus: '<svg viewBox="0 0 24 24"><path d="M12 5v14M5 12h14"/></svg>',
        copy: '<svg viewBox="0 0 24 24"><rect x="8" y="8" width="12" height="12" rx="2.5"/><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2"/></svg>',
        trash: '<svg viewBox="0 0 24 24"><path d="M4 7h16M10 11v6M14 11v6M6 7l1 12a2 2 0 0 0 2 2h6a2 2 0 0 0 2-2l1-12M9 7V4h6v3"/></svg>',
        left: '<svg viewBox="0 0 24 24"><path d="M15 6l-6 6 6 6"/></svg>',
        right: '<svg viewBox="0 0 24 24"><path d="M9 6l6 6-6 6"/></svg>',
        clock: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="8.5"/><path d="M12 7.5V12l3 2"/></svg>',
        close: '<svg viewBox="0 0 24 24"><path d="M6 6l12 12M18 6L6 18"/></svg>',
        off: '<svg viewBox="0 0 24 24"><path d="M12 3v8"/><path d="M6.6 6.6a8 8 0 1 0 10.8 0"/></svg>',
    };

    const TYPE_LABEL = {
        light: 'Light', dimmable_light: 'Dimmable light', color_light: 'Color light',
        outlet: 'Outlet', scene: 'Scene',
    };

    // ---------- state ----------

    const S = {
        loaded: false,
        loading: false,
        loadError: null,
        config: null,      // last loaded EditableConfig (Scenes replaced on save)
        meta: null,
        revision: '',
        scenes: [],        // working copy, see sceneFromConfig
        baseline: '',      // signature() right after load/save
        buttonsBaseline: '',
        sel: { scene: null, state: null, device: null, moment: null },
        live: null,
        saving: false,
        saveErrors: [],
        conflict: false,
        running: false,
        root: null,
    };
    let seq = 0;
    let orderSeq = 1e6;

    // ---------- small helpers ----------

    function h(tag, attrs) {
        const el = document.createElement(tag);
        if (attrs) {
            for (const k of Object.keys(attrs)) {
                const v = attrs[k];
                if (v === null || v === undefined || v === false) continue;
                if (k === 'class') el.className = v;
                else if (k === 'html') el.innerHTML = v; // trusted static markup only
                else if (k === 'style' && typeof v === 'object') Object.assign(el.style, v);
                else if (k.indexOf('on') === 0) el.addEventListener(k.slice(2), v);
                else el.setAttribute(k, v === true ? '' : v);
            }
        }
        for (let i = 2; i < arguments.length; i++) append(el, arguments[i]);
        return el;
    }

    function append(el, c) {
        if (c === null || c === undefined || c === false) return;
        if (Array.isArray(c)) { c.forEach(x => append(el, x)); return; }
        el.appendChild(c instanceof Node ? c : document.createTextNode(String(c)));
    }

    function icon(name, cls) {
        return h('span', { class: 'ss-ico' + (cls ? ' ' + cls : ''), 'aria-hidden': 'true', html: ICON[name] || ICON.light });
    }

    function uid() { return 'k' + (++seq); }
    function clamp(v, lo, hi) { return Math.max(lo, Math.min(hi, v)); }
    function isBri(verb) { return verb === 'brightness' || verb === 'brightness_up' || verb === 'brightness_down'; }
    function deepCopy(x) { return JSON.parse(JSON.stringify(x)); }

    // fmtDelayGo mirrors app.FormatStepDelay: the compact form written to config.
    function fmtDelayGo(ms) {
        if (ms <= 0) return '0s';
        let out = '';
        const hr = Math.floor(ms / 3600000); ms -= hr * 3600000;
        const mn = Math.floor(ms / 60000); ms -= mn * 60000;
        const sc = Math.floor(ms / 1000); ms -= sc * 1000;
        if (hr) out += hr + 'h';
        if (mn) out += mn + 'm';
        if (sc) out += sc + 's';
        if (ms) out += ms + 'ms';
        return out;
    }

    // fmtDelayHuman is the label people read: "5 s", "1 min 30 s", "2 h".
    function fmtDelayHuman(ms) {
        if (ms <= 0) return 'At start';
        const s = Math.round(ms / 1000);
        if (ms < 1000) return ms + ' ms';
        if (s < 60) return s + ' s';
        const hr = Math.floor(s / 3600), mn = Math.floor((s % 3600) / 60), sc = s % 60;
        if (hr) return hr + ' h' + (mn ? ' ' + mn + ' min' : '');
        return mn + ' min' + (sc ? ' ' + sc + ' s' : '');
    }

    function fmtDelayShort(ms) {
        if (ms <= 0) return '0';
        const s = Math.round(ms / 1000);
        if (s < 60) return s + 's';
        if (s < 3600) return (s % 60 ? (s / 60).toFixed(1).replace(/\.0$/, '') : s / 60) + 'm';
        return (s % 3600 ? (s / 3600).toFixed(1).replace(/\.0$/, '') : s / 3600) + 'h';
    }

    // parseGoDuration accepts the Go syntax used in config ("1m30s", "1.5h",
    // "500ms"); parseDelayInput also takes a bare number as seconds.
    function parseGoDuration(str) {
        const s = String(str).trim();
        if (!s) return null;
        const re = /(\d+(?:\.\d+)?)(ms|h|m|s)/gy;
        let total = 0, m, consumed = 0;
        while ((m = re.exec(s)) !== null) {
            const n = parseFloat(m[1]);
            total += n * ({ ms: 1, s: 1000, m: 60000, h: 3600000 })[m[2]];
            consumed = re.lastIndex;
        }
        if (consumed !== s.length) return null;
        return Math.round(total);
    }

    function parseDelayInput(str) {
        const s = String(str).trim().toLowerCase().replace(/\s+/g, '').replace(/min$/, 'm').replace(/sec$/, 's');
        if (/^\d+(\.\d+)?$/.test(s)) return Math.round(parseFloat(s) * 1000);
        return parseGoDuration(s);
    }

    // ---------- config <-> model ----------

    // parseStep mirrors app.ParseSceneStep / app.ParseAction.
    function parseStep(str) {
        let parts = String(str || '').split(':');
        let delay = 0, delayRaw = null;
        if ((parts[0] || '').toLowerCase() === 'after') {
            if (parts.length < 3) return null;
            delay = parseGoDuration(parts[1]);
            if (delay === null || delay < 0 || delay > MAX_DELAY_MS || delay % 1 !== 0) return null;
            delayRaw = parts[1];
            parts = parts.slice(2);
        }
        const verb = (parts[0] || '').toLowerCase();
        if (verb === 'on' || verb === 'off' || verb === 'toggle') {
            if (parts.length !== 2) return null;
            return { delay: delay, delayRaw: delayRaw, verb: verb, level: 0, device: parts[1] };
        }
        if (isBri(verb)) {
            if (parts.length !== 3 || !/^-?\d+$/.test(parts[1])) return null;
            return { delay: delay, delayRaw: delayRaw, verb: verb, level: parseInt(parts[1], 10), device: parts[2] };
        }
        return null;
    }

    function newMoment(device, delay) {
        return { id: uid(), device: device, delay: delay || 0, delayRaw: null, power: null, bri: null, order: orderSeq++, briFirst: null };
    }

    function stateFromConfig(st) {
        const moments = [], raw = [];
        (st.Actions || []).forEach(function (a, i) {
            const p = parseStep(a);
            if (!p) { raw.push({ id: uid(), text: a }); return; }
            const slot = isBri(p.verb) ? 'bri' : 'power';
            let m = moments.find(x => x.device === p.device && x.delay === p.delay && x[slot] === null);
            if (!m) {
                // briFirst records the loaded step order so an untouched
                // moment serializes exactly as written.
                m = { id: uid(), device: p.device, delay: p.delay, delayRaw: p.delayRaw, power: null, bri: null, order: i, briFirst: slot === 'bri' };
                moments.push(m);
            }
            if (slot === 'bri') m.bri = { verb: p.verb, level: p.level };
            else m.power = p.verb;
        });
        return { id: uid(), name: st.Name || '', moments: moments, raw: raw };
    }

    function serializeState(st) {
        const out = st.raw.map(r => r.text);
        st.moments.slice().sort((a, b) => a.delay - b.delay || a.order - b.order).forEach(function (m) {
            if (!m.power && !m.bri) return;
            const prefix = m.delay > 0 ? 'after:' + (m.delayRaw || fmtDelayGo(m.delay)) + ':' : '';
            const pw = m.power ? prefix + m.power + ':' + m.device : null;
            const br = m.bri ? prefix + m.bri.verb + ':' + m.bri.level + ':' + m.device : null;
            // A moment edited here sets the level before switching on, so a
            // dimmable light never flashes at its previous level.
            const briFirst = m.briFirst === null ? m.power === 'on' : m.briFirst;
            (briFirst ? [br, pw] : [pw, br]).forEach(x => { if (x) out.push(x); });
        });
        return out;
    }

    function sceneFromConfig(sc) {
        const s = {
            id: uid(),
            name: sc.Name || '',
            disableHomekit: !!sc.DisableHomekit,
            states: (sc.States || []).map(stateFromConfig),
            orig: sc,
            savedName: sc.Name || '', // name on the server, for Run
            baseSig: '',
        };
        s.baseSig = JSON.stringify(serializeScene(s));
        return s;
    }

    function serializeScene(s) {
        const out = { Name: s.name, States: s.states.map(st => ({ Name: st.name, Actions: serializeState(st) })) };
        if (s.disableHomekit) out.DisableHomekit = true;
        return out;
    }

    function sceneDirty(s) { return !s.orig || JSON.stringify(serializeScene(s)) !== s.baseSig; }

    function signature() {
        return JSON.stringify(S.scenes.map(serializeScene));
    }

    function isDirty() {
        if (!S.loaded) return false;
        return signature() !== S.baseline || JSON.stringify(S.config.Buttons || []) !== S.buttonsBaseline;
    }

    // ---------- targets ----------

    // targetsFor lists what a scene's steps may drive: every light, outlet and
    // color light, plus scenes defined earlier (SwKit builds scenes in order,
    // so a scene can only reference one that already exists).
    function targetsFor(scene) {
        const cfg = S.config;
        const out = [];
        const known = new Set();
        (cfg.Lights || []).forEach(l => { out.push({ name: l.Name, type: 'light' }); known.add(l.Name); });
        (cfg.DimmableLights || []).forEach(l => { out.push({ name: l.Name, type: 'dimmable_light' }); known.add(l.Name); });
        (cfg.Outlets || []).forEach(o => { out.push({ name: o.Name, type: 'outlet' }); known.add(o.Name); });
        const savedScenes = new Set((cfg.Scenes || []).map(s => s.Name));
        (S.meta.output_device_names || []).forEach(function (n) {
            if (!known.has(n) && !savedScenes.has(n)) { out.push({ name: n, type: 'color_light' }); known.add(n); }
        });
        const idx = S.scenes.indexOf(scene);
        S.scenes.slice(0, Math.max(idx, 0)).forEach(s => out.push({ name: s.name, type: 'scene' }));
        return out;
    }

    function targetType(scene, name) {
        const t = targetsFor(scene).find(x => x.name === name);
        return t ? t.type : null;
    }

    // allDeviceNames lists every name a scene must not reuse, other than the
    // scenes themselves: output_device_names also lists the saved scenes,
    // which are checked against the working copy instead (they may have been
    // renamed or deleted in this session).
    function allDeviceNames() {
        const cfg = S.config, names = new Set();
        const savedScenes = new Set((cfg.Scenes || []).map(s => s.Name));
        ['Lights', 'DimmableLights', 'Outlets', 'Buttons'].forEach(k => (cfg[k] || []).forEach(d => names.add(d.Name)));
        (S.meta.output_device_names || []).forEach(n => { if (!savedScenes.has(n)) names.add(n); });
        return names;
    }

    // ---------- selection ----------

    function curScene() { return S.scenes.find(s => s.id === S.sel.scene) || null; }
    function curState() {
        const sc = curScene();
        return sc ? sc.states.find(st => st.id === S.sel.state) || null : null;
    }

    function selectScene(id) {
        S.sel.scene = id;
        const sc = curScene();
        S.sel.state = sc && sc.states.length ? sc.states[Math.min(1, sc.states.length - 1)].id : null;
        S.sel.device = null;
        S.sel.moment = null;
        try { history.replaceState(null, '', '/scenes' + (sc ? '?scene=' + encodeURIComponent(sc.name) : '')); } catch (e) { /* ignore */ }
    }

    // ---------- loading & saving ----------

    async function load(keep) {
        if (S.loading) return;
        S.loading = true;
        S.loadError = null;
        try {
            const resp = await fetch('/api/config/edit', { cache: 'no-store' });
            const data = await resp.json().catch(() => ({}));
            if (!resp.ok) throw new Error((data.errors && data.errors.join('; ')) || 'HTTP ' + resp.status);
            S.config = data.config;
            S.meta = data.meta || {};
            S.revision = data.revision || '';
            S.scenes = (S.config.Scenes || []).map(sceneFromConfig);
            S.baseline = signature();
            S.buttonsBaseline = JSON.stringify(S.config.Buttons || []);
            S.loaded = true;
            S.conflict = false;
            S.saveErrors = [];

            // Restore the selection by name (ids change on reload).
            let want = keep ? keep.scene : null;
            if (!want) {
                const q = new URLSearchParams(location.search).get('scene');
                if (q) want = q;
            }
            const sc = S.scenes.find(s => s.name === want) || S.scenes[0];
            if (sc) {
                selectScene(sc.id);
                if (keep && keep.stateIdx !== undefined && sc.states[keep.stateIdx]) S.sel.state = sc.states[keep.stateIdx].id;
            } else {
                S.sel = { scene: null, state: null, device: null, moment: null };
            }
        } catch (e) {
            S.loadError = e.message;
        } finally {
            S.loading = false;
            renderAll();
        }
    }

    function buildPayload() {
        const cfg = deepCopy(S.config);
        cfg.Scenes = S.scenes.map(s => (sceneDirty(s) ? serializeScene(s) : deepCopy(s.orig)));
        return { config: cfg, revision: S.revision };
    }

    async function save() {
        if (S.saving) return;
        const problems = localProblems();
        if (problems.length) {
            S.saveErrors = problems;
            renderAll();
            return;
        }
        S.saving = true;
        S.saveErrors = [];
        renderSaveBar();
        const sc = curScene(), st = curState();
        const keep = sc ? { scene: sc.name, stateIdx: sc.states.indexOf(st) } : null;
        try {
            const resp = await fetch('/api/config/edit', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(buildPayload()),
            });
            const data = await resp.json().catch(() => ({}));
            if (resp.status === 409) {
                S.conflict = true;
                S.saveErrors = ['Someone else changed the configuration since you opened it. Reload to get their version; your unsaved scene changes will be lost.'];
            } else if (!resp.ok || !data.ok) {
                S.saveErrors = data.errors && data.errors.length ? data.errors : ['Save failed (HTTP ' + resp.status + ').'];
            } else {
                toast('Saved. swkit is reloading its config.');
                S.saving = false;
                // The save triggers a config reload; fetch the new revision.
                setTimeout(() => load(keep), 900);
                return;
            }
        } catch (e) {
            S.saveErrors = ["Couldn't reach swkit: " + e.message + '.'];
        }
        S.saving = false;
        renderAll();
    }

    // localProblems catches what the server would reject, before a round trip.
    function localProblems() {
        const out = [];
        const names = allDeviceNames();
        const seen = new Set();
        S.scenes.forEach(function (s) {
            const label = 'Scene "' + (s.name || '(unnamed)') + '"';
            if (!s.name.trim()) out.push('A scene has no name.');
            else if (s.name.indexOf(':') >= 0) out.push(label + ': names cannot contain ":".');
            else if (names.has(s.name) || seen.has(s.name)) out.push(label + ': another device or scene already uses this name.');
            seen.add(s.name);
            const stNames = new Set();
            s.states.forEach(function (st) {
                if (!st.name.trim()) out.push(label + ': a state has no name.');
                else if (stNames.has(st.name)) out.push(label + ': two states are named "' + st.name + '".');
                stNames.add(st.name);
                st.moments.forEach(function (m) {
                    if (!m.power && !m.bri) return;
                    const t = targetType(s, m.device);
                    if (!t) out.push(label + ', state "' + st.name + '": "' + m.device + '" is not available (removed, renamed, or a scene defined later).');
                    else if (m.bri && t !== 'dimmable_light') out.push(label + ', state "' + st.name + '": "' + m.device + '" has no brightness control.');
                });
            });
        });
        return out;
    }

    async function runState(scene, stateIdx) {
        if (S.running) return;
        S.running = true;
        renderAll();
        try {
            const resp = await fetch('/api/scenes/run', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ scene: scene.savedName, state: stateIdx }),
            });
            const data = await resp.json().catch(() => ({}));
            if (resp.ok && data.ok) toast(scene.savedName + ': ' + scene.states[stateIdx].name + ' is running.');
            else toast("Couldn't run it: " + (data.error || 'HTTP ' + resp.status) + '.', 'error');
        } catch (e) {
            toast("Couldn't reach swkit: " + e.message + '.', 'error');
        } finally {
            S.running = false;
            renderAll();
        }
    }

    // ---------- edits ----------

    function changed() {
        renderAll();
    }

    function uniqueName(base, taken) {
        if (!taken.has(base)) return base;
        for (let i = 2; ; i++) if (!taken.has(base + ' ' + i)) return base + ' ' + i;
    }

    function addScene() {
        const taken = allDeviceNames();
        S.scenes.forEach(s => taken.add(s.name));
        const s = {
            id: uid(), name: uniqueName('New scene', taken), disableHomekit: false, orig: null, savedName: null, baseSig: '',
            states: [{ id: uid(), name: 'Off', moments: [], raw: [] }, { id: uid(), name: 'On', moments: [], raw: [] }],
        };
        S.scenes.push(s);
        selectScene(s.id);
        changed();
        focusLater('#ss-scene-name');
    }

    function duplicateScene(s) {
        const taken = allDeviceNames();
        S.scenes.forEach(x => taken.add(x.name));
        const copy = {
            id: uid(), name: uniqueName(s.name + ' copy', taken), disableHomekit: s.disableHomekit, orig: null, savedName: null, baseSig: '',
            states: s.states.map(st => ({
                id: uid(), name: st.name,
                moments: st.moments.map(m => Object.assign(deepCopy(m), { id: uid() })),
                raw: st.raw.map(r => ({ id: uid(), text: r.text })),
            })),
        };
        S.scenes.push(copy);
        selectScene(copy.id);
        changed();
    }

    // referencesTo lists button relations and other scenes' steps that target name.
    function referencesTo(name) {
        const refs = [];
        (S.config.Buttons || []).forEach(b => (b.ControlDevices || []).forEach(cd => {
            if (cd.DeviceName === name) refs.push('button "' + b.Name + '" (' + String(cd.EventType).replace(/_/g, ' ') + ')');
        }));
        S.scenes.forEach(s => s.states.forEach(st => {
            if (st.moments.some(m => m.device === name)) refs.push('scene "' + s.name + '", state "' + st.name + '"');
        }));
        return refs;
    }

    function deleteScene(s) {
        const refs = referencesTo(s.name);
        const msg = 'Delete the scene "' + s.name + '"?' +
            (refs.length ? '\n\nThese will stop controlling it:\n• ' + refs.join('\n• ') : '') +
            '\n\nNothing changes on swkit until you save.';
        if (!confirm(msg)) return;
        (S.config.Buttons || []).forEach(b => { b.ControlDevices = (b.ControlDevices || []).filter(cd => cd.DeviceName !== s.name); });
        S.scenes.forEach(x => x.states.forEach(st => { st.moments = st.moments.filter(m => m.device !== s.name); }));
        S.scenes = S.scenes.filter(x => x !== s);
        if (S.scenes.length) selectScene(S.scenes[0].id); else S.sel = { scene: null, state: null, device: null, moment: null };
        changed();
    }

    function renameScene(s, name) {
        name = name.trim();
        if (name === s.name) return null;
        if (!name) return 'Give the scene a name.';
        if (name.indexOf(':') >= 0) return 'Names cannot contain ":".';
        if (allDeviceNames().has(name) || S.scenes.some(x => x !== s && x.name === name)) return 'Another device or scene already uses this name.';
        const old = s.name;
        (S.config.Buttons || []).forEach(b => (b.ControlDevices || []).forEach(cd => { if (cd.DeviceName === old) cd.DeviceName = name; }));
        S.scenes.forEach(x => x.states.forEach(st => st.moments.forEach(m => { if (m.device === old) m.device = name; })));
        s.name = name;
        try { history.replaceState(null, '', '/scenes?scene=' + encodeURIComponent(name)); } catch (e) { /* ignore */ }
        return null;
    }

    function addState(s) {
        const taken = new Set(s.states.map(st => st.name));
        const st = { id: uid(), name: uniqueName('State ' + s.states.length, taken), moments: [], raw: [] };
        s.states.push(st);
        S.sel.state = st.id;
        S.sel.device = null;
        changed();
        focusLater('#ss-state-name');
    }

    function duplicateState(s, st) {
        const taken = new Set(s.states.map(x => x.name));
        const copy = {
            id: uid(), name: uniqueName(st.name + ' copy', taken),
            moments: st.moments.map(m => Object.assign(deepCopy(m), { id: uid() })),
            raw: st.raw.map(r => ({ id: uid(), text: r.text })),
        };
        s.states.splice(s.states.indexOf(st) + 1, 0, copy);
        S.sel.state = copy.id;
        changed();
    }

    function moveState(s, st, dir) {
        const i = s.states.indexOf(st), j = i + dir;
        if (j < 0 || j >= s.states.length) return;
        s.states[i] = s.states[j];
        s.states[j] = st;
        changed();
    }

    function deleteState(s, st) {
        if (st.moments.length || st.raw.length) {
            if (!confirm('Delete the state "' + st.name + '" and its ' + (st.moments.length + st.raw.length) + ' step(s)?')) return;
        }
        const i = s.states.indexOf(st);
        s.states.splice(i, 1);
        S.sel.state = s.states.length ? s.states[Math.max(0, i - 1)].id : null;
        S.sel.device = null;
        changed();
    }

    // fillOffState makes state 0 switch off everything the other states turn on.
    function fillOffState(s) {
        const off = s.states[0];
        const used = [];
        s.states.slice(1).forEach(st => st.moments.forEach(m => { if (used.indexOf(m.device) < 0) used.push(m.device); }));
        let added = 0;
        used.forEach(function (dev) {
            if (!targetType(s, dev)) return;
            let m = off.moments.find(x => x.device === dev && x.delay === 0);
            if (m && m.power === 'off') return;
            if (!m) { m = newMoment(dev, 0); off.moments.push(m); }
            m.power = 'off';
            m.bri = null;
            added++;
        });
        toast(added ? 'Added ' + added + ' device(s) to switch off.' : 'Everything this scene uses is already switched off here.');
        changed();
    }

    function includeDevice(st, t) {
        const m = newMoment(t.name, 0);
        m.power = 'on';
        st.moments.push(m);
        S.sel.device = t.name;
        S.sel.moment = m.id;
        changed();
    }

    function addLaterMoment(st, device) {
        const mine = st.moments.filter(m => m.device === device);
        const last = mine.reduce((a, m) => Math.max(a, m.delay), 0);
        const lastM = mine.find(m => m.delay === last);
        const m = newMoment(device, last > 0 ? last + 300000 : 300000);
        m.power = lastM && lastM.power === 'off' ? 'on' : 'off';
        st.moments.push(m);
        S.sel.moment = m.id;
        changed();
    }

    function removeDevice(st, device) {
        st.moments = st.moments.filter(m => m.device !== device);
        S.sel.device = null;
        S.sel.moment = null;
        changed();
    }

    function removeMoment(st, m) {
        st.moments = st.moments.filter(x => x !== m);
        if (!st.moments.some(x => x.device === m.device)) S.sel.device = null;
        changed();
    }

    function setDelay(m, ms) {
        ms = clamp(Math.round(ms), 0, MAX_DELAY_MS);
        if (ms === m.delay) return;
        m.delay = ms;
        m.delayRaw = null;
    }

    // ---------- summaries ----------

    function powerLabel(verb, type) {
        if (type === 'scene') return { on: 'Start', off: 'Off', toggle: 'Next state' }[verb];
        return { on: 'On', off: 'Off', toggle: 'Toggle' }[verb];
    }

    function briLabel(b) {
        if (!b) return '';
        if (b.verb === 'brightness') return b.level + '%';
        return (b.verb === 'brightness_up' ? '+' : '−') + b.level + '%';
    }

    function momentLabel(m, type) {
        const parts = [];
        if (m.power) parts.push(powerLabel(m.power, type));
        if (m.bri) parts.push(briLabel(m.bri));
        return parts.join(' · ') || '—';
    }

    function momentKind(m) {
        if (m.power === 'off') return 'off';
        if (m.power === 'toggle') return 'toggle';
        if (m.power === 'on') return 'on';
        if (m.bri) return m.bri.verb === 'brightness' ? 'on' : 'bri';
        return 'none';
    }

    function summaryNodes(scene, st) {
        if (!st.moments.length && !st.raw.length) {
            return [h('span', { class: 'ss-muted' }, 'This state does nothing yet. Tap a device below to add it.')];
        }
        const groups = new Map();
        st.moments.slice().sort((a, b) => a.delay - b.delay || a.order - b.order).forEach(function (m) {
            if (!m.power && !m.bri) return;
            if (!groups.has(m.delay)) groups.set(m.delay, []);
            groups.get(m.delay).push(m);
        });
        const out = [];
        groups.forEach(function (ms, delay) {
            out.push(h('span', { class: 'ss-sum-when' + (delay ? ' later' : '') }, delay ? 'after ' + fmtDelayHuman(delay) : 'At start'));
            ms.forEach(function (m, i) {
                const t = targetType(scene, m.device);
                out.push(h('span', { class: 'ss-sum-item', 'data-kind': momentKind(m) },
                    h('b', null, m.device), ' ', momentLabel(m, t).toLowerCase() + (i < ms.length - 1 ? ',' : '')));
            });
        });
        if (st.raw.length) out.push(h('span', { class: 'ss-sum-when warn' }, st.raw.length + ' unrecognised'));
        return out;
    }

    // ---------- live state ----------

    function liveDevice(name) {
        if (!S.live || !S.live.devices) return null;
        return S.live.devices.find(d => d.name === name) || null;
    }

    function activeStateIndex(scene) {
        const d = liveDevice(scene.savedName);
        return d && d.type === 'scene' ? (d.scene_state_index || 0) : null;
    }

    function updateLive() {
        if (!S.root) return;
        S.root.querySelectorAll('[data-live-scene]').forEach(function (el) {
            const sc = S.scenes.find(s => s.id === el.getAttribute('data-live-scene'));
            if (!sc) return;
            const idx = activeStateIndex(sc);
            const st = idx === null || sceneDirty(sc) ? null : sc.states[idx];
            el.textContent = st ? st.name : '';
            el.hidden = !st;
        });
        S.root.querySelectorAll('[data-live-state]').forEach(function (el) {
            const sc = curScene();
            const idx = sc ? activeStateIndex(sc) : null;
            el.hidden = !(sc && !sceneDirty(sc) && idx === parseInt(el.getAttribute('data-live-state'), 10));
        });
        S.root.querySelectorAll('[data-live-dev]').forEach(function (el) {
            const d = liveDevice(el.getAttribute('data-live-dev'));
            el.setAttribute('data-now', !d ? 'unknown' : d.type === 'scene' ? ((d.scene_state_index || 0) ? 'on' : 'off') : d.is_on ? 'on' : 'off');
            el.title = !d ? '' : d.type === 'scene' ? 'Now: ' + ((d.scene_state_names || [])[d.scene_state_index || 0] || '') : 'Now: ' + (d.is_on ? 'on' : 'off') + (d.brightness ? ' · ' + d.brightness + '%' : '');
        });
    }

    // ---------- rendering ----------

    function renderAll() {
        if (!S.root) return;
        const root = S.root;
        const scroll = root.querySelector('.ss-lanes') ? root.querySelector('.ss-lanes').scrollLeft : 0;
        root.textContent = '';

        if (!S.loaded) {
            root.appendChild(h('div', { class: 'ss-empty' },
                S.loadError ? h('div', null, h('b', null, "Couldn't load scenes. "), S.loadError, ' ', h('button', { class: 'ss-btn', onclick: () => load() }, 'Retry'))
                    : 'Loading scenes…'));
            return;
        }

        root.appendChild(renderTop());
        const body = h('div', { class: 'ss-body' });
        body.appendChild(renderRail());
        const sc = curScene();
        body.appendChild(sc ? renderEditor(sc) : renderNoScene());
        root.appendChild(body);
        root.appendChild(h('div', { id: 'ss-savebar-slot' }));
        renderSaveBar();
        updateLive();
        layoutTimeline();
        const lanes = root.querySelector('.ss-lanes');
        if (lanes) lanes.scrollLeft = scroll;
    }

    function renderTop() {
        return h('div', { class: 'ss-top' },
            h('div', null,
                h('h2', { class: 'ss-title' }, 'Scenes'),
                h('p', { class: 'ss-sub' }, 'Pick what each device does when a scene state starts, and when.')),
            h('button', { class: 'ss-btn primary', onclick: addScene }, icon('plus'), 'New scene'));
    }

    function renderRail() {
        const rail = h('nav', { class: 'ss-rail', 'aria-label': 'Scenes' });
        S.scenes.forEach(function (s) {
            const devs = new Set();
            s.states.forEach(st => st.moments.forEach(m => devs.add(m.device)));
            rail.appendChild(h('button', {
                class: 'ss-card' + (s.id === S.sel.scene ? ' sel' : ''),
                'aria-current': s.id === S.sel.scene ? 'true' : null,
                onclick: () => { selectScene(s.id); renderAll(); },
            },
                h('span', { class: 'ss-card-ico' }, icon('scene')),
                h('span', { class: 'ss-card-text' },
                    h('span', { class: 'ss-card-name' }, s.name || '(unnamed)', sceneDirty(s) ? h('span', { class: 'ss-dot', title: 'Unsaved changes' }) : null),
                    h('span', { class: 'ss-card-meta' }, s.states.length + ' states · ' + devs.size + (devs.size === 1 ? ' device' : ' devices'))),
                h('span', { class: 'ss-live', 'data-live-scene': s.id, hidden: true })));
        });
        if (!S.scenes.length) rail.appendChild(h('p', { class: 'ss-muted ss-rail-empty' }, 'No scenes yet.'));
        return rail;
    }

    function renderNoScene() {
        return h('section', { class: 'ss-main ss-welcome' },
            icon('scene', 'big'),
            h('h3', null, 'Create your first scene'),
            h('p', null, 'A scene switches several devices at once, like “Evening” or “Movie”. Each scene has states you cycle through; the first one is “Off”. Steps can wait, so a light can fade down later or switch itself off.'),
            h('button', { class: 'ss-btn primary', onclick: addScene }, icon('plus'), 'New scene'));
    }

    function renderEditor(sc) {
        const main = h('section', { class: 'ss-main', 'aria-label': 'Scene editor' });

        // Header: name + scene actions
        const nameErr = h('div', { class: 'ss-field-err', hidden: true });
        const nameInput = h('input', { id: 'ss-scene-name', class: 'ss-name', value: sc.name, 'aria-label': 'Scene name', autocomplete: 'off', spellcheck: 'false' });
        const commitName = function () {
            const err = renameScene(sc, nameInput.value);
            if (err) { nameErr.textContent = err; nameErr.hidden = false; return; }
            nameErr.hidden = true;
            if (nameInput.value.trim() !== nameInput.defaultValue) changed();
        };
        nameInput.addEventListener('blur', commitName);
        nameInput.addEventListener('keydown', e => { if (e.key === 'Enter') nameInput.blur(); if (e.key === 'Escape') { nameInput.value = sc.name; nameInput.blur(); } });
        nameInput.defaultValue = sc.name;

        const refs = referencesTo(sc.name).filter(r => r.indexOf('button') === 0);
        main.appendChild(h('header', { class: 'ss-head' },
            h('div', { class: 'ss-head-main' },
                nameInput, nameErr,
                h('div', { class: 'ss-head-meta' },
                    sc.orig ? null : h('span', { class: 'ss-pill new' }, 'New'),
                    refs.length ? h('span', null, 'Triggered by ' + refs.length + (refs.length === 1 ? ' button' : ' buttons')) : h('span', null, 'No button triggers it yet'))),
            h('div', { class: 'ss-head-actions' },
                h('button', { class: 'ss-btn ghost', title: 'Duplicate scene', onclick: () => duplicateScene(sc) }, icon('copy'), h('span', { class: 'ss-hide-sm' }, 'Duplicate')),
                h('button', { class: 'ss-btn ghost danger', title: 'Delete scene', onclick: () => deleteScene(sc) }, icon('trash'), h('span', { class: 'ss-hide-sm' }, 'Delete')))));

        // State tabs
        const tabs = h('div', { class: 'ss-states', role: 'tablist', 'aria-label': 'States' });
        sc.states.forEach(function (st, i) {
            const n = st.moments.filter(m => m.power || m.bri).length;
            tabs.appendChild(h('button', {
                class: 'ss-state' + (st.id === S.sel.state ? ' sel' : ''),
                role: 'tab', 'aria-selected': st.id === S.sel.state ? 'true' : 'false',
                onclick: () => { S.sel.state = st.id; S.sel.device = null; S.sel.moment = null; renderAll(); },
            },
                i === 0 ? icon('off', 'sm') : h('span', { class: 'ss-state-idx' }, i),
                h('span', { class: 'ss-state-name' }, st.name || '(unnamed)'),
                h('span', { class: 'ss-state-count', title: n + (n === 1 ? ' device step' : ' device steps') }, n),
                h('span', { class: 'ss-live-dot', 'data-live-state': i, hidden: true, title: 'Active now' })));
        });
        tabs.appendChild(h('button', { class: 'ss-state add', onclick: () => addState(sc), 'aria-label': 'Add state' }, icon('plus', 'sm'), h('span', { class: 'ss-hide-sm' }, 'State')));
        main.appendChild(tabs);

        const st = curState();
        if (!st) {
            main.appendChild(h('p', { class: 'ss-muted ss-pad' }, 'This scene has no states. Add one to start.'));
            return main;
        }
        const stIdx = sc.states.indexOf(st);

        // State toolbar
        const stErr = h('div', { class: 'ss-field-err', hidden: true });
        const stName = h('input', { id: 'ss-state-name', class: 'ss-input', value: st.name, 'aria-label': 'State name', autocomplete: 'off' });
        stName.addEventListener('blur', function () {
            const v = stName.value.trim();
            if (v === st.name) { stErr.hidden = true; return; }
            if (!v) { stErr.textContent = 'Give the state a name.'; stErr.hidden = false; return; }
            if (sc.states.some(x => x !== st && x.name === v)) { stErr.textContent = 'Another state already has this name.'; stErr.hidden = false; return; }
            st.name = v;
            changed();
        });
        stName.addEventListener('keydown', e => { if (e.key === 'Enter') stName.blur(); });

        const dirty = sceneDirty(sc);
        const canRun = S.meta.scene_run && sc.savedName && !dirty;
        const runBtn = h('button', {
            class: 'ss-btn run', disabled: !canRun || S.running,
            title: !S.meta.scene_run ? 'Running scenes is not available on this server'
                : !sc.savedName ? 'Save the scene first, then try it'
                    : dirty ? 'Save your changes first; Run uses the saved scene' : 'Run this state on the real devices now',
            onclick: () => runState(sc, stIdx),
        }, icon('play'), S.running ? 'Running…' : 'Run');

        main.appendChild(h('div', { class: 'ss-statebar' },
            h('div', { class: 'ss-statebar-name' }, stName, stErr),
            h('div', { class: 'ss-statebar-actions' },
                h('button', { class: 'ss-icon-btn', title: 'Move earlier', 'aria-label': 'Move state earlier', disabled: stIdx === 0, onclick: () => moveState(sc, st, -1) }, icon('left')),
                h('button', { class: 'ss-icon-btn', title: 'Move later', 'aria-label': 'Move state later', disabled: stIdx === sc.states.length - 1, onclick: () => moveState(sc, st, 1) }, icon('right')),
                h('button', { class: 'ss-icon-btn', title: 'Duplicate state', 'aria-label': 'Duplicate state', onclick: () => duplicateState(sc, st) }, icon('copy')),
                h('button', { class: 'ss-icon-btn danger', title: 'Delete state', 'aria-label': 'Delete state', onclick: () => deleteState(sc, st) }, icon('trash')),
                runBtn)));

        if (stIdx === 0) {
            main.appendChild(h('div', { class: 'ss-hint' },
                icon('off', 'sm'),
                h('span', null, 'The first state is the scene’s “Off”: buttons and the control page switch to it to turn the scene off.'),
                h('button', { class: 'ss-btn small', onclick: () => fillOffState(sc) }, 'Switch off everything this scene uses')));
        } else if (sc.states.length < 2) {
            main.appendChild(h('div', { class: 'ss-hint warn' }, 'A scene needs at least two states (Off plus one more) to be switched on.'));
        }

        main.appendChild(h('div', { class: 'ss-summary', 'aria-label': 'Summary' }, summaryNodes(sc, st)));

        const work = h('div', { class: 'ss-work' + (S.sel.device ? ' has-insp' : '') });
        const col = h('div', { class: 'ss-col' });
        col.appendChild(renderBoard(sc, st));
        col.appendChild(renderTimeline(sc, st));
        if (st.raw.length) col.appendChild(renderRaw(st));
        work.appendChild(col);
        work.appendChild(renderInspector(sc, st));
        main.appendChild(work);
        return main;
    }

    function renderBoard(sc, st) {
        const board = h('section', { class: 'ss-board', 'aria-label': 'Devices' });
        board.appendChild(h('div', { class: 'ss-sec-head' },
            h('h3', null, 'Devices'),
            h('span', { class: 'ss-muted' }, 'Tap to add or edit')));
        const targets = targetsFor(sc);
        const groups = [
            { title: 'Lights', types: ['light', 'dimmable_light', 'color_light'] },
            { title: 'Outlets', types: ['outlet'] },
            { title: 'Scenes', types: ['scene'] },
        ];
        // Devices a step refers to but that no longer exist still get a tile.
        const missing = [];
        st.moments.forEach(m => { if (!targets.some(t => t.name === m.device) && missing.indexOf(m.device) < 0) missing.push(m.device); });

        groups.forEach(function (g) {
            const list = targets.filter(t => g.types.indexOf(t.type) >= 0);
            if (!list.length) return;
            board.appendChild(h('div', { class: 'ss-group-title' }, g.title));
            board.appendChild(h('div', { class: 'ss-tiles' }, list.map(t => renderTile(sc, st, t))));
        });
        if (missing.length) {
            board.appendChild(h('div', { class: 'ss-group-title warn' }, 'Not available'));
            board.appendChild(h('div', { class: 'ss-tiles' }, missing.map(n => renderTile(sc, st, { name: n, type: 'missing' }))));
        }
        if (!targets.length) board.appendChild(h('p', { class: 'ss-muted' }, 'No lights or outlets are configured yet. Add them on the Schema tab first.'));
        return board;
    }

    function renderTile(sc, st, t) {
        const mine = st.moments.filter(m => m.device === t.name && (m.power || m.bri));
        const now = mine.filter(m => m.delay === 0);
        const later = mine.filter(m => m.delay > 0);
        const included = mine.length > 0;
        let fx = 'Not in this state', kind = 'none', level = null;
        if (now.length) {
            fx = now.map(m => momentLabel(m, t.type)).join(' · ');
            kind = momentKind(now[0]);
            const b = now.find(m => m.bri && m.bri.verb === 'brightness');
            if (b) level = b.bri.level;
        } else if (later.length) {
            fx = 'Later';
            kind = 'later';
        }
        const sel = S.sel.device === t.name;
        const tile = h('button', {
            class: 'ss-tile' + (included ? ' in' : '') + (sel ? ' sel' : '') + (t.type === 'missing' ? ' missing' : ''),
            'data-kind': kind,
            'aria-pressed': included ? 'true' : 'false',
            'aria-label': t.name + ': ' + fx + (later.length ? ', ' + later.length + ' later step(s)' : ''),
            onclick: function () {
                if (t.type === 'missing') { S.sel.device = t.name; S.sel.moment = null; renderAll(); return; }
                if (!included) includeDevice(st, t);
                else { S.sel.device = t.name; S.sel.moment = (now[0] || later[0]).id; renderAll(); }
            },
        },
            h('span', { class: 'ss-tile-top' },
                icon(t.type === 'missing' ? 'light' : t.type),
                h('span', { class: 'ss-now', 'data-live-dev': t.name, title: '' })),
            h('span', { class: 'ss-tile-name' }, t.name),
            h('span', { class: 'ss-tile-fx' }, fx),
            later.length ? h('span', { class: 'ss-tile-later' }, icon('clock', 'xs'), later.length === 1 ? fmtDelayShort(later[0].delay) : later.length + '×') : null,
            level !== null ? h('span', { class: 'ss-tile-bar', style: { '--lvl': level + '%' } }) : null);
        return tile;
    }

    // ---------- timeline ----------

    function scaleMax(st) {
        const maxDelay = st.moments.reduce((a, m) => Math.max(a, m.delay), 0) / 1000;
        const want = Math.max(60, maxDelay * 1.35);
        const nice = TICK_S.find(t => t >= want);
        return nice || 86400;
    }

    function toFrac(sec, T) { return Math.log(1 + sec / SCALE_K) / Math.log(1 + T / SCALE_K); }
    function fromFrac(f, T) { return SCALE_K * (Math.pow(1 + T / SCALE_K, clamp(f, 0, 1)) - 1); }
    function snapSec(sec) {
        let best = SNAP_S[0];
        SNAP_S.forEach(s => { if (Math.abs(s - sec) < Math.abs(best - sec)) best = s; });
        return best;
    }

    function renderTimeline(sc, st) {
        const sec = h('section', { class: 'ss-timeline', 'aria-label': 'Timeline' });
        const devs = [];
        st.moments.slice().sort((a, b) => a.order - b.order).forEach(m => { if (devs.indexOf(m.device) < 0) devs.push(m.device); });
        const total = st.moments.reduce((a, m) => Math.max(a, m.delay), 0);
        sec.appendChild(h('div', { class: 'ss-sec-head' },
            h('h3', null, 'Timeline'),
            h('span', { class: 'ss-muted' }, !devs.length ? '' : total ? 'Runs for ' + fmtDelayHuman(total) + '. Drag a step to change when it happens.' : 'Everything happens at once. Add a later step from a device.')));
        if (!devs.length) {
            sec.appendChild(h('div', { class: 'ss-lanes-empty' }, 'Steps appear here on a time line once you add devices.'));
            return sec;
        }
        const T = scaleMax(st);
        const lanes = h('div', { class: 'ss-lanes', 'data-t': T });
        lanes.appendChild(h('div', { class: 'ss-lane ss-ruler' },
            h('span', { class: 'ss-lane-label' }),
            h('span', { class: 'ss-track', 'data-ruler': '1' })));
        devs.forEach(function (dev) {
            const t = targetType(sc, dev);
            const track = h('span', { class: 'ss-track' });
            st.moments.filter(m => m.device === dev).sort((a, b) => a.delay - b.delay).forEach(function (m, i) {
                track.appendChild(renderChip(sc, st, m, t, T, i));
            });
            lanes.appendChild(h('div', { class: 'ss-lane' + (S.sel.device === dev ? ' sel' : '') },
                h('button', { class: 'ss-lane-label', onclick: () => { S.sel.device = dev; S.sel.moment = null; renderAll(); } },
                    icon(t || 'light', 'xs'), h('span', null, dev)),
                track));
        });
        sec.appendChild(lanes);
        return sec;
    }

    function renderChip(sc, st, m, type, T, i) {
        const chip = h('button', {
            class: 'ss-chip' + (S.sel.moment === m.id ? ' sel' : ''),
            'data-kind': momentKind(m),
            'data-f': toFrac(m.delay / 1000, T),
            style: { zIndex: 2 + i },
            'aria-label': m.device + ': ' + momentLabel(m, type) + ', ' + (m.delay ? 'after ' + fmtDelayHuman(m.delay) : 'at start') + '. Drag or use arrow keys to change the time.',
        }, h('span', { class: 'ss-chip-label' }, momentLabel(m, type)), m.delay ? h('span', { class: 'ss-chip-time' }, fmtDelayShort(m.delay)) : null);

        let start = null;
        chip.addEventListener('pointerdown', function (e) {
            if (e.button !== 0) return;
            start = { x: e.clientX, moved: false, track: chip.parentElement.getBoundingClientRect() };
            chip.setPointerCapture(e.pointerId);
        });
        chip.addEventListener('pointermove', function (e) {
            if (!start) return;
            if (!start.moved && Math.abs(e.clientX - start.x) < 5) return;
            start.moved = true;
            chip.classList.add('dragging');
            const f = clamp((e.clientX - start.track.left - TRACK_PAD) / Math.max(start.track.width - 2 * TRACK_PAD, 1), 0, 1);
            const secs = snapSec(fromFrac(f, T));
            start.secs = secs;
            chip.dataset.f = toFrac(secs, T);
            placeChip(chip);
            const tl = chip.querySelector('.ss-chip-time') || chip.appendChild(h('span', { class: 'ss-chip-time' }));
            tl.textContent = secs ? fmtDelayShort(secs * 1000) : '0';
        });
        const end = function () {
            if (!start) return;
            const s = start;
            start = null;
            chip.classList.remove('dragging');
            if (s.moved && s.secs !== undefined) {
                setDelay(m, s.secs * 1000);
                S.sel.device = m.device;
                S.sel.moment = m.id;
                changed();
            } else {
                S.sel.device = m.device;
                S.sel.moment = m.id;
                renderAll();
            }
        };
        chip.addEventListener('pointerup', end);
        chip.addEventListener('pointercancel', function () { start = null; chip.classList.remove('dragging'); renderAll(); });
        chip.addEventListener('click', function (e) {
            if (e.detail !== 0) return; // pointer clicks are handled on pointerup
            S.sel.device = m.device;
            S.sel.moment = m.id;
            renderAll();
        });
        chip.addEventListener('keydown', function (e) {
            if (e.key !== 'ArrowLeft' && e.key !== 'ArrowRight') return;
            e.preventDefault();
            const cur = m.delay / 1000;
            let next;
            if (e.key === 'ArrowRight') next = SNAP_S.find(s => s > cur + 0.001);
            else next = SNAP_S.slice().reverse().find(s => s < cur - 0.001);
            if (next === undefined) return;
            setDelay(m, next * 1000);
            S.sel.device = m.device;
            S.sel.moment = m.id;
            changed();
            const again = S.root.querySelector('.ss-chip.sel');
            if (again) again.focus();
        });
        return chip;
    }

    // Chips are centred on their time; TRACK_PAD keeps the start and end of
    // the scale clear of the track edges. The ruler uses the same mapping.
    const TRACK_PAD = 26;
    function trackX(f, width) { return f * (width - 2 * TRACK_PAD) + TRACK_PAD; }
    function placeChip(chip) {
        const f = parseFloat(chip.dataset.f) || 0;
        const W = chip.parentElement.clientWidth, cw = chip.offsetWidth;
        chip.style.left = clamp(trackX(f, W) - cw / 2, 0, Math.max(W - cw, 0)) + 'px';
    }

    // layoutTimeline positions chips and draws ruler ticks once the lanes
    // are in the DOM, so it can thin ticks to the real track width.
    function layoutTimeline() {
        const lanes = S.root && S.root.querySelector('.ss-lanes');
        if (!lanes) return;
        lanes.querySelectorAll('.ss-chip').forEach(placeChip);
        const ruler = lanes.querySelector('[data-ruler]');
        const T = parseFloat(lanes.getAttribute('data-t'));
        const width = ruler.getBoundingClientRect().width;
        if (!width) return;
        ruler.textContent = '';
        let lastX = -1e9;
        TICK_S.filter(t => t <= T).forEach(function (t) {
            const x = trackX(toFrac(t, T), width);
            if (x - lastX < 46 && t !== T) return;
            lastX = x;
            ruler.appendChild(h('span', { class: 'ss-tick', style: { left: x + 'px' } }, t === 0 ? 'start' : fmtDelayShort(t * 1000)));
        });
    }

    function renderRaw(st) {
        const box = h('section', { class: 'ss-raw' });
        box.appendChild(h('div', { class: 'ss-sec-head' },
            h('h3', null, 'Unrecognised steps'),
            h('span', { class: 'ss-muted' }, 'Kept exactly as written. Fix or remove them.')));
        st.raw.forEach(function (r) {
            const input = h('input', { class: 'ss-input mono', value: r.text, 'aria-label': 'Step text' });
            input.addEventListener('blur', function () {
                if (input.value === r.text) return;
                const p = parseStep(input.value);
                if (p) {
                    st.raw = st.raw.filter(x => x !== r);
                    const m = newMoment(p.device, p.delay);
                    if (isBri(p.verb)) m.bri = { verb: p.verb, level: p.level }; else m.power = p.verb;
                    st.moments.push(m);
                } else {
                    r.text = input.value;
                }
                changed();
            });
            box.appendChild(h('div', { class: 'ss-raw-row' }, input,
                h('button', { class: 'ss-icon-btn danger', 'aria-label': 'Remove step', onclick: () => { st.raw = st.raw.filter(x => x !== r); changed(); } }, icon('trash'))));
        });
        return box;
    }

    // ---------- inspector ----------

    function renderInspector(sc, st) {
        const dev = S.sel.device;
        const insp = h('aside', { class: 'ss-insp' + (dev ? ' open' : ''), 'aria-label': 'Device steps' });
        if (!dev) {
            insp.appendChild(h('div', { class: 'ss-insp-empty' },
                icon('clock', 'big'),
                h('p', null, 'Select a device to choose what it does and when.'),
                h('p', { class: 'ss-muted' }, 'Steps can wait up to 24 hours, so a scene can dim lights later or switch them off on its own.')));
            return insp;
        }
        const type = targetType(sc, dev);
        const moments = st.moments.filter(m => m.device === dev).sort((a, b) => a.delay - b.delay || a.order - b.order);

        insp.appendChild(h('div', { class: 'ss-insp-head' },
            icon(type || 'light'),
            h('div', null,
                h('div', { class: 'ss-insp-title' }, dev),
                h('div', { class: 'ss-muted' }, type ? TYPE_LABEL[type] : 'Not available in this scene')),
            h('button', { class: 'ss-icon-btn', 'aria-label': 'Close', onclick: () => { S.sel.device = null; S.sel.moment = null; renderAll(); } }, icon('close'))));

        if (!type) {
            insp.appendChild(h('p', { class: 'ss-hint warn' }, 'This device no longer exists, was renamed, or is a scene defined after this one. Saving will fail until you remove its steps.'));
        }

        moments.forEach(function (m) { insp.appendChild(renderMoment(sc, st, m, type)); });

        insp.appendChild(h('div', { class: 'ss-insp-foot' },
            type ? h('button', { class: 'ss-btn', onclick: () => addLaterMoment(st, dev) }, icon('clock', 'sm'), 'Add a later step') : null,
            h('button', { class: 'ss-btn ghost danger', onclick: () => removeDevice(st, dev) }, 'Remove from this state')));
        return insp;
    }

    function segmented(options, value, onPick, label) {
        return h('div', { class: 'ss-seg', role: 'radiogroup', 'aria-label': label },
            options.map(o => h('button', {
                class: 'ss-seg-btn' + (o.value === value ? ' on' : ''),
                role: 'radio', 'aria-checked': o.value === value ? 'true' : 'false',
                'data-kind': o.kind || '',
                onclick: () => onPick(o.value),
            }, o.label)));
    }

    function renderMoment(sc, st, m, type) {
        const card = h('div', { class: 'ss-moment' + (S.sel.moment === m.id ? ' sel' : ''), 'data-kind': momentKind(m) });

        // When
        const presetBtns = [h('button', {
            class: 'ss-chipbtn' + (m.delay === 0 ? ' on' : ''),
            onclick: () => { setDelay(m, 0); S.sel.moment = m.id; changed(); },
        }, 'At start')].concat(DELAY_PRESETS_S.map(s => h('button', {
            class: 'ss-chipbtn' + (m.delay === s * 1000 ? ' on' : ''),
            onclick: () => { setDelay(m, s * 1000); S.sel.moment = m.id; changed(); },
        }, fmtDelayShort(s * 1000))));
        const custom = h('input', {
            class: 'ss-input ss-delay-input', value: m.delay ? fmtDelayGo(m.delay) : '', placeholder: 'e.g. 1m30s',
            'aria-label': 'Custom delay', inputmode: 'text', autocomplete: 'off',
        });
        const delayErr = h('div', { class: 'ss-field-err', hidden: true });
        custom.addEventListener('blur', function () {
            const raw = custom.value.trim();
            if (!raw) { if (m.delay !== 0) { setDelay(m, 0); changed(); } return; }
            const ms = parseDelayInput(raw);
            if (ms === null) { delayErr.textContent = 'Use a time like 45s, 10m, 1h30m.'; delayErr.hidden = false; return; }
            if (ms > MAX_DELAY_MS) { delayErr.textContent = 'The longest wait is 24 h.'; delayErr.hidden = false; return; }
            delayErr.hidden = true;
            if (ms !== m.delay) { setDelay(m, ms); S.sel.moment = m.id; changed(); }
        });
        custom.addEventListener('keydown', e => { if (e.key === 'Enter') custom.blur(); });

        card.appendChild(h('div', { class: 'ss-moment-head' },
            h('span', { class: 'ss-moment-when' }, icon(m.delay ? 'clock' : 'play', 'xs'), m.delay ? 'After ' + fmtDelayHuman(m.delay) : 'At start'),
            h('button', { class: 'ss-icon-btn sm danger', 'aria-label': 'Remove this step', onclick: () => removeMoment(st, m) }, icon('trash'))));
        card.appendChild(h('div', { class: 'ss-row' }, h('span', { class: 'ss-label' }, 'When'),
            h('div', { class: 'ss-chips' }, presetBtns, custom), delayErr));

        // Power
        if (type) {
            const powerOpts = type === 'scene'
                ? [{ value: null, label: 'Leave' }, { value: 'on', label: 'Start', kind: 'on' }, { value: 'off', label: 'Off', kind: 'off' }, { value: 'toggle', label: 'Next state', kind: 'toggle' }]
                : [{ value: null, label: 'Leave' }, { value: 'on', label: 'On', kind: 'on' }, { value: 'off', label: 'Off', kind: 'off' }, { value: 'toggle', label: 'Toggle', kind: 'toggle' }];
            card.appendChild(h('div', { class: 'ss-row' }, h('span', { class: 'ss-label' }, type === 'scene' ? 'Scene' : 'Power'),
                segmented(powerOpts, m.power, function (v) {
                    m.power = v;
                    m.briFirst = null;
                    if (v === 'off' && m.bri && m.bri.verb !== 'brightness') m.bri = null;
                    S.sel.moment = m.id;
                    changed();
                }, 'Power')));
        }

        // Brightness (dimmable lights only)
        if (type === 'dimmable_light') {
            const cur = m.bri ? m.bri.verb : null;
            card.appendChild(h('div', { class: 'ss-row' }, h('span', { class: 'ss-label' }, 'Brightness'),
                segmented([
                    { value: null, label: 'Leave' },
                    { value: 'brightness', label: 'Set' },
                    { value: 'brightness_up', label: 'Brighter' },
                    { value: 'brightness_down', label: 'Dimmer' },
                ], cur, function (v) {
                    if (!v) m.bri = null;
                    else if (!m.bri) m.bri = { verb: v, level: v === 'brightness' ? 60 : 20 };
                    else { m.bri.verb = v; if (v === 'brightness' && m.bri.level < 5) m.bri.level = 60; }
                    m.briFirst = null;
                    S.sel.moment = m.id;
                    changed();
                }, 'Brightness')));
            if (m.bri) card.appendChild(renderLevel(m));
        }

        card.addEventListener('focusin', function () {
            if (S.sel.moment === m.id) return;
            S.sel.moment = m.id;
            S.root.querySelectorAll('.ss-moment.sel, .ss-chip.sel').forEach(el => el.classList.remove('sel'));
            card.classList.add('sel');
        });
        return card;
    }

    function renderLevel(m) {
        const abs = m.bri.verb === 'brightness';
        const max = abs ? 100 : 50;
        const val = h('output', { class: 'ss-level-val' }, briLabel(m.bri));
        const range = h('input', {
            type: 'range', class: 'ss-range', min: abs ? 1 : 1, max: max, step: 1, value: clamp(m.bri.level, 1, max),
            'aria-label': abs ? 'Brightness' : 'Change by',
        });
        range.style.setProperty('--pct', (clamp(m.bri.level, 1, max) / max * 100) + '%');
        range.addEventListener('input', function () {
            m.bri.level = parseInt(range.value, 10);
            val.textContent = briLabel(m.bri);
            range.style.setProperty('--pct', (m.bri.level / max * 100) + '%');
            // Keep the tile, chip and summary in step without rebuilding the slider.
            refreshTexts();
        });
        range.addEventListener('change', changed);
        const presets = (abs ? [10, 25, 50, 75, 100] : [5, 10, 20, 30, 50]).map(p => h('button', {
            class: 'ss-chipbtn' + (m.bri.level === p ? ' on' : ''),
            onclick: () => { m.bri.level = p; changed(); },
        }, (abs ? '' : m.bri.verb === 'brightness_up' ? '+' : '−') + p + '%'));
        return h('div', { class: 'ss-row' }, h('span', { class: 'ss-label' }, abs ? 'Level' : 'By'),
            h('div', { class: 'ss-level' }, range, val),
            h('div', { class: 'ss-chips' }, presets));
    }

    // refreshTexts updates labels that depend on a slider while it is being
    // dragged; a full render would replace the slider under the pointer.
    let refreshTimer = null;
    function refreshTexts() {
        clearTimeout(refreshTimer);
        refreshTimer = setTimeout(function () {
            const sc = curScene(), st = curState();
            if (!sc || !st) return;
            const sum = S.root.querySelector('.ss-summary');
            if (sum) { sum.textContent = ''; append(sum, summaryNodes(sc, st)); }
            renderSaveBar();
        }, 60);
    }

    // ---------- save bar & toasts ----------

    function renderSaveBar() {
        const slot = S.root && S.root.querySelector('#ss-savebar-slot');
        if (!slot) return;
        slot.textContent = '';
        const dirty = isDirty();
        if (!dirty && !S.saveErrors.length) return;
        const bar = h('div', { class: 'ss-savebar' + (S.saveErrors.length ? ' err' : '') });
        if (S.saveErrors.length) {
            bar.appendChild(h('div', { class: 'ss-save-errs' },
                h('b', null, S.conflict ? 'Not saved: the config changed elsewhere.' : 'Not saved. Fix these first:'),
                h('ul', null, S.saveErrors.map(e => h('li', null, e)))));
        }
        const deleted = (S.config.Scenes || []).filter(o => !S.scenes.some(s => s.orig === o)).length;
        const n = S.scenes.filter(sceneDirty).length + deleted;
        bar.appendChild(h('div', { class: 'ss-save-row' },
            h('span', { class: 'ss-save-msg' }, dirty ? (n > 1 ? n + ' scenes changed' : 'Unsaved changes') : ''),
            S.conflict
                ? h('button', { class: 'ss-btn primary', onclick: () => load() }, 'Reload')
                : [
                    h('button', { class: 'ss-btn ghost', disabled: S.saving || !dirty, onclick: discard }, 'Discard'),
                    h('button', { class: 'ss-btn primary', disabled: S.saving || !dirty, onclick: save }, S.saving ? 'Saving…' : 'Save & apply'),
                ]));
        slot.appendChild(bar);
    }

    function discard() {
        if (!confirm('Discard all unsaved scene changes?')) return;
        const sc = curScene();
        load(sc ? { scene: sc.orig ? sc.orig.Name : null, stateIdx: sc.states.indexOf(curState()) } : null);
    }

    let toastBox = null;
    function toast(msg, level) {
        if (!toastBox) {
            toastBox = h('div', { class: 'ss-toasts', role: 'status', 'aria-live': 'polite' });
            document.body.appendChild(toastBox);
        }
        const t = h('div', { class: 'ss-toast' + (level ? ' ' + level : '') }, msg);
        toastBox.appendChild(t);
        while (toastBox.children.length > 3) toastBox.firstChild.remove();
        setTimeout(() => { t.classList.add('out'); setTimeout(() => t.remove(), 300); }, level === 'error' ? 6000 : 3500);
    }

    function focusLater(sel) {
        setTimeout(function () {
            const el = S.root && S.root.querySelector(sel);
            if (el) { el.focus(); el.select && el.select(); }
        }, 0);
    }

    // ---------- page lifecycle (called by app.js) ----------

    function mount() {
        const page = document.getElementById('page-content');
        if (!page) return false;
        if (S.root && page.contains(S.root)) return true;
        page.textContent = '';
        S.root = h('div', { class: 'ss' });
        page.appendChild(S.root);
        renderAll();
        return true;
    }

    let resizeTimer = null;
    window.addEventListener('resize', function () {
        clearTimeout(resizeTimer);
        resizeTimer = setTimeout(function () { if (S.root && document.contains(S.root)) layoutTimeline(); }, 120);
    });
    window.addEventListener('beforeunload', function (e) {
        if (S.root && document.contains(S.root) && isDirty()) { e.preventDefault(); e.returnValue = ''; }
    });

    window.swkitScenes = {
        // render is called on every /api/state poll while /scenes is open. It
        // never rebuilds the editor; it only refreshes the live badges.
        render: function (state) {
            S.live = state;
            if (!mount()) return;
            if (!S.loaded && !S.loading && !S.loadError) load();
            else updateLive();
        },
        hasUnsavedChanges: isDirty,
        discardSilently: function () {
            S.loaded = false;
            S.scenes = [];
            S.root = null;
            S.saveErrors = [];
            S.sel = { scene: null, state: null, device: null, moment: null };
        },
    };
})();
