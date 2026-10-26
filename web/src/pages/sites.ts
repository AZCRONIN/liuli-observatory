import { api, Site, Totals } from '../api';
import { Ctx, go, save } from '../state';
import { Scope } from '../scope';
import { load, sheet, table } from '../ui';
import { bytes, h, num } from '../util';

interface Row extends Site {
    today: Totals;
    yesterday: Totals;
    active: number;
}

export const sitesPage = {
    title: '网站列表',
    needsSite: false,
    render(root: HTMLElement, _ctx: Ctx, scope: Scope) {
        const box = h('div');
        root.appendChild(sheet('今日各站点', { tip: '点击一行进入该站点的概览。数字口径和概览页一致。', extra: [h('a', { class: 'btn sm', href: '#/settings' }, '添加站点')] }, box));
        const run = () =>
            load(box, api.get<{ sites: Row[] }>('/api/sites'), (r) =>
                table(r.sites, [
                    { title: '站点', get: (x) => h('b', null, x.site) },
                    { title: '10 分钟活跃', cls: 'r', get: (x) => num(x.active) },
                    { title: '浏览量', cls: 'r', get: (x) => num(x.today.pv) },
                    { title: '访客', cls: 'r', get: (x) => num(x.today.uv) },
                    { title: 'IP', cls: 'r', get: (x) => num(x.today.ip) },
                    { title: '请求', cls: 'r', get: (x) => num(x.today.req) },
                    { title: '流量', cls: 'r', get: (x) => bytes(x.today.bytes) },
                    { title: '蜘蛛', cls: 'r', get: (x) => num(x.today.spider) },
                    { title: '错误(4xx/5xx)', cls: 'r', get: (x) => `${num(x.today.s4)} / ${num(x.today.s5)}` },
                    { title: '昨日浏览量', cls: 'r', get: (x) => h('span', { class: 'muted' }, num(x.yesterday.pv)) },
                ], {
                    onRow: (x) => { save('site', x.site); go('overview', { site: x.site }); },
                    emptyText: '还没有站点，到「设置」里添加',
                }));
        run();
        scope.every(30000, run);
    },
};
