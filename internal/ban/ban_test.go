package ban

import (
	"strings"
	"testing"

	"liuliobs/internal/model"
)

func TestValidate(t *testing.T) {
	ok := map[string]string{
		"8.8.8.8":        "8.8.8.8",
		" 1.2.3.4 ":      "1.2.3.4",
		"1.2.3.0/24":     "1.2.3.0/24",
		"1.2.3.9/24":     "1.2.3.0/24",
		"2001:db8::1":    "2001:db8::1",
		"::ffff:8.8.4.4": "8.8.4.4",
		"2001:db8::/48":  "2001:db8::/48",
		"45.148.0.0/16":  "45.148.0.0/16",
	}
	for in, want := range ok {
		got, err := Validate(in, "9.9.9.9", nil)
		if err != nil || got != want {
			t.Errorf("%q：期望 %q，实际 %q（%v）", in, want, got, err)
		}
	}
	bad := []string{"", "abc", "1.2.3.4;", "1.2.3.4 deny all", "0.0.0.0/0", "1.0.0.0/8", "::/0", "2001::/16",
		"127.0.0.1", "10.1.2.3", "192.168.1.1", "172.18.0.2", "::1", "fe80::1", "100.64.0.1"}
	for _, in := range bad {
		if got, err := Validate(in, "", nil); err == nil {
			t.Errorf("%q 应当被拒绝，实际通过为 %q", in, got)
		}
	}
	if _, err := Validate("5.6.7.8", "5.6.7.8", nil); err == nil {
		t.Error("不应允许封禁自己的 IP")
	}
	if _, err := Validate("5.6.0.0/16", "5.6.7.8", nil); err == nil {
		t.Error("不应允许封禁包含自己 IP 的网段")
	}
	if _, err := Validate("66.249.66.1", "", []string{"66.249.64.0/19"}); err == nil {
		t.Error("白名单里的 IP 不应被封禁")
	}
}

func TestRenderOnlyWritesValidRules(t *testing.T) {
	out := render([]model.Ban{{IP: "1.2.3.4"}, {IP: "5.6.7.0/24"}, {IP: "1.2.3.4; include /etc/passwd"}, {IP: "allow all"}})
	if !strings.Contains(out, "deny 1.2.3.4;\n") || !strings.Contains(out, "deny 5.6.7.0/24;\n") {
		t.Fatalf("缺少合法规则：\n%s", out)
	}
	if strings.Contains(out, "passwd") || strings.Contains(out, "allow") || strings.Count(out, "deny") != 2 {
		t.Fatalf("写入了不合法的内容：\n%s", out)
	}
}

func TestHasInclude(t *testing.T) {
	p := "/www/liuli-ban/deny.conf"
	cases := map[string]bool{
		"http {\n    include /www/liuli-ban/*.conf;\n}":  true,
		"http {\n  include /www/liuli-ban/deny.conf;\n}": true,
		"http {\n  # include /www/liuli-ban/*.conf;\n}":  false,
		"http {\n  include /www/other/*.conf;\n}":        false,
	}
	for conf, want := range cases {
		if got := hasInclude(conf, p); got != want {
			t.Errorf("%q：期望 %v", conf, want)
		}
	}
}
