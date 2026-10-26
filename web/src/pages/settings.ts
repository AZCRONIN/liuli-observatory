import { api, Site } from '../api';
import { Ctx } from '../state';
import { Scope } from '../scope';
import { load, sheet, table } from '../ui';
import { ago, bytes, h, num, pct, toast } from '../util';

interface Settings {
    pv: { static_ext: string[]; exclude_prefixes: string[]; exclude_paths: string[] };
    detail_retention_days: number;
    agg_retention_days: number;
    slow_threshold_ms: number;
    ip_source: string;
    auto_add_sites: boolean;
}
interface Status {
    site: string;
    path: string;
    exists: boolean;
    size: number;
    offset: number;
    lines: number;
    skipped: number;
    errors: number;
    last_error: string;
    last_record: number;
    rotations: number;
}
interface Resp {
    settings: Settings;
    sites: Site[];
    status: Status[];
    discover: { site: string; path: string }[];
    server_id: string;
    geo: boolean;
    log_root: string;
}

const sitesChanged = () => window.dispatchEvent(new Event('liuli:sites'));

export const settingsPage = {
    title: '设置',
    needsSite: false,
    render(root: HTMLElement, _ctx: Ctx, scope: Scope) {
        const sitesBox = h('div');
        const rulesBox = h('div');
        const pwBox = h('div');
        root.append(
            h('div', { style: { marginBottom: '16px' } }, sheet('站点与日志', { tip: '每个站点对应一个访问日志文件（容器里的路径）。新增网站时在这里加一条即可开始统计。' }, sitesBox)),
            h('div', { class: 'grid cols-2' },
                sheet('统计规则与保留', {}, rulesBox),
                sheet('账号', {}, pwBox)),
        );

        const render = (r: Resp) => {
            renderSites(sitesBox, r);
            renderRules(rulesBox, r);
        };
        const reload = () => load(sitesBox, api.get<Resp>('/api/settings'), (r) => { render(r); });
        reload();
        scope.every(5000, () => api.get<Resp>('/api/settings').then((r) => renderSites(sitesBox, r)).catch(() => {}));
        renderPassword(pwBox);

        function renderSites(box: HTMLElement, r: Resp) {
            // 正在输入时不重绘，免得打断
            if (box.contains(document.activeElement) && (document.activeElement as HTMLElement).tagName === 'INPUT') return;
            const st = (site: string) => r.status.find((s) => s.site === site);
            const rows = table(r.sites, [
                { title: '站点', get: (x) => h('b', null, x.site) },
                { title: '日志路径', cls: 'cell-wrap', get: (x) => h('span', { class: 'mono' }, x.log_path) },
                {
                    title: '状态', get: (x) => {
                        if (!x.enabled) return h('span', { class: 'chip' }, '已停用');
                        const s = st(x.site);
                        if (!s) return h('span', { class: 'chip' }, '启动中');
                        if (!s.exists) return h('span', { class: 'chip', style: { borderColor: 'var(--pencil)', color: 'var(--pencil)', borderStyle: 'dashed' } }, '找不到文件');
                        return h('span', { class: 'chip', style: { borderColor: 'var(--c-main)', color: 'var(--c-main)' } }, s.offset >= s.size ? '已跟上' : `读取中 ${pct(s.offset, s.size)}`);
                    },
                },
                { title: '文件大小', cls: 'r', get: (x) => { const s = st(x.site); return s?.exists ? bytes(s.size) : '-'; } },
                { title: '本次启动入库', cls: 'r', get: (x) => num(st(x.site)?.lines ?? 0) },
                {
                    title: '解析失败', cls: 'r', get: (x) => {
                        const s = st(x.site);
                        if (!s?.errors) return '0';
                        return h('span', { title: s.last_error, style: { color: 'var(--pencil)', cursor: 'help' } }, num(s.errors));
                    },
                },
                { title: '最近一条', cls: 'r', get: (x) => { const s = st(x.site); return s?.last_record ? ago(s.last_record) : '-'; } },
                {
                    title: '', cls: 'r', get: (x) => h('span', { style: { display: 'inline-flex', gap: '6px' } },
                        h('button', { class: 'btn sm', onclick: async () => { await api.post('/api/sites', { ...x, enabled: !x.enabled }); toast(x.enabled ? '已停用' : '已启用'); sitesChanged(); reload(); } }, x.enabled ? '停用' : '启用'),
                        h('button', { class: 'btn sm ghost', onclick: async () => {
                            if (!confirm(`删除站点 ${x.site} 的配置？已统计的数据会保留到过期，自动发现也不会再把它加回来。`)) return;
                            await api.del('/api/sites', { site: x.site, server: x.server_id });
                            toast('已删除');
                            sitesChanged();
                            reload();
                        } }, '删除')),
                },
            ], { emptyText: '还没有站点' });

            const nameIn = h('input', { type: 'text', placeholder: '站点域名，例如 sosoquark.com', style: { width: '240px' } });
            const pathIn = h('input', { type: 'text', placeholder: '容器内日志路径，例如 /logs/sosoquark.com/log/access.log', style: { flex: '1', minWidth: '260px' } });
            const add = async (site: string, log_path: string) => {
                try {
                    const res = await api.post<{ exists: boolean }>('/api/sites', { site, log_path, enabled: true });
                    toast(res.exists ? `已添加 ${site}，开始统计` : `已添加 ${site}，但目前找不到这个文件，请检查挂载和路径`);
                    sitesChanged();
                    reload();
                } catch (e: any) {
                    toast(e.message);
                }
            };
            const form = h('div', { class: 'filters', style: { marginTop: '14px' } }, nameIn, pathIn,
                h('button', { class: 'btn primary', onclick: () => add(nameIn.value.trim(), pathIn.value.trim()) }, '添加站点'));

            const known = new Set(r.sites.map((s) => s.log_path));
            const found = r.discover.filter((d) => !known.has(d.path));
            const disc = found.length
                ? h('div', { style: { marginTop: '12px', fontSize: '13px' } },
                    h('div', { class: 'muted', style: { marginBottom: '6px' } }, `在日志目录 ${r.log_root} 下发现这些访问日志：`),
                    h('div', { style: { display: 'grid', gap: '6px' } }, found.map((d) => h('div', { style: { display: 'flex', gap: '10px', alignItems: 'center', flexWrap: 'wrap' } },
                        h('b', null, d.site), h('span', { class: 'mono muted' }, d.path),
                        h('button', { class: 'btn sm', onclick: () => add(d.site, d.path) }, '添加')))))
                : null;
            box.replaceChildren(rows, form, disc || '');
        }

        function renderRules(box: HTMLElement, r: Resp) {
            const s = r.settings;
            const list = (v: string[]) => v.join('\n');
            const parse = (t: string) => t.split(/[\s,，]+/).map((x) => x.trim()).filter(Boolean);
            const ext = h('textarea', { rows: 3 }, s.pv.static_ext.join(' '));
            const pref = h('textarea', { rows: 3 }, list(s.pv.exclude_prefixes));
            const paths = h('textarea', { rows: 3 }, list(s.pv.exclude_paths));
            const nIn = (v: number, min: number, max: number) => h('input', { type: 'number', value: String(v), min, max, style: { width: '120px' } });
            const detail = nIn(s.detail_retention_days, 1, 90);
            const agg = nIn(s.agg_retention_days, 30, 3650);
            const slow = nIn(s.slow_threshold_ms, 10, 600000);
            const autoAdd = h('input', { type: 'checkbox', checked: s.auto_add_sites });
            const ipSrc = h('select', null,
                [['remote_addr', '直接连接的 IP（$remote_addr，没套 CDN 时用这个）'], ['cf', 'Cloudflare 的 CF-Connecting-IP'], ['x_real_ip', 'X-Real-IP 请求头'], ['xff', 'X-Forwarded-For 第一个 IP']]
                    .map(([v, l]) => h('option', { value: v, selected: v === s.ip_source }, l)));
            const reBtn = h('button', { class: 'btn', hidden: true, onclick: async () => {
                reBtn.disabled = true;
                reBtn.textContent = '重新判定中…';
                try {
                    const res = await api.post<{ changed: number }>('/api/reclassify');
                    toast(`已重新判定，${num(res.changed)} 条明细的浏览量标记有变化`);
                    reBtn.hidden = true;
                } catch (e: any) {
                    toast(e.message);
                } finally {
                    reBtn.disabled = false;
                    reBtn.textContent = '按新规则重新判定明细保留期内的数据';
                }
            } }, '按新规则重新判定明细保留期内的数据');
            const save = h('button', { class: 'btn primary', onclick: async () => {
                const body: Settings = {
                    pv: { static_ext: parse(ext.value), exclude_prefixes: parse(pref.value), exclude_paths: parse(paths.value) },
                    detail_retention_days: Number(detail.value), agg_retention_days: Number(agg.value),
                    slow_threshold_ms: Number(slow.value), ip_source: ipSrc.value, auto_add_sites: autoAdd.checked,
                };
                try {
                    const res = await api.put<{ pv_changed: boolean }>('/api/settings', body);
                    toast(res.pv_changed ? '已保存。新规则对之后的日志生效' : '已保存');
                    if (res.pv_changed) reBtn.hidden = false;
                } catch (e: any) {
                    toast(e.message);
                }
            } }, '保存');
            const row = (label: string, ctl: Node, help?: string) => [h('label', null, label), h('div', null, ctl, help ? h('div', { class: 'help' }, help) : null)];
            box.replaceChildren(h('div', { class: 'form-grid' },
                row('静态资源扩展名', ext, '这些扩展名的请求不算浏览量。用空格或逗号分隔。'),
                row('排除的路径前缀', pref, '以这些开头的路径不算浏览量，例如接口 /api/。每行一个。'),
                row('排除的路径', paths, '精确匹配，例如 /robots.txt。每行一个。'),
                row('自动添加站点', h('label', { class: 'radios' }, autoAdd, '日志目录里出现新网站的 access.log 时自动开始统计'),
                    '每 30 秒检查一次。手动删除过的站点和观测台自己的域名不会被自动加回来。'),
                row('真实访客 IP', ipSrc, '没套 Cloudflare 时保持默认，否则别人可以伪造请求头冒充任意 IP。'),
                row('明细保留', h('span', { class: 'field' }, detail, '天'), '明细用于错误日志、慢请求和按分钟的趋势。'),
                row('汇总保留', h('span', { class: 'field' }, agg, '天'), '按小时 / 按天的汇总和排行，用于更长时间的查询。'),
                row('慢请求阈值', h('span', { class: 'field' }, slow, '毫秒')),
                [h('span'), h('div', { class: 'filters' }, save, reBtn)],
            ), h('div', { class: 'muted', style: { marginTop: '16px', fontSize: '12.5px' } },
                `服务器 ID：${r.server_id}　IP 库：${r.geo ? '已加载' : '未加载（地区显示为未知）'}`));
        }

        function renderPassword(box: HTMLElement) {
            const old = h('input', { type: 'password', autocomplete: 'current-password', style: { width: '100%' } });
            const nw = h('input', { type: 'password', autocomplete: 'new-password', style: { width: '100%' } });
            const nw2 = h('input', { type: 'password', autocomplete: 'new-password', style: { width: '100%' } });
            const btn = h('button', { class: 'btn primary', onclick: async () => {
                if (nw.value !== nw2.value) return toast('两次输入的新密码不一样');
                try {
                    await api.post('/api/password', { old: old.value, new: nw.value });
                    toast('密码已修改，其他设备上的登录已失效');
                    old.value = nw.value = nw2.value = '';
                } catch (e: any) {
                    toast(e.message);
                }
            } }, '修改密码');
            box.replaceChildren(h('div', { class: 'form-grid' },
                h('label', null, '原密码'), old, h('label', null, '新密码'), h('div', null, nw, h('div', { class: 'help' }, '至少 10 位')),
                h('label', null, '再输一次'), nw2, h('span'), h('div', null, btn)),
                h('div', { class: 'muted', style: { marginTop: '16px', fontSize: '12.5px', lineHeight: '1.8' } },
                    '忘记密码：在 1Panel 的编排里把环境变量 LIULI_ADMIN_PASSWORD 改成新密码、LIULI_RESET_PASSWORD 改成 1，重建容器后登录，再把 LIULI_RESET_PASSWORD 改回 0。'));
        }
    },
};
