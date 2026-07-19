package surfserve

// kvmPage is de ingebouwde browser-KVM (docs/gui-ontwerp.md §6, trap 1):
// scherm kijken via /screen.png, muis en toetsen terug via POST /input.
// Geen install, geen websockets — een <img>-verversing en fetch() volstaan
// voor P1; mousemove is client-side gesmoord tot ~30/s (last-write-wins).
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
<img id="s" src="/screen.png" tabindex="0" draggable="false">
<script>
"use strict";
const img = document.getElementById("s");
setInterval(() => { img.src = "/screen.png?" + Date.now(); }, 250);
function post(o) { fetch("/input", {method: "POST", body: JSON.stringify(o)}); }
function pos(e) {
  const r = img.getBoundingClientRect();
  const sx = img.naturalWidth / r.width, sy = img.naturalHeight / r.height;
  return {x: Math.round((e.clientX - r.left) * sx), y: Math.round((e.clientY - r.top) * sy)};
}
let moveT = null, lastMove = null;
img.addEventListener("mousemove", e => {
  lastMove = pos(e);
  if (moveT) return;
  moveT = setTimeout(() => { moveT = null; post({k: "move", ...lastMove}); }, 33);
});
img.addEventListener("mousedown", e => { img.focus(); post({k: "btn", c: e.button, v: 1, ...pos(e)}); e.preventDefault(); });
img.addEventListener("mouseup",   e => { post({k: "btn", c: e.button, v: 0, ...pos(e)}); e.preventDefault(); });
img.addEventListener("contextmenu", e => e.preventDefault());
img.addEventListener("wheel", e => { post({k: "wheel", v: Math.sign(e.deltaY), ...pos(e)}); e.preventDefault(); });
img.addEventListener("keydown", e => { if (!e.repeat) post({k: "key", c: e.keyCode, v: 1}); e.preventDefault(); });
img.addEventListener("keyup",   e => { post({k: "key", c: e.keyCode, v: 0}); e.preventDefault(); });
</script></body></html>`
