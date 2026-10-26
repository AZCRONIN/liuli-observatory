import { api, DimRow } from '../api';
import { areaGradient, base, cssVar, merge, palette } from '../charts';
import { Ctx, go, rangeParams, setParams } from '../state';
import { Scope } from '../scope';
import { barCell, donut, load, rank, sheet, table } from '../ui';
import { ago, bytes, esc, h, hm, md, num } from '../util';

export const spiderPage = {
    title: '蜘蛛',
    needsSite: true,
    render(root: HTMLElement, ctx: Ctx, scope: Scope) {
        const rp = rangeParams(ctx);
        const spider = ctx.params.get('spider') || '';
        const statBox = h('div');
        const trendEl = h('div', { class: 'chart' });
        const donutEl = h('div', { class: 'chart sm' });
        const listBox = h('div');
        const uriBox = h('div');
        const trend = scope.chart(trendEl);
        const pie = scope.chart(donutEl);
        const sel = h('select', { 'aria-label': '筛选蜘蛛', onchange: (e: Event) => setParams({ spider: (e.target as HTMLSelectElement).value }) }, h('option', { value: '' }, '全部蜘蛛'));

        root.append(
            h('section', { class: 'sheet', style: { marginBottom: '16px' } }, statBox),
            h('div', { class: 'grid cols-3', style: { marginBottom: '16px' } },
                h('div', { class: 'span-2' }, sheet('抓取趋势', { tip: '前 6 个蜘蛛的抓取次数。单日按小时，多天按天。' }, trendEl)),
                sheet('分类占比', { tip: '搜索引擎、AI 爬虫、SEO 工具、社交预览、其他机器人（监控、脚本工具、空 UA 等）' }, donutEl)),
            h('div', { class: 'grid cols-2' },
                sheet('各蜘蛛抓取', { tip: '点击一行查看该蜘蛛的抓取明细' }, listBox),
                sheet('抓取最多的 URL', { extra: [sel] }, uriBox)),
        );

        const run = () => {
            load(listBox, api.get<{ rows: DimRow[] }>('/api/dim', { ...rp, dim: 'spider', limit: 100 }), (r) => {
                // 统计行
                const total = r.rows.reduce((s, x) => s + x.req, 0);
                const byCat: Record<string, number> = {};
                r.rows.forEach((x) => (byCat[x.extra || '其他'] = (byCat[x.extra || '其他'] || 0) + x.req));
                statBox.replaceChildren(h('div', { class: 'stat-row' },
                    h('div', { class: 'stat' }, h('div', { class: 'k' }, '总抓取'), h('div', { class: 'v' }, num(total))),
                    Object.entries(byCat).sort((a, b) => b[1] - a[1]).map(([k, v]) => h('div', { class: 'stat' }, h('div', { class: 'k' }, k), h('div', { class: 'v' }, num(v))))));
                donut(pie, Object.entries(byCat).map(([k, v]) => ({ key: k, req: v, pv: 0, uv: 0, bytes: 0, last: 0 })), (x) => x.req);
                // 下拉选项
                sel.replaceChildren(h('option', { value: '' }, '全部蜘蛛'), ...r.rows.map((x) => h('option', { value: x.key, selected: x.key === spider }, x.key)));
                const max = Math.max(1, ...r.rows.map((x) => x.req));
                return table(r.rows, [
                    { title: '#', get: (_, i) => rank(i) },
                    { title: '蜘蛛', get: (x) => h('b', null, x.key) },
                    { title: '分类', get: (x) => h('span', { class: 'chip' }, x.extra || '') },
                    { title: '抓取', cls: 'r', get: (x) => barCell(x.req, max) },
                    { title: '流量', cls: 'r hide-sm', get: (x) => bytes(x.bytes) },
                    { title: '最近', cls: 'r', get: (x) => ago(x.last) },
                ], { onRow: (x) => go('errors', { spider: x.key, status: 'all' }) });
            });
            load(uriBox, api.get<{ rows: DimRow[] }>('/api/dim', { ...rp, dim: 'spider_uri', limit: 50, spider }), (r) => {
                const max = Math.max(1, ...r.rows.map((x) => x.req));
                return table(r.rows, [
                    { title: '#', get: (_, i) => rank(i) },
                    { title: 'URL', cls: 'cell-long', get: (x) => h('span', { class: 'mono', title: x.key }, x.key) },
                    { title: '抓取', cls: 'r', get: (x) => barCell(x.req, max) },
                ]);
            });
            trendEl.closest('.sheet')!.classList.add('loading');
            api.get<{ gran: number; buckets: number[]; series: Record<string, number[]>; now: number }>('/api/dimseries', { ...rp, dim: 'spider' })
                .then((d) => {
                    const top = Object.entries(d.series).map(([k, v]) => [k, v, v.reduce((s, x) => s + x, 0)] as const).sort((a, b) => b[2] - a[2]).slice(0, 6);
                    const pal = palette();
                    const labels = d.buckets.map((b) => (d.gran >= 86400 ? md(b) : d.buckets.length > 30 ? `${md(b)} ${hm(b)}` : hm(b)));
                    trend.setOption(merge(base(), {
                        legend: { show: true, top: 0, right: 0, icon: 'circle', itemWidth: 8, itemHeight: 8, textStyle: { color: cssVar('--ink-2') } },
                        grid: { left: 8, right: 16, top: 34, bottom: 8, containLabel: true },
                        tooltip: { trigger: 'axis', formatter: (ps: any[]) => `${esc(ps[0].axisValue)}<br/>` + ps.filter((p) => p.value !== null).map((p) => `<span class="legend-dot" style="background:${p.color}"></span>${esc(p.seriesName)}　<b>${num(p.value)}</b>`).join('<br/>') },
                        xAxis: { data: labels },
                        series: top.map(([k, v], i) => ({
                            name: k, type: 'line', smooth: 0.3, showSymbol: false, data: v.map((x, j) => (d.buckets[j] > d.now ? null : x)),
                            lineStyle: { width: 1.8, color: pal[i % pal.length] }, itemStyle: { color: pal[i % pal.length] },
                            areaStyle: i === 0 ? { color: areaGradient(pal[0], 0.18) } : undefined,
                        })),
                    }), true);
                })
                .catch(() => trend.clear())
                .finally(() => trendEl.closest('.sheet')!.classList.remove('loading'));
        };
        run();
        scope.onRefresh(run);
    },
};
