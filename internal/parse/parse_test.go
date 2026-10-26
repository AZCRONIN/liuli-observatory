package parse

import "testing"

func TestJSON(t *testing.T) {
	line := `{"t":"2026-10-20T11:31:00+08:00","host":"sosoquark.com","ip":"1.2.3.4","xff":"","cfip":"5.6.7.8","xri":"","m":"GET","uri":"/title/1?x=1","st":200,"bytes":1234,"rl":300,"rt":0.052,"urt":"0.050, 0.001","ref":"https://www.baidu.com/","ua":"Mozilla/5.0","proto":"HTTP/2.0"}`
	r, err := Line([]byte(line), "remote_addr")
	if err != nil {
		t.Fatal(err)
	}
	if r.IP != "1.2.3.4" || r.Path != "/title/1" || r.Status != 200 || r.Bytes != 1234 || r.ReqLen != 300 || r.RT != 0.052 {
		t.Fatalf("解析结果不对：%+v", r)
	}
	if r.URT < 0.0509 || r.URT > 0.0511 {
		t.Fatalf("多段 upstream 耗时应累加：%v", r.URT)
	}
	if r.TS != 1792467060 {
		t.Fatalf("时间不对：%d", r.TS)
	}
	// 只有设置为 cf 时才信任 CF-Connecting-IP
	r, _ = Line([]byte(line), "cf")
	if r.IP != "5.6.7.8" {
		t.Fatalf("cf 模式应取 CF-Connecting-IP：%s", r.IP)
	}
}

func TestJSONMissingUpstream(t *testing.T) {
	r, err := Line([]byte(`{"t":"2026-10-20T11:31:00+08:00","ip":"1.2.3.4","m":"GET","uri":"/","st":404,"bytes":10,"rl":1,"rt":0.000,"urt":"","ref":"","ua":"","proto":"HTTP/1.1"}`), "remote_addr")
	if err != nil || r.URT != -1 || r.RT != 0 {
		t.Fatalf("%v %+v", err, r)
	}
}

func TestCombined(t *testing.T) {
	line := `66.249.66.1 - - [20/Oct/2026:11:31:00 +0800] "GET /a?b=1 HTTP/1.1" 301 162 "-" "Mozilla/5.0 (compatible; Googlebot/2.1; +http://www.google.com/bot.html)" "-"`
	r, err := Line([]byte(line), "remote_addr")
	if err != nil {
		t.Fatal(err)
	}
	if r.IP != "66.249.66.1" || r.Method != "GET" || r.Path != "/a" || r.Status != 301 || r.Bytes != 162 || r.RT != -1 || r.Referer != "" {
		t.Fatalf("解析结果不对：%+v", r)
	}
}

func TestBadLine(t *testing.T) {
	if _, err := Line([]byte("garbage"), ""); err == nil {
		t.Fatal("应当报错")
	}
	if _, err := Line([]byte("   "), ""); err != ErrSkip {
		t.Fatal("空行应跳过")
	}
}
