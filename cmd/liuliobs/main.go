// 琉璃观测台：基于 OpenResty/Nginx 访问日志的网站监控报表服务。
//
// 同一个程序三种模式（LIULI_MODE）：
//   - all-in-one（默认）：单台服务器，采集 + 存储 + 网页同进程
//   - agent：只采集，推送给 Hub（第二版）
//   - hub：只接收推送 + 存储 + 网页（第二版）
package main

import (
	"context"
	"errors"
	"flag"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strings"
	"syscall"
	"time"

	"liuliobs/internal/api"
	"liuliobs/internal/ban"
	"liuliobs/internal/classify"
	"liuliobs/internal/ingest"
	"liuliobs/internal/model"
	"liuliobs/internal/onboard"
	"liuliobs/internal/panel"
	"liuliobs/internal/store"
	"liuliobs/web"
)

func env(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}

func main() {
	flag.Parse()
	log.SetFlags(log.LstdFlags)
	// 日常内存目标 < 100MB：给 Go 运行时一个软上限
	if os.Getenv("GOMEMLIMIT") == "" {
		debug.SetMemoryLimit(80 << 20)
	}

	mode := env("LIULI_MODE", "all-in-one")
	if mode != "all-in-one" {
		log.Fatalf("模式 %q 在第二版提供，第一版请使用 all-in-one", mode)
	}
	dataDir := env("LIULI_DATA", "data")
	listen := env("LIULI_LISTEN", "127.0.0.1:8090")
	serverID := env("LIULI_SERVER_ID", "local")
	tz := env("LIULI_TZ", env("TZ", "Asia/Shanghai"))
	loc, err := time.LoadLocation(tz)
	if err != nil {
		log.Printf("时区 %s 无效，使用 UTC+8", tz)
		loc = time.FixedZone("CST", 8*3600)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		log.Fatal(err)
	}

	st, err := store.Open(filepath.Join(dataDir, "liuli.db"), loc)
	if err != nil {
		log.Fatal(err)
	}
	defer st.Close()
	st.EnsureServer(serverID, env("LIULI_SERVER_NAME", serverID))
	if n, err := st.ClearBackground(classify.Background); err != nil {
		log.Printf("修正后台请求的访客口径失败: %v", err)
	} else if n > 0 {
		log.Printf("已修正 %d 条后台请求（sw.js、favicon 等）的访客口径", n)
	}

	// 首次启动：管理员账号
	user := env("LIULI_ADMIN_USER", "admin")
	created, gen, err := api.EnsureAdmin(st.DB, user, os.Getenv("LIULI_ADMIN_PASSWORD"), os.Getenv("LIULI_RESET_PASSWORD") == "1")
	if err != nil {
		log.Fatal(err)
	}
	switch {
	case created && gen != "":
		log.Printf("已创建管理员 %s，随机初始密码：%s （登录后请在「设置」里修改）", user, gen)
	case created:
		log.Printf("已创建管理员 %s（密码来自 LIULI_ADMIN_PASSWORD）", user)
	case os.Getenv("LIULI_RESET_PASSWORD") == "1":
		log.Printf("已按 LIULI_ADMIN_PASSWORD 重置管理员密码，记得把 LIULI_RESET_PASSWORD 改回 0")
	}

	// 首次启动：从环境变量导入站点（格式：域名=日志路径;域名=日志路径）
	if v := os.Getenv("LIULI_SITES"); v != "" {
		if existing, _ := st.Sites(serverID); len(existing) == 0 {
			for _, kv := range strings.Split(v, ";") {
				k, p, ok := strings.Cut(strings.TrimSpace(kv), "=")
				if ok && k != "" && p != "" {
					st.UpsertSite(model.Site{ServerID: serverID, Site: strings.TrimSpace(k), LogPath: strings.TrimSpace(p), Enabled: true})
					log.Printf("导入站点 %s ← %s", k, p)
				}
			}
		}
	}

	geoDir := env("LIULI_IP2REGION", filepath.Join(dataDir, "ip2region"))
	geo := classify.OpenGeo(geoDir)
	if !geo.Loaded() {
		log.Printf("没有找到 IP 库（%s），地区会显示为「未知」", geoDir)
	}
	settings := st.Settings()
	cls := classify.New(geo, settings.PV)

	mgr := ingest.New(st, cls, serverID)
	mgr.LogRoot = env("LIULI_LOG_ROOT", "/logs")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go mgr.Run(ctx)

	// 封禁 IP：规则目录（容器内）+ OpenResty 看到的规则路径 + 1Panel API（可选，没配时只写文件不重载）
	banMgr := &ban.Manager{Store: st, Dir: os.Getenv("LIULI_BAN_DIR"),
		IncludePath: env("LIULI_BAN_INCLUDE", "/www/liuli-ban/deny.conf"),
		Panel:       panel.New(os.Getenv("LIULI_1PANEL_URL"), os.Getenv("LIULI_1PANEL_API_KEY"), os.Getenv("LIULI_1PANEL_KEY_ID"))}
	os.Unsetenv("LIULI_1PANEL_API_KEY") // 读完就从环境变量里拿掉，免得被子进程继承
	if banMgr.Enabled() {
		go banMgr.Run(ctx)
		if banMgr.Panel == nil {
			log.Printf("封禁 IP：没有配置 1Panel API（LIULI_1PANEL_URL / LIULI_1PANEL_API_KEY），规则只写文件、不会自动重载")
		} else {
			log.Printf("封禁 IP：规则写入 %s，通过 1Panel API 重载 OpenResty", banMgr.Dir)
		}
	}

	ui, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		log.Fatal(err)
	}
	a := api.New(api.Config{Mode: mode, ServerID: serverID, CookieSecure: env("LIULI_COOKIE_SECURE", "1") != "0",
		LogRoot: env("LIULI_LOG_ROOT", "/logs")}, st, mgr, cls, ui)
	a.Ban = banMgr
	onb := &onboard.Manager{Store: st, Panel: banMgr.Panel, Ban: banMgr, LogRoot: env("LIULI_LOG_ROOT", "/logs")}
	a.Onboard = onb
	go onb.Run(ctx)
	srv := &http.Server{Addr: listen, Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}()
	log.Printf("琉璃观测台已启动：http://%s（模式 %s，服务器 %s，时区 %s）", listen, mode, serverID, loc)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	// 等采集循环收尾
	time.Sleep(300 * time.Millisecond)
	log.Printf("已退出")
}
