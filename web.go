package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"golang.org/x/crypto/acme/autocert"
)

// runWebServer serves live grid, motion/alerts APIs, media, SSE, optional TLS/ACME.
func runWebServer(ctx context.Context, addr string, workers []*CameraWorker, store *EventStore, logQ chan<- string, tlsDomain, tlsEmail, tlsCert, tlsKey, cfgPath string) {
	if addr == "" {
		return
	}
	byName := make(map[string]*CameraWorker, len(workers))
	for _, w := range workers {
		byName[w.Name()] = w
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/", func(rw http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(rw, r)
			return
		}
		rw.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = rw.Write([]byte(webIndexHTML(workers)))
	})

	mux.HandleFunc("/snap/", func(rw http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/snap/")
		name = strings.TrimSuffix(name, ".jpg")
		name = strings.TrimSpace(name)
		w := byName[name]
		if w == nil {
			http.NotFound(rw, r)
			return
		}
		jpg, err := w.previewJPEGBytes()
		if err != nil {
			rw.Header().Set("Content-Type", "text/plain; charset=utf-8")
			rw.Header().Set("Retry-After", "1")
			rw.WriteHeader(http.StatusServiceUnavailable)
			_, _ = rw.Write([]byte(err.Error()))
			return
		}
		rw.Header().Set("Content-Type", "image/jpeg")
		rw.Header().Set("Cache-Control", "no-store")
		_, _ = rw.Write(jpg)
	})

	mux.HandleFunc("/api/events", func(rw http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeJSON(rw, []EventRecord{})
			return
		}
		typ := r.URL.Query().Get("type")
		limit := 80
		if typ == "alert" {
			limit = 200
		}
		if q := r.URL.Query().Get("limit"); q != "" {
			if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 2000 {
				limit = n
			}
		}
		list := store.List(limit, typ)
		writeJSON(rw, list)
	})

	mux.HandleFunc("/api/events/stream", func(rw http.ResponseWriter, r *http.Request) {
		if store == nil {
			http.Error(rw, "no store", 503)
			return
		}
		flusher, ok := rw.(http.Flusher)
		if !ok {
			http.Error(rw, "no flush", 500)
			return
		}
		rw.Header().Set("Content-Type", "text/event-stream")
		rw.Header().Set("Cache-Control", "no-cache")
		rw.Header().Set("Connection", "keep-alive")
		ch := store.Subscribe()
		defer store.Unsubscribe(ch)
		// hello
		fmt.Fprintf(rw, "event: hello\ndata: {}\n\n")
		flusher.Flush()
		notify := r.Context().Done()
		for {
			select {
			case <-notify:
				return
			case <-ctx.Done():
				return
			case rec, ok := <-ch:
				if !ok {
					return
				}
				b, _ := json.Marshal(rec)
				fmt.Fprintf(rw, "event: event\ndata: %s\n\n", b)
				flusher.Flush()
			}
		}
	})

	mux.HandleFunc("/media/", func(rw http.ResponseWriter, r *http.Request) {
		if store == nil {
			http.NotFound(rw, r)
			return
		}
		rel := strings.TrimPrefix(r.URL.Path, "/media/")
		abs, err := store.AbsMedia(rel)
		if err != nil {
			http.NotFound(rw, r)
			return
		}
		http.ServeFile(rw, r, abs)
	})

	mux.HandleFunc("/api/settings", func(rw http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			out := make([]camMotionSettings, 0, len(workers))
			for _, w := range workers {
				out = append(out, w.MotionSettings())
			}
			writeJSON(rw, map[string]interface{}{"cameras": out})
		case http.MethodPut, http.MethodPost:
			var body struct {
				Cameras []camMotionSettings `json:"cameras"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(rw, err.Error(), 400)
				return
			}
			if err := applyAndSaveSettings(cfgPath, byName, body.Cameras); err != nil {
				http.Error(rw, err.Error(), 400)
				return
			}
			out := make([]camMotionSettings, 0, len(workers))
			for _, w := range workers {
				out = append(out, w.MotionSettings())
			}
			writeJSON(rw, map[string]interface{}{"cameras": out})
		default:
			http.Error(rw, "method", 405)
		}
	})

	mux.HandleFunc("/api/status", func(rw http.ResponseWriter, r *http.Request) {
		type camSt struct {
			Name   string `json:"name"`
			Status string `json:"status"`
		}
		out := make([]camSt, 0, len(workers))
		for _, w := range workers {
			out = append(out, camSt{Name: w.Name(), Status: w.Status()})
		}
		writeJSON(rw, map[string]interface{}{
			"cameras": out,
			"store":   store != nil,
			"secure":  r.TLS != nil || strings.HasPrefix(r.Host, "localhost") || strings.HasPrefix(r.Host, "127.0.0.1"),
		})
	})

	srv := &http.Server{Addr: addr, Handler: mux}
	go func() {
		<-ctx.Done()
		shctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shctx)
	}()

	// Optional Let's Encrypt / cert files
	if tlsDomain != "" && tlsEmail != "" {
		cache := filepath.Join(os.TempDir(), "motioncamotion-certs")
		if store != nil {
			cache = filepath.Join(store.Root(), "certs")
		}
		_ = os.MkdirAll(cache, 0o700)
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(tlsDomain),
			Cache:      autocert.DirCache(cache),
			Email:      tlsEmail,
		}
		srv.TLSConfig = &tls.Config{GetCertificate: m.GetCertificate}
		// HTTP-01 on :80 if main is :443-like; also serve challenge
		go func() {
			_ = http.ListenAndServe(":80", m.HTTPHandler(nil))
		}()
		logQ <- fmt.Sprintf("web https://%s/ (acme email=%s)", tlsDomain, tlsEmail)
		if err := srv.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
			logQ <- fmt.Sprintf("web server: %v", err)
		}
		return
	}
	if tlsCert != "" && tlsKey != "" {
		logQ <- fmt.Sprintf("web https://0.0.0.0%s/ (cert files)", addr)
		if err := srv.ListenAndServeTLS(tlsCert, tlsKey); err != nil && err != http.ErrServerClosed {
			logQ <- fmt.Sprintf("web server: %v", err)
		}
		return
	}

	logQ <- fmt.Sprintf("web http://0.0.0.0%s/ (live + events + SSE /api/events/stream)", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		logQ <- fmt.Sprintf("web server: %v", err)
	}
}

func applyAndSaveSettings(path string, byName map[string]*CameraWorker, updates []camMotionSettings) error {
	if path == "" {
		return fmt.Errorf("no config path")
	}
	valid := make([]camMotionSettings, 0, len(updates))
	for _, u := range updates {
		if byName[u.Name] == nil {
			return fmt.Errorf("unknown camera %s", u.Name)
		}
		if u.Threshold <= 0 || u.Threshold > 0.5 {
			return fmt.Errorf("%s: threshold must be >0 and ≤0.5", u.Name)
		}
		if u.MotionMinBlob < 1 || u.MotionMinBlob > 200 {
			return fmt.Errorf("%s: motion_min_blob out of range", u.Name)
		}
		if u.MotionMinCompact < 0 || u.MotionMinCompact > 1 {
			return fmt.Errorf("%s: motion_min_compact out of range", u.Name)
		}
		if u.MotionCooldownSec < 0 || u.MotionCooldownSec > 300 {
			return fmt.Errorf("%s: motion_cooldown_sec out of range", u.Name)
		}
		if u.MotionSustainFrames < 1 || u.MotionSustainFrames > 30 {
			return fmt.Errorf("%s: motion_sustain_frames out of range", u.Name)
		}
		valid = append(valid, u)
	}
	for _, u := range valid {
		byName[u.Name].ApplyMotion(u)
	}
	return patchCameraSettings(path, valid)
}

func writeJSON(rw http.ResponseWriter, v interface{}) {
	rw.Header().Set("Content-Type", "application/json")
	rw.Header().Set("Cache-Control", "no-store")
	enc := json.NewEncoder(rw)
	_ = enc.Encode(v)
}

func webIndexHTML(workers []*CameraWorker) string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Motion CAMotion</title>
<style>
:root{
  --bg:#0b0d10;--surface:#12151a;--card:#161a21;--bd:#262c36;
  --tx:#e8eaed;--mut:#8b93a0;--acc:#5b9fd4;--acc2:#3d7aab;
  --ok:#3ecf8e;--warn:#e8b84a;--al:#e85d5d;
  --radius:10px;--shadow:0 8px 28px rgba(0,0,0,.35);
  --font:ui-sans-serif,system-ui,-apple-system,"Segoe UI",Roboto,Helvetica,Arial,sans-serif;
  --mono:ui-monospace,"SF Mono",Menlo,Consolas,monospace;
}
*{box-sizing:border-box}
html,body{height:100%}
body{font-family:var(--font);background:var(--bg);color:var(--tx);margin:0;line-height:1.45;-webkit-font-smoothing:antialiased}
a{color:var(--acc);text-decoration:none}
a:hover{text-decoration:underline}
header{
  display:flex;flex-wrap:wrap;gap:14px 20px;align-items:center;
  padding:14px 22px;background:linear-gradient(180deg,#141820 0%,#10141a 100%);
  border-bottom:1px solid var(--bd);position:sticky;top:0;z-index:20;
  backdrop-filter:blur(8px);
}
.brand{display:flex;align-items:baseline;gap:10px;min-width:max-content}
.brand h1{font-size:1.15rem;margin:0;font-weight:650;letter-spacing:.02em}
.brand h1 span.cam{color:var(--acc);font-weight:700}
.brand .tag{font-size:.68rem;color:var(--mut);text-transform:uppercase;letter-spacing:.08em;font-weight:600}
nav{display:flex;gap:6px;background:var(--surface);padding:4px;border-radius:999px;border:1px solid var(--bd)}
nav button{
  background:transparent;border:0;color:var(--mut);padding:7px 14px;border-radius:999px;
  cursor:pointer;font:inherit;font-size:.84rem;font-weight:550;transition:background .15s,color .15s
}
nav button:hover{color:var(--tx);background:rgba(255,255,255,.04)}
nav button.active{background:var(--acc2);color:#fff;box-shadow:0 1px 0 rgba(255,255,255,.08) inset}
.header-right{margin-left:auto;display:flex;align-items:center;gap:12px}
.status-pill{
  display:inline-flex;align-items:center;gap:7px;font-size:.78rem;color:var(--mut);
  background:var(--surface);border:1px solid var(--bd);padding:5px 11px;border-radius:999px
}
.status-pill .dot{width:7px;height:7px;border-radius:50%;background:var(--warn);box-shadow:0 0 0 3px rgba(232,184,74,.15)}
.status-pill.ok .dot{background:var(--ok);box-shadow:0 0 0 3px rgba(62,207,142,.15)}
.status-pill.bad .dot{background:var(--al);box-shadow:0 0 0 3px rgba(232,93,93,.15)}
.btn{
  background:var(--surface);border:1px solid var(--bd);color:var(--tx);
  padding:7px 12px;border-radius:8px;cursor:pointer;font:inherit;font-size:.8rem;font-weight:550
}
.btn:hover{border-color:var(--acc);color:#fff}
#banner{
  display:none;background:linear-gradient(90deg,#3a1c1c,#2a1518);
  color:#ffc9c9;padding:10px 22px;font-size:.88rem;border-bottom:1px solid #5a2a2a;font-weight:500
}
#banner.show{display:block}
.panel{display:none;padding:18px 22px 28px;max-width:1600px;margin:0 auto}
.panel.active{display:block}
.panel-lead{color:var(--mut);font-size:.88rem;margin:0 0 16px;max-width:52rem}
/* Split: list | detail side-by-side (alerts + motion) */
.split{
  display:grid;grid-template-columns:minmax(280px,1fr) minmax(320px,1.1fr);
  gap:16px;align-items:stretch;
  min-height:calc(100vh - 140px);
}
@media (max-width:900px){
  .split{grid-template-columns:1fr;min-height:0}
  .split-detail{position:static!important;max-height:none!important}
}
.split-list{min-width:0;display:flex;flex-direction:column;min-height:0}
.split-list .table-wrap{flex:1;max-height:calc(100vh - 180px);overflow:auto}
.split-detail{
  position:sticky;top:78px;align-self:start;
  background:var(--card);border:1px solid var(--bd);border-radius:var(--radius);
  box-shadow:var(--shadow);padding:16px;min-height:280px;max-height:calc(100vh - 100px);
  overflow:auto;
}
.split-detail .placeholder{
  color:var(--mut);display:flex;align-items:center;justify-content:center;
  min-height:240px;text-align:center;font-size:.9rem;padding:24px;
}
.split-detail .detail-inner{display:flex;flex-direction:column;gap:14px}
.split-detail img{
  width:100%;max-height:min(55vh,520px);object-fit:contain;
  border:1px solid var(--bd);border-radius:8px;background:#000;display:block
}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(300px,1fr));gap:14px}
.card{
  background:var(--card);border:1px solid var(--bd);border-radius:var(--radius);
  overflow:hidden;box-shadow:var(--shadow);transition:border-color .15s
}
.card:hover{border-color:#3a4454}
.card-head{
  font-size:.82rem;margin:0;padding:10px 12px;background:rgba(255,255,255,.02);
  display:flex;justify-content:space-between;align-items:center;border-bottom:1px solid var(--bd)
}
.card-head .name{font-weight:600;letter-spacing:.01em}
.card-head .st{color:var(--mut);font-weight:450;font-size:.75rem;text-transform:capitalize}
.card-media{position:relative;background:#000;aspect-ratio:16/10;display:flex;align-items:center;justify-content:center}
.card-media img{display:block;width:100%;height:100%;object-fit:contain;background:#000}
.card-foot{
  font-size:.74rem;color:var(--mut);padding:8px 12px;display:flex;justify-content:space-between;align-items:center
}
.card-foot a{font-weight:550}
.table-wrap{border:1px solid var(--bd);border-radius:var(--radius);overflow:auto;background:var(--card)}
table{width:100%;border-collapse:collapse;font-size:.84rem}
th,td{text-align:left;padding:11px 14px;border-bottom:1px solid var(--bd)}
th{color:var(--mut);font-weight:600;font-size:.72rem;text-transform:uppercase;letter-spacing:.06em;background:rgba(255,255,255,.02)}
tr:last-child td{border-bottom:0}
tbody tr{cursor:pointer;transition:background .12s}
tbody tr:hover{background:rgba(91,159,212,.08)}
tbody tr.selected{background:rgba(91,159,212,.14)}
.badge{
  display:inline-block;padding:2px 8px;border-radius:999px;font-size:.68rem;font-weight:650;
  text-transform:capitalize;letter-spacing:.03em
}
.badge.alert{background:rgba(232,93,93,.18);color:#ffb4b4;border:1px solid rgba(232,93,93,.35)}
.badge.motion{background:rgba(91,159,212,.15);color:#a8d0f0;border:1px solid rgba(91,159,212,.3)}
.toast{
  position:fixed;right:18px;bottom:18px;max-width:340px;background:var(--card);
  border:1px solid var(--acc);padding:14px 16px;border-radius:var(--radius);
  box-shadow:var(--shadow);display:none;z-index:30;font-size:.88rem
}
.toast.show{display:block;animation:fadeIn .2s ease}
.toast img{max-width:100%;margin-top:10px;border-radius:8px;border:1px solid var(--bd)}
@keyframes fadeIn{from{opacity:0;transform:translateY(6px)}to{opacity:1;transform:none}}
.detlist{font-size:.88rem}
.detlist h3{margin:0 0 6px;font-size:1rem}
.detlist ul{margin:8px 0;padding-left:1.1rem;color:var(--tx)}
.detlist li{margin:4px 0}
.empty{color:var(--mut);padding:28px;text-align:center;border:1px dashed var(--bd);border-radius:var(--radius)}
.mono{font-family:var(--mono);font-size:.8rem}
.card-recent{margin:0;padding:6px 12px 10px;list-style:none;font-size:.72rem;color:var(--mut);border-top:1px solid var(--bd);min-height:3.2em}
.card-recent li{display:flex;justify-content:space-between;gap:8px;padding:2px 0;cursor:pointer}
.card-recent li:hover{color:var(--tx)}
.card-recent .al{color:#ffb4b4}
.set-table{width:100%;border:1px solid var(--bd);border-radius:var(--radius);overflow:auto;background:var(--card)}
.set-table input{width:5.2rem;background:var(--surface);border:1px solid var(--bd);color:var(--tx);padding:5px 7px;border-radius:6px;font:inherit;font-size:.84rem}
.set-actions{margin-top:14px;display:flex;gap:10px;align-items:center}
#setMsg{color:var(--mut);font-size:.84rem}
#setMsg.ok{color:var(--ok)}
#setMsg.bad{color:var(--al)}
</style></head><body>
<header>
  <div class="brand">
    <h1>Motion <span class="cam">CAMotion</span></h1>
    <span class="tag">Operator</span>
  </div>
  <nav>
    <button type="button" class="active" data-tab="live">Live</button>
    <button type="button" data-tab="alerts">Alerts</button>
    <button type="button" data-tab="motion">Motion</button>
    <button type="button" data-tab="settings">Settings</button>
  </nav>
  <div class="header-right">
    <span class="status-pill" id="connPill"><span class="dot"></span><span id="conn">Connecting…</span></span>
    <button type="button" class="btn" id="btnNotify">Notifications</button>
  </div>
</header>
<div id="banner"></div>
<div id="tab-live" class="panel active">
  <p class="panel-lead">Live camera previews. Tiles refresh automatically.</p>
  <div class="grid">`)
	for i, w := range workers {
		name := w.Name()
		st := w.Status()
		b.WriteString(`<div class="card"><div class="card-head"><span class="name">`)
		b.WriteString(name)
		b.WriteString(`</span><span class="st" data-st="`)
		b.WriteString(name)
		b.WriteString(`">`)
		b.WriteString(st)
		b.WriteString(`</span></div><div class="card-media">`)
		b.WriteString(fmt.Sprintf(`<img data-cam="%s" data-delay="%d" alt="%s" src="">`, name, i*150, name))
		b.WriteString(`</div><div class="card-foot"><a href="/snap/`)
		b.WriteString(name)
		b.WriteString(`.jpg" target="_blank" rel="noopener">Open JPEG</a><span data-age="`)
		b.WriteString(name)
		b.WriteString(`">…</span></div><ul class="card-recent" data-recent="`)
		b.WriteString(name)
		b.WriteString(`"></ul></div>`)
	}
	b.WriteString(`</div></div>
<div id="tab-alerts" class="panel">
  <p class="panel-lead">YOLO-pass events with bounding boxes for every object above confidence. Click a row to preview.</p>
  <div class="split">
    <div class="split-list" id="alerts-list"></div>
    <div class="split-detail" id="alert-detail"><div class="placeholder">Select an alert to view the annotated frame.</div></div>
  </div>
</div>
<div id="tab-motion" class="panel">
  <p class="panel-lead">Last motion triggers (including YOLO rejects). Survives restart. Click a row to preview.</p>
  <div class="split">
    <div class="split-list" id="motion-list"></div>
    <div class="split-detail" id="motion-detail"><div class="placeholder">Select a motion event to view the snapshot.</div></div>
  </div>
</div>
<div id="tab-settings" class="panel">
  <p class="panel-lead">Per-camera motion sensitivity. Lower threshold / blob = more triggers. Compactness filters noisy foliage. Save writes cameras_all.json and applies immediately.</p>
  <div class="set-table" id="settings-table"></div>
  <div class="set-actions">
    <button type="button" class="btn" id="btnSaveSettings">Save</button>
    <span id="setMsg"></span>
  </div>
</div>
<div class="toast" id="toast"></div>
<script>
` + webAppJS + `
</script></body></html>`)
	return b.String()
}

const webAppJS = `
(function(){
  var OK_MS=1500, ERR_MS=2000;
  function tick(img){
    var cam=img.getAttribute('data-cam'); if(!cam) return;
    // One in-flight snap per tile — avoids connection storms when decode is slow.
    if(img.getAttribute('data-busy')==='1') return;
    if(document.hidden){
      setTimeout(function(){tick(img);}, OK_MS);
      return;
    }
    var age=document.querySelector('[data-age="'+cam+'"]');
    img.setAttribute('data-busy','1');
    var probe=new Image();
    probe.onload=function(){
      img.src=probe.src;
      img.setAttribute('data-busy','0');
      if(age) age.textContent='Live';
      setTimeout(function(){tick(img);}, OK_MS);
    };
    probe.onerror=function(){
      img.setAttribute('data-busy','0');
      if(age) age.textContent='Waiting…';
      setTimeout(function(){tick(img);}, ERR_MS);
    };
    probe.src='/snap/'+encodeURIComponent(cam)+'.jpg?t='+Date.now();
  }
  document.querySelectorAll('img[data-cam]').forEach(function(img){
    setTimeout(function(){ tick(img); }, parseInt(img.getAttribute('data-delay')||'0',10));
  });

  document.querySelectorAll('nav button[data-tab]').forEach(function(btn){
    btn.addEventListener('click', function(){
      document.querySelectorAll('nav button').forEach(function(b){ b.classList.remove('active'); });
      document.querySelectorAll('.panel').forEach(function(p){ p.classList.remove('active'); });
      btn.classList.add('active');
      document.getElementById('tab-'+btn.getAttribute('data-tab')).classList.add('active');
      if(btn.getAttribute('data-tab')==='alerts') loadEvents('alert','alerts-list','alert-detail');
      if(btn.getAttribute('data-tab')==='motion') loadEvents('motion','motion-list','motion-detail');
      if(btn.getAttribute('data-tab')==='settings') loadSettings();
    });
  });

  function esc(s){ return String(s).replace(/[&<>"']/g,function(c){return {'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c];}); }

  function fmtWhen(iso){
    try{
      var d=new Date(iso);
      if(isNaN(d.getTime())) return iso;
      return d.toLocaleString(undefined,{year:'numeric',month:'short',day:'2-digit',hour:'2-digit',minute:'2-digit',second:'2-digit'});
    }catch(e){ return iso; }
  }

  function loadEvents(type, listId, detailId){
    fetch('/api/events?type='+encodeURIComponent(type)+'&t='+Date.now()).then(function(r){return r.json();}).then(function(rows){
      var el=document.getElementById(listId);
      if(!rows || !rows.length){ el.innerHTML='<div class="empty">No events yet.</div>'; return; }
      var h='<div class="table-wrap"><table><thead><tr><th>When</th><th>Camera</th><th>Type</th><th>Details</th></tr></thead><tbody>';
      rows.forEach(function(e){
        var info = e.type==='alert'
          ? (e.dets||[]).map(function(d){return d.class+' '+d.score.toFixed(2);}).join(', ')
          : (e.still ? 'Still + clip' : 'Clip');
        var typeLabel = e.type==='alert' ? 'Alert' : 'Motion';
        h+='<tr data-id="'+esc(e.id)+'">'
          +'<td class="mono">'+esc(fmtWhen(e.at))+'</td><td>'+esc(e.cam)+'</td>'
          +'<td><span class="badge '+esc(e.type)+'">'+esc(typeLabel)+'</span> <span style="color:var(--mut)">'+esc(e.label||'')+'</span></td>'
          +'<td>'+esc(info)+'</td></tr>';
      });
      h+='</tbody></table></div>';
      el.innerHTML=h;
      el.querySelectorAll('tr[data-id]').forEach(function(tr){
        tr.addEventListener('click', function(){
          el.querySelectorAll('tr').forEach(function(x){ x.classList.remove('selected'); });
          tr.classList.add('selected');
          var id=tr.getAttribute('data-id');
          var e=rows.find(function(x){return x.id===id;});
          showDetail(e, detailId);
        });
      });
    }).catch(function(err){
      document.getElementById(listId).innerHTML='<div class="empty">Load failed: '+esc(err)+'</div>';
    });
  }

  function showDetail(e, detailId){
    var d=document.getElementById(detailId);
    if(!e){
      d.innerHTML='<div class="placeholder">Select an event to preview.</div>';
      return;
    }
    var h='<div class="detail-inner">';
    if(e.still){
      h+='<img src="/media/'+esc(e.still)+'" alt="Preview">';
    } else {
      h+='<div class="placeholder">No preview media for this event.</div>';
    }
    h+='<div class="detlist"><h3>'+esc(e.cam)+'</h3>';
    h+='<p class="mono" style="color:var(--mut);margin:0 0 8px">'+esc(fmtWhen(e.at))+'</p>';
    if(e.clip) h+='<p style="margin:0 0 8px"><a href="/media/'+esc(e.clip)+'" download>Download clip (.h265)</a></p>';
    if(e.dets && e.dets.length){
      h+='<p style="margin:12px 0 4px;color:var(--mut);font-size:.72rem;text-transform:uppercase;letter-spacing:.06em;font-weight:600">Detections</p><ul>';
      e.dets.forEach(function(x){
        h+='<li><strong>'+esc(x.class)+'</strong> · score '+x.score.toFixed(3)+'</li>';
      });
      h+='</ul>';
    } else {
      h+='<p style="color:var(--mut);margin:8px 0 0">No YOLO boxes (motion only or reject).</p>';
    }
    h+='</div></div>';
    d.innerHTML=h;
  }

  var conn=document.getElementById('conn');
  var pill=document.getElementById('connPill');
  var banner=document.getElementById('banner');
  var toast=document.getElementById('toast');
  var es;
  var sseBackoff=1000;
  function setConn(text, cls){
    conn.textContent=text;
    pill.classList.remove('ok','bad');
    if(cls) pill.classList.add(cls);
  }
  function connectSSE(){
    if(es) try{es.close();}catch(e){}
    es=new EventSource('/api/events/stream');
    es.addEventListener('hello', function(){ setConn('Live', 'ok'); sseBackoff=1000; });
    es.addEventListener('event', function(ev){
      try{
        var e=JSON.parse(ev.data);
        refreshLiveRecent();
        if(e.type==='alert'){
          banner.textContent='Alert · '+e.cam+' · '+(e.main_class||'object')+' '+(e.main_score||0).toFixed(2);
          banner.classList.add('show');
          toast.innerHTML='<div style="font-weight:650;margin-bottom:4px">Alert · '+esc(e.cam)+'</div>'
            +esc(e.main_class||'object')+' · '+(e.main_score||0).toFixed(2)
            +(e.still?'<br><img src="/media/'+esc(e.still)+'" alt="">':'');
          toast.classList.add('show');
          setTimeout(function(){ toast.classList.remove('show'); }, 8000);
          if(window.Notification && Notification.permission==='granted'){
            new Notification('Motion CAMotion · '+e.cam, { body: (e.main_class||'object')+' '+(e.main_score||0).toFixed(2) });
          }
          if(document.getElementById('tab-alerts').classList.contains('active'))
            loadEvents('alert','alerts-list','alert-detail');
        } else if(e.type==='motion'){
          if(document.getElementById('tab-motion').classList.contains('active'))
            loadEvents('motion','motion-list','motion-detail');
        }
      }catch(err){}
    });
    es.onerror=function(){
      setConn('Reconnecting…', 'bad');
      try{es.close();}catch(e){}
      // Manual reconnect with backoff — browser auto-reconnect can thrash.
      setTimeout(connectSSE, sseBackoff);
      sseBackoff=Math.min(sseBackoff*2, 15000);
    };
  }
  connectSSE();
  loadEvents('alert','alerts-list','alert-detail');
  loadEvents('motion','motion-list','motion-detail');
  refreshLiveRecent();

  function refreshLiveRecent(){
    fetch('/api/events?limit=80&t='+Date.now()).then(function(r){return r.json();}).then(function(rows){
      var by={};
      (rows||[]).forEach(function(e){
        if(!by[e.cam]) by[e.cam]=[];
        if(by[e.cam].length<3) by[e.cam].push(e);
      });
      document.querySelectorAll('[data-recent]').forEach(function(ul){
        var cam=ul.getAttribute('data-recent');
        var list=by[cam]||[];
        if(!list.length){ ul.innerHTML='<li>No recent events</li>'; return; }
        ul.innerHTML=list.map(function(e){
          var label=e.type==='alert' ? (e.main_class||'alert') : 'motion';
          var cls=e.type==='alert'?'al':'';
          return '<li class="'+cls+'"><span>'+esc(label)+'</span><span>'+esc(fmtWhen(e.at))+'</span></li>';
        }).join('');
      });
    }).catch(function(){});
  }

  function loadSettings(){
    fetch('/api/settings').then(function(r){return r.json();}).then(function(data){
      var cams=data.cameras||[];
      var h='<table><thead><tr><th>Camera</th><th>Threshold</th><th>Min blob</th><th>Compact</th><th>Cooldown s</th><th>Sustain</th></tr></thead><tbody>';
      cams.forEach(function(c){
        h+='<tr data-cam="'+esc(c.name)+'">'
          +'<td>'+esc(c.name)+'</td>'
          +'<td><input data-f="threshold" type="number" step="0.005" min="0.005" max="0.5" value="'+c.threshold+'"></td>'
          +'<td><input data-f="motion_min_blob" type="number" step="1" min="1" max="200" value="'+c.motion_min_blob+'"></td>'
          +'<td><input data-f="motion_min_compact" type="number" step="0.05" min="0" max="1" value="'+c.motion_min_compact+'"></td>'
          +'<td><input data-f="motion_cooldown_sec" type="number" step="1" min="0" max="300" value="'+c.motion_cooldown_sec+'"></td>'
          +'<td><input data-f="motion_sustain_frames" type="number" step="1" min="1" max="30" value="'+c.motion_sustain_frames+'"></td>'
          +'</tr>';
      });
      h+='</tbody></table>';
      document.getElementById('settings-table').innerHTML=h;
    }).catch(function(err){
      document.getElementById('settings-table').innerHTML='<div class="empty">Load failed: '+esc(err)+'</div>';
    });
  }

  document.getElementById('btnSaveSettings').addEventListener('click', function(){
    var msg=document.getElementById('setMsg');
    var cams=[];
    document.querySelectorAll('#settings-table tr[data-cam]').forEach(function(tr){
      var num=function(f){ return parseFloat(tr.querySelector('[data-f="'+f+'"]').value); };
      cams.push({
        name: tr.getAttribute('data-cam'),
        threshold: num('threshold'),
        motion_min_blob: parseInt(tr.querySelector('[data-f="motion_min_blob"]').value,10),
        motion_min_compact: num('motion_min_compact'),
        motion_cooldown_sec: parseInt(tr.querySelector('[data-f="motion_cooldown_sec"]').value,10),
        motion_sustain_frames: parseInt(tr.querySelector('[data-f="motion_sustain_frames"]').value,10)
      });
    });
    fetch('/api/settings',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({cameras:cams})})
      .then(function(r){ if(!r.ok) return r.text().then(function(t){ throw new Error(t||r.status); }); return r.json(); })
      .then(function(){ msg.className='ok'; msg.textContent='Saved'; })
      .catch(function(err){ msg.className='bad'; msg.textContent=String(err.message||err); });
  });

  document.getElementById('btnNotify').addEventListener('click', function(){
    if(!window.Notification){
      alert('Notifications are not available in this browser.');
      return;
    }
    if(!window.isSecureContext && location.hostname!=='localhost' && location.hostname!=='127.0.0.1'){
      alert("Browser notifications need HTTPS (or open via http://127.0.0.1). Use tls_domain/tls_email for Let's Encrypt, or open from localhost.");
      return;
    }
    Notification.requestPermission();
  });
})();
`
