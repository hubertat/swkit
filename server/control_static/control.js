/* swkit control ui */
'use strict';

const ENDPOINT = window.__ENDPOINT || '/control';
const POLL_MS = 2000;

// --- Theme ---

function initTheme() {
    const stored = localStorage.getItem('swkit-theme');
    const preferred = window.matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark';
    const theme = stored || preferred;
    document.documentElement.setAttribute('data-theme', theme);
}

function toggleTheme() {
    const current = document.documentElement.getAttribute('data-theme');
    const next = current === 'light' ? 'dark' : 'light';
    document.documentElement.setAttribute('data-theme', next);
    localStorage.setItem('swkit-theme', next);
}

// --- State ---

let devices = [];
const pendingSet = new Set(); // indices being toggled
let pollTimer = null;

// --- API ---

async function fetchDevices() {
    const resp = await fetch(ENDPOINT + '/api/devices');
    if (!resp.ok) throw new Error('HTTP ' + resp.status);
    return resp.json();
}

async function toggleDevice(index) {
    if (pendingSet.has(index)) return;
    pendingSet.add(index);
    updateCardPending(index, true);
    try {
        const resp = await fetch(ENDPOINT + '/api/devices/' + index + '/toggle', { method: 'POST' });
        if (!resp.ok) throw new Error('HTTP ' + resp.status);
        const result = await resp.json();
        // Update local state optimistically
        const dev = devices.find(d => d.index === index);
        if (dev) dev.is_on = result.is_on;
        updateCardState(index);
    } catch (e) {
        console.error('toggle failed', e);
    } finally {
        pendingSet.delete(index);
        updateCardPending(index, false);
    }
}

// --- Poll ---

function setStatus(ok) {
    const dot = document.getElementById('status-indicator');
    if (!dot) return;
    dot.className = 'status-dot ' + (ok ? 'ok' : 'err');
    dot.title = ok ? 'Connected' : 'Error fetching state';
}

async function poll() {
    try {
        const data = await fetchDevices();
        devices = data;
        renderDevices();
        setStatus(true);
    } catch (e) {
        console.error('poll error', e);
        setStatus(false);
    }
}

function startPolling() {
    poll();
    pollTimer = setInterval(poll, POLL_MS);
}

function stopPolling() {
    if (pollTimer) { clearInterval(pollTimer); pollTimer = null; }
}

document.addEventListener('visibilitychange', () => {
    if (document.hidden) { stopPolling(); } else { startPolling(); }
});

// --- Render ---

const TYPE_ORDER = ['light', 'color_light', 'outlet', 'button'];
const TYPE_LABELS = {
    'light': 'Lights',
    'color_light': 'Color Lights',
    'outlet': 'Outlets',
    'button': 'Buttons',
};

function renderDevices() {
    const content = document.getElementById('content');
    if (!content) return;

    if (devices.length === 0) {
        content.innerHTML = '<p class="empty">No devices found.</p>';
        return;
    }

    // Group by type
    const groups = {};
    for (const dev of devices) {
        const t = dev.type || 'unknown';
        if (!groups[t]) groups[t] = [];
        groups[t].push(dev);
    }

    const html = [];
    for (const type of TYPE_ORDER) {
        const group = groups[type];
        if (!group || group.length === 0) continue;
        html.push('<section class="section">');
        html.push('<h2 class="section-title">' + escHtml(TYPE_LABELS[type] || type) + '</h2>');
        html.push('<div class="device-grid">');
        for (const dev of group) {
            html.push(renderCard(dev));
        }
        html.push('</div></section>');
    }

    // Unknown types
    for (const type of Object.keys(groups)) {
        if (TYPE_ORDER.includes(type)) continue;
        const group = groups[type];
        html.push('<section class="section">');
        html.push('<h2 class="section-title">' + escHtml(type) + '</h2>');
        html.push('<div class="device-grid">');
        for (const dev of group) html.push(renderCard(dev));
        html.push('</div></section>');
    }

    content.innerHTML = html.join('');

    // Attach toggle listeners
    content.querySelectorAll('.toggle-btn[data-index]').forEach(btn => {
        btn.addEventListener('click', () => {
            const idx = parseInt(btn.dataset.index, 10);
            toggleDevice(idx);
        });
    });
}

function renderCard(dev) {
    const isOn = dev.is_on;
    const isFaulty = dev.is_faulty;
    const notHealthy = !dev.is_healthy;
    const controllable = dev.controllable;
    const isPending = pendingSet.has(dev.index);

    let cardClass = 'device-card type-' + dev.type;
    if (isOn) cardClass += ' is-on';
    if (isFaulty) cardClass += ' is-faulty';
    if (notHealthy) cardClass += ' not-healthy';

    let badgeHtml = '';
    if (isFaulty) {
        badgeHtml = '<span class="badge badge-faulty">fault</span>';
    } else if (isOn) {
        badgeHtml = '<span class="badge badge-on">on</span>';
    } else {
        badgeHtml = '<span class="badge badge-off">off</span>';
    }

    let btnHtml = '';
    if (controllable) {
        let btnClass = 'toggle-btn';
        if (isOn) btnClass += ' is-on';
        if (isPending) btnClass += ' pending';
        const disabled = isPending || !dev.is_healthy ? 'disabled' : '';
        const label = isOn ? 'Turn Off' : 'Turn On';
        btnHtml = `<button class="${btnClass}" data-index="${dev.index}" ${disabled}>${label}</button>`;
    }

    let eventHtml = '';
    if (dev.type === 'button' && dev.last_event_type) {
        eventHtml = '<span class="last-event">Last: ' + escHtml(dev.last_event_type) + '</span>';
    }

    return `<div class="${cardClass}" id="card-${dev.index}">
  <div class="device-name">${escHtml(dev.name)}</div>
  <div class="device-meta">${badgeHtml}${eventHtml}</div>
  ${btnHtml}
</div>`;
}

// Fine-grained update helpers (avoid full re-render during toggle)

function updateCardPending(index, pending) {
    const btn = document.querySelector('.toggle-btn[data-index="' + index + '"]');
    if (!btn) return;
    if (pending) {
        btn.classList.add('pending');
        btn.disabled = true;
    } else {
        btn.classList.remove('pending');
        btn.disabled = false;
    }
}

function updateCardState(index) {
    const dev = devices.find(d => d.index === index);
    if (!dev) return;
    const card = document.getElementById('card-' + index);
    if (!card) return;

    // Update card border
    card.classList.toggle('is-on', dev.is_on);

    // Update badge
    const badge = card.querySelector('.badge');
    if (badge) {
        badge.className = 'badge ' + (dev.is_on ? 'badge-on' : 'badge-off');
        badge.textContent = dev.is_on ? 'on' : 'off';
    }

    // Update button label/class
    const btn = card.querySelector('.toggle-btn');
    if (btn) {
        btn.classList.toggle('is-on', dev.is_on);
        btn.textContent = dev.is_on ? 'Turn Off' : 'Turn On';
    }
}

// --- Util ---

function escHtml(str) {
    return String(str)
        .replace(/&/g, '&amp;')
        .replace(/</g, '&lt;')
        .replace(/>/g, '&gt;')
        .replace(/"/g, '&quot;');
}

// --- Init ---

document.addEventListener('DOMContentLoaded', () => {
    initTheme();
    document.getElementById('theme-toggle').addEventListener('click', toggleTheme);
    startPolling();
});
