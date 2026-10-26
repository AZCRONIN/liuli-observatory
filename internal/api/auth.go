package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	cookieName = "liuli_sid"
	sessionTTL = 7 * 24 * time.Hour
)

type ctxKey struct{}

// EnsureAdmin 确保有管理员账号。没有账号时用 password 创建；password 为空则随机生成并返回。
// reset 为 true 时用 password 重置已有账号的密码（忘记密码时用）。
func EnsureAdmin(db *sql.DB, user, password string, reset bool) (created bool, generated string, err error) {
	var n int
	if err = db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return
	}
	if n > 0 && !(reset && password != "") {
		return false, "", nil
	}
	if password == "" {
		b := make([]byte, 12)
		rand.Read(b)
		password = base64.RawURLEncoding.EncodeToString(b)
		generated = password
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), 12)
	if err != nil {
		return
	}
	if n > 0 {
		_, err = db.Exec(`UPDATE users SET pass_hash=?, updated=?`, string(h), time.Now().Unix())
		db.Exec(`DELETE FROM sessions`)
		return false, generated, err
	}
	_, err = db.Exec(`INSERT INTO users(username, pass_hash, updated) VALUES(?,?,?)`, user, string(h), time.Now().Unix())
	return true, generated, err
}

var (
	dummyOnce sync.Once
	dummy     []byte
)

func dummyHash() []byte {
	dummyOnce.Do(func() { dummy, _ = bcrypt.GenerateFromPassword([]byte("liuli-dummy"), 12) })
	return dummy
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

// ---- 登录限流：同一 IP 15 分钟内失败 5 次锁 15 分钟；全局 15 分钟内失败 30 次暂停登录 5 分钟 ----

type limiter struct {
	mu     sync.Mutex
	fails  map[string][]time.Time
	global []time.Time
}

func newLimiter() *limiter { return &limiter{fails: map[string][]time.Time{}} }

func prune(ts []time.Time, win time.Duration) []time.Time {
	cut := time.Now().Add(-win)
	i := 0
	for i < len(ts) && ts[i].Before(cut) {
		i++
	}
	return ts[i:]
}

// blocked 返回还要等多久才能再试，0 表示可以。
func (l *limiter) blocked(ip string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.global = prune(l.global, 5*time.Minute)
	if len(l.global) >= 30 {
		return time.Until(l.global[0].Add(5 * time.Minute))
	}
	f := prune(l.fails[ip], 15*time.Minute)
	l.fails[ip] = f
	if len(f) >= 5 {
		return time.Until(f[0].Add(15 * time.Minute))
	}
	return 0
}

func (l *limiter) fail(ip string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	l.fails[ip] = append(l.fails[ip], now)
	l.global = append(l.global, now)
	if len(l.fails) > 10000 {
		l.fails = map[string][]time.Time{}
	}
}

func (l *limiter) success(ip string) {
	l.mu.Lock()
	delete(l.fails, ip)
	l.mu.Unlock()
}

// clientIP 取请求方 IP。观测台只监听本机 / 容器网络，来自内网的请求信任反代传来的 X-Real-IP。
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLoopback() || ip.IsPrivate()) {
		if x := strings.TrimSpace(r.Header.Get("X-Real-IP")); x != "" && net.ParseIP(x) != nil {
			return x
		}
		if x := r.Header.Get("X-Forwarded-For"); x != "" {
			x = strings.TrimSpace(strings.Split(x, ",")[0])
			if net.ParseIP(x) != nil {
				return x
			}
		}
	}
	return host
}

// ---- 处理函数 ----

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if d := a.lim.blocked(ip); d > 0 {
		writeErr(w, http.StatusTooManyRequests, "尝试次数过多，请 "+fmtWait(d)+" 后再试")
		return
	}
	var in struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	var hash string
	err := a.Store.DB.QueryRow(`SELECT pass_hash FROM users WHERE username=?`, in.Username).Scan(&hash)
	if err != nil {
		// 用户名不存在时也做一次 bcrypt，避免通过耗时判断用户名
		hash = string(dummyHash())
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Password)) != nil || err != nil {
		a.lim.fail(ip)
		writeErr(w, http.StatusUnauthorized, "用户名或密码不对")
		return
	}
	a.lim.success(ip)
	a.rememberSelfHost(r)
	b := make([]byte, 32)
	rand.Read(b)
	tok := base64.RawURLEncoding.EncodeToString(b)
	now := time.Now()
	ua := r.UserAgent()
	if len(ua) > 300 {
		ua = ua[:300]
	}
	if _, err := a.Store.DB.Exec(`INSERT INTO sessions(token_hash, username, created, expires, ip, ua) VALUES(?,?,?,?,?,?)`,
		hashToken(tok), in.Username, now.Unix(), now.Add(sessionTTL).Unix(), ip, ua); err != nil {
		writeErr(w, 500, "登录失败："+err.Error())
		return
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: tok, Path: "/", HttpOnly: true, Secure: a.Cfg.CookieSecure,
		SameSite: http.SameSiteStrictMode, Expires: now.Add(sessionTTL)})
	writeJSON(w, map[string]any{"username": in.Username})
}

// rememberSelfHost 记下观测台自己的域名（经反代访问时的 Host），自动发现站点时跳过它。
func (a *API) rememberSelfHost(r *http.Request) {
	host := strings.ToLower(r.Host)
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host == "" || host == "localhost" || net.ParseIP(host) != nil {
		return
	}
	st := a.Store.Settings()
	for _, x := range st.SelfHosts {
		if x == host {
			return
		}
	}
	st.SelfHosts = append(st.SelfHosts, host)
	a.Store.SaveSettings(st)
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		a.Store.DB.Exec(`DELETE FROM sessions WHERE token_hash=?`, hashToken(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, Secure: a.Cfg.CookieSecure,
		SameSite: http.SameSiteStrictMode, MaxAge: -1})
	writeJSON(w, map[string]any{"ok": true})
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, map[string]any{"username": r.Context().Value(ctxKey{}), "server_id": a.Cfg.ServerID, "mode": a.Cfg.Mode})
}

func (a *API) changePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	if !readJSON(w, r, &in) {
		return
	}
	if len(in.New) < 10 {
		writeErr(w, 400, "新密码至少 10 位")
		return
	}
	user, _ := r.Context().Value(ctxKey{}).(string)
	var hash string
	if err := a.Store.DB.QueryRow(`SELECT pass_hash FROM users WHERE username=?`, user).Scan(&hash); err != nil ||
		bcrypt.CompareHashAndPassword([]byte(hash), []byte(in.Old)) != nil {
		writeErr(w, 400, "原密码不对")
		return
	}
	h, _ := bcrypt.GenerateFromPassword([]byte(in.New), 12)
	a.Store.DB.Exec(`UPDATE users SET pass_hash=?, updated=? WHERE username=?`, string(h), time.Now().Unix(), user)
	// 其他设备上的会话全部失效，当前会话保留
	c, _ := r.Cookie(cookieName)
	a.Store.DB.Exec(`DELETE FROM sessions WHERE username=? AND token_hash<>?`, user, hashToken(c.Value))
	writeJSON(w, map[string]any{"ok": true})
}

// requireAuth 校验会话 Cookie；写操作还要求带上自定义请求头，防止跨站请求伪造。
func (a *API) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := a.sessionUser(r)
		if err != nil {
			writeErr(w, http.StatusUnauthorized, "请先登录")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get("X-Liuli") != "1" {
			writeErr(w, http.StatusForbidden, "缺少请求头")
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKey{}, user)))
	})
}

func (a *API) sessionUser(r *http.Request) (string, error) {
	c, err := r.Cookie(cookieName)
	if err != nil || c.Value == "" {
		return "", errors.New("no session")
	}
	var user string
	err = a.Store.DB.QueryRow(`SELECT username FROM sessions WHERE token_hash=? AND expires>?`, hashToken(c.Value), time.Now().Unix()).Scan(&user)
	return user, err
}

func fmtWait(d time.Duration) string {
	m := int(d.Minutes()) + 1
	return itoa(m) + " 分钟"
}
