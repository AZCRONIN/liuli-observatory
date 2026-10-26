/*!
 * 琉璃 LiuliGlass v1.0 — 可交互的液态玻璃 WebGL 动画
 * 几团琉璃色玻璃在画面里漂浮、融合又分开，实时折射 + 色散 + 虹彩，并在下方台面投出彩色光斑。
 * 无依赖，单文件。浏览器直接 <script> 引入，或在打包工具里 import。
 *
 * 用法：
 *   const glass = LiuliGlass.mount(document.querySelector('#hero'), { theme: 'light' });
 *   glass.destroy();
 *
 * 技法：对四个球体的有符号距离场做 smooth-min（Inigo Quilez 的写法）后光线步进；
 * 折射按 R/G/B 三个折射率分别取样形成色散；菲涅尔反射摄影棚环境光；余弦调色板做边缘虹彩。
 */
(function (global) {
    'use strict';

    // ------------------------------------------------------------------ 默认参数

    var DEFAULTS = {
        /** 四团玻璃的颜色（CSS 十六进制）。默认是琉璃的琥珀、翡翠、钴蓝、胭脂 */
        colors: ['#f29933', '#33ad8a', '#386beb', '#e05270'],
        /** 'light' 适合浅色背景，'dark' 适合深色背景；也可以用 envTop/envBottom 自定义 */
        theme: 'light',
        /** 玻璃内部的自发光（光在玻璃里散射），0~1。不填则浅色 0、深色 0.62 */
        innerGlow: null,
        /** 环境光顶部/底部颜色，会被玻璃折射和反射。不填则按 theme 取 */
        envTop: null,
        envBottom: null,
        /** 漂浮速度倍率，1 为默认 */
        speed: 1,
        /** 玻璃团整体上下偏移（-0.5 ~ 0.5，正数往上），给压在下面的内容让位 */
        offsetY: 0.12,
        /** 镜头缩放倍率，大于 1 玻璃更小 */
        zoom: 1,
        /** 台面彩色光斑强度，0 关闭 */
        caustics: 1,
        /** 跟随指针倾斜（电脑跟随鼠标，手机拖动） */
        interactive: true,
        /** 按下时玻璃果冻般晃动 */
        wobbleOnPress: true,
        /** 遵守系统「减少动态效果」：true 时只渲染一帧静止画面 */
        respectReducedMotion: false,
        /** 滚出视口或标签页在后台时暂停（看不见时不耗电，回来自动继续） */
        pauseWhenHidden: true,
        /** 渲染分辨率上限（相对 CSS 像素），手机和电脑分开设 */
        maxPixelRatio: { mobile: 1.5, desktop: 1.25 },
        /** 首帧出来后淡入的时长（毫秒），0 不淡入 */
        fadeIn: 1200,
        /** 不支持 WebGL 时显示一层静态彩色光 */
        fallback: true,
        /** 动画开始时间（秒），换个值就是另一种初始造型 */
        startTime: 6,
    };

    var ENV = {
        light: { top: '#fafcfa', bottom: '#dbe3e0', glow: 0 },
        dark: { top: '#3a4250', bottom: '#0e1116', glow: 0.62 },
    };

    // ------------------------------------------------------------------ 着色器

    var VERT = 'attribute vec2 a_pos;void main(){gl_Position=vec4(a_pos,0.0,1.0);}';

    var FRAG = [
        'precision highp float;',
        'uniform vec2 u_res;',
        'uniform float u_time;',
        'uniform vec2 u_rot;',
        'uniform float u_wobble;',
        'uniform float u_spread;',
        'uniform float u_zoom;',
        'uniform float u_offsetY;',
        'uniform float u_caustics;',
        'uniform float u_inner;',
        'uniform vec3 u_c0; uniform vec3 u_c1; uniform vec3 u_c2; uniform vec3 u_c3;',
        'uniform vec3 u_envTop; uniform vec3 u_envBottom;',
        'vec3 P0; vec3 P1; vec3 P2; vec3 P3;',

        // 平滑并集：让几团球体像液体一样连在一起
        'float smin(float a,float b,float k){float h=clamp(0.5+0.5*(b-a)/k,0.0,1.0);return mix(b,a,h)-k*h*(1.0-h);}',

        'mat3 rot(vec2 r){float cy=cos(r.x),sy=sin(r.x),cx=cos(r.y),sx=sin(r.y);',
        '  mat3 yaw=mat3(cy,0.0,-sy,0.0,1.0,0.0,sy,0.0,cy);',
        '  mat3 pitch=mat3(1.0,0.0,0.0,0.0,cx,sx,0.0,-sx,cx);return yaw*pitch;}',

        'float map(vec3 p){',
        '  float d=length(p-P0)-0.62;',
        '  d=smin(d,length(p-P1)-0.54,0.55);',
        '  d=smin(d,length(p-P2)-0.52,0.55);',
        '  d=smin(d,length(p-P3)-0.42,0.55);',
        '  d+=sin(p.x*5.0+u_time*5.0)*sin(p.y*5.0+u_time*3.0)*sin(p.z*5.0)*0.05*u_wobble;',
        '  return d;}',

        'vec3 normal(vec3 p){vec2 e=vec2(0.0015,-0.0015);',
        '  return normalize(e.xyy*map(p+e.xyy)+e.yyx*map(p+e.yyx)+e.yxy*map(p+e.yxy)+e.xxx*map(p+e.xxx));}',

        // 表面颜色：按到各球心的距离加权混合，交界处自然渐变
        'vec3 tint(vec3 p){vec4 w=vec4(exp(-4.0*length(p-P0)),exp(-4.0*length(p-P1)),exp(-4.0*length(p-P2)),exp(-4.0*length(p-P3)));',
        '  return (u_c0*w.x+u_c1*w.y+u_c2*w.z+u_c3*w.w)/(w.x+w.y+w.z+w.w);}',

        // 摄影棚环境：上亮下暗，一盏顶光，两条竖向灯带
        'vec3 env(vec3 d){',
        '  vec3 base=mix(u_envBottom,u_envTop,smoothstep(-0.6,0.8,d.y));',
        '  float top=pow(max(d.y,0.0),6.0)*0.6;',
        '  float strip=smoothstep(0.985,1.0,sin(d.x*2.4+1.2))*0.5+smoothstep(0.99,1.0,sin(d.x*3.1-2.0))*0.35;',
        '  float floorDark=smoothstep(0.0,-0.7,d.y)*0.12;',
        '  return base+top+strip*smoothstep(-0.3,0.6,d.y)-floorDark;}',

        'void main(){',
        '  float t=u_time; float s=u_spread;',
        '  P0=vec3(sin(t*0.43)*0.9*s, cos(t*0.37)*0.22, sin(t*0.31)*0.35);',
        '  P1=vec3(cos(t*0.39+1.0)*0.85*s, sin(t*0.51+2.0)*0.28, cos(t*0.27)*0.4);',
        '  P2=vec3(sin(t*0.29+4.0)*1.0*s, cos(t*0.45+1.0)*0.22, sin(t*0.38+3.0)*0.35);',
        '  P3=vec3(cos(t*0.33+2.5)*0.7*s, sin(t*0.31+0.5)*0.32, cos(t*0.42+1.0)*0.35);',
        '  vec2 uv=(gl_FragCoord.xy-0.5*u_res)/u_res.y;',
        '  uv.y-=u_offsetY;',
        '  mat3 R=rot(u_rot);',
        '  vec3 ro=R*vec3(0.0,0.18,3.6);',
        '  vec3 rd=R*normalize(vec3(uv*u_zoom,-1.9));',

        // 光线步进；记下离表面最近的一点，给轮廓做一像素宽的抗锯齿
        '  float dist=0.0; float hit=0.0; float md=1e9; float mdist=0.0; vec3 p;',
        '  for(int i=0;i<72;i++){p=ro+rd*dist;float d=map(p);',
        '    if(d<md){md=d;mdist=dist;}',
        '    if(d<0.0012){hit=1.0;break;}',
        '    dist+=d*0.9; if(dist>9.0)break;}',
        '  float px=mdist*u_zoom/u_res.y/1.9; float edge=1.0;',
        '  if(hit<0.5&&md<px*1.2){hit=1.0;p=ro+rd*mdist;edge=1.0-md/(px*1.2);}',

        '  if(hit>0.5){',
        '    vec3 n=normal(p); float cosi=max(dot(n,-rd),0.0); float fres=pow(1.0-cosi,3.0);',
        '    vec3 c=tint(p);',
        // 色散：R/G/B 用三个略有差别的折射率
        '    vec3 refr=vec3(env(refract(rd,n,1.0/1.42)).r,env(refract(rd,n,1.0/1.46)).g,env(refract(rd,n,1.0/1.52)).b);',
        '    float thick=0.45+0.55*cosi;',
        '    vec3 body=refr*mix(vec3(1.0),c,0.75*thick)*(0.9+0.25*thick);',
        // 深色背景下折射到的光太少，用玻璃内部散射的自发光补上颜色
        '    body=mix(body,c*(0.42+0.5*thick)+refr*0.25,u_inner);',
        '    vec3 rl=reflect(rd,n); vec3 refl=env(rl);',
        '    float spec=pow(max(dot(rl,normalize(vec3(-0.4,0.8,0.5))),0.0),80.0)*1.6',
        '              +pow(max(dot(rl,normalize(vec3(0.7,0.3,0.6))),0.0),40.0)*0.5;',
        // 边缘虹彩
        '    vec3 irid=0.5+0.5*cos(6.2831*(fres*1.3+t*0.04+vec3(0.0,0.33,0.67)));',
        '    vec3 col=mix(body,refl,fres*0.55)+spec+irid*fres*0.28;',
        '    gl_FragColor=vec4(col*edge,edge); return;}',

        // 没打到玻璃：透明，但在下方台面投出彩色光斑
        '  float a=0.0; vec3 glow=vec3(0.0);',
        '  if(rd.y<0.0&&u_caustics>0.0){',
        '    float k=(-0.78-ro.y)/rd.y; vec3 q=ro+rd*k;',
        '    glow=u_c0*exp(-dot(q.xz-P0.xz,q.xz-P0.xz)*4.0)+u_c1*exp(-dot(q.xz-P1.xz,q.xz-P1.xz)*4.0)',
        '        +u_c2*exp(-dot(q.xz-P2.xz,q.xz-P2.xz)*4.0)+u_c3*exp(-dot(q.xz-P3.xz,q.xz-P3.xz)*5.0);',
        '    a=clamp(max(glow.r,max(glow.g,glow.b))*0.42*u_caustics,0.0,0.6);}',
        '  gl_FragColor=vec4(glow*a,a);',
        '}',
    ].join('\n');

    // ------------------------------------------------------------------ 工具

    function hex(c) {
        var h = String(c).replace('#', '');
        if (h.length === 3) h = h[0] + h[0] + h[1] + h[1] + h[2] + h[2];
        var n = parseInt(h, 16);
        return [((n >> 16) & 255) / 255, ((n >> 8) & 255) / 255, (n & 255) / 255];
    }

    function merge(base, extra) {
        var out = {};
        for (var k in base) out[k] = base[k];
        for (var j in extra || {}) if (extra[j] !== undefined) out[j] = extra[j];
        return out;
    }

    function fallbackLayer(colors) {
        var el = document.createElement('div');
        el.setAttribute('aria-hidden', 'true');
        el.style.cssText =
            'position:absolute;inset:0;pointer-events:none;filter:blur(6px);background:' +
            'radial-gradient(18% 34% at 38% 52%,' + colors[0] + '8c,transparent 70%),' +
            'radial-gradient(16% 30% at 52% 46%,' + colors[1] + '80,transparent 70%),' +
            'radial-gradient(17% 32% at 63% 55%,' + colors[2] + '80,transparent 70%)';
        return el;
    }

    // ------------------------------------------------------------------ 主体

    function mount(container, options) {
        if (!container) throw new Error('LiuliGlass.mount: 需要一个容器元素');
        var opts = merge(DEFAULTS, options);
        var cleanups = [];
        var on = function (target, type, fn, o) {
            target.addEventListener(type, fn, o);
            cleanups.push(function () {
                target.removeEventListener(type, fn, o);
            });
        };

        if (getComputedStyle(container).position === 'static') container.style.position = 'relative';

        var canvas = document.createElement('canvas');
        canvas.setAttribute('aria-hidden', 'true');
        canvas.style.cssText =
            'position:absolute;inset:0;width:100%;height:100%;display:block;' +
            (opts.fadeIn ? 'opacity:0;transition:opacity ' + opts.fadeIn + 'ms cubic-bezier(.16,1,.3,1);' : '');
        container.appendChild(canvas);

        var gl = canvas.getContext('webgl', {
            alpha: true,
            antialias: false,
            premultipliedAlpha: true,
            powerPreference: 'high-performance',
        });

        function giveUp(reason) {
            if (reason) console.warn('[LiuliGlass] WebGL 不可用，改用静态画面：', reason);
            canvas.remove();
            var fb = opts.fallback ? fallbackLayer(opts.colors) : null;
            if (fb) container.appendChild(fb);
            return {
                supported: false,
                destroy: function () {
                    if (fb) fb.remove();
                },
                set: function () {},
                play: function () {},
                pause: function () {},
                wobble: function () {},
            };
        }
        if (!gl) return giveUp('getContext 返回空');

        function compile(type, src) {
            var s = gl.createShader(type);
            gl.shaderSource(s, src);
            gl.compileShader(s);
            if (!gl.getShaderParameter(s, gl.COMPILE_STATUS)) throw new Error(gl.getShaderInfoLog(s));
            return s;
        }
        var prog = gl.createProgram();
        try {
            gl.attachShader(prog, compile(gl.VERTEX_SHADER, VERT));
            gl.attachShader(prog, compile(gl.FRAGMENT_SHADER, FRAG));
            gl.linkProgram(prog);
            if (!gl.getProgramParameter(prog, gl.LINK_STATUS)) throw new Error(gl.getProgramInfoLog(prog));
        } catch (e) {
            return giveUp(e && e.message);
        }
        gl.useProgram(prog);

        // 一个盖满屏幕的大三角形
        gl.bindBuffer(gl.ARRAY_BUFFER, gl.createBuffer());
        gl.bufferData(gl.ARRAY_BUFFER, new Float32Array([-1, -1, 3, -1, -1, 3]), gl.STATIC_DRAW);
        var loc = gl.getAttribLocation(prog, 'a_pos');
        gl.enableVertexAttribArray(loc);
        gl.vertexAttribPointer(loc, 2, gl.FLOAT, false, 0, 0);

        var U = {};
        ['res', 'time', 'rot', 'wobble', 'spread', 'zoom', 'offsetY', 'caustics', 'inner', 'c0', 'c1', 'c2', 'c3', 'envTop', 'envBottom'].forEach(
            function (n) {
                U[n] = gl.getUniformLocation(prog, 'u_' + n);
            }
        );

        var baseZoom = 1.15;
        function applyLook() {
            var cs = opts.colors;
            for (var i = 0; i < 4; i++) gl.uniform3fv(U['c' + i], hex(cs[i % cs.length]));
            var env = ENV[opts.theme] || ENV.light;
            gl.uniform3fv(U.envTop, hex(opts.envTop || env.top));
            gl.uniform3fv(U.envBottom, hex(opts.envBottom || env.bottom));
            gl.uniform1f(U.offsetY, opts.offsetY);
            gl.uniform1f(U.caustics, opts.caustics);
            gl.uniform1f(U.inner, opts.innerGlow == null ? env.glow : opts.innerGlow);
        }
        applyLook();

        var mobile = matchMedia('(pointer: coarse)').matches;
        var mpr = opts.maxPixelRatio || {};
        var scale = Math.min(window.devicePixelRatio || 1, mobile ? mpr.mobile || 1.5 : mpr.desktop || 1.25);

        function resize() {
            var r = canvas.getBoundingClientRect();
            canvas.width = Math.max(1, Math.round(r.width * scale));
            canvas.height = Math.max(1, Math.round(r.height * scale));
            gl.viewport(0, 0, canvas.width, canvas.height);
            // 宽画面里玻璃分得开一些；窄画面把镜头拉远，整组都在画面里
            var aspect = r.width / Math.max(r.height, 1);
            gl.uniform1f(U.spread, Math.min(Math.max(aspect / 2.2, 0.7), 1.6));
            var z = aspect < 1.8 ? baseZoom * Math.pow(1.8 / Math.max(aspect, 1), 0.85) : baseZoom;
            gl.uniform1f(U.zoom, z * opts.zoom);
            if (!raf) draw();
        }
        var ro = new ResizeObserver(resize);
        ro.observe(canvas);
        cleanups.push(function () {
            ro.disconnect();
        });

        // ---- 交互
        var rot = { x: 0, y: 0 };
        var target = { x: 0, y: 0 };
        var wobble = 0;
        if (opts.interactive) {
            if (!container.style.touchAction) container.style.touchAction = 'pan-y';
            on(container, 'pointermove', function (e) {
                if (e.pointerType !== 'mouse' && !(e.buttons & 1)) return;
                var r = container.getBoundingClientRect();
                target.x = ((e.clientX - r.left) / r.width - 0.5) * 0.9;
                target.y = ((e.clientY - r.top) / r.height - 0.5) * -0.5;
                kick();
            });
            on(container, 'pointerleave', function () {
                target = { x: 0, y: 0 };
                kick();
            });
            on(container, 'pointerup', function (e) {
                if (e.pointerType !== 'mouse') target = { x: 0, y: 0 };
                kick();
            });
        }
        if (opts.wobbleOnPress) {
            on(container, 'pointerdown', function () {
                wobble = 1;
                kick();
            });
        }

        // ---- 动画循环
        var still = opts.respectReducedMotion && matchMedia('(prefers-reduced-motion: reduce)').matches;
        var visible = true;
        var paused = false;
        var raf = 0;
        var last = performance.now();
        var clock = opts.startTime;
        var shown = false;

        function draw() {
            gl.uniform2f(U.res, canvas.width, canvas.height);
            gl.uniform1f(U.time, clock);
            gl.uniform2f(U.rot, rot.x, rot.y);
            gl.uniform1f(U.wobble, wobble);
            gl.drawArrays(gl.TRIANGLES, 0, 3);
            if (!shown) {
                shown = true;
                canvas.style.opacity = '1';
            }
        }

        function frame(now) {
            raf = 0;
            var dt = Math.min((now - last) / 1000, 0.05);
            last = now;
            if (!still) clock += dt * opts.speed;
            rot.x += (target.x - rot.x) * 0.06;
            rot.y += (target.y - rot.y) * 0.06;
            wobble *= 0.965;
            draw();
            var settling = Math.abs(target.x - rot.x) + Math.abs(target.y - rot.y) > 0.001 || wobble > 0.01;
            if (!paused && (!opts.pauseWhenHidden || (visible && !document.hidden)) && (!still || settling)) {
                raf = requestAnimationFrame(frame);
            }
        }
        function kick() {
            if (!raf && !paused) {
                last = performance.now();
                raf = requestAnimationFrame(frame);
            }
        }

        if (opts.pauseWhenHidden) {
            var io = new IntersectionObserver(function (entries) {
                visible = entries[0].isIntersecting;
                if (visible) kick();
            });
            io.observe(container);
            cleanups.push(function () {
                io.disconnect();
            });
            on(document, 'visibilitychange', function () {
                if (!document.hidden) kick();
            });
        }

        resize();
        kick();

        return {
            supported: true,
            canvas: canvas,
            /** 运行时修改参数，例如 set({ theme: 'dark' }) 或 set({ colors: [...] }) */
            set: function (patch) {
                opts = merge(opts, patch);
                applyLook();
                resize();
                kick();
            },
            play: function () {
                paused = false;
                kick();
            },
            pause: function () {
                paused = true;
                if (raf) cancelAnimationFrame(raf);
                raf = 0;
            },
            /** 让玻璃晃一下，可以挂在按钮点击、搜索提交等时刻 */
            wobble: function () {
                wobble = 1;
                kick();
            },
            destroy: function () {
                if (raf) cancelAnimationFrame(raf);
                raf = 0;
                cleanups.forEach(function (fn) {
                    fn();
                });
                var lose = gl.getExtension('WEBGL_lose_context');
                if (lose) lose.loseContext();
                canvas.remove();
            },
        };
    }

    var LiuliGlass = { mount: mount, defaults: DEFAULTS, version: '1.0.0' };
    global.LiuliGlass = LiuliGlass;
    if (typeof module === 'object' && module.exports) module.exports = LiuliGlass;
})(typeof globalThis !== 'undefined' ? globalThis : window);
