package classify

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/lionsoul2014/ip2region/binding/golang/xdb"
)

// Geo 用 ip2region 离线库查 IP 归属地。只在入库时调用，结果写进明细表。
type Geo struct {
	mu     sync.Mutex
	v4, v6 *xdb.Searcher
	cache  map[string][4]string
}

// OpenGeo 从目录里加载 ip2region_v4.xdb / ip2region_v6.xdb，缺哪个就不查哪个。
func OpenGeo(dir string) *Geo {
	g := &Geo{cache: make(map[string][4]string)}
	open := func(name string, v *xdb.Version) *xdb.Searcher {
		p := filepath.Join(dir, name)
		if _, err := os.Stat(p); err != nil {
			return nil
		}
		vi, err := xdb.LoadVectorIndexFromFile(p)
		if err != nil {
			return nil
		}
		s, err := xdb.NewWithVectorIndex(v, p, vi)
		if err != nil {
			return nil
		}
		return s
	}
	g.v4 = open("ip2region_v4.xdb", xdb.IPv4)
	g.v6 = open("ip2region_v6.xdb", xdb.IPv6)
	return g
}

// Loaded 报告是否至少加载了一个库。
func (g *Geo) Loaded() bool { return g != nil && (g.v4 != nil || g.v6 != nil) }

// Lookup 返回 国家、省份、城市、运营商。国外 IP 的国家名转成中文，省份留空。
func (g *Geo) Lookup(ip string) (country, province, city, isp string) {
	p := net.ParseIP(ip)
	if p == nil {
		return "未知", "", "", ""
	}
	if p.IsPrivate() || p.IsLoopback() || p.IsLinkLocalUnicast() || p.IsUnspecified() {
		return "内网", "", "", ""
	}
	if g == nil {
		return "未知", "", "", ""
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if v, ok := g.cache[ip]; ok {
		return v[0], v[1], v[2], v[3]
	}
	s := g.v4
	if p.To4() == nil {
		s = g.v6
	}
	country = "未知"
	if s != nil {
		if r, err := s.Search(ip); err == nil && r != "" {
			country, province, city, isp = parseRegion(r)
		}
	}
	if len(g.cache) > 50000 {
		g.cache = make(map[string][4]string)
	}
	g.cache[ip] = [4]string{country, province, city, isp}
	return
}

// parseRegion 解析 ip2region 的 "国家|省份|城市|运营商|国家代码" 格式。
func parseRegion(r string) (country, province, city, isp string) {
	f := strings.Split(r, "|")
	for len(f) < 5 {
		f = append(f, "0")
	}
	z := func(s string) string {
		if s == "0" || s == "Reserved" {
			return ""
		}
		return s
	}
	country, province, city, isp = z(f[0]), z(f[1]), z(f[2]), z(f[3])
	code := z(f[4])
	if f[0] == "Reserved" {
		return "内网", "", "", ""
	}
	if country == "中国" {
		// 港澳台在库里以省份形式出现，单独作为地区展示
		return country, province, city, isp
	}
	if n, ok := countryNames[code]; ok {
		country = n
	}
	if country == "" {
		country = "未知"
	}
	return country, "", city, isp
}

// countryNames 把常见国家代码转成中文名，没有的保留库里的英文名。
var countryNames = map[string]string{
	"CN": "中国", "HK": "中国香港", "MO": "中国澳门", "TW": "中国台湾",
	"US": "美国", "CA": "加拿大", "MX": "墨西哥", "BR": "巴西", "AR": "阿根廷", "CL": "智利", "CO": "哥伦比亚", "PE": "秘鲁",
	"GB": "英国", "IE": "爱尔兰", "FR": "法国", "DE": "德国", "NL": "荷兰", "BE": "比利时", "LU": "卢森堡", "CH": "瑞士", "AT": "奥地利",
	"IT": "意大利", "ES": "西班牙", "PT": "葡萄牙", "SE": "瑞典", "NO": "挪威", "FI": "芬兰", "DK": "丹麦", "IS": "冰岛",
	"PL": "波兰", "CZ": "捷克", "SK": "斯洛伐克", "HU": "匈牙利", "RO": "罗马尼亚", "BG": "保加利亚", "GR": "希腊", "UA": "乌克兰",
	"RU": "俄罗斯", "BY": "白俄罗斯", "LT": "立陶宛", "LV": "拉脱维亚", "EE": "爱沙尼亚", "MD": "摩尔多瓦", "RS": "塞尔维亚", "HR": "克罗地亚", "SI": "斯洛文尼亚",
	"TR": "土耳其", "IL": "以色列", "AE": "阿联酋", "SA": "沙特阿拉伯", "QA": "卡塔尔", "IR": "伊朗", "IQ": "伊拉克", "EG": "埃及",
	"ZA": "南非", "NG": "尼日利亚", "KE": "肯尼亚", "MA": "摩洛哥",
	"JP": "日本", "KR": "韩国", "KP": "朝鲜", "MN": "蒙古", "SG": "新加坡", "MY": "马来西亚", "TH": "泰国", "VN": "越南", "PH": "菲律宾",
	"ID": "印度尼西亚", "KH": "柬埔寨", "LA": "老挝", "MM": "缅甸", "IN": "印度", "PK": "巴基斯坦", "BD": "孟加拉国", "LK": "斯里兰卡", "NP": "尼泊尔",
	"KZ": "哈萨克斯坦", "UZ": "乌兹别克斯坦", "KG": "吉尔吉斯斯坦",
	"AU": "澳大利亚", "NZ": "新西兰",
}
