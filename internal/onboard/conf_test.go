package onboard

import (
	"strings"
	"testing"
)

// 截取自 1Panel 2.x 默认的 nginx.conf
const nginxConf = `user  root;
worker_processes  auto;
# http { 这是注释，不能当成 http 块
events {
    worker_connections  1024;
}
http {
    include       mime.types;
    log_format  main  '$remote_addr - $remote_user [$time_local] "$request" '
                      '$status $body_bytes_sent "$http_referer" '
                      '"$http_user_agent" "$http_x_forwarded_for"';
    include /usr/local/openresty/nginx/conf/conf.d/*.conf;
}
stream {
    include /usr/local/openresty/nginx/conf/stream.d/*.conf;
}
`

func TestPatchNginx(t *testing.T) {
	if HasFormat(nginxConf) {
		t.Fatal("还没有 liuli_json")
	}
	out, added, err := PatchNginx(nginxConf, true, "include /www/liuli-ban/*.conf;")
	if err != nil {
		t.Fatal(err)
	}
	if !HasFormat(out) || len(added) != 2 {
		t.Fatalf("没加上：%v\n%s", added, out)
	}
	// 只能插在 http 块里，且原有内容一个字不少
	h := strings.Index(out, "http {")
	f := strings.Index(out, "log_format liuli_json")
	inc := strings.Index(out, "include /www/liuli-ban/*.conf;")
	s := strings.Index(out, "stream {")
	if !(h < f && f < inc && inc < s) {
		t.Fatalf("插入位置不对：\n%s", out)
	}
	if strings.Replace(out, out[strings.Index(out, marker):inc+len("include /www/liuli-ban/*.conf;\n")], "", 1) != nginxConf {
		t.Fatalf("改动了原有内容：\n%s", out)
	}
	// 已经有格式时只加 include
	out2, added2, _ := PatchNginx(out, false, "")
	if out2 != out || added2 != nil {
		t.Fatal("没有要加的就不该改")
	}
	if _, _, err := PatchNginx("events {}\n", true, ""); err == nil {
		t.Fatal("没有 http 块时应报错")
	}
	// 注释里的 log_format 不算
	if HasFormat("http {\n  # log_format liuli_json escape=json '{}';\n}") {
		t.Fatal("注释不算")
	}
}

const siteConf = `server {
    listen 80 ;
    listen 443 ssl ;
    server_name example.com;
    index index.php index.html index.htm default.php default.htm default.html;
    access_log /www/sites/example.com/log/access.log main;
    error_log /www/sites/example.com/log/error.log;
    location ^~ /.well-known/acme-challenge {
        allow all;
        root /usr/share/nginx/html;
    }
    # access_log /tmp/x.log main;
    include /www/sites/example.com/proxy/*.conf;
}
`

func TestSiteStateAndPatch(t *testing.T) {
	st, line := SiteState(siteConf)
	if st != StateMain || line != "access_log /www/sites/example.com/log/access.log main;" {
		t.Fatalf("%s %q", st, line)
	}
	out, n := PatchSite(siteConf)
	if n != 1 {
		t.Fatalf("应只改未注释的那一行，实际 %d", n)
	}
	if !strings.Contains(out, "access_log /www/sites/example.com/log/access.log liuli_json;") ||
		!strings.Contains(out, "# access_log /tmp/x.log main;") {
		t.Fatalf("改错了：\n%s", out)
	}
	if strings.Replace(out, "log liuli_json;", "log main;", 1) != siteConf {
		t.Fatal("动了别的内容")
	}
	if st, _ := SiteState(out); st != StateOK {
		t.Fatalf("切换后应为 ok，实际 %s", st)
	}
	if _, n := PatchSite(out); n != 0 {
		t.Fatal("已切换的不应再改")
	}

	cases := map[string]string{
		"server {\n access_log off;\n}":                          StateOff,
		"server {\n listen 80;\n}":                               StateNone,
		"server {\n access_log /a.log combined buffer=32k;\n}":   StateCustom,
		"server {\n access_log /a.log;\n}":                       StateCustom,
		"server {\n access_log /a.log liuli_json buffer=32k;\n}": StateOK,
	}
	for conf, want := range cases {
		if got, _ := SiteState(conf); got != want {
			t.Errorf("%q：期望 %s，实际 %s", conf, want, got)
		}
		if want != StateMain {
			if out, n := PatchSite(conf); n != 0 || out != conf {
				t.Errorf("%q 不应被修改", conf)
			}
		}
	}
}
