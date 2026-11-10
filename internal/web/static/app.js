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
let showTools = store.get("ccwatch.tools") !== "0"; // tool rows in the session log
let logReverse = store.get("ccwatch.logReverse") === "1"; // newest log row on top
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
  if (view === "flow") renderFlow(d);
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

// ---- what a session / agent is doing now ----
const secs = (ms) => { const s = Math.max(0, Math.floor(ms / 1000)); return s < 60 ? s + "s" : Math.floor(s / 60) + "m" + String(s % 60).padStart(2, "0") + "s"; };
const dur = (ms) => ms < 1000 ? ms + "ms" : ms < 10000 ? (ms / 1000).toFixed(1) + "s" : secs(ms);
// paintNow writes "now: Bash go test ./... · 4s" (running) or "last: Bash go test ./... ✓ 4.2s" (muted,
// finished) into el as ONE line; the full text (the whole command) is the hover title.
function paintNow(el) {
  const a = el._act;
  if (!a) { el.hidden = true; return; }
  el.hidden = false;
  el.classList.toggle("idle", !a.running);
  el.title = a.title || a.text;
  el.textContent = "";
  if (a.running) {
    const t = Date.parse(a.start);
    el.append(h("span", { class: "nt" }, "now: " + a.text + (a.more ? " (+" + a.more + " more)" : "")));
    if (t) el.append(h("span", { class: "el" }, " · " + secs(Date.now() - t)));
  } else {
    el.append(h("span", { class: "nt" }, "last: " + a.text), h("span", { class: "mk" }, (a.failed ? " ✗" : " ✓") + (a.durMs ? " " + dur(a.durMs) : "")));
  }
}
function setNow(el, act) { el._act = act || null; paintNow(el); }

function renderMain(d) {
  setNow($("now"), d.now);
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
        h("div", { class: "status" }),
        h("div", { class: "now nowline", hidden: "" }));
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
      sl.title = (l.titles && l.titles[i]) || s;
    });
    setNow(el.querySelector(".now"), l.now);
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

// ---- the log's "who" column is as wide as its longest label, between 5ch and 16ch ----
const WHO_MIN = 5, WHO_MAX = 16;
let whoMax = WHO_MAX; // width in characters of the log being rendered
// whoText cuts a label in the middle, keeping the agent id at the end: "general-purpose ab93" -> "general-p… ab93".
function whoText(who, max) {
  const c = [...who];
  if (c.length <= max) return who;
  const i = who.lastIndexOf(" ");
  const tail = i > 0 && c.length - [...who.slice(0, i)].length <= 9 ? who.slice(i) : [...who].slice(-4).join("");
  const keep = Math.max(1, max - [...tail].length - 2); // the ellipsis counts as two cells: fallback fonts draw it double width
  return c.slice(0, keep).join("") + "…" + tail;
}
const whoCell = (who, max) => h("span", { class: "who", title: who }, whoText(who, max));

// ---- tool rows: "Bash  go test ./... ✓ 28ms"; a click expands the stored params and the Bash output ----
const toolCache = new Map(); // sessionId|key -> detail of a finished call
async function loadTool(sid, key) {
  const ck = sid + "|" + key;
  if (toolCache.has(ck)) return toolCache.get(ck);
  const r = await fetch("/api/tool?session=" + encodeURIComponent(sid) + "&key=" + encodeURIComponent(key));
  if (!r.ok) return { error: "could not load the call: " + r.status };
  const d = await r.json();
  if (d.ok !== null) toolCache.set(ck, d);
  return d;
}

function previewText(label, p) {
  if (!p) return null;
  const box = h("div", { class: "out" }, h("div", { class: "k" }, label + " (" + p.chars + " chars)"));
  if (p.tail) box.append(h("pre", { class: "v" }, p.head + "\n… " + (p.chars - [...p.head].length - [...p.tail].length) + " chars omitted …\n" + p.tail));
  else box.append(h("pre", { class: "v" }, p.head));
  return box;
}

function toolDetailNode(d) {
  const box = h("div", { class: "detail" });
  if (d.error && !d.input) { box.append(d.error); return box; }
  const input = d.input || {};
  const keys = Object.keys(input).filter((k) => !k.endsWith("_chars"));
  if (!keys.length) box.append(h("div", { class: "k" }, "no params stored"));
  for (const k of keys) {
    const v = input[k];
    const cut = input[k + "_chars"];
    const text = typeof v === "string" ? v : JSON.stringify(v, null, 2);
    box.append(h("div", { class: "param" }, h("div", { class: "k" }, k + (cut ? " (cut, " + cut + " chars)" : "")), h("pre", { class: "v" }, text)));
  }
  const o = d.output;
  if (o) {
    for (const n of [previewText("stdout", o.stdout), previewText("stderr", o.stderr)]) if (n) box.append(n);
    if (o.interrupted) box.append(h("div", { class: "k" }, "interrupted"));
    if (!o.stdout && !o.stderr && o.bytes) box.append(h("div", { class: "k" }, "response: " + o.bytes + " bytes"));
  }
  if (d.error) box.append(h("div", { class: "param" }, h("div", { class: "k" }, "error"), h("pre", { class: "v" }, d.error)));
  return box;
}

// toolLine builds one tool call row (also used inside a group): name, summary, outcome; click to expand.
function toolLine(sid, r, grouped) {
  const rk = sid + "|" + r.key;
  const mark = r.tag === "ok" ? " ✓" : r.tag === "fail" ? " ✗" : r.tag === "end" ? " (no result)" : "";
  const txt = h("span", { class: "txt" });
  const head = h("span", { class: "head" }, h("span", { class: "tn" }, r.tool || ""), "  ",
    h("span", { class: "ts", title: r.summary }, r.summary || ""), h("span", { class: "tm" }, mark + (r.dur_ms ? " " + dur(r.dur_ms) : "")));
  const row = h("div", { class: (grouped ? "titem " : "row tool ") + (r.tag || "") }, h("span", { class: "t" }, clock(r.ts)));
  row.append(grouped ? h("span", { class: "who" }) : whoCell(r.who, whoMax));
  row.append(txt);
  row.classList.add("more");
  const fill = () => {
    txt.textContent = "";
    txt.append(head);
    row.classList.toggle("open", openRows.has(rk));
    if (!openRows.has(rk)) return;
    const body = h("div", { class: "detail" }, "loading…");
    txt.append(body);
    loadTool(sid, r.key).then((d) => { if (body.isConnected) body.replaceWith(toolDetailNode(d)); });
  };
  fill();
  row.addEventListener("click", (ev) => {
    ev.stopPropagation();
    if (ev.target.closest(".detail") || window.getSelection().toString()) return;
    if (openRows.has(rk)) openRows.delete(rk); else openRows.add(rk);
    fill();
  });
  return row;
}

function toolRows(sid, r) { // a grouped row: collapsed to "N tool calls", expands to its calls
  const gk = sid + "|g|" + r.ts + "|" + r.who;
  const txt = h("span", { class: "txt" });
  const row = h("div", { class: "row tools" + (openRows.has(gk) ? " open" : "") },
    h("span", { class: "t" }, clock(r.ts)), whoCell(r.who, whoMax), txt);
  const fill = () => {
    txt.textContent = r.text + (r.count > (r.items || []).length ? " (last " + r.items.length + " listed)" : "");
    if (!openRows.has(gk)) return;
    const sub = h("div", { class: "sub" });
    const items = logReverse ? [...(r.items || [])].reverse() : (r.items || []);
    for (const it of items) sub.append(toolLine(sid, it, true));
    txt.append(sub);
  };
  fill();
  row.addEventListener("click", (ev) => {
    if (ev.target.closest(".sub")) return;
    if (openRows.has(gk)) { openRows.delete(gk); row.classList.remove("open"); } else { openRows.add(gk); row.classList.add("open"); }
    fill();
  });
  return row;
}

function renderLog(d, toggled) {
  const box = $("log");
  // reverse mode grows at the top: keep what the reader looks at in place when the log starts above the viewport
  const anchor = logReverse && !toggled && box.getBoundingClientRect().top < 0;
  const h0 = box.offsetHeight;
  box.textContent = "";
  let rows = showTools ? d.log : d.log.filter((r) => r.kind !== "tool" && r.kind !== "tools");
  if (logReverse) rows = [...rows].reverse();
  whoMax = Math.min(WHO_MAX, Math.max(WHO_MIN, ...rows.map((r) => [...(r.who || "")].length)));
  box.style.setProperty("--who-w", whoMax + "ch");
  if (!rows.length) box.append(h("div", { class: "empty" }, "no events yet"));
  rows.forEach((r, i) => {
    if (r.kind === "tools") { box.append(toolRows(d.sessionId, r)); return; }
    if (r.kind === "tool" && r.key) { box.append(toolLine(d.sessionId, r, false)); return; }
    const key = d.sessionId + "|" + r.ts + "|" + r.who + "|" + r.text + "|" + r.tag;
    const isNew = !firstLog && !seenLog.has(key);
    seenLog.add(key);
    const text = r.kind === "test" ? r.tag + "  " + r.text : r.kind === "notice" ? (r.tag ? r.tag + "  " : "") + r.text : r.text;
    const txt = h("span", { class: "txt" }, text);
    const row = h("div", { class: "row " + r.kind + (r.tag ? " " + r.tag : "") + (isNew ? " new" : "") },
      h("span", { class: "t" }, clock(r.ts)), whoCell(r.who, whoMax), txt);
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
  if (anchor) { const dh = box.offsetHeight - h0; if (dh) window.scrollBy(0, dh); }
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

// ---- flow view: one session as a picture of who does what ----
const REVIEWER_TYPES = ["code-reviewer", "kongming", "advisor"];
const FLOW_MAX_CARDS = 12;
const firstLine = (t) => ((t || "").split("\n").find((l) => l.trim()) || "").trim();
const clip = (t, n) => { const c = [...t]; return c.length > n ? c.slice(0, n - 1).join("") + "…" : t; };

// flowFailures counts failing tool rows by (actor, tool, summary); keys seen 2+ times are repeated errors.
function flowFailures(log) {
  const m = new Map();
  for (const r of log) for (const t of r.kind === "tools" ? (r.items || []) : [r]) {
    if (t.kind !== "tool" || t.tag !== "fail") continue;
    const k = (t.actor || "") + "\u0000" + (t.tool || "") + "\u0000" + (t.summary || "");
    const e = m.get(k) || { tool: t.tool || "", summary: t.summary || "", n: 0 };
    e.n++; m.set(k, e);
  }
  return [...m.values()].filter((e) => e.n >= 2).sort((a, b) => b.n - a.n);
}

// flowCards picks the subagent rows to draw: all running ones plus the most recent finished, up to the cap.
function flowCards(rows) {
  if (rows.length <= FLOW_MAX_CARDS) return { shown: rows, hidden: 0 };
  const running = rows.filter((r) => r.state === "running");
  const room = Math.max(0, FLOW_MAX_CARDS - running.length);
  const rest = rows.filter((r) => r.state !== "running");
  const keep = new Set([...running, ...rest.slice(rest.length - room)]);
  const shown = rows.filter((r) => keep.has(r));
  return { shown, hidden: rows.length - shown.length };
}

function flowCard(r) {
  const c = h("div", { class: "fcard " + r.state, "data-edge": "in" });
  c.append(h("div", { class: "fc-h" }, h("b", {}, r.type), h("span", { class: "badge " + r.state }, r.state)));
  c.append(h("div", { class: "fc-m" }, r.model || "?"));
  if (r.description) c.append(h("div", { class: "fc-d", title: r.description }, r.description));
  const meta = tok(r.tokens) + " tok" + (r.durationMs ? " · " + dur(r.durationMs) : "");
  c.append(h("div", { class: "fc-t muted" }, meta));
  if (r.state === "done" && firstLine(r.summary)) c.append(h("div", { class: "fc-s", title: r.summary }, firstLine(r.summary)));
  return c;
}

function flowTaskCard(t) {
  const st = /run|progress|active|dispatched/i.test(t.status) ? "running" : /fail|error|abort/i.test(t.status) ? "failed" : /done|complete|succe|finish/i.test(t.status) ? "done" : "";
  const c = h("div", { class: "fcard " + st, "data-edge": "in" });
  c.append(h("div", { class: "fc-h" }, h("b", { title: t.title }, t.title || t.id), h("span", { class: "badge " + st }, t.status || "?")));
  if (t.workerName) c.append(h("div", { class: "fc-m" }, t.workerName));
  return c;
}

function flowGroup(label, cards, extra) {
  const g = h("div", { class: "fgroup" }, h("div", { class: "fg-l", "data-edge": "label" }, label));
  if (extra) g.append(extra);
  g.append(h("div", { class: "fcards" }, ...cards));
  return g;
}

function renderFlow(d) {
  const box = $("flow");
  const sy = window.scrollY;
  const rows = d.table.filter((r) => !REVIEWER_TYPES.includes(r.type));
  const revs = d.table.filter((r) => REVIEWER_TYPES.includes(r.type));
  const tasks = d.run && d.run.tasks ? d.run.tasks : [];
  const model = d.model || "main";

  // header: one chip per distinct model with its roles
  const roles = new Map([[model, ["main"]]]);
  for (const r of d.table) {
    const m = r.model || "?";
    if (!roles.has(m)) roles.set(m, []);
    const l = roles.get(m);
    const cnt = d.table.filter((x) => (x.model || "?") === m && x.type === r.type).length;
    const label = r.type + (cnt > 1 ? " ×" + cnt : "");
    if (!l.some((x) => x.split(" ×")[0] === r.type)) l.push(label);
  }
  const head = h("div", { class: "fhead" }, h("b", {}, roles.size + (roles.size === 1 ? " MODEL" : " MODELS") + " · 1 SESSION"));
  for (const [m, l] of roles) head.append(h("span", { class: "chip" }, h("b", {}, m), h("span", { class: "muted" }, " " + l.join(", "))));

  // main node
  const main = h("div", { class: "fmain panel purple", "data-edge": "main" });
  const title = h("div", { class: "fm-t" }, model);
  if (d.profile) title.append(" ", h("span", { class: "prof-name" }, d.profile));
  main.append(title);
  const live = d.state === "orchestrating" || d.state === "working";
  main.append(h("div", { class: "muted" }, (d.ended ? "ended · " : "") + d.state + " ", h("span", { class: "spin" }, live && !d.ended ? SPIN[spinN % 4] : "")));
  const now = h("div", { class: "nowline" });
  setNow(now, d.now);
  main.append(now);
  main.append(h("div", { class: "ctx" }, h("span", { class: "muted" }, "context"), h("span", { class: "green" }, tok(d.ctx) + " tok" + (d.ctxKnown ? " · " + d.ctxPct + "%" : ""))));
  main.append(h("div", { class: "bar" }, h("i", { style: "width:" + (d.ctxKnown ? d.ctxPct : 0) + "%" })));

  // reviewer panel
  let rev = null;
  if (revs.length) {
    rev = h("div", { class: "frev panel blue", "data-edge": "rev" }, h("h2", {}, "reviewers ", h("span", { class: "tag" }, "calls " + revs.length)));
    for (const r of revs) {
      const txt = r.state === "running" ? r.description : firstLine(r.summary) || r.description;
      rev.append(h("div", { class: "fr-row " + r.state, title: txt }, h("b", {}, r.type), " ", h("span", { class: "badge " + r.state }, r.state), " ", h("span", { class: "muted" }, clip(txt || "", 80))));
    }
  }
  const top = h("div", { class: "ftop" + (rev ? " has-rev" : "") });
  if (rev) top.append(rev);
  top.append(main);

  // fan-out groups
  const groups = h("div", { class: "fgroups" });
  const { shown, hidden } = flowCards(rows);
  if (rows.length) {
    const more = hidden ? h("span", { class: "chip more" }, "+" + hidden + " earlier") : null;
    groups.append(flowGroup("delegate → " + rows.length + (rows.length === 1 ? " subagent" : " subagents"), shown.map(flowCard), more));
  }
  if (tasks.length) groups.append(flowGroup("dispatch → " + tasks.length + (tasks.length === 1 ? " Orca task" : " Orca tasks"), tasks.map(flowTaskCard)));
  if (!rows.length && !tasks.length) groups.append(h("div", { class: "fg-l empty", "data-edge": "label" }, "no delegation yet"));

  // return bar, repeated failures
  const ret = h("div", { class: "fret panel green dashed", "data-edge": "ret" });
  ret.append(h("span", {}, "back to " + model + " · " + tok(d.back.processed) + " read → " + tok(d.back.received) + " returned" + (d.back.received > 0 ? " · ×" + Math.round(d.leverage) : "")));
  if (d.testsPass + d.testsFail > 0) ret.append(h("span", { class: d.testsFail ? "bad" : "green" }, "tests " + d.testsPass + " pass / " + d.testsFail + " fail"));
  if (d.toolFailures > 0) ret.append(h("span", { class: "bad" }, "tool failures " + d.toolFailures));
  const rep = h("div", { class: "frep" });
  for (const e of flowFailures(d.log)) rep.append(h("span", { class: "chip bad", title: "same error ×" + e.n + ": " + e.tool + " " + e.summary }, "same error ×" + e.n + ": " + e.tool + " " + clip(e.summary, 48)));

  // key events
  const ev = h("div", { class: "fev panel grey" }, h("h2", {}, "key events"));
  const evRows = d.log.filter((r) => r.kind !== "tool" && r.kind !== "tools").slice(-8);
  if (!evRows.length) ev.append(h("div", { class: "empty" }, "no events yet"));
  for (const r of evRows) {
    const text = (r.kind === "test" || r.kind === "notice") && r.tag ? r.tag + "  " + r.text : r.text;
    ev.append(h("div", { class: "fe-row " + r.kind }, h("span", { class: "t muted" }, clock(r.ts)), h("span", { class: "who" }, r.who || ""), h("span", { class: "tx", title: text }, firstLine(text))));
  }

  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  svg.setAttribute("class", "fsvg");
  svg.setAttribute("aria-hidden", "true");
  box.replaceChildren(svg, head, top, groups, ret, rep, ev);
  drawFlow();
  window.scrollTo(window.scrollX, sy);
}

// drawFlow redraws the connectors from element positions: reviewer -> main, main -> each group label, group label -> cards.
function drawFlow() {
  const box = $("flow");
  if (box.hidden) return;
  const svg = box.querySelector(".fsvg");
  if (!svg) return;
  const o = box.getBoundingClientRect();
  svg.setAttribute("width", Math.round(o.width));
  svg.setAttribute("height", Math.round(box.scrollHeight));
  svg.setAttribute("viewBox", "0 0 " + Math.round(o.width) + " " + Math.round(box.scrollHeight));
  svg.textContent = "";
  const stacked = !window.matchMedia("(min-width: 900px)").matches;
  const pos = (el) => { const b = el.getBoundingClientRect(); return { l: b.left - o.left, r: b.right - o.left, t: b.top - o.top, b: b.bottom - o.top, cx: (b.left + b.right) / 2 - o.left, cy: (b.top + b.bottom) / 2 - o.top }; };
  const line = (x1, y1, x2, y2, cls) => {
    const p = document.createElementNS("http://www.w3.org/2000/svg", "path");
    const my = (y1 + y2) / 2;
    p.setAttribute("d", x1 === x2 || y1 === y2 ? "M" + x1 + " " + y1 + "L" + x2 + " " + y2 : "M" + x1 + " " + y1 + "C" + x1 + " " + my + " " + x2 + " " + my + " " + x2 + " " + y2);
    p.setAttribute("class", cls);
    svg.append(p);
  };
  const main = box.querySelector(".fmain"), rev = box.querySelector(".frev");
  if (!main) return;
  const m = pos(main);
  if (rev) { const r = pos(rev); if (stacked) line(r.cx, r.b, m.cx, m.t, "wire"); else line(r.r, r.cy, m.l, m.cy, "wire"); }
  for (const g of box.querySelectorAll(".fgroup")) {
    const lab = pos(g.querySelector(".fg-l"));
    line(m.cx, m.b, lab.cx, lab.t, "wire");
    if (stacked) continue;
    const cs = [...g.querySelectorAll(".fcard")];
    const top = Math.min(...cs.map((c) => c.getBoundingClientRect().top)); // only the first row hangs from the label
    for (const c of cs) {
      const p = pos(c);
      if (c.getBoundingClientRect().top > top + 1) { if (c.classList.contains("running")) line(p.cx, p.t - 12, p.cx, p.t, "wire run"); continue; }
      line(lab.cx, lab.b, p.cx, p.t, "wire" + (c.classList.contains("running") ? " run" : ""));
    }
  }
  const lone = box.querySelector(".fgroups > .fg-l.empty");
  if (lone) { const l = pos(lone); line(m.cx, m.b, l.cx, l.t, "wire"); }
  const ret = box.querySelector(".fret");
  if (ret && !stacked) {
    const r = pos(ret);
    for (const g of box.querySelectorAll(".fgroup")) { const gb = pos(g); line(gb.cx, gb.b, gb.cx, r.t, "wire back"); }
  }
}
window.addEventListener("resize", drawFlow);
if (window.ResizeObserver) new ResizeObserver(drawFlow).observe($("flow"));

function setView(v, save) {
  view = v;
  $("dash").hidden = v !== "dash";
  $("tbl").hidden = v !== "table";
  $("flow").hidden = v !== "flow";
  for (const n of ["dash", "table", "flow"]) $("v-" + n).classList.toggle("on", v === n);
  if (save) store.set("ccwatch.view", v);
  if (v === "flow" && lastData) renderFlow(lastData);
}

function syncTools() {
  const b = $("tools-toggle");
  b.classList.toggle("on", showTools);
  b.setAttribute("aria-pressed", String(showTools));
}
syncTools();
$("tools-toggle").addEventListener("click", () => {
  showTools = !showTools;
  store.set("ccwatch.tools", showTools ? "1" : "0");
  syncTools();
  if (lastData) renderLog(lastData, true);
});

$("log-reverse").checked = logReverse;
$("log-reverse").addEventListener("change", (e) => {
  logReverse = e.target.checked;
  store.set("ccwatch.logReverse", logReverse ? "1" : "0");
  if (lastData) renderLog(lastData, true);
});

$("v-dash").addEventListener("click", () => setView("dash", true));
$("v-table").addEventListener("click", () => setView("table", true));
$("v-flow").addEventListener("click", () => setView("flow", true));
{ const v = store.get("ccwatch.view"); if (v === "table" || v === "flow") setView(v); }
$("follow").addEventListener("change", (e) => {
  follow = e.target.checked;
  firstLog = true;
  connect();
});
setInterval(() => {
  spinN++;
  for (const n of document.querySelectorAll(".spin")) if (n.textContent.trim()) n.textContent = SPIN[spinN % 4];
  for (const n of document.querySelectorAll(".nowline")) if (n._act && n._act.running) paintNow(n);
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
