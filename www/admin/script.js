// ── state ─────────────────────────────────────────────────────────────────
let allGroups = [];
let selectedUserId = null;

// ── auth redirect ─────────────────────────────────────────────────────────
async function redirectToAuth() {
    const res = await fetch('/api/config').catch(() => null);
    if (res && res.ok) {
        const data = await res.json().catch(() => ({}));
        if (data.auth_url) { window.location.href = data.auth_url; return; }
    }
    document.body.innerHTML = '<p style="text-align:center;margin-top:40vh;color:#2d6385">Not authenticated. Please log in.</p>';
}

// ── api helper ────────────────────────────────────────────────────────────
async function api(method, path, body) {
    const res = await fetch('/api/' + path, {
        method,
        headers: body ? { 'Content-Type': 'application/json' } : {},
        body: body ? JSON.stringify(body) : undefined,
    });
    if (res.status === 401) { redirectToAuth(); return null; }
    if (res.status === 403) {
        document.body.innerHTML = '<p style="text-align:center;margin-top:40vh;color:#c0392b">Access denied — admin group required.</p>';
        return null;
    }
    return res.ok ? res.json() : null;
}

// ── menu view switching ───────────────────────────────────────────────────
document.querySelectorAll('.menuContainer button').forEach(btn => {
    btn.addEventListener('click', () => {
        document.querySelectorAll('.menuContainer button').forEach(b => b.classList.remove('active'));
        document.querySelectorAll('.view').forEach(v => v.classList.remove('active'));
        btn.classList.add('active');
        document.getElementById('view-' + btn.dataset.view).classList.add('active');
        if (btn.dataset.view === 'users') loadUsersView();
        if (btn.dataset.view === 'routes') { loadRoutes(); loadSettings(); }
        if (btn.dataset.view === 'metrics') loadMetrics();
    });
});

// ── init ──────────────────────────────────────────────────────────────────
document.addEventListener('DOMContentLoaded', async () => {
    const res = await fetch('/api/auth/me').catch(() => null);
    if (!res || !res.ok) { redirectToAuth(); return; }
    loadUsersView();
});

async function loadUsersView() {
    await Promise.all([loadUsers(), loadInvites(), loadGroups()]);
}

// ── users ─────────────────────────────────────────────────────────────────
async function loadUsers() {
    const data = await api('GET', 'admin/users');
    if (!data) return;
    const list = document.getElementById('userItems');
    list.innerHTML = '';
    (data.users || []).forEach(u => {
        const el = document.createElement('div');
        el.className = 'item' + (u.id === selectedUserId ? ' selected' : '');
        el.dataset.uid = u.id;
        const groupTags = (u.groups || []).map(g => `<span class="tag">${g.name}</span>`).join('');
        el.innerHTML = `
            <div class="itemMain">${u.username}</div>
            <div class="tags">${groupTags}</div>
            <button class="delBtn" title="Delete">×</button>
        `;
        el.querySelector('.delBtn').addEventListener('click', e => {
            e.stopPropagation();
            deleteUser(u.id, u.username);
        });
        el.addEventListener('click', () => selectUser(u));
        list.appendChild(el);
    });
}

async function deleteUser(id, name) {
    if (!confirm(`Delete user "${name}"?`)) return;
    await api('DELETE', 'admin/users?id=' + id);
    if (selectedUserId === id) clearUserSelection();
    loadUsers();
}

function selectUser(user) {
    selectedUserId = user.id;
    document.querySelectorAll('#userItems .item').forEach(el => {
        el.classList.toggle('selected', Number(el.dataset.uid) === user.id);
    });
    showUserDetail(user);
}

async function showUserDetail(user) {
    const panel = document.getElementById('userDetailPanel');
    const groupsPanel = document.getElementById('groupsPanel');
    groupsPanel.style.display = 'none';
    panel.style.display = '';

    document.getElementById('userDetailName').textContent = user.username;

    const list = document.getElementById('userGroupItems');
    list.innerHTML = '';
    (user.groups || []).forEach(g => {
        const el = document.createElement('div');
        el.className = 'item';
        el.innerHTML = `
            <div class="itemMain">${g.name}</div>
            <div class="itemSub">${g.description || ''}</div>
            <button class="delBtn" title="Remove">×</button>
        `;
        el.querySelector('.delBtn').addEventListener('click', () =>
            removeFromGroup(user.id, g.id)
        );
        list.appendChild(el);
    });

    const sel = document.getElementById('groupAssignSelect');
    sel.innerHTML = '<option value="">Add to group…</option>';
    const userGroupIds = new Set((user.groups || []).map(g => g.id));
    allGroups.forEach(g => {
        if (!userGroupIds.has(g.id)) {
            const opt = document.createElement('option');
            opt.value = g.id;
            opt.textContent = g.name;
            sel.appendChild(opt);
        }
    });
}

function clearUserSelection() {
    selectedUserId = null;
    document.getElementById('userDetailPanel').style.display = 'none';
    document.getElementById('groupsPanel').style.display = '';
    document.querySelectorAll('#userItems .item').forEach(el => el.classList.remove('selected'));
}

async function addUserToGroup() {
    const gid = parseInt(document.getElementById('groupAssignSelect').value);
    if (!gid || !selectedUserId) return;
    await api('POST', 'admin/users/groups', { user_id: selectedUserId, group_id: gid });
    loadUsers();
    const data = await api('GET', 'admin/users');
    if (data) {
        const u = (data.users || []).find(u => u.id === selectedUserId);
        if (u) showUserDetail(u);
    }
}

async function removeFromGroup(uid, gid) {
    await api('DELETE', `admin/users/groups?user_id=${uid}&group_id=${gid}`);
    loadUsers();
    const data = await api('GET', 'admin/users');
    if (data) {
        const u = (data.users || []).find(u => u.id === uid);
        if (u) showUserDetail(u);
    }
}

// ── invites ───────────────────────────────────────────────────────────────
async function loadInvites() {
    const data = await api('GET', 'admin/invites');
    if (!data) return;
    const list = document.getElementById('inviteItems');
    list.innerHTML = '';
    (data.invites || []).forEach(inv => {
        const el = document.createElement('div');
        el.className = 'item';
        const exp = new Date(inv.expires_at).toLocaleString(undefined, { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' });
        el.innerHTML = `
            <div style="flex:1;min-width:0">
                <div class="itemMain">${inv.description || '(no description)'}</div>
                <div class="itemSub">expires ${exp}</div>
            </div>
            <span class="tag${inv.used ? ' used' : ''}">${inv.used ? 'used' : 'active'}</span>
            ${!inv.used ? `<button class="delBtn" title="Delete">×</button>` : ''}
        `;
        el.querySelector('.delBtn')?.addEventListener('click', () => deleteInvite(inv.id));
        list.appendChild(el);
    });
}

async function createInvite() {
    const description = document.getElementById('inviteDesc').value.trim();
    const hours = parseInt(document.getElementById('inviteHours').value) || 24;
    const data = await api('POST', 'admin/invites', { description, hours });
    if (!data) return;
    document.getElementById('inviteDesc').value = '';
    const box = document.getElementById('newInviteCode');
    box.style.display = '';
    box.textContent = data.code;
    loadInvites();
}

async function deleteInvite(id) {
    await api('DELETE', 'admin/invites?id=' + id);
    loadInvites();
}

// ── groups ────────────────────────────────────────────────────────────────
async function loadGroups() {
    const data = await api('GET', 'admin/groups');
    if (!data) return;
    allGroups = data.groups || [];
    const list = document.getElementById('groupItems');
    list.innerHTML = '';
    allGroups.forEach(g => {
        const el = document.createElement('div');
        el.className = 'item';
        const delBtn = g.name === 'admin'
            ? `<span class="tag" title="Protected system group" style="opacity:.5;cursor:default">protected</span>`
            : `<button class="delBtn" title="Delete">×</button>`;
        el.innerHTML = `
            <div class="itemMain">${g.name}</div>
            <div class="itemSub">${g.description || ''}</div>
            ${delBtn}
        `;
        if (g.name !== 'admin') {
            el.querySelector('.delBtn').addEventListener('click', () => deleteGroup(g.id, g.name));
        }
        list.appendChild(el);
    });
}

async function createGroup() {
    const name = document.getElementById('groupName').value.trim();
    if (!name) return;
    const desc = document.getElementById('groupDesc').value.trim();
    await api('POST', 'admin/groups', { name, description: desc });
    document.getElementById('groupName').value = '';
    document.getElementById('groupDesc').value = '';
    loadGroups();
}

async function deleteGroup(id, name) {
    if (!confirm(`Delete group "${name}"?`)) return;
    await api('DELETE', 'admin/groups?id=' + id);
    loadGroups();
    loadUsers();
}

// ── routes ────────────────────────────────────────────────────────────────
async function loadRoutes() {
    const [routeData, groupData] = await Promise.all([
        api('GET', 'admin/routes'),
        api('GET', 'admin/groups'),
    ]);
    if (!routeData) return;
    const groups = groupData?.groups || [];
    const list = document.getElementById('routeItems');
    list.innerHTML = '';

    (routeData.routes || []).forEach(r => {
        const wrapper = document.createElement('div');
        wrapper.className = 'item';
        wrapper.style.cssText = 'flex-direction:column;align-items:stretch;cursor:default;';

        const activeGroups = r.allowed_groups
            ? r.allowed_groups.split(',').map(id => groups.find(g => String(g.id) === id.trim())?.name).filter(Boolean)
            : [];
        const groupHint = activeGroups.length
            ? activeGroups.map(n => `<span class="tag">${n}</span>`).join('')
            : '<span class="tag">public</span>';

        const typeBadge   = `<span class="badge badge-${r.type || 'proxy'}">${r.type || 'proxy'}</span>`;
        const sourceBadge = `<span class="badge badge-${r.source}">${r.source}</span>`;
        const delBtn = r.source === 'ui'
            ? `<button class="delBtn" title="Delete route">×</button>`
            : '';

        const header = document.createElement('div');
        header.style.cssText = 'display:flex;align-items:center;gap:8px;';
        header.innerHTML = `
            <div style="flex:1;min-width:0">
                <div class="itemMain">${r.url}</div>
                <div class="itemSub">→ ${r.target}</div>
            </div>
            <div class="tags">${groupHint}</div>
            ${typeBadge}${sourceBadge}
            ${delBtn}
            <button class="editBtn" style="flex-shrink:0;font-size:11px;padding:0 10px;height:24px">Edit</button>
        `;

        if (r.source === 'ui') {
            header.querySelector('.delBtn').addEventListener('click', e => {
                e.stopPropagation();
                deleteRoute(r.id, r.url);
            });
        }

        const editPanel = buildRouteEditPanel(r, groups);
        editPanel.style.display = 'none';
        header.querySelector('.editBtn').addEventListener('click', () => {
            editPanel.style.display = editPanel.style.display === 'none' ? '' : 'none';
        });

        wrapper.appendChild(header);
        wrapper.appendChild(editPanel);
        list.appendChild(wrapper);
    });
}

function buildRouteEditPanel(route, groups) {
    const panel = document.createElement('div');
    panel.className = 'routeEdit';

    const isTcp = route.type === 'tcp';

    const targetRow = route.source === 'ui' ? `
        <div class="routeEditRow">
            <label>Backend</label>
            <input type="text" class="targetInput" value="${route.target}" placeholder="host:port">
        </div>
    ` : '';

    const selectedIds = new Set((route.allowed_groups || '').split(',').map(s => s.trim()).filter(Boolean));
    const checkboxes = groups.map(g => `
        <label class="groupCheck">
            <input type="checkbox" value="${g.id}" ${selectedIds.has(String(g.id)) ? 'checked' : ''}>
            ${g.name}
        </label>
    `).join('');

    // IP session auth + static allowlist rows (shown for all route types).
    const ipAuthRows = `
        <div class="routeEditRow">
            <label>IP session auth</label>
            <input type="checkbox" class="ipAuthCheck" ${route.ip_auth ? 'checked' : ''}>
            <span style="font-size:11px;color:#888">grant access if the connecting IP has an active login session</span>
        </div>
        <div class="routeEditRow">
            <label>Allowed groups</label>
            <div class="groupCheckList">${checkboxes || '<em style="font-size:11px;color:#888">No groups yet</em>'}</div>
        </div>
        <div class="routeEditRow">
            <label>Allowed IPs</label>
            <input type="text" class="ipsInput" value="${route.allowed_ips || ''}" placeholder="10.0.0.1, 192.168.0.0/24">
        </div>
    `;

    // Cookie policy row (HTTP routes only).
    const cookieRows = !isTcp ? `
        <div class="routeEditRow">
            <label>Cookie policy</label>
            <select>
                <option value="persistent" ${route.cookie_policy === 'persistent' ? 'selected' : ''}>Persistent</option>
                <option value="session"    ${route.cookie_policy === 'session'    ? 'selected' : ''}>Session only</option>
                <option value="none"       ${route.cookie_policy === 'none'       ? 'selected' : ''}>None</option>
            </select>
        </div>
    ` : `<div class="routeEditRow" style="color:#888;font-size:11px;font-style:italic;padding-left:118px">Cookie auth not available for TCP routes.</div>`;

    panel.innerHTML = `
        ${targetRow}
        ${ipAuthRows}
        ${cookieRows}
        <div class="routeEditActions">
            <button onclick="this.closest('.routeEdit').style.display='none'">Cancel</button>
            <button class="saveBtn">Save</button>
        </div>
    `;

    panel.querySelector('.saveBtn').addEventListener('click', async () => {
        const checked = [...panel.querySelectorAll('.groupCheckList input:checked')].map(el => el.value);
        const body = {
            ip_auth:        panel.querySelector('.ipAuthCheck').checked,
            allowed_groups: checked.join(','),
            allowed_ips:    (panel.querySelector('.ipsInput')?.value || '').trim(),
        };
        if (!isTcp) {
            body.cookie_policy = panel.querySelector('select').value;
        }
        const ti = panel.querySelector('.targetInput');
        if (ti) body.target = ti.value;
        await api('PUT', 'admin/routes?id=' + route.id, body);
        loadRoutes();
    });

    return panel;
}

function onRouteTypeChange() {
    const isTcp = document.getElementById('newRouteType').value === 'tcp';
    document.getElementById('newRouteTlsRow').style.display = isTcp ? 'none' : '';
    if (isTcp) document.getElementById('newRouteTls').checked = false;
}

async function loadSettings() {
    const data = await api('GET', 'admin/settings');
    if (!data) return;
    const s = data.settings || {};
    document.getElementById('settingsDuration').value = s.session_duration_hours || 168;
    document.getElementById('settingsRenew').checked = !!s.renew_on_access;
}

async function saveSettings() {
    const dur = parseInt(document.getElementById('settingsDuration').value) || 168;
    const renew = document.getElementById('settingsRenew').checked;
    const msg = document.getElementById('settingsMsg');
    const data = await api('PUT', 'admin/settings', { session_duration_hours: dur, renew_on_access: renew });
    msg.style.display = '';
    msg.textContent = data ? '✓ Saved' : 'Save failed';
    setTimeout(() => { msg.style.display = 'none'; }, 2000);
}

async function createRoute() {
    const url    = document.getElementById('newRouteUrl').value.trim();
    const target = document.getElementById('newRouteTarget').value.trim();
    const type   = document.getElementById('newRouteType').value || 'proxy';
    const tls    = document.getElementById('newRouteTls').checked;
    const msg    = document.getElementById('newRouteMsg');
    msg.style.display = 'none';
    if (!url || !target) {
        msg.style.display = '';
        msg.textContent = 'URL and target are required.';
        return;
    }
    const data = await api('POST', 'admin/routes', { url, target, type, tls });
    if (!data) return;
    document.getElementById('newRouteUrl').value = '';
    document.getElementById('newRouteTarget').value = '';
    msg.style.display = '';
    msg.textContent = data.warning
        ? `Saved — ${data.warning}`
        : '✓ Route added and live.';
    loadRoutes();
}

async function deleteRoute(id, url) {
    if (!confirm(`Delete route "${url}"?\nThis cannot be undone.`)) return;
    await api('DELETE', 'admin/routes?id=' + id);
    loadRoutes();
}

// ── metrics ───────────────────────────────────────────────────────────────
let metricsAccessLogData = [];
let metricsFailureData   = [];

function getMetricsFilters() {
    return {
        username: document.getElementById('filterUsername')?.value.trim().toLowerCase() || '',
        ip:       document.getElementById('filterIP')?.value.trim().toLowerCase() || '',
        route:    document.getElementById('filterRoute')?.value.trim().toLowerCase() || '',
        status:   document.getElementById('filterStatus')?.value || '',
    };
}

function applyMetricsFilters() {
    const f = getMetricsFilters();
    renderAccessLog(f);
    renderFailures(f);
}

function clearMetricsFilters() {
    document.getElementById('filterUsername').value = '';
    document.getElementById('filterIP').value = '';
    document.getElementById('filterRoute').value = '';
    document.getElementById('filterStatus').value = '';
    document.querySelectorAll('#routeStatItems .item').forEach(i => i.classList.remove('selected'));
    applyMetricsFilters();
}

function accessEventBadge(username) {
    if (!username) return '<span class="evtBadge anon" data-status="anon">anon</span>';
    if (isDenied(username)) return '<span class="evtBadge denied" data-status="denied">denied</span>';
    return `<span class="evtBadge ok" data-status="ok">${username}</span>`;
}

function isDenied(u) { return u === 'Unauthorized User' || u === 'Unauthorized'; }
function isAnon(u)   { return !u; }
function isOk(u)     { return !!u && !isDenied(u); }

function renderAccessLog(filters) {
    const { username, ip, route, status } = filters || {};
    let entries = metricsAccessLogData;
    if (username) entries = entries.filter(e => (e.username || '').toLowerCase().includes(username));
    if (ip)       entries = entries.filter(e => e.ip.toLowerCase().includes(ip));
    if (route)    entries = entries.filter(e => e.route_url.toLowerCase().includes(route));
    if (status === 'denied') entries = entries.filter(e => isDenied(e.username));
    if (status === 'anon')   entries = entries.filter(e => isAnon(e.username));
    if (status === 'ok')     entries = entries.filter(e => isOk(e.username));

    document.getElementById('accessLogCount').textContent =
        entries.length < metricsAccessLogData.length
            ? `${entries.length} / ${metricsAccessLogData.length}`
            : `${metricsAccessLogData.length}`;

    const list = document.getElementById('accessLogItems');
    list.innerHTML = '';
    entries.forEach(e => {
        const el = document.createElement('div');
        el.className = 'item';
        el.style.cursor = 'default';
        el.innerHTML = `
            <span class="failureIp">${e.ip}</span>
            ${accessEventBadge(e.username)}
            <span class="itemSub" style="flex:1;overflow:hidden;text-overflow:ellipsis;white-space:nowrap" title="${e.route_url}">${e.route_url}</span>
            <span class="itemSub" style="flex-shrink:0">${relTime(e.created_at)}</span>
        `;
        el.addEventListener('click', ev => {
            const badge = ev.target.closest('[data-status]');
            if (badge) {
                // Clicking the badge toggles the status filter.
                const sel = document.getElementById('filterStatus');
                sel.value = sel.value === badge.dataset.status ? '' : badge.dataset.status;
            } else {
                // Clicking anywhere else on the row pre-fills the IP filter.
                document.getElementById('filterIP').value = e.ip;
            }
            applyMetricsFilters();
        });
        el.style.cursor = 'pointer';
        list.appendChild(el);
    });
    if (!entries.length) {
        list.innerHTML = '<p style="font-size:12px;color:#aaa;margin:10px 0 0 4px">No matching events.</p>';
    }
}

function renderFailures(filters) {
    const { username, ip, route } = filters || {};
    let entries = metricsFailureData;

    if (username) entries = entries.filter(f => (f.username || '').toLowerCase().includes(username));
    if (ip)       entries = entries.filter(f => f.ip.toLowerCase().includes(ip));

    // Route filter: cross-reference — show failures from IPs seen in matching access log entries.
    if (route) {
        const routeIPs = new Set(
            metricsAccessLogData
                .filter(e => e.route_url.toLowerCase().includes(route))
                .map(e => e.ip)
        );
        entries = entries.filter(f => routeIPs.has(f.ip));
    }

    document.getElementById('failureCount').textContent =
        entries.length < metricsFailureData.length
            ? `${entries.length} / ${metricsFailureData.length}`
            : `${metricsFailureData.length}`;

    const list = document.getElementById('failureItems');
    list.innerHTML = '';
    entries.forEach(f => {
        const el = document.createElement('div');
        el.className = 'item';
        el.style.cursor = 'pointer';
        el.innerHTML = `
            <span class="failureIp">${f.ip}</span>
            <span class="failureUser">${f.username || '—'}</span>
            <span class="itemSub" style="flex-shrink:0">${relTime(f.created_at)}</span>
        `;
        // Clicking a failure pre-fills the IP filter.
        el.addEventListener('click', () => {
            document.getElementById('filterIP').value = f.ip;
            applyMetricsFilters();
        });
        list.appendChild(el);
    });
    if (!entries.length) {
        list.innerHTML = '<p style="font-size:12px;color:#aaa;margin:10px 0 0 4px">No matching failures.</p>';
    }
}

function relTime(dateStr) {
    const diff = Date.now() - new Date(dateStr).getTime();
    if (diff < 60000) return 'just now';
    if (diff < 3600000) return Math.floor(diff / 60000) + 'm ago';
    if (diff < 86400000) return Math.floor(diff / 3600000) + 'h ago';
    return Math.floor(diff / 86400000) + 'd ago';
}

function relExpiry(dateStr) {
    const diff = new Date(dateStr).getTime() - Date.now();
    if (diff < 0) return 'expired';
    if (diff < 3600000) return 'expires in ' + Math.floor(diff / 60000) + 'm';
    if (diff < 86400000) return 'expires in ' + Math.floor(diff / 3600000) + 'h';
    return 'expires in ' + Math.floor(diff / 86400000) + 'd';
}

async function loadMetrics() {
    const data = await api('GET', 'admin/metrics');
    if (!data) return;

    // Active sessions
    const sessionList = document.getElementById('sessionItems');
    sessionList.innerHTML = '';
    (data.sessions || []).forEach(s => {
        const el = document.createElement('div');
        el.className = 'item';
        el.style.cssText = 'align-items:flex-start;cursor:default;';
        el.innerHTML = `
            <div class="sessionRow">
                <div class="itemMain">${s.username}</div>
                <div class="sessionMeta">
                    <span>${s.client_ip}</span>
                    <span>signed in ${relTime(s.created_at)}</span>
                    <span>${relExpiry(s.expires_at)}</span>
                </div>
            </div>
            <button class="delBtn" title="Revoke session">×</button>
        `;
        el.querySelector('.delBtn').addEventListener('click', async () => {
            await api('DELETE', 'admin/metrics?id=' + s.id);
            loadMetrics();
        });
        sessionList.appendChild(el);
    });
    if (!data.sessions?.length) {
        sessionList.innerHTML = '<p style="font-size:12px;color:#aaa;margin:10px 0 0 4px">No active sessions.</p>';
    }

    // Store raw data for filtering
    metricsAccessLogData = data.access_log || [];
    metricsFailureData   = data.auth_failures || [];

    // Route stats — clicking sets the route filter input
    const routeList = document.getElementById('routeStatItems');
    routeList.innerHTML = '';
    const stats = data.route_stats || {};
    const statEntries = Object.entries(stats).sort((a, b) => b[1] - a[1]);
    const maxVal = statEntries[0]?.[1] || 1;

    statEntries.forEach(([url, count]) => {
        const el = document.createElement('div');
        el.className = 'item';
        el.style.cssText = 'flex-direction:column;align-items:stretch;gap:4px;';
        const pct = Math.round((count / maxVal) * 100);
        el.innerHTML = `
            <div style="display:flex;justify-content:space-between;align-items:center;gap:8px;">
                <div class="itemMain">${url}</div>
                <span style="font-size:12px;color:#2d6385;flex-shrink:0">${count.toLocaleString()}</span>
            </div>
            <div class="statBar"><div class="statBarFill" style="width:${pct}%"></div></div>
        `;
        el.addEventListener('click', () => {
            const routeInput = document.getElementById('filterRoute');
            const isSelected = el.classList.contains('selected');
            document.querySelectorAll('#routeStatItems .item').forEach(i => i.classList.remove('selected'));
            if (isSelected) {
                routeInput.value = '';
            } else {
                routeInput.value = url;
                el.classList.add('selected');
            }
            applyMetricsFilters();
        });
        routeList.appendChild(el);
    });
    if (!statEntries.length) {
        routeList.innerHTML = '<p style="font-size:12px;color:#aaa;margin:10px 0 0 4px">No requests recorded yet.</p>';
    }

    applyMetricsFilters();
}
