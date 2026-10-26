// Package model 定义各模块之间传递的数据结构。
package model

// Record 是一行访问日志解析并补全后的结果，也是明细表的一行。
type Record struct {
	ServerID string
	Site     string
	TS       int64 // Unix 秒
	IP       string
	Method   string
	URI      string // 原始请求 URI（含查询串）
	Path     string // 去掉查询串的路径
	Proto    string
	Status   int
	Bytes    int64   // bytes_sent，出站
	ReqLen   int64   // request_length，入站；combined 格式下为 0
	RT       float64 // request_time，秒；combined 格式下为 -1
	URT      float64 // upstream_response_time，秒；没有上游时为 -1
	Referer  string
	RefHost  string // 来源域名（站外，空表示直接访问或站内跳转）
	UA       string

	Country  string
	Province string
	City     string
	ISP      string

	Browser string
	OS      string
	Device  string
	Spider  string // 命中的蜘蛛名，空表示不是蜘蛛
	IsPV    bool
	Visitor int64 // (IP + UA) 的哈希
}

// Settings 是可以在界面「设置」里修改的运行参数，存在 settings 表里。
type Settings struct {
	PV                  PVRules `json:"pv"`
	DetailRetentionDays int     `json:"detail_retention_days"`
	AggRetentionDays    int     `json:"agg_retention_days"`
	SlowThresholdMs     int     `json:"slow_threshold_ms"`
	// IPSource 决定真实访客 IP 的取法：remote_addr / cf / x_real_ip / xff
	IPSource string `json:"ip_source"`
}

// PVRules 是浏览量的排除规则。
type PVRules struct {
	StaticExt       []string `json:"static_ext"`       // 静态资源扩展名，不带点
	ExcludePrefixes []string `json:"exclude_prefixes"` // 排除的路径前缀，例如 /api/
	ExcludePaths    []string `json:"exclude_paths"`    // 精确排除的路径，例如 /robots.txt
}

// DefaultSettings 返回第一次启动时使用的设置。
func DefaultSettings() Settings {
	return Settings{
		PV: PVRules{
			StaticExt: []string{
				"js", "mjs", "css", "map", "json", "xml", "txt",
				"png", "jpg", "jpeg", "gif", "webp", "avif", "svg", "ico", "bmp",
				"woff", "woff2", "ttf", "otf", "eot",
				"mp4", "webm", "m3u8", "ts", "mp3", "ogg", "wav",
				"zip", "gz", "rar", "7z", "pdf", "wasm",
			},
			ExcludePrefixes: []string{"/api/", "/_astro/", "/.well-known/"},
			ExcludePaths:    []string{"/favicon.ico", "/robots.txt", "/sitemap.xml", "/ads.txt"},
		},
		DetailRetentionDays: 7,
		AggRetentionDays:    365,
		SlowThresholdMs:     1000,
		IPSource:            "remote_addr",
	}
}

// Site 是「站点 ↔ 日志路径」的对应关系。
type Site struct {
	ServerID string `json:"server_id"`
	Site     string `json:"site"`
	LogPath  string `json:"log_path"`
	Enabled  bool   `json:"enabled"`
}

// ---- 多台模式：Agent → Hub 推送协议（第一版只定义，hub 端返回 501） ----

// PushBatch 是 Agent 一次推送的内容。Hub 以 (server_id, site, minute) 为单位幂等：
// 收到某分钟的数据时，先删除该分钟已有的明细再整体写入，所以重复推送不会重复计数。
// 站长的量级（约每天 10 万请求）下直接推送全量明细，Hub 端的聚合逻辑和单台模式完全一样。
type PushBatch struct {
	ServerID string       `json:"server_id"`
	Minutes  []PushMinute `json:"minutes"`
}

// PushMinute 是某站点某一分钟（已结束的分钟）的全部明细。
type PushMinute struct {
	Site    string   `json:"site"`
	Minute  int64    `json:"minute"` // 分钟起点的 Unix 秒
	Records []Record `json:"records"`
}

// ---- 第二版：封禁 IP（第一版只预留结构和接口） ----

// Ban 是一条封禁记录。生效方式：生成 deny 规则文件给 OpenResty include 并重载。
type Ban struct {
	ID       int64  `json:"id"`
	ServerID string `json:"server_id"` // 空表示所有服务器
	Site     string `json:"site"`      // 空表示所有站点
	IP       string `json:"ip"`        // 单个 IP 或 CIDR
	Reason   string `json:"reason"`
	Source   string `json:"source"` // manual / auto
	Created  int64  `json:"created"`
	Expires  int64  `json:"expires"` // 0 表示永久
	Active   bool   `json:"active"`
}
