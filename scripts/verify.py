#!/usr/bin/env python3
"""手工核对某一天的统计数字（独立实现，不依赖观测台）。

用法（在服务器上）：
    python3 verify.py /opt/1panel/www/sites/sosoquark.com/log/access.log 2026-10-21

输出的 请求、流量、IP 数 应和观测台「概览」选那一天时完全一致；
浏览量、访客、蜘蛛按默认规则计算，若在「设置」里改过 PV 规则，这里也要同步改。
只支持 liuli_json 日志格式。日志被切割过时，把切下来的文件也一起传进来：
    python3 verify.py access.log.1 access.log 2026-10-21
"""
import json
import re
import sys

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


def main():
    if len(sys.argv) < 3:
        print(__doc__)
        sys.exit(1)
    *files, day = sys.argv[1:]
    req = bytes_ = pv = spider = 0
    ips, uv = set(), set()
    for fn in files:
        with open(fn, encoding="utf-8", errors="replace") as f:
            for line in f:
                line = line.strip()
                if not line.startswith("{"):
                    continue
                try:
                    j = json.loads(line)
                except ValueError:
                    continue
                if not j.get("t", "").startswith(day):
                    continue
                req += 1
                bytes_ += int(j.get("bytes") or 0)
                ip = j.get("ip", "")
                ips.add(ip)
                ua = j.get("ua", "") or ""
                if ua.strip() == "" or BOT.search(ua):
                    spider += 1
                    continue
                uv.add((ip, ua))
                path = (j.get("uri") or "/").split("?")[0].split("#")[0] or "/"
                last = path.rsplit("/", 1)[-1]
                ext = last.rsplit(".", 1)[1].lower() if "." in last else ""
                st = int(j.get("st") or 0)
                if (j.get("m") == "GET" and 200 <= st < 400 and ext not in STATIC
                        and not path.startswith(PREFIX) and path not in PATHS):
                    pv += 1
    print(f"日期 {day}")
    print(f"  请求      {req:,}")
    print(f"  流量      {bytes_:,} 字节（{bytes_ / 1024 / 1024:.2f} MB）")
    print(f"  IP 数     {len(ips):,}")
    print(f"  浏览量    {pv:,}")
    print(f"  访客      {len(uv):,}")
    print(f"  蜘蛛爬虫  {spider:,}")


if __name__ == "__main__":
    main()
