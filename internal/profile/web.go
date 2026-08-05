package profile

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
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
	DeviceID string
}

func NewWebServer(mgr *ProbeManager, deviceID string, cam *CameraStream, task *TaskTracker) *WebServer {
	return &WebServer{mgr: mgr, cam: cam, task: task, DeviceID: deviceID}
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
<title>Edge Agent 设备总览</title>
<style>
  * { box-sizing: border-box; margin: 0; padding: 0; }
  body { font-family: "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif;
         background: linear-gradient(160deg, #0f172a 0%, #111c33 60%, #0b1226 100%);
         color: #e2e8f0; min-height: 100vh; }
  .wrap { max-width: 1180px; margin: 0 auto; padding: 28px 20px 48px; }

  header { display: flex; align-items: center; justify-content: space-between;
           flex-wrap: wrap; gap: 12px; margin-bottom: 8px; }
  header h1 { font-size: 22px; font-weight: 600; display: flex; align-items: center; gap: 10px; }
  .sub { color: #7c8db0; font-size: 13px; margin-bottom: 24px; }
  .chips { display: flex; gap: 8px; flex-wrap: wrap; }
  .chip { display: inline-flex; align-items: center; gap: 8px; background: #1e293b;
          border: 1px solid #334155; border-radius: 999px; padding: 6px 14px; font-size: 13px; }
  .chip.dev { color: #7dd3fc; }
  .dot { width: 8px; height: 8px; border-radius: 50%; display: inline-block; }

  h2.sec { font-size: 14px; font-weight: 600; color: #94a3b8; text-transform: uppercase;
           letter-spacing: .08em; margin: 28px 0 14px; display: flex; align-items: center; gap: 8px; }

  .row2 { display: grid; grid-template-columns: 1fr 1fr; gap: 16px; }
  @media (max-width: 860px) { .row2 { grid-template-columns: 1fr; } }

  .card { background: rgba(30,41,59,.75); backdrop-filter: blur(6px); border: 1px solid #334155;
          border-radius: 16px; padding: 20px; }
  .card h3 { font-size: 13px; font-weight: 600; color: #94a3b8; margin-bottom: 14px;
             letter-spacing: .05em; }

  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(160px, 1fr)); gap: 10px; }
  .tile { background: #16233d; border: 1px solid #24354f; border-radius: 10px; padding: 12px 14px; }
  .tile .label { font-size: 11px; color: #7c8db0; margin-bottom: 5px; }
  .tile .value { font-size: 14px; font-weight: 500; word-break: break-all; }
  .tile .value small { font-size: 11px; color: #7c8db0; font-weight: 400; }

  table { width: 100%; border-collapse: collapse; }
  td { padding: 8px 6px; border-bottom: 1px dashed #24354f; font-size: 13px; }
  tr:last-child td { border-bottom: none; }
  td.k { color: #7c8db0; width: 34%; }
  td.v { text-align: right; word-break: break-all; }
  td.v .empty { color: #475569; font-style: italic; }

  .caps { display: grid; grid-template-columns: repeat(auto-fill, minmax(300px, 1fr)); gap: 16px; }
  .cap { background: rgba(30,41,59,.75); backdrop-filter: blur(6px); border: 1px solid #334155;
         border-radius: 16px; padding: 18px; display: flex; flex-direction: column; gap: 10px; }
  .cap .head { display: flex; align-items: center; justify-content: space-between; }
  .cap .name { font-size: 15px; font-weight: 600; display: flex; align-items: center; gap: 8px; }
  .badge { font-size: 11px; padding: 3px 10px; border-radius: 999px; font-weight: 600; }
  .b-ok { background: #14532d; color: #4ade80; }
  .b-warn { background: #451a03; color: #fbbf24; }
  .b-bad { background: #450a0a; color: #f87171; }
  .kv { display: flex; justify-content: space-between; font-size: 12px; padding: 3px 0;
        border-bottom: 1px dashed #24354f; gap: 10px; }
  .kv:last-child { border-bottom: none; }
  .kv .k { color: #7c8db0; flex-shrink: 0; }
  .kv .v { text-align: right; word-break: break-all; }
  .err { color: #f87171; font-size: 12px; }
  .msg { color: #a5b4fc; font-size: 12px; }
  .cap .btns { margin-top: auto; }
  button { background: #334155; color: #e2e8f0; border: 1px solid #475569; border-radius: 8px;
           padding: 7px 14px; font-size: 12px; cursor: pointer; }
  button:hover { background: #475569; }
  button.primary { background: #0ea5e9; border-color: #0ea5e9; color: #fff; }
  button.primary:hover { background: #38bdf8; }

  footer { display: flex; align-items: center; justify-content: space-between;
           color: #64748b; font-size: 12px; margin-top: 32px; }
  .load { text-align: center; color: #64748b; padding: 80px 0; font-size: 14px; }
</style>
</head>
<body>
<div class="wrap">
  <header>
    <h1>🚀 Edge Agent 设备总览</h1>
    <div class="chips">
      <span class="chip dev" id="chipDevice">--</span>
      <span class="chip"><span class="dot" id="connDot"></span><span id="connTxt">连接中...</span></span>
      <button class="primary" onclick="probeAll()">全部重新探测</button>
    </div>
  </header>
  <div class="sub" id="lastUpdate">等待数据...</div>

  <div id="loading" class="load">正在加载探测数据...</div>
  <div id="main" style="display:none;">

    <h2 class="sec">⚙️ 设备参数</h2>
    <div class="row2">
      <div class="card">
        <h3>硬件参数</h3>
        <div class="grid" id="hwGrid"></div>
      </div>
      <div class="card">
        <h3>软件环境</h3>
        <table id="swTable"></table>
      </div>
    </div>

    <h2 class="sec">🤖 当前任务 & 所用模型</h2>
    <div class="row2">
      <div class="card">
        <h3>当前执行的任务</h3>
        <div style="font-size:24px; font-weight:700; color:#7dd3fc;" id="curTask">--</div>
        <div class="msg" id="curTaskSub" style="margin-top:8px;">任务来源于设备收到的推理调用(detect_objects / run_ocr)</div>
      </div>
      <div class="card">
        <h3>所用模型 (最近一次 OTA 自动下载)</h3>
        <div id="modelInfo">
          <div class="msg">尚未发生过模型自动下载</div>
        </div>
      </div>
    </div>

    <h2 class="sec">🔍 能力探测</h2>
    <div class="caps" id="caps"></div>

    <h2 class="sec">📷 实时画面</h2>
    <div class="card" id="camCard">
      <h3 style="margin-bottom:12px;">摄像头实时预览 (MJPEG · 按需启停,观看即开,离开即关)</h3>
      <div style="text-align:center;">
        <img id="camImg" style="max-width:100%; border-radius:10px; background:#0b1226; display:none;"
             alt="camera stream">
        <div id="camOff" style="color:#7c8db0; padding:24px 0; font-size:14px;">
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
      <span>连接: <span id="connState">--</span> · 最近更新: <span id="lastUpd">--</span></span>
      <span>SSE 实时推送 · 每 5 秒心跳</span>
    </footer>
  </div>
</div>
<script>
const HARDWARE = [
  ['device_type','设备型号'],['architecture','架构'],['cpu_cores','CPU 核心数'],
  ['memory_mb','内存'],['gpu','GPU'],['disk','磁盘'],['hostname','主机名'],
];
const RUNTIME = [['uptime','运行时长'],['load','负载']];
const SOFTWARE = [
  ['os','操作系统'],['kernel','内核版本'],['cuda','CUDA'],['tensorrt','TensorRT'],
  ['python','Python'],['ros','ROS'],['agent_version','Agent 版本'],
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

function render(ev) {
  document.getElementById('loading').style.display = 'none';
  document.getElementById('main').style.display = 'block';
  const caps = ev.caps || {};
  document.getElementById('chipDevice').textContent = ev.device_id;
  document.getElementById('lastUpd').textContent = new Date(ev.timestamp * 1000).toLocaleTimeString();
  document.getElementById('lastUpdate').textContent = '设备 ' + ev.device_id + ' · 最近更新 ' + new Date(ev.timestamp * 1000).toLocaleString();

  const dev = caps.device?.details || {};
  const hw = dev.hardware || {}, sw = dev.software || {}, rt = dev.runtime || {};
  document.getElementById('hwGrid').innerHTML =
    HARDWARE.map(([k, l]) => tile(l, hw[k])).join('') + RUNTIME.map(([k, l]) => tile(l, rt[k])).join('');
  document.getElementById('swTable').innerHTML = SOFTWARE.map(([k, l]) => row(l, sw[k])).join('');

  document.getElementById('curTask').textContent = ev.task || '未知';
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
    card.className = 'cap';
    card.innerHTML =
      '<div class="head"><div class="name"><span class="dot ' + dotCls + '"></span>' + esc(n) + '</div>' +
      '<span class="badge ' + cls + '">' + label + '</span></div>' +
      rows.map(([k, v]) => '<div class="kv"><span class="k">' + esc(k) + '</span><span class="v">' + esc(v) + '</span></div>').join('') +
      (c.error_code ? '<div class="err">' + esc(c.error_code) + '</div>' : '') +
      (c.message ? '<div class="msg">' + esc(c.message) + '</div>' : '') +
      '<div class="btns"><button onclick="probeCap(\'' + esc(n) + '\')">重新探测</button></div>';
    grid.appendChild(card);
  }
}
async function probeCap(name) {
  await fetch('/probe?cap=' + encodeURIComponent(name) + '&force=1', { method: 'POST' });
}
async function probeAll() {
  await fetch('/probe?force=1', { method: 'POST' });
}
function camOn() {
  const img = document.getElementById('camImg');
  img.src = '/cam/stream?t=' + Date.now();
  img.onload = () => {
    document.getElementById('camOff').style.display = 'none';
    document.getElementById('camOnBtn').style.display = 'none';
    document.getElementById('camOffBtn').style.display = '';
    document.getElementById('camMsg').textContent = '';
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
  fetch('/cam/stop', { method: 'POST' }).catch(() => {});
}
let esFailed = 0;
function wireSSE() {
  const es = new EventSource('/events');
  es.onopen = () => { esFailed = 0; document.getElementById('connTxt').textContent = '已连接';
    document.getElementById('connDot').className = 'dot'; document.getElementById('connState').textContent = 'SSE 在线'; };
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
