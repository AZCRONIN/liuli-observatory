// 小工具：建 DOM（只用 textContent，日志里的 URI / UA 都是外部输入，绝不拼进 innerHTML）、格式化数字。

type Child = Node | string | number | null | undefined | false | Child[];
type Attrs = Record<string, any>;

export function h<K extends keyof HTMLElementTagNameMap>(tag: K, attrs?: Attrs | null, ...children: Child[]): HTMLElementTagNameMap[K] {
    const el = document.createElement(tag);
    if (attrs) {
        for (const [k, v] of Object.entries(attrs)) {
            if (v === undefined || v === null || v === false) continue;
            if (k === 'class') el.className = v;
            else if (k === 'style' && typeof v === 'object') Object.assign(el.style, v);
            else if (k.startsWith('on') && typeof v === 'function') el.addEventListener(k.slice(2), v);
            else if (k === 'dataset') Object.assign(el.dataset, v);
            else if (k in el && typeof v !== 'string') (el as any)[k] = v;
            else el.setAttribute(k, v === true ? '' : String(v));
        }
    }
    append(el, children);
    return el;
}

export function append(el: Node, children: Child[]) {
    for (const c of children) {
        if (c === null || c === undefined || c === false) continue;
        if (Array.isArray(c)) append(el, c);
        else if (c instanceof Node) el.appendChild(c);
        else el.appendChild(document.createTextNode(String(c)));
    }
}

// 静态 SVG 图标（内容来自代码本身，不含外部数据）
export function svg(markup: string): SVGElement {
    const t = document.createElement('template');
    t.innerHTML = markup.trim();
    return t.content.firstElementChild as SVGElement;
}

export function clear(el: Element) {
    while (el.firstChild) el.removeChild(el.firstChild);
}

export function esc(s: unknown): string {
    return String(s ?? '').replace(/[&<>"']/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[c]!);
}

const nf = new Intl.NumberFormat('zh-CN');

export function num(n: number | null | undefined): string {
    if (n === null || n === undefined || Number.isNaN(n)) return '-';
    return nf.format(Math.round(n));
}

export function bytes(n: number | null | undefined, digits = 2): string {
    if (!n) return '0 B';
    const u = ['B', 'KB', 'MB', 'GB', 'TB'];
    let i = 0;
    let v = n;
    while (v >= 1024 && i < u.length - 1) {
        v /= 1024;
        i++;
    }
    return `${v.toFixed(i === 0 ? 0 : digits)} ${u[i]}`;
}

// 返回 [数值, 单位]，用于大号数字旁边的小单位
export function bytesParts(n: number): [string, string] {
    const s = bytes(n);
    const i = s.lastIndexOf(' ');
    return [s.slice(0, i), s.slice(i + 1)];
}

export function ms(n: number | null | undefined): string {
    if (n === null || n === undefined || n < 0) return '-';
    if (n >= 1000) return `${(n / 1000).toFixed(2)} 秒`;
    if (n < 1) return '<1 毫秒';
    return `${Math.round(n)} 毫秒`;
}

export function pct(a: number, b: number): string {
    if (!b) return '-';
    return `${((a / b) * 100).toFixed(1)}%`;
}

const pad = (n: number) => String(n).padStart(2, '0');

export function time(ts: number, withDate = true): string {
    const d = new Date(ts * 1000);
    const t = `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
    if (!withDate) return t;
    return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${t}`;
}

export function hm(ts: number): string {
    const d = new Date(ts * 1000);
    return `${pad(d.getHours())}:${pad(d.getMinutes())}`;
}

export function md(ts: number): string {
    const d = new Date(ts * 1000);
    return `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

export function ymd(d: Date): string {
    return `${d.getFullYear()}-${pad(d.getMonth() + 1)}-${pad(d.getDate())}`;
}

export function ago(ts: number): string {
    const s = Math.max(0, Math.floor(Date.now() / 1000 - ts));
    if (s < 60) return `${s} 秒前`;
    if (s < 3600) return `${Math.floor(s / 60)} 分钟前`;
    if (s < 86400) return `${Math.floor(s / 3600)} 小时前`;
    return time(ts);
}

export function statusClass(code: number | string): string {
    const c = Number(code);
    if (c >= 500) return 's5';
    if (c >= 400) return 's4';
    if (c >= 300) return 's3';
    return 's2';
}

let toastTimer = 0;
export function toast(msg: string) {
    let el = document.querySelector<HTMLDivElement>('.toast');
    if (!el) {
        el = h('div', { class: 'toast', role: 'status' });
        document.body.appendChild(el);
    }
    el.textContent = msg;
    requestAnimationFrame(() => el!.classList.add('show'));
    clearTimeout(toastTimer);
    toastTimer = window.setTimeout(() => el!.classList.remove('show'), 2600);
}

export function tip(text: string): HTMLElement {
    return h('button', { class: 'tip', type: 'button', 'aria-label': text }, '?', h('span', { class: 'pop', role: 'tooltip' }, text));
}
