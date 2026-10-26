import { h } from '../util';
import { sheet } from '../ui';

// 第二版：封禁 IP。第一版先把位置留好，说明会怎么工作。
export const banPage = {
    title: '封禁 IP',
    needsSite: false,
    render(root: HTMLElement) {
        root.appendChild(sheet('封禁 IP', {},
            h('div', { class: 'placeholder' },
                h('b', null, '这个功能在第二版提供'), h('br'),
                '届时可以在 IP 列表、错误日志、实时访问里一键封禁 / 解封，支持到期自动解封、自动封禁（疑似 CC）和白名单。', h('br'),
                '实现方式：观测台生成一个 deny 规则文件给 OpenResty include，再让 OpenResty 重载。', h('br'),
                '重载需要给观测台额外的权限（1Panel API、docker exec 或发信号三选一），上线前会先和你讲清楚风险再做。')));
    },
};
