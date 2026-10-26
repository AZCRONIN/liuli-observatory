import { api } from '../api';
import { banDialog } from '../banui';
import { Ctx, go } from '../state';
import { Scope } from '../scope';
import { load, sheet, table } from '../ui';
import { ago, h, num, time, toast } from '../util';

interface Status {
    panel_configured: boolean;
    panel_ok: boolean;
    panel_error: string;
    dir_ok: boolean;
    dir_error: string;
    include_ok: boolean;
    include_line: string;
    active: number;
    last_sync: number;
    last_reload: number;
    last_error: string;
    ip_source_ok: boolean;
}
interface Ban {
    id: number;
    ip: string;
    reason: string;
    source: string;
    created: number;
    expires: number;
    active: boolean;
    region: string;
    hits: number;
}
interface Resp {
    enabled: boolean;
    bans: Ban[];
    status: Status;
    whitelist: string[];
    self_ip: string;
}

// 一行状态：实线勾 = 正常，红色虚线框 = 有问题（琉璃里状态靠线形表达）
function check(ok: boolean, title: string, detail: Node | string) {
    return h('div', { class: 'check ' + (ok ? 'ok' : 'bad') }, h('span', { class: 'mark', 'aria-hidden': 'true' }, ok ? '✓' : '!'),
        h('div', null, h('b', null, title), h('div', { class: 'muted' }, detail)));
}

function copyable(text: string) {
    return h('span', { style: { display: 'inline-flex', gap: '8px', alignItems: 'center', flexWrap: 'wrap' } },
        h('code', { class: 'mono' }, text),
        h('button', { class: 'btn sm', type: 'button', onclick: () => navigator.clipboard?.writeText(text).then(() => toast('已复制')) }, '复制'));
}

export const banPage = {
    title: '封禁 IP',
    needsSite: false,
    render(root: HTMLElement, _ctx: Ctx, scope: Scope) {
        const statusBox = h('div');
        const listBox = h('div');
        const wlBox = h('div');
        root.append(
            h('div', { style: { marginBottom: '16px' } }, sheet('生效状态', {
                tip: '封禁对所有网站生效：观测台把封禁名单写成 OpenResty 的 deny 规则文件，再通过 1Panel API 平滑重载 OpenResty（不断开现有连接）。被封的 IP 访问时返回 403。',
                extra: [h('button', { class: 'btn sm', onclick: () => resync() }, '重新同步')],
            }, statusBox)),
            h('div', { class: 'grid cols-3' },
                h('div', { class: 'span-2' }, sheet('封禁列表', {
                    tip: '「已拦截」是封禁之后这个 IP 被返回 403 的次数（只统计明细保留期内）。到期的封禁会在 15 秒内自动解封。',
                    extra: [h('button', { class: 'btn primary sm', onclick: () => banDialog('', reload) }, '封禁 IP')],
                }, listBox)),
                sheet('白名单', { tip: '白名单里的 IP / 网段永远不会被封禁，例如你家里和公司的 IP、监控服务的 IP。本机和内网地址默认就不能封。' }, wlBox)),
        );

        const renderStatus = (r: Resp) => {
            const s = r.status;
            const items: HTMLElement[] = [];
            if (!s.ip_source_ok) items.push(check(false, '真实访客 IP 不是直接连接的 IP', '设置里「真实访客 IP」选了 CDN 请求头。OpenResty 按连接 IP 封禁，套 CDN 时会误伤所有访客，封禁已禁用。'));
            items.push(check(s.dir_ok, '规则文件', s.dir_ok ? `可写，当前生效 ${num(s.active)} 条` : (s.dir_error || '还没写入过') + '。检查规则目录的挂载和权限（见部署说明）。'));
            items.push(check(s.panel_ok, '1Panel API',
                !s.panel_configured ? '还没配置：在 .env 里填 LIULI_1PANEL_URL 和 LIULI_1PANEL_API_KEY 后重建容器。没配时规则只写文件，需要在 1Panel 里手动重载 OpenResty。'
                    : s.panel_ok ? `已连接${s.last_reload ? `，上次重载 ${ago(s.last_reload)}` : ''}` : s.panel_error));
            items.push(check(s.include_ok, 'OpenResty 已加载规则文件',
                s.include_ok ? h('span', null, '已在 nginx.conf 里找到：', h('code', { class: 'mono' }, s.include_line))
                    : h('span', null, '在 1Panel「网站」→「设置」→「配置修改」里，把下面这一行加到 http { 里面（例如紧挨着 include conf.d 那几行），保存后点「重新同步」：', h('br'), copyable(s.include_line))));
            statusBox.replaceChildren(h('div', { class: 'checks' }, items),
                s.last_error && s.include_ok ? h('div', { class: 'err-line', style: { marginTop: '12px' } }, s.last_error) : '');
        };

        const renderList = (r: Resp) => {
            listBox.replaceChildren(table(r.bans, [
                { title: 'IP / 网段', get: (b) => h('span', { class: 'mono' }, b.ip) },
                { title: '地区', cls: 'hide-sm', get: (b) => b.region || '-' },
                { title: '原因', cls: 'cell-long', get: (b) => b.reason || h('span', { class: 'muted' }, '-') },
                { title: '封禁时间', cls: 'hide-sm', get: (b) => time(b.created) },
                { title: '到期', get: (b) => (!b.active ? h('span', { class: 'chip' }, '已解封') : b.expires ? time(b.expires) : h('span', { class: 'chip', style: { borderColor: 'var(--pencil)', color: 'var(--pencil)' } }, '永久')) },
                { title: '已拦截', cls: 'r', get: (b) => (b.ip.includes('/') || b.hits < 0 ? '-' : num(b.hits)) },
                {
                    title: '', cls: 'r', get: (b) => h('span', { style: { display: 'inline-flex', gap: '6px' } },
                        b.ip.includes('/') ? null : h('button', { class: 'btn sm ghost', onclick: () => go('errors', { ip: b.ip, status: 'all' }) }, '明细'),
                        b.active ? h('button', { class: 'btn sm', onclick: async () => {
                            if (!confirm(`解封 ${b.ip}？`)) return;
                            const res = await api.del<{ sync_error: string }>('/api/bans', { id: b.id });
                            toast(res.sync_error ? `已解封，但同步出错：${res.sync_error}` : `已解封 ${b.ip}`);
                            reload();
                        } }, '解封') : null),
                },
            ], { emptyText: '还没有封禁任何 IP。可以在「IP / 地区」「错误日志」里对可疑 IP 点「封禁」。' }));
        };

        const renderWhitelist = (r: Resp) => {
            const ta = h('textarea', { rows: 6, placeholder: '每行一个 IP 或网段，例如\n203.0.113.8\n198.51.100.0/24' }, (r.whitelist || []).join('\n'));
            wlBox.replaceChildren(ta,
                h('div', { class: 'help muted', style: { margin: '8px 0 10px', fontSize: '12.5px' } }, `你当前的 IP：${r.self_ip || '未知'}（不在白名单里也不会被封：封禁时会拒绝封掉自己）`),
                h('button', { class: 'btn primary sm', onclick: async () => {
                    try {
                        await api.put('/api/bans/whitelist', { list: ta.value.split(/[\s,，]+/).filter(Boolean) });
                        toast('白名单已保存');
                    } catch (e: any) {
                        toast(e.message);
                    }
                } }, '保存白名单'));
        };

        const reload = () =>
            load(listBox, api.get<Resp>('/api/bans'), (r) => {
                if (!r.enabled) {
                    statusBox.replaceChildren(h('div', { class: 'placeholder' }, '封禁功能还没启用：需要在 docker-compose 里挂载规则目录（LIULI_BAN_DIR）。按部署说明「封禁 IP」一节配置后重建容器。'));
                    listBox.replaceChildren();
                    wlBox.replaceChildren();
                    return;
                }
                renderStatus(r);
                renderList(r);
                if (!wlBox.contains(document.activeElement)) renderWhitelist(r);
            });
        const resync = async () => {
            statusBox.closest('.sheet')?.classList.add('loading');
            try {
                const r = await api.post<{ ok: boolean; sync_error: string }>('/api/bans/sync');
                toast(r.ok ? '已同步并重载 OpenResty' : r.sync_error);
            } catch (e: any) {
                toast(e.message);
            } finally {
                statusBox.closest('.sheet')?.classList.remove('loading');
                reload();
            }
        };
        reload();
        scope.every(30000, reload);
    },
};
