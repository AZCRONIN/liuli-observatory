// Package ingest 是 Agent 的核心：按站点跟读日志 → 解析 → 补全 → 入库，并定时汇总和清理。
// 单台模式下直接写本机数据库；多台模式下（第二版）改为把已结束的分钟推送给 Hub。
package ingest

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"liuliobs/internal/classify"
	"liuliobs/internal/model"
	"liuliobs/internal/parse"
	"liuliobs/internal/store"
	"liuliobs/internal/tail"
)

const chunkBytes = 1 << 20

// SiteStatus 是某个站点日志的采集状态，展示在设置页。
type SiteStatus struct {
	Site       string `json:"site"`
	Path       string `json:"path"`
	Exists     bool   `json:"exists"`
	Size       int64  `json:"size"`
	Offset     int64  `json:"offset"`
	Lines      int64  `json:"lines"`       // 本次启动以来入库的行数
	Skipped    int64  `json:"skipped"`     // 早于明细保留期而跳过的行数
	Errors     int64  `json:"errors"`      // 解析失败的行数
	LastError  string `json:"last_error"`  // 最近一次解析失败的原因和样例
	LastRecord int64  `json:"last_record"` // 最近一条日志的时间
	Rotations  int    `json:"rotations"`
}

type siteTail struct {
	site model.Site
	t    *tail.Tailer
	st   SiteStatus
}

// Manager 驱动所有站点的采集。所有写库操作都在 Run 的 goroutine 里串行执行。
type Manager struct {
	Store    *store.Store
	Class    *classify.Classifier
	ServerID string
	LogRoot  string // 日志挂载根目录，用于自动发现站点

	mu     sync.Mutex
	tails  map[string]*siteTail
	reload chan struct{}
	jobs   chan func()
}

// New 创建 Manager。
func New(s *store.Store, c *classify.Classifier, serverID string) *Manager {
	return &Manager{Store: s, Class: c, ServerID: serverID, tails: map[string]*siteTail{},
		reload: make(chan struct{}, 1), jobs: make(chan func(), 8)}
}

// Reload 通知重新读取站点配置（设置页修改后调用）。
func (m *Manager) Reload() {
	select {
	case m.reload <- struct{}{}:
	default:
	}
}

// Do 在采集 goroutine 里执行一个写库任务并等待完成，避免和入库抢写锁。
func (m *Manager) Do(ctx context.Context, fn func() error) error {
	done := make(chan error, 1)
	select {
	case m.jobs <- func() { done <- fn() }:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Status 返回各站点的采集状态。
func (m *Manager) Status() []SiteStatus {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SiteStatus, 0, len(m.tails))
	for _, x := range m.tails {
		s := x.st
		if fi, err := os.Stat(x.site.LogPath); err == nil {
			s.Exists, s.Size = true, fi.Size()
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Site < out[j].Site })
	return out
}

// Run 运行采集循环，直到 ctx 结束。
func (m *Manager) Run(ctx context.Context) {
	m.autoAdd()
	m.syncSites()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	lastRollup, lastClean, lastDiscover := time.Time{}, time.Now(), time.Now()
	for {
		select {
		case <-ctx.Done():
			m.mu.Lock()
			for _, x := range m.tails {
				x.t.Close()
			}
			m.mu.Unlock()
			return
		case <-m.reload:
			m.syncSites()
		case job := <-m.jobs:
			job()
		case <-tick.C:
			m.pollAll(ctx)
			if time.Since(lastRollup) >= 15*time.Second {
				if err := m.Store.Rollup(time.Now()); err != nil {
					log.Printf("汇总失败: %v", err)
				}
				lastRollup = time.Now()
			}
			if time.Since(lastDiscover) >= 30*time.Second {
				if m.autoAdd() {
					m.syncSites()
				}
				lastDiscover = time.Now()
			}
			if time.Since(lastClean) >= time.Hour {
				if err := m.Store.Cleanup(time.Now(), m.Store.Settings()); err != nil {
					log.Printf("清理过期数据失败: %v", err)
				}
				lastClean = time.Now()
			}
		}
	}
}

// Discovered 是在日志目录里发现的一个访问日志。
type Discovered struct {
	Site string `json:"site"`
	Path string `json:"path"`
}

// Discover 在日志挂载目录下找 access.log，按 1Panel 的目录结构（<域名>/log/access.log）猜测站点名。
func Discover(root string) []Discovered {
	out := []Discovered{}
	if root == "" {
		return out
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return out
	}
	filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if rel, _ := filepath.Rel(root, p); strings.Count(rel, string(filepath.Separator)) >= 4 {
				return filepath.SkipDir
			}
			return nil
		}
		if d.Name() != "access.log" {
			return nil
		}
		dir := filepath.Dir(p)
		site := filepath.Base(dir)
		if site == "log" || site == "logs" {
			site = filepath.Base(filepath.Dir(dir))
		}
		out = append(out, Discovered{Site: strings.ToLower(site), Path: filepath.ToSlash(p)})
		return nil
	})
	return out
}

// autoAdd 把新发现的日志自动添加为站点，返回是否有新增。
// 跳过：已配置的路径或站点名、手动删除过的路径、观测台自己的域名。
func (m *Manager) autoAdd() bool {
	st := m.Store.Settings()
	if !st.AutoAddSites || m.LogRoot == "" {
		return false
	}
	sites, err := m.Store.Sites("")
	if err != nil {
		return false
	}
	names, paths := map[string]bool{}, map[string]bool{}
	for _, s := range sites {
		names[s.Site] = true
		paths[s.LogPath] = true
	}
	added := false
	for _, d := range Discover(m.LogRoot) {
		if names[d.Site] || paths[d.Path] || slices.Contains(st.IgnoredLogs, d.Path) || slices.Contains(st.SelfHosts, d.Site) {
			continue
		}
		if err := m.Store.UpsertSite(model.Site{ServerID: m.ServerID, Site: d.Site, LogPath: d.Path, Enabled: true}); err != nil {
			log.Printf("自动添加站点 %s 失败: %v", d.Site, err)
			continue
		}
		names[d.Site] = true
		log.Printf("自动发现并添加站点 %s ← %s", d.Site, d.Path)
		added = true
	}
	return added
}

// syncSites 按数据库里的站点配置增删 tailer。
func (m *Manager) syncSites() {
	sites, err := m.Store.Sites(m.ServerID)
	if err != nil {
		log.Printf("读取站点配置失败: %v", err)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	want := map[string]model.Site{}
	for _, s := range sites {
		if s.Enabled {
			want[s.Site] = s
		}
	}
	for k, x := range m.tails {
		if w, ok := want[k]; !ok || w.LogPath != x.site.LogPath {
			x.t.Close()
			delete(m.tails, k)
		}
	}
	for k, s := range want {
		if _, ok := m.tails[k]; ok {
			continue
		}
		saved := m.Store.TailState(m.ServerID, s.LogPath)
		m.tails[k] = &siteTail{site: s, t: tail.New(s.LogPath, saved), st: SiteStatus{Site: s.Site, Path: s.LogPath}}
		log.Printf("开始采集 %s ← %s", s.Site, s.LogPath)
	}
}

func (m *Manager) pollAll(ctx context.Context) {
	m.mu.Lock()
	tails := make([]*siteTail, 0, len(m.tails))
	for _, x := range m.tails {
		tails = append(tails, x)
	}
	m.mu.Unlock()
	st := m.Store.Settings()
	for _, x := range tails {
		// 补读历史日志时每个站点每轮最多占 2 秒，让其他站点和汇总有机会运行
		deadline := time.Now().Add(2 * time.Second)
		for time.Now().Before(deadline) && ctx.Err() == nil {
			n, err := m.pollOne(x, st)
			if err != nil {
				log.Printf("采集 %s 失败: %v", x.site.Site, err)
				break
			}
			if n == 0 {
				break
			}
		}
	}
}

// pollOne 读一块日志并入库，返回读到的行数。
func (m *Manager) pollOne(x *siteTail, st model.Settings) (int, error) {
	c, err := x.t.Poll(chunkBytes)
	if err != nil {
		return 0, err
	}
	if len(c.Lines) == 0 {
		// 进度可能因为切割而变化，也需要保存
		if saved := m.Store.TailState(m.ServerID, x.site.LogPath); saved == nil || saved.Ino != c.State.Ino || saved.Offset != c.State.Offset {
			if c.State.Offset > 0 || saved != nil {
				if err := m.Store.InsertBatch(m.ServerID, nil, &c.State); err != nil {
					return 0, err
				}
			}
		}
		x.t.Commit(c)
		m.mu.Lock()
		x.st.Offset, x.st.Rotations = x.t.State().Offset, x.t.Rotated
		m.mu.Unlock()
		return 0, nil
	}
	cut := m.Store.DayStart(time.Now().AddDate(0, 0, -st.DetailRetentionDays+1))
	recs := make([]model.Record, 0, len(c.Lines))
	var errs, skipped int64
	var lastErr string
	for _, l := range c.Lines {
		r, err := parse.Line(l, st.IPSource)
		if err != nil {
			if !errors.Is(err, parse.ErrSkip) {
				errs++
				sample := string(l)
				if len(sample) > 160 {
					sample = sample[:160] + "…"
				}
				lastErr = err.Error() + "：" + sample
			}
			continue
		}
		if r.TS < cut {
			skipped++
			continue
		}
		r.ServerID = m.ServerID
		r.Site = x.site.Site
		m.Class.Enrich(&r)
		recs = append(recs, r)
	}
	if err := m.Store.InsertBatch(m.ServerID, recs, &c.State); err != nil {
		return 0, err
	}
	x.t.Commit(c)
	m.mu.Lock()
	x.st.Offset, x.st.Rotations = x.t.State().Offset, x.t.Rotated
	x.st.Lines += int64(len(recs))
	x.st.Errors += errs
	x.st.Skipped += skipped
	if lastErr != "" {
		x.st.LastError = lastErr
	}
	if n := len(recs); n > 0 && recs[n-1].TS > x.st.LastRecord {
		x.st.LastRecord = recs[n-1].TS
	}
	m.mu.Unlock()
	return len(c.Lines), nil
}
