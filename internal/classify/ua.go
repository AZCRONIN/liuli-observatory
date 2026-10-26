package classify

import "strings"

type uaRule struct {
	name string
	pats []string
}

// 浏览器：国内壳浏览器的标识要排在 Chrome / Safari 之前。
var browserRules = []uaRule{
	{"微信", []string{"micromessenger"}},
	{"QQ", []string{" qq/", "mqqbrowser", "qqbrowser"}},
	{"夸克", []string{"quark/"}},
	{"UC", []string{"ucbrowser", "ucweb"}},
	{"百度", []string{"baiduboxapp", "bidubrowser", "baidubrowser"}},
	{"360", []string{"360se", "360ee", "qihoobrowser", "qhbrowser"}},
	{"搜狗", []string{"metasr", "sogoumobilebrowser", "sogousearch"}},
	{"钉钉", []string{"dingtalk"}},
	{"飞书", []string{"lark/", "feishu"}},
	{"抖音", []string{"aweme", "bytedancewebview"}},
	{"小米", []string{"miuibrowser", "xiaomi/miuibrowser"}},
	{"华为", []string{"huaweibrowser"}},
	{"vivo", []string{"vivobrowser"}},
	{"OPPO", []string{"heytapbrowser", "oppobrowser"}},
	{"三星", []string{"samsungbrowser"}},
	{"Edge", []string{"edg/", "edga/", "edgios/", "edge/"}},
	{"Opera", []string{"opr/", "opera"}},
	{"Yandex", []string{"yabrowser"}},
	{"Firefox", []string{"firefox/", "fxios/"}},
	{"Chrome", []string{"chrome/", "crios/", "chromium/"}},
	{"Safari", []string{"safari/"}},
	{"IE", []string{"msie ", "trident/"}},
}

var osRules = []uaRule{
	{"HarmonyOS", []string{"harmonyos", "openharmony"}},
	{"iPadOS", []string{"ipad"}},
	{"iOS", []string{"iphone", "ipod"}},
	{"Android", []string{"android"}},
	{"Windows", []string{"windows"}},
	{"macOS", []string{"mac os x", "macintosh"}},
	{"ChromeOS", []string{"cros "}},
	{"Linux", []string{"linux", "x11"}},
}

func match(rules []uaRule, l string) string {
	for _, r := range rules {
		for _, p := range r.pats {
			if strings.Contains(l, p) {
				return r.name
			}
		}
	}
	return "其他"
}

// Client 返回浏览器、操作系统、设备类型。spider 非空时设备记为「爬虫」。
func Client(ua, spider string) (browser, os, device string) {
	l := strings.ToLower(ua)
	if spider != "" {
		return "爬虫", match(osRules, l), "爬虫"
	}
	browser = match(browserRules, l)
	os = match(osRules, l)
	switch {
	case strings.Contains(l, "ipad") || strings.Contains(l, "tablet") ||
		(strings.Contains(l, "android") && !strings.Contains(l, "mobile")):
		device = "平板"
	case strings.Contains(l, "mobi") || strings.Contains(l, "iphone") || strings.Contains(l, "android") || strings.Contains(l, "harmonyos"):
		device = "手机"
	case os == "Windows" || os == "macOS" || os == "Linux" || os == "ChromeOS":
		device = "电脑"
	default:
		device = "其他"
	}
	return
}
