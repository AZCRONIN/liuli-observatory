// Package store 是 SQLite 存储：明细表、小时/天聚合、维度日表，以及设置、账号、会话。
// 所有数据表从第一天起就带 server_id 和 site，以后加机器不需要迁移数据。
package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"liuliobs/internal/model"
	"liuliobs/internal/tail"
)

// Store 包装数据库连接。
type Store struct {
	DB  *sql.DB
	Loc *time.Location
}

const schema = `
CREATE TABLE IF NOT EXISTS servers(
	id TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', token_hash TEXT NOT NULL DEFAULT '', created INTEGER NOT NULL DEFAULT 0);

CREATE TABLE IF NOT EXISTS sites(
	server_id TEXT NOT NULL, site TEXT NOT NULL, log_path TEXT NOT NULL, enabled INTEGER NOT NULL DEFAULT 1,
	created INTEGER NOT NULL DEFAULT 0, PRIMARY KEY(server_id, site));

CREATE TABLE IF NOT EXISTS tail_state(
	server_id TEXT NOT NULL, path TEXT NOT NULL, ino INTEGER NOT NULL, offset INTEGER NOT NULL,
	fp TEXT NOT NULL, fplen INTEGER NOT NULL, updated INTEGER NOT NULL, PRIMARY KEY(server_id, path));

-- 明细：保留 N 天（默认 7），用于明细查询、今日统计和维度日表的计算
CREATE TABLE IF NOT EXISTS req(
	id INTEGER PRIMARY KEY,
	server_id TEXT NOT NULL, site TEXT NOT NULL, ts INTEGER NOT NULL,
	ip TEXT NOT NULL, country TEXT NOT NULL, province TEXT NOT NULL, city TEXT NOT NULL, isp TEXT NOT NULL,
	method TEXT NOT NULL, uri TEXT NOT NULL, path TEXT NOT NULL, proto TEXT NOT NULL,
	status INTEGER NOT NULL, bytes INTEGER NOT NULL, req_len INTEGER NOT NULL, rt REAL NOT NULL, urt REAL NOT NULL,
	referer TEXT NOT NULL, ref_host TEXT NOT NULL, ua TEXT NOT NULL,
	browser TEXT NOT NULL, os TEXT NOT NULL, device TEXT NOT NULL, spider TEXT NOT NULL,
	is_pv INTEGER NOT NULL, visitor INTEGER NOT NULL);
CREATE INDEX IF NOT EXISTS req_site_ts ON req(site, ts);
CREATE INDEX IF NOT EXISTS req_site_status_ts ON req(site, status, ts);
CREATE INDEX IF NOT EXISTS req_ts ON req(ts);

-- 小时 / 天聚合：保留 1 年。uv、ip 是该时段内的去重数
CREATE TABLE IF NOT EXISTS agg_hour(
	server_id TEXT NOT NULL, site TEXT NOT NULL, ts INTEGER NOT NULL,
	req INTEGER NOT NULL, pv INTEGER NOT NULL, uv INTEGER NOT NULL, ip INTEGER NOT NULL,
	bytes INTEGER NOT NULL, req_bytes INTEGER NOT NULL, spider INTEGER NOT NULL,
	rt_sum REAL NOT NULL, rt_cnt INTEGER NOT NULL,
	s2 INTEGER NOT NULL, s3 INTEGER NOT NULL, s4 INTEGER NOT NULL, s5 INTEGER NOT NULL,
	PRIMARY KEY(server_id, site, ts));
CREATE TABLE IF NOT EXISTS agg_day(
	server_id TEXT NOT NULL, site TEXT NOT NULL, ts INTEGER NOT NULL,
	req INTEGER NOT NULL, pv INTEGER NOT NULL, uv INTEGER NOT NULL, ip INTEGER NOT NULL,
	bytes INTEGER NOT NULL, req_bytes INTEGER NOT NULL, spider INTEGER NOT NULL,
	rt_sum REAL NOT NULL, rt_cnt INTEGER NOT NULL,
	s2 INTEGER NOT NULL, s3 INTEGER NOT NULL, s4 INTEGER NOT NULL, s5 INTEGER NOT NULL,
	PRIMARY KEY(server_id, site, ts));

-- 维度日表：每天每个维度只留前若干名，保留 1 年。今天的数据实时从明细算
CREATE TABLE IF NOT EXISTS dim_day(
	server_id TEXT NOT NULL, site TEXT NOT NULL, ts INTEGER NOT NULL, dim TEXT NOT NULL, key TEXT NOT NULL,
	extra TEXT NOT NULL, req INTEGER NOT NULL, pv INTEGER NOT NULL, uv INTEGER NOT NULL, bytes INTEGER NOT NULL,
	last_ts INTEGER NOT NULL, PRIMARY KEY(server_id, site, ts, dim, key));

-- 待重算的小时（写入明细时同一事务里标记），以及待生成维度日表的日期
CREATE TABLE IF NOT EXISTS dirty(
	id INTEGER PRIMARY KEY, server_id TEXT NOT NULL, site TEXT NOT NULL, hour INTEGER NOT NULL,
	UNIQUE(server_id, site, hour));
CREATE TABLE IF NOT EXISTS dim_pending(
	server_id TEXT NOT NULL, site TEXT NOT NULL, ts INTEGER NOT NULL, PRIMARY KEY(server_id, site, ts));

CREATE TABLE IF NOT EXISTS settings(k TEXT PRIMARY KEY, v TEXT NOT NULL);
CREATE TABLE IF NOT EXISTS users(username TEXT PRIMARY KEY, pass_hash TEXT NOT NULL, updated INTEGER NOT NULL);
CREATE TABLE IF NOT EXISTS sessions(
	token_hash TEXT PRIMARY KEY, username TEXT NOT NULL, created INTEGER NOT NULL, expires INTEGER NOT NULL,
	ip TEXT NOT NULL, ua TEXT NOT NULL);

-- 第二版：封禁 IP（表先建好）
CREATE TABLE IF NOT EXISTS bans(
	id INTEGER PRIMARY KEY, server_id TEXT NOT NULL DEFAULT '', site TEXT NOT NULL DEFAULT '', ip TEXT NOT NULL,
	reason TEXT NOT NULL DEFAULT '', source TEXT NOT NULL DEFAULT 'manual', created INTEGER NOT NULL,
	expires INTEGER NOT NULL DEFAULT 0, active INTEGER NOT NULL DEFAULT 1);
`

// migrations 是建表之后追加的列。
var migrations = []string{
	onboardSchema,
	`ALTER TABLE bans ADD COLUMN removed INTEGER NOT NULL DEFAULT 0`, // 解封时间（手动解封或到期）
}

// Open 打开（必要时创建）数据库。
func Open(path string, loc *time.Location) (*Store, error) {
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)&_pragma=synchronous(NORMAL)" +
		"&_pragma=cache_size(-8000)&_pragma=temp_store(MEMORY)&_txlock=immediate"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(2)
	db.SetConnMaxIdleTime(5 * time.Minute)
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("初始化数据库: %w", err)
	}
	// 增量迁移：列已存在时 SQLite 报错，忽略即可
	for _, m := range migrations {
		db.Exec(m)
	}
	return &Store{DB: db, Loc: loc}, nil
}

// Close 关闭数据库。
func (s *Store) Close() error { return s.DB.Close() }

// ---- 设置 ----

// Settings 读取设置，缺失的字段用默认值补齐。
func (s *Store) Settings() model.Settings {
	st := model.DefaultSettings()
	var v string
	if err := s.DB.QueryRow(`SELECT v FROM settings WHERE k='main'`).Scan(&v); err == nil {
		json.Unmarshal([]byte(v), &st)
	}
	if st.DetailRetentionDays < 1 {
		st.DetailRetentionDays = 7
	}
	if st.AggRetentionDays < 30 {
		st.AggRetentionDays = 365
	}
	if st.SlowThresholdMs < 1 {
		st.SlowThresholdMs = 1000
	}
	return st
}

// SaveSettings 保存设置。
func (s *Store) SaveSettings(st model.Settings) error {
	b, _ := json.Marshal(st)
	_, err := s.DB.Exec(`INSERT INTO settings(k,v) VALUES('main',?) ON CONFLICT(k) DO UPDATE SET v=excluded.v`, string(b))
	return err
}

// ---- 站点 ----

// Sites 列出站点。serverID 为空时列出所有服务器的。
func (s *Store) Sites(serverID string) ([]model.Site, error) {
	q := `SELECT server_id, site, log_path, enabled FROM sites`
	var args []any
	if serverID != "" {
		q += ` WHERE server_id=?`
		args = append(args, serverID)
	}
	rows, err := s.DB.Query(q+` ORDER BY created, site`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Site
	for rows.Next() {
		var x model.Site
		if err := rows.Scan(&x.ServerID, &x.Site, &x.LogPath, &x.Enabled); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}

// UpsertSite 新增或修改站点。
func (s *Store) UpsertSite(x model.Site) error {
	_, err := s.DB.Exec(`INSERT INTO sites(server_id, site, log_path, enabled, created) VALUES(?,?,?,?,?)
		ON CONFLICT(server_id, site) DO UPDATE SET log_path=excluded.log_path, enabled=excluded.enabled`,
		x.ServerID, x.Site, x.LogPath, x.Enabled, time.Now().Unix())
	return err
}

// DeleteSite 删除站点配置（已有统计数据保留，到期自然清理）。
func (s *Store) DeleteSite(serverID, site string) error {
	_, err := s.DB.Exec(`DELETE FROM sites WHERE server_id=? AND site=?`, serverID, site)
	return err
}

// EnsureServer 确保服务器记录存在。
func (s *Store) EnsureServer(id, name string) error {
	_, err := s.DB.Exec(`INSERT OR IGNORE INTO servers(id, name, created) VALUES(?,?,?)`, id, name, time.Now().Unix())
	return err
}

// ---- 跟读进度 ----

// TailState 读取某个日志文件的进度，没有记录时返回 nil。
func (s *Store) TailState(serverID, path string) *tail.State {
	var st tail.State
	var ino int64
	err := s.DB.QueryRow(`SELECT ino, offset, fp, fplen FROM tail_state WHERE server_id=? AND path=?`, serverID, path).
		Scan(&ino, &st.Offset, &st.FP, &st.FPLen)
	if err != nil {
		return nil
	}
	st.Ino = uint64(ino)
	st.Path = path
	return &st
}

// ---- 写入 ----

const insertReq = `INSERT INTO req(server_id, site, ts, ip, country, province, city, isp, method, uri, path, proto,
	status, bytes, req_len, rt, urt, referer, ref_host, ua, browser, os, device, spider, is_pv, visitor)
	VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`

// InsertBatch 在一个事务里写入明细、标记待重算的小时、保存跟读进度。
// 事务成功才算这批日志处理完，所以重启后既不会丢也不会重复。
func (s *Store) InsertBatch(serverID string, recs []model.Record, st *tail.State) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if len(recs) > 0 {
		stmt, err := tx.Prepare(insertReq)
		if err != nil {
			return err
		}
		defer stmt.Close()
		hours := map[[2]any]bool{}
		for i := range recs {
			r := &recs[i]
			if _, err := stmt.Exec(serverID, r.Site, r.TS, r.IP, r.Country, r.Province, r.City, r.ISP,
				r.Method, trunc(r.URI, 2048), trunc(r.Path, 1024), r.Proto, r.Status, r.Bytes, r.ReqLen, r.RT, r.URT,
				trunc(r.Referer, 1024), r.RefHost, trunc(r.UA, 512), r.Browser, r.OS, r.Device, r.Spider, r.IsPV, r.Visitor); err != nil {
				return err
			}
			hours[[2]any{r.Site, r.TS / 3600 * 3600}] = true
		}
		for k := range hours {
			// REPLACE 会换一个新的 id，汇总任务据此区分「处理期间又被标记」的情况
			if _, err := tx.Exec(`INSERT OR REPLACE INTO dirty(server_id, site, hour) VALUES(?,?,?)`, serverID, k[0], k[1]); err != nil {
				return err
			}
		}
	}
	if st != nil {
		if _, err := tx.Exec(`INSERT INTO tail_state(server_id, path, ino, offset, fp, fplen, updated) VALUES(?,?,?,?,?,?,?)
			ON CONFLICT(server_id, path) DO UPDATE SET ino=excluded.ino, offset=excluded.offset, fp=excluded.fp,
			fplen=excluded.fplen, updated=excluded.updated`,
			serverID, st.Path, int64(st.Ino), st.Offset, st.FP, st.FPLen, time.Now().Unix()); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func trunc(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// ---- 汇总 ----

// DayStart 返回 t 所在自然日（按配置的时区）零点的 Unix 秒。
func (s *Store) DayStart(t time.Time) int64 {
	t = t.In(s.Loc)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, s.Loc).Unix()
}

// NextDay 返回 dayTS 的下一天零点。
func (s *Store) NextDay(dayTS int64) int64 {
	t := time.Unix(dayTS, 0).In(s.Loc)
	return time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, s.Loc).Unix()
}

const aggCols = `COUNT(*), COALESCE(SUM(is_pv),0),
	COUNT(DISTINCT CASE WHEN spider='' AND visitor<>0 THEN visitor END), COUNT(DISTINCT ip),
	COALESCE(SUM(bytes),0), COALESCE(SUM(req_len),0), COALESCE(SUM(spider<>''),0),
	COALESCE(SUM(CASE WHEN rt>=0 THEN rt ELSE 0 END),0), COALESCE(SUM(rt>=0),0),
	COALESCE(SUM(status BETWEEN 200 AND 299),0), COALESCE(SUM(status BETWEEN 300 AND 399),0),
	COALESCE(SUM(status BETWEEN 400 AND 499),0), COALESCE(SUM(status>=500),0)`

func (s *Store) recompute(tx *sql.Tx, table, serverID, site string, from, to int64) error {
	var n int64
	if err := tx.QueryRow(`SELECT COUNT(*) FROM req WHERE site=? AND server_id=? AND ts>=? AND ts<?`, site, serverID, from, to).Scan(&n); err != nil {
		return err
	}
	if n == 0 {
		_, err := tx.Exec(`DELETE FROM `+table+` WHERE server_id=? AND site=? AND ts=?`, serverID, site, from)
		return err
	}
	_, err := tx.Exec(`INSERT OR REPLACE INTO `+table+`(server_id, site, ts, req, pv, uv, ip, bytes, req_bytes, spider,
		rt_sum, rt_cnt, s2, s3, s4, s5) SELECT ?, ?, ?, `+aggCols+` FROM req WHERE site=? AND server_id=? AND ts>=? AND ts<?`,
		serverID, site, from, site, serverID, from, to)
	return err
}

// Rollup 重算被标记的小时和对应的天，并为已经结束的日期生成维度日表。
func (s *Store) Rollup(now time.Time) error {
	type dk struct {
		server, site string
		ts           int64
	}
	rows, err := s.DB.Query(`SELECT id, server_id, site, hour FROM dirty ORDER BY id`)
	if err != nil {
		return err
	}
	var maxID int64
	var hours []dk
	for rows.Next() {
		var id int64
		var d dk
		if err := rows.Scan(&id, &d.server, &d.site, &d.ts); err != nil {
			rows.Close()
			return err
		}
		maxID = id
		hours = append(hours, d)
	}
	rows.Close()

	if len(hours) > 0 {
		tx, err := s.DB.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		days := map[dk]bool{}
		for _, h := range hours {
			if err := s.recompute(tx, "agg_hour", h.server, h.site, h.ts, h.ts+3600); err != nil {
				return err
			}
			days[dk{h.server, h.site, s.DayStart(time.Unix(h.ts, 0))}] = true
		}
		for d := range days {
			if err := s.recompute(tx, "agg_day", d.server, d.site, d.ts, s.NextDay(d.ts)); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT OR IGNORE INTO dim_pending(server_id, site, ts) VALUES(?,?,?)`, d.server, d.site, d.ts); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`DELETE FROM dirty WHERE id<=?`, maxID); err != nil {
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return s.buildPendingDims(now)
}

func (s *Store) buildPendingDims(now time.Time) error {
	today := s.DayStart(now)
	rows, err := s.DB.Query(`SELECT server_id, site, ts FROM dim_pending WHERE ts<?`, today)
	if err != nil {
		return err
	}
	type p struct {
		server, site string
		ts           int64
	}
	var ps []p
	for rows.Next() {
		var x p
		rows.Scan(&x.server, &x.site, &x.ts)
		ps = append(ps, x)
	}
	rows.Close()
	for _, x := range ps {
		if err := s.buildDims(x.server, x.site, x.ts, s.NextDay(x.ts)); err != nil {
			return err
		}
	}
	return nil
}

// buildDims 生成某天的维度日表。
func (s *Store) buildDims(serverID, site string, from, to int64) error {
	tx, err := s.DB.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM dim_day WHERE server_id=? AND site=? AND ts=?`, serverID, site, from); err != nil {
		return err
	}
	f := Filter{ServerID: serverID, Site: site, From: from, To: to}
	for _, d := range Dims {
		rs, err := liveDim(tx, f, d, dimKeep(d))
		if err != nil {
			return err
		}
		for _, r := range rs {
			if _, err := tx.Exec(`INSERT INTO dim_day(server_id, site, ts, dim, key, extra, req, pv, uv, bytes, last_ts)
				VALUES(?,?,?,?,?,?,?,?,?,?,?)`, serverID, site, from, d, r.Key, r.Extra, r.Req, r.PV, r.UV, r.Bytes, r.Last); err != nil {
				return err
			}
		}
	}
	if _, err := tx.Exec(`DELETE FROM dim_pending WHERE server_id=? AND site=? AND ts=?`, serverID, site, from); err != nil {
		return err
	}
	return tx.Commit()
}

// Cleanup 清理过期数据。
func (s *Store) Cleanup(now time.Time, st model.Settings) error {
	detailCut := s.DayStart(now.AddDate(0, 0, -st.DetailRetentionDays+1))
	aggCut := s.DayStart(now.AddDate(0, 0, -st.AggRetentionDays+1))
	// 删除前确保要删的明细已经汇总过
	if err := s.Rollup(now); err != nil {
		return err
	}
	stmts := []struct {
		q string
		v int64
	}{
		{`DELETE FROM req WHERE ts<?`, detailCut},
		{`DELETE FROM agg_hour WHERE ts<?`, aggCut},
		{`DELETE FROM agg_day WHERE ts<?`, aggCut},
		{`DELETE FROM dim_day WHERE ts<?`, aggCut},
		{`DELETE FROM sessions WHERE expires<?`, now.Unix()},
	}
	for _, x := range stmts {
		if _, err := s.DB.Exec(x.q, x.v); err != nil {
			return err
		}
	}
	_, err := s.DB.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`)
	return err
}

// ErrNotFound 表示记录不存在。
var ErrNotFound = errors.New("not found")

// Reclassify 按新的 PV 规则重新判定明细保留期内的 is_pv，并重算受影响的汇总和维度日表。
func (s *Store) Reclassify(isPV func(method string, status int, path, spider string) bool) (int64, error) {
	rows, err := s.DB.Query(`SELECT id, server_id, site, ts, method, status, path, spider, is_pv FROM req`)
	if err != nil {
		return 0, err
	}
	type upd struct {
		id   int64
		pv   bool
		srv  string
		site string
		hour int64
	}
	var ups []upd
	for rows.Next() {
		var u upd
		var ts int64
		var method, path, spider string
		var status int
		var cur bool
		if err := rows.Scan(&u.id, &u.srv, &u.site, &ts, &method, &status, &path, &spider, &cur); err != nil {
			rows.Close()
			return 0, err
		}
		if u.pv = isPV(method, status, path, spider); u.pv != cur {
			u.hour = ts / 3600 * 3600
			ups = append(ups, u)
		}
	}
	rows.Close()
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, u := range ups {
		if _, err := tx.Exec(`UPDATE req SET is_pv=? WHERE id=?`, u.pv, u.id); err != nil {
			return 0, err
		}
		if _, err := tx.Exec(`INSERT OR REPLACE INTO dirty(server_id, site, hour) VALUES(?,?,?)`, u.srv, u.site, u.hour); err != nil {
			return 0, err
		}
	}
	// 维度日表里的 pv 也要重算
	if len(ups) > 0 {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO dim_pending(server_id, site, ts) SELECT DISTINCT server_id, site, ts FROM dim_day
			WHERE ts>=(SELECT COALESCE(MIN(ts),0) FROM req)`); err != nil {
			return 0, err
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int64(len(ups)), s.Rollup(time.Now())
}

// ClearBackground 一次性修正旧版本入库的明细：后台自动请求（bg 返回 true 的路径）不算访客、不算浏览量，
// 并重算受影响的汇总和维度日表。做过一次后记在 settings 里，以后启动直接跳过。
func (s *Store) ClearBackground(bg func(path string) bool) (int64, error) {
	var done string
	if s.DB.QueryRow(`SELECT v FROM settings WHERE k='mig_bg_visitor'`).Scan(&done) == nil {
		return 0, nil
	}
	type key struct {
		srv, site string
		hour      int64
	}
	rows, err := s.DB.Query(`SELECT id, server_id, site, ts, path FROM req WHERE visitor<>0 OR is_pv<>0`)
	if err != nil {
		return 0, err
	}
	var ids []int64
	hours := map[key]bool{}
	for rows.Next() {
		var id, ts int64
		var k key
		var p string
		if err := rows.Scan(&id, &k.srv, &k.site, &ts, &p); err != nil {
			rows.Close()
			return 0, err
		}
		if bg(p) {
			ids = append(ids, id)
			k.hour = ts / 3600 * 3600
			hours[k] = true
		}
	}
	rows.Close()
	tx, err := s.DB.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	for _, id := range ids {
		if _, err := tx.Exec(`UPDATE req SET visitor=0, is_pv=0 WHERE id=?`, id); err != nil {
			return 0, err
		}
	}
	for k := range hours {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO dirty(server_id, site, hour) VALUES(?,?,?)`, k.srv, k.site, k.hour); err != nil {
			return 0, err
		}
	}
	if len(ids) > 0 {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO dim_pending(server_id, site, ts) SELECT DISTINCT server_id, site, ts FROM dim_day
			WHERE ts>=(SELECT COALESCE(MIN(ts),0) FROM req)`); err != nil {
			return 0, err
		}
	}
	if _, err := tx.Exec(`INSERT OR REPLACE INTO settings(k,v) VALUES('mig_bg_visitor','1')`); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return int64(len(ids)), s.Rollup(time.Now())
}
