// 设置页的「OpenResty 接入」区块：检查日志格式、封禁 include、各网站 access_log，一键接入和撤销
import { api } from '../api';
import { Scope } from '../scope';
import { sheet, table } from '../ui';
import { ago, h, toast } from '../util';

interface Site {
    id: number;
    domain: string;
    alias: string;
    state: string;
    line: string;
    note: string;
    backup: boolean;
    skipped: boolean;
}
interface Report {
    panel_configured: boolean;
    error: string;
    format: boolean;
    include: boolean;
    include_line: string;
    sites: Site[];
    auto: boolean;
    last_auto: number;
    last_auto_msg: string;
}
interface Change {
    target: string;
    site_id?: number;
    lines: string[];
    error?: string;
    done: boolean;
}

const STATE: Record<string, [string, string]> = {
    ok: ['已接入', 'ok'],
    main: ['待接入', 'todo'],
    off: ['访问日志已关闭', 'muted'],
    none: ['没有 access_log', 'muted'],
    custom: ['自定义写法，不自动修改', 'muted'],
    skip: ['跳过', 'muted'],
    error: ['读取失败', 'bad'],
};

function chip(text: string, kind: string) {
    const style: Record<string, string> =
        kind === 'ok' ? { borderColor: 'var(--c-main)', color: 'var(--c-main)' }
            : kind === 'todo' ? { borderColor: 'var(--s4)', color: 'var(--s4)' }
                : kind === 'bad' ? { borderColor: 'var(--pencil)', color: 'var(--pencil)', borderStyle: 'dashed' } : {};
    return h('span', { class: 'chip', style }, text);
}

// 确认对话框：列出将要修改的每一处
function confirmDialog(changes: Change[], onOk: () => Promise<void>) {
    const dlg = h('dialog', { class: 'dlg', style: { width: 'min(620px, calc(100% - 32px))' } });
    const ok = h('button', { class: 'btn primary', type: 'button' }, '确认修改');
    const close = () => { dlg.close(); dlg.remove(); };
    ok.addEventListener('click', async () => {
        ok.disabled = true;
        ok.textContent = '修改中，每处都要等 1Panel 检查和重载…';
        await onOk();
        close();
    });
    dlg.append(h('form', { method: 'dialog' },
        h('h2', null, '一键接入：将要修改这些地方'),
        h('p', { class: 'muted' }, '每一处都由 1Panel 保存：写入后自动执行 nginx -t，不通过会自动恢复原内容。网站配置在修改前会备份，之后可以在列表里一键撤销。'),
        h('div', { style: { display: 'grid', gap: '10px', maxHeight: '50vh', overflow: 'auto' } },
            changes.map((c) => h('div', { class: 'check ok', style: { display: 'block' } },
                h('b', null, c.target), c.lines.map((l) => h('div', { class: 'mono muted', style: { marginTop: '4px', wordBreak: 'break-all' } }, l))))),
        h('div', { class: 'dlg-actions' }, h('button', { class: 'btn', type: 'button', onclick: close }, '取消'), ok)));
    dlg.addEventListener('cancel', () => dlg.remove());
    document.body.appendChild(dlg);
    dlg.showModal();
}

export function onboardSection(scope: Scope): HTMLElement {
    const box = h('div');
    const btn = h('button', { class: 'btn primary sm' }, '一键接入');
    const s = sheet('OpenResty 接入', {
        tip: '通过 1Panel API 自动完成部署说明里的手动步骤：在 nginx.conf 里加上 liuli_json 日志格式和封禁规则的 include，并把各网站的 access_log 从 main 切到 liuli_json。',
        extra: [h('button', { class: 'btn sm', onclick: () => load() }, '重新检查'), btn],
    }, box);

    const render = (r: Report) => {
        if (r.error) {
            btn.disabled = true;
            box.replaceChildren(h('div', { class: 'err-line' }, r.error));
            return;
        }
        // 撤销过的网站是你主动选择不接入的，不算「待接入」，也不会被一键接入改动
        const todo = r.sites.filter((x) => x.state === 'main' && !x.skipped).length;
        const undone = r.sites.filter((x) => x.state === 'main' && x.skipped).length;
        btn.disabled = r.format && r.include && todo === 0;
        btn.textContent = btn.disabled ? '已全部接入' : '一键接入';
        const check = (ok: boolean, title: string, detail: string) =>
            h('div', { class: 'check ' + (ok ? 'ok' : 'bad') }, h('span', { class: 'mark', 'aria-hidden': 'true' }, ok ? '✓' : '!'),
                h('div', null, h('b', null, title), h('div', { class: 'muted' }, detail)));
        box.replaceChildren(
            h('div', { class: 'checks', style: { marginBottom: '14px' } },
                check(r.format, '日志格式 liuli_json', r.format ? 'nginx.conf 里已定义' : '还没定义，点「一键接入」会加到 nginx.conf 的 http 块里'),
                check(r.include, '封禁规则', r.include ? 'nginx.conf 里已 include' : `还没加载，点「一键接入」会加上 ${r.include_line}`),
                check(todo === 0, '网站访问日志', (todo === 0 ? '所有网站都已接入或不需要接入' : `${todo} 个网站还在用默认的 main 格式（没有耗时等数据）`)
                    + (undone ? `；${undone} 个已撤销的保持原样` : ''))),
            table(r.sites, [
                { title: '网站', get: (x) => h('b', null, x.domain) },
                { title: '状态', get: (x) => { const [t, k] = STATE[x.state] || [x.state, 'muted']; return chip(x.skipped && x.state === 'main' ? '已撤销，不自动接入' : t, x.skipped ? 'muted' : k); } },
                { title: '当前 access_log', cls: 'cell-wrap', get: (x) => (x.line ? h('span', { class: 'mono' }, x.line) : x.note ? h('span', { class: 'muted' }, x.note) : '-') },
                {
                    title: '', cls: 'r', get: (x) => (x.skipped && x.state === 'main'
                        ? h('button', { class: 'btn sm', title: '重新把这个网站的访问日志切到 liuli_json，并恢复自动接入', onclick: () => run(x.id) }, '重新接入')
                        : x.backup ? h('button', { class: 'btn sm ghost', title: '恢复成接入前的配置，之后自动接入也不再修改它', onclick: async () => {
                        if (!confirm(`把 ${x.domain} 的配置恢复成接入前的样子？之后自动接入不会再修改它。`)) return;
                        try {
                            await api.post('/api/onboard/undo', { site_id: x.id });
                            toast(`已恢复 ${x.domain} 的原配置`);
                        } catch (e: any) {
                            toast(e.message);
                        }
                        load();
                    } }, '撤销') : null),
                },
            ], { emptyText: '1Panel 里还没有网站' }),
            h('div', { class: 'muted', style: { marginTop: '10px', fontSize: '12.5px' } },
                r.auto ? `自动接入新网站：已开启，每 5 分钟检查一次${r.last_auto ? `（${ago(r.last_auto)}：${r.last_auto_msg}）` : ''}` : '自动接入新网站：已关闭（在下方「统计规则与保留」里打开）'));
    };

    const load = async () => {
        s.classList.add('loading');
        try {
            render(await api.get<Report>('/api/onboard'));
        } catch (e: any) {
            box.replaceChildren(h('div', { class: 'err-line' }, '检查失败：' + e.message));
        } finally {
            s.classList.remove('loading');
        }
    };

    // siteId 不为空时只处理这一个网站（重新接入撤销过的网站）
    const run = async (siteId?: number) => {
        const body = siteId ? { site_id: siteId } : {};
        btn.disabled = true;
        try {
            const p = await api.post<{ changes: Change[] }>('/api/onboard/plan', body);
            if (!p.changes.length) {
                toast('没有需要修改的地方');
                load();
                return;
            }
            confirmDialog(p.changes, async () => {
                try {
                    const r = await api.post<{ changes: Change[] }>('/api/onboard/apply', body);
                    const failed = r.changes.filter((c) => c.error);
                    if (failed.length) toast(`完成 ${r.changes.length - failed.length} 处，${failed.length} 处失败：${failed.map((c) => `${c.target}（${c.error}）`).join('；')}`);
                    else toast(`已完成 ${r.changes.length} 处修改，OpenResty 已重载`);
                } catch (e: any) {
                    toast(e.message);
                }
                load();
            });
        } catch (e: any) {
            toast(e.message);
        } finally {
            btn.disabled = false;
        }
    };
    btn.addEventListener('click', () => run());

    load();
    scope.onDispose(() => document.querySelectorAll('dialog.dlg').forEach((d) => d.remove()));
    return s;
}
