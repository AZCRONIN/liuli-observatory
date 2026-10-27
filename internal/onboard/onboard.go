package onboard

import (
	"context"
	"errors"
	"fmt"
	"log"
	"path"
	"slices"
	"strings"
	"sync"
	"time"

	"liuliobs/internal/ban"
	"liuliobs/internal/panel"
	"liuliobs/internal/store"
)

// Manager 负责检查和执行接入。所有修改操作串行执行。
type Manager struct {
	Store       *store.Store
	Panel       *panel.Client // nil 表示没配置 1Panel API
	Ban         *ban.Manager  // 用来拿 include 行；nginx.conf 改完后让它刷新状态
	LogRoot     string        // 容器里的日志根目录，用来对应「手动删除过的站点」
	mu          sync.Mutex    // 串行化所有修改操作
	amu         sync.Mutex    // 只保护下面两个状态字段
	lastAuto    int64
	lastAutoMsg string
}

// Site 是一个网站的接入状态。
type Site struct {
	ID      uint   `json:"id"`
	Domain  string `json:"domain"`
	Alias   string `json:"alias"`
	State   string `json:"state"` // ok / main / off / none / custom / error / skip
	Line    string `json:"line"`  // 当前的 access_log 行
	Note    string `json:"note"`  // 跳过原因或错误
	Backup  bool   `json:"backup"`
	Skipped bool   `json:"skipped"` // 撤销过，自动接入不会再动它
}

// Report 是接入检查的结果。
type Report struct {
	PanelConfigured bool   `json:"panel_configured"`
	Error           string `json:"error"`
	Format          bool   `json:"format"`  // nginx.conf 已定义 liuli_json
	Include         bool   `json:"include"` // nginx.conf 已 include 封禁规则
	IncludeLine     string `json:"include_line"`
	Sites           []Site `json:"sites"`
	Auto            bool   `json:"auto"`
	LastAuto        int64  `json:"last_auto"`
	LastAutoMsg     string `json:"last_auto_msg"`
}

// Change 是一处将要做 / 已经做的修改。
type Change struct {
	Target string   `json:"target"` // nginx.conf 或网站域名
	SiteID uint     `json:"site_id,omitempty"`
	Lines  []string `json:"lines"` // 人能看懂的修改说明
	Error  string   `json:"error,omitempty"`
	Done   bool     `json:"done"`
}

func (m *Manager) includeLine() string {
	if m.Ban == nil || !m.Ban.Enabled() {
		return ""
	}
	return m.Ban.IncludeLine()
}

func (m *Manager) includePath() string {
	if m.Ban == nil {
		return ""
	}
	return m.Ban.IncludePath
}

// skipReason 返回网站不需要 / 不应该接入的原因，空串表示可以接入。
func (m *Manager) skipReason(w panel.Website) string {
	st := m.Store.Settings()
	if w.Type == "stream" {
		return "TCP/UDP 转发站点，没有 HTTP 访问日志"
	}
	if slices.Contains(st.SelfHosts, strings.ToLower(w.PrimaryDomain)) || slices.Contains(st.SelfHosts, strings.ToLower(w.Alias)) {
		return "观测台自己的域名，不统计"
	}
	if m.LogRoot != "" && slices.Contains(st.IgnoredLogs, path.Join(m.LogRoot, w.Alias, "log", "access.log")) {
		return "你在观测台里删除过这个站点"
	}
	return ""
}

// Inspect 读取 nginx.conf 和每个网站的配置，汇报接入状态。
func (m *Manager) Inspect(ctx context.Context) Report {
	st := m.Store.Settings()
	r := Report{PanelConfigured: m.Panel != nil, IncludeLine: m.includeLine(), Auto: st.AutoOnboard, Sites: []Site{}}
	m.amu.Lock()
	r.LastAuto, r.LastAutoMsg = m.lastAuto, m.lastAutoMsg
	m.amu.Unlock()
	if m.Panel == nil {
		r.Error = "还没配置 1Panel API（.env 里的 LIULI_1PANEL_URL / LIULI_1PANEL_API_KEY），只能按部署说明手动接入"
		return r
	}
	conf, err := m.Panel.NginxConf(ctx)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	r.Format = HasFormat(conf)
	r.Include = r.IncludeLine == "" || ban.HasInclude(conf, m.includePath())
	sites, err := m.Panel.Websites(ctx)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	backups := m.Store.OnboardBackupIDs()
	for _, w := range sites {
		s := Site{ID: w.ID, Domain: w.PrimaryDomain, Alias: w.Alias, Backup: backups[w.ID], Skipped: slices.Contains(st.OnboardSkip, w.ID)}
		if w.Type == "stream" {
			s.State, s.Note = "skip", m.skipReason(w)
			r.Sites = append(r.Sites, s)
			continue
		}
		c, err := m.Panel.SiteConf(ctx, w.ID)
		if err != nil {
			s.State, s.Note = "error", err.Error()
		} else {
			s.State, s.Line = SiteState(c)
			if s.State == StateMain {
				if why := m.skipReason(w); why != "" {
					s.State, s.Note = "skip", why
				}
			}
		}
		r.Sites = append(r.Sites, s)
	}
	return r
}

// Plan 列出一键接入会做的修改（不执行）。only 为 0 时处理所有待接入的网站（不含撤销过的）；
// 不为 0 时只处理这一个网站（用于把撤销过的网站重新接入），nginx.conf 缺的内容照样补上。
func (m *Manager) Plan(ctx context.Context, only uint) ([]Change, Report) {
	r := m.Inspect(ctx)
	var out []Change
	if r.Error != "" {
		return out, r
	}
	if !r.Format || !r.Include {
		inc := ""
		if !r.Include {
			inc = r.IncludeLine
		}
		var lines []string
		if !r.Format {
			lines = append(lines, "在 http { 里加上 log_format liuli_json …（观测台的 JSON 日志格式）")
		}
		if inc != "" {
			lines = append(lines, "在 http { 里加上 "+inc+"（封禁规则）")
		}
		out = append(out, Change{Target: "nginx.conf", Lines: lines})
	}
	for _, s := range r.Sites {
		if s.State != StateMain || (only == 0 && s.Skipped) || (only != 0 && s.ID != only) {
			continue
		}
		{
			out = append(out, Change{Target: s.Domain, SiteID: s.ID,
				Lines: []string{s.Line + "  →  " + strings.Replace(s.Line, " main;", " "+FormatName+";", 1)}})
		}
	}
	return out, r
}

// Apply 执行一键接入：先改 nginx.conf（必须先有 liuli_json 格式，否则网站配置会过不了 nginx -t），再逐个切换网站。
func (m *Manager) Apply(ctx context.Context, only uint) ([]Change, error) {
	if m.Panel == nil {
		return nil, errors.New("还没配置 1Panel API")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	plan, r := m.Plan(ctx, only)
	if r.Error != "" {
		return nil, errors.New(r.Error)
	}
	formatReady := r.Format
	for i := range plan {
		c := &plan[i]
		if c.SiteID == 0 {
			if err := m.patchNginx(ctx, !r.Format, !r.Include); err != nil {
				c.Error = err.Error()
				continue
			}
			c.Done, formatReady = true, true
			continue
		}
		if !formatReady {
			c.Error = "nginx.conf 还没有 liuli_json 格式，先跳过"
			continue
		}
		if err := m.patchSite(ctx, c.SiteID, c.Target); err != nil {
			c.Error = err.Error()
			continue
		}
		c.Done = true
		// 单独重新接入的网站，重新纳入自动接入
		m.unskip(c.SiteID)
	}
	return plan, nil
}

func (m *Manager) patchNginx(ctx context.Context, addFormat, addInclude bool) error {
	conf, err := m.Panel.NginxConf(ctx)
	if err != nil {
		return err
	}
	inc := ""
	if addInclude {
		inc = m.includeLine()
	}
	out, added, err := PatchNginx(conf, addFormat && !HasFormat(conf), inc)
	if err != nil || len(added) == 0 {
		return err
	}
	if err := m.Panel.SaveNginxConf(ctx, out); err != nil {
		return fmt.Errorf("1Panel 保存 nginx.conf 失败（已自动恢复原内容）：%v", err)
	}
	log.Printf("一键接入：nginx.conf 已加入 %d 处配置", len(added))
	if m.Ban != nil && m.Ban.Enabled() {
		go m.Ban.Sync(context.Background(), true)
	}
	return nil
}

func (m *Manager) patchSite(ctx context.Context, id uint, domain string) error {
	conf, err := m.Panel.SiteConf(ctx, id)
	if err != nil {
		return err
	}
	out, n := PatchSite(conf)
	if n == 0 {
		return nil
	}
	if err := m.Store.SaveOnboardBackup(id, domain, conf); err != nil {
		return fmt.Errorf("保存备份失败，没有修改：%v", err)
	}
	if err := m.Panel.SaveSiteConf(ctx, id, out); err != nil {
		return fmt.Errorf("1Panel 保存失败（已自动恢复原内容）：%v", err)
	}
	log.Printf("接入：%s 的访问日志已切换到 liuli_json", domain)
	return nil
}

func (m *Manager) unskip(id uint) {
	st := m.Store.Settings()
	if slices.Contains(st.OnboardSkip, id) {
		st.OnboardSkip = slices.DeleteFunc(st.OnboardSkip, func(x uint) bool { return x == id })
		m.Store.SaveSettings(st)
	}
}

// Undo 把网站配置恢复成接入前的原始内容，并让自动接入以后跳过它。
func (m *Manager) Undo(ctx context.Context, id uint) error {
	if m.Panel == nil {
		return errors.New("还没配置 1Panel API")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.Store.OnboardBackup(id)
	if !ok {
		return errors.New("这个网站没有备份")
	}
	if err := m.Panel.SaveSiteConf(ctx, id, b.Content); err != nil {
		return fmt.Errorf("恢复失败：%v", err)
	}
	st := m.Store.Settings()
	if !slices.Contains(st.OnboardSkip, id) {
		st.OnboardSkip = append(st.OnboardSkip, id)
		m.Store.SaveSettings(st)
	}
	log.Printf("撤销接入：%s 的配置已恢复原样", b.Domain)
	return m.Store.DeleteOnboardBackup(id)
}

// Auto 自动接入：只在 nginx.conf 已有 liuli_json 格式时，把仍是 main 的网站切过来（不会自动改 nginx.conf）。
func (m *Manager) Auto(ctx context.Context) {
	st := m.Store.Settings()
	if !st.AutoOnboard || m.Panel == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	conf, err := m.Panel.NginxConf(ctx)
	if err != nil {
		m.setAuto("连接 1Panel 失败：" + err.Error())
		return
	}
	if !HasFormat(conf) {
		m.setAuto("nginx.conf 还没有 liuli_json 格式，先点一次「一键接入」")
		return
	}
	sites, err := m.Panel.Websites(ctx)
	if err != nil {
		m.setAuto("读取网站列表失败：" + err.Error())
		return
	}
	var done []string
	for _, w := range sites {
		if slices.Contains(st.OnboardSkip, w.ID) || m.skipReason(w) != "" {
			continue
		}
		c, err := m.Panel.SiteConf(ctx, w.ID)
		if err != nil {
			continue
		}
		if s, _ := SiteState(c); s != StateMain {
			continue
		}
		if err := m.patchSite(ctx, w.ID, w.PrimaryDomain); err != nil {
			log.Printf("自动接入 %s 失败：%v", w.PrimaryDomain, err)
			continue
		}
		done = append(done, w.PrimaryDomain)
	}
	if len(done) > 0 {
		m.setAuto("已自动接入 " + strings.Join(done, "、"))
	} else {
		m.setAuto("没有需要接入的新网站")
	}
}

func (m *Manager) setAuto(msg string) {
	m.amu.Lock()
	m.lastAuto, m.lastAutoMsg = time.Now().Unix(), msg
	m.amu.Unlock()
}

// Run 每 5 分钟执行一次自动接入（启动 1 分钟后第一次）。
func (m *Manager) Run(ctx context.Context) {
	if m.Panel == nil {
		return
	}
	t := time.NewTimer(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
			m.Auto(cctx)
			cancel()
			t.Reset(5 * time.Minute)
		}
	}
}
