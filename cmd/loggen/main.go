// loggen 生成逼真的 OpenResty 访问日志，用于本地演示和测试（没有真实日志时也能跑起来）。
//
//	go run ./cmd/loggen -out demo/sosoquark.com/log/access.log -days 3 -per-day 30000
//	go run ./cmd/loggen -out demo/sosoquark.com/log/access.log -days 0 -follow   # 持续追加实时日志
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var (
	out    = flag.String("out", "demo/sosoquark.com/log/access.log", "输出文件（追加写入）")
	site   = flag.String("site", "sosoquark.com", "站点域名")
	days   = flag.Int("days", 3, "生成过去多少天的历史日志（含今天到此刻）")
	perDay = flag.Int("per-day", 30000, "每天大约多少请求")
	follow = flag.Bool("follow", false, "生成完历史后继续按实时速度追加")
	format = flag.String("format", "json", "json（liuli_json）或 combined")
	seed   = flag.Int64("seed", 0, "随机种子，0 表示按时间")
)

type line struct {
	t                    time.Time
	ip, method, uri, ref string
	ua, proto            string
	st                   int
	bytes, rl            int64
	rt                   float64
	upstream             bool
}

var rng *rand.Rand

func main() {
	flag.Parse()
	s := *seed
	if s == 0 {
		s = time.Now().UnixNano()
	}
	rng = rand.New(rand.NewSource(s))
	if err := os.MkdirAll(filepath.Dir(*out), 0o755); err != nil {
		log.Fatal(err)
	}
	f, err := os.OpenFile(*out, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Fatal(err)
	}
	defer f.Close()
	w := bufio.NewWriterSize(f, 1<<20)

	now := time.Now()
	if *days > 0 {
		y, m, d := now.Date()
		start := time.Date(y, m, d, 0, 0, 0, 0, now.Location()).AddDate(0, 0, -(*days - 1))
		total := 0
		for h := start; h.Before(now); h = h.Add(time.Hour) {
			end := h.Add(time.Hour)
			if end.After(now) {
				end = now
			}
			ls := genWindow(h, end)
			sort.Slice(ls, func(i, j int) bool { return ls[i].t.Before(ls[j].t) })
			for _, l := range ls {
				write(w, l)
			}
			total += len(ls)
		}
		w.Flush()
		log.Printf("已写入 %d 行历史日志到 %s", total, *out)
	}
	if !*follow {
		return
	}
	log.Printf("持续追加实时日志（Ctrl+C 结束）…")
	last := time.Now()
	for range time.Tick(time.Second) {
		now := time.Now()
		ls := genWindow(last, now)
		sort.Slice(ls, func(i, j int) bool { return ls[i].t.Before(ls[j].t) })
		for _, l := range ls {
			write(w, l)
		}
		w.Flush()
		last = now
	}
}

// 一天里各小时的流量权重：凌晨低谷、午间小高峰、晚上 20–22 点最高
func hourWeight(t time.Time) float64 {
	h := float64(t.Hour()) + float64(t.Minute())/60
	base := 0.25 + 0.75*math.Pow(math.Sin(math.Pi*(h-5)/24), 2)
	if h < 5 {
		base = 0.2 + 0.05*(5-h)
	}
	noon := math.Exp(-math.Pow(h-12, 2) / 2)
	night := math.Exp(-math.Pow(h-20.5, 2) / 4)
	return base*0.6 + noon*0.35 + night*0.9
}

// genWindow 生成 [from, to) 时间段内的日志：按会话生成，蜘蛛和扫描器单独生成。
func genWindow(from, to time.Time) []line {
	secs := to.Sub(from).Seconds()
	if secs <= 0 {
		return nil
	}
	// 平均每个会话约 22 个请求（页面 + 静态资源）
	expectReq := float64(*perDay) / 86400 * secs * hourWeight(from) * 1.35
	sessions := poisson(expectReq * 0.78 / 22)
	var ls []line
	for i := 0; i < sessions; i++ {
		ls = append(ls, session(randTime(from, to))...)
	}
	for i, n := 0, poisson(expectReq*0.16); i < n; i++ {
		ls = append(ls, spiderHit(randTime(from, to)))
	}
	for i, n := 0, poisson(expectReq*0.03); i < n; i++ {
		ls = append(ls, scanHit(randTime(from, to)))
	}
	// 会话里的后续请求可能超出窗口，截掉，保证日志按时间递增
	kept := ls[:0]
	for _, l := range ls {
		if l.t.Before(to) {
			kept = append(kept, l)
		}
	}
	return kept
}

func randTime(from, to time.Time) time.Time {
	return from.Add(time.Duration(rng.Int63n(int64(to.Sub(from)) + 1)))
}

func poisson(lambda float64) int {
	if lambda <= 0 {
		return 0
	}
	if lambda > 50 {
		v := int(math.Round(rng.NormFloat64()*math.Sqrt(lambda) + lambda))
		if v < 0 {
			return 0
		}
		return v
	}
	l, k, p := math.Exp(-lambda), 0, 1.0
	for {
		p *= rng.Float64()
		if p <= l {
			return k
		}
		k++
	}
}

func pick[T any](xs []T) T { return xs[rng.Intn(len(xs))] }

// ---- 访客 ----

var cnPrefixes = []string{"113.92", "113.88", "120.229", "183.14", "183.62", "223.104", "36.112", "117.136", "58.250", "61.140",
	"111.30", "112.96", "114.86", "115.236", "116.22", "119.123", "121.35", "122.96", "123.58", "124.64", "125.88", "171.43",
	"175.10", "180.168", "182.150", "202.96", "218.18", "219.133", "220.200", "221.4", "222.70", "27.38", "39.144", "42.120",
	"14.153", "1.192", "101.228", "106.120", "110.52", "139.207", "140.207", "153.3", "157.0", "163.142"}
var foreignPrefixes = []string{"8.217", "104.28", "185.220", "45.87", "51.15", "103.152", "203.0", "210.140", "176.9", "95.216",
	"2.58", "31.13", "62.210", "78.46", "81.2", "89.187", "151.101", "172.217", "1.36", "49.255", "61.244", "118.163"}

var browserUAs = []string{
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36 Edg/141.0.0.0",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64; rv:143.0) Gecko/20100101 Firefox/143.0",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Safari/605.1.15",
	"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 26_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/26.0 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148 MicroMessenger/8.0.61(0x18003d2a) NetType/WIFI Language/zh_CN",
	"Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148 Quark/7.12.0.2490 Mobile",
	"Mozilla/5.0 (iPad; CPU OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.6 Mobile/15E148 Safari/604.1",
	"Mozilla/5.0 (Linux; Android 15; 23127PN0CC Build/AQ3A.240829.003) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/131.0.6778.135 Mobile Safari/537.36 XiaoMi/MiuiBrowser/19.6.80",
	"Mozilla/5.0 (Linux; Android 14; V2309A) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/123.0.6312.118 Mobile Safari/537.36 VivoBrowser/24.5.10.0",
	"Mozilla/5.0 (Linux; Android 13; PHB110) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/115.0.5790.168 Mobile Safari/537.36 HeyTapBrowser/40.10.2.1",
	"Mozilla/5.0 (Linux; Android 15; SM-S9280) AppleWebKit/537.36 (KHTML, like Gecko) SamsungBrowser/28.0 Chrome/130.0.0.0 Mobile Safari/537.36",
	"Mozilla/5.0 (Linux; Android 14; ALN-AL00) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/114.0.5735.196 Mobile Safari/537.36 MQQBrowser/15.6 Mobile",
	"Mozilla/5.0 (Linux; U; Android 14; zh-CN; V2241A) AppleWebKit/537.36 (KHTML, like Gecko) Version/4.0 Chrome/123.0.6312.80 UCBrowser/17.6.6.1374 Mobile Safari/537.36",
	"Mozilla/5.0 (Linux; Android 12; HarmonyOS; NOH-AN00) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/114.0.5735.196 Mobile Safari/537.36 HuaweiBrowser/15.0.4.312",
	"Mozilla/5.0 (Linux; Android 15; Pixel 9) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Mobile Safari/537.36",
	"Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36",
	"Mozilla/5.0 (Windows NT 10.0; WOW64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/108.0.5359.95 Safari/537.36 QIHU 360SE",
	"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36 Edg/141.0.0.0",
}

// 越靠前越常见
var browserWeights = []int{18, 9, 3, 4, 5, 14, 12, 6, 2, 6, 4, 3, 2, 3, 2, 3, 1, 1, 1, 1}

var titles = []string{"沙丘", "流浪地球", "繁花", "三体", "奥本海默", "封神", "庆余年", "漫长的季节", "葬送的芙莉莲", "鬼灭之刃", "进击的巨人", "热辣滚烫", "飞驰人生", "哪吒之魔童闹海", "唐探1900", "白夜追凶"}
var refs = []string{"", "", "", "", "", "https://www.baidu.com/link?url=abc", "https://www.google.com/", "https://cn.bing.com/search?q=%E7%89%87%E6%BA%90",
	"https://www.sogou.com/web?query=x", "https://m.sm.cn/s?q=x", "https://www.so.com/s?q=x", "https://weibo.com/", "https://www.douban.com/group/topic/1",
	"https://tieba.baidu.com/p/1", "https://www.zhihu.com/question/1", "https://t.me/", "https://www.v2ex.com/t/1", "https://quark.cn/", "https://www.bilibili.com/video/BV1"}

func weightedUA() string {
	sum := 0
	for _, w := range browserWeights {
		sum += w
	}
	n := rng.Intn(sum)
	for i, w := range browserWeights {
		if n < w {
			return browserUAs[i]
		}
		n -= w
	}
	return browserUAs[0]
}

func randIP(prefixes []string) string {
	return fmt.Sprintf("%s.%d.%d", pick(prefixes), rng.Intn(256), 1+rng.Intn(254))
}

func pageURI() string {
	switch r := rng.Float64(); {
	case r < 0.18:
		return "/"
	case r < 0.50:
		return fmt.Sprintf("/title/%d", 1000+int(math.Abs(rng.NormFloat64())*800))
	case r < 0.68:
		return "/search?q=" + urlEnc(pick(titles))
	case r < 0.78:
		return pick([]string{"/movie", "/tv", "/anime", "/hot"})
	case r < 0.86:
		return fmt.Sprintf("/movie?page=%d", 1+rng.Intn(20))
	case r < 0.92:
		return fmt.Sprintf("/tv/%d", 200+rng.Intn(300))
	default:
		return pick([]string{"/about", "/hot?type=week", "/anime/rank", "/title/missing-page"})
	}
}

func urlEnc(s string) string {
	var b strings.Builder
	for _, c := range []byte(s) {
		fmt.Fprintf(&b, "%%%02X", c)
	}
	return b.String()
}

var assets = []string{"/_astro/index.Bd3kF9.js", "/_astro/client.C8aQ2.js", "/_astro/Loupe.D2kq.css", "/_astro/base.A91x.css",
	"/fonts/puhuiti/400-000.woff2", "/fonts/puhuiti/700-000.woff2", "/favicon.svg", "/favicon.ico"}

func rt(base float64) float64 {
	v := base * math.Exp(rng.NormFloat64()*0.6)
	if rng.Float64() < 0.008 {
		v = 1 + rng.Float64()*4 // 偶发慢请求
	}
	return math.Round(v*1000) / 1000
}

func session(start time.Time) []line {
	ip := randIP(cnPrefixes)
	if rng.Float64() < 0.12 {
		ip = randIP(foreignPrefixes)
	}
	ua := weightedUA()
	pages := 1 + int(math.Abs(rng.NormFloat64())*2.5)
	t := start
	ref := pick(refs)
	var ls []line
	for p := 0; p < pages; p++ {
		uri := pageURI()
		st := 200
		switch r := rng.Float64(); {
		case strings.HasSuffix(uri, "missing-page") || r < 0.025:
			st = 404
		case r < 0.032:
			st = pick([]int{500, 502, 504})
		case r < 0.05:
			st = 301
		case r < 0.09:
			st = 304
		}
		l := line{t: t, ip: ip, method: "GET", uri: uri, ref: ref, ua: ua, proto: "HTTP/2.0", st: st,
			bytes: int64(8000 + rng.Intn(60000)), rl: int64(400 + rng.Intn(600)), rt: rt(0.045), upstream: true}
		if st >= 500 {
			l.rt = rt(0.8)
			l.bytes = 560
		}
		if st == 301 || st == 304 {
			l.bytes = int64(180 + rng.Intn(200))
		}
		if strings.HasPrefix(uri, "/search") {
			l.rt = rt(0.18)
		}
		ls = append(ls, l)
		// 页面带出的静态资源和接口请求
		if p == 0 {
			for _, a := range assets {
				if rng.Float64() < 0.85 {
					ast := 200
					size := int64(3000 + rng.Intn(200000))
					if rng.Float64() < 0.4 {
						ast, size = 304, 180
					}
					ls = append(ls, line{t: t.Add(time.Duration(50+rng.Intn(800)) * time.Millisecond), ip: ip, method: "GET", uri: a,
						ref: "https://" + *site + uri, ua: ua, proto: "HTTP/2.0", st: ast, bytes: size, rl: 300, rt: rt(0.002)})
				}
			}
		}
		for c, n := 0, 6+rng.Intn(10); c < n; c++ {
			ls = append(ls, line{t: t.Add(time.Duration(100+rng.Intn(3000)) * time.Millisecond), ip: ip, method: "GET",
				uri: fmt.Sprintf("/covers/%d.webp", rng.Intn(5000)), ref: "https://" + *site + uri, ua: ua, proto: "HTTP/2.0",
				st: 200, bytes: int64(15000 + rng.Intn(90000)), rl: 320, rt: rt(0.004)})
		}
		if strings.HasPrefix(uri, "/title/") {
			ls = append(ls, line{t: t.Add(time.Duration(300+rng.Intn(1500)) * time.Millisecond), ip: ip, method: "POST",
				uri: "/api/resolve", ref: "https://" + *site + uri, ua: ua, proto: "HTTP/2.0", st: 200,
				bytes: int64(500 + rng.Intn(3000)), rl: 900, rt: rt(0.6), upstream: true})
		}
		ref = "https://" + *site + uri
		t = t.Add(time.Duration(5+rng.Intn(90)) * time.Second)
	}
	return ls
}

// ---- 蜘蛛与扫描器 ----

type bot struct {
	ua     string
	ips    []string
	weight int
}

var bots = []bot{
	{"Mozilla/5.0 (compatible; Baiduspider/2.0; +http://www.baidu.com/search/spider.html)", []string{"220.181.108", "116.179.32", "180.76.15"}, 30},
	{"Mozilla/5.0 (Linux; Android 6.0.1; Nexus 5X Build/MMB29P) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.7390.122 Mobile Safari/537.36 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)", []string{"66.249.66", "66.249.79"}, 18},
	{"Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)", []string{"40.77.167", "207.46.13", "157.55.39"}, 12},
	{"Mozilla/5.0 (Linux; Android 5.0) AppleWebKit/537.36 (KHTML, like Gecko) Mobile Safari/537.36 (compatible; Bytespider; spider-feedback@bytedance.com)", []string{"110.249.201", "111.225.148"}, 14},
	{"Sogou web spider/4.0(+http://www.sogou.com/docs/help/webmasters.htm#07)", []string{"123.126.113", "106.38.241"}, 6},
	{"Mozilla/5.0 (compatible; YisouSpider/1.0; http://www.yisou.com)", []string{"42.156.137", "42.120.160"}, 5},
	{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/91.0.4472.114 Safari/537.36 360Spider", []string{"180.153.236", "101.226.168"}, 3},
	{"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; GPTBot/1.2; +https://openai.com/gptbot)", []string{"20.171.207"}, 4},
	{"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; ClaudeBot/1.0; +claudebot@anthropic.com)", []string{"216.73.216"}, 3},
	{"Mozilla/5.0 (compatible; AhrefsBot/7.0; +http://ahrefs.com/robot/)", []string{"5.39.1", "54.36.148"}, 3},
	{"Mozilla/5.0 (compatible; SemrushBot/7~bl; +http://www.semrush.com/bot.html)", []string{"185.191.171"}, 2},
	{"Mozilla/5.0 (Linux; Android 7.0;) AppleWebKit/537.36 (KHTML, like Gecko) Mobile Safari/537.36 (compatible; PetalBot;+https://webmaster.petalsearch.com/site/petalbot)", []string{"114.119.157"}, 3},
	{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/17.4 Safari/605.1.15 (Applebot/0.1; +http://www.apple.com/go/applebot)", []string{"17.241.219"}, 1},
}

func spiderHit(t time.Time) line {
	sum := 0
	for _, b := range bots {
		sum += b.weight
	}
	n := rng.Intn(sum)
	b := bots[0]
	for _, x := range bots {
		if n < x.weight {
			b = x
			break
		}
		n -= x.weight
	}
	uri := pageURI()
	if rng.Float64() < 0.08 {
		uri = pick([]string{"/robots.txt", "/sitemap.xml"})
	}
	st := 200
	if rng.Float64() < 0.04 {
		st = 404
	}
	return line{t: t, ip: fmt.Sprintf("%s.%d", pick(b.ips), 1+rng.Intn(254)), method: "GET", uri: uri, ua: b.ua, proto: "HTTP/1.1",
		st: st, bytes: int64(6000 + rng.Intn(50000)), rl: 300, rt: rt(0.06), upstream: true}
}

var scanPaths = []string{"/wp-login.php", "/.env", "/xmlrpc.php", "/phpmyadmin/", "/.git/config", "/admin.php", "/vendor/phpunit/phpunit/src/Util/PHP/eval-stdin.php", "/config.json", "/actuator/health", "/boaform/admin/formLogin"}
var scanUAs = []string{"", "curl/8.5.0", "python-requests/2.32.3", "Go-http-client/1.1", "Mozilla/5.0 zgrab/0.x", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36"}
var scanIPs = []string{"45.148.10.95", "193.41.206.12", "87.236.176.4", "167.94.138.60", "204.76.203.18", "185.242.226.9"}

func scanHit(t time.Time) line {
	return line{t: t, ip: pick(scanIPs), method: pick([]string{"GET", "GET", "POST", "HEAD"}), uri: pick(scanPaths), ua: pick(scanUAs),
		proto: "HTTP/1.1", st: pick([]int{404, 404, 404, 403, 400}), bytes: 548, rl: 200, rt: rt(0.001)}
}

// ---- 输出 ----

func write(w *bufio.Writer, l line) {
	urt := ""
	if l.upstream {
		urt = fmt.Sprintf("%.3f", math.Max(0, l.rt-0.001))
	}
	if *format == "combined" {
		fmt.Fprintf(w, "%s - - [%s] \"%s %s %s\" %d %d \"%s\" \"%s\"\n", l.ip, l.t.Format("02/Jan/2006:15:04:05 -0700"),
			l.method, l.uri, l.proto, l.st, l.bytes, dashIf(l.ref), dashIf(l.ua))
		return
	}
	m := map[string]any{
		"t": l.t.Format(time.RFC3339), "host": *site, "ip": l.ip, "xff": "", "cfip": "", "xri": "",
		"m": l.method, "uri": l.uri, "st": l.st, "bytes": l.bytes, "rl": l.rl, "rt": l.rt, "urt": urt,
		"ref": l.ref, "ua": l.ua, "proto": l.proto,
	}
	b, _ := json.Marshal(m)
	w.Write(b)
	w.WriteByte('\n')
}

func dashIf(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
