#!/usr/bin/env python3
"""手工核对某一天的统计数字（独立实现，不依赖观测台），并拆解数字是怎么来的。

用法（在服务器上）：
    python3 verify.py /opt/1panel/www/sites/sosoquark.com/log/access.log 2026-10-21

输出的 请求、流量、IP 数 应和观测台「概览」选那一天时完全一致；
浏览量、访客、蜘蛛按默认规则计算，若在「设置」里改过 PV 规则，这里也要同步改。
支持 liuli_json 和 Nginx 默认格式（main / combined）。日志被切割过时，把切下来的文件也一起传进来：
    python3 verify.py access.log.1 access.log 2026-10-21
"""
import json
import re
import sys
from collections import Counter
from datetime import datetime

STATIC = set("js mjs css map json xml txt png jpg jpeg gif webp avif svg ico bmp woff woff2 ttf otf eot "
             "mp4 webm m3u8 ts mp3 ogg wav zip gz rar 7z pdf wasm".split())
PREFIX = ("/api/", "/_astro/", "/.well-known/")
PATHS = {"/favicon.ico", "/robots.txt", "/sitemap.xml", "/ads.txt"}
# 和观测台的蜘蛛判断一致：已知蜘蛛 + 常见脚本工具 + 含 bot/spider/crawl 等字样
BOT = re.compile(r"bot|spider|crawl|scan|fetch|monitor|preview|slurp|yeti/|yandex|petal|bytespider|sogou|"
                 r"curl/|wget/|python-|aiohttp|go-http-client|okhttp|java/|apache-httpclient|libwww|scrapy|axios/|"
                 r"node-fetch|undici|httpclient|headlesschrome|phantomjs|zgrab|masscan|nmap|nuclei|censys|expanse|"
                 r"facebookexternalhit|whatsapp|meta-external|diffbot|cohere-ai|anthropic-ai|claude-|chatgpt-user|"
                 r"perplexity|pingdom|uptime|statuscake|site24x7|hetrixtools|l9explore|httpx -", re.I)
# 宝塔「蜘蛛」只算这些搜索引擎
SEARCH = re.compile(r"baiduspider|googlebot|bingbot|msnbot|sogou|360spider|haosouspider|yisouspider|bytespider|"
                    r"petalbot|yandex|applebot|duckduckbot|slurp", re.I)
# 和观测台一致：浏览器 / App 后台自动请求的文件不算访客、不算浏览量
BG = {"sw.js", "service-worker.js", "serviceworker.js", "firebase-messaging-sw.js", "ngsw-worker.js",
      "robots.txt", "browserconfig.xml", "manifest.json"}


def background(path):
    base = path.rsplit("/", 1)[-1].lower()
    return base in BG or base.startswith(("favicon", "apple-touch-icon")) or base.endswith(".webmanifest")


COMBINED = re.compile(r'^(\S+) \S+ \S+ \[([^\]]+)\] "((?:[^"\\]|\\.)*)" (\d{3}) (\d+|-) "((?:[^"\\]|\\.)*)" "((?:[^"\\]|\\.)*)"')


def parse(line):
    if line.startswith("{"):
        try:
            j = json.loads(line)
        except ValueError:
            return None
        return j.get("t", ""), j.get("ip", ""), j.get("m", ""), j.get("uri") or "/", int(j.get("st") or 0), \
            int(j.get("bytes") or 0), j.get("ua", "") or ""
    m = COMBINED.match(line)
    if not m:
        return None
    t = datetime.strptime(m.group(2), "%d/%b/%Y:%H:%M:%S %z").isoformat()
    req = m.group(3).split(" ")
    method, uri = (req[0], req[1]) if len(req) >= 2 else ("", req[0])
    ua = m.group(7)
    return t, m.group(1), method, uri, int(m.group(4)), int(m.group(5)) if m.group(5) != "-" else 0, \
        "" if ua == "-" else ua


def main():
    if len(sys.argv) < 3:
        print(__doc__)
        sys.exit(1)
    *files, day = sys.argv[1:]
    req = bytes_ = pv = spider = search = 0
    ips, human_ips, uv = set(), set(), set()
    bot_ua, ip_req, human_ip_req = Counter(), Counter(), Counter()
    for fn in files:
        with open(fn, encoding="utf-8", errors="replace") as f:
            for line in f:
                r = parse(line.strip())
                if not r or not r[0].startswith(day):
                    continue
                t, ip, method, uri, st, nbytes, ua = r
                req += 1
                bytes_ += nbytes
                ips.add(ip)
                ip_req[ip] += 1
                if ua.strip() == "" or BOT.search(ua):
                    spider += 1
                    if SEARCH.search(ua):
                        search += 1
                    bot_ua[ua[:110] or "(空 UA)"] += 1
                    continue
                human_ips.add(ip)
                human_ip_req[ip] += 1
                path = uri.split("?")[0].split("#")[0] or "/"
                if background(path):
                    continue
                uv.add((ip, ua))
                last = path.rsplit("/", 1)[-1]
                ext = last.rsplit(".", 1)[1].lower() if "." in last else ""
                if (method == "GET" and 200 <= st < 400 and ext not in STATIC
                        and not path.startswith(PREFIX) and path not in PATHS):
                    pv += 1
    print(f"日期 {day}")
    print(f"  请求      {req:,}")
    print(f"  流量      {bytes_:,} 字节（{bytes_ / 1024 / 1024:.2f} MB）")
    print(f"  IP 数     {len(ips):,}")
    print(f"  浏览量    {pv:,}")
    print(f"  访客      {len(uv):,}")
    print(f"  蜘蛛爬虫  {spider:,}")
    print()
    print("拆解（帮助判断数字为什么偏高）：")
    print(f"  IP 数（不含蜘蛛）            {len(human_ips):,}   ← 宝塔的 IP 数比访客还少，推测是这个口径")
    print(f"  蜘蛛里属于搜索引擎的请求     {search:,}   ← 宝塔的蜘蛛数推测是这个口径")
    print(f"  其他机器人（AI/SEO/脚本/空UA）{spider - search:,}")
    print("\n  请求最多的机器人 UA：")
    for ua, n in bot_ua.most_common(10):
        print(f"    {n:>8,}  {ua}")
    print("\n  请求最多的 IP（含蜘蛛）：")
    for ip, n in ip_req.most_common(10):
        print(f"    {n:>8,}  {ip}")
    print("\n  请求最多的 IP（不含蜘蛛，看有没有伪装成浏览器的采集）：")
    for ip, n in human_ip_req.most_common(10):
        print(f"    {n:>8,}  {ip}")


if __name__ == "__main__":
    main()
