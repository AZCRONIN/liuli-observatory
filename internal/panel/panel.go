// Package panel 是调用 1Panel API 的最小客户端，只用到封禁 IP 需要的三个接口：
// 查询 OpenResty 安装信息、读取 nginx.conf（检查是否 include 了封禁规则）、重载 OpenResty。
//
// 鉴权：1Panel-Token = HMAC-SHA256(API 密钥, "1panel:" + 时间戳)，密钥本身不在网络上传输。
// 1Panel 的 API 密钥拥有面板的全部权限，所以只在服务端使用，绝不返回给前端、不写日志。
package panel

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Client 调用 1Panel API。零值不可用，用 New 创建；未配置时 New 返回 nil。
type Client struct {
	base  string
	key   string
	keyID string
	hc    *http.Client
}

// New 创建客户端。base 例如 https://172.18.0.1:39541，key 为空时返回 nil（表示未配置）。
func New(base, key, keyID string) *Client {
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	key = strings.TrimSpace(key)
	if base == "" || key == "" {
		return nil
	}
	return &Client{
		base:  base,
		key:   key,
		keyID: strings.TrimSpace(keyID),
		hc: &http.Client{
			Timeout: 30 * time.Second,
			Transport: &http.Transport{
				// 1Panel 默认是自签名证书；请求只在本机的 Docker 网桥上传输，且用 HMAC 签名、不传密钥
				TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
				Proxy:           nil,
			},
		},
	}
}

type envelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rd)
	if err != nil {
		return err
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	mac := hmac.New(sha256.New, []byte(c.key))
	mac.Write([]byte("1panel:" + ts))
	req.Header.Set("1Panel-Token", hex.EncodeToString(mac.Sum(nil)))
	req.Header.Set("1Panel-Timestamp", ts)
	req.Header.Set("1Panel-Signature-Version", "hmac-sha256")
	if c.keyID != "" {
		req.Header.Set("1Panel-Key-ID", c.keyID)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("连不上 1Panel：%v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("1Panel 返回了无法识别的内容（HTTP %d），请检查面板地址", resp.StatusCode)
	}
	if env.Code != 200 {
		return fmt.Errorf("1Panel 拒绝了请求：%s", explain(env.Message))
	}
	if out != nil && len(env.Data) > 0 {
		return json.Unmarshal(env.Data, out)
	}
	return nil
}

// explain 把 1Panel 的错误码翻译成能照着处理的中文。
func explain(msg string) string {
	switch {
	case strings.Contains(msg, "StatusInvalid"):
		return "面板没有开启 API 接口（面板设置 → API 接口）"
	case strings.Contains(msg, "KeyTimeInvalid"):
		return "时间戳校验失败，服务器时间可能不准"
	case strings.Contains(msg, "KeyInvalid"):
		return "API 密钥不对"
	case strings.Contains(msg, "IPInvalid"):
		return "观测台的 IP 不在这个 API 密钥的白名单里"
	}
	if msg == "" {
		return "未知错误"
	}
	return msg
}

// OpenResty 是 1Panel 里 OpenResty 应用的安装信息。
type OpenResty struct {
	IsExist       bool   `json:"isExist"`
	AppInstallID  uint   `json:"appInstallId"`
	ContainerName string `json:"containerName"`
	Status        string `json:"status"`
	Version       string `json:"version"`
}

// OpenRestyInfo 查询 OpenResty 的安装信息（同时用来测试连接和密钥）。
func (c *Client) OpenRestyInfo(ctx context.Context) (OpenResty, error) {
	var o OpenResty
	err := c.do(ctx, http.MethodPost, "/api/v2/apps/installed/check", map[string]string{"key": "openresty"}, &o)
	if err == nil && !o.IsExist {
		err = errors.New("1Panel 里没有安装 OpenResty")
	}
	return o, err
}

// NginxConf 读取 OpenResty 的主配置 nginx.conf。
func (c *Client) NginxConf(ctx context.Context) (string, error) {
	var f struct {
		Content string `json:"content"`
	}
	err := c.do(ctx, http.MethodGet, "/api/v2/openresty", nil, &f)
	return f.Content, err
}

// Reload 平滑重载 OpenResty（等同面板上的「重载」：nginx -s reload，不断开现有连接）。
func (c *Client) Reload(ctx context.Context, installID uint) error {
	return c.do(ctx, http.MethodPost, "/api/v2/apps/installed/op", map[string]any{"installId": installID, "operate": "reload"}, nil)
}

// SaveNginxConf 保存 nginx.conf。1Panel 会先备份（backup=true）、写入后执行 nginx -t，不通过就自动改回原内容，通过则重载。
func (c *Client) SaveNginxConf(ctx context.Context, content string) error {
	return c.do(ctx, http.MethodPost, "/api/v2/openresty/file", map[string]any{"content": content, "backup": true}, nil)
}

// Website 是 1Panel 里的一个网站。
type Website struct {
	ID            uint   `json:"id"`
	PrimaryDomain string `json:"primaryDomain"`
	Alias         string `json:"alias"` // 网站目录名，日志在 /www/sites/<alias>/log/
	Type          string `json:"type"`  // proxy / static / runtime / deployment / stream …
	AccessLog     bool   `json:"accessLog"`
}

// Websites 列出 1Panel 里的所有网站。
func (c *Client) Websites(ctx context.Context) ([]Website, error) {
	var ws []Website
	err := c.do(ctx, http.MethodGet, "/api/v2/websites/list", nil, &ws)
	return ws, err
}

// SiteConf 读取网站的 OpenResty 配置文件内容。
func (c *Client) SiteConf(ctx context.Context, id uint) (string, error) {
	var f struct {
		Content string `json:"content"`
	}
	err := c.do(ctx, http.MethodGet, "/api/v2/websites/"+strconv.FormatUint(uint64(id), 10)+"/config/openresty", nil, &f)
	return f.Content, err
}

// SaveSiteConf 保存网站的配置文件。1Panel 写入后执行 nginx -t，不通过就自动改回原内容，通过则重载。
func (c *Client) SaveSiteConf(ctx context.Context, id uint, content string) error {
	return c.do(ctx, http.MethodPost, "/api/v2/websites/nginx/update", map[string]any{"id": id, "content": content}, nil)
}
