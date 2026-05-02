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
        if (btn.dataset.view === 'routes') loadRoutes();
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
        const exp = new Date(inv.expires_at).toLocaleDateString();
        el.innerHTML = `
            <div class="itemMain">#${inv.id}</div>
            <span class="tag${inv.used ? ' used' : ''}">${inv.used ? 'used' : 'active'}</span>
            <div class="itemSub">exp ${exp}</div>
            ${!inv.used ? `<button class="delBtn" title="Delete">×</button>` : ''}
        `;
        el.querySelector('.delBtn')?.addEventListener('click', () => deleteInvite(inv.id));
        list.appendChild(el);
    });
}

async function createInvite() {
    const hours = parseInt(document.getElementById('inviteHours').value) || 24;
    const data = await api('POST', 'admin/invites', { hours });
    if (!data) return;
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

    let authRows = '';
    if (!isTcp) {
        const selectedIds = new Set(route.allowed_groups.split(',').map(s => s.trim()).filter(Boolean));
        const checkboxes = groups.map(g => `
            <label class="groupCheck">
                <input type="checkbox" value="${g.id}" ${selectedIds.has(String(g.id)) ? 'checked' : ''}>
                ${g.name}
            </label>
        `).join('');
        authRows = `
            <div class="routeEditRow">
                <label>Allowed groups</label>
                <div class="groupCheckList">${checkboxes || '<em style="font-size:11px;color:#888">No groups yet</em>'}</div>
            </div>
            <div class="routeEditRow">
                <label>Cookie policy</label>
                <select>
                    <option value="persistent" ${route.cookie_policy === 'persistent' ? 'selected' : ''}>Persistent (7 days)</option>
                    <option value="session"    ${route.cookie_policy === 'session'    ? 'selected' : ''}>Session only</option>
                    <option value="none"       ${route.cookie_policy === 'none'       ? 'selected' : ''}>None</option>
                </select>
            </div>
            <div class="routeEditRow">
                <label>Renew on access</label>
                <input type="checkbox" class="renewCheck" ${route.renew_on_access ? 'checked' : ''}>
            </div>
        `;
    } else {
        authRows = `<div class="routeEditRow" style="color:#888;font-size:11px;font-style:italic">Auth settings do not apply to raw TCP routes.</div>`;
    }

    panel.innerHTML = `
        ${targetRow}
        ${authRows}
        <div class="routeEditActions">
            <button onclick="this.closest('.routeEdit').style.display='none'">Cancel</button>
            <button class="saveBtn">Save</button>
        </div>
    `;

    panel.querySelector('.saveBtn').addEventListener('click', async () => {
        const body = {};
        if (!isTcp) {
            const checked = [...panel.querySelectorAll('.groupCheckList input:checked')].map(el => el.value);
            body.allowed_groups  = checked.join(',');
            body.cookie_policy   = panel.querySelector('select').value;
            body.renew_on_access = panel.querySelector('.renewCheck').checked;
        }
        const ti = panel.querySelector('.targetInput');
        if (ti) body.target = ti.value;
        await api('PUT', 'admin/routes?id=' + route.id, body);
        loadRoutes();
    });

    return panel;
}

async function createRoute() {
    const url    = document.getElementById('newRouteUrl').value.trim();
    const target = document.getElementById('newRouteTarget').value.trim();
    const type   = document.getElementById('newRouteType').value || 'proxy';
    const msg    = document.getElementById('newRouteMsg');
    msg.style.display = 'none';
    if (!url || !target) {
        msg.style.display = '';
        msg.textContent = 'URL and target are required.';
        return;
    }
    const data = await api('POST', 'admin/routes', { url, target, type });
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
