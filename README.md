# 琉璃观测台

基于 OpenResty / Nginx 访问日志的网站监控报表服务。体验对标宝塔「网站监控报表」，不依赖宝塔、不往网页里加 JS，部署在 1Panel 上，界面是琉璃风格。

- 部署：看 [部署说明.md](部署说明.md)
- 需求：看 [需求说明.md](需求说明.md)

## 功能（第一版）

概览（10 分钟活跃、PV/UV/IP/请求/流量/蜘蛛、趋势含前一天和上周同期对比、性能、实时流量）、网站列表、IP/地区、访客/客户端、蜘蛛、URI/来源、状态码、错误日志、慢请求、登录、设置。浅色 / 暗色两套主题，手机可看。

## 本地开发

需要 Go 1.26、Node 22+。

```bash
# 1. 生成示例日志（3 天历史，约每天 3 万请求）
go run ./cmd/loggen -days 3 -per-day 30000

# 2. 打包前端（嵌入二进制）
cd web && npm install && npm run build && cd ..

# 3. 启动（本地 http 调试需要关掉 Secure Cookie）
LIULI_ADMIN_PASSWORD=本地测试密码123 LIULI_COOKIE_SECURE=0 LIULI_LOG_ROOT=demo \
LIULI_SITES="sosoquark.com=$PWD/demo/sosoquark.com/log/access.log" go run ./cmd/liuliobs

# 4. 另开一个终端持续追加实时日志
go run ./cmd/loggen -days 0 -follow -per-day 60000
```

打开 http://127.0.0.1:8090 。改前端时可以 `cd web && npm run dev`（Vite 把 `/api` 代理到 8090）。

测试：`go test ./...`（日志切割相关的用例依赖 inode，在 Linux 上运行）。

## 结构

```
cmd/liuliobs     主程序（模式：all-in-one / agent / hub）
cmd/loggen       示例日志生成器
internal/tail    跟读日志：inode + 偏移量 + 文件头指纹，处理改名切割和 copytruncate，确认入库后才前进
internal/parse   解析 liuli_json 和 combined 两种格式
internal/classify 蜘蛛识别、浏览器/系统/设备、IP 归属地（ip2region 离线库）、PV 判定
internal/ingest  采集调度：读 → 解析 → 入库（明细、进度、待汇总标记在同一事务），定时汇总和清理
internal/store   SQLite（WAL）：明细 req、小时/天聚合、维度日表、设置、账号、会话
internal/api     查询接口、登录（bcrypt + HttpOnly Cookie + 限流）、安全响应头、嵌入的前端
web/             前端（Vite + TypeScript + ECharts，无框架）
scripts/verify.py 独立的数字核对脚本
```

## 数据口径

和需求说明第 5 节一致，界面每个指标旁的问号里也写了：

| 指标 | 口径 |
|---|---|
| 请求 | 所有日志行 |
| 浏览量 PV | 非蜘蛛、2xx/3xx、GET、页面类请求（排除静态资源扩展名、`/api/` 等前缀、`/robots.txt` 等路径，可配置） |
| 访客 UV | 当天按 (IP + UA) 去重，非蜘蛛；多天为每天之和 |
| IP 数 | 当天去重 IP；多天为每天之和 |
| 流量 | `bytes_sent` 之和 |
| 蜘蛛爬虫 | UA 命中已知蜘蛛 / 机器人列表（含 AI 爬虫、SEO 工具、脚本工具、空 UA） |
| 10 分钟活跃 | 最近 10 分钟去重访客 |

## 存储

- `req` 明细：保留 7 天（可配），用于明细查询、按分钟趋势、今日统计。
- `agg_hour` / `agg_day`：从明细重算（幂等），保留 1 年。写入明细时同一事务标记 `dirty` 小时，汇总任务每 15 秒处理一次。
- `dim_day`：每天结束后生成各维度（IP、地区、浏览器、系统、设备、蜘蛛、蜘蛛 URL、页面、URI、来源、状态码）的日表，高基数维度每天保留前 2000 名；今天的维度实时从明细算。
- 所有表都带 `server_id` 和 `site`。

## 多台服务器（第二版）

数据模型和接口已经按多台设计，第一版只交付 `all-in-one`：

- `agent` 模式：复用 `internal/tail` + `internal/parse` + `internal/classify`，把**已结束的分钟**的全部明细打包成 `model.PushBatch`，`POST /api/push`（`Authorization: Bearer <token>`）推给 Hub，失败重试，成功后才前进本地进度。
- `hub` 模式：校验 Token（`servers.token_hash`，只存哈希），按 `(server_id, site, minute)` 幂等写入：先删该分钟已有明细再写入，然后标记 `dirty`，之后的汇总、查询、页面与单台完全相同。
- 界面：顶部筛选条加「服务器」下拉（接口已支持 `server` 参数，空表示汇总）。

站长的量级下全量推送明细（每天约 10 万行、压缩后几 MB）比只推聚合更简单，口径也完全一致。

## 第二版预留

- 封禁 IP：`bans` 表、`GET/POST/DELETE /api/bans` 接口（目前返回 501）、侧栏「封禁 IP」入口、IP 列表 / 错误日志 / 慢请求每行的「封禁」按钮（目前禁用）都已就位。生效方式计划为生成 deny 文件给 OpenResty `include` 后重载，重载方式需和站长确认权限后再做。
- 告警、自动封禁、大屏、假蜘蛛识别（反向 DNS 校验）。
