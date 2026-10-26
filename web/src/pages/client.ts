import { api, DimRow } from '../api';
import { Ctx, rangeParams } from '../state';
import { Scope } from '../scope';
import { barCell, donut, load, rank, sheet, table } from '../ui';
import { h } from '../util';

export const clientPage = {
    title: '访客 / 客户端',
    needsSite: true,
    render(root: HTMLElement, ctx: Ctx, scope: Scope) {
        const rp = rangeParams(ctx);
        const dims: [string, string][] = [['browser', '浏览器'], ['os', '操作系统'], ['device', '设备类型']];
        const tipText = '按访客数（IP + UA 去重，不含蜘蛛）统计。浏览器和系统由 UA 识别，微信、QQ、夸克等内置浏览器单独列出。';
        const blocks = dims.map(([d, title]) => {
            const chartEl = h('div', { class: 'chart xs' });
            const box = h('div');
            const s = sheet(title, { tip: tipText }, chartEl, box);
            return { d, s, chart: scope.chart(chartEl), box };
        });
        root.appendChild(h('div', { class: 'grid cols-3' }, blocks.map((b) => b.s)));
        const run = () => {
            for (const b of blocks) {
                load(b.box, api.get<{ rows: DimRow[] }>('/api/dim', { ...rp, dim: b.d, limit: 50, sort: 'uv' }), (r) => {
                    donut(b.chart, r.rows, (x) => x.uv);
                    const total = r.rows.reduce((s, x) => s + x.uv, 0);
                    const max = Math.max(1, ...r.rows.map((x) => x.uv));
                    return table(r.rows.slice(0, 15), [
                        { title: '#', get: (_, i) => rank(i) },
                        { title: '名称', get: (x) => x.key },
                        { title: '访客', cls: 'r', get: (x) => barCell(x.uv, max) },
                        { title: '占比', cls: 'r', get: (x) => (total ? ((x.uv / total) * 100).toFixed(1) + '%' : '-') },
                    ]);
                });
            }
        };
        run();
        scope.onRefresh(run);
    },
};
