// 封禁对话框和各页面行内的「封禁」按钮
import { api } from './api';
import { h, toast } from './util';

export const DURATIONS: [number, string][] = [[60, '1 小时'], [1440, '1 天'], [10080, '7 天'], [43200, '30 天'], [0, '永久']];

export interface BanResult {
    ok: boolean;
    ip: string;
    applied: boolean;
    sync_error: string;
}

// 打开封禁对话框；成功后调用 onDone
export function banDialog(ip = '', onDone?: () => void) {
    const ipIn = h('input', { type: 'text', value: ip, placeholder: 'IP 或网段，例如 1.2.3.4 或 1.2.3.0/24', required: true, style: { width: '100%' } });
    const dur = h('select', { style: { width: '100%' } }, DURATIONS.map(([v, l]) => h('option', { value: String(v), selected: v === 1440 }, l)));
    const reason = h('input', { type: 'text', placeholder: '原因（可选），例如 扫描 /wp-login.php', maxlength: 100, style: { width: '100%' } });
    const msg = h('div', { class: 'dlg-msg', role: 'alert' });
    const ok = h('button', { class: 'btn primary', type: 'submit' }, '封禁');
    const dlg = h('dialog', { class: 'dlg' });
    const close = () => {
        dlg.close();
        dlg.remove();
    };
    const form = h('form', {
        method: 'dialog',
        onsubmit: async (e: Event) => {
            e.preventDefault();
            msg.textContent = '';
            ok.disabled = true;
            ok.textContent = '封禁中…';
            try {
                const r = await api.post<BanResult>('/api/bans', { ip: ipIn.value.trim(), minutes: Number(dur.value), reason: reason.value.trim() });
                close();
                if (r.applied) toast(`已封禁 ${r.ip}，OpenResty 已重载生效`);
                else toast(`已记录封禁 ${r.ip}，但还没生效：${r.sync_error || '请到「封禁 IP」页查看状态'}`);
                onDone?.();
            } catch (err: any) {
                msg.textContent = err.message;
            } finally {
                ok.disabled = false;
                ok.textContent = '封禁';
            }
        },
    },
        h('h2', null, '封禁 IP'),
        h('p', { class: 'muted' }, '封禁对所有网站生效，被封的 IP 访问时返回 403。到期后自动解封。'),
        h('label', null, h('span', null, 'IP / 网段'), ipIn),
        h('label', null, h('span', null, '时长'), dur),
        h('label', null, h('span', null, '原因'), reason),
        msg,
        h('div', { class: 'dlg-actions' }, h('button', { class: 'btn', type: 'button', onclick: close }, '取消'), ok));
    dlg.appendChild(form);
    dlg.addEventListener('cancel', () => dlg.remove());
    document.body.appendChild(dlg);
    dlg.showModal();
    (ip ? reason : ipIn).focus();
}

export function banButton(ip: string) {
    return h('button', {
        class: 'btn sm', title: `封禁 ${ip}`,
        onclick: (e: Event) => {
            e.stopPropagation();
            banDialog(ip);
        },
    }, '封禁');
}
