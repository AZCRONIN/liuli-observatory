#!/usr/bin/env python3
"""分析某一天的「访客」都是谁：帮助判断访客数是否被伪装成浏览器的采集程序抬高。

用法（在服务器上）：
    python3 visitors.py <主站日志> <日期> [资源站日志 ...]

例如主站 vcsoso.com 的图片都从图床 img.vcsoso.com 加载：
    python3 visitors.py /opt/1panel/www/sites/vcsoso.com/log/access.log 2026-10-27 \\
        /opt/1panel/www/sites/img.vcsoso.com/log/access.log

思路：真人用浏览器打开页面，会去加载页面里的图片 / 脚本 / 样式；只拿 HTML、从不加载资源的「访客」，
多半是伪装成浏览器 UA 的采集程序。资源站日志（图床、CDN 源站）可选，给了就把在资源站出现过的 IP 也算作「加载过资源」。
只统计观测台口径下的「访客」（非蜘蛛的 IP + UA）。
"""
import os
import re
import sys
from collections import Counter, defaultdict

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
from verify import BOT, STATIC, parse  # noqa: E402  与核对脚本共用解析和口径


def is_asset(uri):
    path = uri.split("?")[0].split("#")[0]
    last = path.rsplit("/", 1)[-1]
    return "." in last and last.rsplit(".", 1)[1].lower() in STATIC


def read(fn, day):
    with open(fn, encoding="utf-8", errors="replace") as f:
        for line in f:
            r = parse(line.strip())
            if r and r[0].startswith(day):
                yield r


def browser_version(ua):
    m = re.search(r"(Chrome|Firefox|Version)/(\d+)", ua)
    return (m.group(1), int(m.group(2))) if m else (None, 0)


def main():
    if len(sys.argv) < 3:
        print(__doc__)
        sys.exit(1)
    main_log, day, extra = sys.argv[1], sys.argv[2], sys.argv[3:]

    # 资源站：当天出现过的 IP（不含蜘蛛）
    asset_ips = set()
    for fn in extra:
        for t, ip, method, uri, st, nbytes, ua in read(fn, day):
            if ua.strip() and not BOT.search(ua):
                asset_ips.add(ip)

    # 主站：按访客（IP + UA）汇总
    v = defaultdict(lambda: {"req": 0, "pages": 0, "assets": 0, "paths": set()})
    for t, ip, method, uri, st, nbytes, ua in read(main_log, day):
        if not ua.strip() or BOT.search(ua):
            continue
        x = v[(ip, ua)]
        x["req"] += 1
        if is_asset(uri):
            x["assets"] += 1
        else:
            x["pages"] += 1
            if len(x["paths"]) < 50:
                x["paths"].add(uri.split("?")[0])

    total = len(v)
    loaded = {k for k, x in v.items() if x["assets"] > 0 or k[0] in asset_ips}
    bare = {k: x for k, x in v.items() if k not in loaded}
    print(f"日期 {day}，主站 {os.path.basename(os.path.dirname(os.path.dirname(main_log)))}")
    print(f"  访客（非蜘蛛 IP+UA）           {total:,}")
    print(f"  加载过资源的（很可能是真人）     {len(loaded):,}")
    print(f"  只拿网页、从不加载资源的         {len(bare):,}  （{len(bare) / max(total, 1) * 100:.1f}%）")
    if extra:
        print(f"  资源站当天出现的 IP              {len(asset_ips):,}")
    if not bare:
        return

    buckets = Counter()
    for x in bare.values():
        n = x["req"]
        buckets["1 次" if n == 1 else "2～5 次" if n <= 5 else "6～50 次" if n <= 50 else "50 次以上"] += 1
    print("\n「只拿网页」的访客，每人请求次数：")
    for k in ("1 次", "2～5 次", "6～50 次", "50 次以上"):
        print(f"    {k:<8} {buckets[k]:>8,}")

    ua_count = Counter(ua for (_, ua) in bare)
    print(f"\n「只拿网页」的访客用了 {len(ua_count):,} 种 UA，最多的 15 种（访客数）：")
    for ua, n in ua_count.most_common(15):
        print(f"    {n:>7,}  {ua[:120]}")

    old = sum(1 for (_, ua) in bare if browser_version(ua)[0] == "Chrome" and browser_version(ua)[1] < 110)
    print(f"\n  其中 Chrome 版本低于 110（2023 年初以前的版本，真人很少用）的访客：{old:,}")

    nets = Counter(".".join(ip.split(".")[:3]) + ".0/24" for (ip, _) in bare if ip.count(".") == 3)
    print("\n「只拿网页」的访客最集中的网段（/24，访客数）：")
    for net, n in nets.most_common(10):
        print(f"    {n:>7,}  {net}")

    ips = Counter()
    for (ip, _), x in bare.items():
        ips[ip] += x["req"]
    print("\n「只拿网页」请求最多的 IP：")
    for ip, n in ips.most_common(10):
        uas = len({ua for (i, ua) in bare if i == ip})
        print(f"    {n:>7,} 次  {ip}（{uas} 种 UA）")


if __name__ == "__main__":
    main()
