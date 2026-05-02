authForm.addEventListener('submit', async (e) => {
    e.preventDefault();
    const err = document.getElementById('errorMsg');
    err.textContent = '';

    const res = await fetch('/api/auth/register', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
            username: document.getElementById('username').value,
            password: document.getElementById('password').value,
            invite:   document.getElementById('key').value,
        }),
    }).catch(() => null);

    if (!res || !res.ok) {
        const data = res ? await res.json().catch(() => ({})) : {};
        err.textContent = data.error || 'Registration failed';
        return;
    }

    window.location.href = '/';
});
