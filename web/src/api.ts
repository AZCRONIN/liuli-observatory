// 接口调用：会话在 HttpOnly Cookie 里，写操作带上 X-Liuli 头（防跨站请求伪造）。

export class ApiError extends Error {
    constructor(public status: number, message: string) {
        super(message);
    }
}

let onUnauthorized: () => void = () => {};
export function setUnauthorizedHandler(fn: () => void) {
    onUnauthorized = fn;
}

async function call<T>(method: string, path: string, body?: unknown): Promise<T> {
    const init: RequestInit = { method, credentials: 'same-origin', headers: {} };
    if (method !== 'GET') (init.headers as Record<string, string>)['X-Liuli'] = '1';
    if (body !== undefined) {
        (init.headers as Record<string, string>)['Content-Type'] = 'application/json';
        init.body = JSON.stringify(body);
    }
    const res = await fetch(path, init);
    let data: any = null;
    try {
        data = await res.json();
    } catch {
        /* 非 JSON */
    }
    if (!res.ok) {
        if (res.status === 401 && path !== '/api/login') onUnauthorized();
        throw new ApiError(res.status, data?.error || `请求失败（${res.status}）`);
    }
    return data as T;
}

export function qs(params: Record<string, string | number | undefined | null>): string {
    const u = new URLSearchParams();
    for (const [k, v] of Object.entries(params)) if (v !== undefined && v !== null && v !== '') u.set(k, String(v));
    const s = u.toString();
    return s ? `?${s}` : '';
}

export const api = {
    get: <T>(path: string, params: Record<string, any> = {}) => call<T>('GET', path + qs(params)),
    post: <T>(path: string, body?: unknown) => call<T>('POST', path, body ?? {}),
    put: <T>(path: string, body?: unknown) => call<T>('PUT', path, body ?? {}),
    del: <T>(path: string, params: Record<string, any> = {}) => call<T>('DELETE', path + qs(params)),
};

// ---- 类型 ----

export interface Totals {
    req: number;
    pv: number;
    uv: number;
    ip: number;
    bytes: number;
    req_bytes: number;
    spider: number;
    rt_avg: number;
    s2: number;
    s3: number;
    s4: number;
    s5: number;
}

export interface Point extends Totals {
    ts: number;
}

export interface DimRow {
    key: string;
    extra?: string;
    req: number;
    pv: number;
    uv: number;
    bytes: number;
    last: number;
}

export interface LogRow {
    id: number;
    server: string;
    ts: number;
    ip: string;
    region: string;
    method: string;
    uri: string;
    status: number;
    bytes: number;
    rt: number;
    urt: number;
    referer: string;
    ua: string;
    spider: string;
    browser: string;
    os: string;
    device: string;
}

export interface Site {
    server_id: string;
    site: string;
    log_path: string;
    enabled: boolean;
}
