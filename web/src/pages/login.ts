// 登录页：唯一用琉璃液态玻璃动画的地方，卡片本身是毛玻璃，透出下面的琉璃
import { api } from '../api';
import { h } from '../util';

export function renderLogin(app: HTMLElement, onDone: () => void) {
    document.title = '登录 · 琉璃观测台';
    const stage = h('div', { class: 'stage', 'aria-hidden': 'true' });
    const user = h('input', { type: 'text', name: 'username', autocomplete: 'username', placeholder: '用户名', value: 'admin', required: true });
    const pass = h('input', { type: 'password', name: 'password', autocomplete: 'current-password', placeholder: '密码', required: true });
    const msg = h('div', { class: 'msg', role: 'alert' });
    const btn = h('button', { class: 'btn primary', type: 'submit' }, '登录');
    let glass: any = null;
    const form = h('form', {
        onsubmit: async (e: Event) => {
            e.preventDefault();
            msg.textContent = '';
            btn.disabled = true;
            btn.textContent = '登录中…';
            try {
                await api.post('/api/login', { username: user.value.trim(), password: pass.value });
                glass?.destroy();
                onDone();
            } catch (err: any) {
                msg.textContent = err.message;
                glass?.wobble();
                pass.select();
            } finally {
                btn.disabled = false;
                btn.textContent = '登录';
            }
        },
    }, user, pass, btn, msg);
    const card = h('div', { class: 'card glass' },
        h('div', { class: 'brand' }, h('img', { src: '/favicon.svg', alt: '' }), h('b', null, '琉璃'), h('span', null, '观测台')),
        h('p', { class: 'sub' }, '网站访问日志监控报表'),
        form);
    app.replaceChildren(h('div', { class: 'login' }, stage, card));
    pass.focus();
    // 动画组件单独打包，加载失败也不影响登录
    import('../vendor/liuli-glass.mjs')
        .then((m) => {
            const dark = document.documentElement.dataset.theme === 'dark';
            glass = m.default.mount(stage, { theme: dark ? 'dark' : 'light', offsetY: 0.02, zoom: 1.1, respectReducedMotion: false });
        })
        .catch(() => {});
}
