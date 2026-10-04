/* swkit control ui
 *
 * State model
 * -----------
 * Drivers report device state asynchronously (a Shelly output only changes
 * once the device publishes its new status over MQTT, a Wago output on the
 * next Modbus poll), so a read straight after a command often still returns
 * the old value. Rendering every poll verbatim makes a tile flick
 * on → off → on. Instead each command creates an "intent":
 *
 *   sending      request in flight; the tile shows the requested state, hatched
 *   waiting      server accepted it; still hatched until a poll that started
 *                after the acknowledgement reports the requested value
 *   unconfirmed  no matching report within CONFIRM_MS; the tile falls back to
 *                the reported value with an amber "not confirmed" marker
 *
 * Poll results never override an active sending/waiting intent, so the
 * displayed state only moves forward. Independently, the whole page is
 * flagged stale when no poll has succeeded for STALE_MS, and devices whose
 * state the server could not read are shown as unknown, never as "off".
 */
'use strict';

const ENDPOINT = window.__ENDPOINT || '/control';
const VARIANTS = window.__VARIANTS || ['tiles', 'list', 'compact'];

const POLL_MS = 2000;          // idle poll cadence
const POLL_FAST_MS = 500;      // cadence while a command awaits confirmation
const POLL_TIMEOUT_MS = 5000;
const CMD_TIMEOUT_MS = 8000;
const CONFIRM_MS = 6000;       // how long a device has to report a commanded state
const UNCONFIRMED_HOLD_MS = 15000;
const STALE_MS = 6000;         // no successful poll for this long → page is stale
const RESUME_GRACE_MS = 1500;  // after returning to the tab, before showing the banner
const LONG_PRESS_MS = 450;

// ---------- small helpers ----------

function h(tag, attrs, ...children) {
    const el = document.createElement(tag);
    if (attrs) {
        for (const [k, v] of Object.entries(attrs)) {
            if (v === null || v === undefined || v === false) continue;
            if (k === 'class') el.className = v;
            else if (k === 'html') el.innerHTML = v; // trusted static markup only
            else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
            else el.setAttribute(k, v === true ? '' : v);
        }
    }
    for (const c of children.flat()) {
        if (c === null || c === undefined || c === false) continue;
        el.append(c instanceof Node ? c : document.createTextNode(String(c)));
    }
    return el;
}

function storeGet(key) { try { return localStorage.getItem(key); } catch (e) { return null; } }
function storeSet(key, val) { try { localStorage.setItem(key, val); } catch (e) { /* ignore */ } }

function haptic() { try { navigator.vibrate && navigator.vibrate(8); } catch (e) { /* ignore */ } }

function clamp(v, lo, hi) { return Math.max(lo, Math.min(hi, v)); }

function ago(ms) {
    const s = Math.max(0, Math.round(ms / 1000));
    if (s < 5) return 'just now';
    if (s < 60) return s + 's ago';
    const m = Math.floor(s / 60);
    if (m < 60) return m + ' min ago';
    const hr = Math.floor(m / 60);
    if (hr < 24) return hr + ' h ago';
    return Math.floor(hr / 24) + ' d ago';
}

function clock(ts) {
    return new Date(ts).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit', second: '2-digit' });
}

function eventLabel(ev) {
    const s = String(ev || '').replace(/_/g, ' ');
    return s.charAt(0).toUpperCase() + s.slice(1);
}

async function request(path, opts = {}) {
    const ctrl = new AbortController();
    const timer = setTimeout(() => ctrl.abort(), opts.timeout || CMD_TIMEOUT_MS);
    try {
        const resp = await fetch(ENDPOINT + path, {
            method: opts.method || 'GET',
            headers: opts.body ? { 'Content-Type': 'application/json' } : undefined,
            body: opts.body ? JSON.stringify(opts.body) : undefined,
            cache: 'no-store',
            signal: ctrl.signal,
        });
        if (!resp.ok) {
            const text = (await resp.text().catch(() => '')).trim();
            throw new Error(text || ('HTTP ' + resp.status));
        }
        return await resp.json();
    } catch (e) {
        if (e.name === 'AbortError') throw new Error('no response from the controller');
        if (e instanceof TypeError) throw new Error('controller unreachable');
        throw e;
    } finally {
        clearTimeout(timer);
    }
}

// ---------- icons ----------

const ICON = {
    light: '<svg viewBox="0 0 24 24"><path d="M9 18h6M10 21h4"/><path d="M12 3a6 6 0 0 0-3.6 10.8c.7.6 1.1 1.3 1.1 2.2h5c0-.9.4-1.6 1.1-2.2A6 6 0 0 0 12 3z"/></svg>',
    color_light: '<svg viewBox="0 0 24 24"><path d="M12 3a9 9 0 1 0 0 18c1 0 1.6-.8 1.6-1.6 0-.5-.2-.9-.5-1.2-.3-.3-.5-.7-.5-1.2 0-.9.7-1.6 1.6-1.6H16a5 5 0 0 0 5-5C21 6.5 17 3 12 3z"/><circle cx="7.5" cy="11" r="1.2"/><circle cx="10.5" cy="7" r="1.2"/><circle cx="15.5" cy="7.5" r="1.2"/></svg>',
    dimmable_light: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></svg>',
    outlet: '<svg viewBox="0 0 24 24"><rect x="4" y="4" width="16" height="16" rx="4.5"/><path d="M9.5 9.5v2M14.5 9.5v2M10 15.5h4"/></svg>',
    scene: '<svg viewBox="0 0 24 24"><path d="M11 3l1.7 4.6L17.3 9.3l-4.6 1.7L11 15.6 9.3 11 4.7 9.3l4.6-1.7z"/><path d="M18 14l.8 2.2L21 17l-2.2.8L18 20l-.8-2.2L15 17l2.2-.8z"/></svg>',
    button: '<svg viewBox="0 0 24 24"><rect x="4" y="4" width="16" height="16" rx="4.5"/><circle cx="12" cy="12" r="3.2"/></svg>',
    other: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="8"/></svg>',
    sun: '<svg viewBox="0 0 24 24"><circle cx="12" cy="12" r="4"/><path d="M12 2v2M12 20v2M4.9 4.9l1.4 1.4M17.7 17.7l1.4 1.4M2 12h2M20 12h2M4.9 19.1l1.4-1.4M17.7 6.3l1.4-1.4"/></svg>',
    moon: '<svg viewBox="0 0 24 24"><path d="M12 3a9 9 0 1 0 9 9 7 7 0 0 1-9-9z"/></svg>',
};

const TYPE_NAME = {
    light: 'Light',
    color_light: 'Color light',
    dimmable_light: 'Dimmable light',
    outlet: 'Outlet',
    scene: 'Scene',
    button: 'Button',
};

// Sections in display order; each lists the device types it holds.
const SECTIONS = [
    { key: 'lights', title: 'Lights', types: ['light', 'color_light', 'dimmable_light'], allOff: true },
    { key: 'outlets', title: 'Outlets', types: ['outlet'], allOff: true },
    { key: 'scenes', title: 'Scenes', types: ['scene'] },
    { key: 'inputs', title: 'Buttons', types: ['button'] },
];

// ---------- state ----------

let devices = new Map();      // index → last reported device
let snapshotAt = 0;           // Date.now() when the current snapshot arrived
let lastOkAt = 0;             // last successful poll
let lastErr = null;
let structureKey = null;     // null until the first snapshot is rendered
let variant = document.documentElement.getAttribute('data-variant') || VARIANTS[0];

const intents = new Map();    // index → intent (see header comment)
const views = new Map();      // index → { root, hit, name, sub, chip, range, rangeVal }
const sectionViews = [];      // { def, root, count, act }

let pollTimer = null;
let pollInFlight = false;
let pollQueued = false;
let resumedAt = 0;
let sheetIndex = null;
let sheetOpenedAt = 0;
let sheetSig = '';

// ---------- derived view of a device ----------

function kindOf(dev) {
    if (dev.type === 'scene') return 'scene';
    if (dev.controllable) return 'power';
    return 'input';
}

function viewOf(dev) {
    const it = intents.get(dev.index);
    const kind = kindOf(dev);
    let on = !!dev.is_on;
    let bri = dev.brightness || 0;
    let sync = 'ok';

    if (!dev.is_healthy) sync = 'offline';
    else if (dev.state_error) sync = 'unknown';

    if (it && sync !== 'offline') {
        if (it.phase === 'unconfirmed') {
            sync = 'unconfirmed';
        } else {
            sync = it.phase;
            if (it.kind === 'power' && it.target !== null) on = it.target;
            else if (it.kind === 'bri') bri = it.target;
            else if (it.kind === 'scene') on = it.target !== 0;
        }
    }
    if (sync === 'unknown') on = false;
    const busy = sync === 'sending' || sync === 'waiting' || sync === 'dragging';

    let sub;
    if (kind === 'input') {
        if (!dev.is_healthy) sub = 'Offline';
        else sub = dev.last_event_type ? eventLabel(dev.last_event_type) + ' · ' + ago(eventAge(dev)) : 'No presses yet';
    } else if (sync === 'offline') {
        sub = 'Offline';
    } else if (sync === 'unknown') {
        sub = 'State unknown';
    } else if (busy) {
        if (sync === 'waiting') sub = 'Confirming…';
        else if (it.kind === 'bri') sub = 'Dimming…';
        else if (it.kind === 'scene' || it.target === null) sub = 'Switching…';
        else sub = on ? 'Turning on…' : 'Turning off…';
    } else if (sync === 'unconfirmed') {
        sub = 'Unconfirmed';
    } else if (kind === 'scene') {
        sub = dev.scene_state || (on ? 'On' : 'Off');
    } else {
        sub = on ? 'On' : 'Off';
    }

    return { on, bri, sync, sub, kind, busy, fault: !!dev.is_faulty && sync !== 'offline' };
}

function eventAge(dev) {
    return (dev.last_event_age_ms || 0) + (Date.now() - snapshotAt);
}

function hasActiveIntent() {
    for (const it of intents.values()) {
        if (it.phase === 'sending' || it.phase === 'waiting' || it.phase === 'dragging') return true;
    }
    return false;
}

// ---------- intents ----------

function clearIntent(index, it) {
    const cur = intents.get(index);
    if (it && cur !== it) return;
    if (cur) {
        clearTimeout(cur.timer);
        clearTimeout(cur.holdTimer);
        clearTimeout(cur.debounce);
    }
    intents.delete(index);
}

function newIntent(dev, kind, target) {
    clearIntent(dev.index);
    const it = { kind, target, name: dev.name, phase: 'sending', sentAt: Date.now(), ackAt: 0 };
    intents.set(dev.index, it);
    return it;
}

function acknowledge(index, it) {
    if (intents.get(index) !== it) return;
    it.phase = 'waiting';
    it.ackAt = Date.now();
    it.timer = setTimeout(() => expire(index, it), CONFIRM_MS);
    updateDevice(index);
    schedulePoll(150);
}

function expire(index, it) {
    if (intents.get(index) !== it || it.phase !== 'waiting') return;
    it.phase = 'unconfirmed';
    it.holdTimer = setTimeout(() => { clearIntent(index, it); updateDevice(index); }, UNCONFIRMED_HOLD_MS);
    updateDevice(index);
    const dev = devices.get(index);
    if (dev) {
        const reported = dev.state_error ? 'its state is unknown' : 'it still reports ' + (it.kind === 'bri' ? (dev.brightness + '%') : (dev.is_on ? 'on' : 'off'));
        toast(dev.name + " didn't confirm the change; " + reported + '.', 'warn');
    }
}

function fail(index, it, verb, err) {
    if (intents.get(index) === it) clearIntent(index, it);
    updateDevice(index);
    toast("Couldn't " + verb + ': ' + err.message + '.', 'error');
}

// Does a snapshot taken at `pollStartedAt` confirm this intent?
function confirms(it, dev, pollStartedAt) {
    if (it.phase === 'sending' || it.phase === 'dragging') return false;
    if (it.phase === 'waiting' && pollStartedAt < it.ackAt) return false;
    if (!dev.is_healthy) return false;
    switch (it.kind) {
        case 'power':
            if (it.target === null) return !dev.state_error; // toggled from unknown: any real reading
            return !dev.state_error && !!dev.is_on === it.target;
        case 'bri':
            return Math.abs((dev.brightness || 0) - it.target) <= 1;
        case 'scene':
            return (dev.scene_index || 0) === it.target;
    }
    return false;
}

// ---------- commands ----------

function setPower(dev, target, opts = {}) {
    const index = dev.index;
    const v = viewOf(dev);
    if (v.sync === 'offline') return;

    // Explicit "set" is idempotent, so a repeated tap or a stale cache can't
    // flip the device twice. Fall back to toggle only when the current state
    // is unknown (and scenes, whose toggle cycles through states).
    let path, body, it;
    if (dev.type === 'scene') {
        const count = dev.scene_count || 2;
        const cur = intents.get(index);
        const from = cur && cur.kind === 'scene' && cur.phase !== 'unconfirmed' ? cur.target : (dev.scene_index || 0);
        const next = (from + 1) % count;
        it = newIntent(dev, 'scene', next);
        path = '/api/devices/' + index + '/toggle';
    } else if (target === null) {
        it = newIntent(dev, 'power', null);
        path = '/api/devices/' + index + '/toggle';
    } else {
        it = newIntent(dev, 'power', target);
        if (opts.seconds) {
            path = '/api/devices/' + index + '/set_for';
            body = { value: target, seconds: opts.seconds };
        } else {
            path = '/api/devices/' + index + '/set';
            body = { value: target };
        }
    }
    haptic();
    updateDevice(index);

    request(path, { method: 'POST', body })
        .then(() => {
            acknowledge(index, it);
            if (opts.seconds) toast(dev.name + (target ? ' on' : ' off') + ' for ' + durLabel(opts.seconds) + ', then back.');
        })
        .catch(e => fail(index, it, (target === null ? 'switch ' : target ? 'turn on ' : 'turn off ') + dev.name, e));
}

function tapDevice(dev) {
    const v = viewOf(dev);
    if (v.kind === 'input') { openSheet(dev.index); return; }
    if (v.sync === 'offline') { toast(dev.name + ' is offline.', 'warn'); return; }
    if (v.kind === 'scene') { setPower(dev, null); return; }
    setPower(dev, v.sync === 'unknown' ? null : !v.on);
}

// Brightness: one request in flight per device; newer values queue behind it
// so a late reply for an old value can never win.
function setBrightness(dev, value, immediate) {
    const index = dev.index;
    value = clamp(Math.round(value), 0, 100);
    let it = intents.get(index);
    if (!it || it.kind !== 'bri' || it.phase === 'unconfirmed') {
        it = newIntent(dev, 'bri', value);
    }
    clearTimeout(it.timer);
    clearTimeout(it.holdTimer);
    it.target = value;
    it.phase = it.inFlight ? 'sending' : 'dragging';
    updateDevice(index);

    clearTimeout(it.debounce);
    const send = () => {
        if (intents.get(index) !== it) return;
        if (it.inFlight) { it.queued = true; return; }
        it.inFlight = true;
        it.queued = false;
        it.phase = 'sending';
        const sent = it.target;
        updateDevice(index);
        request('/api/devices/' + index + '/set_brightness', { method: 'POST', body: { value: sent } })
            .then(() => {
                it.inFlight = false;
                if (it.queued || it.target !== sent) { send(); return; }
                acknowledge(index, it);
            })
            .catch(e => { it.inFlight = false; fail(index, it, 'set brightness of ' + dev.name, e); });
    };
    if (immediate) send(); else it.debounce = setTimeout(send, 160);
}

function allOff(def) {
    const targets = [];
    for (const dev of devices.values()) {
        if (!def.types.includes(dev.type)) continue;
        const v = viewOf(dev);
        if (v.kind === 'power' && v.on && v.sync !== 'offline') targets.push(dev);
    }
    targets.forEach(d => setPower(d, false));
}

function durLabel(s) {
    if (s >= 3600) return (s / 3600) + ' h';
    return Math.round(s / 60) + ' min';
}

// ---------- polling ----------

function schedulePoll(delay) {
    if (document.hidden) return;
    clearTimeout(pollTimer);
    pollTimer = setTimeout(poll, delay);
}

async function poll() {
    if (pollInFlight) { pollQueued = true; return; }
    pollInFlight = true;
    clearTimeout(pollTimer);
    const startedAt = Date.now();
    try {
        const data = await request('/api/devices', { timeout: POLL_TIMEOUT_MS });
        lastOkAt = Date.now();
        lastErr = null;
        applySnapshot(data, startedAt);
        beat();
    } catch (e) {
        lastErr = e;
    } finally {
        pollInFlight = false;
        renderFreshness();
        if (pollQueued) { pollQueued = false; schedulePoll(0); }
        else schedulePoll(hasActiveIntent() ? POLL_FAST_MS : POLL_MS);
    }
}

function applySnapshot(list, startedAt) {
    const key = list.map(d => d.index + ':' + d.type + ':' + d.name).join('|');
    snapshotAt = Date.now();
    const next = new Map();
    for (const d of list) next.set(d.index, d);

    // Config reloaded and indices moved: drop intents that no longer match.
    for (const [index, it] of intents) {
        const d = next.get(index);
        if (!d || d.name !== it.name) clearIntent(index);
    }

    devices = next;

    if (key !== structureKey) {
        structureKey = key;
        build();
    }

    for (const d of list) {
        const it = intents.get(d.index);
        if (it && confirms(it, d, startedAt)) {
            const wasUnconfirmed = it.phase === 'unconfirmed';
            clearIntent(d.index, it);
            flashConfirmed(d.index, wasUnconfirmed);
        }
        updateDevice(d.index);
    }
    updateSections();
    if (sheetIndex !== null) renderSheet();
}

function flashConfirmed(index, wasUnconfirmed) {
    const v = views.get(index);
    if (!v) return;
    v.root.classList.remove('confirmed');
    void v.root.offsetWidth;
    v.root.classList.add('confirmed');
    if (wasUnconfirmed) {
        const d = devices.get(index);
        if (d) toast(d.name + ' confirmed the change.');
    }
}

// ---------- freshness ----------

function freshness() {
    const now = Date.now();
    if (!lastOkAt) return { level: lastErr ? 'stale' : 'connecting', text: lastErr ? 'Offline' : 'Connecting…' };
    const age = now - lastOkAt;
    if (age <= STALE_MS) {
        if (lastErr) return { level: 'aging', text: 'Retrying…' };
        return { level: 'live', text: 'Live' };
    }
    if (now - resumedAt < RESUME_GRACE_MS && !lastErr) return { level: 'aging', text: 'Refreshing…' };
    return { level: 'stale', text: 'Updated ' + ago(age) };
}

function renderFreshness() {
    const f = freshness();
    const btn = document.getElementById('fresh');
    btn.dataset.fresh = f.level;
    document.getElementById('fresh-text').textContent = f.text;
    btn.title = lastOkAt ? 'Last update ' + clock(lastOkAt) + '. Tap to refresh.' : 'Tap to refresh';

    const stale = f.level === 'stale';
    document.body.classList.toggle('is-stale', stale);
    const banner = document.getElementById('stale-banner');
    banner.hidden = !stale;
    if (stale) {
        document.getElementById('stale-detail').textContent = lastOkAt
            ? 'Showing the state from ' + clock(lastOkAt) + ', which may be out of date.'
            : 'No device state received yet.';
    }
    if (!lastOkAt && lastErr) {
        const content = document.getElementById('content');
        if (structureKey === null) {
            content.setAttribute('aria-busy', 'false');
            content.replaceChildren(h('p', { class: 'empty' }, "Can't load devices: " + lastErr.message + '.'));
        }
    }
}

function beat() {
    const btn = document.getElementById('fresh');
    btn.classList.remove('beat');
    void btn.offsetWidth;
    btn.classList.add('beat');
}

// ---------- rendering ----------

function build() {
    const content = document.getElementById('content');
    content.setAttribute('aria-busy', 'false');
    views.clear();
    sectionViews.length = 0;

    if (devices.size === 0) {
        content.replaceChildren(h('p', { class: 'empty' }, 'No devices configured.'));
        return;
    }

    const used = new Set();
    const frag = document.createDocumentFragment();
    const defs = SECTIONS.slice();
    const known = new Set(defs.flatMap(d => d.types));
    const otherTypes = [...new Set([...devices.values()].map(d => d.type).filter(t => !known.has(t)))];
    for (const t of otherTypes) defs.push({ key: 'other-' + t, title: TYPE_NAME[t] || t, types: [t] });

    for (const def of defs) {
        const list = [...devices.values()].filter(d => def.types.includes(d.type));
        if (list.length === 0) continue;
        list.forEach(d => used.add(d.index));

        const count = h('span', { class: 'section-count' });
        const act = def.allOff ? h('button', { class: 'section-act', type: 'button', hidden: true }) : null;
        if (act) act.addEventListener('click', () => onAllOff(def, act));
        const grid = h('div', { class: 'grid' }, list.map(buildDevice));
        const root = h('section', { class: 'section', 'aria-label': def.title },
            h('div', { class: 'section-head' }, h('h2', { class: 'section-title' }, def.title), count, act),
            grid);
        sectionViews.push({ def, root, count, act });
        frag.append(root);
    }
    content.replaceChildren(frag);
}

function buildDevice(dev) {
    const kind = kindOf(dev);
    const hit = h('button', { class: 'dev-hit', type: 'button' });
    const name = h('span', { class: 'dev-name' }, dev.name);
    const sub = h('span', { class: 'dev-sub' });
    const icon = h('span', { class: 'dev-icon', 'aria-hidden': 'true', html: ICON[dev.type] || ICON.other },
        h('span', { class: 'dev-mark' }));
    const sw = h('span', { class: 'dev-switch', 'aria-hidden': 'true' });
    let chip = null, bri = null, range = null, rangeVal = null;

    if (dev.has_brightness) {
        chip = h('button', { class: 'dev-chip', type: 'button', 'aria-label': 'Brightness of ' + dev.name });
        chip.addEventListener('click', e => { e.stopPropagation(); openSheet(dev.index); });
        range = h('input', { class: 'range', type: 'range', min: 0, max: 100, step: 1, 'aria-label': 'Brightness of ' + dev.name });
        rangeVal = h('span', { class: 'val' });
        const step = (delta, label) => {
            const b = h('button', { class: 'step', type: 'button', 'aria-label': label }, delta < 0 ? '−' : '+');
            b.addEventListener('click', () => {
                const d = devices.get(dev.index);
                if (d) setBrightness(d, viewOf(d).bri + delta, true);
            });
            return b;
        };
        bindRange(range, () => devices.get(dev.index));
        bri = h('div', { class: 'dev-bri' }, step(-10, 'Dimmer'), range, step(10, 'Brighter'), rangeVal);
    }

    const root = h('div', { class: 'dev', 'data-type': dev.type, 'data-kind': kind, id: 'dev-' + dev.index },
        hit, icon, h('span', { class: 'dev-text' }, name, sub), chip, sw, bri);

    bindPress(hit, () => devices.get(dev.index));
    views.set(dev.index, { root, hit, name, sub, chip, range, rangeVal });
    return root;
}

function bindRange(range, getDev) {
    range.addEventListener('input', () => {
        const d = getDev();
        if (!d) return;
        range.dataset.active = '1';
        range.style.setProperty('--pct', range.value + '%');
        setBrightness(d, parseInt(range.value, 10), false);
    });
    range.addEventListener('change', () => {
        const d = getDev();
        delete range.dataset.active;
        if (d) setBrightness(d, parseInt(range.value, 10), true);
    });
}

// Tap toggles; long-press (or right-click) opens the detail sheet.
function bindPress(hit, getDev) {
    let timer = null, startX = 0, startY = 0, longFired = false;
    const cancel = () => { clearTimeout(timer); timer = null; };
    hit.addEventListener('pointerdown', e => {
        if (e.button !== 0) return;
        longFired = false;
        startX = e.clientX; startY = e.clientY;
        cancel();
        timer = setTimeout(() => {
            longFired = true;
            haptic();
            const d = getDev();
            if (d) openSheet(d.index);
        }, LONG_PRESS_MS);
    });
    hit.addEventListener('pointermove', e => {
        if (timer && Math.hypot(e.clientX - startX, e.clientY - startY) > 10) cancel();
    });
    ['pointerup', 'pointercancel', 'pointerleave'].forEach(t => hit.addEventListener(t, cancel));
    hit.addEventListener('contextmenu', e => {
        e.preventDefault();
        cancel();
        if (longFired) return;
        const d = getDev();
        if (d) openSheet(d.index);
    });
    hit.addEventListener('click', e => {
        if (longFired) { longFired = false; e.preventDefault(); return; }
        const d = getDev();
        if (d) tapDevice(d);
    });
}

function updateDevice(index) {
    const v = views.get(index);
    const dev = devices.get(index);
    if (!v || !dev) return;
    const s = viewOf(dev);
    const r = v.root;

    r.dataset.on = s.on ? '1' : '0';
    r.dataset.sync = s.sync;
    r.dataset.fault = s.fault ? '1' : '0';
    if (v.name.textContent !== dev.name) v.name.textContent = dev.name;
    v.sub.textContent = s.sub;

    if (s.kind === 'input') {
        r.classList.toggle('recent', !!dev.last_event_type && eventAge(dev) < 3000);
        v.hit.setAttribute('aria-label', dev.name + ', button. ' + s.sub + '. Show details');
    } else {
        v.hit.setAttribute('aria-label', dev.name + ', ' + (TYPE_NAME[dev.type] || dev.type).toLowerCase() + ', ' + s.sub);
        if (s.kind === 'power' && s.sync !== 'unknown') v.hit.setAttribute('aria-pressed', s.on ? 'true' : 'false');
        else v.hit.removeAttribute('aria-pressed');
    }
    v.hit.setAttribute('aria-disabled', s.sync === 'offline' ? 'true' : 'false');

    if (v.chip) {
        v.chip.textContent = s.sync === 'unknown' ? '—' : s.bri + '%';
    }
    if (v.range) {
        const disabled = s.sync === 'offline';
        v.range.disabled = disabled;
        if (!v.range.dataset.active) {
            v.range.value = s.bri;
            v.range.style.setProperty('--pct', s.bri + '%');
        }
        v.rangeVal.textContent = (v.range.dataset.active ? v.range.value : s.bri) + '%';
        v.root.querySelectorAll('.dev-bri .step').forEach(b => { b.disabled = disabled; });
    }
    if (sheetIndex === index) renderSheet();
}

function updateSections() {
    for (const sv of sectionViews) {
        const list = [...devices.values()].filter(d => sv.def.types.includes(d.type));
        const kind = list.length ? kindOf(list[0]) : 'input';
        if (kind === 'input') {
            sv.count.textContent = list.length;
            continue;
        }
        const on = list.filter(d => viewOf(d).on).length;
        sv.count.replaceChildren(h('b', null, on), ' of ' + list.length + ' on');
        if (sv.act) {
            sv.act.hidden = on === 0;
            if (!sv.act.classList.contains('armed')) sv.act.textContent = 'All off';
        }
    }
}

function onAllOff(def, btn) {
    if (btn.classList.contains('armed')) {
        clearTimeout(btn._t);
        btn.classList.remove('armed');
        btn.textContent = 'All off';
        allOff(def);
        return;
    }
    const n = [...devices.values()].filter(d => def.types.includes(d.type) && viewOf(d).on).length;
    btn.classList.add('armed');
    btn.textContent = 'Turn off ' + n + '?';
    btn._t = setTimeout(() => { btn.classList.remove('armed'); btn.textContent = 'All off'; }, 3000);
}

// Relative times on button tiles keep ticking between polls.
function tickInputs() {
    for (const [index, dev] of devices) {
        if (kindOf(dev) === 'input') updateDevice(index);
    }
}

// ---------- detail sheet ----------

function openSheet(index) {
    const dlg = document.getElementById('sheet');
    sheetIndex = index;
    sheetOpenedAt = Date.now();
    renderSheet(true);
    if (!dlg.open) {
        if (typeof dlg.showModal === 'function') dlg.showModal();
        else dlg.setAttribute('open', '');
    }
}

function closeSheet() {
    const dlg = document.getElementById('sheet');
    if (dlg.open) { if (typeof dlg.close === 'function') dlg.close(); else dlg.removeAttribute('open'); }
    sheetIndex = null;
}

function statusText(dev, s) {
    const it = intents.get(dev.index);
    if (freshness().level === 'stale') {
        return { sync: 'stale', title: 'Connection lost', detail: lastOkAt ? 'Last known state from ' + clock(lastOkAt) + '. It may have changed since.' : 'No state received yet.' };
    }
    switch (s.sync) {
        case 'offline': return { sync: 'offline', title: 'Device offline', detail: 'The controller cannot reach this device, so it cannot be switched right now.' };
        case 'unknown': return { sync: 'unknown', title: 'State unknown', detail: h('span', null, 'The controller could not read this device. ', h('code', null, dev.state_error || '')) };
        case 'sending': return { sync: 'sending', title: 'Sending command…', detail: 'Waiting for the controller to accept it.' };
        case 'dragging': return { sync: 'sending', title: 'Adjusting…', detail: '' };
        case 'waiting': return { sync: 'waiting', title: 'Waiting for the device to confirm', detail: 'The command was accepted ' + ago(Date.now() - it.ackAt) + '. The display updates once the device reports its new state.' };
        case 'unconfirmed': {
            const rep = it && it.kind === 'bri' ? dev.brightness + '%' : (dev.is_on ? 'on' : 'off');
            return { sync: 'unconfirmed', title: 'Not confirmed', detail: 'The device still reports ' + rep + '. The command may have been lost; try again.' };
        }
    }
    return { sync: 'ok', title: s.kind === 'input' ? 'Listening' : 'Confirmed by device', detail: 'Updated ' + ago(Date.now() - lastOkAt) + '.' };
}

function renderSheet(fresh) {
    const dev = devices.get(sheetIndex);
    if (!dev) { closeSheet(); return; }
    const s = viewOf(dev);
    document.getElementById('sheet-title').textContent = dev.name;
    document.getElementById('sheet-type').textContent = TYPE_NAME[dev.type] || dev.type;

    const st = statusText(dev, s);
    const status = document.getElementById('sheet-status');
    status.dataset.sync = st.sync;
    status.replaceChildren(h('span', { class: 'dot' }), h('div', null, h('strong', null, st.title), st.detail));

    // Rebuild the controls only when what they show changes, never on the
    // once-a-second tick: replacing a button mid-tap would swallow the tap.
    const body = document.getElementById('sheet-body');
    const sig = [dev.index, dev.name, s.on, s.sync, s.bri, dev.scene_index, dev.scene_state, dev.last_event_type,
        s.kind === 'input' ? Math.floor(eventAge(dev) / 5000) : 0].join('|');
    const activeRange = body.querySelector('.range[data-active]');
    if (activeRange && !fresh) {
        const val = body.querySelector('.bri-row .val');
        if (val) val.textContent = activeRange.value + '%';
        return;
    }
    if (!fresh && sig === sheetSig) return;
    sheetSig = sig;

    const offline = s.sync === 'offline';
    const parts = [];

    if (s.kind === 'power') {
        const known = s.sync !== 'unknown';
        const onBtn = h('button', { class: 'pick', type: 'button', 'aria-pressed': known && s.on ? 'true' : 'false', disabled: offline }, 'On');
        const offBtn = h('button', { class: 'pick off', type: 'button', 'aria-pressed': known && !s.on ? 'true' : 'false', disabled: offline }, 'Off');
        onBtn.addEventListener('click', () => setPower(dev, true));
        offBtn.addEventListener('click', () => setPower(dev, false));
        parts.push(h('div', null, h('div', { class: 'sheet-label' }, 'Power'), h('div', { class: 'pair' }, onBtn, offBtn)));

        if (dev.has_brightness) {
            const range = h('input', { class: 'range', type: 'range', min: 0, max: 100, step: 1, value: s.bri, disabled: offline, 'aria-label': 'Brightness' });
            range.style.setProperty('--pct', s.bri + '%');
            bindRange(range, () => devices.get(dev.index));
            const step = (delta) => {
                const b = h('button', { class: 'step', type: 'button', disabled: offline, 'aria-label': delta < 0 ? 'Dimmer' : 'Brighter' }, delta < 0 ? '−' : '+');
                b.addEventListener('click', () => setBrightness(dev, viewOf(dev).bri + delta, true));
                return b;
            };
            const presets = [10, 25, 50, 75, 100].map(p => {
                const b = h('button', { class: 'chip-btn' + (s.bri === p ? ' cur' : ''), type: 'button', disabled: offline }, p + '%');
                b.addEventListener('click', () => setBrightness(dev, p, true));
                return b;
            });
            parts.push(h('div', null,
                h('div', { class: 'sheet-label' }, 'Brightness'),
                h('div', { class: 'bri-row' }, step(-10), range, step(10), h('span', { class: 'val' }, s.bri + '%')),
                h('div', { class: 'chips', style: 'margin-top:8px' }, presets)));
        }

        const timedOn = !(known && s.on);
        const timers = [300, 900, 1800, 3600].map(sec => {
            const b = h('button', { class: 'chip-btn', type: 'button', disabled: offline }, durLabel(sec));
            b.addEventListener('click', () => setPower(dev, timedOn, { seconds: sec }));
            return b;
        });
        parts.push(h('div', null,
            h('div', { class: 'sheet-label' }, timedOn ? 'Turn on for' : 'Turn off for'),
            h('div', { class: 'chips' }, timers)));
    } else if (s.kind === 'scene') {
        const next = h('button', { class: 'pick', type: 'button', style: 'width:100%' }, 'Next state');
        next.addEventListener('click', () => setPower(dev, null));
        parts.push(h('div', null,
            h('div', { class: 'sheet-label' }, 'Scene'),
            h('dl', { class: 'kv', style: 'margin-bottom:10px' },
                h('dt', null, 'Active'), h('dd', null, dev.scene_state || '—'),
                h('dt', null, 'Position'), h('dd', null, ((dev.scene_index || 0) + 1) + ' of ' + (dev.scene_count || '?'))),
            next));
    } else {
        parts.push(h('dl', { class: 'kv' },
            h('dt', null, 'Last press'), h('dd', null, dev.last_event_type ? eventLabel(dev.last_event_type) : 'None yet'),
            h('dt', null, 'When'), h('dd', null, dev.last_event_type ? ago(eventAge(dev)) : '—'),
            h('dt', null, 'Health'), h('dd', null, dev.is_healthy ? 'OK' : 'Offline')));
    }

    if (dev.is_faulty) {
        parts.push(h('p', { class: 'sheet-type' }, 'HomeKit reports a fault on this device.'));
    }
    body.replaceChildren(...parts);
}

// ---------- toasts ----------

function toast(msg, level) {
    const box = document.getElementById('toasts');
    const el = h('div', { class: 'toast' + (level ? ' ' + level : '') }, msg);
    box.append(el);
    while (box.children.length > 3) box.firstChild.remove();
    setTimeout(() => { el.classList.add('out'); setTimeout(() => el.remove(), 320); }, level === 'error' ? 6000 : 4000);
}

// ---------- variant & theme ----------

function setVariant(v, push) {
    if (!VARIANTS.includes(v)) return;
    variant = v;
    document.documentElement.setAttribute('data-variant', v);
    storeSet('swkit-control-variant', v);
    document.querySelectorAll('.seg-btn').forEach(a => a.setAttribute('aria-current', a.dataset.variant === v ? 'true' : 'false'));
    if (push) history.replaceState(null, '', ENDPOINT + '/' + v);
}

function renderThemeIcon() {
    const light = document.documentElement.getAttribute('data-theme') === 'light';
    document.getElementById('theme-toggle').innerHTML = light ? ICON.moon : ICON.sun;
}

function toggleTheme() {
    const next = document.documentElement.getAttribute('data-theme') === 'light' ? 'dark' : 'light';
    document.documentElement.setAttribute('data-theme', next);
    storeSet('swkit-theme', next);
    renderThemeIcon();
}

// ---------- init ----------

document.addEventListener('visibilitychange', () => {
    if (document.hidden) {
        clearTimeout(pollTimer);
    } else {
        resumedAt = Date.now();
        renderFreshness();
        schedulePoll(0);
    }
});

document.addEventListener('DOMContentLoaded', () => {
    setVariant(variant, false);
    renderThemeIcon();

    document.querySelectorAll('.seg-btn').forEach(a => a.addEventListener('click', e => {
        e.preventDefault();
        setVariant(a.dataset.variant, true);
    }));
    document.getElementById('theme-toggle').addEventListener('click', toggleTheme);
    document.getElementById('fresh').addEventListener('click', () => schedulePoll(0));
    document.getElementById('stale-retry').addEventListener('click', () => schedulePoll(0));

    const dlg = document.getElementById('sheet');
    document.getElementById('sheet-close').addEventListener('click', closeSheet);
    dlg.addEventListener('close', () => { sheetIndex = null; sheetSig = ''; });
    // Backdrop tap closes; ignore the release of the long-press that opened it.
    dlg.addEventListener('click', e => { if (e.target === dlg && Date.now() - sheetOpenedAt > 500) closeSheet(); });

    setInterval(() => { renderFreshness(); tickInputs(); if (sheetIndex !== null) renderSheet(); }, 1000);
    poll();
});
