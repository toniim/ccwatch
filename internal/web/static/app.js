"use strict";
const $ = (id) => document.getElementById(id);
const SPIN = "|/-\\";
let spinN = 0;
let es = null;
let follow = true;
let current = "";       // session id the picker shows
let view = "dash";
let lastData = null;
let lastList = "";
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

function connect() {
  if (es) es.close();
  const q = follow || !current ? "latest" : current;
  es = new EventSource("/api/stream?session=" + encodeURIComponent(q));
  es.onopen = () => setLive(true);
  es.onerror = () => setLive(false);
  es.addEventListener("state", (e) => { lastData = JSON.parse(e.data); render(lastData); });
  es.addEventListener("sessions", (e) => { if (e.data !== lastList) { lastList = e.data; renderPicker(JSON.parse(e.data)); } });
}

function renderPicker(list) {
  const sel = $("session");
  sel.textContent = "";
  if (!list.length) sel.append(h("option", {}, "no sessions yet"));
  for (const s of list) {
    const label = [s.id.slice(0, 8), s.model || "?", base(s.cwd), s.subagents + " agents" + (s.live ? " · " + s.live + " live" : "") + (s.ended ? " · ended" : "")].join(" · ");
    const o = h("option", { value: s.id }, label);
    if (s.id === current) o.selected = true;
    sel.append(o);
  }
}

function render(d) {
  for (const k of ["agents", "plan", "lanes", "log", "table"]) d[k] = d[k] || []; // never trust null arrays
  current = d.sessionId;
  for (const o of $("session").options) o.selected = o.value === current;
  $("cwd").textContent = "cwd " + d.cwd + " · session " + d.sessionId + (d.ended ? " · ended" : "");
  renderAgents(d);
  renderMain(d);
  const showTask = d.lanes.length > 0 || !d.orcaLane;
  $("arrow").hidden = !showTask;
  $("arrow").textContent = "» Task tool · " + d.subagents + " subagents · each in its own context window «";
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

function renderLog(d) {
  const box = $("log");
  box.textContent = "";
  if (!d.log.length) box.append(h("div", { class: "empty" }, "no events yet"));
  d.log.forEach((r, i) => {
    const key = d.sessionId + "|" + r.ts + "|" + r.who + "|" + r.text + "|" + r.tag;
    const isNew = !firstLog && !seenLog.has(key);
    seenLog.add(key);
    const text = r.kind === "test" ? r.tag + "  " + r.text : r.text;
    box.append(h("div", { class: "row " + r.kind + (r.tag ? " " + r.tag : "") + (isNew ? " new" : "") },
      h("span", { class: "t" }, clock(r.ts)), h("span", { class: "who", title: r.who }, r.who), h("span", { class: "txt" }, text)));
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
$("session").addEventListener("change", (e) => {
  current = e.target.value;
  follow = false;
  $("follow").checked = false;
  firstLog = true;
  connect();
});

setInterval(() => {
  spinN++;
  for (const n of document.querySelectorAll(".spin")) if (n.textContent.trim()) n.textContent = SPIN[spinN % 4];
  for (const n of document.querySelectorAll("#plan .mk")) if (n.dataset.mark === "|") n.textContent = "[" + SPIN[spinN % 4] + "]";
}, 150);

connect();
