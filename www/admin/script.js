document.querySelectorAll('.menuContainer button').forEach(btn => {
    btn.addEventListener('click', () => {
        document.querySelectorAll('.menuContainer button').forEach(b => 
            b.classList.remove('active')
        )
        btn.classList.add('active')
    })
})

