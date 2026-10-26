// ECharts 按需引入 + 跟随琉璃主题的统一样式。颜色全部从 CSS 变量读，切换深浅色后重新渲染即可。
import * as echarts from 'echarts/core';
import { BarChart, LineChart, PieChart } from 'echarts/charts';
import { GridComponent, LegendComponent, TooltipComponent } from 'echarts/components';
import { CanvasRenderer } from 'echarts/renderers';

echarts.use([LineChart, BarChart, PieChart, GridComponent, TooltipComponent, LegendComponent, CanvasRenderer]);

export type Chart = echarts.ECharts;
export type Option = echarts.EChartsCoreOption;

export function cssVar(name: string): string {
    return getComputedStyle(document.documentElement).getPropertyValue(name).trim();
}

export function palette(): string[] {
    return cssVar('--cat')
        .split(',')
        .map((s) => s.trim())
        .filter(Boolean);
}

export function statusColor(cls: 's2' | 's3' | 's4' | 's5'): string {
    return cssVar(`--${cls}`);
}

export function alpha(color: string, a: number): string {
    const m = color.match(/^#([0-9a-f]{6})$/i);
    if (!m) return color;
    const n = parseInt(m[1], 16);
    return `rgba(${(n >> 16) & 255}, ${(n >> 8) & 255}, ${n & 255}, ${a})`;
}

// 所有图表共用的基础样式
export function base(): Option {
    const ink3 = cssVar('--ink-3');
    const grid = cssVar('--grid');
    const font = cssVar('--sans');
    return {
        animationDuration: 600,
        animationEasing: 'cubicOut',
        color: palette(),
        textStyle: { fontFamily: font, color: ink3 },
        grid: { left: 8, right: 16, top: 16, bottom: 8, containLabel: true },
        tooltip: {
            trigger: 'axis',
            confine: true,
            backgroundColor: cssVar('--sheet'),
            borderColor: cssVar('--rule-strong'),
            borderWidth: 1,
            padding: [8, 12],
            textStyle: { color: cssVar('--ink'), fontFamily: font, fontSize: 12.5 },
            extraCssText: 'border-radius:9px;box-shadow:0 12px 28px -12px rgba(0,0,0,.35);',
            axisPointer: { type: 'line', lineStyle: { color: cssVar('--rule-strong') } },
        },
        legend: { show: false },
        xAxis: {
            type: 'category',
            boundaryGap: false,
            axisLine: { lineStyle: { color: cssVar('--rule-strong') } },
            axisTick: { show: false },
            axisLabel: { color: ink3, fontSize: 11.5, hideOverlap: true },
            splitLine: { show: false },
        },
        yAxis: {
            type: 'value',
            axisLine: { show: false },
            axisTick: { show: false },
            axisLabel: { color: ink3, fontSize: 11.5 },
            splitLine: { lineStyle: { color: grid } },
        },
    };
}

export function areaGradient(color: string, top = 0.28): any {
    return new echarts.graphic.LinearGradient(0, 0, 0, 1, [
        { offset: 0, color: alpha(color, top) },
        { offset: 1, color: alpha(color, 0.01) },
    ]);
}

// 合并基础样式和具体配置（浅合并到第二层）
export function merge(a: any, b: any): any {
    const out: any = { ...a };
    for (const [k, v] of Object.entries(b)) {
        if (v && typeof v === 'object' && !Array.isArray(v) && a[k] && typeof a[k] === 'object' && !Array.isArray(a[k])) {
            out[k] = { ...a[k], ...v };
        } else out[k] = v;
    }
    return out;
}

export function init(el: HTMLElement): Chart {
    const c = echarts.init(el, undefined, { renderer: 'canvas' });
    const ro = new ResizeObserver(() => c.resize());
    ro.observe(el);
    (c as any).__ro = ro;
    return c;
}

export function dispose(c: Chart) {
    (c as any).__ro?.disconnect();
    c.dispose();
}
