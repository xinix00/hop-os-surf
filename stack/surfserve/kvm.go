package surfserve

// kvmPage is de ingebouwde browser-KVM (docs/gui-ontwerp.md §6, trap 1):
// kijken via de damage-stream (/stream: alleen veranderde rechthoeken, met
// putImageData op een canvas — idle scherm = nul bytes, geen PNG-encodes
// display-side), met /screen.png-polling als fallback. Muis en toetsen terug
// via POST /input. Geen install, geen websockets: fetch() leest de chunked
// response als ReadableStream. Het canvas heeft altijd een geldige maat en
// pos() geeft liever nil dan een verzonnen (0,0) — de "muis schiet naar
// linksboven"-bug van de <img>-polling (Derek, 19-07, live op de eerste
// demo). Mousemove is client-side gesmoord tot ~15/s (last-write-wins).
// Paginatekst in het Engels: dit is scherm-output.
const kvmPage = `<!doctype html>
<html><head><meta charset="utf-8"><title>HopOS display</title><style>
  body{margin:0;background:#0a101c;color:#f0f4ff;font-family:ui-monospace,monospace}
  #bar{padding:6px 10px;background:#101828;border-bottom:1px solid #3a4a6a;font-size:13px}
  #bar b{color:#6ea8ff}
  #s{display:block;margin:10px auto;outline:none;image-rendering:pixelated;
     border:1px solid #3a4a6a;max-width:calc(100vw - 20px)}
</style></head><body>
<div id="bar"><b>HopOS web-KVM</b> — click the screen to focus a window; keys and mouse are forwarded</div>
<canvas id="s" tabindex="0"></canvas>
<script>
"use strict";
const cv = document.getElementById("s");
const ctx = cv.getContext("2d");
// Frame: u32 len | u16 nRects | nRects×(x,y,w,h u16) | RGBA-pixels aaneen.
function paint(p) {
  const dv = new DataView(p.buffer, p.byteOffset, p.byteLength);
  const n = dv.getUint16(0, true);
  let off = 2, pix = 2 + n * 8;
  for (let i = 0; i < n; i++) {
    const x = dv.getUint16(off, true), y = dv.getUint16(off + 2, true);
    const w = dv.getUint16(off + 4, true), h = dv.getUint16(off + 6, true);
    off += 8;
    if (x === 0 && y === 0 && (w > cv.width || h > cv.height)) {
      cv.width = w; cv.height = h; // eerste (volledige) frame zet de maat
    }
    const data = new Uint8ClampedArray(p.buffer, p.byteOffset + pix, w * h * 4);
    ctx.putImageData(new ImageData(data, w, h), x, y);
    pix += w * h * 4;
  }
}
const canInflate = typeof DecompressionStream !== "undefined";
async function inflate(u8) {
  const ds = new DecompressionStream("deflate-raw");
  const st = new Blob([u8]).stream().pipeThrough(ds);
  return new Uint8Array(await new Response(st).arrayBuffer());
}
async function stream() {
  try {
    const resp = await fetch(canInflate ? "/stream?z=1" : "/stream");
    if (!resp.ok || !resp.body) throw new Error(resp.status);
    const rd = resp.body.getReader();
    let buf = new Uint8Array(0);
    for (;;) {
      const {done, value} = await rd.read();
      if (done) break;
      if (buf.length === 0) { buf = value; } else {
        const nb = new Uint8Array(buf.length + value.length);
        nb.set(buf); nb.set(value, buf.length); buf = nb;
      }
      for (;;) {
        if (buf.length < 4) break;
        const len = new DataView(buf.buffer, buf.byteOffset).getUint32(0, true);
        if (buf.length < 4 + len) break;
        const body = buf.subarray(4, 4 + len);
        paint(canInflate ? await inflate(body) : body);
        buf = buf.subarray(4 + len);
      }
    }
  } catch (e) { pollOnce(); } // display weg of oude versie: even pollen
  setTimeout(stream, 1000);   // en de stream opnieuw proberen
}
function pollOnce() {
  const im = new Image();
  im.onload = () => {
    if (cv.width !== im.width || cv.height !== im.height) { cv.width = im.width; cv.height = im.height; }
    ctx.drawImage(im, 0, 0);
  };
  im.src = "/screen.png?" + Date.now();
}
stream();
// Eén reconnect-pad met één timer: onerror sluit alleen (dat triggert
// onclose), onclose plant precies één poging — anders verdubbelt elke
// disconnect het aantal lussen ("input gaat bezurk", Derek 19-07).
let ws = null, wsTimer = null;
function scheduleWS() { if (!wsTimer) wsTimer = setTimeout(() => { wsTimer = null; wsConnect(); }, 1000); }
function wsConnect() {
  let s;
  const u = (location.protocol === "https:" ? "wss://" : "ws://") + location.host + "/input";
  try { s = new WebSocket(u); } catch (e) { scheduleWS(); return; }
  s.onopen = () => { ws = s; };
  s.onclose = () => { if (ws === s) ws = null; scheduleWS(); };
  s.onerror = () => { try { s.close(); } catch (e) {} };
}
wsConnect();
function post(o) {
  const j = JSON.stringify(o);
  if (ws && ws.readyState === 1) { try { ws.send(j); } catch (e) {} }
  // Zonder socket: moves droppen (verse-waarde-stroom) en de rest via een
  // stille POST — geen fetch-storm terwijl de display weg is.
  else if (o.k !== "move") { fetch("/input", {method: "POST", body: j}).catch(() => {}); }
}
function pos(e) {
  const r = cv.getBoundingClientRect();
  if (!cv.width || !r.width) return null; // nog geen frame: liever niks dan (0,0)
  return {x: Math.round((e.clientX - r.left) * cv.width / r.width),
          y: Math.round((e.clientY - r.top) * cv.height / r.height)};
}
let moveT = null, lastMove = null;
cv.addEventListener("mousemove", e => {
  lastMove = pos(e);
  if (moveT || !lastMove) return;
  moveT = setTimeout(() => { moveT = null; if (lastMove) post({k: "move", ...lastMove}); }, 33);
});
cv.addEventListener("mousedown", e => { cv.focus(); const p = pos(e); if (p) post({k: "btn", c: e.button, v: 1, ...p}); e.preventDefault(); });
cv.addEventListener("mouseup",   e => { const p = pos(e); if (p) post({k: "btn", c: e.button, v: 0, ...p}); e.preventDefault(); });
cv.addEventListener("contextmenu", e => e.preventDefault());
cv.addEventListener("wheel", e => { const p = pos(e); if (p) post({k: "wheel", v: Math.sign(e.deltaY), ...p}); e.preventDefault(); });
cv.addEventListener("keydown", e => { if (!e.repeat) post({k: "key", c: e.keyCode, v: 1}); e.preventDefault(); });
cv.addEventListener("keyup",   e => { post({k: "key", c: e.keyCode, v: 0}); e.preventDefault(); });
</script></body></html>`
