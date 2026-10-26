import './styles/fonts.css';
import './styles/app.css';

import { api, setUnauthorizedHandler, Site } from './api';
import { icons } from './icons';
import { Scope } from './scope';
import { Ctx, href, parseHash, save, setParams, store } from './state';
import { clear, h, svg, ymd } from './util';

import { renderLogin } from './pages/login';
import { overview } from './pages/overview';
import { sitesPage } from './pages/sites';
import { ipPage } from './pages/ip';
import { clientPage } from './pages/client';
import { spiderPage } from './pages/spider';
import { uriPage } from './pages/uri';
import { statusPage } from './pages/status';
import { errorsPage, slowPage } from './pages/logs';
import { banPage } from './pages/ban';
import { settingsPage } from './pages/settings';

export interface PageDef {
    title: string;
    icon: string;
    needsSite?: boolean;
    noRange?: boolean;
    tag?: string;
    render(root: HTMLElement, ctx: Ctx, scope: Scope): void | Promise<void>;
}

const pages: Record<string, PageDef> = {
    overview: { ...overview, icon: 'overview' },
    sites: { ...sitesPage, icon: 'sites' },
    ip: { ...ipPage, icon: 'ip' },
    client: { ...clientPage, icon: 'client' },
    spider: { ...spiderPage, icon: 'spider' },
    uri: { ...uriPage, icon: 'uri' },
    status: { ...statusPage, icon: 'status' },
    errors: { ...errorsPage, icon: 'errors' },
    slow: { ...slowPage, icon: 'slow' },
    ban: { ...banPage, icon: 'ban', tag: '第二版' },
    settings: { ...settingsPage, icon: 'settings' },
};
const navOrder = ['overview', 'sites', 'ip', 'client', 'spider', 'uri', 'status', 'errors', 'slow', '-', 'ban', 'settings'];

const REFRESH_SECONDS = 60;

let sites: Site[] = [];
let me = { username: '' };
let scope: Scope | null = null;
const app = document.getElementById('app')!;

// ---- 主题：默认跟随系统，手动切换后记住 ----
function applyTheme() {
    const saved = store<string>('theme', '');
    const dark = saved ? saved === 'dark' : matchMedia('(prefers-color-scheme: dark)').matches;
    document.documentElement.dataset.theme = dark ? 'dark' : 'light';
}
applyTheme();
matchMedia('(prefers-color-scheme: dark)').addEventListener('change', () => {
    if (!store('theme', '') && shell) {
        applyTheme();
        shell = null;
        route();
    }
});

function toggleTheme() {
    const next = document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark';
    save('theme', next);
    applyTheme();
    shell = null; // 侧栏里的图标要换，重建外壳
    route();
}

// ---- 启动 ----
setUnauthorizedHandler(() => showLogin());

async function boot() {
    try {
        me = await api.get('/api/me');
    } catch {
        showLogin();
        return;
    }
    await loadSites();
    window.addEventListener('hashchange', route);
    route();
}

export async function loadSites() {
    try {
        const r = await api.get<{ sites: Site[] }>('/api/sites');
        sites = r.sites;
    } catch {
        sites = [];
    }
}

function showLogin() {
    scope?.dispose();
    scope = null;
    window.removeEventListener('hashchange', route);
    clear(app);
    renderLogin(app, async () => {
        me = await api.get('/api/me');
        await loadSites();
        window.addEventListener('hashchange', route);
        route();
    });
}

// ---- 外壳 ----
let shell: { main: HTMLElement; content: HTMLElement; bar: HTMLElement; nav: HTMLElement; root: HTMLElement } | null = null;

function buildShell() {
    const nav = h('nav', { class: 'nav', 'aria-label': '主菜单' });
    for (const k of navOrder) {
        if (k === '-') {
            nav.appendChild(h('div', { class: 'nav-sep' }));
            continue;
        }
        const p = pages[k];
        nav.appendChild(h('a', { href: href(k), dataset: { page: k } }, svg(icons[p.icon]), p.title, p.tag ? h('span', { class: 'tag' }, p.tag) : null));
    }
    const brand = () => h('a', { class: 'brand', href: '#/overview' }, h('img', { src: '/favicon.svg', alt: '' }), h('b', null, '琉璃'), h('span', null, '观测台'));
    const side = h('aside', { class: 'side' },
        brand(), nav,
        h('div', { class: 'side-foot' },
            h('span', { class: 'who' }, me.username),
            h('button', { class: 'icon-btn', title: '切换深浅色', 'aria-label': '切换深浅色', onclick: toggleTheme },
                svg(document.documentElement.dataset.theme === 'dark' ? icons.sun : icons.moon)),
            h('button', { class: 'icon-btn', title: '退出登录', 'aria-label': '退出登录', onclick: async () => { await api.post('/api/logout'); showLogin(); } }, svg(icons.logout))));
    const root = h('div', { class: 'app' });
    const bar = h('div', { class: 'bar glass' });
    const content = h('div', { class: 'content' });
    const main = h('main', { class: 'main' },
        h('div', { class: 'mob-top' },
            h('button', { class: 'icon-btn', 'aria-label': '打开菜单', onclick: () => root.classList.add('nav-open') }, svg(icons.menu)),
            brand(), h('div', { style: { flex: '1' } }),
            h('button', { class: 'icon-btn', 'aria-label': '切换深浅色', onclick: toggleTheme }, svg(document.documentElement.dataset.theme === 'dark' ? icons.sun : icons.moon))),
        bar, content);
    const scrim = h('div', { class: 'scrim', onclick: () => root.classList.remove('nav-open') });
    nav.addEventListener('click', () => root.classList.remove('nav-open'));
    root.append(side, scrim, main);
    clear(app);
    app.appendChild(root);
    shell = { main, content, bar, nav, root };
}

function currentCtx(): Ctx {
    const { params } = parseHash();
    let site = params.get('site') || store<string>('site', '');
    if (!sites.find((s) => s.site === site)) site = sites[0]?.site || '';
    const today = ymd(new Date());
    return {
        site,
        range: params.get('range') || 'today',
        from: params.get('from') || today,
        to: params.get('to') || today,
        server: params.get('server') || '',
        params,
    };
}

const ranges: [string, string][] = [['today', '今日'], ['yesterday', '昨日'], ['7d', '近 7 天'], ['30d', '近 30 天'], ['custom', '自定义']];

function renderBar(def: PageDef, ctx: Ctx) {
    const bar = shell!.bar;
    clear(bar);
    bar.appendChild(h('h1', { class: 'title' }, def.title));
    if (def.needsSite && sites.length) {
        const sel = h('select', { 'aria-label': '网站', onchange: (e: Event) => { const v = (e.target as HTMLSelectElement).value; save('site', v); setParams({ site: v, page: undefined }); } },
            sites.map((s) => h('option', { value: s.site, selected: s.site === ctx.site }, s.site)));
        bar.appendChild(h('label', { class: 'field' }, h('span', null, '网站'), sel));
        if (!def.noRange) {
            const seg = h('div', { class: 'seg', role: 'group', 'aria-label': '时间' },
                ranges.map(([k, label]) => h('button', { type: 'button', 'aria-pressed': String(ctx.range === k), onclick: () => setParams({ range: k, page: undefined }) }, label)));
            bar.appendChild(seg);
            if (ctx.range === 'custom') {
                const max = ymd(new Date());
                const f = h('input', { type: 'date', value: ctx.from, max, 'aria-label': '开始日期' });
                const t = h('input', { type: 'date', value: ctx.to, max, 'aria-label': '结束日期' });
                const apply = () => { if (f.value && t.value) setParams({ from: f.value <= t.value ? f.value : t.value, to: f.value <= t.value ? t.value : f.value, page: undefined }); };
                f.addEventListener('change', apply);
                t.addEventListener('change', apply);
                bar.appendChild(h('span', { class: 'field' }, f, '至', t));
            }
        }
    }
    bar.appendChild(h('div', { class: 'grow' }));
    if (def.needsSite) bar.appendChild(refreshRing());
}

// 自动刷新倒计时圈：到 0 时调用页面注册的刷新函数，点一下立即刷新
function refreshRing() {
    const R = 12;
    const C = 2 * Math.PI * R;
    const run = svg(`<svg viewBox="0 0 28 28"><circle class="track" cx="14" cy="14" r="${R}"/><circle class="run" cx="14" cy="14" r="${R}" stroke-dasharray="${C}" stroke-dashoffset="0"/></svg>`);
    const label = h('span', { class: 'num' }, String(REFRESH_SECONDS));
    const btn = h('button', { class: 'refresh', type: 'button', title: '自动刷新（点击立即刷新）', 'aria-label': '立即刷新' }, run, label);
    let left = REFRESH_SECONDS;
    const circle = run.querySelector('.run') as SVGCircleElement;
    const fire = () => {
        left = REFRESH_SECONDS;
        scope?.refreshers.forEach((f) => f());
    };
    const tick = () => {
        left--;
        if (left <= 0) fire();
        label.textContent = String(left);
        circle.style.strokeDashoffset = String(C * (1 - left / REFRESH_SECONDS));
    };
    btn.addEventListener('click', () => { fire(); label.textContent = String(left); circle.style.strokeDashoffset = '0'; });
    scope!.every(1000, tick);
    return btn;
}

function route() {
    const { page } = parseHash();
    const key = pages[page] ? page : 'overview';
    const def = pages[key];
    if (!shell || !document.body.contains(shell.root)) buildShell();

    scope?.dispose();
    scope = new Scope();
    const ctx = currentCtx();
    if (ctx.site) save('site', ctx.site);
    for (const a of shell!.nav.querySelectorAll<HTMLAnchorElement>('a[data-page]')) {
        const k = a.dataset.page!;
        a.href = href(k);
        if (k === key) a.setAttribute('aria-current', 'page');
        else a.removeAttribute('aria-current');
    }
    document.title = `${def.title} · 琉璃观测台`;
    renderBar(def, ctx);
    const content = shell!.content;
    clear(content);
    if (def.needsSite && !ctx.site) {
        content.appendChild(h('div', { class: 'placeholder' }, '还没有配置任何站点。', h('br'), '到「设置」里添加一条「站点 ↔ 日志路径」就开始统计。',
            h('div', { style: { marginTop: '14px' } }, h('a', { class: 'btn primary', href: '#/settings' }, '去设置'))));
        return;
    }
    def.render(content, ctx, scope);
}

// 设置页增删站点后，刷新顶部的站点下拉
window.addEventListener('liuli:sites', async () => {
    await loadSites();
});

boot();
