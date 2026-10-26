// Package parse 把一行访问日志解析成 model.Record。
// 支持本项目推荐的 liuli_json 格式，以及 Nginx 默认的 combined 格式（作为后备，没有耗时字段）。
package parse

import (
	"bytes"
	"encoding/json"
	"errors"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"liuliobs/internal/model"
)

// ErrSkip 表示这一行不是访问日志（空行、注释等），直接跳过即可。
var ErrSkip = errors.New("skip")

// flex 兼容数字和字符串两种写法（"-"、"" 视为缺失）。
type flex string

func (f *flex) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		*f = flex(s)
		return nil
	}
	*f = flex(b)
	return nil
}

func (f flex) float() (float64, bool) {
	s := strings.TrimSpace(string(f))
	if s == "" || s == "-" || s == "null" {
		return 0, false
	}
	// upstream_response_time 可能是 "0.012, 0.003" 或 "0.01 : 0.02"，累加各段
	if strings.ContainsAny(s, ",:") {
		var sum float64
		ok := false
		for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ':' || r == ' ' }) {
			if v, err := strconv.ParseFloat(p, 64); err == nil {
				sum += v
				ok = true
			}
		}
		return sum, ok
	}
	v, err := strconv.ParseFloat(s, 64)
	return v, err == nil
}

func (f flex) int() int64 {
	v, ok := f.float()
	if !ok {
		return 0
	}
	return int64(v)
}

type jsonLine struct {
	T     string `json:"t"`
	Host  string `json:"host"`
	IP    string `json:"ip"`
	XFF   string `json:"xff"`
	CFIP  string `json:"cfip"`
	XRI   string `json:"xri"`
	M     string `json:"m"`
	URI   string `json:"uri"`
	St    flex   `json:"st"`
	Bytes flex   `json:"bytes"`
	RL    flex   `json:"rl"`
	RT    flex   `json:"rt"`
	URT   flex   `json:"urt"`
	Ref   string `json:"ref"`
	UA    string `json:"ua"`
	Proto string `json:"proto"`
}

// combined: $remote_addr - $remote_user [$time_local] "$request" $status $body_bytes_sent "$http_referer" "$http_user_agent" ["$http_x_forwarded_for"]
var combinedRe = regexp.MustCompile(`^(\S+) \S+ \S+ \[([^\]]+)\] "((?:[^"\\]|\\.)*)" (\d{3}) (\d+|-) "((?:[^"\\]|\\.)*)" "((?:[^"\\]|\\.)*)"(?: "((?:[^"\\]|\\.)*)")?`)

// Line 解析一行日志。ipSource 决定真实 IP 的取法，见 model.Settings.IPSource。
func Line(line []byte, ipSource string) (model.Record, error) {
	line = bytes.TrimSpace(line)
	if len(line) == 0 || line[0] == '#' {
		return model.Record{}, ErrSkip
	}
	if line[0] == '{' {
		return parseJSON(line, ipSource)
	}
	return parseCombined(line, ipSource)
}

func parseJSON(line []byte, ipSource string) (model.Record, error) {
	var j jsonLine
	if err := json.Unmarshal(line, &j); err != nil {
		return model.Record{}, err
	}
	t, err := time.Parse(time.RFC3339, j.T)
	if err != nil {
		return model.Record{}, err
	}
	r := model.Record{
		TS:      t.Unix(),
		IP:      pickIP(ipSource, j.IP, j.CFIP, j.XRI, j.XFF),
		Method:  j.M,
		URI:     j.URI,
		Proto:   j.Proto,
		Status:  int(j.St.int()),
		Bytes:   j.Bytes.int(),
		ReqLen:  j.RL.int(),
		RT:      -1,
		URT:     -1,
		Referer: dash(j.Ref),
		UA:      dash(j.UA),
	}
	if v, ok := j.RT.float(); ok {
		r.RT = v
	}
	if v, ok := j.URT.float(); ok {
		r.URT = v
	}
	r.Path = pathOf(r.URI)
	return r, nil
}

func parseCombined(line []byte, ipSource string) (model.Record, error) {
	m := combinedRe.FindSubmatch(line)
	if m == nil {
		return model.Record{}, errors.New("无法识别的日志格式")
	}
	t, err := time.Parse("02/Jan/2006:15:04:05 -0700", string(m[2]))
	if err != nil {
		return model.Record{}, err
	}
	st, _ := strconv.Atoi(string(m[4]))
	b, _ := strconv.ParseInt(string(m[5]), 10, 64)
	r := model.Record{
		TS:      t.Unix(),
		IP:      pickIP(ipSource, string(m[1]), "", "", unesc(m[8])),
		Status:  st,
		Bytes:   b,
		RT:      -1,
		URT:     -1,
		Referer: dash(unesc(m[6])),
		UA:      dash(unesc(m[7])),
	}
	req := strings.SplitN(unesc(m[3]), " ", 3)
	switch len(req) {
	case 3:
		r.Method, r.URI, r.Proto = req[0], req[1], req[2]
	case 2:
		r.Method, r.URI = req[0], req[1]
	default:
		r.URI = req[0]
	}
	r.Path = pathOf(r.URI)
	return r, nil
}

func unesc(b []byte) string {
	if bytes.IndexByte(b, '\\') < 0 {
		return string(b)
	}
	s := string(b)
	s = strings.ReplaceAll(s, `\"`, `"`)
	s = strings.ReplaceAll(s, `\\`, `\`)
	return s
}

func dash(s string) string {
	if s == "-" {
		return ""
	}
	return s
}

func pathOf(uri string) string {
	if i := strings.IndexAny(uri, "?#"); i >= 0 {
		uri = uri[:i]
	}
	if uri == "" {
		return "/"
	}
	return uri
}

// pickIP 按设置选择真实访客 IP，取不到合法 IP 时退回 remote_addr。
func pickIP(source, remote, cfip, xri, xff string) string {
	var cand string
	switch source {
	case "cf":
		cand = cfip
		if cand == "" {
			cand = xri
		}
	case "x_real_ip":
		cand = xri
	case "xff":
		if i := strings.IndexByte(xff, ','); i >= 0 {
			xff = xff[:i]
		}
		cand = xff
	}
	cand = strings.TrimSpace(cand)
	if cand != "" && net.ParseIP(cand) != nil {
		return cand
	}
	return remote
}
