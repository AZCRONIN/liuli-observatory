// 错误日志 / 慢请求：共用一个明细表格，筛选条件放在地址栏里
import { api, LogRow } from '../api';
import { Ctx, rangeParams, setParams } from '../state';
import { Scope } from '../scope';
import { load, pager, sheet } from '../ui';
import { bytes, clear, h, ms, statusClass, time } from '../util';
import { banButton } from '../banui';

function detailRow(r: LogRow, span: number) {
    const item = (k: string, v: string) => [h('dt', null, k), h('dd', null, v || '-')];
    return h('tr', { class: 'detail' }, h('td', { colspan: span },
        h('dl', null,
            item('完整 URI', r.uri), item('来源', r.referer), item('UA', r.ua),
            item('客户端', r.spider ? `蜘蛛：${r.spider}` : `${r.browser} / ${r.os} / ${r.device}`),
            item('耗时', `总 ${ms(r.rt)}，上游 ${ms(r.urt)}`), item('服务器', r.server))));
}

function renderTable(rows: LogRow[], slow: boolean): HTMLElement {
    if (!rows.length) return h('div', { class: 'empty' }, slow ? '这段时间没有超过阈值的慢请求' : '没有符合条件的请求');
    const heads = ['时间', 'IP', '地区', '方法', 'URI', '状态', slow ? '耗时' : '流量', slow ? '上游' : 'UA', ''];
    const tbody = h('tbody');
    for (const r of rows) {
        const tr = h('tr', { class: 'click' },
            h('td', { class: 'num', style: { whiteSpace: 'nowrap' } }, time(r.ts)),
            h('td', { class: 'mono' }, r.ip),
            h('td', { class: 'hide-sm', style: { whiteSpace: 'nowrap' } }, r.region || '-'),
            h('td', { class: 'hide-sm' }, r.method),
            h('td', { class: 'cell-long mono', title: r.uri }, r.uri),
            h('td', null, h('span', { class: 'st ' + statusClass(r.status) }, r.status)),
            slow ? h('td', { class: 'r' }, h('b', null, ms(r.rt))) : h('td', { class: 'r hide-sm' }, bytes(r.bytes)),
            slow ? h('td', { class: 'r hide-sm' }, ms(r.urt)) : h('td', { class: 'cell-long hide-sm muted', title: r.ua, style: { maxWidth: '200px' } }, r.spider ? `蜘蛛：${r.spider}` : r.ua),
            h('td', { class: 'r' }, banButton(r.ip)));
        let open: HTMLElement | null = null;
        tr.addEventListener('click', () => {
            if (open) {
                open.remove();
                open = null;
            } else {
                open = detailRow(r, heads.length);
                tr.after(open);
            }
        });
        tbody.appendChild(tr);
    }
    return h('div', { class: 'table-wrap' }, h('table', null, h('thead', null, h('tr', null, heads.map((x, i) => h('th', { class: i >= 6 ? 'r' : i === 2 || i === 3 ? 'hide-sm' : '' }, x)))), tbody));
}

function makePage(kind: 'errors' | 'slow') {
    const slow = kind === 'slow';
    return {
        title: slow ? '慢请求' : '错误日志',
        needsSite: true,
        render(root: HTMLElement, ctx: Ctx, scope: Scope) {
            const p = ctx.params;
            const status = p.get('status') || (slow ? '' : 'err');
            const page = Number(p.get('page') || 1);
            const input = (name: string, ph: string, width = '160px') => {
                const el = h('input', { type: 'text', placeholder: ph, value: p.get(name) || '', style: { width } });
                el.addEventListener('change', () => setParams({ [name]: el.value.trim(), page: undefined }));
                return el;
            };
            const statusSel = h('select', { 'aria-label': '状态码', onchange: (e: Event) => setParams({ status: (e.target as HTMLSelectElement).value, page: undefined }) },
                (slow
                    ? [['', '全部状态'], ['2xx', '2xx'], ['3xx', '3xx'], ['err', '4xx + 5xx']]
                    : [['err', '4xx + 5xx'], ['4xx', '4xx'], ['5xx', '5xx'], ['404', '404'], ['403', '403'], ['499', '499'], ['500', '500'], ['502', '502'], ['504', '504'], ['all', '全部（不限状态）']]
                ).map(([v, l]) => h('option', { value: v, selected: v === status }, l)));
            if (!slow && !['err', '4xx', '5xx', '404', '403', '499', '500', '502', '504', 'all'].includes(status)) statusSel.appendChild(h('option', { value: status, selected: true }, status));
            const spiderSel = h('select', { 'aria-label': '蜘蛛', onchange: (e: Event) => setParams({ spider: (e.target as HTMLSelectElement).value, page: undefined }) },
                [['', '含蜘蛛'], ['none', '排除蜘蛛'], ['only', '只看蜘蛛']].map(([v, l]) => h('option', { value: v, selected: v === (p.get('spider') || '') }, l)));
            const sp = p.get('spider');
            if (sp && !['none', 'only'].includes(sp)) spiderSel.appendChild(h('option', { value: sp, selected: true }, `蜘蛛：${sp}`));
            const filters = h('div', { class: 'filters' }, statusSel, input('ip', 'IP（精确）', '150px'), input('path', 'URI 包含…', '200px'), spiderSel);
            if (slow) {
                const th = h('input', { type: 'number', min: 10, step: 100, placeholder: '阈值', value: p.get('min_rt') || '', style: { width: '120px' } });
                th.addEventListener('change', () => setParams({ min_rt: th.value, page: undefined }));
                filters.append(h('label', { class: 'field' }, '耗时 ≥', th, '毫秒（留空用设置里的阈值）'));
            }
            const any = ['ip', 'path', 'spider', 'min_rt'].some((k) => p.get(k)) || (p.get('status') && p.get('status') !== (slow ? '' : 'err'));
            if (any) filters.append(h('button', { class: 'btn ghost sm', onclick: () => setParams({ ip: undefined, path: undefined, spider: undefined, min_rt: undefined, status: undefined, page: undefined }) }, '清除筛选'));

            const box = h('div');
            const pagerBox = h('div');
            root.appendChild(sheet(slow ? '慢请求' : '错误请求明细', {
                tip: slow
                    ? '按 request_time 从慢到快排序。只有使用 liuli_json 日志格式时才有耗时；明细保留天数见设置。点击一行展开详情。'
                    : '状态码 ≥ 400 的请求明细。只保留明细保留期内的数据。点击一行展开 UA、来源等详情。',
            }, filters, box, pagerBox));

            const run = () =>
                load(box, api.get<{ rows: LogRow[]; total: number; page: number; size: number }>('/api/logs', {
                    ...rangeParams(ctx), kind, status: status === 'all' ? '' : status, ip: p.get('ip'), path: p.get('path'),
                    spider: p.get('spider'), min_rt: p.get('min_rt'), page, size: 50,
                }), (r) => {
                    clear(pagerBox);
                    if (r.total > r.size) pagerBox.appendChild(pager(r.page, r.size, r.total, (n) => setParams({ page: n })));
                    else if (r.total) pagerBox.appendChild(h('div', { class: 'pager' }, `共 ${r.total} 条`));
                    return renderTable(r.rows, slow);
                });
            run();
            scope.onRefresh(run);
        },
    };
}

export const errorsPage = makePage('errors');
export const slowPage = makePage('slow');
