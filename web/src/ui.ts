// 页面里反复用到的组件：面板、维度表格、加载状态、分页、维度图表。
import { DimRow } from './api';
import { Chart, alpha, base, cssVar, merge, palette } from './charts';
import { bytes, clear, esc, h, num, tip } from './util';

export function sheet(title: string, opts: { tip?: string; extra?: (Node | string)[]; cls?: string } = {}, ...body: Node[]) {
    const head = h('div', { class: 'sheet-head' }, h('h2', null, title, opts.tip ? tip(opts.tip) : null), h('div', { class: 'grow' }), ...(opts.extra || []));
    return h('section', { class: 'sheet ' + (opts.cls || '') }, head, ...body);
}

// 把异步结果渲染进 el：加载中显示行进虚线，失败显示红色虚线框
export async function load<T>(el: HTMLElement, p: Promise<T>, render: (v: T) => void | Node | Node[]): Promise<T | undefined> {
    const host = (el.closest('.sheet') as HTMLElement) || el;
    host.classList.add('loading');
    try {
        const v = await p;
        const out = render(v);
        if (out !== undefined) {
            clear(el);
            if (Array.isArray(out)) out.forEach((n) => el.appendChild(n));
            else el.appendChild(out);
        }
        return v;
    } catch (e: any) {
        clear(el);
        el.appendChild(h('div', { class: 'err-line' }, '加载失败：' + (e?.message || e)));
        return undefined;
    } finally {
        host.classList.remove('loading');
    }
}

export const empty = (text = '这段时间没有数据') => h('div', { class: 'empty' }, text);

export interface Col<T> {
    title: string;
    cls?: string;
    get: (r: T, i: number) => Node | string | number | null;
}

export function table<T>(rows: T[], cols: Col<T>[], opts: { onRow?: (r: T) => void; emptyText?: string } = {}): HTMLElement {
    if (!rows.length) return empty(opts.emptyText);
    const thead = h('thead', null, h('tr', null, cols.map((c) => h('th', { class: c.cls }, c.title))));
    const tbody = h(
        'tbody',
        null,
        rows.map((r, i) =>
            h(
                'tr',
                { class: opts.onRow ? 'click' : undefined, onclick: opts.onRow ? () => opts.onRow!(r) : undefined },
                cols.map((c) => {
                    const v = c.get(r, i);
                    return h('td', { class: c.cls }, v === null ? '-' : v instanceof Node ? v : String(v));
                }),
            ),
        ),
    );
    return h('div', { class: 'table-wrap' }, h('table', null, thead, tbody));
}

export function rank(i: number) {
    return h('span', { class: 'rank' + (i < 3 ? ' top' : '') }, i + 1);
}

// 带占比条的数字单元格
export function barCell(v: number, max: number, text?: string) {
    const w = max > 0 ? Math.max(2, (v / max) * 100) : 0;
    return h('span', { style: { display: 'inline-flex', alignItems: 'center', gap: '10px', justifyContent: 'flex-end', width: '100%' } },
        h('span', { style: { width: '90px', height: '6px', borderRadius: '3px', background: cssVar('--c-main-soft'), overflow: 'hidden', flexShrink: '0' }, class: 'hide-sm' },
            h('span', { style: { display: 'block', height: '100%', width: w + '%', background: cssVar('--c-main'), borderRadius: '3px' } })),
        text ?? num(v));
}

export function pager(page: number, size: number, total: number, onGo: (p: number) => void): HTMLElement {
    const pages = Math.max(1, Math.ceil(total / size));
    return h('div', { class: 'pager' },
        `共 ${num(total)} 条`,
        h('button', { class: 'btn sm', disabled: page <= 1, onclick: () => onGo(page - 1) }, '上一页'),
        h('span', { class: 'num' }, `${page} / ${pages}`),
        h('button', { class: 'btn sm', disabled: page >= pages, onclick: () => onGo(page + 1) }, '下一页'));
}

// 维度占比：环形图
export function donut(c: Chart, rows: DimRow[], value: (r: DimRow) => number, label: (r: DimRow) => string = (r) => r.key, top = 8) {
    const sorted = [...rows].sort((a, b) => value(b) - value(a));
    const head = sorted.slice(0, top).map((r) => ({ name: label(r), value: value(r) }));
    const rest = sorted.slice(top).reduce((s, r) => s + value(r), 0);
    if (rest > 0) head.push({ name: '其他', value: rest });
    const pal = palette();
    c.setOption(
        merge(base(), {
            xAxis: undefined,
            yAxis: undefined,
            tooltip: { trigger: 'item', formatter: (p: any) => `${esc(p.name)}<br/><b>${num(p.value)}</b>　${p.percent}%` },
            legend: { show: true, type: 'scroll', orient: 'vertical', right: 0, top: 'middle', icon: 'circle', itemWidth: 8, itemHeight: 8, textStyle: { color: cssVar('--ink-2'), fontSize: 12 } },
            series: [{
                type: 'pie', radius: ['52%', '78%'], center: ['32%', '50%'], avoidLabelOverlap: true, label: { show: false },
                itemStyle: { borderColor: cssVar('--sheet'), borderWidth: 2, borderRadius: 4 },
                data: head.map((d, i) => ({ ...d, itemStyle: { color: d.name === '其他' ? alpha(cssVar('--ink-3'), 0.4) : pal[i % pal.length] } })),
            }],
        }),
        true,
    );
}

// 维度排行：横向柱状图
export function hbar(c: Chart, rows: { name: string; value: number }[], color = cssVar('--c-main'), fmt: (v: number) => string = num) {
    const data = [...rows].reverse();
    c.setOption(
        merge(base(), {
            grid: { left: 8, right: 48, top: 4, bottom: 4, containLabel: true },
            tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' }, formatter: (ps: any) => `${esc(ps[0].name)}<br/><b>${fmt(ps[0].value)}</b>` },
            xAxis: { type: 'value', boundaryGap: [0, 0.05], splitLine: { show: false }, axisLabel: { show: false }, axisLine: { show: false } },
            yAxis: { type: 'category', data: data.map((d) => d.name), axisLabel: { color: cssVar('--ink-2'), fontSize: 12, width: 110, overflow: 'truncate' }, splitLine: { show: false } },
            series: [{ type: 'bar', data: data.map((d) => d.value), barMaxWidth: 14, itemStyle: { color, borderRadius: [0, 4, 4, 0] },
                label: { show: true, position: 'right', color: cssVar('--ink-3'), fontSize: 11.5, formatter: (p: any) => fmt(p.value) } }],
        }),
        true,
    );
}

export function bytesCell(v: number) {
    return bytes(v);
}
