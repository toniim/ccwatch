"use strict";
const $ = (id) => document.getElementById(id);
const SPIN = "|/-\\";
let spinN = 0;
let es = null;
const pinned = new URLSearchParams(location.search).get("session") || "";
let follow = !pinned;   // ?session=<id> pins that session on load
let current = pinned;   // session id the tree highlights
let view = "dash";
let lastData = null;
let lastList = "";
let sessions = [];
const seenLog = new Set();
let firstLog = true;

// h builds a DOM node; text always goes through textContent (event data is untrusted).
function h(tag, attrs, ...kids) {
  const n = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (k === "class") n.className = v; else n.setAttribute(k, v);
  }
  for (const k of kids) n.append(k instanceof Node ? k : document.createTextNode(String(k)));
  return n;
}
const tok = (n) => n >= 1e6 ? (n / 1e6).toFixed(1) + "M" : n >= 1e3 ? (n / 1e3).toFixed(1) + "k" : String(n || 0);
const base = (p) => (p || "").replace(/\/+$/, "").split("/").pop() || p || "";
const clock = (ts) => ts ? new Date(ts).toLocaleTimeString("en-GB") : "--:--:--";

function setLive(ok) {
  const p = $("live");
  p.textContent = ok ? "live" : "disconnected";
  p.className = "pill" + (ok ? "" : " off");
}

// syncURL keeps ?session=<id> in step with the pin: set while pinned, gone while following latest.
function syncURL() {
  const u = new URL(location.href);
  if (follow || !current) u.searchParams.delete("session"); else u.searchParams.set("session", current);
  if (u.href !== location.href) history.replaceState(null, "", u);
}

function connect() {
  if (es) es.close();
  const q = follow || !current ? "latest" : current;
  syncURL();
  es = new EventSource("/api/stream?session=" + encodeURIComponent(q));
  es.onopen = () => setLive(true);
  es.onerror = () => setLive(false);
  es.addEventListener("state", (e) => { lastData = JSON.parse(e.data); render(lastData); });
  es.addEventListener("sessions", (e) => { if (e.data !== lastList) { lastList = e.data; sessions = JSON.parse(e.data); renderTree(); } });
}

// ---- session tree (sidebar) ----
const DESKTOP = window.matchMedia("(min-width: 900px)");
const store = {
  get(k) { try { return localStorage.getItem(k); } catch (e) { return null; } },
  set(k, v) { try { localStorage.setItem(k, v); } catch (e) { /* storage may be unavailable */ } },
};
let expanded = {};  // node key -> explicit expand (true) / collapse (false); unset = default
try { expanded = JSON.parse(store.get("ccwatch.tree") || "{}") || {}; } catch (e) { expanded = {}; }
const isOpen = (key, dflt) => key in expanded ? expanded[key] : dflt;
function setOpen(key, v) { expanded[key] = v; store.set("ccwatch.tree", JSON.stringify(expanded)); }

const dirName = (s) => base(s.cwd) || s.id.slice(0, 8);
// nameNode keeps the end of a long name: the head shrinks with an ellipsis, the last TAIL characters stay.
// cut chars of a shared prefix are dropped and shown as a muted "…".
const TAIL = 9;
function nameNode(name, cut) {
  const n = h("span", { class: "dir" });
  if (cut) { n.append(h("span", { class: "pre" }, "…")); name = name.slice(cut); }
  const chars = [...name];
  if (chars.length <= TAIL + 2) n.append(h("span", { class: "h" }, name));
  else n.append(h("span", { class: "h" }, chars.slice(0, -TAIL).join("")), h("span", { class: "t" }, h("span", {}, chars.slice(-TAIL).join(""))));
  return n;
}
// sharedCut returns how many leading characters every name has in common, cut back to a
// separator and only for 2+ names, so "ccwatch-" goes but no name is ever stripped whole.
function sharedCut(names) {
  if (names.length < 2) return 0;
  let p = names[0];
  for (const nm of names) { let i = 0; while (i < p.length && i < nm.length && p[i] === nm[i]) i++; p = p.slice(0, i); }
  const m = /^.*[-_./ ]/.exec(p);
  const cut = m ? m[0].length : 0;
  return names.every((nm) => nm.length > cut) ? cut : 0;
}
const runText = (r) => "orca run: " + (r.objective || r.id);

// buildTree turns the flat /api/sessions list into nodes: coordinators and
// standalone sessions at the roots, a run node per Run under a coordinator
// (a synthetic one at the root for workers whose coordinator is unknown),
// workers under their run, nested sessions under their parent.
function buildTree(list) {
  const byId = new Map(list.map((s) => [s.id, s]));
  const sess = (s) => ({ key: s.id, kind: "session", s, kids: [] });
  const nodes = new Map(list.map((s) => [s.id, sess(s)]));
  const roots = [];
  const runNodes = new Map(); // coordinator|run id -> run node
  const runNode = (owner, run) => {
    const key = "run:" + (owner || "") + ":" + run.id;
    if (!runNodes.has(key)) runNodes.set(key, { key, kind: "run", run, kids: [] });
    return runNodes.get(key);
  };
  for (const s of list) if (s.role === "coordinator" && s.run) { const rn = runNode(s.id, s.run); nodes.get(s.id).kids.push(rn); }
  for (const s of list) {
    const n = nodes.get(s.id);
    const par = s.parent && byId.has(s.parent) ? s.parent : "";
    if (s.role === "worker" && s.run) {
      if (par) { const rn = runNode(par, s.run); if (!nodes.get(par).kids.includes(rn)) nodes.get(par).kids.push(rn); rn.kids.push(n); }
      else { const rn = runNode("", s.run); if (!roots.includes(rn)) roots.push(rn); rn.kids.push(n); }
    } else if (par) nodes.get(par).kids.push(n);
    else roots.push(n);
  }
  return roots;
}
const nodeEnded = (n) => n.kind === "session" ? n.s.ended : n.kids.length > 0 && n.kids.every(nodeEnded);

function nodeRow(n, depth, selId, cut) {
  const hasKids = n.kids.length > 0;
  const open = hasKids && isOpen(n.key, true);
  const arw = h("button", { class: "arw" + (hasKids ? "" : " none"), type: "button", "aria-label": open ? "collapse" : "expand", tabindex: hasKids ? "0" : "-1" }, open ? "▾" : "▸");
  arw.addEventListener("click", () => { setOpen(n.key, !open); renderTree(); });
  let sel, cls = "row";
  if (n.kind === "session") {
    const s = n.s;
    sel = h("button", { class: "sel", type: "button", title: s.label },
      h("span", { class: "dot " + s.state }), nameNode(dirName(s), cut));
    if (s.subagents > 0) sel.append(h("span", { class: "badge", title: s.live + " of " + (s.subagents === 1 ? "1 agent" : s.subagents + " agents") + " running" }, s.live + "/" + s.subagents));
    if (s.profile) sel.append(h("span", { class: "prof", title: "profile " + s.profile }, s.profile));
    sel.append(h("span", { class: "model" }, s.model || ""));
    sel.append(h("span", { class: "sid" }, s.id.slice(0, 8)));
    sel.addEventListener("click", () => pick(s.id));
    if (s.id === selId) cls += " cur";
    if (s.ended) cls += " ended-row";
  } else {
    const t = runText(n.run);
    sel = h("button", { class: "sel", type: "button", title: t }, h("span", { class: "txt" }, t));
    sel.addEventListener("click", () => { setOpen(n.key, !open); renderTree(); });
    cls += " runnode";
  }
  const row = h("div", { class: cls, role: "treeitem", style: "--d:" + depth }, arw, sel);
  if (hasKids) row.setAttribute("aria-expanded", String(open));
  return row;
}

function paint(box, n, depth, selId, cut) {
  box.append(nodeRow(n, depth, selId, cut));
  if (!n.kids.length || !isOpen(n.key, true)) return;
  const sib = n.kind === "run" ? sharedCut(n.kids.filter((k) => k.kind === "session").map((k) => dirName(k.s))) : 0;
  for (const k of n.kids) paint(box, k, depth + 1, selId, k.kind === "session" ? sib : 0);
}

// pathTo lists the node keys above a session, so a selection inside a collapsed branch can be revealed.
function pathTo(nodes, id, trail = []) {
  for (const n of nodes) {
    if (n.kind === "session" && n.s.id === id) return trail;
    const r = pathTo(n.kids, id, trail.concat(n.key));
    if (r) return r;
  }
  return null;
}

let revealed = "";
function renderTree() {
  const box = $("tree");
  box.textContent = "";
  if (!sessions.length) { box.append(h("div", { class: "muted" }, "no sessions yet")); return; }
  const roots = buildTree(sessions);
  const active = roots.filter((n) => !nodeEnded(n));
  const ended = roots.filter(nodeEnded);
  const grp = { key: "group:ended", kind: "group", kids: ended };
  if (current && current !== revealed) { // reveal the selection once, after it changed
    const inAct = pathTo(active, current);
    const inEnd = pathTo(ended, current);
    if (inEnd) for (const k of ["group:ended", ...inEnd]) if (!isOpen(k, k !== "group:ended")) setOpen(k, true);
    if (inAct) for (const k of inAct) if (!isOpen(k, true)) setOpen(k, true);
    if (inAct || inEnd) revealed = current;
  }
  for (const n of active) paint(box, n, 0, current, 0);
  if (ended.length) {
    const open = isOpen(grp.key, false);
    const arw = h("button", { class: "arw", type: "button", "aria-label": open ? "collapse ended" : "expand ended" }, open ? "▾" : "▸");
    const sel = h("button", { class: "sel", type: "button" }, h("span", { class: "txt" }, "ended (" + ended.length + ")"));
    const toggle = () => { setOpen(grp.key, !open); renderTree(); };
    arw.addEventListener("click", toggle);
    sel.addEventListener("click", toggle);
    const row = h("div", { class: "row group", role: "treeitem", "aria-expanded": String(open), style: "--d:0" }, arw, sel);
    box.append(row);
    if (open) for (const n of ended) paint(box, n, 1, current, 0);
  }
}

function pick(id) {
  current = id;
  follow = false;
  $("follow").checked = false;
  firstLog = true;
  if (!DESKTOP.matches) setSide(false);
  renderTree();
  connect();
}

// ---- sidebar visibility ----
function setSide(on, save) {
  document.body.classList.toggle("side-on", on);
  document.body.classList.toggle("side-off", !on);
  $("side-toggle").setAttribute("aria-expanded", String(on));
  $("scrim").hidden = DESKTOP.matches || !on;
  if (save && DESKTOP.matches) store.set("ccwatch.side", on ? "1" : "0");
}
const sideOn = () => document.body.classList.contains("side-on");
function initSide() { // wide: the stored choice, shown by default; narrow: a drawer, hidden by default
  const v = store.get("ccwatch.side");
  setSide(DESKTOP.matches ? v !== "0" : false);
}
$("side-toggle").addEventListener("click", () => setSide(!sideOn(), true));
$("scrim").addEventListener("click", () => setSide(false));
DESKTOP.addEventListener("change", initSide);
document.addEventListener("keydown", (e) => {
  if (e.ctrlKey || e.metaKey || e.altKey) return;
  const t = e.target;
  const typing = t && (t.isContentEditable || ["INPUT", "TEXTAREA", "SELECT"].includes(t.tagName) && t.type !== "checkbox");
  if (e.key === "[" && !typing) { e.preventDefault(); setSide(!sideOn(), true); }
  else if (e.key === "Escape" && !DESKTOP.matches && sideOn()) setSide(false);
});
initSide();

// ---- sidebar width: drag the right edge, double-click resets ----
const W_MIN = 240, W_MAX = 560, W_DEF = 340;
const clampW = (w) => Math.max(W_MIN, Math.min(W_MAX, Math.round(w)));
function setWidth(w, save) {
  $("layout").style.setProperty("--side-w", w + "px");
  $("side-resize").setAttribute("aria-valuenow", String(w));
  if (save) { if (w === W_DEF) store.set("ccwatch.sideW", ""); else store.set("ccwatch.sideW", String(w)); }
}
const storedW = parseInt(store.get("ccwatch.sideW") || "", 10);
setWidth(storedW >= W_MIN && storedW <= W_MAX ? storedW : W_DEF);
(() => {
  const bar = $("side-resize");
  let width = 0;
  bar.setAttribute("aria-valuemin", String(W_MIN)); bar.setAttribute("aria-valuemax", String(W_MAX));
  bar.addEventListener("pointerdown", (e) => {
    if (e.button !== 0) return;
    bar.setPointerCapture(e.pointerId);
    bar.classList.add("drag"); document.body.classList.add("resizing");
    width = $("side").getBoundingClientRect().width;
  });
  bar.addEventListener("pointermove", (e) => {
    if (!bar.hasPointerCapture(e.pointerId)) return;
    width = clampW(e.clientX - $("side").getBoundingClientRect().left);
    setWidth(width);
  });
  const end = (e) => {
    if (!bar.classList.contains("drag")) return;
    bar.classList.remove("drag"); document.body.classList.remove("resizing");
    setWidth(clampW(width), true);
  };
  bar.addEventListener("pointerup", end);
  bar.addEventListener("pointercancel", end);
  bar.addEventListener("dblclick", () => setWidth(W_DEF, true));
  bar.addEventListener("keydown", (e) => {
    const d = e.key === "ArrowLeft" ? -16 : e.key === "ArrowRight" ? 16 : 0;
    if (d) { e.preventDefault(); setWidth(clampW($("side").getBoundingClientRect().width + d), true); }
  });
  // the id column hides when the sidebar is narrower than 300 px (the full label stays in the title)
  new ResizeObserver(() => $("side").classList.toggle("narrow", $("side").getBoundingClientRect().width < 300)).observe($("side"));
})();

function render(d) {
  for (const k of ["agents", "plan", "lanes", "log", "table"]) d[k] = d[k] || []; // never trust null arrays
  if (current !== d.sessionId) { current = d.sessionId; renderTree(); } // follow latest moves the highlight
  $("cwd").textContent = "cwd " + d.cwd + " · session " + d.sessionId + (d.ended ? " · ended" : "");
  renderAgents(d);
  renderMain(d);
  const showTask = d.lanes.length > 0 || !d.orcaLane;
  $("arrow").hidden = !showTask;
  $("arrow").textContent = "» Task tool · " + d.subagents + (d.subagents === 1 ? " subagent" : " subagents") + " · each in its own context window «";
  renderLanes($("lanes"), d.lanes);
  $("orca-arrow").hidden = !d.orcaLane;
  $("orca-arrow").textContent = d.orcaHeader || "";
  renderLanes($("orca-lane"), d.orcaLane ? [d.orcaLane] : []);
  renderBack(d);
  renderLog(d);
  renderTable(d);
  $("foot").textContent = "tests " + d.testsPass + "/" + (d.testsPass + d.testsFail) + " · tool failures " + d.toolFailures +
    " · cost $" + d.costUsd.toFixed(4) + " · peak subagent ctx " + tok(d.peakCtx);
}

function renderAgents(d) {
  const b = $("agents-body");
  b.textContent = "";
  if (!d.agents.length) b.append(h("div", { class: "empty" }, "no subagent types used yet"));
  for (const a of d.agents) {
    const box = h("div", { class: "agent" });
    box.append(h("div", {}, "├ ", h("b", {}, a.builtin ? a.type : a.file), a.builtin ? " built-in" : " (" + a.type + ")"));
    box.append(h("div", { class: "kv" }, "model: ", h("span", {}, a.model || "?")));
    if (a.tools && a.tools.length) box.append(h("div", { class: "kv" }, "tools: ", h("span", {}, a.tools.join(", "))));
    b.append(box);
  }
  b.append(h("div", { class: "muted" }, h("span", { class: "green" }, "settings.json"), "  hooks: ", d.hooksLine));
  b.append(h("div", { class: "muted" }, "ccwatch hooks: ", d.ccwatchHooks ? "present" : "not in settings.json"));
}

function renderMain(d) {
  $("main-title").textContent = (d.model || "main") + " · main session";
  if (d.profile) $("main-title").append(" · ", h("span", { class: "prof-name" }, d.profile));
  const live = d.state === "orchestrating" || d.state === "working";
  $("main-state").textContent = "";
  $("main-state").append(d.state + " [ ", h("span", { class: "spin" }, live ? SPIN[spinN % 4] : " "), " ]");
  $("run").textContent = d.planSource === "orca run" ? "orca run: " + d.planTitle : "";
  $("worker-line").textContent = [d.workerLine, d.nestedLine].filter(Boolean).join("\n");
  const ul = $("plan");
  ul.textContent = "";
  if (!d.plan.length) ul.append(h("li", { class: "empty" }, "no subagent launches yet"));
  for (const p of d.plan) {
    const cls = p.mark === "✓" ? "ok" : p.mark === "|" ? "run" : p.mark === "✗" ? "bad" : "";
    ul.append(h("li", { class: cls }, h("span", { class: "mk" }, "[" + (p.mark === "|" ? SPIN[spinN % 4] : p.mark) + "]"), h("span", { class: "tx" }, p.text + (p.detail ? " -> " + p.detail : ""))));
    ul.lastChild.firstChild.dataset.mark = p.mark;
  }
  $("ctx-val").textContent = tok(d.ctx) + " tok";
  $("ctx-bar").style.width = d.ctxKnown ? d.ctxPct + "%" : "0";
  $("ctx-note").textContent = d.ctxKnown ? d.ctxPct + "% of " + tok(d.ctxSize) + " window" : "window unknown";
}

function renderLanes(box, lanes) {
  const keep = new Set();
  for (const l of lanes) {
    keep.add(l.name);
    let el = box.querySelector('[data-lane="' + CSS.escape(l.name) + '"]');
    if (!el) {
      el = h("div", { class: "lane", "data-lane": l.name },
        h("div", {}, h("div", { class: "name" }, l.name), h("div", { class: "model" })),
        h("div", { class: "slots" }),
        h("div", { class: "pct" }, h("div", { class: "bar" }, h("i")), h("span")),
        h("div", { class: "status" }));
      box.append(el);
    }
    el.querySelector(".name").textContent = l.name;
    el.querySelector(".model").textContent = l.model;
    const slots = el.querySelector(".slots");
    while (slots.children.length > l.slots.length) slots.lastChild.remove();
    l.slots.forEach((s, i) => {
      let sl = slots.children[i];
      if (!sl) { sl = h("div"); slots.append(sl); }
      sl.className = "slot " + (s === "launched" ? "" : s);
      sl.title = s;
    });
    el.querySelector(".bar > i").style.width = l.pct + "%";
    el.querySelector(".pct span").textContent = l.pct + "%";
    el.querySelector(".status").textContent = "○ " + l.status;
  }
  for (const el of [...box.children]) if (!keep.has(el.dataset.lane)) el.remove();
  for (const l of lanes) box.append(box.querySelector('[data-lane="' + CSS.escape(l.name) + '"]')); // keep server order
}

function renderBack(d) {
  $("back-title").firstChild.textContent = "back to " + (d.model || "main") + " · summaries only ";
  $("b-proc").textContent = tok(d.back.processed) + " tok";
  $("b-recv").textContent = tok(d.back.received) + " tok";
  $("b-proc-bar").style.width = (d.back.procFrac * 100) + "%";
  $("b-recv-bar").style.width = (d.back.recvFrac * 100) + "%";
  $("b-note").textContent = "bars use a log scale · leverage " + (d.back.received > 0 ? "×" + Math.round(d.leverage) : "-");
  $("log-tag").textContent = "[ stream: " + (d.ended ? "ended" : "live") + " ]";
}

// Prompt rows are collapsed by default; the full text is fetched once per key and cached.
const promptCache = new Map(); // sessionId|key|raw -> text
const openRows = new Set();    // sessionId|key
const rawRows = new Set();     // sessionId|key showing the wrapper

async function loadPrompt(sid, key, raw) {
  const ck = sid + "|" + key + "|" + (raw ? 1 : 0);
  if (promptCache.has(ck)) return promptCache.get(ck);
  const r = await fetch("/api/prompt?session=" + encodeURIComponent(sid) + "&key=" + encodeURIComponent(key) + (raw ? "&raw=1" : ""));
  const text = r.ok ? (await r.json()).text : "(could not load the prompt: " + r.status + ")";
  if (r.ok) promptCache.set(ck, text);
  return text;
}

function fillRow(sid, r, txt) {
  const rk = sid + "|" + r.key;
  txt.textContent = "";
  if (!openRows.has(rk)) {
    txt.append(r.text + (r.full_chars ? " … (" + r.full_chars + " chars)" : ""));
    return;
  }
  const raw = rawRows.has(rk);
  const body = h("div", { class: "full" }, "loading…");
  const head = [];
  if (r.tag === "orca_task") {
    const tg = h("button", { class: "wrap-toggle", type: "button" }, raw ? "show task" : "show wrapper");
    tg.addEventListener("click", (ev) => {
      ev.stopPropagation();
      if (rawRows.has(rk)) rawRows.delete(rk); else rawRows.add(rk);
      fillRow(sid, r, txt);
    });
    head.push(tg);
  }
  txt.append(...head, body);
  loadPrompt(sid, r.key, raw).then((t) => { if (body.isConnected) body.textContent = t; });
}

function renderLog(d) {
  const box = $("log");
  box.textContent = "";
  if (!d.log.length) box.append(h("div", { class: "empty" }, "no events yet"));
  d.log.forEach((r, i) => {
    const key = d.sessionId + "|" + r.ts + "|" + r.who + "|" + r.text + "|" + r.tag;
    const isNew = !firstLog && !seenLog.has(key);
    seenLog.add(key);
    const text = r.kind === "test" ? r.tag + "  " + r.text : r.kind === "notice" ? (r.tag ? r.tag + "  " : "") + r.text : r.text;
    const txt = h("span", { class: "txt" }, text);
    const row = h("div", { class: "row " + r.kind + (r.tag ? " " + r.tag : "") + (isNew ? " new" : "") },
      h("span", { class: "t" }, clock(r.ts)), h("span", { class: "who", title: r.who }, r.who), txt);
    if (r.key) {
      const rk = d.sessionId + "|" + r.key;
      row.classList.add("more");
      if (openRows.has(rk)) row.classList.add("open");
      fillRow(d.sessionId, r, txt);
      row.addEventListener("click", (ev) => {
        if (ev.target.closest(".wrap-toggle") || (row.classList.contains("open") && window.getSelection().toString())) return;
        if (openRows.has(rk)) { openRows.delete(rk); row.classList.remove("open"); } else { openRows.add(rk); row.classList.add("open"); }
        fillRow(d.sessionId, r, txt);
      });
    }
    box.append(row);
  });
  firstLog = false;
}

function renderTable(d) {
  const tb = $("tbody");
  tb.textContent = "";
  if (!d.table.length) tb.append(h("tr", {}, h("td", { colspan: 9, class: "empty" }, "no subagents")));
  for (const r of d.table) {
    tb.append(h("tr", {}, h("td", {}, r.id.slice(0, 8)), h("td", {}, r.type), h("td", {}, r.description + (r.background ? " (bg)" : "")),
      h("td", {}, r.model), h("td", {}, r.state), h("td", {}, tok(r.tokens)), h("td", {}, tok(r.peak)),
      h("td", {}, r.durationMs ? (r.durationMs / 1000).toFixed(1) + "s" : "-"), h("td", {}, r.summary)));
  }
}

function setView(v) {
  view = v;
  $("dash").hidden = v !== "dash";
  $("tbl").hidden = v !== "table";
  $("v-dash").classList.toggle("on", v === "dash");
  $("v-table").classList.toggle("on", v === "table");
}

$("v-dash").addEventListener("click", () => setView("dash"));
$("v-table").addEventListener("click", () => setView("table"));
$("follow").addEventListener("change", (e) => {
  follow = e.target.checked;
  firstLog = true;
  connect();
});
setInterval(() => {
  spinN++;
  for (const n of document.querySelectorAll(".spin")) if (n.textContent.trim()) n.textContent = SPIN[spinN % 4];
  for (const n of document.querySelectorAll("#plan .mk")) if (n.dataset.mark === "|") n.textContent = "[" + SPIN[spinN % 4] + "]";
}, 150);

// Start: honour ?session=<id> only if that session exists, else follow latest.
fetch("/api/sessions").then((r) => r.json()).catch(() => []).then((list) => {
  sessions = list || [];
  renderTree();
  if (pinned && !(list || []).some((s) => s.id === pinned)) { follow = true; current = ""; }
  $("follow").checked = follow;
  connect();
});
