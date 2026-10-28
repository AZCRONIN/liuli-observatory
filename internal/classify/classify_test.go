package classify

import (
	"testing"

	"liuliobs/internal/model"
)

func TestSpider(t *testing.T) {
	cases := map[string]string{
		"Mozilla/5.0 (compatible; Baiduspider/2.0; +http://www.baidu.com/search/spider.html)":                            "百度",
		"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; Googlebot/2.1; +http://www.google.com/bot.html)": "谷歌",
		"Mozilla/5.0 (compatible; bingbot/2.0; +http://www.bing.com/bingbot.htm)":                                        "必应",
		"Mozilla/5.0 (compatible; Bytespider; spider-feedback@bytedance.com)":                                            "头条",
		"Mozilla/5.0 AppleWebKit/537.36 (KHTML, like Gecko; compatible; GPTBot/1.2; +https://openai.com/gptbot)":         "OpenAI",
		"curl/8.5.0":          "脚本工具",
		"":                    "空 UA",
		"SomeRandomCrawler/1": "其他爬虫",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148 MicroMessenger/8.0.61": "",
	}
	for ua, want := range cases {
		if got := Spider(ua); got != want {
			t.Errorf("%q：期望 %q，实际 %q", ua, want, got)
		}
	}
}

func TestClient(t *testing.T) {
	b, o, d := Client("Mozilla/5.0 (iPhone; CPU iPhone OS 18_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148 MicroMessenger/8.0.61", "")
	if b != "微信" || o != "iOS" || d != "手机" {
		t.Fatal(b, o, d)
	}
	b, o, d = Client("Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36 Edg/141.0.0.0", "")
	if b != "Edge" || o != "Windows" || d != "电脑" {
		t.Fatal(b, o, d)
	}
	b, _, d = Client("Mozilla/5.0 (iPad; CPU OS 18_6 like Mac OS X) Version/18.6 Mobile/15E148 Safari/604.1", "")
	if b != "Safari" || d != "平板" {
		t.Fatal(b, d)
	}
}

func TestPV(t *testing.T) {
	c := New(nil, model.DefaultSettings().PV)
	ok := func(method string, st int, path, spider string) bool {
		return c.IsPV(&model.Record{Method: method, Status: st, Path: path, Spider: spider})
	}
	if !ok("GET", 200, "/title/1", "") || !ok("GET", 304, "/", "") {
		t.Fatal("页面应算 PV")
	}
	if ok("GET", 200, "/_astro/a.js", "") || ok("GET", 200, "/covers/1.webp", "") || ok("GET", 200, "/api/x", "") ||
		ok("GET", 404, "/x", "") || ok("POST", 200, "/x", "") || ok("GET", 200, "/x", "百度") || ok("GET", 200, "/robots.txt", "") {
		t.Fatal("不应算 PV")
	}
}

func TestRefHost(t *testing.T) {
	if refHost("https://www.sosoquark.com/a", "sosoquark.com") != "" || refHost("https://m.sosoquark.com/", "sosoquark.com") != "" {
		t.Fatal("站内跳转应为空")
	}
	if refHost("https://www.baidu.com/link?x", "sosoquark.com") != "baidu.com" {
		t.Fatal("来源域名不对")
	}
}

func TestBackground(t *testing.T) {
	for _, p := range []string{"/sw.js", "/static/service-worker.js", "/favicon.ico", "/favicon-32x32.png",
		"/apple-touch-icon-120x120-precomposed.png", "/manifest.webmanifest", "/site.webmanifest", "/robots.txt"} {
		if !Background(p) {
			t.Errorf("%s 应是后台请求", p)
		}
	}
	for _, p := range []string{"/", "/s/abc.html", "/static/js/app.js", "/api/search", "/news/sw"} {
		if Background(p) {
			t.Errorf("%s 不应是后台请求", p)
		}
	}
	c := New(nil, model.DefaultSettings().PV)
	if c.IsPage("/manifest.webmanifest") {
		t.Error("webmanifest 不应算页面")
	}
}
