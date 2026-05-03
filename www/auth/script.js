// ── state ─────────────────────────────────────────────────────────────────────
let currentSessionId = null;

function showLoggedIn(username) {
    document.getElementById('formState').style.display = 'none';
    document.getElementById('loggedState').style.display = '';
    document.getElementById('loggedUser').textContent = username;
    document.getElementById('routesPanel').style.display = '';
    document.getElementById('sessionsPanel').style.display = '';
    loadRoutes();
    loadSessions();
}

function showLoginForm() {
    document.getElementById('formState').style.display = '';
    document.getElementById('loggedState').style.display = 'none';
    document.getElementById('routesPanel').style.display = 'none';
    document.getElementById('sessionsPanel').style.display = 'none';
    currentSessionId = null;
}

async function loadRoutes() {
    const res = await fetch('/api/auth/routes').catch(() => null);
    if (!res || !res.ok) return;
    const data = await res.json().catch(() => ({}));
    const list = document.getElementById('routeList');
    const noRoutes = document.getElementById('noRoutes');
    const routes = data.routes || [];
    list.innerHTML = '';
    if (routes.length === 0) {
        noRoutes.style.display = '';
        return;
    }
    noRoutes.style.display = 'none';
    routes.forEach(r => {
        const el = document.createElement('div');
        el.className = 'routeItem';
        const scheme = r.tls ? 'https://' : 'http://';
        el.innerHTML = `<a href="${scheme}${r.url}" target="_blank">${r.url}</a>`;
        list.appendChild(el);
    });
}

async function loadSessions() {
    const res = await fetch('/api/auth/sessions').catch(() => null);
    if (!res || !res.ok) return;
    const data = await res.json().catch(() => ({}));
    currentSessionId = data.current_id;
    const list = document.getElementById('sessionList');
    list.innerHTML = '';
    (data.sessions || []).forEach(s => {
        const isCurrent = s.id === currentSessionId;
        const el = document.createElement('div');
        el.className = 'sessionItem' + (isCurrent ? ' current' : '');
        const exp = new Date(s.expires_at);
        const expStr = exp.toLocaleDateString(undefined, { month: 'short', day: 'numeric' });
        el.innerHTML = `
            <div class="sessionInfo">
                <span class="sessionIp">${s.client_ip}</span>
                ${isCurrent ? '<span class="sessionCurrent">this device</span>' : ''}
                <span class="sessionExp">until ${expStr}</span>
            </div>
            <button class="sessionRevokeBtn" title="${isCurrent ? 'Sign out everywhere' : 'Revoke'}">×</button>
        `;
        el.querySelector('.sessionRevokeBtn').addEventListener('click', () => revokeSession(s.id, isCurrent));
        list.appendChild(el);
    });
    if (!data.sessions?.length) {
        list.innerHTML = '<p style="font-size:11px;color:rgba(45,99,133,0.5);margin:4px 0">No active sessions.</p>';
    }
}

async function revokeSession(id, isCurrent) {
    const res = await fetch('/api/auth/sessions?id=' + id, { method: 'DELETE' }).catch(() => null);
    if (!res || !res.ok) return;
    if (isCurrent) {
        showLoginForm();
    } else {
        loadSessions();
    }
}

async function logout() {
    await fetch('/api/auth/logout', { method: 'POST' }).catch(() => null);
    showLoginForm();
}

// ── init ──────────────────────────────────────────────────────────────────────
document.addEventListener('DOMContentLoaded', async () => {
    const res = await fetch('/api/auth/me').catch(() => null);
    if (res && res.ok) {
        const data = await res.json().catch(() => ({}));
        showLoggedIn(data.user?.username || '');
    }
});

// ── login form ────────────────────────────────────────────────────────────────
document.getElementById('authForm').addEventListener('submit', async (e) => {
    e.preventDefault();
    const err = document.getElementById('errorMsg');
    err.textContent = '';

    const res = await fetch('/api/auth/login', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
            username: document.getElementById('username').value,
            password: document.getElementById('password').value,
        }),
    }).catch(() => null);

    if (!res || !res.ok) {
        const data = res ? await res.json().catch(() => ({})) : {};
        err.textContent = data.error || 'Login failed';
        return;
    }

    const data = await res.json();
    showLoggedIn(data.user?.username || '');
});
