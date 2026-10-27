package api

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"liuliobs/internal/onboard"
)

func (a *API) onboardStatus(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	writeJSON(w, a.Onboard.Inspect(ctx))
}

// siteParam 读取可选的 {"site_id": N}；没有请求体时为 0（处理全部）。
func siteParam(r *http.Request) uint {
	var in struct {
		SiteID uint `json:"site_id"`
	}
	json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<16)).Decode(&in)
	return in.SiteID
}

func (a *API) onboardPlan(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	plan, rep := a.Onboard.Plan(ctx, siteParam(r))
	if plan == nil {
		plan = []onboardChange{}
	}
	writeJSON(w, map[string]any{"changes": plan, "report": rep})
}

func (a *API) onboardApply(w http.ResponseWriter, r *http.Request) {
	// 每处修改都要等 1Panel 执行 nginx -t 和重载，网站多时比较慢，不跟随浏览器断开而中止
	only := siteParam(r)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res, err := a.Onboard.Apply(ctx, only)
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	if res == nil {
		res = []onboardChange{}
	}
	writeJSON(w, map[string]any{"changes": res})
}

func (a *API) onboardUndo(w http.ResponseWriter, r *http.Request) {
	var in struct {
		SiteID uint `json:"site_id"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	if err := a.Onboard.Undo(ctx, in.SiteID); err != nil {
		writeErr(w, 400, err.Error())
		return
	}
	writeJSON(w, map[string]any{"ok": true})
}

type onboardChange = onboard.Change
