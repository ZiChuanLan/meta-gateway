package httpapi

import (
	"net/http"
)

// Root landing page: a slim brand surface served at "/" so the root URL is
// never a bare 404. Styled to match the admin connect screen (dark, squared,
// mono accents) and probes /readyz live to show gateway health.
const landingPageHTML = `<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8" />
<meta name="viewport" content="width=device-width, initial-scale=1.0" />
<meta name="color-scheme" content="dark" />
<title>Meta Gateway</title>
<style>
  :root {
    --bg: #141210;
    --panel: #1b1815;
    --line: rgba(247, 242, 234, 0.1);
    --ink: #f4efe7;
    --muted: #a39a8e;
    --accent: #7ea0d8;
    --ok: #46c98a;
    --warn: #d09b35;
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  html, body { height: 100%; }
  body {
    background:
      radial-gradient(900px 420px at 15% -10%, rgba(126, 160, 216, 0.12), transparent 60%),
      radial-gradient(700px 380px at 88% -12%, rgba(126, 160, 216, 0.08), transparent 55%),
      var(--bg);
    color: var(--ink);
    font-family: Inter, "Segoe UI", "PingFang SC", "Microsoft YaHei", system-ui, sans-serif;
    display: grid;
    place-items: center;
    padding: 24px;
    -webkit-font-smoothing: antialiased;
  }
  .card {
    width: min(520px, 100%);
    background: var(--panel);
    border: 1px solid var(--line);
    border-radius: 2px;
    box-shadow: 0 24px 64px rgba(0, 0, 0, 0.45);
    overflow: hidden;
  }
  .brand {
    display: flex;
    align-items: center;
    gap: 12px;
    padding: 20px 24px;
    border-bottom: 1px solid var(--line);
  }
  .mark {
    width: 36px; height: 36px;
    display: grid; place-items: center;
    background: #2a3348;
    border: 1px solid rgba(247, 242, 234, 0.14);
    border-radius: 2px;
    color: #f7f2ea;
    font-weight: 700;
    font-size: 13px;
    letter-spacing: 0.02em;
  }
  .brand-copy { display: flex; flex-direction: column; gap: 2px; }
  .brand-copy strong { font-size: 15px; font-weight: 650; letter-spacing: 0.01em; }
  .brand-copy span { color: var(--muted); font-size: 11px; letter-spacing: 0.08em; text-transform: uppercase; }
  .body { padding: 26px 24px 24px; }
  h1 { font-size: 21px; font-weight: 650; letter-spacing: -0.02em; margin-bottom: 8px; }
  p.lead { color: var(--muted); font-size: 13px; line-height: 1.6; margin-bottom: 20px; }
  .status {
    display: flex;
    align-items: center;
    gap: 10px;
    padding: 12px 14px;
    background: rgba(247, 242, 234, 0.04);
    border: 1px solid var(--line);
    border-radius: 2px;
    margin-bottom: 20px;
  }
  .dot {
    width: 9px; height: 9px; flex: 0 0 auto;
    border-radius: 50%;
    background: var(--warn);
    box-shadow: 0 0 0 4px rgba(208, 155, 53, 0.12);
    transition: background 200ms ease, box-shadow 200ms ease;
  }
  .dot.ok { background: var(--ok); box-shadow: 0 0 0 4px rgba(70, 201, 138, 0.12); }
  .dot.err { background: #e06a60; box-shadow: 0 0 0 4px rgba(224, 106, 96, 0.12); }
  .status-copy { display: flex; flex-direction: column; gap: 1px; min-width: 0; }
  .status-copy strong { font-size: 12px; font-weight: 600; }
  .status-copy span { color: var(--muted); font-size: 11px; font-family: "JetBrains Mono", "IBM Plex Mono", Consolas, monospace; }
  .actions { display: flex; gap: 10px; margin-bottom: 22px; }
  .btn {
    height: 40px;
    padding: 0 18px;
    display: inline-flex;
    align-items: center;
    justify-content: center;
    gap: 8px;
    border-radius: 2px;
    border: 1px solid transparent;
    font-size: 13px;
    font-weight: 600;
    text-decoration: none;
    transition: background 140ms ease, border-color 140ms ease;
  }
  .btn-primary { background: #2f4f86; color: #fff; }
  .btn-primary:hover { background: #3a5f9c; }
  .btn-secondary { background: transparent; color: var(--ink); border-color: var(--line); }
  .btn-secondary:hover { background: rgba(247, 242, 234, 0.06); }
  .endpoints { border-top: 1px solid var(--line); padding-top: 16px; }
  .endpoints h2 {
    font-size: 10px; font-weight: 700; letter-spacing: 0.14em;
    color: var(--muted); text-transform: uppercase; margin-bottom: 10px;
  }
  .endpoint {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    padding: 7px 0;
    font-size: 12px;
    border-bottom: 1px solid rgba(247, 242, 234, 0.05);
  }
  .endpoint:last-child { border-bottom: 0; }
  .endpoint code {
    font-family: "JetBrains Mono", "IBM Plex Mono", Consolas, monospace;
    color: var(--accent);
    font-size: 11.5px;
  }
  .endpoint span { color: var(--muted); font-size: 11px; }
  .foot {
    padding: 14px 24px;
    border-top: 1px solid var(--line);
    color: var(--muted);
    font-size: 10px;
    letter-spacing: 0.06em;
    display: flex;
    justify-content: space-between;
    gap: 8px;
  }
  .foot em { font-style: normal; opacity: 0.7; }
</style>
</head>
<body>
  <main class="card">
    <header class="brand">
      <div class="mark" aria-hidden="true">MG</div>
      <div class="brand-copy">
        <strong>Meta Gateway</strong>
        <span>OpenAI-Compatible Relay</span>
      </div>
    </header>
    <div class="body">
      <h1>多通道 API 中继网关</h1>
      <p class="lead">统一接入多个上游渠道，模型路由、失败重试、密钥加密、用量审计与在线备份。</p>
      <div class="status" role="status">
        <span class="dot" id="dot" aria-hidden="true"></span>
        <div class="status-copy">
          <strong id="status-label">正在检测网关状态…</strong>
          <span id="status-detail">GET /readyz</span>
        </div>
      </div>
      <div class="actions">
        <a class="btn btn-primary" href="/console/">进入管理控制台</a>
        <a class="btn btn-secondary" href="/healthz">健康检查</a>
      </div>
      <section class="endpoints">
        <h2>Endpoints</h2>
        <div class="endpoint"><code>/console/</code><span>管理控制台</span></div>
        <div class="endpoint"><code>/healthz · /readyz</code><span>健康检查</span></div>
        <div class="endpoint"><code>/metrics</code><span>Prometheus 指标</span></div>
      </section>
    </div>
    <footer class="foot">
      <span>META GATEWAY</span>
      <em>MULTI-CHANNEL ROUTING · RETRY · AUDIT</em>
    </footer>
  </main>
  <script>
    (function () {
      var dot = document.getElementById("dot");
      var label = document.getElementById("status-label");
      var detail = document.getElementById("status-detail");
      function probe() {
        fetch("/readyz", { cache: "no-store" })
          .then(function (r) {
            dot.className = "dot" + (r.ok ? " ok" : " err");
            label.textContent = r.ok ? "网关运行正常" : "网关未就绪";
            detail.textContent = "GET /readyz → " + r.status;
          })
          .catch(function () {
            dot.className = "dot err";
            label.textContent = "无法连接网关";
            detail.textContent = "GET /readyz → network error";
          });
      }
      probe();
      setInterval(probe, 15000);
    })();
  </script>
</body>
</html>
`

func handleLanding(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write([]byte(landingPageHTML))
}
