package surfserve

// kvmPage is de ingebouwde browser-KVM (docs/gui-ontwerp.md §6, trap 1):
// scherm kijken via /screen.png, muis en toetsen terug via POST /input.
// Geen install, geen websockets. Het beeld wordt dubbel gebufferd in een
// canvas getekend: een <img> die z'n src wisselt heeft tijdens elke reload
// even naturalWidth=0, en dan klapt de coördinaat-schaal naar (0,0) — de
// "muis schiet naar linksboven / klik mist"-bug (Derek, 19-07, live op de
// eerste demo). Het canvas heeft altijd een geldige maat en pos() geeft
// liever nil dan een verzonnen (0,0). Mousemove is client-side gesmoord tot
// ~15/s (last-write-wins): elke move maakt display-side het scherm vuil en
// dus een verse PNG — op een emulated core wil je die kraan niet vol open.
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
function refresh() {
  const im = new Image();
  im.onload = () => {
    if (cv.width !== im.width || cv.height !== im.height) {
      cv.width = im.width; cv.height = im.height;
    }
    ctx.drawImage(im, 0, 0);
    setTimeout(refresh, 200);
  };
  im.onerror = () => setTimeout(refresh, 1000);
  im.src = "/screen.png?" + Date.now();
}
refresh();
function post(o) { fetch("/input", {method: "POST", body: JSON.stringify(o)}); }
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
  moveT = setTimeout(() => { moveT = null; if (lastMove) post({k: "move", ...lastMove}); }, 66);
});
cv.addEventListener("mousedown", e => { cv.focus(); const p = pos(e); if (p) post({k: "btn", c: e.button, v: 1, ...p}); e.preventDefault(); });
cv.addEventListener("mouseup",   e => { const p = pos(e); if (p) post({k: "btn", c: e.button, v: 0, ...p}); e.preventDefault(); });
cv.addEventListener("contextmenu", e => e.preventDefault());
cv.addEventListener("wheel", e => { const p = pos(e); if (p) post({k: "wheel", v: Math.sign(e.deltaY), ...p}); e.preventDefault(); });
cv.addEventListener("keydown", e => { if (!e.repeat) post({k: "key", c: e.keyCode, v: 1}); e.preventDefault(); });
cv.addEventListener("keyup",   e => { post({k: "key", c: e.keyCode, v: 0}); e.preventDefault(); });
</script></body></html>`
