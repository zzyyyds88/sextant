package engine

import (
	"html"
	"sort"
	"strings"
)

// docGroup is one section of the generated /doc page. The endpoints come from
// apiPaths, so the page cannot drift from the routes the daemon actually wires.
type docGroup struct {
	ID        string
	Title     string
	Intro     string
	Endpoints []docEndpoint
}

type docEndpoint struct {
	Method  string
	Path    string
	Summary string
	Auth    string
}

// docGroupOrder is the reading order. Paths that match no prefix fall into the
// final "其他" group rather than disappearing.
var docGroupOrder = []struct {
	id, title, intro string
}{
	{"start", "文档与鉴权", "这些端点不需要 bearer token；先读本页和 /doc/skill，再按 /doc 的说明完成配对。"},
	{"status", "状态与健康", "确认守护活着、当前发布的状态对象和内存指标。"},
	{"device", "设备与屏幕", "锁屏、解锁、屏幕、旋转、Wi-Fi、飞行模式和保持唤醒。"},
	{"input", "输入与 IME", "点击、滑动、按键、文字注入和输入法租约。"},
	{"ui", "感知与操作", "看屏、树、OCR、动作、读回、等待、验证码分流和地图原语。"},
	{"session", "会话与 HUD", "claim/release、心跳、reaper、HUD 状态和安全停手。"},
	{"apps", "应用、文件与设置", "启动/停止应用、文件往返、日常设置和上传。"},
	{"info", "通知、短信、定位与音频", "读取通知/短信/位置，控制麦克风和音频路由。"},
	{"media", "显示、串流与触发器", "虚拟屏、WebRTC、通知流和触发器。"},
	{"other", "其他", "未归类的已接线端点；以本表和 /doc/openapi.json 为准。"},
}

func docGroupFor(path string) string {
	switch {
	case path == "/doc" || strings.HasPrefix(path, "/doc/") || path == "/openapi.json" ||
		path == "/v1/health" || path == "/v1/pair":
		return "start"
	case path == "/v1/status" || path == "/v1/watchdog" || path == "/v1/metrics":
		return "status"
	case strings.HasPrefix(path, "/v1/device/"):
		return "device"
	case strings.HasPrefix(path, "/v1/input/"):
		return "input"
	case strings.HasPrefix(path, "/v1/ui/") || strings.HasPrefix(path, "/v1/challenge/"):
		return "ui"
	case strings.HasPrefix(path, "/v1/sessions") || strings.HasPrefix(path, "/v1/roster") ||
		strings.HasPrefix(path, "/v1/guard/") || strings.HasPrefix(path, "/v1/hud"):
		return "session"
	case strings.HasPrefix(path, "/v1/apps") || strings.HasPrefix(path, "/v1/files") ||
		strings.HasPrefix(path, "/v1/uploads") || strings.HasPrefix(path, "/v1/settings"):
		return "apps"
	case strings.HasPrefix(path, "/v1/notifications") || strings.HasPrefix(path, "/v1/sms") ||
		strings.HasPrefix(path, "/v1/location") || strings.HasPrefix(path, "/v1/audio"):
		return "info"
	case strings.HasPrefix(path, "/v1/displays") || strings.HasPrefix(path, "/v1/stream") ||
		strings.HasPrefix(path, "/v1/triggers"):
		return "media"
	default:
		return "other"
	}
}

func docAuth(path, method string) string {
	key := method + " " + path
	if publicOperations[key] {
		return "公开"
	}
	switch {
	case strings.HasPrefix(path, "/v1/hud"):
		return "HUD token（回环）"
	case path == "/v1/device/unlock-local":
		return "App token（回环）"
	case method == "get":
		return "Bearer"
	default:
		return "Bearer + 会话"
	}
}

func buildDocGroups() []docGroup {
	groups := make([]docGroup, 0, len(docGroupOrder))
	index := map[string]int{}
	for i, g := range docGroupOrder {
		groups = append(groups, docGroup{ID: g.id, Title: g.title, Intro: g.intro})
		index[g.id] = i
	}
	paths := make([]string, 0, len(apiPaths))
	for p := range apiPaths {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, path := range paths {
		gid := docGroupFor(path)
		gi, ok := index[gid]
		if !ok {
			gi = index["other"]
		}
		methods := make([]string, 0, len(apiPaths[path]))
		for m := range apiPaths[path] {
			methods = append(methods, strings.ToUpper(m))
		}
		sort.Strings(methods)
		for _, method := range methods {
			summary := apiPaths[path][strings.ToLower(method)]
			groups[gi].Endpoints = append(groups[gi].Endpoints, docEndpoint{
				Method: method, Path: path, Summary: summary, Auth: docAuth(path, strings.ToLower(method)),
			})
		}
	}
	return groups
}

func renderDocHTML(host string) []byte {
	base := "https://" + host
	var b strings.Builder
	b.WriteString(`<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Sextant API 调用说明</title>
<style>
:root{--bg:#f5f7fa;--panel:#fff;--text:#1f2937;--muted:#64748b;--line:#e2e8f0;--accent:#0f766e;--code:#f1f5f9}
*{box-sizing:border-box}body{margin:0;background:var(--bg);color:var(--text);font-family:ui-sans-serif,system-ui,-apple-system,"Segoe UI",sans-serif;line-height:1.65}
a{color:var(--accent)}code,pre{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}
code{background:var(--code);border-radius:4px;padding:1px 5px}pre{background:#0f172a;color:#e2e8f0;border-radius:8px;padding:14px 16px;overflow-x:auto;line-height:1.5}pre code{background:none;color:inherit;padding:0}
.hero{background:linear-gradient(135deg,#0f766e,#134e4a);color:#fff;padding:34px 20px}.hero .inner{max-width:1100px;margin:0 auto}
.eyebrow{margin:0 0 6px;opacity:.8;font-size:13px;letter-spacing:.08em;text-transform:uppercase}.hero h1{margin:0;font-size:30px}.lead{margin:10px 0 0;max-width:850px;opacity:.92}.hero code{background:rgba(255,255,255,.16);color:#fff}
.meta{margin-top:18px;display:flex;flex-wrap:wrap;gap:8px}.meta span{background:rgba(255,255,255,.12);border:1px solid rgba(255,255,255,.18);border-radius:999px;padding:3px 10px;font-size:13px}
main{max-width:1100px;margin:0 auto;padding:30px 20px 60px}section{margin-bottom:34px}h2{margin:0 0 12px;font-size:22px}h3{margin:0 0 4px;font-size:18px}h4{margin:18px 0 8px;font-size:15px}p{margin:8px 0}ul{margin:8px 0;padding-left:22px}li{margin:4px 0}
table{width:100%;border-collapse:collapse;background:var(--panel);border:1px solid var(--line);border-radius:8px;overflow:hidden}th,td{text-align:left;padding:9px 12px;border-bottom:1px solid var(--line);vertical-align:top}th{background:#f8fafc;font-size:13px;color:var(--muted);font-weight:600}tr:last-child td{border-bottom:none}
.card{background:var(--panel);border:1px solid var(--line);border-radius:10px;padding:18px 20px;margin:14px 0}.card-head p{color:var(--muted);margin:4px 0 0}
.endpoint{margin:14px 0 4px;word-break:break-all}.muted{color:var(--muted)}.tag{display:inline-block;font-size:12px;font-weight:600;border:1px solid var(--line);border-radius:999px;padding:1px 8px;margin-left:6px;vertical-align:2px;color:var(--muted)}
.method{display:inline-block;min-width:58px;text-align:center;font-size:12px;font-weight:700;border-radius:5px;padding:2px 6px;color:#fff}.method.get{background:#0369a1}.method.post{background:#15803d}.method.delete{background:#b91c1c}.method.patch{background:#7c3aed}.method.other{background:#475569}
footer{max-width:1100px;margin:0 auto;padding:0 20px 50px;color:var(--muted)}@media(max-width:700px){.hero h1{font-size:24px}th,td{padding:7px 8px}}
</style>
</head>
<body>
<header class="hero"><div class="inner">
<p class="eyebrow">Sextant · API Reference</p>
<h1>API 调用说明</h1>
<p class="lead">电脑上的 agent 通过 <code>` + html.EscapeString(base) + `</code> 操作手机。先读本页和 <code>` + html.EscapeString(base) + `/doc/skill</code>，按本页完成 Bearer 鉴权，再调用 <code>/v1/*</code>。</p>
<div class="meta"><span>版本 ` + html.EscapeString(Version) + `</span><span>` + html.EscapeString(base) + `</span><span>GET /doc</span></div>
</div></header>
<main>
<section id="basics"><h2>1. 基础约定</h2><ul>
<li>统一入口：<code>` + html.EscapeString(base) + `</code>。手机状态页显示同一地址；agent 用同主机的 <code>/doc</code>。</li>
<li>鉴权：<code>/doc</code>、<code>/doc/skill</code>、<code>/doc/openapi.json</code>、<code>/v1/health</code>、<code>/v1/pair</code> 公开；其余 <code>/v1/*</code> 需要 <code>Authorization: Bearer &lt;token&gt;</code>。</li>
<li>写操作通常再带 <code>X-Sextant-Session: &lt;session-token&gt;</code>；会话来自 <code>POST /v1/sessions</code>。</li>
<li>TLS 是设备自签证书。命令行用 <code>curl -k</code>；需要 pin 时用配对时观察到的证书 SHA-256（CLI 写入本地配置的 <code>cert_sha256</code>，也可用 <code>openssl s_client</code> 复核）。</li>
<li>错误统一是 <code>{"error":{"code","message","detail"}}</code>；HTTP 200 只说明请求被处理，动作是否生效看响应字段和 readback。</li>
</ul></section>
<section id="auth"><h2>2. 鉴权与配对</h2>
<div class="card"><h3>第一次：拿一个 token</h3><p>推荐在电脑上执行一次（凭据只经 stdin）：</p>
<pre><code>printf '%s\\n' '&lt;解锁图案&gt;' | sextant init --serial &lt;adb-serial&gt;
# 或已初始化时只配对：
sextant pair --serial &lt;adb-serial&gt;</code></pre>
<p>没有 CLI 时（adb + root）：</p>
<pre><code>adb -s &lt;adb-serial&gt; shell su -c 'sextant-core ctl pair new --agent pc'
curl -k -X POST -H "Content-Type: application/json" \
  -d '{"code":"&lt;一次性码&gt;","agent":"pc"}' ` + html.EscapeString(base) + `/v1/pair</code></pre>
<p>一次性码 5 分钟有效、只能用一次；响应里的 <code>server_cert_sha256</code> 用于 pin。token 只存本地 0600 文件，不写日志、不发聊天。</p>
<p class="muted">配对成功后把 token 与 <code>/doc/skill</code> 存成本地缓存（token 0600）。之后每次任务直接读本地 skill + token 调用 <code>/v1/*</code>，不要重复读文档、重复初始化；只有设备重置、模块重装或证书变化导致 401 / pin 失败时才重新配对。</p></div>
<div class="card"><h3>之后的每次调用</h3><pre><code>TOKEN='&lt;api-token&gt;'
curl -k -H "Authorization: Bearer $TOKEN" ` + html.EscapeString(base) + `/v1/status</code></pre>
<p>token 是长期凭据；失效（设备重置/模块重装）后重新配对。手机上的 <code>app-token</code> 是 APK 自己用的回环 token，不是给外部 agent 的 token。</p></div>
</section>
<section id="quickstart"><h2>3. 最小调用流程</h2>
<pre><code># 1) 存活与鉴权
curl -k ` + html.EscapeString(base) + `/v1/health
curl -k -H "Authorization: Bearer $TOKEN" ` + html.EscapeString(base) + `/v1/status

# 2) 看当前页
curl -k -H "Authorization: Bearer $TOKEN" ` + html.EscapeString(base) + `/v1/ui/page

# 3) 占一个写会话（同一块屏只能有一个写者）
curl -k -X POST -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \\
  -d '{"agent":"demo","display":0}' ` + html.EscapeString(base) + `/v1/sessions

# 4) 执行并验证；把响应里的 session token 放进 X-Sextant-Session
curl -k -X POST -H "Authorization: Bearer $TOKEN" -H "X-Sextant-Session: $SESSION" \\
  -H "Content-Type: application/json" -d '{"text":"WLAN","action":"click","expect":{"selected":true},"verify_ms":1500}' \\
  ` + html.EscapeString(base) + `/v1/ui/act

# 5) 收尾：释放会话并锁屏
curl -k -X DELETE -H "Authorization: Bearer $TOKEN" ` + html.EscapeString(base) + `/v1/sessions/&lt;id&gt;</code></pre>
<p class="muted">长任务用 <code>POST /v1/sessions/{id}/heartbeat</code> 只保活；业务请求才刷新活动时间。默认 300 秒没有业务操作会被当作僵尸会话回收并锁屏。</p>
</section>
`)
	for _, g := range buildDocGroups() {
		if len(g.Endpoints) == 0 {
			continue
		}
		b.WriteString(`<section id="` + html.EscapeString(g.ID) + `"><h2>` + html.EscapeString(g.Title) + `</h2><p>` + html.EscapeString(g.Intro) + `</p><table><thead><tr><th>方法</th><th>路径</th><th>鉴权</th><th>说明</th></tr></thead><tbody>`)
		for _, e := range g.Endpoints {
			class := strings.ToLower(e.Method)
			if class != "get" && class != "post" && class != "delete" && class != "patch" {
				class = "other"
			}
			b.WriteString(`<tr><td><span class="method ` + class + `">` + html.EscapeString(e.Method) + `</span></td><td><code>` + html.EscapeString(e.Path) + `</code></td><td>` + html.EscapeString(e.Auth) + `</td><td>` + html.EscapeString(e.Summary) + `</td></tr>`)
		}
		b.WriteString(`</tbody></table></section>`)
	}
	b.WriteString(`<section id="errors"><h2>4. 常见错误</h2><table><thead><tr><th>code</th><th>含义</th><th>处理</th></tr></thead><tbody>
<tr><td><code>session_conflict</code></td><td>同一块屏已有写会话</td><td>先 release 或换 display；不要抢写</td></tr>
<tr><td><code>handed_over</code></td><td>人已暂停/接管</td><td>停止注入；等 <code>handover --return</code></td></tr>
<tr><td><code>target_not_found</code> / <code>target_disabled</code></td><td>目标不存在或不可点</td><td>重新 page/tree，换节点或停止</td></tr>
<tr><td><code>no_effect</code> / <code>verify_timeout</code></td><td>已注入但界面没达到预期</td><td>不要重复点；读 tree/OCR 后改路径</td></tr>
<tr><td><code>offscreen</code></td><td>目标不在可视区域</td><td>滚动或换入口，不要盲点</td></tr>
<tr><td><code>challenge_unresolved</code></td><td>滑块没解开</td><td>停止；交给 handover</td></tr>
<tr><td><code>credential_missing</code></td><td>没有解锁凭据</td><td>由持机人录入，不要猜图案</td></tr>
<tr><td><code>frame_not_found</code></td><td>diff 的帧已过期/不存在</td><td>重新 observe 取新 frame_id</td></tr>
<tr><td><code>unsupported</code></td><td>该 ROM/状态不提供此能力</td><td>如实上报，不要做只读兜底</td></tr>
</tbody></table></section>
<section id="links"><h2>5. 相关入口</h2><ul>
<li><a href="/doc/skill">/doc/skill</a>：agent 操作手册（Markdown）。</li>
<li><a href="/doc/skill.zip">/doc/skill.zip</a>：可安装 skill 包。</li>
<li><a href="/doc/openapi.json">/doc/openapi.json</a>：完整 OpenAPI 3.1（公开）。</li>
<li><a href="/openapi.json">/openapi.json</a>：同上的短别名。</li>
<li><a href="/ui/">/ui/</a>：人类用的 WebUI（需要 app token）。</li>
</ul></section>
</main><footer>Sextant ` + html.EscapeString(Version) + ` · 本页由守护根据当前路由生成 · 不需要鉴权</footer>
</body></html>`)
	return []byte(b.String())
}
