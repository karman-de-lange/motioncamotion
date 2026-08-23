package main

import (
	"fmt"
	"strings"
)

func webIndexHTML(workers []*CameraWorker) string {
	var b strings.Builder
	b.WriteString(`<!DOCTYPE html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Motion CAMotion</title>
<style>
:root{
  --bg:#0c0e12;--surface:#14181f;--card:#181c24;--bd:#2a3140;
  --tx:#e9edf2;--mut:#8e97a8;--acc:#6aa8e8;--acc2:#3d7ab0;
  --ok:#3ecf8e;--warn:#e8b84a;--al:#e85d5d;
  --radius:10px;--font:ui-sans-serif,system-ui,-apple-system,"Segoe UI",Roboto,sans-serif;
  --mono:ui-monospace,"SF Mono",Menlo,Consolas,monospace;
}
*{box-sizing:border-box}
html,body{height:100%;margin:0}
body{font-family:var(--font);background:var(--bg);color:var(--tx);line-height:1.4;display:flex;flex-direction:column}
a{color:var(--acc);text-decoration:none}
a:hover{text-decoration:underline}
header{
  display:flex;flex-wrap:wrap;gap:12px 18px;align-items:center;
  padding:10px 18px;background:#11151c;border-bottom:1px solid var(--bd);flex:0 0 auto
}
.brand{display:flex;align-items:baseline;gap:8px}
.brand h1{font-size:1.05rem;margin:0;font-weight:650}
.brand h1 span{color:var(--acc)}
nav{display:flex;gap:4px;background:var(--surface);padding:3px;border-radius:999px;border:1px solid var(--bd)}
nav button{
  background:transparent;border:0;color:var(--mut);padding:6px 14px;border-radius:999px;
  cursor:pointer;font:inherit;font-size:.84rem;font-weight:550
}
nav button:hover{color:var(--tx)}
nav button.active{background:var(--acc2);color:#fff}
.header-right{margin-left:auto;display:flex;align-items:center;gap:10px}
.status-pill{
  display:inline-flex;align-items:center;gap:6px;font-size:.76rem;color:var(--mut);
  background:var(--surface);border:1px solid var(--bd);padding:4px 10px;border-radius:999px
}
.status-pill .dot{width:7px;height:7px;border-radius:50%;background:var(--warn)}
.status-pill.ok .dot{background:var(--ok)}
.status-pill.bad .dot{background:var(--al)}
.btn{
  background:var(--surface);border:1px solid var(--bd);color:var(--tx);
  padding:6px 11px;border-radius:8px;cursor:pointer;font:inherit;font-size:.8rem
}
.btn:hover{border-color:var(--acc)}
#banner{display:none;background:#3a1c1c;color:#ffc9c9;padding:8px 18px;font-size:.86rem}
#banner.show{display:block}
.panel{display:none;flex:1;min-height:0;overflow:auto;padding:14px 18px}
.panel.active{display:flex;flex-direction:column}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(280px,1fr));gap:12px}
.card{background:var(--card);border:1px solid var(--bd);border-radius:var(--radius);overflow:hidden}
.card-head{display:flex;justify-content:space-between;align-items:center;padding:8px 10px;font-size:.82rem;border-bottom:1px solid var(--bd)}
.card-head .name{font-weight:600}
.card-head .st{color:var(--mut);font-size:.74rem;text-transform:capitalize}
.card-head .st.ok{color:var(--ok)}
.card-head .st.bad{color:var(--al)}
.card-media{background:#000;aspect-ratio:16/9;cursor:zoom-in}
.card-media img{display:block;width:100%;height:100%;object-fit:contain}
.card-foot{font-size:.74rem;color:var(--mut);padding:7px 10px;display:flex;justify-content:space-between}
.card-recent{margin:0;padding:6px 10px 8px;list-style:none;font-size:.72rem;color:var(--mut);border-top:1px solid var(--bd);min-height:2.6em}
.card-recent li{display:flex;justify-content:space-between;gap:8px;padding:2px 0;cursor:pointer}
.card-recent li:hover{color:var(--tx)}
.card-recent .al{color:#ffb4b4}
.filters{display:flex;flex-wrap:wrap;gap:8px;align-items:end;margin:0 0 10px;flex:0 0 auto}
.filters label{display:flex;flex-direction:column;gap:3px;font-size:.65rem;color:var(--mut);font-weight:650;text-transform:uppercase;letter-spacing:.04em}
.filters select,.filters input[type="date"]{
  background:var(--surface);border:1px solid var(--bd);color:var(--tx);
  padding:6px 8px;border-radius:8px;font:inherit;font-size:.84rem;min-width:8rem
}
.filter-count{color:var(--mut);font-size:.8rem;align-self:center;text-transform:none;letter-spacing:0;font-weight:500}
.split{display:grid;grid-template-columns:minmax(300px,380px) 1fr;gap:12px;flex:1;min-height:0}
@media (max-width:900px){.split{grid-template-columns:1fr}}
.split-list{min-width:0;display:flex;flex-direction:column;min-height:0;background:var(--card);border:1px solid var(--bd);border-radius:var(--radius);overflow:hidden}
.ev-scroll{flex:1;overflow:auto}
.day{position:sticky;top:0;z-index:1;background:#1c222c;color:var(--mut);font-size:.7rem;font-weight:650;letter-spacing:.04em;text-transform:uppercase;padding:6px 12px;border-bottom:1px solid var(--bd)}
.ev{display:grid;grid-template-columns:96px 1fr;gap:10px;padding:8px 10px;border-bottom:1px solid var(--bd);cursor:pointer}
.ev:hover{background:rgba(106,168,232,.08)}
.ev.selected{background:rgba(106,168,232,.16)}
.ev-thumb{width:96px;height:64px;object-fit:cover;border-radius:4px;background:#000;border:1px solid var(--bd)}
.ev-thumb-empty{width:96px;height:64px;border-radius:4px;border:1px dashed var(--bd);color:var(--mut);display:flex;align-items:center;justify-content:center;font-size:.68rem}
.ev-meta{min-width:0}
.ev-top{display:flex;gap:6px;align-items:center;font-size:.82rem;font-weight:600}
.ev-when{color:var(--mut);font-size:.74rem;font-family:var(--mono);margin-top:2px}
.ev-det{color:var(--tx);font-size:.78rem;margin-top:2px;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}
.split-detail{min-width:0;background:var(--card);border:1px solid var(--bd);border-radius:var(--radius);display:flex;flex-direction:column;overflow:hidden}
.detail-stage{flex:1;min-height:240px;background:#000;display:flex;align-items:center;justify-content:center}
.detail-stage img{max-width:100%;max-height:100%;object-fit:contain;cursor:zoom-in}
.detail-stage .placeholder{color:var(--mut);padding:24px;text-align:center}
.detail-meta{padding:12px 14px;border-top:1px solid var(--bd);font-size:.86rem}
.detail-meta h3{margin:0 0 4px;font-size:1rem}
.chips{display:flex;flex-wrap:wrap;gap:6px;margin-top:8px}
.chip{background:var(--surface);border:1px solid var(--bd);border-radius:999px;padding:3px 9px;font-size:.75rem}
.badge{display:inline-block;padding:1px 7px;border-radius:999px;font-size:.66rem;font-weight:650;text-transform:capitalize}
.badge.alert{background:rgba(232,93,93,.18);color:#ffb4b4;border:1px solid rgba(232,93,93,.35)}
.badge.motion{background:rgba(106,168,232,.15);color:#a8d0f0;border:1px solid rgba(106,168,232,.3)}
.more-wrap{padding:10px;text-align:center;border-top:1px solid var(--bd)}
.empty{color:var(--mut);padding:28px;text-align:center}
.toast{
  position:fixed;right:16px;bottom:16px;max-width:320px;background:var(--card);
  border:1px solid var(--acc);padding:12px 14px;border-radius:var(--radius);display:none;z-index:40;font-size:.86rem
}
.toast.show{display:block}
.toast img{max-width:100%;margin-top:8px;border-radius:6px}
.lightbox{position:fixed;inset:0;background:rgba(0,0,0,.92);display:none;z-index:50;align-items:center;justify-content:center;padding:16px;cursor:zoom-out}
.lightbox.show{display:flex}
.lightbox img{max-width:100%;max-height:100%;object-fit:contain}
.set-table{width:100%;border:1px solid var(--bd);border-radius:var(--radius);overflow:auto;background:var(--card)}
.set-table table{width:100%;border-collapse:collapse;font-size:.84rem}
.set-table th,.set-table td{text-align:left;padding:10px 12px;border-bottom:1px solid var(--bd)}
.set-table input{width:5.2rem;background:var(--surface);border:1px solid var(--bd);color:var(--tx);padding:5px 7px;border-radius:6px;font:inherit}
.set-actions{margin-top:12px;display:flex;gap:10px;align-items:center}
#setMsg{color:var(--mut);font-size:.84rem}
#setMsg.ok{color:var(--ok)}
#setMsg.bad{color:var(--al)}
.mask-grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(300px,1fr));gap:12px;margin-top:16px}
.mask-card{background:var(--card);border:1px solid var(--bd);border-radius:var(--radius);overflow:hidden}
.mask-stage{position:relative;background:#000}
.mask-stage img{width:100%;height:auto;display:block}
.mask-stage canvas{position:absolute;left:0;top:0;width:100%;height:100%;cursor:crosshair}
.mask-help{font-size:.74rem;color:var(--mut);padding:8px 10px}
</style></head><body>
<header>
  <div class="brand"><h1>Motion <span>CAMotion</span></h1></div>
  <nav>
    <button type="button" class="active" data-tab="live">Live</button>
    <button type="button" data-tab="events">Events</button>
    <button type="button" data-tab="settings">Settings</button>
  </nav>
  <div class="header-right">
    <span class="status-pill" id="connPill"><span class="dot"></span><span id="conn">Connecting…</span></span>
    <button type="button" class="btn" id="btnNotify">Notifications</button>
  </div>
</header>
<div id="banner"></div>
<div id="tab-live" class="panel active">
  <div class="grid">`)
	for i, w := range workers {
		name := w.Name()
		st := w.Status()
		fmt.Fprintf(&b, `<div class="card"><div class="card-head"><span class="name">%s</span><span class="st" data-st="%s">%s</span></div>`, name, name, st)
		fmt.Fprintf(&b, `<div class="card-media"><img data-cam="%s" data-delay="%d" alt="%s" src=""></div>`, name, i*150, name)
		fmt.Fprintf(&b, `<div class="card-foot"><a href="/snap/%s.jpg" target="_blank" rel="noopener">Open JPEG</a><span data-age="%s">…</span></div>`, name, name)
		fmt.Fprintf(&b, `<ul class="card-recent" data-recent="%s"></ul></div>`, name)
	}
	b.WriteString(`</div></div>
<div id="tab-events" class="panel">
  <div id="events-filters"></div>
  <div class="split">
    <div class="split-list">
      <div class="ev-scroll" id="events-list"></div>
    </div>
    <div class="split-detail" id="event-detail">
      <div class="detail-stage"><div class="placeholder">Select an event.</div></div>
    </div>
  </div>
</div>
<div id="tab-settings" class="panel">
  <div class="set-table" id="settings-table"></div>
  <p class="mask-help">Ignore regions (OSD clock, etc.): drag a box on the snap. Double-click a box to remove it. Save applies immediately.</p>
  <div class="mask-grid" id="mask-editors"></div>
  <div class="set-actions">
    <button type="button" class="btn" id="btnSaveSettings">Save</button>
    <span id="setMsg"></span>
  </div>
</div>
<div class="toast" id="toast"></div>
<div class="lightbox" id="lightbox"><img alt=""></div>
<script>
var CAMS=` + cameraNamesJSON(workers) + `;
` + webAppJS + `
</script></body></html>`)
	return b.String()
}

const webAppJS = `
(function(){
  var OK_MS=1500, ERR_MS=2000, PAGE=50;
  var CLASSES=['person','animal','car','truck','bus'];
  var st={rows:[],total:0,hasMore:false,selected:null,loading:false};

  function esc(s){ return String(s).replace(/[&<>"']/g,function(c){return {'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c];}); }
  function fmtWhen(iso){
    try{
      var d=new Date(iso);
      if(isNaN(d.getTime())) return iso;
      return d.toLocaleString(undefined,{month:'short',day:'2-digit',hour:'2-digit',minute:'2-digit',second:'2-digit'});
    }catch(e){ return iso; }
  }
  function fmtDay(iso){
    try{ return new Date(iso).toLocaleDateString(undefined,{weekday:'short',year:'numeric',month:'short',day:'numeric'}); }
    catch(e){ return ''; }
  }
  function media(rel){ return '/media/'+esc(rel); }

  function tick(img){
    var cam=img.getAttribute('data-cam'); if(!cam) return;
    if(img.getAttribute('data-busy')==='1') return;
    if(document.hidden){ setTimeout(function(){tick(img);}, OK_MS); return; }
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
    img.addEventListener('click', function(){ if(img.src) openLight(img.src); });
  });

  document.querySelectorAll('nav button[data-tab]').forEach(function(btn){
    btn.addEventListener('click', function(){
      document.querySelectorAll('nav button').forEach(function(b){ b.classList.remove('active'); });
      document.querySelectorAll('.panel').forEach(function(p){ p.classList.remove('active'); });
      btn.classList.add('active');
      document.getElementById('tab-'+btn.getAttribute('data-tab')).classList.add('active');
      if(btn.getAttribute('data-tab')==='settings') loadSettings();
      if(btn.getAttribute('data-tab')==='events' && st.selected){
        var e=st.rows.find(function(x){return x.id===st.selected;});
        if(e) showDetail(e);
      }
    });
  });

  function isoSince(d){ return d ? new Date(d+'T00:00:00').toISOString() : ''; }
  function isoUntil(d){
    if(!d) return '';
    var dt=new Date(d+'T00:00:00'); dt.setDate(dt.getDate()+1); return dt.toISOString();
  }
  function val(f){
    var n=document.querySelector('#events-filters [data-f="'+f+'"]');
    return n?n.value:'';
  }
  function readFilters(){
    var since=val('since'), until=val('until');
    return {type:val('type'), cam:val('cam'), class:val('class'), since:isoSince(since), until:isoUntil(until)};
  }
  function hasActiveFilters(f){ return !!(f.type||f.cam||f.class||f.since||f.until); }

  function eventMatches(e, f){
    if(!e) return false;
    if(f.type && e.type!==f.type) return false;
    if(f.cam && e.cam!==f.cam) return false;
    if(f.class){
      var want=f.class.toLowerCase();
      var ok=((e.main_class||'').toLowerCase()===want);
      (e.dets||[]).forEach(function(d){ if((d.class||'').toLowerCase()===want) ok=true; });
      if(!ok) return false;
    }
    if(f.since && new Date(e.at)<new Date(f.since)) return false;
    if(f.until && new Date(e.at)>=new Date(f.until)) return false;
    return true;
  }

  function filterBarHTML(){
    var cams=CAMS||[];
    var camOpts='<option value="">All cameras</option>'+cams.map(function(c){return '<option value="'+esc(c)+'">'+esc(c)+'</option>';}).join('');
    return '<div class="filters">'
      +'<label>Type <select data-f="type"><option value="">All</option><option value="alert">Alerts</option><option value="motion">Motion</option></select></label>'
      +'<label>Camera <select data-f="cam">'+camOpts+'</select></label>'
      +'<label>Class <select data-f="class"><option value="">Any</option>'
      +CLASSES.map(function(c){return '<option value="'+c+'">'+c+'</option>';}).join('')
      +'</select></label>'
      +'<label>From <input type="date" data-f="since"></label>'
      +'<label>To <input type="date" data-f="until"></label>'
      +'<button type="button" class="btn" id="btnReset">Reset</button>'
      +'<span class="filter-count" id="evCount"></span></div>';
  }

  document.getElementById('events-filters').innerHTML=filterBarHTML();
  document.getElementById('btnReset').addEventListener('click', function(){
    document.querySelectorAll('#events-filters select, #events-filters input').forEach(function(n){ n.value=''; });
    loadEvents(true);
  });
  document.querySelectorAll('#events-filters select, #events-filters input').forEach(function(n){
    n.addEventListener('change', function(){ loadEvents(true); });
  });

  function eventQuery(extra){
    var f=readFilters();
    var q='/api/events?limit='+PAGE+'&t='+Date.now();
    if(f.type) q+='&type='+encodeURIComponent(f.type);
    if(f.cam) q+='&cam='+encodeURIComponent(f.cam);
    if(f.class) q+='&class='+encodeURIComponent(f.class);
    if(f.since) q+='&since='+encodeURIComponent(f.since);
    if(f.until) q+='&until='+encodeURIComponent(f.until);
    if(extra && extra.before) q+='&before='+encodeURIComponent(extra.before);
    return q;
  }

  function pageRows(data){
    if(!data) return [];
    if(Array.isArray(data)) return data;
    return data.events||[];
  }

  function loadEvents(reset){
    if(st.loading) return;
    if(reset){ st.rows=[]; st.hasMore=false; st.total=0; }
    var extra={};
    if(!reset && st.rows.length) extra.before=st.rows[st.rows.length-1].at;
    st.loading=true;
    fetch(eventQuery(extra)).then(function(r){return r.json();}).then(function(data){
      var rows=pageRows(data);
      var seen={};
      st.rows.forEach(function(e){ seen[e.id]=true; });
      rows.forEach(function(e){ if(e && e.id && !seen[e.id]){ st.rows.push(e); seen[e.id]=true; } });
      st.total=typeof data.total==='number' ? data.total : st.rows.length;
      st.hasMore=!!data.has_more;
      renderList();
    }).catch(function(err){
      document.getElementById('events-list').innerHTML='<div class="empty">Load failed: '+esc(err)+'</div>';
    }).then(function(){ st.loading=false; });
  }

  function prependEvent(e){
    if(!eventMatches(e, readFilters())) return;
    for(var i=0;i<st.rows.length;i++) if(st.rows[i].id===e.id) return;
    st.rows.unshift(e);
    st.total+=1;
    renderList();
  }

  function detsLine(e){
    if(e.type==='alert'){
      return (e.dets||[]).map(function(d){ return d.class+' '+d.score.toFixed(2); }).join(', ') || (e.main_class||'alert');
    }
    return e.still ? 'Still' : 'Clip';
  }

  function renderList(){
    var el=document.getElementById('events-list');
    var count=document.getElementById('evCount');
    if(count){
      var shown=st.rows.length;
      count.textContent=st.total ? (shown+' of '+st.total) : (shown? String(shown) : '');
    }
    if(!st.rows.length){
      el.innerHTML='<div class="empty">'+(hasActiveFilters(readFilters())?'No events match.':'No events yet.')+'</div>';
      return;
    }
    var h='', lastDay='';
    st.rows.forEach(function(e){
      var day=fmtDay(e.at);
      if(day!==lastDay){ h+='<div class="day">'+esc(day)+'</div>'; lastDay=day; }
      var thumb=e.still ? '<img class="ev-thumb" src="'+media(e.still)+'" alt="" loading="lazy">' : '<div class="ev-thumb-empty">—</div>';
      h+='<div class="ev'+(st.selected===e.id?' selected':'')+'" data-id="'+esc(e.id)+'">'
        +thumb
        +'<div class="ev-meta"><div class="ev-top"><span class="badge '+esc(e.type)+'">'+esc(e.type)+'</span> '+esc(e.cam)+'</div>'
        +'<div class="ev-when">'+esc(fmtWhen(e.at))+'</div>'
        +'<div class="ev-det">'+esc(detsLine(e))+'</div></div></div>';
    });
    if(st.hasMore) h+='<div class="more-wrap"><button type="button" class="btn" id="btnMore">Load older</button></div>';
    el.innerHTML=h;
    el.querySelectorAll('.ev[data-id]').forEach(function(row){
      row.addEventListener('click', function(){ selectEvent(row.getAttribute('data-id')); });
    });
    var more=document.getElementById('btnMore');
    if(more) more.addEventListener('click', function(){ loadEvents(false); });
  }

  function selectEvent(id){
    st.selected=id;
    document.querySelectorAll('.ev').forEach(function(x){
      x.classList.toggle('selected', x.getAttribute('data-id')===id);
    });
    var e=st.rows.find(function(x){return x.id===id;});
    showDetail(e);
  }

  function showDetail(e){
    var d=document.getElementById('event-detail');
    if(!e){
      d.innerHTML='<div class="detail-stage"><div class="placeholder">Select an event.</div></div>';
      return;
    }
    var h='<div class="detail-stage">';
    if(e.still) h+='<img src="'+media(e.still)+'" alt="" id="detailImg">';
    else h+='<div class="placeholder">No still.</div>';
    h+='</div><div class="detail-meta"><h3>'+esc(e.cam)+' · <span class="badge '+esc(e.type)+'">'+esc(e.type)+'</span></h3>';
    h+='<p class="ev-when" style="margin:0 0 8px">'+esc(fmtWhen(e.at))+'</p>';
    if(e.clip) h+='<p style="margin:0 0 8px"><a href="'+media(e.clip)+'" download>Download clip</a></p>';
    if(e.dets && e.dets.length){
      h+='<div class="chips">';
      e.dets.forEach(function(x){ h+='<span class="chip"><strong>'+esc(x.class)+'</strong> '+x.score.toFixed(2)+'</span>'; });
      h+='</div>';
    } else if(e.type==='motion'){
      h+='<p style="color:var(--mut);margin:8px 0 0">Motion only — no YOLO box.</p>';
    }
    h+='</div>';
    d.innerHTML=h;
    var img=document.getElementById('detailImg');
    if(img) img.addEventListener('click', function(){ openLight(img.src); });
  }

  function openLight(src){
    var box=document.getElementById('lightbox');
    box.querySelector('img').src=src;
    box.classList.add('show');
  }
  document.getElementById('lightbox').addEventListener('click', function(){ this.classList.remove('show'); });
  document.addEventListener('keydown', function(ev){
    if(ev.key==='Escape') document.getElementById('lightbox').classList.remove('show');
    if(document.getElementById('tab-events').classList.contains('active') && (ev.key==='j'||ev.key==='k')){
      if(!st.rows.length) return;
      var i=st.rows.findIndex(function(x){return x.id===st.selected;});
      if(ev.key==='j') i=Math.min(st.rows.length-1, (i<0?0:i+1));
      else i=Math.max(0, (i<0?0:i-1));
      selectEvent(st.rows[i].id);
      var row=document.querySelector('.ev.selected');
      if(row) row.scrollIntoView({block:'nearest'});
    }
  });

  var conn=document.getElementById('conn');
  var pill=document.getElementById('connPill');
  var banner=document.getElementById('banner');
  var toast=document.getElementById('toast');
  var es, sseBackoff=1000;
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
        prependEvent(e);
        if(e.type==='alert'){
          banner.textContent='Alert · '+e.cam+' · '+(e.main_class||'object')+' '+(e.main_score||0).toFixed(2);
          banner.classList.add('show');
          toast.innerHTML='<div style="font-weight:650;margin-bottom:4px">Alert · '+esc(e.cam)+'</div>'
            +esc(e.main_class||'object')+' · '+(e.main_score||0).toFixed(2)
            +(e.still?'<br><img src="'+media(e.still)+'" alt="">':'');
          toast.classList.add('show');
          setTimeout(function(){ toast.classList.remove('show'); }, 8000);
          if(window.Notification && Notification.permission==='granted'){
            new Notification('Motion CAMotion · '+e.cam, { body: (e.main_class||'object')+' '+(e.main_score||0).toFixed(2) });
          }
        }
      }catch(err){}
    });
    es.onerror=function(){
      setConn('Reconnecting…', 'bad');
      try{es.close();}catch(e){}
      setTimeout(connectSSE, sseBackoff);
      sseBackoff=Math.min(sseBackoff*2, 15000);
    };
  }
  connectSSE();
  loadEvents(true);
  refreshLiveRecent();
  fetch('/api/status').then(function(r){return r.json();}).then(function(data){
    (data.cameras||[]).forEach(function(c){
      var el=document.querySelector('[data-st="'+c.name+'"]');
      if(!el) return;
      el.textContent=c.status||'';
      el.classList.toggle('ok', c.status==='connected');
      el.classList.toggle('bad', c.status && c.status!=='connected');
    });
  }).catch(function(){});

  function refreshLiveRecent(){
    fetch('/api/events?limit=80&t='+Date.now()).then(function(r){return r.json();}).then(function(data){
      var by={};
      pageRows(data).forEach(function(e){
        if(!e) return;
        if(!by[e.cam]) by[e.cam]=[];
        if(by[e.cam].length<3) by[e.cam].push(e);
      });
      document.querySelectorAll('[data-recent]').forEach(function(ul){
        var cam=ul.getAttribute('data-recent');
        var list=by[cam]||[];
        if(!list.length){ ul.innerHTML='<li>No recent events</li>'; return; }
        ul.innerHTML=list.map(function(e){
          var label=e.type==='alert' ? (e.main_class||'alert') : 'motion';
          return '<li class="'+(e.type==='alert'?'al':'')+'" data-jump="'+esc(e.id)+'"><span>'+esc(label)+'</span><span>'+esc(fmtWhen(e.at))+'</span></li>';
        }).join('');
        ul.querySelectorAll('[data-jump]').forEach(function(li){
          li.addEventListener('click', function(){
            document.querySelector('nav button[data-tab="events"]').click();
            st.selected=li.getAttribute('data-jump');
            loadEvents(true);
          });
        });
      });
    }).catch(function(){});
  }

  var maskByCam={};

  function normMask(m){
    var x=Math.max(0,Math.min(1,m.x)), y=Math.max(0,Math.min(1,m.y));
    var w=Math.max(0.01,Math.min(1-x,m.w)), h=Math.max(0.01,Math.min(1-y,m.h));
    return {x:x,y:y,w:w,h:h};
  }

  function drawMasks(cv, masks, drag){
    var w=cv.width, h=cv.height;
    var ctx=cv.getContext('2d');
    ctx.clearRect(0,0,w,h);
    (masks||[]).forEach(function(m){
      ctx.fillStyle='rgba(232,93,93,.28)';
      ctx.strokeStyle='rgba(255,180,180,.9)';
      ctx.lineWidth=2;
      ctx.fillRect(m.x*w, m.y*h, m.w*w, m.h*h);
      ctx.strokeRect(m.x*w, m.y*h, m.w*w, m.h*h);
    });
    if(drag){
      ctx.strokeStyle='#6aa8e8';
      ctx.lineWidth=2;
      ctx.strokeRect(drag.x*w, drag.y*h, drag.w*w, drag.h*h);
    }
  }

  function bindMaskEditor(card, name){
    var img=card.querySelector('img');
    var cv=card.querySelector('canvas');
    var drag=null;
    function syncSize(){
      cv.width=img.clientWidth||img.naturalWidth||1;
      cv.height=img.clientHeight||img.naturalHeight||1;
      drawMasks(cv, maskByCam[name], drag);
    }
    img.addEventListener('load', syncSize);
    if(img.complete) syncSize();
    function local(ev){
      var r=cv.getBoundingClientRect();
      return {x:(ev.clientX-r.left)/r.width, y:(ev.clientY-r.top)/r.height};
    }
    cv.addEventListener('pointerdown', function(ev){
      cv.setPointerCapture(ev.pointerId);
      var p=local(ev);
      drag={x0:p.x,y0:p.y,x:p.x,y:p.y,w:0,h:0};
    });
    cv.addEventListener('pointermove', function(ev){
      if(!drag) return;
      var p=local(ev);
      drag.x=Math.min(drag.x0,p.x); drag.y=Math.min(drag.y0,p.y);
      drag.w=Math.abs(p.x-drag.x0); drag.h=Math.abs(p.y-drag.y0);
      drawMasks(cv, maskByCam[name], drag);
    });
    cv.addEventListener('pointerup', function(){
      if(!drag) return;
      if(drag.w>0.02 && drag.h>0.02){
        if(!maskByCam[name]) maskByCam[name]=[];
        if(maskByCam[name].length<8) maskByCam[name].push(normMask(drag));
      }
      drag=null;
      drawMasks(cv, maskByCam[name], null);
    });
    cv.addEventListener('dblclick', function(ev){
      var p=local(ev);
      var list=maskByCam[name]||[];
      for(var i=list.length-1;i>=0;i--){
        var m=list[i];
        if(p.x>=m.x && p.x<=m.x+m.w && p.y>=m.y && p.y<=m.y+m.h){
          list.splice(i,1);
          break;
        }
      }
      maskByCam[name]=list;
      drawMasks(cv, list, null);
    });
    card.querySelector('[data-clear]').addEventListener('click', function(){
      maskByCam[name]=[];
      drawMasks(cv, [], null);
    });
  }

  function loadSettings(){
    fetch('/api/settings').then(function(r){return r.json();}).then(function(data){
      var cams=data.cameras||[];
      var h='<table><thead><tr><th>Camera</th><th>Threshold</th><th>Min blob</th><th>Compact</th><th>Sustain</th></tr></thead><tbody>';
      cams.forEach(function(c){
        h+='<tr data-cam="'+esc(c.name)+'">'
          +'<td>'+esc(c.name)+'</td>'
          +'<td><input data-f="threshold" type="number" step="0.005" min="0.005" max="0.5" value="'+c.threshold+'"></td>'
          +'<td><input data-f="motion_min_blob" type="number" step="1" min="1" max="200" value="'+c.motion_min_blob+'"></td>'
          +'<td><input data-f="motion_min_compact" type="number" step="0.05" min="0" max="1" value="'+c.motion_min_compact+'"></td>'
          +'<td><input data-f="motion_sustain_frames" type="number" step="1" min="1" max="30" value="'+c.motion_sustain_frames+'"></td>'
          +'</tr>';
      });
      h+='</tbody></table>';
      document.getElementById('settings-table').innerHTML=h;
      maskByCam={};
      var mh='';
      cams.forEach(function(c){
        maskByCam[c.name]=(c.motion_masks||[]).map(normMask);
        mh+='<div class="mask-card" data-cam="'+esc(c.name)+'"><div class="card-head"><span class="name">'+esc(c.name)+'</span>'
          +'<button type="button" class="btn" data-clear>Clear</button></div>'
          +'<div class="mask-stage"><img alt="" src="/snap/'+encodeURIComponent(c.name)+'.jpg?t='+Date.now()+'"><canvas></canvas></div></div>';
      });
      document.getElementById('mask-editors').innerHTML=mh;
      document.querySelectorAll('#mask-editors .mask-card').forEach(function(card){
        bindMaskEditor(card, card.getAttribute('data-cam'));
      });
    }).catch(function(err){
      document.getElementById('settings-table').innerHTML='<div class="empty">Load failed: '+esc(err)+'</div>';
    });
  }

  document.getElementById('btnSaveSettings').addEventListener('click', function(){
    var msg=document.getElementById('setMsg');
    var cams=[];
    document.querySelectorAll('#settings-table tr[data-cam]').forEach(function(tr){
      var num=function(f){ return parseFloat(tr.querySelector('[data-f="'+f+'"]').value); };
      var name=tr.getAttribute('data-cam');
      cams.push({
        name: name,
        threshold: num('threshold'),
        motion_min_blob: parseInt(tr.querySelector('[data-f="motion_min_blob"]').value,10),
        motion_min_compact: num('motion_min_compact'),
        motion_sustain_frames: parseInt(tr.querySelector('[data-f="motion_sustain_frames"]').value,10),
        motion_masks: maskByCam[name]||[]
      });
    });
    fetch('/api/settings',{method:'PUT',headers:{'Content-Type':'application/json'},body:JSON.stringify({cameras:cams})})
      .then(function(r){ if(!r.ok) return r.text().then(function(t){ throw new Error(t||r.status); }); return r.json(); })
      .then(function(){ msg.className='ok'; msg.textContent='Saved'; })
      .catch(function(err){ msg.className='bad'; msg.textContent=String(err.message||err); });
  });

  document.getElementById('btnNotify').addEventListener('click', function(){
    if(!window.Notification){ alert('Notifications are not available in this browser.'); return; }
    if(!window.isSecureContext && location.hostname!=='localhost' && location.hostname!=='127.0.0.1'){
      alert("Browser notifications need HTTPS (or open via http://127.0.0.1).");
      return;
    }
    Notification.requestPermission();
  });
})();
`
