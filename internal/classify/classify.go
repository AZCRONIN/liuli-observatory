// Package classify 给解析后的日志补上蜘蛛、客户端、地区、是否 PV、访客哈希等信息。
package classify

import (
	"hash/fnv"
	"net/url"
	"path"
	"strings"
	"sync"

	"liuliobs/internal/model"
)

// Classifier 持有 PV 规则和 IP 库，规则可以在运行时替换（设置页修改后）。
type Classifier struct {
	Geo   *Geo
	mu    sync.RWMutex
	ext   map[string]bool
	pref  []string
	exact map[string]bool
}

// New 用给定的 PV 规则创建分类器。
func New(geo *Geo, rules model.PVRules) *Classifier {
	c := &Classifier{Geo: geo}
	c.SetRules(rules)
	return c
}

// SetRules 替换 PV 排除规则。
func (c *Classifier) SetRules(r model.PVRules) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ext = make(map[string]bool, len(r.StaticExt))
	for _, e := range r.StaticExt {
		c.ext[strings.ToLower(strings.TrimPrefix(strings.TrimSpace(e), "."))] = true
	}
	c.pref = nil
	for _, p := range r.ExcludePrefixes {
		if p = strings.TrimSpace(p); p != "" {
			c.pref = append(c.pref, p)
		}
	}
	c.exact = make(map[string]bool, len(r.ExcludePaths))
	for _, p := range r.ExcludePaths {
		if p = strings.TrimSpace(p); p != "" {
			c.exact[p] = true
		}
	}
}

// Enrich 补全记录里的派生字段。site 用于判断来源是否为站内跳转。
func (c *Classifier) Enrich(r *model.Record) {
	r.Spider = Spider(r.UA)
	r.Browser, r.OS, r.Device = Client(r.UA, r.Spider)
	r.Country, r.Province, r.City, r.ISP = c.Geo.Lookup(r.IP)
	r.RefHost = refHost(r.Referer, r.Site)
	r.IsPV = c.IsPV(r)
	r.Visitor = VisitorHash(r.IP, r.UA)
}

// IsPV 按口径判断：非蜘蛛、2xx/3xx、GET、页面类请求。
func (c *Classifier) IsPV(r *model.Record) bool {
	if r.Spider != "" || r.Status < 200 || r.Status >= 400 {
		return false
	}
	if r.Method != "" && r.Method != "GET" {
		return false
	}
	return c.IsPage(r.Path)
}

// IsPage 判断路径是不是页面（不是静态资源、不在排除列表里）。
func (c *Classifier) IsPage(p string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	if c.exact[p] {
		return false
	}
	for _, pre := range c.pref {
		if strings.HasPrefix(p, pre) {
			return false
		}
	}
	if e := path.Ext(p); e != "" && c.ext[strings.ToLower(e[1:])] {
		return false
	}
	return true
}

// VisitorHash 是访客（IP + UA）的 64 位哈希。
func VisitorHash(ip, ua string) int64 {
	h := fnv.New64a()
	h.Write([]byte(ip))
	h.Write([]byte{0})
	h.Write([]byte(ua))
	return int64(h.Sum64() >> 1)
}

// refHost 取来源域名；站内跳转（同域或子域）和直接访问返回空串。
func refHost(ref, site string) string {
	if ref == "" {
		return ""
	}
	u, err := url.Parse(ref)
	if err != nil || u.Host == "" {
		return ""
	}
	h := strings.ToLower(u.Hostname())
	h = strings.TrimPrefix(h, "www.")
	s := strings.TrimPrefix(strings.ToLower(site), "www.")
	if h == s || strings.HasSuffix(h, "."+s) {
		return ""
	}
	return h
}

// SearchEngine 由来源域名判断是不是搜索引擎，返回引擎名或空串。
func SearchEngine(host string) string {
	switch {
	case strings.Contains(host, "baidu."):
		return "百度"
	case strings.Contains(host, "google."):
		return "谷歌"
	case strings.Contains(host, "bing.com"):
		return "必应"
	case strings.Contains(host, "sogou.com"):
		return "搜狗"
	case host == "so.com" || strings.HasSuffix(host, ".so.com"):
		return "360"
	case strings.Contains(host, "sm.cn"):
		return "神马"
	case strings.Contains(host, "toutiao.com") || strings.Contains(host, "douyin.com"):
		return "头条"
	case strings.Contains(host, "yandex."):
		return "Yandex"
	case strings.Contains(host, "duckduckgo.com"):
		return "DuckDuckGo"
	case strings.Contains(host, "yahoo."):
		return "雅虎"
	case strings.Contains(host, "quark.cn"):
		return "夸克"
	case strings.Contains(host, "perplexity.ai"):
		return "Perplexity"
	case strings.Contains(host, "chatgpt.com"):
		return "ChatGPT"
	}
	return ""
}
