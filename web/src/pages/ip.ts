import { api, DimRow } from '../api';
import { Ctx, go, rangeParams, setParams } from '../state';
import { Scope } from '../scope';
import { barCell, hbar, load, rank, sheet, table } from '../ui';
import { ago, bytes, h, num } from '../util';

function region(extra = ''): { place: string; isp: string } {
    const [country, prov, city, isp] = extra.split('|');
    let place = country || '未知';
    if (country === '中国') place = [prov, city && city !== prov ? city : ''].filter(Boolean).join(' ') || '中国';
    else if (city) place = `${country} ${city}`;
    return { place, isp: isp || '' };
}

// 第二版的封禁按钮：位置先占好
export function banButton(ip: string) {
    return h('button', { class: 'btn sm', disabled: true, title: `封禁 ${ip}：第二版提供`, onclick: (e: Event) => e.stopPropagation() }, '封禁');
}

export const ipPage = {
    title: 'IP / 地区',
    needsSite: true,
    render(root: HTMLElement, ctx: Ctx, scope: Scope) {
        const rp = rangeParams(ctx);
        const tab = ctx.params.get('area') === 'world' ? 'world' : 'cn';
        const ipBox = h('div');
        const chartEl = h('div', { class: 'chart', style: { height: '380px' } });
        const areaBox = h('div');
        const chart = scope.chart(chartEl);
        const seg = h('div', { class: 'seg' },
            h('button', { 'aria-pressed': String(tab === 'cn'), onclick: () => setParams({ area: 'cn' }) }, '国内省份'),
            h('button', { 'aria-pressed': String(tab === 'world'), onclick: () => setParams({ area: 'world' }) }, '国家 / 地区'));

        root.append(
            h('div', { class: 'grid cols-2', style: { marginBottom: '16px' } },
                sheet('地区分布', { tip: '按访客数排序。国内到省（港澳台单列），国外到国家。地区来自离线 IP 库 ip2region，只在本机查询。', extra: [seg] }, chartEl),
                sheet('地区明细', {}, areaBox)),
            sheet('Top IP', { tip: '按请求次数排序，含蜘蛛。点击一行查看该 IP 的访问明细。' }, ipBox),
        );

        const run = () => {
            load(areaBox, api.get<{ rows: DimRow[] }>('/api/dim', { ...rp, dim: 'region', limit: 1000, sort: 'uv' }), (r) => {
                let rows = r.rows;
                if (tab === 'cn') rows = rows.filter((x) => x.key.startsWith('中国|')).map((x) => ({ ...x, key: x.key.slice(3) }));
                else {
                    // 国内各省合并成「中国」
                    const cn = rows.filter((x) => x.key.startsWith('中国|'));
                    const other = rows.filter((x) => !x.key.startsWith('中国|'));
                    if (cn.length) other.push(cn.reduce((a, x) => ({ ...a, req: a.req + x.req, pv: a.pv + x.pv, uv: a.uv + x.uv, bytes: a.bytes + x.bytes }), { key: '中国', req: 0, pv: 0, uv: 0, bytes: 0, last: 0 }));
                    rows = other.sort((a, b) => b.uv - a.uv);
                }
                hbar(chart, rows.slice(0, 15).map((x) => ({ name: x.key, value: x.uv })));
                const max = Math.max(1, ...rows.map((x) => x.uv));
                const totalUV = rows.reduce((s, x) => s + x.uv, 0);
                return h('div', { style: { maxHeight: '380px', overflow: 'auto' } }, table(rows, [
                    { title: '#', get: (_, i) => rank(i) },
                    { title: '地区', get: (x) => x.key },
                    { title: '访客', cls: 'r', get: (x) => barCell(x.uv, max) },
                    { title: '占比', cls: 'r', get: (x) => (totalUV ? ((x.uv / totalUV) * 100).toFixed(1) + '%' : '-') },
                    { title: '浏览量', cls: 'r', get: (x) => num(x.pv) },
                    { title: '请求', cls: 'r', get: (x) => num(x.req) },
                ]));
            });
            load(ipBox, api.get<{ rows: DimRow[] }>('/api/dim', { ...rp, dim: 'ip', limit: 100 }), (r) => {
                const max = Math.max(1, ...r.rows.map((x) => x.req));
                return table(r.rows, [
                    { title: '#', get: (_, i) => rank(i) },
                    { title: 'IP', get: (x) => h('span', { class: 'mono' }, x.key) },
                    { title: '地区', get: (x) => region(x.extra).place },
                    { title: '运营商', cls: 'hide-sm', get: (x) => region(x.extra).isp || '-' },
                    { title: '请求', cls: 'r', get: (x) => barCell(x.req, max) },
                    { title: '浏览量', cls: 'r', get: (x) => num(x.pv) },
                    { title: '流量', cls: 'r', get: (x) => bytes(x.bytes) },
                    { title: '最近访问', cls: 'r', get: (x) => ago(x.last) },
                    { title: '', cls: 'r', get: (x) => banButton(x.key) },
                ], { onRow: (x) => go('errors', { ip: x.key, status: 'all' }) });
            });
        };
        run();
        scope.onRefresh(run);
    },
};
