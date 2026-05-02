// ── state ─────────────────────────────────────────────────────────────────────
function showLoggedIn(username) {
    document.getElementById('formState').style.display = 'none';
    document.getElementById('loggedState').style.display = '';
    document.getElementById('loggedUser').textContent = username;
    document.getElementById('routesPanel').style.display = '';
    loadRoutes();
}

function showLoginForm() {
    document.getElementById('formState').style.display = '';
    document.getElementById('loggedState').style.display = 'none';
    document.getElementById('routesPanel').style.display = 'none';
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
