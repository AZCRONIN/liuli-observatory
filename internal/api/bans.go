package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"liuliobs/internal/ban"
	"liuliobs/internal/model"
)

type banRow struct {
	model.Ban
	Region string `json:"region"`
	Hits   int64  `json:"hits"` // 封禁后被拦截（403）的次数，只统计明细保留期内
}

func (a *API) banList(w http.ResponseWriter, r *http.Request) {
	if !a.Ban.Enabled() {
		writeJSON(w, map[string]any{"enabled": false})
		return
	}
	now := time.Now()
	bans, err := a.Store.Bans(r.URL.Query().Get("all") != "1", now)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if r.URL.Query().Get("all") != "1" {
		// 默认：生效中的全部 + 最近 50 条已解封的
		all, _ := a.Store.Bans(false, now)
		n := 0
		for _, b := range all {
			if !b.Active && n < 50 {
				bans = append(bans, b)
				n++
			}
		}
	}
	rows := make([]banRow, 0, len(bans))
	for _, b := range bans {
		row := banRow{Ban: b}
		if !strings.Contains(b.IP, "/") {
			c, p, ci, _ := a.Class.Geo.Lookup(b.IP)
			row.Region = strings.TrimSpace(strings.Join([]string{c, p, ci}, " "))
			if b.Active || b.Removed > 0 {
				row.Hits = a.Store.BanHits(b.IP, b.Created, b.Removed)
			} else {
				row.Hits = -1 // 旧版本留下的记录没有解封时间，无法统计
			}
		}
		rows = append(rows, row)
	}
	writeJSON(w, map[string]any{
		"enabled":   true,
		"bans":      rows,
		"status":    a.Ban.Status(),
		"whitelist": a.Store.Settings().BanWhitelist,
		"self_ip":   clientIP(r),
	})
}

func (a *API) banAdd(w http.ResponseWriter, r *http.Request) {
	if !a.Ban.Enabled() {
		writeErr(w, 400, "没有配置封禁规则目录，见部署说明「封禁 IP」一节")
		return
	}
	var in struct {
		IP      string `json:"ip"`
		Minutes int    `json:"minutes"` // 0 表示永久
		Reason  string `json:"reason"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	st := a.Store.Settings()
	if st.IPSource != "remote_addr" {
		// 套了 CDN 时 OpenResty 看到的 $remote_addr 是 CDN 节点，按它封会误伤大量访客
		writeErr(w, 400, "当前「真实访客 IP」不是直接连接的 IP（可能套了 CDN），按 IP 封禁会误伤 CDN 节点后面的所有访客，已禁止")
		return
	}
	target, err := ban.Validate(in.IP, clientIP(r), st.BanWhitelist)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if in.Minutes < 0 || in.Minutes > 3650*24*60 {
		writeErr(w, 400, "封禁时长不对")
		return
	}
	now := time.Now()
	b := model.Ban{ServerID: a.Cfg.ServerID, IP: target, Reason: truncRunes(strings.TrimSpace(in.Reason), 100),
		Source: "manual", Created: now.Unix()}
	if in.Minutes > 0 {
		b.Expires = now.Add(time.Duration(in.Minutes) * time.Minute).Unix()
	}
	id, err := a.Store.AddBan(b)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	syncErr := a.Ban.Sync(r.Context(), false)
	writeJSON(w, map[string]any{"ok": true, "id": id, "ip": target, "applied": syncErr == nil && a.Ban.Status().IncludeOK, "sync_error": errText(syncErr)})
}

func (a *API) banRemove(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.URL.Query().Get("id"), 10, 64)
	if err != nil {
		writeErr(w, 400, "缺少 id")
		return
	}
	if err := a.Store.Unban(id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	syncErr := a.Ban.Sync(r.Context(), false)
	writeJSON(w, map[string]any{"ok": true, "sync_error": errText(syncErr)})
}

func (a *API) banSync(w http.ResponseWriter, r *http.Request) {
	if !a.Ban.Enabled() {
		writeErr(w, 400, "没有配置封禁规则目录")
		return
	}
	err := a.Ban.Sync(r.Context(), true)
	writeJSON(w, map[string]any{"ok": err == nil, "status": a.Ban.Status(), "sync_error": errText(err)})
}

func (a *API) banWhitelist(w http.ResponseWriter, r *http.Request) {
	var in struct {
		List []string `json:"list"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	var clean []string
	for _, x := range in.List {
		x = strings.TrimSpace(x)
		if x == "" {
			continue
		}
		if _, err := ban.Validate(x, "", nil); err != nil && !isReservedErr(err) {
			writeErr(w, 400, "白名单里的 "+x+" 不是合法的 IP 或网段")
			return
		}
		clean = append(clean, x)
	}
	st := a.Store.Settings()
	st.BanWhitelist = clean
	if err := a.Store.SaveSettings(st); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

// 白名单允许写内网地址和大网段，校验时只要求格式正确
func isReservedErr(err error) bool {
	m := err.Error()
	return strings.Contains(m, "内网") || strings.Contains(m, "太大")
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func truncRunes(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		return string(r[:n])
	}
	return s
}
