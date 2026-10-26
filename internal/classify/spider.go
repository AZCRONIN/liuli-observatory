package classify

import "strings"

// 蜘蛛分类，用于「蜘蛛」页分组展示。
const (
	CatSearch = "搜索引擎"
	CatAI     = "AI 爬虫"
	CatSEO    = "SEO 工具"
	CatSocial = "社交预览"
	CatOther  = "其他机器人"
)

type spiderRule struct {
	name string
	cat  string
	pats []string // 小写子串，任一命中即可
}

// 顺序有意义：越具体的越靠前。
var spiderRules = []spiderRule{
	{"百度", CatSearch, []string{"baiduspider"}},
	{"谷歌", CatSearch, []string{"googlebot", "adsbot-google", "mediapartners-google", "google-inspectiontool", "googleother", "storebot-google", "apis-google", "feedfetcher-google"}},
	{"必应", CatSearch, []string{"bingbot", "msnbot", "adidxbot", "bingpreview"}},
	{"搜狗", CatSearch, []string{"sogou web spider", "sogou inst spider", "sogou spider", "sogou-test-spider"}},
	{"360", CatSearch, []string{"360spider", "haosouspider"}},
	{"神马", CatSearch, []string{"yisouspider"}},
	{"头条", CatSearch, []string{"bytespider"}},
	{"花瓣（华为）", CatSearch, []string{"petalbot"}},
	{"Yandex", CatSearch, []string{"yandex"}},
	{"苹果", CatSearch, []string{"applebot"}},
	{"DuckDuckGo", CatSearch, []string{"duckduckbot", "duckassistbot"}},
	{"Naver", CatSearch, []string{"yeti/"}},
	{"Seznam", CatSearch, []string{"seznambot"}},
	{"雅虎", CatSearch, []string{"slurp"}},
	{"Coccoc", CatSearch, []string{"coccocbot"}},

	{"OpenAI", CatAI, []string{"gptbot", "chatgpt-user", "oai-searchbot"}},
	{"Anthropic", CatAI, []string{"claudebot", "claude-user", "claude-searchbot", "anthropic-ai"}},
	{"Perplexity", CatAI, []string{"perplexitybot", "perplexity-user"}},
	{"Common Crawl", CatAI, []string{"ccbot"}},
	{"Amazon", CatAI, []string{"amazonbot"}},
	{"Meta AI", CatAI, []string{"meta-externalagent", "meta-externalfetcher", "facebookbot"}},
	{"Cohere", CatAI, []string{"cohere-ai"}},
	{"Diffbot", CatAI, []string{"diffbot"}},
	{"ImagesiftBot", CatAI, []string{"imagesiftbot"}},

	{"Ahrefs", CatSEO, []string{"ahrefsbot", "ahrefssiteaudit"}},
	{"Semrush", CatSEO, []string{"semrushbot", "siteauditbot"}},
	{"Majestic", CatSEO, []string{"mj12bot"}},
	{"DotBot", CatSEO, []string{"dotbot"}},
	{"BLEXBot", CatSEO, []string{"blexbot"}},
	{"DataForSEO", CatSEO, []string{"dataforseobot"}},
	{"Serpstat", CatSEO, []string{"serpstatbot"}},
	{"Barkrowler", CatSEO, []string{"barkrowler"}},

	{"Facebook", CatSocial, []string{"facebookexternalhit", "facebookcatalog"}},
	{"Twitter", CatSocial, []string{"twitterbot"}},
	{"Telegram", CatSocial, []string{"telegrambot"}},
	{"WhatsApp", CatSocial, []string{"whatsapp"}},
	{"Discord", CatSocial, []string{"discordbot"}},
	{"Slack", CatSocial, []string{"slackbot", "slack-imgproxy"}},
	{"LinkedIn", CatSocial, []string{"linkedinbot"}},

	{"监控探针", CatOther, []string{"uptimerobot", "pingdom", "statuscake", "uptime-kuma", "site24x7", "better uptime", "hetrixtools"}},
	{"脚本工具", CatOther, []string{"curl/", "wget/", "python-requests", "python-urllib", "python-httpx", "aiohttp", "go-http-client", "okhttp", "java/", "apache-httpclient", "libwww-perl", "scrapy", "axios/", "node-fetch", "undici", "httpclient", "headlesschrome", "phantomjs", "zgrab", "masscan", "nmap", "nuclei", "censys", "expanse", "l9explore", "httpx -"}},
}

// 未命中具体规则、但 UA 明显是机器人的。
var genericBot = []string{"bot", "spider", "crawler", "crawl", "scan", "fetch", "monitor", "preview"}

// spiderCat 由蜘蛛名查分类。
var spiderCat = func() map[string]string {
	m := map[string]string{"其他爬虫": CatOther, "空 UA": CatOther}
	for _, r := range spiderRules {
		m[r.name] = r.cat
	}
	return m
}()

// Spider 返回 UA 命中的蜘蛛名，不是蜘蛛时返回空串。
func Spider(ua string) string {
	if strings.TrimSpace(ua) == "" {
		return "空 UA"
	}
	l := strings.ToLower(ua)
	for _, r := range spiderRules {
		for _, p := range r.pats {
			if strings.Contains(l, p) {
				return r.name
			}
		}
	}
	for _, p := range genericBot {
		if strings.Contains(l, p) {
			return "其他爬虫"
		}
	}
	return ""
}

// SpiderCategory 返回蜘蛛所属分类。
func SpiderCategory(name string) string {
	if c, ok := spiderCat[name]; ok {
		return c
	}
	return CatOther
}
