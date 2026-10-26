// 每个页面一个 Scope：页面离开时统一清理定时器和图表。
import { Chart, dispose, init } from './charts';

export class Scope {
    alive = true;
    private timers: number[] = [];
    private charts: Chart[] = [];
    private cleanups: (() => void)[] = [];
    refreshers: (() => unknown)[] = [];

    every(ms: number, fn: () => void) {
        const id = window.setInterval(() => {
            if (this.alive && !document.hidden) fn();
        }, ms);
        this.timers.push(id);
    }

    chart(el: HTMLElement): Chart {
        const c = init(el);
        this.charts.push(c);
        return c;
    }

    // 注册给顶部「自动刷新」调用的刷新函数
    onRefresh(fn: () => unknown) {
        this.refreshers.push(fn);
    }

    onDispose(fn: () => void) {
        this.cleanups.push(fn);
    }

    dispose() {
        this.alive = false;
        this.timers.forEach(clearInterval);
        this.charts.forEach(dispose);
        this.cleanups.forEach((f) => f());
        this.timers = [];
        this.charts = [];
        this.refreshers = [];
    }
}
