package store

import (
	"math/rand"
	"path/filepath"
	"testing"
	"time"

	"liuliobs/internal/model"
	"liuliobs/internal/tail"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	loc, _ := time.LoadLocation("Asia/Shanghai")
	if loc == nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	s, err := Open(filepath.Join(t.TempDir(), "t.db"), loc)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

// 汇总表（小时 / 天 / 维度日表）和直接从明细算的结果必须一致
func TestAggregatesMatchLive(t *testing.T) {
	s := testStore(t)
	now := time.Now()
	today := s.DayStart(now)
	from := s.DayStart(now.AddDate(0, 0, -2))
	rng := rand.New(rand.NewSource(1))
	var recs []model.Record
	for ts := from; ts < now.Unix(); ts += int64(5 + rng.Intn(60)) {
		ip := []string{"1.1.1.1", "2.2.2.2", "3.3.3.3", "4.4.4.4"}[rng.Intn(4)]
		spider := ""
		if rng.Intn(5) == 0 {
			spider = "百度"
		}
		st := []int{200, 200, 200, 304, 404, 500}[rng.Intn(6)]
		recs = append(recs, model.Record{Site: "a.com", TS: ts, IP: ip, Method: "GET", URI: "/p", Path: []string{"/", "/a", "/b"}[rng.Intn(3)],
			Status: st, Bytes: int64(rng.Intn(5000)), RT: rng.Float64(), URT: -1, Spider: spider, Browser: "Chrome", OS: "Windows", Device: "电脑",
			Country: "中国", Province: "广东省", IsPV: spider == "" && st < 400, Visitor: int64(rng.Intn(6)), RefHost: []string{"", "baidu.com"}[rng.Intn(2)]})
	}
	st := &tail.State{Path: "/x"}
	if err := s.InsertBatch("local", recs, st); err != nil {
		t.Fatal(err)
	}
	if err := s.Rollup(now); err != nil {
		t.Fatal(err)
	}
	for d := from; d < today; d = s.NextDay(d) {
		f := Filter{Site: "a.com", From: d, To: s.NextDay(d)}
		live, _ := s.TotalsLive(f)
		days, _ := s.TotalsDays(f)
		if live.Req != days.Req || live.PV != days.PV || live.UV != days.UV || live.IP != days.IP || live.Bytes != days.Bytes || live.S5 != days.S5 {
			t.Fatalf("天聚合与明细不一致：%+v vs %+v", days, live)
		}
		pts, _ := s.Trend(f, 3600)
		var req, pv int64
		for _, p := range pts {
			req += p.Req
			pv += p.PV
		}
		if req != live.Req || pv != live.PV {
			t.Fatalf("小时趋势之和不一致：%d/%d vs %d/%d", req, pv, live.Req, live.PV)
		}
		// 已结束的日期应该生成了维度日表，结果与实时计算一致
		fromTable, _ := s.Dim(f, "status", 0, "req", now)
		liveRows, _ := liveDim(s.DB, f, "status", 0)
		if len(fromTable) != len(liveRows) {
			t.Fatalf("维度日表行数不一致 %d vs %d", len(fromTable), len(liveRows))
		}
		var built int
		s.DB.QueryRow(`SELECT COUNT(*) FROM dim_day WHERE ts=?`, d).Scan(&built)
		if built == 0 {
			t.Fatalf("%d 没有生成维度日表", d)
		}
	}
	// 进度和明细在同一事务
	if got := s.TailState("local", "/x"); got == nil {
		t.Fatal("没有保存跟读进度")
	}
}
