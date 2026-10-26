import { api, DimRow, Point, Totals } from '../api';
import { areaGradient, base, cssVar, merge, statusColor } from '../charts';
import { Ctx, rangeParams, save, store } from '../state';
import { Scope } from '../scope';
import { barCell, empty, load, rank, sheet, table } from '../ui';
import { bytes, bytesParts, esc, h, hm, md, ms, num, tip, time } from '../util';

// 指标口径：和需求说明第 5 节一致，问号提示里写清楚
export const METRIC_TIPS = {
    pv: '浏览量：非蜘蛛、状态码 2xx/3xx、GET 方式的页面类请求。排除 js/css/图片/字体/视频等静态资源，以及 /api/ 等接口路径（规则可在「设置」里改）。',
    uv: '访客：按（IP + 浏览器 UA）去重的访客数，不含蜘蛛。多天时为每天去重数之和。',
    ip: 'IP 数：去重的来源 IP 数（含蜘蛛）。多天时为每天去重数之和。',
    req: '请求：所有日志行，包括静态资源、接口、蜘蛛和错误请求。',
    bytes: '流量：响应发给访客的字节数之和（bytes_sent，出站）。',
    spider: '蜘蛛爬虫：浏览器 UA 命中已知蜘蛛 / 机器人列表的请求数，包括搜索引擎、AI 爬虫、SEO 工具、脚本工具和空 UA。',
    active: '最近 10 分钟的去重访客数（IP + UA，不含蜘蛛）。下方小柱是每分钟的访客数。',
};

type MetricKey = 'pv' | 'uv' | 'ip' | 'bytes' | 'req' | 'spider';
const METRICS: [MetricKey, string][] = [['pv', '浏览量(PV)'], ['uv', '访客量(UV)'], ['ip', 'IP数'], ['req', '请求'], ['bytes', '流量'], ['spider', '蜘蛛爬虫']];

function delta(cur: number, prev: number) {
    if (!prev) return null;
    const d = (cur - prev) / prev;
    if (Math.abs(d) < 0.0005) return h('span', { class: 'delta' }, '持平');
    return h('span', { class: 'delta ' + (d > 0 ? 'up' : 'down') }, `${d > 0 ? '▲' : '▼'}${Math.abs(d * 100).toFixed(1)}%`);
}

function metricValue(k: MetricKey, t: Totals): Node[] {
    if (k === 'bytes') {
        const [v, u] = bytesParts(t.bytes);
        return [document.createTextNode(v), h('small', null, u)];
    }
    return [document.createTextNode(num(t[k]))];
}

function fmtMetric(k: string, v: number) {
    return k === 'bytes' ? bytes(v) : num(v);
}

export const overview = {
    title: '概览',
    needsSite: true,
    render(root: HTMLElement, ctx: Ctx, scope: Scope) {
        const rp = rangeParams(ctx);
        const singleDay = ctx.range === 'today' || ctx.range === 'yesterday' || (ctx.range === 'custom' && ctx.from === ctx.to);

        // ---- 10 分钟活跃 + 六个指标 ----
        const activeNum = h('div', { class: 'big num' }, '-');
        const bars = h('div', { class: 'minibars', 'aria-hidden': 'true' }, Array.from({ length: 10 }, () => h('i')));
        const activeBox = h('section', { class: 'sheet active-box' },
            h('div', { class: 'label' }, h('span', { class: 'live-dot' }), '10 分钟活跃用户', tip(METRIC_TIPS.active)), activeNum, bars);
        const metrics = h('div', { class: 'metrics' });
        const kpis = h('div', { class: 'kpis' }, activeBox, h('section', { class: 'sheet' }, metrics));

        const loadActive = async () => {
            try {
                const r = await api.get<{ active: number; bars: number[] }>('/api/active', { site: ctx.site, server: ctx.server });
                activeNum.textContent = num(r.active);
                const max = Math.max(1, ...r.bars);
                r.bars.forEach((v, i) => ((bars.children[i] as HTMLElement).style.height = `${Math.max(4, (v / max) * 100)}%`));
            } catch { /* 下次再试 */ }
        };
        const loadCards = () =>
            load(metrics, api.get<{ cur: Totals; cmp: Totals; cmp_label: string; cmp_partial: boolean; first_ts: number }>('/api/overview', rp), (r) =>
                METRICS.map(([k, label]) =>
                    h('div', { class: 'metric' },
                        h('div', { class: 'label' }, label, tip((METRIC_TIPS as any)[k])),
                        h('div', { class: 'value num' }, metricValue(k, r.cur)),
                        r.cmp_partial
                            // 站点中途才开始统计，对比时段开头没有数据，不显示涨幅
                            ? h('div', { class: 'cmp', title: (r.first_ts ? `这个站点从 ${time(r.first_ts)} 开始有数据，` : '') + `${r.cmp_label}不完整，不显示涨幅` }, '对比数据不完整')
                            : h('div', { class: 'cmp num', title: r.cmp_label }, `${r.cmp_label} ${fmtMetric(k, r.cmp[k])}`, delta(r.cur[k], r.cmp[k])))));

        // ---- 趋势 ----
        let metric = store<MetricKey>('trend.metric', 'pv');
        let gran = store<string>('trend.gran', 'hour');
        if (!singleDay && gran === 'minute') gran = 'hour';
        let cmpDay = store<boolean>('trend.cmpDay', true);
        let cmpWeek = store<boolean>('trend.cmpWeek', false);
        const chartEl = h('div', { class: 'chart' });
        const chart = scope.chart(chartEl);

        const radio = (name: string, opts: [string, string][], cur: string, on: (v: string) => void) =>
            h('div', { class: 'radios', role: 'radiogroup' }, opts.map(([v, l]) =>
                h('label', null, h('input', { type: 'radio', name, value: v, checked: v === cur, onchange: () => on(v) }), l)));
        const granOpts: [string, string][] = singleDay ? [['minute', '分钟'], ['hour', '小时']] : [['hour', '小时'], ['day', '天']];
        if (!singleDay && !['hour', 'day'].includes(gran)) gran = 'hour';
        const check = (label: string, cur: boolean, on: (v: boolean) => void) =>
            h('label', null, h('input', { type: 'checkbox', checked: cur, onchange: (e: Event) => on((e.target as HTMLInputElement).checked) }), label);

        const trendHead = [
            h('span', { class: 'radios' }, h('span', { class: 'lbl' }, '指标')),
            radio('metric', METRICS.map(([k, l]) => [k, l] as [string, string]), metric, (v) => { metric = v as MetricKey; save('trend.metric', v); drawTrend(); }),
            h('span', { class: 'grow' }),
            h('span', { class: 'radios' }, h('span', { class: 'lbl' }, '颗粒度'), radio('gran', granOpts, gran, (v) => { gran = v; save('trend.gran', v); loadTrend(); })),
            singleDay ? h('span', { class: 'radios' }, h('span', { class: 'lbl' }, '对比'),
                check('前一天', cmpDay, (v) => { cmpDay = v; save('trend.cmpDay', v); loadTrend(); }),
                check('上周同期', cmpWeek, (v) => { cmpWeek = v; save('trend.cmpWeek', v); loadTrend(); })) : null,
        ].filter(Boolean) as Node[];
        const trend = h('section', { class: 'sheet', style: { marginBottom: '16px' } },
            h('div', { class: 'sheet-head' }, h('h2', null, '趋势'), ...trendHead), chartEl);

        let trendData: { gran: number; now: number; first_ts: number; cur: Point[]; prev_day?: Point[]; prev_week?: Point[] } | null = null;
        const loadTrend = async () => {
            const compare = singleDay ? [cmpDay && 'day', cmpWeek && 'week'].filter(Boolean).join(',') : '';
            trend.classList.add('loading');
            try {
                trendData = await api.get('/api/trend', { ...rp, gran, compare });
                drawTrend();
            } catch (e: any) {
                chart.clear();
            } finally {
                trend.classList.remove('loading');
            }
        };
        const drawTrend = () => {
            if (!trendData) return;
            const d = trendData;
            const g = d.gran;
            const labels = d.cur.map((p) => (g >= 86400 ? md(p.ts) : g === 3600 && !singleDay ? `${md(p.ts)} ${hm(p.ts)}` : hm(p.ts)));
            const val = (p: Point) => (p as any)[metric] as number;
            const main = cssVar('--c-main');
            const c1 = cssVar('--c-cmp1');
            const c2 = cssVar('--c-cmp2');
            const series: any[] = [];
            const line = (name: string, data: (number | null)[], color: string, opts: any = {}) => ({
                name, type: 'line', data, smooth: 0.35, showSymbol: data.length <= 48, symbolSize: 5,
                lineStyle: { width: 2, color }, itemStyle: { color }, emphasis: { focus: 'none' }, ...opts,
            });
            // 站点开始统计之前的时段不画（画成 0 会让人以为那时没人访问）
            const known = (p: Point) => (d.first_ts && p.ts + g <= d.first_ts ? null : val(p));
            if (d.prev_week) series.push(line('上周同期', d.prev_week.map(known), c2, { lineStyle: { width: 1.4, color: c2, type: 'dashed' }, showSymbol: false }));
            if (d.prev_day) series.push(line('前一天', d.prev_day.map(known), c1, { lineStyle: { width: 1.4, color: c1, type: 'dashed' }, areaStyle: { color: areaGradient(c1, 0.1) }, showSymbol: false }));
            // 今天还没到的时段不画
            const name = METRICS.find((m) => m[0] === metric)![1];
            const cur = d.cur.map((p) => (p.ts > d.now ? null : known(p)));
            // 还没结束的那个时段（例如 22:05 时的 22 点）只统计了一部分，画成虚线，免得看起来像流量骤降
            const live = d.cur.findIndex((p) => p.ts <= d.now && d.now < p.ts + g);
            if (live > 0 && cur[live] !== null && cur[live - 1] !== null) {
                const partial = cur.map((v, i) => (i === live - 1 || i === live ? v : null));
                cur[live] = null;
                series.push(line(`${name}（本时段进行中）`, partial, main, {
                    lineStyle: { width: 2, color: main, type: 'dashed' }, showSymbol: true, symbol: 'emptyCircle', z: 3,
                    tooltip: { show: true },
                }));
            }
            series.push(line(name, cur, main, { areaStyle: { color: areaGradient(main, 0.3) }, z: 3 }));
            const fmt = (v: number) => (metric === 'bytes' ? bytes(v, 1) : num(v));
            chart.setOption(merge(base(), {
                tooltip: {
                    trigger: 'axis',
                    formatter: (ps: any[]) => `<div style="margin-bottom:4px;color:${cssVar('--ink-3')}">${esc(ps[0]?.axisValue)}</div>` +
                        // 虚线段只在「进行中」那个点显示，前一个点和实线重复
                        ps.filter((p) => p.value !== null && p.value !== undefined && !(p.seriesName.endsWith('进行中）') && p.dataIndex !== live)).reverse()
                            .map((p) => `<div><span class="legend-dot" style="background:${p.color}"></span>${esc(p.seriesName)}　<b>${fmt(p.value)}</b></div>`).join(''),
                },
                xAxis: { data: labels },
                yAxis: { axisLabel: { formatter: (v: number) => (metric === 'bytes' ? bytes(v, 0) : num(v)) } },
                series,
            }), true);
        };

        // ---- 性能 + 实时流量 ----
        const perfEl = h('div', { class: 'chart sm' });
        const flowEl = h('div', { class: 'chart sm' });
        const perf = scope.chart(perfEl);
        const flow = scope.chart(flowEl);
        const qpsNow = h('span', { class: 'num' }, '-');
        const rtNow = h('span', { class: 'num' }, '-');
        const upNow = h('span', { class: 'num' }, '-');
        const downNow = h('span', { class: 'num' }, '-');
        const dot = (c: string) => h('span', { class: 'legend-dot', style: { background: cssVar(c) } });
        const perfSheet = sheet('性能', {
            tip: 'QPS：每秒请求数。平均响应耗时：request_time 的平均值（从读到请求到发完响应）。最近 60 秒，每 3 秒刷新。',
            extra: [h('span', { class: 'note' }, dot('--c-main'), 'QPS ', qpsNow, dot('--c-cmp1'), '平均响应耗时 ', rtNow)],
        }, perfEl);
        const flowSheet = sheet('实时流量', {
            tip: '上行：访客发来的请求字节数（request_length）；下行：发给访客的字节数（bytes_sent）。最近 60 秒，每 3 秒刷新。',
            extra: [h('span', { class: 'note' }, dot('--c-main'), '上行 ', upNow, dot('--c-cmp1'), '下行 ', downNow)],
        }, flowEl);

        const loadRealtime = async () => {
            let r: { points: { ts: number; req: number; rt_avg: number; bytes: number; req_bytes: number }[] };
            try {
                r = await api.get('/api/realtime', { site: ctx.site, server: ctx.server, seconds: 60 });
            } catch {
                return;
            }
            const pts = r.points;
            const labels = pts.map((p) => hm(p.ts) + ':' + String(new Date(p.ts * 1000).getSeconds()).padStart(2, '0'));
            const last5 = pts.slice(-5);
            const qps = last5.reduce((s, p) => s + p.req, 0) / Math.max(1, last5.length);
            const rtPts = last5.filter((p) => p.req > 0);
            const rt = rtPts.length ? rtPts.reduce((s, p) => s + p.rt_avg * p.req, 0) / rtPts.reduce((s, p) => s + p.req, 0) : 0;
            qpsNow.textContent = qps.toFixed(qps < 10 ? 1 : 0) + ' 次';
            rtNow.textContent = rtPts.length ? ms(rt) : '-';
            upNow.textContent = bytes(last5.reduce((s, p) => s + p.req_bytes, 0) / Math.max(1, last5.length), 1) + '/s';
            downNow.textContent = bytes(last5.reduce((s, p) => s + p.bytes, 0) / Math.max(1, last5.length), 1) + '/s';
            const main = cssVar('--c-main');
            const c1 = cssVar('--c-cmp1');
            const rtLine = (name: string, data: number[], color: string, yi = 0, area = true) => ({
                name, type: 'line', data, smooth: 0.3, showSymbol: false, yAxisIndex: yi, lineStyle: { width: 1.6, color }, itemStyle: { color },
                areaStyle: area ? { color: areaGradient(color, 0.22) } : undefined,
            });
            const common = merge(base(), { animation: false, grid: { left: 8, right: 8, top: 16, bottom: 4, containLabel: true }, xAxis: { data: labels, axisLabel: { interval: 14 } } });
            perf.setOption(merge(common, {
                tooltip: { trigger: 'axis', formatter: (ps: any[]) => `${esc(ps[0].axisValue)}<br/>QPS <b>${num(ps[0].value)}</b><br/>平均耗时 <b>${ms(ps[1]?.value)}</b>` },
                yAxis: [{ ...base().yAxis as any, minInterval: 1 }, { ...base().yAxis as any, splitLine: { show: false }, axisLabel: { color: cssVar('--ink-3'), fontSize: 11.5, formatter: (v: number) => `${v}ms` } }],
                series: [rtLine('QPS', pts.map((p) => p.req), main), rtLine('平均耗时', pts.map((p) => Math.round(p.rt_avg)), c1, 1, false)],
            }), true);
            flow.setOption(merge(common, {
                tooltip: { trigger: 'axis', formatter: (ps: any[]) => `${esc(ps[0].axisValue)}<br/>上行 <b>${bytes(ps[0].value, 1)}</b><br/>下行 <b>${bytes(ps[1].value, 1)}</b>` },
                yAxis: { axisLabel: { formatter: (v: number) => bytes(v, 0) } },
                series: [rtLine('上行', pts.map((p) => p.req_bytes), main), rtLine('下行', pts.map((p) => p.bytes), c1)],
            }), true);
        };

        // ---- 速览：状态码、Top 页面、Top 来源 ----
        const stBox = h('div');
        const pageBox = h('div');
        const refBox = h('div');
        const quick = h('div', { class: 'grid cols-3', style: { marginTop: '16px' } },
            sheet('状态码', { extra: [h('a', { class: 'note', href: '#/status' }, '详情')] }, stBox),
            sheet('Top 页面', { tip: '按浏览量排序', extra: [h('a', { class: 'note', href: '#/uri' }, '更多')] }, pageBox),
            sheet('Top 来源', { tip: '站外来源域名（Referer），按浏览量排序', extra: [h('a', { class: 'note', href: '#/uri?tab=ref' }, '更多')] }, refBox));
        const loadQuick = () => {
            load(stBox, api.get<{ cur: Totals }>('/api/overview', rp), (r) => {
                const t = r.cur;
                const total = t.s2 + t.s3 + t.s4 + t.s5;
                if (!total) return empty();
                const rows: [string, number, 's2' | 's3' | 's4' | 's5'][] = [['2xx 成功', t.s2, 's2'], ['3xx 重定向', t.s3, 's3'], ['4xx 客户端错误', t.s4, 's4'], ['5xx 服务端错误', t.s5, 's5']];
                return h('div', { style: { display: 'grid', gap: '12px', paddingTop: '4px' } }, rows.map(([l, v, c]) =>
                    h('a', { href: c === 's2' || c === 's3' ? '#/status' : `#/errors?status=${c[1]}xx`, style: { textDecoration: 'none', display: 'grid', gap: '5px' } },
                        h('div', { style: { display: 'flex', justifyContent: 'space-between', fontSize: '13px' } }, h('span', null, h('span', { class: 'legend-dot', style: { background: statusColor(c) } }), l),
                            h('span', { class: 'num' }, num(v), h('span', { class: 'muted', style: { display: 'inline-block', minWidth: '52px', textAlign: 'right' } }, `${((v / total) * 100).toFixed(1)}%`))),
                        h('div', { style: { height: '6px', borderRadius: '3px', background: cssVar('--hover'), overflow: 'hidden' } },
                            h('div', { style: { height: '100%', width: `${(v / total) * 100}%`, background: statusColor(c), borderRadius: '3px' } })))));
            });
            const mini = (box: HTMLElement, dim: string, label: (r: DimRow) => string) =>
                load(box, api.get<{ rows: DimRow[] }>('/api/dim', { ...rp, dim, limit: 8, sort: 'pv' }), (r) => {
                    const max = Math.max(...r.rows.map((x) => x.pv), 1);
                    return table(r.rows.filter((x) => x.pv > 0), [
                        { title: '#', get: (_, i) => rank(i) },
                        { title: '名称', cls: 'cell-long', get: (x) => h('span', { title: x.key }, label(x)) },
                        { title: '浏览量', cls: 'r', get: (x) => barCell(x.pv, max) },
                    ]);
                });
            mini(pageBox, 'page', (x) => x.key);
            mini(refBox, 'ref', (x) => x.key);
        };

        root.append(kpis, trend, h('div', { class: 'grid cols-2' }, perfSheet, flowSheet), quick);

        loadActive();
        loadCards();
        loadTrend();
        loadRealtime();
        loadQuick();
        scope.every(3000, loadRealtime);
        scope.every(15000, loadActive);
        scope.onRefresh(() => { loadCards(); loadTrend(); loadQuick(); });
    },
};
