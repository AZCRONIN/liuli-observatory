import { api, DimRow } from '../api';
import { Ctx, go, rangeParams, setParams } from '../state';
import { Scope } from '../scope';
import { barCell, donut, load, rank, sheet, table } from '../ui';
import { bytes, h, num } from '../util';

const TABS: [string, string][] = [['page', 'Top 页面'], ['uri', 'Top URI（全部请求）'], ['ref', 'Top 来源'], ['search', '搜索引擎来源']];

export const uriPage = {
    title: 'URI / 来源',
    needsSite: true,
    render(root: HTMLElement, ctx: Ctx, scope: Scope) {
        const rp = rangeParams(ctx);
        const tab = TABS.find((t) => t[0] === ctx.params.get('tab'))?.[0] || 'page';
        const box = h('div');
        const seg = h('div', { class: 'seg' }, TABS.map(([k, l]) => h('button', { 'aria-pressed': String(k === tab), onclick: () => setParams({ tab: k }) }, l)));
        const tips: Record<string, string> = {
            page: '只统计算作浏览量的页面请求（排除静态资源、接口、蜘蛛），按浏览量排序。路径不含查询串。',
            uri: '所有请求（含静态资源、接口、蜘蛛），按请求数排序，可看出哪些资源最耗流量。',
            ref: '来源页面的域名（Referer），不含站内跳转和直接访问，不含蜘蛛，按浏览量排序。',
            search: '来源域名属于搜索引擎（百度、谷歌、必应、搜狗、360、神马、夸克等）的访问。',
        };
        const extraEl = h('div');
        root.append(h('div', { style: { marginBottom: '14px' } }, seg));
        if (tab === 'search') {
            const chartEl = h('div', { class: 'chart sm' });
            const chart = scope.chart(chartEl);
            root.appendChild(h('div', { class: 'grid cols-2' }, sheet('搜索引擎来源', { tip: tips.search }, box), sheet('占比', {}, chartEl)));
            const run = () => load(box, api.get<{ rows: DimRow[] }>('/api/dim', { ...rp, dim: 'search' }), (r) => {
                donut(chart, r.rows, (x) => x.pv);
                const max = Math.max(1, ...r.rows.map((x) => x.pv));
                return table(r.rows, [
                    { title: '#', get: (_, i) => rank(i) },
                    { title: '搜索引擎', get: (x) => h('b', null, x.key) },
                    { title: '浏览量', cls: 'r', get: (x) => barCell(x.pv, max) },
                    { title: '访客', cls: 'r', get: (x) => num(x.uv) },
                ], { emptyText: '这段时间没有来自搜索引擎的访问' });
            });
            run();
            scope.onRefresh(run);
            return;
        }
        root.appendChild(sheet(TABS.find((t) => t[0] === tab)![1], { tip: tips[tab] }, box, extraEl));
        const run = () =>
            load(box, api.get<{ rows: DimRow[] }>('/api/dim', { ...rp, dim: tab, limit: 200, sort: tab === 'uri' ? 'req' : 'pv' }), (r) => {
                const key = tab === 'uri' ? 'req' : 'pv';
                const max = Math.max(1, ...r.rows.map((x) => x[key]));
                const cols =
                    tab === 'uri'
                        ? [
                              { title: '#', get: (_: DimRow, i: number) => rank(i) },
                              { title: 'URI', cls: 'cell-long', get: (x: DimRow) => h('span', { class: 'mono', title: x.key }, x.key) },
                              { title: '请求', cls: 'r', get: (x: DimRow) => barCell(x.req, max) },
                              { title: '浏览量', cls: 'r hide-sm', get: (x: DimRow) => num(x.pv) },
                              { title: '流量', cls: 'r', get: (x: DimRow) => bytes(x.bytes) },
                          ]
                        : [
                              { title: '#', get: (_: DimRow, i: number) => rank(i) },
                              { title: tab === 'ref' ? '来源域名' : '页面', cls: 'cell-long', get: (x: DimRow) => h('span', { class: tab === 'page' ? 'mono' : '', title: x.key }, x.key) },
                              { title: '浏览量', cls: 'r', get: (x: DimRow) => barCell(x.pv, max) },
                              { title: '访客', cls: 'r', get: (x: DimRow) => num(x.uv) },
                              { title: '请求', cls: 'r hide-sm', get: (x: DimRow) => num(x.req) },
                          ];
                return table(tab === 'uri' ? r.rows : r.rows.filter((x) => x.pv > 0), cols, {
                    onRow: tab === 'ref' ? undefined : (x) => go('errors', { path: x.key, status: 'all' }),
                });
            });
        run();
        scope.onRefresh(run);
    },
};
