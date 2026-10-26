// 路由与全局筛选状态：都放在地址栏的 hash 里（#/overview?site=…&range=today），刷新、分享链接都能还原。

export interface Route {
    page: string;
    params: URLSearchParams;
}

export function parseHash(): Route {
    const raw = location.hash.replace(/^#\/?/, '');
    const [page, query] = raw.split('?');
    return { page: page || 'overview', params: new URLSearchParams(query || '') };
}

export function href(page: string, params: Record<string, string | number | undefined | null> = {}, keepGlobal = true): string {
    const cur = parseHash().params;
    const p = new URLSearchParams();
    if (keepGlobal) {
        for (const k of ['site', 'range', 'from', 'to', 'server']) {
            const v = cur.get(k);
            if (v) p.set(k, v);
        }
    }
    for (const [k, v] of Object.entries(params)) {
        if (v === undefined || v === null || v === '') p.delete(k);
        else p.set(k, String(v));
    }
    const s = p.toString();
    return `#/${page}${s ? '?' + s : ''}`;
}

export function go(page: string, params: Record<string, string | number | undefined | null> = {}, keepGlobal = true) {
    location.hash = href(page, params, keepGlobal);
}

// 只改当前页的部分参数
export function setParams(params: Record<string, string | number | undefined | null>) {
    const r = parseHash();
    for (const [k, v] of Object.entries(params)) {
        if (v === undefined || v === null || v === '') r.params.delete(k);
        else r.params.set(k, String(v));
    }
    const s = r.params.toString();
    location.hash = `#/${r.page}${s ? '?' + s : ''}`;
}

export function store<T>(key: string, fallback: T): T {
    try {
        const v = localStorage.getItem('liuli.' + key);
        return v === null ? fallback : (JSON.parse(v) as T);
    } catch {
        return fallback;
    }
}

export function save(key: string, v: unknown) {
    try {
        localStorage.setItem('liuli.' + key, JSON.stringify(v));
    } catch {
        /* 隐私模式等 */
    }
}

// 当前全局筛选
export interface Ctx {
    site: string;
    range: string;
    from: string;
    to: string;
    server: string;
    params: URLSearchParams;
}

export function rangeParams(c: Ctx): Record<string, string> {
    const p: Record<string, string> = { site: c.site, range: c.range };
    if (c.range === 'custom') {
        p.from = c.from;
        p.to = c.to;
    }
    if (c.server) p.server = c.server;
    return p;
}
