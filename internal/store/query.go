package store

import (
	"database/sql"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Filter 是查询的公共条件。ServerID 为空表示所有服务器汇总。To 不含。
type Filter struct {
	ServerID string
	Site     string
	From, To int64
}

func (f Filter) where(alias string) (string, []any) {
	p := alias
	if p != "" {
		p += "."
	}
	w := p + "site=? AND " + p + "ts>=? AND " + p + "ts<?"
	args := []any{f.Site, f.From, f.To}
	if f.ServerID != "" {
		w += " AND " + p + "server_id=?"
		args = append(args, f.ServerID)
	}
	return w, args
}

type querier interface {
	Query(q string, args ...any) (*sql.Rows, error)
	QueryRow(q string, args ...any) *sql.Row
}

// ---- 总量 ----

// Totals 是一段时间的汇总指标。
type Totals struct {
	Req      int64   `json:"req"`
	PV       int64   `json:"pv"`
	UV       int64   `json:"uv"`
	IP       int64   `json:"ip"`
	Bytes    int64   `json:"bytes"`
	ReqBytes int64   `json:"req_bytes"`
	Spider   int64   `json:"spider"`
	RTSum    float64 `json:"-"`
	RTCnt    int64   `json:"-"`
	RTAvg    float64 `json:"rt_avg"` // 毫秒
	S2       int64   `json:"s2"`
	S3       int64   `json:"s3"`
	S4       int64   `json:"s4"`
	S5       int64   `json:"s5"`
}

func (t *Totals) finish() {
	if t.RTCnt > 0 {
		t.RTAvg = t.RTSum / float64(t.RTCnt) * 1000
	}
}

func (t *Totals) add(o Totals) {
	t.Req += o.Req
	t.PV += o.PV
	t.UV += o.UV
	t.IP += o.IP
	t.Bytes += o.Bytes
	t.ReqBytes += o.ReqBytes
	t.Spider += o.Spider
	t.RTSum += o.RTSum
	t.RTCnt += o.RTCnt
	t.S2 += o.S2
	t.S3 += o.S3
	t.S4 += o.S4
	t.S5 += o.S5
}

func scanTotals(sc interface{ Scan(...any) error }, t *Totals, extra ...any) error {
	dst := append(extra, &t.Req, &t.PV, &t.UV, &t.IP, &t.Bytes, &t.ReqBytes, &t.Spider, &t.RTSum, &t.RTCnt, &t.S2, &t.S3, &t.S4, &t.S5)
	return sc.Scan(dst...)
}

// TotalsLive 直接从明细计算（精确去重，用于明细保留期内的短时段）。
func (s *Store) TotalsLive(f Filter) (Totals, error) {
	w, args := f.where("")
	var t Totals
	err := scanTotals(s.DB.QueryRow(`SELECT `+aggCols+` FROM req WHERE `+w, args...), &t)
	t.finish()
	return t, err
}

const sumCols = `COALESCE(SUM(req),0), COALESCE(SUM(pv),0), COALESCE(SUM(uv),0), COALESCE(SUM(ip),0),
	COALESCE(SUM(bytes),0), COALESCE(SUM(req_bytes),0), COALESCE(SUM(spider),0), COALESCE(SUM(rt_sum),0),
	COALESCE(SUM(rt_cnt),0), COALESCE(SUM(s2),0), COALESCE(SUM(s3),0), COALESCE(SUM(s4),0), COALESCE(SUM(s5),0)`

// TotalsDays 从天聚合累加（UV、IP 为每天去重数之和）。
func (s *Store) TotalsDays(f Filter) (Totals, error) {
	w, args := f.where("")
	var t Totals
	err := scanTotals(s.DB.QueryRow(`SELECT `+sumCols+` FROM agg_day WHERE `+w, args...), &t)
	t.finish()
	return t, err
}

// ---- 趋势 ----

// Point 是趋势图上的一个点。
type Point struct {
	TS int64 `json:"ts"`
	Totals
}

// Trend 返回按颗粒度（60 / 3600 / 86400 秒）分桶的序列，空桶补零。
func (s *Store) Trend(f Filter, gran int64) ([]Point, error) {
	var rows *sql.Rows
	var err error
	w, args := f.where("")
	switch gran {
	case 60:
		rows, err = s.DB.Query(`SELECT ts/60*60 b, `+aggCols+` FROM req WHERE `+w+` GROUP BY b ORDER BY b`, args...)
	case 3600:
		rows, err = s.DB.Query(`SELECT ts, `+sumCols+` FROM agg_hour WHERE `+w+` GROUP BY ts ORDER BY ts`, args...)
	default:
		rows, err = s.DB.Query(`SELECT ts, `+sumCols+` FROM agg_day WHERE `+w+` GROUP BY ts ORDER BY ts`, args...)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	got := map[int64]Point{}
	for rows.Next() {
		var p Point
		if err := scanTotals(rows, &p.Totals, &p.TS); err != nil {
			return nil, err
		}
		p.finish()
		got[p.TS] = p
	}
	var out []Point
	for _, b := range s.Buckets(f.From, f.To, gran) {
		p, ok := got[b]
		if !ok {
			p = Point{TS: b}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Buckets 列出 [from, to) 内各桶的起点；按天分桶时按本地零点对齐。
func (s *Store) Buckets(from, to, gran int64) []int64 {
	var out []int64
	if gran >= 86400 {
		for d := s.DayStart(time.Unix(from, 0)); d < to; d = s.NextDay(d) {
			out = append(out, d)
		}
		return out
	}
	for b := from / gran * gran; b < to; b += gran {
		out = append(out, b)
	}
	return out
}

// ---- 维度 ----

// Dims 是维度日表里保存的维度。
var Dims = []string{"ip", "region", "country", "browser", "os", "device", "spider", "spider_uri", "page", "uri", "ref", "status"}

// DimRow 是维度统计的一行。
type DimRow struct {
	Key   string `json:"key"`
	Extra string `json:"extra,omitempty"`
	Req   int64  `json:"req"`
	PV    int64  `json:"pv"`
	UV    int64  `json:"uv"`
	Bytes int64  `json:"bytes"`
	Last  int64  `json:"last"`
}

// 高基数维度每天只保留前若干名
func dimKeep(d string) int {
	switch d {
	case "ip", "page", "uri", "ref", "spider_uri":
		return 2000
	}
	return 0
}

func dimSQL(d string) (key, extra, cond string) {
	switch d {
	case "ip":
		return "ip", "MAX(country||'|'||province||'|'||city||'|'||isp)", ""
	case "region":
		return "CASE WHEN country='中国' THEN '中国|'||CASE WHEN province='' THEN '未知' ELSE province END ELSE country END", "''", ""
	case "country":
		return "country", "''", ""
	case "browser", "os", "device":
		return d, "''", "spider=''"
	case "spider":
		return "spider", "''", "spider<>''"
	case "spider_uri":
		return "spider||char(9)||path", "''", "spider<>''"
	case "page":
		return "path", "''", "is_pv=1"
	case "uri":
		return "path", "''", ""
	case "ref":
		return "ref_host", "''", "ref_host<>'' AND spider=''"
	case "status":
		return "CAST(status AS TEXT)", "''", ""
	}
	return "", "", ""
}

// ValidDim 报告维度名是否合法。
func ValidDim(d string) bool {
	k, _, _ := dimSQL(d)
	return k != ""
}

func liveDim(q querier, f Filter, d string, limit int) ([]DimRow, error) {
	key, extra, cond := dimSQL(d)
	w, args := f.where("")
	if cond != "" {
		w += " AND " + cond
	}
	sqlq := `SELECT ` + key + ` k, ` + extra + `, COUNT(*) c, COALESCE(SUM(is_pv),0),
		COUNT(DISTINCT CASE WHEN spider='' THEN visitor END), COALESCE(SUM(bytes),0), MAX(ts)
		FROM req WHERE ` + w + ` GROUP BY k ORDER BY c DESC`
	if limit > 0 {
		sqlq += ` LIMIT ` + strconv.Itoa(limit)
	}
	rows, err := q.Query(sqlq, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []DimRow
	for rows.Next() {
		var r DimRow
		if err := rows.Scan(&r.Key, &r.Extra, &r.Req, &r.PV, &r.UV, &r.Bytes, &r.Last); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Dim 查询一个维度：已结束的日期读维度日表，今天（以及还没生成日表的日期）实时从明细算，再合并。
// 多天合并时 UV 是每天去重数之和。sortBy 取 req / pv / uv / bytes。
func (s *Store) Dim(f Filter, d string, limit int, sortBy string, now time.Time) ([]DimRow, error) {
	today := s.DayStart(now)
	merged := map[string]*DimRow{}
	addRows := func(rs []DimRow) {
		for _, r := range rs {
			m, ok := merged[r.Key]
			if !ok {
				c := r
				merged[r.Key] = &c
				continue
			}
			m.Req += r.Req
			m.PV += r.PV
			m.UV += r.UV
			m.Bytes += r.Bytes
			if r.Last > m.Last {
				m.Last = r.Last
				if r.Extra != "" {
					m.Extra = r.Extra
				}
			}
		}
	}
	// 时段不按天对齐（例如最近一小时）或者完全在今天：全部实时算
	if f.From >= today || f.From != s.DayStart(time.Unix(f.From, 0)) {
		rs, err := liveDim(s.DB, f, d, 0)
		if err != nil {
			return nil, err
		}
		addRows(rs)
	} else {
		// 已有日表的日期
		built := map[int64]bool{}
		w, args := f.where("")
		args = append(args, d)
		rows, err := s.DB.Query(`SELECT ts, key, extra, req, pv, uv, bytes, last_ts FROM dim_day WHERE `+w+` AND dim=?`, args...)
		if err != nil {
			return nil, err
		}
		var rs []DimRow
		for rows.Next() {
			var r DimRow
			var ts int64
			if err := rows.Scan(&ts, &r.Key, &r.Extra, &r.Req, &r.PV, &r.UV, &r.Bytes, &r.Last); err != nil {
				rows.Close()
				return nil, err
			}
			built[ts] = true
			rs = append(rs, r)
		}
		rows.Close()
		addRows(rs)
		// 没有日表的日期（今天、或者还没生成）实时算
		for day := f.From; day < f.To; day = s.NextDay(day) {
			if built[day] {
				continue
			}
			end := s.NextDay(day)
			if end > f.To {
				end = f.To
			}
			lf := f
			lf.From, lf.To = day, end
			rs, err := liveDim(s.DB, lf, d, 0)
			if err != nil {
				return nil, err
			}
			addRows(rs)
		}
	}
	out := make([]DimRow, 0, len(merged))
	for _, r := range merged {
		out = append(out, *r)
	}
	val := func(r DimRow) int64 {
		switch sortBy {
		case "pv":
			return r.PV
		case "uv":
			return r.UV
		case "bytes":
			return r.Bytes
		}
		return r.Req
	}
	sort.Slice(out, func(i, j int) bool {
		vi, vj := val(out[i]), val(out[j])
		if vi != vj {
			return vi > vj
		}
		return out[i].Key < out[j].Key
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// DimSeries 返回低基数维度（蜘蛛、状态码）各取值随时间的请求数。
// 按小时时实时从明细算（需在明细保留期内）；按天时读维度日表并补上今天。
func (s *Store) DimSeries(f Filter, d string, gran int64, now time.Time) (buckets []int64, series map[string][]int64, err error) {
	key, _, cond := dimSQL(d)
	buckets = s.Buckets(f.From, f.To, gran)
	idx := make(map[int64]int, len(buckets))
	for i, b := range buckets {
		idx[b] = i
	}
	series = map[string][]int64{}
	put := func(b int64, k string, c int64) {
		i, ok := idx[b]
		if !ok {
			return
		}
		arr, ok := series[k]
		if !ok {
			arr = make([]int64, len(buckets))
			series[k] = arr
		}
		arr[i] += c
	}
	scan := func(rows *sql.Rows, err error, day bool) error {
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var b, c int64
			var k string
			if err := rows.Scan(&b, &k, &c); err != nil {
				return err
			}
			if day {
				b = s.DayStart(time.Unix(b, 0))
			}
			put(b, k, c)
		}
		return rows.Err()
	}
	w, args := f.where("")
	if cond != "" {
		w += " AND " + cond
	}
	if gran < 86400 {
		g := strconv.FormatInt(gran, 10)
		rows, qerr := s.DB.Query(`SELECT ts/`+g+`*`+g+` b, `+key+` k, COUNT(*) FROM req WHERE `+w+` GROUP BY b, k`, args...)
		err = scan(rows, qerr, false)
		return
	}
	fw, fargs := f.where("")
	fargs = append(fargs, d)
	rows, qerr := s.DB.Query(`SELECT ts, key, req FROM dim_day WHERE `+fw+` AND dim=?`, fargs...)
	if err = scan(rows, qerr, false); err != nil {
		return
	}
	// 今天还没有日表
	today := s.DayStart(now)
	if f.To > today {
		lf := f
		if lf.From < today {
			lf.From = today
		}
		lw, largs := lf.where("")
		if cond != "" {
			lw += " AND " + cond
		}
		rows, qerr := s.DB.Query(`SELECT MIN(ts), `+key+` k, COUNT(*) FROM req WHERE `+lw+` GROUP BY k`, largs...)
		err = scan(rows, qerr, true)
	}
	return
}

// ---- 实时 ----

// Active 返回最近 10 分钟的去重访客数，以及每分钟的去重访客数（共 10 个桶）。
func (s *Store) Active(serverID, site string, now time.Time) (int64, []int64, error) {
	end := now.Unix()/60*60 + 60
	f := Filter{ServerID: serverID, Site: site, From: end - 600, To: end}
	w, args := f.where("")
	var total int64
	if err := s.DB.QueryRow(`SELECT COUNT(DISTINCT visitor) FROM req WHERE `+w+` AND spider=''`, args...).Scan(&total); err != nil {
		return 0, nil, err
	}
	rows, err := s.DB.Query(`SELECT ts/60*60 b, COUNT(DISTINCT visitor) FROM req WHERE `+w+` AND spider='' GROUP BY b`, args...)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	bars := make([]int64, 10)
	for rows.Next() {
		var b, c int64
		rows.Scan(&b, &c)
		if i := (b - f.From) / 60; i >= 0 && i < 10 {
			bars[i] = c
		}
	}
	return total, bars, rows.Err()
}

// RTPoint 是实时图上的一秒。
type RTPoint struct {
	TS       int64   `json:"ts"`
	Req      int64   `json:"req"`
	RTAvg    float64 `json:"rt_avg"` // 毫秒
	Bytes    int64   `json:"bytes"`
	ReqBytes int64   `json:"req_bytes"`
}

// Realtime 返回 [from, to) 每秒的请求数、平均耗时、出入流量。
func (s *Store) Realtime(serverID, site string, from, to int64) ([]RTPoint, error) {
	f := Filter{ServerID: serverID, Site: site, From: from, To: to}
	w, args := f.where("")
	rows, err := s.DB.Query(`SELECT ts, COUNT(*), COALESCE(AVG(CASE WHEN rt>=0 THEN rt END),0), COALESCE(SUM(bytes),0),
		COALESCE(SUM(req_len),0) FROM req WHERE `+w+` GROUP BY ts`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	got := map[int64]RTPoint{}
	for rows.Next() {
		var p RTPoint
		rows.Scan(&p.TS, &p.Req, &p.RTAvg, &p.Bytes, &p.ReqBytes)
		p.RTAvg *= 1000
		got[p.TS] = p
	}
	out := make([]RTPoint, 0, to-from)
	for t := from; t < to; t++ {
		p, ok := got[t]
		if !ok {
			p = RTPoint{TS: t}
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ---- 明细 ----

// LogFilter 是明细查询条件。
type LogFilter struct {
	Filter
	Status string  // "" / 2xx / 3xx / 4xx / 5xx / err（>=400）/ 具体状态码
	IP     string  // 精确匹配
	Path   string  // 子串匹配
	Spider string  // "" 全部 / only 只看蜘蛛 / none 排除蜘蛛 / 具体蜘蛛名
	MinRT  float64 // 秒，>0 时只看耗时不低于它的
	Sort   string  // ts（默认，新到旧）/ rt（慢到快）
	Limit  int
	Offset int
}

// LogRow 是一条明细。
type LogRow struct {
	ID      int64   `json:"id"`
	Server  string  `json:"server"`
	TS      int64   `json:"ts"`
	IP      string  `json:"ip"`
	Region  string  `json:"region"`
	Method  string  `json:"method"`
	URI     string  `json:"uri"`
	Status  int     `json:"status"`
	Bytes   int64   `json:"bytes"`
	RT      float64 `json:"rt"` // 毫秒，-1 表示日志里没有
	URT     float64 `json:"urt"`
	Referer string  `json:"referer"`
	UA      string  `json:"ua"`
	Spider  string  `json:"spider"`
	Browser string  `json:"browser"`
	OS      string  `json:"os"`
	Device  string  `json:"device"`
}

// Logs 查询明细，返回本页和总条数。
func (s *Store) Logs(lf LogFilter) ([]LogRow, int64, error) {
	w, args := lf.where("")
	switch st := lf.Status; {
	case st == "":
	case st == "err":
		w += " AND status>=400"
	case len(st) == 3 && strings.HasSuffix(st, "xx"):
		base := int(st[0]-'0') * 100
		w += " AND status>=? AND status<?"
		args = append(args, base, base+100)
	default:
		if code, err := strconv.Atoi(st); err == nil {
			w += " AND status=?"
			args = append(args, code)
		}
	}
	if lf.IP != "" {
		w += " AND ip=?"
		args = append(args, lf.IP)
	}
	if lf.Path != "" {
		w += " AND instr(uri, ?)>0"
		args = append(args, lf.Path)
	}
	switch lf.Spider {
	case "":
	case "only":
		w += " AND spider<>''"
	case "none":
		w += " AND spider=''"
	default:
		w += " AND spider=?"
		args = append(args, lf.Spider)
	}
	if lf.MinRT > 0 {
		w += " AND rt>=?"
		args = append(args, lf.MinRT)
	}
	var total int64
	if err := s.DB.QueryRow(`SELECT COUNT(*) FROM req WHERE `+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	order := "ts DESC, id DESC"
	if lf.Sort == "rt" {
		order = "rt DESC, ts DESC"
	}
	if lf.Limit <= 0 || lf.Limit > 500 {
		lf.Limit = 50
	}
	args = append(args, lf.Limit, lf.Offset)
	rows, err := s.DB.Query(`SELECT id, server_id, ts, ip,
		CASE WHEN country='中国' THEN province||CASE WHEN city<>'' AND city<>province THEN ' '||city ELSE '' END ELSE country END,
		method, uri, status, bytes, rt, urt, referer, ua, spider, browser, os, device
		FROM req WHERE `+w+` ORDER BY `+order+` LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []LogRow
	for rows.Next() {
		var r LogRow
		if err := rows.Scan(&r.ID, &r.Server, &r.TS, &r.IP, &r.Region, &r.Method, &r.URI, &r.Status, &r.Bytes,
			&r.RT, &r.URT, &r.Referer, &r.UA, &r.Spider, &r.Browser, &r.OS, &r.Device); err != nil {
			return nil, 0, err
		}
		if r.RT >= 0 {
			r.RT *= 1000
		}
		if r.URT >= 0 {
			r.URT *= 1000
		}
		out = append(out, r)
	}
	return out, total, rows.Err()
}
