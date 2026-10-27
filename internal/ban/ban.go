// Package ban 实现封禁 IP：把生效中的封禁写成 OpenResty 的 deny 规则文件，再通过 1Panel API 平滑重载。
//
// 生效链路：观测台写 <规则目录>/deny.conf → nginx.conf 的 http 块里 include 这个文件 → 1Panel API 重载 OpenResty。
// 被封的 IP 访问任何站点都返回 403（仍然写访问日志，所以观测台能统计拦截次数）。
package ban

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"liuliobs/internal/model"
	"liuliobs/internal/panel"
	"liuliobs/internal/store"
)

// 默认放行、永远不允许封禁的网段：本机、内网、链路本地、组播等。
var reserved = []netip.Prefix{
	netip.MustParsePrefix("0.0.0.0/8"),
	netip.MustParsePrefix("10.0.0.0/8"),
	netip.MustParsePrefix("100.64.0.0/10"),
	netip.MustParsePrefix("127.0.0.0/8"),
	netip.MustParsePrefix("169.254.0.0/16"),
	netip.MustParsePrefix("172.16.0.0/12"),
	netip.MustParsePrefix("192.168.0.0/16"),
	netip.MustParsePrefix("224.0.0.0/4"),
	netip.MustParsePrefix("240.0.0.0/4"),
	netip.MustParsePrefix("::/128"),
	netip.MustParsePrefix("::1/128"),
	netip.MustParsePrefix("fc00::/7"),
	netip.MustParsePrefix("fe80::/10"),
	netip.MustParsePrefix("ff00::/8"),
}

// 最大允许封禁的网段：IPv4 不大于 /16，IPv6 不大于 /32，防止误操作把大片用户封掉。
const (
	minBitsV4 = 16
	minBitsV6 = 32
	maxRules  = 5000
)

// Status 是封禁功能的运行状态，展示在「封禁 IP」页面顶部。
type Status struct {
	PanelConfigured bool   `json:"panel_configured"` // 配置了 1Panel 地址和密钥
	PanelOK         bool   `json:"panel_ok"`         // 能连上 1Panel 且密钥有效
	PanelError      string `json:"panel_error"`
	DirOK           bool   `json:"dir_ok"` // 规则目录可写
	DirError        string `json:"dir_error"`
	IncludeOK       bool   `json:"include_ok"` // nginx.conf 里已 include 规则文件
	IncludeLine     string `json:"include_line"`
	Active          int    `json:"active"` // 生效中的封禁条数
	LastSync        int64  `json:"last_sync"`
	LastReload      int64  `json:"last_reload"`
	LastError       string `json:"last_error"`
	IPSourceOK      bool   `json:"ip_source_ok"` // 真实 IP 取法是 remote_addr（封禁按 $remote_addr 生效）
}

// Manager 管理封禁规则的生成与生效。
type Manager struct {
	Store       *store.Store
	Panel       *panel.Client // nil 表示没配置 1Panel API
	Dir         string        // 容器内的规则目录，例如 /ban
	IncludePath string        // OpenResty 看到的规则路径，例如 /www/liuli-ban/deny.conf

	mu        sync.Mutex
	st        Status
	lastBody  string
	install   uint
	lastCheck time.Time // 上次检查 1Panel 连接和 include 的时间
}

// 规则没变化时，多久复查一次 1Panel 连接和 nginx.conf 里的 include（避免频繁调用面板接口）
const healthEvery = 5 * time.Minute

// Enabled 报告封禁功能是否配置了规则目录。
func (m *Manager) Enabled() bool { return m != nil && m.Dir != "" }

// IncludeLine 是要加到 nginx.conf http 块里的那一行。
func (m *Manager) IncludeLine() string {
	return "include " + filepath.ToSlash(filepath.Dir(m.IncludePath)) + "/*.conf;"
}

// Validate 校验要封禁的目标，返回规范化后的写法（单个 IP 或 CIDR）。
// self 是操作者当前的 IP，whitelist 是设置里的白名单（IP 或 CIDR）。
func Validate(target, self string, whitelist []string) (string, error) {
	target = strings.TrimSpace(target)
	var pfx netip.Prefix
	if strings.Contains(target, "/") {
		p, err := netip.ParsePrefix(target)
		if err != nil {
			return "", fmt.Errorf("%q 不是合法的 IP 或网段", target)
		}
		pfx = p.Masked()
		min := minBitsV4
		if pfx.Addr().Is6() && !pfx.Addr().Is4In6() {
			min = minBitsV6
		}
		if pfx.Bits() < min {
			return "", fmt.Errorf("网段 %s 太大，IPv4 最多封到 /%d，IPv6 最多 /%d", pfx, minBitsV4, minBitsV6)
		}
	} else {
		a, err := netip.ParseAddr(target)
		if err != nil {
			return "", fmt.Errorf("%q 不是合法的 IP 或网段", target)
		}
		a = a.Unmap()
		pfx = netip.PrefixFrom(a, a.BitLen())
	}
	for _, r := range reserved {
		if r.Overlaps(pfx) {
			return "", fmt.Errorf("%s 是本机或内网地址，不允许封禁", pfx.Addr())
		}
	}
	if a, err := netip.ParseAddr(strings.TrimSpace(self)); err == nil && pfx.Contains(a.Unmap()) {
		return "", errors.New("这会把你自己当前的 IP 封掉，已拒绝")
	}
	for _, w := range whitelist {
		w = strings.TrimSpace(w)
		if w == "" {
			continue
		}
		var wp netip.Prefix
		if p, err := netip.ParsePrefix(w); err == nil {
			wp = p.Masked()
		} else if a, err := netip.ParseAddr(w); err == nil {
			wp = netip.PrefixFrom(a.Unmap(), a.Unmap().BitLen())
		} else {
			continue
		}
		if wp.Overlaps(pfx) {
			return "", fmt.Errorf("%s 在白名单 %s 里，不允许封禁", pfx, w)
		}
	}
	if pfx.IsSingleIP() {
		return pfx.Addr().String(), nil
	}
	return pfx.String(), nil
}

// render 生成 deny 规则文件内容。只写经过校验的 IP / CIDR，不写任何用户输入的文字。
func render(bans []model.Ban) string {
	var b strings.Builder
	b.WriteString("# 由琉璃观测台自动生成，请勿手动修改（改了也会被覆盖）。\n")
	n := 0
	for _, x := range bans {
		if n >= maxRules {
			break
		}
		// 二次校验：任何解析不了的条目都不写进 nginx 配置
		if _, err := netip.ParsePrefix(x.IP); err != nil {
			if _, err := netip.ParseAddr(x.IP); err != nil {
				continue
			}
		}
		if net.ParseIP(strings.Split(x.IP, "/")[0]) == nil {
			continue
		}
		fmt.Fprintf(&b, "deny %s;\n", x.IP)
		n++
	}
	return b.String()
}

// Sync 把生效中的封禁写入规则文件，内容有变化（或 force）时重载 OpenResty，并刷新状态。
func (m *Manager) Sync(ctx context.Context, force bool) error {
	if !m.Enabled() {
		return errors.New("没有配置封禁规则目录")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	st := m.Store.Settings()
	m.st.IPSourceOK = st.IPSource == "remote_addr"
	m.st.IncludeLine = m.IncludeLine()
	m.st.PanelConfigured = m.Panel != nil
	m.Store.ExpireBans(now)
	bans, err := m.Store.Bans(true, now)
	if err != nil {
		return m.fail(err)
	}
	m.st.Active = len(bans)
	m.st.LastSync = now.Unix()

	body := render(bans)
	changed := body != m.lastBody
	if changed || force {
		if err := writeAtomic(filepath.Join(m.Dir, "deny.conf"), body); err != nil {
			m.st.DirOK, m.st.DirError = false, err.Error()
			return m.fail(fmt.Errorf("写规则文件失败：%v", err))
		}
		m.st.DirOK, m.st.DirError = true, ""
	}
	if m.Panel == nil {
		m.lastBody = body
		return m.fail(errors.New("还没配置 1Panel API，规则已写入文件，但要在 1Panel 里手动重载 OpenResty 才生效"))
	}
	if !changed && !force && time.Since(m.lastCheck) < healthEvery {
		return nil
	}
	m.lastCheck = now
	cctx, cancel := context.WithTimeout(ctx, 40*time.Second)
	defer cancel()
	info, err := m.Panel.OpenRestyInfo(cctx)
	if err != nil {
		m.st.PanelOK, m.st.PanelError = false, err.Error()
		return m.fail(err)
	}
	m.st.PanelOK, m.st.PanelError = true, ""
	m.install = info.AppInstallID
	if conf, err := m.Panel.NginxConf(cctx); err == nil {
		m.st.IncludeOK = HasInclude(conf, m.IncludePath)
	}
	if changed || force {
		if err := m.Panel.Reload(cctx, m.install); err != nil {
			return m.fail(fmt.Errorf("重载 OpenResty 失败：%v", err))
		}
		m.st.LastReload = now.Unix()
		m.lastBody = body
	}
	if !m.st.IncludeOK {
		m.st.LastError = "nginx.conf 里还没有 include 规则文件，封禁不会生效"
		return nil
	}
	m.st.LastError = ""
	return nil
}

func (m *Manager) fail(err error) error {
	m.st.LastError = err.Error()
	return err
}

// Status 返回当前状态。
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.st
}

// Run 每 15 秒处理一次到期解封；规则没变化时每 5 分钟复查一次 1Panel 连接和 include 是否还在。
func (m *Manager) Run(ctx context.Context) {
	if !m.Enabled() {
		return
	}
	m.Sync(ctx, true)
	tick := time.NewTicker(15 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			m.Sync(ctx, false)
		}
	}
}

// HasInclude 检查 nginx.conf 里有没有（未被注释的）include 能匹配到规则文件。
func HasInclude(conf, includePath string) bool {
	dir := filepath.ToSlash(filepath.Dir(includePath))
	for _, line := range strings.Split(conf, "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "#") || !strings.HasPrefix(l, "include") {
			continue
		}
		l = strings.TrimSuffix(strings.TrimSpace(strings.TrimPrefix(l, "include")), ";")
		l = strings.TrimSpace(l)
		if l == includePath || l == dir+"/*.conf" || l == dir+"/*" {
			return true
		}
	}
	return false
}

func writeAtomic(path, body string) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
