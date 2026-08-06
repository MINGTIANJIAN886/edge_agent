package profile

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/user/agent/internal/mqttstats"
)

// WebServer serves a real-time capability dashboard:
//
//	GET  /           HTML dashboard
//	GET  /profile    current profile as JSON
//	GET  /device     detailed device hardware/software info as JSON
//	GET  /events     Server-Sent Events stream (pushes on each probe)
//	POST /probe?cap=camera&force=1   trigger a probe
//	POST /cam/start  start the live MJPEG camera stream
//	POST /cam/stop   stop the live camera stream
//	GET  /cam/stream live MJPEG stream (multipart/x-mixed-replace)
//	GET  /cam/snapshot  latest JPEG frame
type WebServer struct {
	mgr      *ProbeManager
	cam      *CameraStream
	task     *TaskTracker
	stats    *mqttstats.Tracker
	DeviceID string
}

func NewWebServer(mgr *ProbeManager, deviceID string, cam *CameraStream, task *TaskTracker, stats *mqttstats.Tracker) *WebServer {
	return &WebServer{mgr: mgr, cam: cam, task: task, stats: stats, DeviceID: deviceID}
}

func (w *WebServer) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", w.handleIndex)
	mux.HandleFunc("/profile", w.handleProfile)
	mux.HandleFunc("/device", w.handleDevice)
	mux.HandleFunc("/events", w.handleEvents)
	mux.HandleFunc("/probe", w.handleProbe)
	mux.HandleFunc("/cam/start", w.handleCamStart)
	mux.HandleFunc("/cam/stop", w.handleCamStop)
	mux.HandleFunc("/cam/stream", w.handleCamStream)
	mux.HandleFunc("/cam/snapshot", w.handleCamSnapshot)
	return mux
}

func (w *WebServer) handleIndex(rw http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(rw, r)
		return
	}
	rw.Header().Set("Content-Type", "text/html; charset=utf-8")
	rw.Write([]byte(indexHTML))
}

func (w *WebServer) handleProfile(rw http.ResponseWriter, r *http.Request) {
	rw.Header().Set("Content-Type", "application/json")
	out := map[string]interface{}{
		"device_id": w.DeviceID,
		"capabilities": w.mgr.All(),
	}
	if w.task != nil {
		out["task"] = w.task.Current()
		model := w.task.Model()
		if model.Model != "" || model.Version != "" {
			out["model"] = model
		}
	}
	if w.stats != nil {
		s := w.stats.Sample()
		n := len(s.In)
		curIn, curOut := int64(0), int64(0)
		if n > 0 {
			curIn, curOut = s.In[n-1], s.Out[n-1]
		}
		out["mqtt_stats"] = map[string]interface{}{
			"current_in_bps":  curIn,
			"current_out_bps": curOut,
			"total_in_bytes":  s.TotalIn,
			"total_out_bytes": s.TotalOut,
			"window_seconds":  n,
		}
	}
	json.NewEncoder(rw).Encode(out)
}

// handleDevice returns the detailed device parameters: hardware,
// software and runtime sections collected by the device probe.
// Serves a styled HTML page for browsers, JSON otherwise.
func (w *WebServer) handleDevice(rw http.ResponseWriter, r *http.Request) {
	if strings.Contains(r.Header.Get("Accept"), "text/html") {
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		rw.Write([]byte(deviceHTML))
		return
	}
	res, ok := w.mgr.Get("device")
	if !ok {
		http.Error(rw, `{"error":"device probe result not available yet"}`, http.StatusServiceUnavailable)
		return
	}
	out := map[string]interface{}{
		"device_id":  w.DeviceID,
		"probed_at":  res.TestedAt,
		"latency_ms": res.LatencyMS,
	}
	if sw, ok := res.Details["software"].(map[string]interface{}); ok {
		out["agent_version"] = sw["agent_version"]
	}
	for _, section := range []string{"hardware", "software", "runtime"} {
		if v, ok := res.Details[section].(map[string]interface{}); ok {
			out[section] = v
		}
	}
	rw.Header().Set("Content-Type", "application/json")
	json.NewEncoder(rw).Encode(out)
}

func (w *WebServer) handleCamStart(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if w.cam == nil {
		http.Error(rw, `{"error":"camera stream not configured"}`, http.StatusServiceUnavailable)
		return
	}
	if err := w.cam.Start(); err != nil {
		http.Error(rw, `{"error":"`+err.Error()+`"}`, http.StatusInternalServerError)
		return
	}
	rw.WriteHeader(http.StatusOK)
	fmt.Fprintln(rw, "camera stream started")
}

func (w *WebServer) handleCamStop(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "POST required", http.StatusMethodNotAllowed)
		return
	}
	if w.cam != nil {
		w.cam.Stop()
	}
	rw.WriteHeader(http.StatusOK)
	fmt.Fprintln(rw, "camera stream stopped")
}

// handleCamStream serves the live MJPEG stream. The stream process is
// started on demand when a client connects and stopped when the last
// client disconnects.
func (w *WebServer) handleCamStream(rw http.ResponseWriter, r *http.Request) {
	if w.cam == nil {
		http.Error(rw, "camera stream not configured", http.StatusServiceUnavailable)
		return
	}
	ch, unsub := w.cam.Subscribe()

	flusher, ok := rw.(http.Flusher)
	if !ok {
		http.Error(rw, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	if !w.cam.Running() {
		if err := w.cam.Start(); err != nil && !w.cam.Running() {
			http.Error(rw, "camera unavailable: "+err.Error(), http.StatusServiceUnavailable)
			return
		}
	}
	defer func() {
		unsub()
		if w.cam.NumSubscribers() == 0 {
			w.cam.Stop()
		}
	}()

	rw.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=frame")
	rw.Header().Set("Cache-Control", "no-cache")

	deadline := time.NewTicker(10 * time.Second)
	defer deadline.Stop()

	for {
		select {
		case frame := <-ch:
			if frame == nil {
				// stream process died; the supervisor auto-restarts it
				// and frames will resume on this channel
				continue
			}
			fmt.Fprintf(rw, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: %d\r\n\r\n", len(frame))
			rw.Write(frame)
			fmt.Fprint(rw, "\r\n")
			flusher.Flush()
		case <-deadline.C:
			// keep the connection alive even if frames stall
			fmt.Fprint(rw, "--frame\r\nContent-Type: image/jpeg\r\nContent-Length: 0\r\n\r\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (w *WebServer) handleCamSnapshot(rw http.ResponseWriter, r *http.Request) {
	if w.cam == nil {
		http.Error(rw, "camera stream not configured", http.StatusServiceUnavailable)
		return
	}
	frame := w.cam.Snapshot()
	if frame == nil && !w.cam.Running() {
		http.Error(rw, `{"error":"no frame yet, start the stream first"}`, http.StatusNotFound)
		return
	}
	// the stream process is running but its first frame may still be on
	// the way (camera open can take a few seconds); wait for it briefly
	deadline := time.Now().Add(5 * time.Second)
	for frame == nil && time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
		frame = w.cam.Snapshot()
	}
	if frame == nil {
		http.Error(rw, `{"error":"no frame captured yet"}`, http.StatusNotFound)
		return
	}
	rw.Header().Set("Content-Type", "image/jpeg")
	rw.Header().Set("Cache-Control", "no-store")
	rw.Write(frame)
}

func (w *WebServer) handleProbe(rw http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(rw, "POST required", http.StatusMethodNotAllowed)
		return
	}
	capName := r.URL.Query().Get("cap")
	force := r.URL.Query().Get("force") == "1"
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		if capName == "" {
			w.mgr.ProbeAll(ctx, nil, force)
		} else {
			w.mgr.Probe(ctx, capName, force)
		}
	}()
	rw.WriteHeader(http.StatusAccepted)
	fmt.Fprintf(rw, "probe triggered: %s\n", capName)
}

func (w *WebServer) handleEvents(rw http.ResponseWriter, r *http.Request) {
	flusher, ok := rw.(http.Flusher)
	if !ok {
		http.Error(rw, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	ch := make(chan struct{}, 1)
	taskCh := make(chan struct{}, 1)
	w.mgr.Subscribe(ch)
	defer w.mgr.Unsubscribe(ch)
	if w.task != nil {
		w.task.Subscribe(taskCh)
		defer w.task.Unsubscribe(taskCh)
	}

	rw.Header().Set("Content-Type", "text/event-stream")
	rw.Header().Set("Cache-Control", "no-cache")
	rw.Header().Set("Connection", "keep-alive")

	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	send := func() {
		out := map[string]interface{}{
			"device_id": w.DeviceID,
			"timestamp": time.Now().Unix(),
			"caps":      w.mgr.All(),
		}
		if w.task != nil {
			out["task"] = w.task.Current()
			model := w.task.Model()
			if model.Model != "" || model.Version != "" {
				out["model"] = model
			}
		}
		if w.stats != nil {
			out["stats"] = w.stats.Sample()
		}
		data, err := json.Marshal(out)
		if err != nil {
			return
		}
		fmt.Fprintf(rw, "data: %s\n\n", data)
		flusher.Flush()
	}

	send()
	for {
		select {
		case <-ch:
			send()
		case <-taskCh:
			send()
		case <-ticker.C:
			send()
		case <-r.Context().Done():
			return
		}
	}
}

const deviceHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>设备详情 - Edge Agent</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif;
         background: linear-gradient(160deg, #0f172a 0%, #111c33 60%, #0b1226 100%);
         color: #e2e8f0; min-height: 100vh; }
  .wrap { max-width: 960px; margin: 0 auto; padding: 32px 20px 48px; }

  header { display: flex; align-items: center; justify-content: space-between;
           flex-wrap: wrap; gap: 12px; margin-bottom: 28px; }
  header h1 { font-size: 22px; font-weight: 600; display: flex; align-items: center; gap: 10px; }
  .chip { display: inline-flex; align-items: center; gap: 8px; background: #1e293b;
          border: 1px solid #334155; border-radius: 999px; padding: 6px 14px; font-size: 13px; }
  .dot { width: 8px; height: 8px; border-radius: 50%; background: #22c55e; box-shadow: 0 0 8px #22c55e88; }
  .chip.dev { color: #7dd3fc; }

  .card { background: rgba(30,41,59,.75); backdrop-filter: blur(6px); border: 1px solid #334155;
          border-radius: 16px; padding: 20px; margin-bottom: 20px; }
  .card h2 { font-size: 14px; font-weight: 600; color: #94a3b8; text-transform: uppercase;
             letter-spacing: .08em; margin-bottom: 16px; display: flex; align-items: center; gap: 8px; }
  .card h2 .ico { font-size: 16px; }

  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(200px, 1fr)); gap: 12px; }
  .tile { background: #16233d; border: 1px solid #24354f; border-radius: 12px; padding: 14px 16px; }
  .tile .label { font-size: 12px; color: #7c8db0; margin-bottom: 6px; }
  .tile .value { font-size: 15px; font-weight: 500; color: #e2e8f0; word-break: break-all; }
  .tile .value small { font-size: 12px; color: #7c8db0; font-weight: 400; }

  table { width: 100%; border-collapse: collapse; }
  td { padding: 10px 8px; border-bottom: 1px dashed #24354f; font-size: 14px; }
  tr:last-child td { border-bottom: none; }
  td.k { color: #7c8db0; width: 30%; }
  td.v { color: #e2e8f0; word-break: break-all; text-align: right; }
  td.v .empty { color: #475569; font-style: italic; }

  footer { display: flex; align-items: center; justify-content: space-between;
           color: #64748b; font-size: 12px; margin-top: 8px; }
  footer button { background: #334155; color: #e2e8f0; border: 1px solid #475569;
                  border-radius: 8px; padding: 8px 18px; font-size: 13px; cursor: pointer; }
  footer button:hover { background: #475569; }
  footer button:disabled { opacity: .5; cursor: wait; }

  .load { text-align: center; color: #64748b; padding: 60px 0; font-size: 14px; }
  @media (max-width: 560px) { .grid { grid-template-columns: 1fr 1fr; } }
</style>
</head>
<body>
<div class="wrap">
  <header>
    <h1>🖥️ 设备详情</h1>
    <div style="display:flex; gap:8px; flex-wrap:wrap;">
      <span class="chip dev" id="chipDevice">--</span>
      <span class="chip"><span class="dot"></span><span id="chipStatus">探测中</span></span>
    </div>
  </header>

  <div id="loading" class="load">正在加载设备信息...</div>
  <main id="main" style="display:none;">

    <section class="card">
      <h2><span class="ico">⚙️</span> 硬件参数</h2>
      <div class="grid" id="hwGrid"></div>
    </section>

    <section class="card">
      <h2><span class="ico">🧩</span> 软件环境</h2>
      <table id="swTable"></table>
    </section>

    <section class="card">
      <h2><span class="ico">📊</span> 运行时状态</h2>
      <div class="grid" id="rtGrid"></div>
    </section>

    <footer>
      <span>最近探测: <span id="probedAt">--</span> · 每 10 秒自动刷新</span>
      <button id="refresh" onclick="load(true)">立即刷新</button>
    </footer>
  </main>
</div>
<script>
const HARDWARE = [
  ['device_type','设备型号'],
  ['architecture','架构'],
  ['cpu_cores','CPU 核心数'],
  ['memory_mb','内存'],
  ['gpu','GPU'],
  ['disk','磁盘'],
  ['hostname','主机名'],
];
const RUNTIME = [
  ['uptime','运行时长'],
  ['load','负载'],
];
const SOFTWARE = [
  ['os','操作系统'],
  ['kernel','内核版本'],
  ['cuda','CUDA'],
  ['tensorrt','TensorRT'],
  ['python','Python'],
  ['ros','ROS'],
  ['ros_distro','ROS 发行版'],
  ['agent_version','Agent 版本'],
];
const esc = s => (s ?? '').toString().replace(/[&<>"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));
function tile(label, value) {
  return '<div class="tile"><div class="label">' + esc(label) + '</div><div class="value">' + (value ? esc(value) : '<small>未检测到</small>') + '</div></div>';
}
function row(k, v) {
  return '<tr><td class="k">' + esc(k) + '</td><td class="v">' + (v ? esc(v) : '<span class="empty">未安装 / 未检测到</span>') + '</td></tr>';
}
async function load(manual) {
  const btn = document.getElementById('refresh');
  if (manual) { btn.disabled = true; btn.textContent = '刷新中...'; }
  try {
    const r = await fetch('/device', { headers: { 'Accept': 'application/json' } });
    if (!r.ok) throw new Error('HTTP ' + r.status);
    const d = await r.json();
    document.getElementById('loading').style.display = 'none';
    document.getElementById('main').style.display = 'block';
    document.getElementById('chipDevice').textContent = d.device_id + ' · ' + (d.hardware?.hostname || '');
    document.getElementById('chipStatus').textContent = '在线 · agent ' + (d.software?.agent_version || '--');
    document.getElementById('probedAt').textContent = d.probed_at ? new Date(d.probed_at).toLocaleString() : '--';

    document.getElementById('hwGrid').innerHTML =
      HARDWARE.map(([k, label]) => tile(label, d.hardware?.[k])).join('');
    document.getElementById('rtGrid').innerHTML =
      RUNTIME.map(([k, label]) => tile(label, d.runtime?.[k])).join('');
    document.getElementById('swTable').innerHTML =
      SOFTWARE.map(([k, label]) => row(label, d.software?.[k])).join('');
  } catch (e) {
    document.getElementById('loading').style.display = 'block';
    document.getElementById('loading').textContent = '加载失败: ' + e.message + ' (设备探测可能尚未完成)';
    document.getElementById('main').style.display = 'none';
  } finally {
    if (manual) { btn.disabled = false; btn.textContent = '立即刷新'; }
  }
}
load();
setInterval(() => load(false), 10000);
</script>
</body>
</html>`

const indexHTML = `<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>EDGE.AI 智能体控制台</title>
<style>
  :root {
    --bg0: #01040d; --bg1: #02081a;
    --fg: #b8e4ff; --dim: #5f87a8;
    --cyan: #00e5ff; --blue: #3aa0ff;
    --ok: #2bff88; --warn: #ffb020; --bad: #ff3b5c;
    --line: #123a52; --card: rgba(4, 17, 34, .78);
    --glow: rgba(0, 229, 255, .5);
  }
  * { box-sizing: border-box; margin: 0; padding: 0; }
  html { font-size: 15px; }
  body {
    font-family: "Cascadia Mono", Consolas, "SFMono-Regular", "PingFang SC", "Microsoft YaHei", monospace;
    background:
      radial-gradient(1200px 600px at 72% -10%, rgba(0,229,255,.09), transparent 60%),
      radial-gradient(900px 500px at 8% 110%, rgba(58,160,255,.08), transparent 60%),
      linear-gradient(180deg, #01040d 0%, #02081a 55%, #01040d 100%);
    color: var(--fg); min-height: 100vh; overflow-x: hidden;
  }
  body::before {
    content: ""; position: fixed; inset: 0; pointer-events: none; z-index: 0;
    background:
      linear-gradient(rgba(0,229,255,.05) 1px, transparent 1px),
      linear-gradient(90deg, rgba(0,229,255,.05) 1px, transparent 1px);
    background-size: 44px 44px;
    -webkit-mask-image: radial-gradient(ellipse at center, rgba(0,0,0,.85), transparent 85%);
    mask-image: radial-gradient(ellipse at center, rgba(0,0,0,.85), transparent 85%);
  }
  body::after {
    content: ""; position: fixed; left: 0; right: 0; height: 200px; top: -200px; z-index: 0;
    pointer-events: none;
    background: linear-gradient(180deg, transparent, rgba(0,229,255,.05) 55%, rgba(0,229,255,.13) 95%, transparent);
    animation: scan 10s linear infinite;
  }
  @keyframes scan { to { transform: translateY(calc(100vh + 400px)); } }
  .wrap { position: relative; z-index: 1; max-width: 1180px; margin: 0 auto; padding: 30px 20px 90px; }

  header { display: flex; align-items: flex-end; justify-content: space-between; flex-wrap: wrap; gap: 14px; }
  .logo { display: flex; align-items: center; gap: 18px; }
  .radar { position: relative; width: 54px; height: 54px; flex-shrink: 0; }
  .radar::before {
    content: ""; position: absolute; inset: 0; border-radius: 50%;
    border: 1px solid rgba(0,229,255,.5);
    box-shadow: 0 0 16px rgba(0,229,255,.35), inset 0 0 16px rgba(0,229,255,.2);
  }
  .radar::after {
    content: ""; position: absolute; inset: 7px; border-radius: 50%;
    border: 1px dashed rgba(0,229,255,.35); animation: spin 14s linear infinite;
  }
  .radar .core {
    position: absolute; left: 50%; top: 50%; width: 8px; height: 8px; margin: -4px 0 0 -4px;
    border-radius: 50%; background: var(--cyan);
    box-shadow: 0 0 12px var(--cyan), 0 0 26px rgba(0,229,255,.6);
    animation: pulse 1.8s ease-in-out infinite;
  }
  @keyframes spin { to { transform: rotate(360deg); } }
  @keyframes pulse { 0%,100% { transform: scale(1); opacity: 1; } 50% { transform: scale(1.7); opacity: .5; } }
  h1.title {
    font-size: 23px; font-weight: 700; letter-spacing: .22em; color: #e6f7ff;
    text-shadow: 0 0 12px rgba(0,229,255,.55), 0 0 44px rgba(0,229,255,.25);
  }
  h1.title .ai { color: var(--cyan); }
  .sub { color: var(--dim); font-size: 12px; letter-spacing: .16em; margin-top: 5px; }
  .chips { display: flex; gap: 10px; flex-wrap: wrap; }
  .chip {
    display: inline-flex; align-items: center; gap: 9px; padding: 7px 15px; font-size: 12px;
    letter-spacing: .06em; color: var(--fg);
    background: rgba(0,229,255,.04); border: 1px solid var(--line); border-radius: 4px;
    box-shadow: inset 0 0 12px rgba(0,229,255,.06), 0 0 12px rgba(0,229,255,.05);
  }
  .chip.dev { color: #7dd3fc; }
  .dot { width: 8px; height: 8px; border-radius: 50%; display: inline-block; background: var(--dim); }
  .dot.ok { background: var(--ok); box-shadow: 0 0 8px var(--ok); animation: pulse 1.6s ease-in-out infinite; }
  .dot.warn { background: var(--warn); box-shadow: 0 0 8px var(--warn); animation: pulse .9s ease-in-out infinite; }
  .dot.bad { background: var(--bad); box-shadow: 0 0 8px var(--bad); }

  h2.sec {
    font-size: 13px; font-weight: 600; color: var(--cyan); letter-spacing: .28em;
    text-transform: uppercase; margin: 34px 0 14px; display: flex; align-items: center; gap: 10px;
  }
  h2.sec::after { content: ""; flex: 1; height: 1px;
    background: linear-gradient(90deg, rgba(0,229,255,.4), transparent); }

  .row2 { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
  @media (max-width: 860px) { .row2 { grid-template-columns: 1fr; } }

  .card {
    position: relative; background: var(--card); backdrop-filter: blur(6px);
    border: 1px solid var(--line); border-radius: 8px; padding: 20px;
    box-shadow: 0 0 18px rgba(0,229,255,.06), inset 0 0 24px rgba(0,229,255,.03);
  }
  .card::before, .card::after {
    content: ""; position: absolute; width: 12px; height: 12px; pointer-events: none;
    border-color: rgba(0,229,255,.55); border-style: solid;
  }
  .card::before { top: -1px; left: -1px; border-width: 2px 0 0 2px; }
  .card::after { bottom: -1px; right: -1px; border-width: 0 2px 2px 0; }
  .card h3 {
    font-size: 12px; font-weight: 600; color: var(--cyan); margin-bottom: 14px;
    letter-spacing: .18em; text-transform: uppercase; display: flex; align-items: center; gap: 8px;
  }
  .card.live { border-color: rgba(0,229,255,.6); box-shadow: 0 0 26px rgba(0,229,255,.18), inset 0 0 30px rgba(0,229,255,.05); }

  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(160px, 1fr)); gap: 10px; }
  .tile {
    background: rgba(0,229,255,.03); border: 1px solid var(--line); border-radius: 6px;
    padding: 12px 14px; transition: border-color .2s, box-shadow .2s;
  }
  .tile:hover { border-color: rgba(0,229,255,.45); box-shadow: 0 0 12px rgba(0,229,255,.15); }
  .tile .label { font-size: 10px; color: var(--dim); margin-bottom: 5px; letter-spacing: .1em; }
  .tile .value { font-size: 13.5px; font-weight: 500; word-break: break-all; }
  .tile .value small { font-size: 11px; color: var(--dim); font-weight: 400; }

  table { width: 100%; border-collapse: collapse; }
  td { padding: 8px 6px; border-bottom: 1px dashed var(--line); font-size: 12.5px; }
  tr:last-child td { border-bottom: none; }
  td.k { color: var(--dim); width: 34%; letter-spacing: .04em; }
  td.v { text-align: right; word-break: break-all; }
  td.v .empty { color: #2c4a63; font-style: italic; }

  .caps { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 16px; }
  .cap {
    position: relative; background: var(--card); backdrop-filter: blur(6px);
    border: 1px solid var(--line); border-radius: 8px; padding: 0;
    box-shadow: 0 0 18px rgba(0,229,255,.05); transition: border-color .2s, box-shadow .2s;
    align-self: start;
  }
  .cap:hover { border-color: rgba(0,229,255,.45); box-shadow: 0 0 18px rgba(0,229,255,.14); }
  .cap .head {
    display: flex; align-items: center; justify-content: space-between;
    padding: 15px 16px 13px; cursor: pointer; user-select: none;
  }
  .cap .head .name { font-size: 14px; font-weight: 600; letter-spacing: .08em;
    display: flex; align-items: center; gap: 10px; text-transform: uppercase; }
  .cap .arrow { color: var(--dim); font-size: 11px; transition: transform .25s; }
  .cap.open .arrow { transform: rotate(180deg); }
  .cap .capBody { display: none; padding: 0 16px 14px; }
  .cap.open .capBody { display: block; animation: fadein .3s ease; }
  @keyframes fadein { from { opacity: 0; transform: translateY(-4px); } to { opacity: 1; transform: none; } }
  .badge { font-size: 10px; padding: 3px 10px; border-radius: 2px; font-weight: 600; letter-spacing: .12em; }
  .b-ok { background: rgba(43,255,136,.12); color: var(--ok); box-shadow: 0 0 8px rgba(43,255,136,.25) inset; }
  .b-warn { background: rgba(255,176,32,.12); color: var(--warn); box-shadow: 0 0 8px rgba(255,176,32,.25) inset; }
  .b-bad { background: rgba(255,59,92,.12); color: var(--bad); box-shadow: 0 0 8px rgba(255,59,92,.25) inset; }
  .kv { display: flex; justify-content: space-between; font-size: 11.5px; padding: 3px 0;
    border-bottom: 1px dashed var(--line); gap: 10px; }
  .kv:last-child { border-bottom: none; }
  .kv .k { color: var(--dim); flex-shrink: 0; letter-spacing: .05em; }
  .kv .v { text-align: right; word-break: break-all; }
  .err { color: var(--bad); font-size: 11.5px; margin-top: 6px; }
  .msg { color: #7fb8ff; font-size: 11.5px; margin-top: 6px; line-height: 1.5; }
  .cap .btns { margin-top: 12px; }

  button {
    background: rgba(0,229,255,.06); color: var(--fg); border: 1px solid var(--line);
    border-radius: 4px; padding: 7px 14px; font-size: 11.5px; cursor: pointer; letter-spacing: .06em;
    font-family: inherit; transition: all .18s;
  }
  button:hover { background: rgba(0,229,255,.16); border-color: rgba(0,229,255,.55);
    box-shadow: 0 0 12px rgba(0,229,255,.25); }
  button.primary { background: rgba(0,229,255,.18); border-color: rgba(0,229,255,.6); color: #e6f7ff; }
  button.primary:hover { background: rgba(0,229,255,.3); box-shadow: 0 0 18px rgba(0,229,255,.4); }
  button:disabled { opacity: .5; cursor: wait; }

  .statline { font-size: 24px; font-weight: 700; color: var(--cyan);
    text-shadow: 0 0 12px rgba(0,229,255,.5); letter-spacing: .06em; }
  .flash { animation: flash 1.2s ease; }
  @keyframes flash { 0% { text-shadow: 0 0 4px rgba(255,255,255,.4); } 30% { color: #fff; } }

  footer { display: flex; align-items: center; justify-content: space-between;
    color: var(--dim); font-size: 11px; margin-top: 36px; letter-spacing: .06em; }
  .load { text-align: center; color: var(--dim); padding: 80px 0; font-size: 13px; letter-spacing: .2em; }

  /* 开机自检遮罩 */
  #boot {
    position: fixed; inset: 0; z-index: 60; display: flex; flex-direction: column;
    align-items: center; justify-content: center; gap: 20px;
    background: radial-gradient(800px 500px at 50% 42%, rgba(0,229,255,.08), transparent 70%), var(--bg0);
    transition: opacity .7s ease, visibility .7s;
  }
  #boot.off { opacity: 0; visibility: hidden; }
  #boot .t { color: var(--cyan); letter-spacing: .34em; font-size: 15px;
    text-shadow: 0 0 14px rgba(0,229,255,.6); }
  #boot .bar { width: 340px; height: 2px; background: rgba(0,229,255,.15); position: relative; overflow: hidden; }
  #boot .bar::after { content: ""; position: absolute; top: 0; bottom: 0; width: 38%;
    background: linear-gradient(90deg, transparent, var(--cyan), transparent);
    animation: bootbar 1.1s ease-in-out infinite; }
  @keyframes bootbar { from { left: -40%; } to { left: 100%; } }
  #boot .pct { color: var(--dim); font-size: 11px; letter-spacing: .12em; }

  /* 底部状态条 */
  #statusbar {
    position: fixed; left: 0; right: 0; bottom: 0; z-index: 40;
    display: flex; align-items: center; gap: 14px; padding: 9px 20px;
    background: rgba(2,8,26,.92); border-top: 1px solid var(--line);
    box-shadow: 0 -6px 24px rgba(0,0,0,.55), inset 0 1px 0 rgba(0,229,255,.12);
    font-size: 11px; color: var(--dim); letter-spacing: .08em;
  }
  #statusbar .sys { color: var(--ok); text-shadow: 0 0 8px var(--ok); font-weight: 700; }
  #statusbar .txt { flex: 1; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; color: var(--fg); }
  #statusbar .sp { flex: 1; }
  #statusbar .kv2 { color: var(--dim); }
  #statusbar .kv2 b { color: var(--cyan); font-weight: 600; }

  @media (max-width: 560px) {
    .grid { grid-template-columns: 1fr 1fr; }
    h1.title { font-size: 18px; letter-spacing: .14em; }
  }
</style>
</head>
<body>
<div class="wrap">
  <header>
    <div class="logo">
      <div class="radar"><span class="core"></span></div>
      <div>
        <h1 class="title">EDGE.<span class="ai">AI</span> 控制台</h1>
        <div class="sub" id="lastUpdate">WAITING FOR TELEMETRY...</div>
      </div>
    </div>
    <div class="chips">
      <span class="chip dev" id="chipDevice">--</span>
      <span class="chip"><span class="dot" id="connDot"></span><span id="connTxt">CONNECTING...</span></span>
      <button class="primary" onclick="probeAll()">全部重新探测</button>
    </div>
  </header>

  <div id="loading" class="load">正在加载探测数据...</div>
  <div id="main" style="display:none;">

    <h2 class="sec">设备参数</h2>
    <div class="row2">
      <div class="card">
        <h3>硬件参数 / HARDWARE</h3>
        <div class="grid" id="hwGrid"></div>
      </div>
      <div class="card">
        <h3>软件环境 / SOFTWARE</h3>
        <table id="swTable"></table>
      </div>
    </div>

    <h2 class="sec">当前任务 &amp; 所用模型</h2>
    <div class="row2">
      <div class="card">
        <h3>当前执行的任务 / ACTIVE TASK</h3>
        <div class="statline" id="curTask">--</div>
        <div class="msg" id="curTaskSub" style="margin-top:8px;">任务来源于设备收到的推理调用 (detect_objects / run_ocr)</div>
      </div>
      <div class="card">
        <h3>所用模型 / DEPLOYED MODEL</h3>
        <div id="modelInfo">
          <div class="msg">尚未发生过模型自动下载</div>
        </div>
      </div>
    </div>

    <h2 class="sec">MQTT 传输速率</h2>
    <div class="card" id="mqttCard">
      <h3 style="margin-bottom:12px;">最近 60 秒 MQTT 吞吐 (B/s · 青=上行 橙=下行)</h3>
      <div id="mqttSummary" style="font-size:12px; color:#7fb8ff; margin-bottom:10px;">等待数据...</div>
      <canvas id="mqttChart" height="220" style="width:100%; background:rgba(1,4,13,.8); border-radius:6px; border:1px solid var(--line);"></canvas>
    </div>

    <h2 class="sec">能力探测</h2>
    <div class="caps" id="caps"></div>

    <h2 class="sec">实时画面</h2>
    <div class="card" id="camCard">
      <h3 style="margin-bottom:12px;">摄像头实时预览 / LIVE CAMERA FEED
        <span class="badge b-ok" style="display:none;" id="liveTag">● LIVE</span>
      </h3>
      <div style="text-align:center;">
        <img id="camImg" style="max-width:100%; border-radius:6px; background:rgba(1,4,13,.8); display:none; border:1px solid var(--line);"
             alt="camera stream">
        <div id="camOff" style="color:var(--dim); padding:24px 0; font-size:13px; letter-spacing:.1em;">
          摄像头未开启 — 点击下方按钮开始实时画面
        </div>
      </div>
      <div class="btns" style="margin-top:12px; display:flex; gap:8px;">
        <button class="primary" id="camOnBtn" onclick="camOn()">▶ 开启画面</button>
        <button id="camOffBtn" onclick="camOff()" style="display:none;">■ 关闭画面</button>
      </div>
      <div class="msg" id="camMsg" style="margin-top:8px;"></div>
    </div>

    <footer>
      <span>LINK: <span id="connState">--</span> · UPD: <span id="lastUpd">--</span></span>
      <span>SSE LIVE FEED · KEY [R] = 全部重新探测</span>
    </footer>
  </div>
</div>

<div id="boot">
  <div class="radar" style="width:64px;height:64px;"><span class="core"></span></div>
  <div class="t">INITIALIZING EDGE.AI INTERFACE</div>
  <div class="bar"></div>
  <div class="pct" id="bootPct">0%</div>
</div>

<div id="statusbar">
  <span class="sys" id="sysState">●</span>
  <span class="txt" id="statusText">SYSTEM OFFLINE</span>
  <span class="sp"></span>
  <span class="kv2">PKT <b id="pktCount">0</b></span>
  <span class="kv2">UPTIME <b id="bootTxt">--</b></span>
  <span class="kv2" id="clk">--:--:--</span>
</div>

<script>
const HARDWARE = [
  ['device_type','设备型号'],['architecture','架构'],['cpu_cores','CPU 核心数'],
  ['memory_mb','内存'],['gpu','GPU'],['disk','磁盘'],['hostname','主机名'],
];
const RUNTIME = [['uptime','运行时长'],['load','负载']];
const SOFTWARE = [
  ['os','操作系统'],['kernel','内核版本'],['cuda','CUDA'],['tensorrt','TensorRT'],
  ['python','Python'],['ros','ROS'],['ros_distro','ROS 发行版'],['agent_version','Agent 版本'],
];
const CAP_DETAILS = {
  camera:    [['device','设备'],['width','宽度'],['height','高度'],['latency_ms','耗时(ms)']],
  ota:       [['candidate_count','候选数'],['update_available','有更新'],['remote_version','远端版本'],['manifest_url','清单地址']],
  ros:       [['ros_version','ROS'],['distro','发行版']],
  inference: [['url','服务地址'],['status','HTTP']],
};
const esc = s => (s ?? '').toString().replace(/[&<>"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c]));
const tile = (label, value) => '<div class="tile"><div class="label">' + esc(label) + '</div><div class="value">' + (value ? esc(value) : '<small>未检测到</small>') + '</div></div>';
const row = (k, v) => '<tr><td class="k">' + esc(k) + '</td><td class="v">' + (v ? esc(v) : '<span class="empty">未安装</span>') + '</td></tr>';
const say = t => { const el = document.getElementById('statusText'); if (el) el.textContent = t; };
const beep = el => { if (!el) return; el.classList.remove('flash'); void el.offsetWidth; el.classList.add('flash'); };
function clockTick() {
  const el = document.getElementById('clk');
  if (el) el.textContent = new Date().toLocaleTimeString();
}
setInterval(clockTick, 1000); clockTick();
let bootDone = false;
function setBoot(n) {
  document.getElementById('bootPct').textContent = n + '%';
  if (n >= 100 && !bootDone) {
    bootDone = true;
    setTimeout(() => document.getElementById('boot').classList.add('off'), 350);
    say('ALL SYSTEMS ONLINE');
  }
}
let bootN = 0;
const bootTimer = setInterval(() => {
  if (bootDone) { clearInterval(bootTimer); return; }
  bootN = Math.min(bootN + 3 + Math.floor(Math.random() * 7), 92);
  setBoot(bootN);
}, 160);
let PKT = 0;
function render(ev) {
  document.getElementById('loading').style.display = 'none';
  document.getElementById('main').style.display = 'block';
  const caps = ev.caps || {};
  document.getElementById('chipDevice').textContent = ev.device_id;
  document.getElementById('lastUpd').textContent = new Date(ev.timestamp * 1000).toLocaleTimeString();
  document.getElementById('lastUpdate').textContent = 'DEVICE ' + ev.device_id + ' · LAST UPDATE ' + new Date(ev.timestamp * 1000).toLocaleString();
  PKT++;
  document.getElementById('pktCount').textContent = PKT;

  const dev = caps.device?.details || {};
  const hw = dev.hardware || {}, sw = dev.software || {}, rt = dev.runtime || {};
  document.getElementById('hwGrid').innerHTML =
    HARDWARE.map(([k, l]) => tile(l, hw[k])).join('') + RUNTIME.map(([k, l]) => tile(l, rt[k])).join('');
  document.getElementById('swTable').innerHTML = SOFTWARE.map(([k, l]) => row(l, sw[k])).join('');

  const taskEl = document.getElementById('curTask');
  const prevTask = taskEl.textContent;
  taskEl.textContent = ev.task || '未知';
  if (ev.task && prevTask !== ev.task) {
    beep(taskEl);
    say('TASK UPDATE :: ' + ev.task.toUpperCase() + ' / ' + ev.device_id);
  }
  const m = ev.model;
  const modelInfo = document.getElementById('modelInfo');
  if (m && (m.model || m.version)) {
    const rows = [];
    if (m.model) rows.push(['模型文件', m.model]);
    rows.push(['版本', m.version]);
    if (m.model_task) rows.push(['训练任务', m.model_task]);
    if (m.model) {
      rows.push(['准确率', m.accuracy], ['格式', m.format],
        ['延迟', (m.latency_ms || 0) + ' ms'], ['大小', (m.size_mb || 0) + ' MB']);
    }
    if (m.requested_task) rows.push(['OTA 目标任务', m.requested_task]);
    rows.push(['更新时间', new Date(m.updated_at).toLocaleString()]);
    modelInfo.innerHTML = rows.map(([k, v]) => '<div class="kv"><span class="k">' + esc(k) + '</span><span class="v">' + esc(v) + '</span></div>').join('') +
      (!m.model ? '<div class="msg" style="margin-top:6px;">模型文件详情将在下一次 OTA 自动下载后补全</div>' : '');
  } else {
    modelInfo.innerHTML = '<div class="msg">尚未发生过模型自动下载</div>';
  }

  const names = Object.keys(caps).filter(n => n !== 'device').sort();
  const grid = document.getElementById('caps');
  grid.innerHTML = '';
  for (const n of names) {
    const c = caps[n];
    const cls = c.result ? 'b-ok' : (c.supported ? 'b-warn' : 'b-bad');
    const label = c.result ? '正常' : (c.supported ? '降级' : '不可用');
    const dotCls = c.result ? 'ok' : (c.supported ? 'warn' : 'bad');
    const rows = [
      ['状态', c.result ? '可用' : '不可用'],
      ['supported', c.supported], ['available', c.available], ['healthy', c.healthy],
      ['耗时', (c.latency_ms ?? 0) + ' ms'],
      ['探测时间', new Date(c.tested_at).toLocaleString()],
    ];
    const spec = CAP_DETAILS[n] || [];
    for (const [k, l] of spec) {
      if (c.details && c.details[k] !== undefined) rows.push([l, c.details[k]]);
    }
    const tps = c.details && c.details.topics;
    if (tps && typeof tps === 'object' && !Array.isArray(tps)) {
      for (const [t, ok] of Object.entries(tps)) {
        rows.push(['话题 ' + t, ok ? 'True' : 'False']);
      }
      if (Array.isArray(c.details.topics_all)) {
        rows.push(['全部话题', c.details.topics_all.length ? c.details.topics_all.join(', ') : '(无活跃话题)']);
      }
    }
    const card = document.createElement('div');
    card.className = 'cap open';
    card.innerHTML =
      '<div class="head" onclick="toggleCap(this)"><div class="name"><span class="dot ' + dotCls + '"></span>' + esc(n) + '</div>' +
      '<div style="display:flex;align-items:center;gap:10px;"><span class="badge ' + cls + '">' + label + '</span><span class="arrow">▼</span></div></div>' +
      '<div class="capBody">' +
      rows.map(([k, v]) => '<div class="kv"><span class="k">' + esc(k) + '</span><span class="v">' + esc(v) + '</span></div>').join('') +
      (c.error_code ? '<div class="err">' + esc(c.error_code) + '</div>' : '') +
      (c.message ? '<div class="msg">' + esc(c.message) + '</div>' : '') +
      '<div class="btns"><button onclick="probeCap(\'' + esc(n) + '\')">重新探测</button></div>' +
      '</div>';
    grid.appendChild(card);
  }
  if (ev.stats) drawStats(ev.stats);
  setBoot(100);
}
function toggleCap(head) { head.parentElement.classList.toggle('open'); }
const fmtBytes = n => n >= 1048576 ? (n / 1048576).toFixed(2) + ' MB'
              : n >= 1024 ? (n / 1024).toFixed(1) + ' KB' : n + ' B';
function drawStats(s) {
  const cv = document.getElementById('mqttChart');
  const title = document.getElementById('mqttSummary');
  const n = s.in.length;
  let curIn = 0, curOut = 0;
  if (n > 0) { curIn = s.in[n - 1]; curOut = s.out[n - 1]; }
  title.textContent =
    'CURRENT 上行 ' + fmtBytes(curIn) + '/s · 下行 ' + fmtBytes(curOut) + '/s' +
    '      TOTAL 上行 ' + fmtBytes(s.total_in_bytes) + ' · 下行 ' + fmtBytes(s.total_out_bytes);
  if (!cv || typeof cv.getContext !== 'function') return;
  const dpr = window.devicePixelRatio || 1;
  const w = cv.clientWidth, h = cv.clientHeight || 220;
  cv.width = w * dpr; cv.height = h * dpr;
  const ctx = cv.getContext('2d');
  ctx.save(); ctx.scale(dpr, dpr);
  const padL = 46, padR = 8, padT = 8, padB = 20;
  const gw = w - padL - padR, gh = h - padT - padB;
  const maxY = Math.max(1, ...s.in, ...s.out) * 1.1;
  const X = i => n <= 1 ? padL + gw / 2 : padL + gw * i / (n - 1);
  const Y = v => padT + gh - (v / maxY) * gh;
  ctx.clearRect(0, 0, w, h);
  ctx.strokeStyle = '#0e2a40'; ctx.fillStyle = '#4b6a8f'; ctx.font = '10px monospace';
  ctx.lineWidth = 1;
  for (let g = 0; g <= 4; g++) {
    const y = padT + gh * g / 4;
    ctx.beginPath(); ctx.moveTo(padL, y); ctx.lineTo(w - padR, y); ctx.stroke();
    ctx.fillText(Math.round(maxY * (1 - g / 4)) + '', 4, y + 3);
  }
  const plot = (vals, color) => {
    if (vals.length < 1) return;
    const grad = ctx.createLinearGradient(0, padT, 0, padT + gh);
    grad.addColorStop(0, color + '55');
    grad.addColorStop(1, color + '00');
    ctx.beginPath();
    for (let i = 0; i < vals.length; i++) {
      const x = X(i), y = Y(vals[i]);
      i === 0 ? ctx.moveTo(x, y) : ctx.lineTo(x, y);
    }
    ctx.strokeStyle = color; ctx.lineWidth = 2;
    ctx.shadowColor = color; ctx.shadowBlur = 8;
    ctx.stroke();
    ctx.shadowBlur = 0;
    ctx.lineTo(X(vals.length - 1), padT + gh); ctx.lineTo(X(0), padT + gh); ctx.closePath();
    ctx.fillStyle = grad; ctx.fill();
  };
  plot(s.out, '#fb923c');
  plot(s.in, '#00e5ff');
  ctx.fillStyle = '#4b6a8f';
  ctx.fillText('60s', padL, h - 6);
  ctx.fillText('now', w - padR - 14, h - 6);
  ctx.restore();
}
async function probeCap(name) {
  const r = await fetch('/probe?cap=' + encodeURIComponent(name) + '&force=1', { method: 'POST' });
  say('PROBING :: ' + name.toUpperCase());
  return r;
}
async function probeAll() {
  const r = await fetch('/probe?force=1', { method: 'POST' });
  say('FULL DIAGNOSTIC RUN INITIATED');
  return r;
}
function camOn() {
  const img = document.getElementById('camImg');
  img.src = '/cam/stream?t=' + Date.now();
  img.onload = () => {
    document.getElementById('camOff').style.display = 'none';
    document.getElementById('camOnBtn').style.display = 'none';
    document.getElementById('camOffBtn').style.display = '';
    document.getElementById('camMsg').textContent = '';
    document.getElementById('camCard').classList.add('live');
    document.getElementById('liveTag').style.display = '';
    say('LIVE FEED ONLINE');
  };
  img.onerror = () => {
    img.style.display = 'none';
    document.getElementById('camOff').style.display = '';
    document.getElementById('camMsg').textContent = '无法打开摄像头(可能被占用或未连接)';
  };
  img.style.display = '';
}
function camOff() {
  const img = document.getElementById('camImg');
  img.src = '';
  img.style.display = 'none';
  document.getElementById('camOff').style.display = '';
  document.getElementById('camOnBtn').style.display = '';
  document.getElementById('camOffBtn').style.display = 'none';
  document.getElementById('camCard').classList.remove('live');
  document.getElementById('liveTag').style.display = 'none';
  say('LIVE FEED OFFLINE');
  fetch('/cam/stop', { method: 'POST' }).catch(() => {});
}
document.addEventListener('keydown', e => {
  if (e.key.toLowerCase() === 'r' && !e.ctrlKey && !e.metaKey && !e.altKey && !e.shiftKey) probeAll();
});
let esFailed = 0;
function wireSSE() {
  const es = new EventSource('/events');
  es.onopen = () => { esFailed = 0; document.getElementById('connTxt').textContent = '已连接';
    document.getElementById('connDot').className = 'dot ok'; document.getElementById('connState').textContent = 'SSE 在线'; };
  es.onerror = () => {
    esFailed++;
    document.getElementById('connTxt').textContent = '重连中(' + esFailed + ')';
    document.getElementById('connDot').className = 'dot warn';
    document.getElementById('connState').textContent = 'SSE 重连中';
    es.close();
    setTimeout(wireSSE, 3000);
  };
  es.onmessage = e => { try { render(JSON.parse(e.data)); } catch (err) {} };
}
wireSSE();
</script>
</body>
</html>`
