// Package api 提供查询接口、登录、设置，以及嵌入的网页仪表盘。
package api

import (
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"liuliobs/internal/classify"
	"liuliobs/internal/ingest"
	"liuliobs/internal/model"
	"liuliobs/internal/store"
)

// Config 是 API 需要的运行参数。
type Config struct {
	Mode         string
	ServerID     string
	CookieSecure bool
	LogRoot      string // 日志挂载根目录，用于「发现日志文件」
}

// API 是 HTTP 处理器的集合。
type API struct {
	Cfg   Config
	Store *store.Store
	Mgr   *ingest.Manager
	Class *classify.Classifier
	UI    fs.FS
	lim   *limiter
}

// New 创建 API。
func New(cfg Config, s *store.Store, m *ingest.Manager, c *classify.Classifier, ui fs.FS) *API {
	return &API{Cfg: cfg, Store: s, Mgr: m, Class: c, UI: ui, lim: newLimiter()}
}

// Handler 返回带安全响应头的总路由。
func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	auth := func(h http.HandlerFunc) http.Handler { return a.requireAuth(h) }

	mux.HandleFunc("POST /api/login", a.login)
	mux.HandleFunc("POST /api/logout", a.logout)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, map[string]any{"ok": true}) })

	mux.Handle("GET /api/me", auth(a.me))
	mux.Handle("POST /api/password", auth(a.changePassword))
	mux.Handle("GET /api/sites", auth(a.sites))
	mux.Handle("GET /api/overview", auth(a.overview))
	mux.Handle("GET /api/trend", auth(a.trend))
	mux.Handle("GET /api/active", auth(a.active))
	mux.Handle("GET /api/realtime", auth(a.realtime))
	mux.Handle("GET /api/dim", auth(a.dim))
	mux.Handle("GET /api/dimseries", auth(a.dimSeries))
	mux.Handle("GET /api/logs", auth(a.logs))
	mux.Handle("GET /api/settings", auth(a.getSettings))
	mux.Handle("PUT /api/settings", auth(a.putSettings))
	mux.Handle("POST /api/sites", auth(a.upsertSite))
	mux.Handle("DELETE /api/sites", auth(a.deleteSite))
	mux.Handle("POST /api/reclassify", auth(a.reclassify))

	// 第二版：封禁 IP。接口先占位，界面上的按钮会提示「第二版提供」
	mux.Handle("GET /api/bans", auth(a.notYet))
	mux.Handle("POST /api/bans", auth(a.notYet))
	mux.Handle("DELETE /api/bans", auth(a.notYet))
	// 多台模式：Agent → Hub 推送（Bearer Token 鉴权，按 (server_id, site, minute) 幂等），第二版实现
	mux.HandleFunc("POST /api/push", a.notYet)

	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeErr(w, 404, "接口不存在") }))
	mux.Handle("/", a.static())
	return securityHeaders(mux)
}

func (a *API) notYet(w http.ResponseWriter, r *http.Request) {
	writeErr(w, http.StatusNotImplemented, "这个功能在第二版提供")
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
			"img-src 'self' data: blob:; font-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; "+
			"form-action 'self'; frame-ancestors 'none'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Permissions-Policy", "camera=(), microphone=(), geolocation=(), payment=()")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		h.Set("Cross-Origin-Resource-Policy", "same-origin")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// static 提供嵌入的前端，未知路径回落到 index.html。
func (a *API) static() http.Handler {
	fsrv := http.FileServer(http.FS(a.UI))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "" {
			p = "index.html"
		}
		if f, err := a.UI.Open(p); err == nil {
			f.Close()
			if strings.HasPrefix(p, "assets/") || strings.HasPrefix(p, "fonts/") {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "no-cache")
			}
			fsrv.ServeHTTP(w, r)
			return
		}
		b, err := fs.ReadFile(a.UI, "index.html")
		if err != nil {
			http.Error(w, "前端未打包：请先在 web 目录执行 npm run build", 500)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Write(b)
	})
}

// ---- 工具函数 ----

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, 400, "请求格式不对")
		return false
	}
	return true
}

func itoa(n int) string { return strconv.Itoa(n) }

func qint(r *http.Request, k string, def int) int {
	if v, err := strconv.Atoi(r.URL.Query().Get(k)); err == nil {
		return v
	}
	return def
}

// ---- 时间范围 ----

type span struct {
	F        store.Filter
	Cmp      store.Filter
	CmpLabel string
	Label    string
	Today    bool
}

// parseRange 解析 range=today|yesterday|7d|30d|custom（custom 带 from、to，格式 2006-01-02，含两端）。
func (a *API) parseRange(r *http.Request) (span, error) {
	q := r.URL.Query()
	site := q.Get("site")
	if site == "" {
		return span{}, errors.New("缺少站点参数")
	}
	s := a.Store
	now := time.Now().In(s.Loc)
	today := s.DayStart(now)
	day := func(ts int64, delta int) int64 { return s.DayStart(time.Unix(ts, 0).In(s.Loc).AddDate(0, 0, delta)) }
	f := store.Filter{ServerID: q.Get("server"), Site: site}
	var sp span
	switch rg := q.Get("range"); rg {
	case "", "today":
		f.From, f.To = today, now.Unix()+1
		sp.Today, sp.Label = true, "今日"
		sp.Cmp = f
		sp.Cmp.From, sp.Cmp.To = day(today, -1), now.AddDate(0, 0, -1).Unix()+1
		sp.CmpLabel = "昨日同时段"
	case "yesterday":
		f.From, f.To = day(today, -1), today
		sp.Label = "昨日"
		sp.Cmp = f
		sp.Cmp.From, sp.Cmp.To = day(today, -2), day(today, -1)
		sp.CmpLabel = "前一日"
	case "7d", "30d":
		n := 7
		if rg == "30d" {
			n = 30
		}
		f.From, f.To = day(today, -(n-1)), s.NextDay(today)
		sp.Label = "近 " + itoa(n) + " 天"
		sp.Cmp = f
		sp.Cmp.From, sp.Cmp.To = day(today, -(2*n-1)), f.From
		sp.CmpLabel = "前 " + itoa(n) + " 天"
	case "custom":
		from, err1 := time.ParseInLocation("2006-01-02", q.Get("from"), s.Loc)
		to, err2 := time.ParseInLocation("2006-01-02", q.Get("to"), s.Loc)
		if err1 != nil || err2 != nil || to.Before(from) {
			return span{}, errors.New("自定义时间格式不对")
		}
		f.From, f.To = s.DayStart(from), s.NextDay(s.DayStart(to))
		if f.To-f.From > 400*86400 {
			return span{}, errors.New("时间跨度太长")
		}
		n := int((f.To - f.From + 3600) / 86400)
		sp.Label = from.Format("01-02") + " ~ " + to.Format("01-02")
		sp.Cmp = f
		sp.Cmp.From, sp.Cmp.To = day(f.From, -n), f.From
		sp.CmpLabel = "前 " + itoa(n) + " 天"
	default:
		return span{}, errors.New("未知的时间范围")
	}
	sp.F = f
	return sp, nil
}

// totals 在明细保留期内的短时段用明细精确计算，否则用天聚合累加。
func (a *API) totals(f store.Filter) (store.Totals, error) {
	st := a.Store.Settings()
	cut := a.Store.DayStart(time.Now().AddDate(0, 0, -st.DetailRetentionDays+1))
	if f.From >= cut && f.To-f.From <= 2*86400+3600 {
		return a.Store.TotalsLive(f)
	}
	return a.Store.TotalsDays(f)
}

// ---- 概览 ----

func (a *API) overview(w http.ResponseWriter, r *http.Request) {
	sp, err := a.parseRange(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	cur, err := a.totals(sp.F)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	cmp, err := a.totals(sp.Cmp)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"label": sp.Label, "cmp_label": sp.CmpLabel, "cur": cur, "cmp": cmp,
		"from": sp.F.From, "to": sp.F.To, "multi_day": sp.F.To-sp.F.From > 86400+3600})
}

func (a *API) trend(w http.ResponseWriter, r *http.Request) {
	sp, err := a.parseRange(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	s := a.Store
	f := sp.F
	if sp.Today {
		f.To = s.NextDay(f.From) // 今天的横轴画满 24 小时
	}
	span := f.To - f.From
	gran := int64(3600)
	switch r.URL.Query().Get("gran") {
	case "minute":
		gran = 60
	case "day":
		gran = 86400
	case "hour":
	default:
		if span > 8*86400 {
			gran = 86400
		}
	}
	if gran == 60 && span > 2*86400 {
		gran = 3600
	}
	if gran == 3600 && span > 62*86400 {
		gran = 86400
	}
	shift := func(days int) store.Filter {
		g := f
		g.From = s.DayStart(time.Unix(f.From, 0).In(s.Loc).AddDate(0, 0, -days))
		g.To = g.From + (f.To - f.From)
		return g
	}
	cur, err := s.Trend(f, gran)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	out := map[string]any{"gran": gran, "now": time.Now().Unix(), "cur": cur}
	for _, c := range strings.Split(r.URL.Query().Get("compare"), ",") {
		var days int
		switch c {
		case "day":
			days = 1
		case "week":
			days = 7
		default:
			continue
		}
		pts, err := s.Trend(shift(days), gran)
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		out["prev_"+c] = pts
	}
	writeJSON(w, out)
}

func (a *API) active(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	n, bars, err := a.Store.Active(q.Get("server"), q.Get("site"), time.Now())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"active": n, "bars": bars})
}

func (a *API) realtime(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	secs := int64(qint(r, "seconds", 60))
	if secs < 10 || secs > 600 {
		secs = 60
	}
	// 最近 2 秒的日志可能还没读到，不画，避免末尾总是掉下去
	to := time.Now().Unix() - 1
	pts, err := a.Store.Realtime(q.Get("server"), q.Get("site"), to-secs, to)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"points": pts})
}

// ---- 网站列表 ----

func (a *API) sites(w http.ResponseWriter, r *http.Request) {
	sites, err := a.Store.Sites("")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	s := a.Store
	now := time.Now()
	today := s.DayStart(now)
	yday := s.DayStart(now.AddDate(0, 0, -1))
	type row struct {
		model.Site
		Today     store.Totals `json:"today"`
		Yesterday store.Totals `json:"yesterday"`
		Active    int64        `json:"active"`
	}
	out := []row{}
	seen := map[string]bool{}
	for _, x := range sites {
		if seen[x.Site] {
			continue // 多台服务器上的同名站点合并显示
		}
		seen[x.Site] = true
		var rw row
		rw.Site = x
		rw.Today, _ = s.TotalsLive(store.Filter{Site: x.Site, From: today, To: now.Unix() + 1})
		rw.Yesterday, _ = s.TotalsDays(store.Filter{Site: x.Site, From: yday, To: today})
		rw.Active, _, _ = s.Active("", x.Site, now)
		out = append(out, rw)
	}
	writeJSON(w, map[string]any{"sites": out, "server_id": a.Cfg.ServerID})
}

// ---- 维度 ----

func (a *API) dim(w http.ResponseWriter, r *http.Request) {
	sp, err := a.parseRange(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	q := r.URL.Query()
	d := q.Get("dim")
	limit := qint(r, "limit", 50)
	if limit <= 0 || limit > 1000 {
		limit = 50
	}
	sortBy := q.Get("sort")
	if d == "search" {
		// 搜索引擎来源：由来源域名归类
		rows, err := a.Store.Dim(sp.F, "ref", 0, "pv", time.Now())
		if err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		agg := map[string]*store.DimRow{}
		for _, x := range rows {
			e := classify.SearchEngine(x.Key)
			if e == "" {
				continue
			}
			m, ok := agg[e]
			if !ok {
				m = &store.DimRow{Key: e}
				agg[e] = m
			}
			m.Req += x.Req
			m.PV += x.PV
			m.UV += x.UV
			m.Bytes += x.Bytes
			if x.Last > m.Last {
				m.Last = x.Last
			}
		}
		out := []store.DimRow{}
		for _, m := range agg {
			out = append(out, *m)
		}
		sort.Slice(out, func(i, j int) bool { return out[i].Req > out[j].Req })
		writeJSON(w, map[string]any{"rows": out})
		return
	}
	if !store.ValidDim(d) {
		writeErr(w, 400, "未知的维度")
		return
	}
	fetch := limit
	spider := q.Get("spider")
	if d == "spider_uri" {
		fetch = 0 // 先全部取出再按蜘蛛过滤、按路径合并
	}
	rows, err := a.Store.Dim(sp.F, d, fetch, sortBy, time.Now())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	switch d {
	case "spider":
		for i := range rows {
			rows[i].Extra = classify.SpiderCategory(rows[i].Key)
		}
	case "spider_uri":
		agg := map[string]*store.DimRow{}
		var order []string
		for _, x := range rows {
			name, path, _ := strings.Cut(x.Key, "\t")
			if spider != "" && name != spider {
				continue
			}
			m, ok := agg[path]
			if !ok {
				m = &store.DimRow{Key: path, Extra: name}
				agg[path] = m
				order = append(order, path)
			}
			m.Req += x.Req
			m.Bytes += x.Bytes
			if x.Last > m.Last {
				m.Last = x.Last
			}
		}
		rows = rows[:0]
		for _, p := range order {
			rows = append(rows, *agg[p])
		}
		sort.SliceStable(rows, func(i, j int) bool { return rows[i].Req > rows[j].Req })
		if len(rows) > limit {
			rows = rows[:limit]
		}
	}
	if rows == nil {
		rows = []store.DimRow{}
	}
	writeJSON(w, map[string]any{"rows": rows})
}

func (a *API) dimSeries(w http.ResponseWriter, r *http.Request) {
	sp, err := a.parseRange(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	d := r.URL.Query().Get("dim")
	if d != "spider" && d != "status" {
		writeErr(w, 400, "只支持蜘蛛和状态码")
		return
	}
	f := sp.F
	if sp.Today {
		f.To = a.Store.NextDay(f.From)
	}
	gran := int64(3600)
	st := a.Store.Settings()
	cut := a.Store.DayStart(time.Now().AddDate(0, 0, -st.DetailRetentionDays+1))
	if f.To-f.From > 2*86400+3600 || f.From < cut || r.URL.Query().Get("gran") == "day" {
		gran = 86400
	}
	b, series, err := a.Store.DimSeries(f, d, gran, time.Now())
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"gran": gran, "buckets": b, "series": series, "now": time.Now().Unix()})
}

// ---- 明细 ----

func (a *API) logs(w http.ResponseWriter, r *http.Request) {
	sp, err := a.parseRange(r)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	q := r.URL.Query()
	lf := store.LogFilter{Filter: sp.F, Status: q.Get("status"), IP: strings.TrimSpace(q.Get("ip")),
		Path: strings.TrimSpace(q.Get("path")), Spider: q.Get("spider"), Sort: q.Get("sort")}
	if v, err := strconv.ParseFloat(q.Get("min_rt"), 64); err == nil && v > 0 {
		lf.MinRT = v / 1000
	}
	if q.Get("kind") == "slow" {
		if lf.MinRT == 0 {
			lf.MinRT = float64(a.Store.Settings().SlowThresholdMs) / 1000
		}
		if lf.Sort == "" {
			lf.Sort = "rt"
		}
	}
	size := qint(r, "size", 50)
	if size <= 0 || size > 200 {
		size = 50
	}
	page := qint(r, "page", 1)
	if page < 1 {
		page = 1
	}
	lf.Limit, lf.Offset = size, (page-1)*size
	rows, total, err := a.Store.Logs(lf)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if rows == nil {
		rows = []store.LogRow{}
	}
	writeJSON(w, map[string]any{"rows": rows, "total": total, "page": page, "size": size})
}

// ---- 设置 ----

type discovered struct {
	Site string `json:"site"`
	Path string `json:"path"`
}

// discover 在日志挂载目录下找 access.log，按 1Panel 的目录结构猜测站点名。
func (a *API) discover() []discovered {
	out := []discovered{}
	if a.Cfg.LogRoot == "" {
		return out
	}
	root, err := filepath.Abs(a.Cfg.LogRoot)
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
		// .../<域名>/log/access.log
		dir := filepath.Dir(p)
		site := filepath.Base(dir)
		if site == "log" || site == "logs" {
			site = filepath.Base(filepath.Dir(dir))
		}
		out = append(out, discovered{Site: site, Path: filepath.ToSlash(p)})
		return nil
	})
	return out
}

func (a *API) getSettings(w http.ResponseWriter, r *http.Request) {
	sites, err := a.Store.Sites("")
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if sites == nil {
		sites = []model.Site{}
	}
	writeJSON(w, map[string]any{
		"settings":  a.Store.Settings(),
		"sites":     sites,
		"status":    a.Mgr.Status(),
		"discover":  a.discover(),
		"server_id": a.Cfg.ServerID,
		"geo":       a.Class.Geo.Loaded(),
		"log_root":  a.Cfg.LogRoot,
	})
}

func (a *API) putSettings(w http.ResponseWriter, r *http.Request) {
	var st model.Settings
	if !readJSON(w, r, &st) {
		return
	}
	if st.DetailRetentionDays < 1 || st.DetailRetentionDays > 90 {
		writeErr(w, 400, "明细保留天数需在 1～90 之间")
		return
	}
	if st.AggRetentionDays < 30 || st.AggRetentionDays > 3650 {
		writeErr(w, 400, "汇总保留天数需在 30～3650 之间")
		return
	}
	if st.SlowThresholdMs < 10 {
		writeErr(w, 400, "慢请求阈值至少 10 毫秒")
		return
	}
	switch st.IPSource {
	case "remote_addr", "cf", "x_real_ip", "xff":
	default:
		writeErr(w, 400, "未知的真实 IP 取法")
		return
	}
	old := a.Store.Settings()
	if err := a.Store.SaveSettings(st); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.Class.SetRules(st.PV)
	b1, _ := json.Marshal(old.PV)
	b2, _ := json.Marshal(st.PV)
	writeJSON(w, map[string]any{"ok": true, "pv_changed": string(b1) != string(b2)})
}

func (a *API) reclassify(w http.ResponseWriter, r *http.Request) {
	var n int64
	err := a.Mgr.Do(r.Context(), func() error {
		var err error
		n, err = a.Store.Reclassify(func(method string, status int, path, spider string) bool {
			return a.Class.IsPV(&model.Record{Method: method, Status: status, Path: path, Spider: spider})
		})
		return err
	})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true, "changed": n})
}

func validSiteName(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for _, c := range s {
		if !(c == '.' || c == '-' || c == '_' || c == ':' || (c >= '0' && c <= '9') || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')) {
			return false
		}
	}
	return true
}

func (a *API) upsertSite(w http.ResponseWriter, r *http.Request) {
	var x model.Site
	if !readJSON(w, r, &x) {
		return
	}
	x.Site = strings.ToLower(strings.TrimSpace(x.Site))
	x.LogPath = strings.TrimSpace(x.LogPath)
	if !validSiteName(x.Site) {
		writeErr(w, 400, "站点名只能是域名（字母、数字、点、横线）")
		return
	}
	if x.LogPath == "" || !filepath.IsAbs(x.LogPath) && !strings.HasPrefix(x.LogPath, "/") {
		writeErr(w, 400, "日志路径要写容器里的绝对路径，例如 /logs/sosoquark.com/log/access.log")
		return
	}
	if x.ServerID == "" {
		x.ServerID = a.Cfg.ServerID
	}
	if err := a.Store.UpsertSite(x); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.Mgr.Reload()
	_, statErr := os.Stat(x.LogPath)
	writeJSON(w, map[string]any{"ok": true, "exists": statErr == nil})
}

func (a *API) deleteSite(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	srv := q.Get("server")
	if srv == "" {
		srv = a.Cfg.ServerID
	}
	if err := a.Store.DeleteSite(srv, q.Get("site")); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	a.Mgr.Reload()
	writeJSON(w, map[string]any{"ok": true})
}
