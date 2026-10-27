// Package onboard 实现「一键接入」：通过 1Panel API 给 OpenResty 加上 liuli_json 日志格式和封禁规则的 include，
// 并把各网站的 access_log 从 main 切到 liuli_json；之后自动把新建的网站也切过来。
//
// 只做两种文本修改，别的一个字都不碰：
//  1. nginx.conf：在 `http {` 这一行后面插入缺少的 log_format / include；
//  2. 网站配置：把 `access_log <路径> main;` 这一行的 main 换成 liuli_json。
//
// 保存都交给 1Panel：它会执行 nginx -t，不通过就自动改回原内容。
package onboard

import (
	"errors"
	"regexp"
	"strings"
)

// FormatName 是观测台的日志格式名。
const FormatName = "liuli_json"

// LogFormat 是要加到 nginx.conf http 块里的日志格式（和部署说明里的一致）。
const LogFormat = `    log_format liuli_json escape=json '{'
      '"t":"$time_iso8601","host":"$host","ip":"$remote_addr",'
      '"xff":"$http_x_forwarded_for","cfip":"$http_cf_connecting_ip","xri":"$http_x_real_ip",'
      '"m":"$request_method","uri":"$request_uri","st":$status,'
      '"bytes":$bytes_sent,"rl":$request_length,"rt":$request_time,"urt":"$upstream_response_time",'
      '"ref":"$http_referer","ua":"$http_user_agent","proto":"$server_protocol"'
    '}';`

const marker = "    # 琉璃观测台：访问日志格式与封禁规则（可在观测台「设置 → OpenResty 接入」查看）"

var (
	reFormat = regexp.MustCompile(`(?m)^[ \t]*log_format[ \t]+liuli_json\b`)
	reHTTP   = regexp.MustCompile(`(?m)^[ \t]*http[ \t]*\{[^\n]*\n`)
	// access_log <路径> main;  —— 1Panel 建站时写的标准形式
	reMain = regexp.MustCompile(`(?m)^([ \t]*access_log[ \t]+[^\s;#]+[ \t]+)main([ \t]*;)`)
	reAny  = regexp.MustCompile(`(?m)^[ \t]*access_log[ \t]+([^;#\n]*);`)
)

// HasFormat 报告 nginx.conf 里是否已定义 liuli_json 日志格式（忽略注释行）。
func HasFormat(conf string) bool { return reFormat.MatchString(conf) }

// PatchNginx 在 nginx.conf 的 `http {` 后插入缺少的内容，返回新内容和插入的行（用于预览）。
func PatchNginx(conf string, addFormat bool, includeLine string) (string, []string, error) {
	if !addFormat && includeLine == "" {
		return conf, nil, nil
	}
	loc := reHTTP.FindStringIndex(conf)
	if loc == nil {
		return "", nil, errors.New("nginx.conf 里找不到 `http {` 这一行，没法自动修改，请按部署说明手动添加")
	}
	var add []string
	add = append(add, marker)
	if addFormat {
		add = append(add, LogFormat)
	}
	if includeLine != "" {
		add = append(add, "    "+includeLine)
	}
	block := strings.Join(add, "\n") + "\n"
	return conf[:loc[1]] + block + conf[loc[1]:], add[1:], nil
}

// 网站配置的接入状态
const (
	StateOK     = "ok"     // 已经是 liuli_json
	StateMain   = "main"   // 1Panel 默认的 main，可以自动切换
	StateOff    = "off"    // 访问日志被关掉了（access_log off）
	StateNone   = "none"   // 没有 access_log 指令
	StateCustom = "custom" // 自定义写法，不自动修改
)

// SiteState 判断网站配置里 access_log 的状态，line 是找到的那一行（去掉缩进）。
func SiteState(conf string) (state, line string) {
	ms := reAny.FindAllStringSubmatch(conf, -1)
	if len(ms) == 0 {
		return StateNone, ""
	}
	state = ""
	for _, m := range ms {
		args := strings.Fields(m[1])
		l := "access_log " + strings.Join(args, " ") + ";"
		switch {
		case len(args) == 1 && args[0] == "off":
			if state == "" {
				state, line = StateOff, l
			}
		case len(args) >= 2 && args[1] == FormatName:
			if state == "" || state == StateOff {
				state, line = StateOK, l
			}
		case len(args) == 2 && args[1] == "main":
			state, line = StateMain, l // 只要还有一处是 main，就算需要切换
		default:
			if state != StateMain {
				state, line = StateCustom, l
			}
		}
	}
	return state, line
}

// PatchSite 把网站配置里所有 `access_log <路径> main;` 换成 liuli_json，返回新内容和修改的行数。
func PatchSite(conf string) (string, int) {
	n := len(reMain.FindAllStringIndex(conf, -1))
	if n == 0 {
		return conf, 0
	}
	return reMain.ReplaceAllString(conf, "${1}"+FormatName+"${2}"), n
}
