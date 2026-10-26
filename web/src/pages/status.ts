import { api, DimRow } from '../api';
import { areaGradient, base, cssVar, merge, statusColor } from '../charts';
import { Ctx, go, rangeParams } from '../state';
import { Scope } from '../scope';
import { barCell, load, sheet, table } from '../ui';
import { esc, h, hm, md, num, statusClass } from '../util';

const CLASSES: ['s2' | 's3' | 's4' | 's5', string][] = [['s2', '2xx'], ['s3', '3xx'], ['s4', '4xx'], ['s5', '5xx']];

const MEANING: Record<string, string> = {
    '200': '成功', '204': '无内容', '206': '部分内容', '301': '永久重定向', '302': '临时重定向', '304': '未修改（缓存）', '307': '临时重定向',
    '308': '永久重定向', '400': '请求有误', '401': '未认证', '403': '禁止访问', '404': '不存在', '405': '方法不允许', '408': '请求超时',
    '413': '请求体过大', '429': '请求过多', '444': '连接被关闭', '499': '客户端提前断开', '500': '服务器内部错误', '502': '网关错误（上游挂了）',
    '503': '服务不可用', '504': '网关超时（上游太慢）',
};

export const statusPage = {
    title: '状态码',
    needsSite: true,
    render(root: HTMLElement, ctx: Ctx, scope: Scope) {
        const rp = rangeParams(ctx);
        const statBox = h('div');
        const trendEl = h('div', { class: 'chart' });
        const trend = scope.chart(trendEl);
        const box = h('div');
        root.append(
            h('section', { class: 'sheet', style: { marginBottom: '16px' } }, statBox),
            h('div', { class: 'grid cols-3' },
                h('div', { class: 'span-2' }, sheet('状态码趋势', { tip: '按类别堆叠：2xx 绿、3xx 蓝、4xx 琥珀、5xx 红。' }, trendEl)),
                sheet('各状态码', { tip: '点击一行查看对应的请求明细' }, box)),
        );
        const run = () => {
            load(box, api.get<{ rows: DimRow[] }>('/api/dim', { ...rp, dim: 'status', limit: 100 }), (r) => {
                const total = r.rows.reduce((s, x) => s + x.req, 0);
                const byCls: Record<string, number> = { s2: 0, s3: 0, s4: 0, s5: 0 };
                r.rows.forEach((x) => (byCls[statusClass(x.key)] += x.req));
                statBox.replaceChildren(h('div', { class: 'stat-row' },
                    h('div', { class: 'stat' }, h('div', { class: 'k' }, '总请求'), h('div', { class: 'v' }, num(total))),
                    CLASSES.map(([c, l]) => h('div', { class: 'stat' },
                        h('div', { class: 'k' }, h('span', { class: 'legend-dot', style: { background: statusColor(c) } }), l),
                        h('div', { class: 'v' }, num(byCls[c]), h('span', { class: 'muted', style: { fontSize: '13px', fontWeight: '400', marginLeft: '6px' } }, total ? `${((byCls[c] / total) * 100).toFixed(1)}%` : ''))))));
                const max = Math.max(1, ...r.rows.map((x) => x.req));
                return table(r.rows, [
                    { title: '状态码', get: (x) => h('span', { class: 'st ' + statusClass(x.key) }, x.key) },
                    { title: '含义', get: (x) => MEANING[x.key] || '' },
                    { title: '次数', cls: 'r', get: (x) => barCell(x.req, max) },
                ], { onRow: (x) => go('errors', { status: x.key }) });
            });
            trendEl.closest('.sheet')!.classList.add('loading');
            api.get<{ gran: number; buckets: number[]; series: Record<string, number[]>; now: number }>('/api/dimseries', { ...rp, dim: 'status' })
                .then((d) => {
                    const agg: Record<string, number[]> = { s2: [], s3: [], s4: [], s5: [] };
                    for (const k of Object.keys(agg)) agg[k] = d.buckets.map(() => 0);
                    for (const [code, arr] of Object.entries(d.series)) arr.forEach((v, i) => (agg[statusClass(code)][i] += v));
                    const labels = d.buckets.map((b) => (d.gran >= 86400 ? md(b) : d.buckets.length > 30 ? `${md(b)} ${hm(b)}` : hm(b)));
                    trend.setOption(merge(base(), {
                        legend: { show: true, top: 0, right: 0, icon: 'circle', itemWidth: 8, itemHeight: 8, textStyle: { color: cssVar('--ink-2') } },
                        grid: { left: 8, right: 16, top: 34, bottom: 8, containLabel: true },
                        tooltip: { trigger: 'axis', formatter: (ps: any[]) => `${esc(ps[0].axisValue)}<br/>` + ps.map((p) => `<span class="legend-dot" style="background:${p.color}"></span>${esc(p.seriesName)}　<b>${num(p.value)}</b>`).join('<br/>') },
                        xAxis: { data: labels },
                        series: CLASSES.map(([c, l]) => ({
                            name: l, type: 'line', stack: 'all', smooth: 0.3, showSymbol: false,
                            data: agg[c].map((v, i) => (d.buckets[i] > d.now || (d.gran < 86400 && d.buckets[i] + d.gran > d.now) ? null : v)),
                            lineStyle: { width: 1.4, color: statusColor(c) }, itemStyle: { color: statusColor(c) },
                            areaStyle: { color: areaGradient(statusColor(c), 0.35) },
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
