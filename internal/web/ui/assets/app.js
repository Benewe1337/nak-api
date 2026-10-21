// naknak web UI. Vanilla ES module; all data goes into the DOM via textContent,
// never as HTML, and inline styles are avoided (CSP) — custom properties are
// set through the CSSOM instead.

const $ = (sel, root = document) => root.querySelector(sel);
const reduced = document.documentElement.classList.contains("still") ? { matches: true } : matchMedia("(prefers-reduced-motion: reduce)");

// ── DOM helper ──────────────────────────────────────────────────────────────

function h(tag, props = {}, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (v == null || v === false) continue;
    if (k === "class") el.className = v;
    else if (k === "text") el.textContent = v;
    else if (k === "vars") for (const [n, x] of Object.entries(v)) el.style.setProperty(n, x);
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (k === "href") el.setAttribute("href", safeHref(v));
    else el.setAttribute(k, v === true ? "" : v);
  }
  for (const kid of kids.flat(Infinity)) {
    if (kid == null || kid === false) continue;
    el.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  return el;
}

// Only in-app links (#…, /path) and https URLs; "//host" is protocol-relative
// and would leave the site, "/\\host" is treated the same way by browsers.
function safeHref(v) {
  const s = String(v ?? "").trim();
  if (s.startsWith("#")) return s;
  if (s.startsWith("/") && !s.startsWith("//") && !s.startsWith("/\\")) return s;
  if (/^https:\/\/[^/\\]/i.test(s)) return s;
  return "#";
}

const svg = (paths) => {
  const s = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  s.setAttribute("viewBox", "0 0 24 24");
  s.setAttribute("aria-hidden", "true");
  for (const d of paths) {
    const p = document.createElementNS("http://www.w3.org/2000/svg", "path");
    p.setAttribute("d", d);
    s.append(p);
  }
  return s;
};

const icons = {
  refresh: ["M20 11a8 8 0 1 0-2.3 5.7", "M20 4v7h-7"],
  system: ["M4 5h16v11H4z", "M9 20h6", "M12 16v4"],
  light: ["M12 4V2", "M12 22v-2", "M4.9 4.9 3.5 3.5", "M20.5 20.5l-1.4-1.4", "M4 12H2", "M22 12h-2", "M4.9 19.1l-1.4 1.4", "M20.5 3.5l-1.4 1.4", "M12 16a4 4 0 1 0 0-8 4 4 0 0 0 0 8z"],
  dark: ["M20 14.5A8.5 8.5 0 0 1 9.5 4a8.5 8.5 0 1 0 10.5 10.5z"],
  back: ["M15 18l-6-6 6-6"],
  eye: ["M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12z", "M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z"],
  eyeOff: ["M3 3l18 18", "M10.6 5.1A10 10 0 0 1 12 5c6.5 0 10 7 10 7a17 17 0 0 1-3.2 4.1", "M6.6 6.6A17 17 0 0 0 2 12s3.5 7 10 7a9.7 9.7 0 0 0 5.4-1.6", "M9.9 9.9a3 3 0 0 0 4.2 4.2"],
  gear: ["M12 15a3 3 0 1 0 0-6 3 3 0 0 0 0 6z", "M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"],
};

// ── API ─────────────────────────────────────────────────────────────────────

let fresh = false;
let newest = null;
// Calls of the current page that are still running, and the ones the server
// answered with stale data — those are revalidated once the page is drawn.
const pending = new Set();
const staleSeen = new Map();

class ToolError extends Error {
  constructor(msg, body) {
    super(msg);
    this.body = body || {};
  }
}

async function post(tool, args, query = "", headers = {}) {
  // the service worker answers from its cache after a few seconds; without
  // one (first visit) a hanging connection must still end
  const stop = new AbortController();
  const timer = setTimeout(() => stop.abort(), 30_000);
  let res;
  try {
    res = await fetch(`/api/tools/${tool}${query}`, {
      method: "POST",
      headers: { "Content-Type": "application/json", ...headers },
      body: JSON.stringify(args),
      credentials: "same-origin",
      signal: stop.signal,
    });
  } catch {
    setOffline(true);
    throw new ToolError("Offline: für diese Ansicht ist noch kein Stand gespeichert. Sobald wieder eine Verbindung da ist, lädt naknak sie.", { offline: true });
  } finally {
    clearTimeout(timer);
  }
  if (res.status === 401) {
    location.href = "/login";
    throw new ToolError("nicht angemeldet");
  }
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new ToolError(body.error || `HTTP ${res.status}`, body);
  if (res.headers.get("X-Naknak-Offline")) { body.offline = true; setOffline(true); } else setOffline(false);
  return body;
}

let offline = false;

// the bar under the header: visible on every screen size, gone with the first fresh answer
function setOffline(on) {
  if (offline === on) return;
  offline = on;
  let bar = $(".offline-bar");
  if (on && !bar) {
    bar = h("div", { class: "offline-bar", role: "status" });
    $(".top")?.after(bar);
  }
  if (bar) bar.hidden = !on;
  stamp();
}

function fmtStamp(d) {
  return dayDiff(d) === 0 ? fmtTime.format(d) + " Uhr" : `${fmtShort.format(d)}, ${fmtTime.format(d)} Uhr`;
}

addEventListener("online", () => render());
addEventListener("offline", () => setOffline(true));

function api(tool, args = {}, { wait = false } = {}) {
  const p = post(tool, args, fresh ? "?fresh=1" : wait ? "?wait=1" : "").then((body) => {
    const at = new Date(body.fetched_at);
    if (!newest || at < newest) newest = at; // the oldest data on screen decides
    stamp();
    if (body.stale && !wait) staleSeen.set(tool + JSON.stringify(args), { tool, args, text: JSON.stringify(body.result) });
    return body.result;
  });
  if (!wait) {
    pending.add(p);
    const done = () => pending.delete(p);
    p.then(done, done);
  }
  return p;
}

async function settle() {
  while (pending.size) await Promise.allSettled([...pending]);
}

// ── dates ───────────────────────────────────────────────────────────────────

// CIS and the cross tools use "Fr 02.10.2026 10:40" / "02.10.2026" / "02.10.2026 10:40".
function parseDE(s) {
  const m = String(s || "").match(/(\d{2})\.(\d{2})\.(\d{4})(?:\s+(\d{1,2}):(\d{2}))?/);
  if (!m) return null;
  return new Date(+m[3], +m[2] - 1, +m[1], +(m[4] || 0), +(m[5] || 0));
}

const fmtDay = new Intl.DateTimeFormat("de-DE", { weekday: "long", day: "numeric", month: "long" });
const fmtShort = new Intl.DateTimeFormat("de-DE", { weekday: "short", day: "2-digit", month: "2-digit" });
const fmtTime = new Intl.DateTimeFormat("de-DE", { hour: "2-digit", minute: "2-digit" });
const wd = ["So", "Mo", "Di", "Mi", "Do", "Fr", "Sa"];

const startOfDay = (d) => new Date(d.getFullYear(), d.getMonth(), d.getDate());
const dayDiff = (d, now = new Date()) => Math.round((startOfDay(d) - startOfDay(now)) / 864e5);

function relDay(n) {
  if (n < 0) return "überfällig";
  if (n === 0) return "heute";
  if (n === 1) return "morgen";
  return `in ${n} T.`;
}

// "{gruß}, {name}." with the parts that are known; a missing name takes its
// comma with it
function greet(template, now, name) {
  const t = (template || "{gruß}, {name}.").replaceAll("{gruß}", greeting(now)).replaceAll("{gruss}", greeting(now)).replaceAll("{name}", name);
  return t.replace(/\s*,\s*(?=[.!?]|$)/, "").replace(/\s{2,}/g, " ").trim();
}

function greeting(now = new Date()) {
  const hr = now.getHours();
  if (hr < 5) return "Gute Nacht";
  if (hr < 11) return "Guten Morgen";
  if (hr < 18) return "Hallo";
  return "Guten Abend";
}

const isoDate = (d) => `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;

// ── shared bits ─────────────────────────────────────────────────────────────

const srcOf = (s) => (String(s || "").startsWith("moodle") ? "moodle" : "cis");
const srcName = { cis: "CIS", moodle: "Moodle" };

function tile(title, { cls = "", link, i = 0 } = {}, ...body) {
  const t = h("section", { class: `tile ${cls}`, vars: { "--i": i } },
    h("div", { class: "tile-head" }, h("h2", { text: title }), link && h("a", { href: link[1], text: link[0] })),
    ...body);
  return t;
}

const skeleton = () => [h("div", { class: "skel big-s" }), h("div", { class: "skel w80" }), h("div", { class: "skel w60" })];

function errorBox(err) {
  const box = h("div", { class: "err", role: "status" }, err.message);
  if (err.body?.drift && err.body.report_url) {
    box.append(" ", h("a", { href: err.body.report_url, target: "_blank", rel: "noopener", text: "Änderung melden" }));
  }
  return box;
}

// fill replaces a tile's skeleton once its data has arrived.
async function fill(tileEl, load, render) {
  const body = h("div", {}, skeleton());
  tileEl.append(body);
  try {
    const data = await load();
    body.replaceChildren(...[render(data)].flat().filter(Boolean));
  } catch (err) {
    body.replaceChildren(errorBox(err));
  }
}

function count(n, unit) {
  return h("span", { class: "big" }, h("span", { class: "count", vars: { "--n": Math.round(n) } }), unit && h("span", { class: "unit", text: unit }));
}

// decimal numbers (grade averages) count up in JS; integers use the CSS counter
function decimal(n, digits = 1) {
  const el = h("span", { class: "big sens", text: (0).toFixed(digits).replace(".", ",") });
  const target = Number(n) || 0;
  if (reduced.matches) {
    el.textContent = target.toFixed(digits).replace(".", ",");
    return el;
  }
  const t0 = performance.now();
  const step = (t) => {
    const k = Math.min(1, (t - t0) / 1100);
    const e = 1 - Math.pow(1 - k, 4);
    el.textContent = (target * e).toFixed(digits).replace(".", ",");
    if (k < 1) requestAnimationFrame(step);
  };
  requestAnimationFrame(step);
  return el;
}

function bar(p, color) {
  return h("div", { class: "progress" }, h("i", { vars: { "--p": Math.max(0, Math.min(1, p)), ...(color ? { "--c": color } : {}) } }));
}

// latest conversations for the dashboard tile
function messageRows(r) {
  const list = (Array.isArray(r) ? r : r?.conversations || []).slice(0, 4);
  if (!list.length) return emptyRow("Keine Unterhaltungen.");
  return h("ul", { class: "rows" }, list.map((c, j) => h("li", {}, h("a", { class: `row ${c.unread ? "unread" : ""}`, href: `#/nachrichten/${c.conversationid}`, vars: { "--j": j } },
    h("span", { class: "dot", "data-src": "moodle" }),
    h("span", { class: "t" }, c.name || "Unterhaltung", h("span", { class: "s preview sens", text: c.last_message })),
    c.unread ? h("span", { class: "chip moodle", text: String(c.unread) }) : h("span", { class: "s", text: (c.last_time || "").slice(5, 10).split("-").reverse().join(".") })))));
}

function emptyRow(text) {
  return h("p", { class: "empty", text });
}

// ── overview ────────────────────────────────────────────────────────────────

function nextUp(agenda, dash) {
  const now = new Date();
  const items = [];
  for (const d of agenda?.days || []) {
    for (const e of d.events || []) {
      const start = parseDE(`${e.date} ${e.start}`);
      const end = parseDE(`${e.date} ${e.end}`) || start;
      if (start && end >= now) items.push({ start, end, title: e.title, nr: e.module_nr, room: e.room, kind: e.kind, src: srcOf(e.source) });
    }
  }
  for (const x of dash?.registered_exams || []) {
    const start = parseDE(x.start);
    if (start && start >= now) items.push({ start, end: start, title: `Klausur ${x.module}`, kind: "Klausur", src: "cis" });
  }
  items.sort((a, b) => a.start - b.start);
  return items;
}

function renderNext(items) {
  const [item, ...rest] = items;
  if (!item) {
    return h("div", { class: "next" },
      h("div", {}, h("div", { class: "what", text: "Nichts geplant." }), h("div", { class: "meta", text: "In den nächsten sieben Tagen stehen keine Termine im Stundenplan." })));
  }
  const now = new Date();
  const running = item.start <= now && item.end > now;
  const mins = Math.round(((running ? item.end : item.start) - now) / 6e4);
  let n = mins, unit = "min";
  if (mins >= 48 * 60) { n = dayDiff(item.start, now); unit = n === 1 ? "Tag" : "Tage"; }
  else if (mins >= 90) { n = Math.round(mins / 60); unit = "Std"; }
  const when = running
    ? `läuft seit ${fmtTime.format(item.start)}`
    : `${dayDiff(item.start) === 0 ? "heute" : fmtShort.format(item.start)} · ${fmtTime.format(item.start)}${item.end > item.start ? `–${fmtTime.format(item.end)}` : ""}`;
  return h("div", { class: "next", "data-src": item.src },
    h("div", {},
      h("span", { class: `chip ${item.src}`, text: item.kind || srcName[item.src] }),
      h("a", { class: "what", href: item.nr ? `#/modul/${firstNr(item.nr)}` : "#/woche", text: item.title }),
      h("div", { class: "meta" }, h("span", { text: when }), item.room && h("span", { text: item.room }), item.nr && h("span", { text: item.nr }))),
    h("div", { class: "countdown" },
      h("h2", { text: running ? "noch" : "in" }),
      count(n, unit)),
    running && bar((now - item.start) / (item.end - item.start)),
    rest.length > 0 && h("div", { class: "after" },
      h("h2", { text: "Danach" }),
      h("ul", { class: "rows" }, rest.slice(0, 2).map((x, j) => h("li", {}, h("div", { class: "row", vars: { "--j": j + 2 } },
        h("span", { class: "dot", "data-src": x.src }),
        h("span", { class: "t" }, x.title, h("span", { class: "s", text: [`${dayDiff(x.start) === 0 ? "heute" : fmtShort.format(x.start)} · ${fmtTime.format(x.start)}`, x.room].filter(Boolean).join(" · ") })),
        h("span", { class: `chip ${x.src}`, text: x.kind || srcName[x.src] })))))));
}

function deadlineRows(list, limit = 6) {
  if (!list.length) return emptyRow("Keine Fristen in den nächsten 30 Tagen.");
  return h("ul", { class: "rows" }, list.slice(0, limit).map((d, j) => {
    const src = srcOf(d.source);
    const due = d.urgent || d.overdue;
    const kids = [
      h("span", { class: "dot", "data-src": src, title: srcName[src] }),
      h("span", { class: "t" }, d.what, h("span", { class: "s", text: [d.when, d.course].filter(Boolean).join(" · ") })),
      h("span", { class: `chip ${due ? "due" : ""}`, text: relDay(d.in_days) }),
    ];
    const props = { class: "row", vars: { "--j": j } };
    const href = src === "moodle" ? "#/abgaben" : d.module_nr ? `#/modul/${firstNr(d.module_nr)}` : null;
    return h("li", {}, href ? h("a", { ...props, href }, kids) : h("div", props, kids));
  }));
}

function weekStrip(agenda) {
  const byDay = new Map();
  for (const d of agenda?.days || []) {
    const day = parseDE(d.date);
    if (day) byDay.set(isoDate(day), d.events || []);
  }
  const today = startOfDay(new Date());
  const cells = [];
  for (let j = 0; j < 7; j++) {
    const day = new Date(today.getFullYear(), today.getMonth(), today.getDate() + j);
    const evs = byDay.get(isoDate(day)) || [];
    cells.push(h("a", { class: `d ${j === 0 ? "today" : ""}`, href: "#/woche", vars: { "--j": j }, title: `${evs.length} Termine` },
      h("span", { class: "dn" }, wd[day.getDay()], h("b", { text: day.getDate() })),
      evs.slice(0, 5).map((e) => h("span", { class: "ev", "data-src": srcOf(e.source) }))));
  }
  const n = [...byDay.values()].reduce((s, e) => s + e.length, 0);
  return [h("div", { class: "strip" }, cells), h("p", { class: "empty", text: n ? `${n} Termine in 7 Tagen` : "Diese Woche ist frei." })];
}

const num = (x) => Number(String(x ?? "").replace(",", ".")) || 0;
const fmtNum = (x) => (Math.round(x * 10) / 10).toLocaleString("de-DE");

// Credits as three parts: modules and Transferleistungen from the study plan,
// seminars from the grades page — the CIS total (cis_credits_total) leaves
// the seminars out, the 210 of the programme include them.
function credits(progress, g) {
  const c = progress?.credits;
  const sem = num(g?.overview?.seminar_credits);
  if (c?.total_required) {
    const parts = [
      { label: "Module", v: c.module_earned || 0, of: c.module_planned || 0, color: "var(--ok)" },
      { label: "Transferleistungen", v: c.transfer_earned || 0, of: c.transfer_planned || 0, color: "var(--cis)" },
      { label: "Seminare", v: sem, of: Math.max(0, c.total_required - (c.module_planned || 0) - (c.transfer_planned || 0)), color: "var(--moodle)" },
    ];
    return { earned: parts.reduce((s, p) => s + p.v, 0), total: c.total_required, parts, cis: num(c.cis_credits_total) };
  }
  return { earned: num(g?.stats?.credits_earned), total: 0, parts: [], cis: 0 };
}

function creditMeter(progress, g) {
  const { earned, total, parts, cis } = credits(progress, g);
  if (!total) return h("div", { class: "meter" }, h("div", { class: "meter-label" }, h("span", { text: `${fmtNum(earned)} Credits` })));
  return h("div", { class: "meter sens" },
    h("div", { class: "meter-label" }, h("span", { text: `${fmtNum(earned)} von ${total} Credits` }), h("span", { text: `${Math.round((earned / total) * 100)} %` })),
    h("div", { class: "progress stacked", role: "img", "aria-label": parts.map((p) => `${p.label} ${fmtNum(p.v)}`).join(", ") },
      parts.map((p, j) => h("i", { vars: { "--w": `${(p.v / total) * 100}%`, "--c": p.color, "--j": j }, title: `${p.label}: ${fmtNum(p.v)}${p.of ? ` von ${p.of}` : ""}` }))),
    h("ul", { class: "legend" }, parts.map((p) => h("li", { vars: { "--c": p.color } }, `${p.label} ${fmtNum(p.v)}${p.of ? ` / ${p.of}` : ""}`))),
    cis && Math.abs(cis - earned) > 0.01 && h("p", { class: "empty fine-note", text: `Das CIS zeigt ${fmtNum(cis)} Credits (ohne Seminare).` }));
}

function gradeSummary([g, progress]) {
  const st = g.stats || {};
  const recent = [...(g.overview?.modules || [])]
    .filter((m) => m.grade_value)
    .sort((a, b) => (parseDE(b.entry_date || b.exam_date) || 0) - (parseDE(a.entry_date || a.exam_date) || 0))
    .slice(0, 3);
  return [
    h("div", { class: "avg" }, decimal(st.weighted_average, 1), h("span", { class: "empty", text: "Schnitt (gewichtet)" })),
    creditMeter(progress, g),
    h("ul", { class: "rows" }, recent.map((m, j) => h("li", {}, h("a", { class: "row", href: `#/modul/${firstNr(m.module_nr)}`, vars: { "--j": j } },
      h("span", { class: "dot", "data-src": "cis" }),
      h("span", { class: "t" }, m.title, h("span", { class: "s", text: m.entry_date || m.exam_date })),
      h("span", { class: `chip sens ${gradeClass(m)}`, text: m.grade }))))),
  ];
}

function examRows(dash) {
  const list = (dash.registered_exams || []).map((x) => ({ ...x, at: parseDE(x.start) })).sort((a, b) => (a.at || 0) - (b.at || 0));
  if (!list.length) return emptyRow("Zu keiner Prüfung angemeldet.");
  return h("ul", { class: "rows" }, list.map((x, j) => h("li", {}, h("div", { class: "row", vars: { "--j": j } },
    h("span", { class: "dot", "data-src": "cis" }),
    h("span", { class: "t" }, x.module, h("span", { class: "s", text: x.start })),
    x.at && h("span", { class: `chip ${dayDiff(x.at) <= 3 ? "due" : ""}`, text: relDay(dayDiff(x.at)) })))));
}

const courseTitle = (c) => String(c.name || c.shortname).replace(/^\s*[A-Z]{1,2}\d{3}\s*-\s*/i, "").split(" - ")[0];

function moodleSummary([dash, courses]) {
  const u = dash.moodle_unread || {};
  const list = courses || [];
  return h("div", { class: "band" },
    h("div", { class: "band-num" },
      h("div", { class: "avg" }, count((u.notifications || 0) + (u.conversations || 0)), h("span", { class: "empty", text: "ungelesen" })),
      h("p", { class: "empty", text: `${u.notifications || 0} Benachrichtigungen · ${u.conversations || 0} Nachrichten` }),
      h("div", { class: "go-links" }, h("a", { class: "go", href: "#/neu", text: "Neuigkeiten" }), h("a", { class: "go", href: "#/nachrichten", text: "Nachrichten" })),
      (u.notifications || u.conversations) ? confirmFlow({
        steps: [u.notifications && ["moodle_mark_notifications_read", {}], u.conversations && ["moodle_mark_messages_read", {}]].filter(Boolean),
        label: "Alle als gelesen markieren", confirm: "Ja, markieren", done: refreshPage,
      }) : null),
    h("ul", { class: "courses", "aria-label": `${list.length} aktuelle Kurse` }, list.map((c, j) => h("li", { vars: { "--j": j } },
      h("a", { href: `#/kurs/${c.id}`, title: c.name },
        h("span", { class: "nr", text: firstNr(c.name) || firstNr(c.shortname) }), courseTitle(c),
        c.progress && h("span", { class: "pct", text: c.progress }))))));
}

function firstName(full) {
  const s = String(full || "").trim();
  if (!s) return "";
  return s.includes(",") ? s.split(",")[1].trim().split(/\s+/)[0] : s.split(/\s+/)[0];
}

async function overview(root) {
  const now = new Date();
  const ui = savedUI();
  const hero = h("section", { class: "hero" });
  if (!ui.greeting_off) {
    const title = h("h1", {}, greet(ui.greeting_text, now, ""), h("small", { text: fmtDay.format(now) }));
    hero.append(title);
    api("moodle_whoami").then((me) => {
      const name = firstName(me.user);
      if (name) title.firstChild.textContent = greet(ui.greeting_text, now, name);
    }).catch(() => {});
  }
  if (ui.search === "home") hero.append(searchBox());
  if (hero.children.length) root.append(hero);
  const dash = api("nak_dashboard");
  const agenda = api("nak_agenda", { days: 7 });
  const grid = h("div", { class: "grid", id: "overview-grid" });
  root.append(grid);

  const make = {
    next: (i) => { const t = tile("Als Nächstes", { cls: "w8", i }); fill(t, () => Promise.all([agenda, dash]), ([a, d]) => renderNext(nextUp(a, d))); return t; },
    deadlines: (i) => { const t = tile("Fristen", { i, link: ["Woche", "#/woche"] }); fill(t, () => api("nak_deadlines", { days: 30 }), (r) => deadlineRows(r.deadlines || [])); return t; },
    week: (i) => { const t = tile("Diese Woche", { cls: "half", i, link: ["Alle Termine", "#/woche"] }); fill(t, () => agenda, weekStrip); return t; },
    grades: (i) => { const t = tile("Noten", { cls: "half", i, link: ["Alle Noten", "#/noten"] }); fill(t, () => Promise.all([api("cis_grades"), api("cis_progress").catch(() => null)]), gradeSummary); return t; },
    exams: (i) => {
      const t = tile("Prüfungen", { cls: "half", i, link: ["Alle Prüfungen", "#/pruefungen"] });
      // with its own tile on the page, pending grades are not repeated here
      fill(t, () => Promise.all([dash, loadPending()]), ([d, p]) => [examRows(d), ...(order.includes("pending") ? [] : pendingRows(p) || [])]);
      return t;
    },
    moodle: (i) => { const t = tile("Moodle · aktuelle Kurse", { cls: "w12", i, link: ["Alle Kurse", "#/kurse"] }); fill(t, () => Promise.all([dash, api("moodle_courses", { classification: "current" })]), moodleSummary); return t; },
    messages: (i) => { const t = tile("Nachrichten", { cls: "half", i, link: ["Inbox", "#/nachrichten"] }); fill(t, () => api("moodle_conversations", { limit: 4 }), messageRows); return t; },
    transfer: (i) => { const t = tile("Transferleistungen", { cls: "half", i, link: ["Alle", "#/transferleistungen"] }); fill(t, () => Promise.all([api("cis_list_transfer"), api("nak_transfer_plan").catch(() => null)]), ([list, plan]) => transferOverview(list, plan)); return t; },
    thesis: (i) => { const t = tile("Bachelorthesis", { cls: "half", i, link: ["Alles", "#/bachelorthesis"] }); fill(t, () => api("nak_thesis"), thesisSummary); return t; },
    transfer_grades: (i) => { const t = tile("TL-Noten · rechnerisch", { cls: "half", i, link: ["Details", "#/transferleistungen"] }); fill(t, () => api("nak_transfer_grades"), (g) => transferGradeSummary(g, 4)); return t; },
    mensa: (i) => { const t = tile("Mensa", { cls: "half", i, link: ["Speiseplan", "#/mensa"] }); fill(t, () => Promise.all([api("mensa_menu", { weeks: 2 }), api("mensa_account", { days: 7 }).catch(() => null)]), ([m, a]) => mensaToday(m, a)); return t; },
    mensa_spending: (i) => { const t = tile("Mensa-Ausgaben", { cls: "half", i, link: ["Mensa", "#/mensa"] }); fill(t, () => api("mensa_spending"), spendingView); return t; },
    pending: (i) => { const t = tile("Noten ausstehend", { cls: "half", i, link: ["Prüfungen", "#/pruefungen"] }); fill(t, loadPending, (p) => pendingRows(p) || emptyRow("Keine Note offen.")); return t; },
  };
  const order = savedHome();
  grid.append(...order.map((id, i) => make[id](i)));

  const foot = h("div", { class: "foot" });
  root.append(foot);
  dash.then((d) => {
    const q = d.quarter?.current;
    if (q) foot.append(h("span", {}, "Quartal ", h("b", { text: q.name }), ` · ${q.from} – ${q.to}`));
    const bal = d.balance && Object.entries(d.balance)[0];
    if (bal) foot.append(h("span", {}, `${bal[0]} `, h("b", { text: bal[1] })));
  }).catch(() => {});
}

// ── week ────────────────────────────────────────────────────────────────────

async function week(root) {
  let view = localStorage.getItem("nak-week-view") || (innerWidth < 760 ? "liste" : "raster");
  const seg = h("div", { class: "filters", role: "group", "aria-label": "Ansicht" });
  root.append(h("div", { class: "page-head" }, h("h1", {}, "Woche", h("small", { text: "Vorlesungen, Klausuren und Moodle-Fristen der nächsten 14 Tage" })), seg));
  const box = h("div", { class: "days" }, skeleton(), skeleton());
  root.append(box);
  let data;
  const draw = () => {
    seg.replaceChildren(...[["raster", "Raster"], ["liste", "Liste"]].map(([k, label]) => h("button", { type: "button", "aria-pressed": k === view ? "true" : "false", text: label, onclick: () => {
      view = k;
      localStorage.setItem("nak-week-view", k);
      draw();
    } })));
    if (!data) return;
    const days = data.days || [];
    if (!days.length) return box.replaceChildren(emptyRow("In den nächsten 14 Tagen steht nichts an."));
    box.className = view === "raster" ? "week-grid-wrap" : "days";
    box.replaceChildren(...(view === "raster" ? [weekGrid(days)] : weekList(days)));
    if (data.unavailable_sources) box.prepend(errorBox(new Error(`Nicht erreichbar: ${Object.keys(data.unavailable_sources).join(", ")}`)));
  };
  draw();
  try {
    data = await api("nak_agenda", { days: 14 });
    draw();
  } catch (err) {
    box.replaceChildren(errorBox(err));
  }
}

function eventHref(e) {
  const nr = firstNr(e.module_nr);
  if (nr) return `#/modul/${nr}`;
  return srcOf(e.source) === "moodle" ? "#/abgaben" : null;
}

function weekList(days) {
  return days.map((d) => {
    const day = parseDE(d.date);
    const diff = day ? dayDiff(day) : 99;
    return h("section", { class: `day ${diff === 0 ? "today" : ""}` },
      h("h3", {}, day ? fmtDay.format(day).split(",")[0] : d.date, h("span", { text: day ? `${day.getDate()}.${day.getMonth() + 1}.` : "" })),
      h("ul", { class: "rows" }, (d.events || []).map((e) => {
        const src = srcOf(e.source);
        const href = eventHref(e);
        const kids = [
          h("span", { class: "time" }, e.start, e.end && e.end !== e.start && h("small", { text: e.end })),
          h("span", { class: "dot", "data-src": src }),
          h("span", { class: "t" }, e.title, h("span", { class: "s", text: [e.module_nr, e.room, e.lecturer].filter(Boolean).join(" · ") })),
          h("span", { class: `chip ${e.kind === "Klausur" ? "due" : src}`, text: e.kind || srcName[src] })];
        return h("li", {}, href ? h("a", { class: "row", href }, kids) : h("div", { class: "row" }, kids));
      })));
  });
}

// weekGrid: days as columns, 07–21 h as rows, events positioned by time.
function weekGrid(days) {
  const H0 = 7, H1 = 21;
  const span = (H1 - H0) * 60;
  const minutes = (hhmm) => { const [hh, mm] = String(hhmm).split(":").map(Number); return (hh * 60 + mm) - H0 * 60; };
  const today = startOfDay(new Date());
  // seven days from today; weekend days only when something is on
  const byDate = new Map(days.map((d) => [isoDate(parseDE(d.date) || today), d.events || []]));
  const cols = [];
  for (let i = 0; i < 14 && cols.length < 7; i++) {
    const d = new Date(today.getFullYear(), today.getMonth(), today.getDate() + i);
    const evs = byDate.get(isoDate(d)) || [];
    const weekend = d.getDay() === 0 || d.getDay() === 6;
    if (weekend && !evs.length) continue;
    cols.push({ d, evs });
  }
  const hours = h("div", { class: "wg-hours" }, Array.from({ length: H1 - H0 + 1 }, (_, i) => h("span", { vars: { "--t": i / (H1 - H0) }, text: `${H0 + i}` })));
  const grid = h("div", { class: "week-grid", lang: "de", vars: { "--cols": cols.length } }, hours, cols.map(({ d, evs }, j) => {
    const isToday = isoDate(d) === isoDate(today);
    const col = h("div", { class: `wg-col ${isToday ? "today" : ""} ${d < today ? "past" : ""}`, vars: { "--j": j } },
      h("div", { class: "wg-head" }, h("span", { text: wd[d.getDay()] }), h("b", { text: d.getDate() })),
      h("div", { class: "wg-body" }, evs.map((e, k) => {
        const src = srcOf(e.source);
        // things outside 07–21 (a 23:59 deadline) sit at the edge, not off-grid
        const top = Math.min(span - 75, Math.max(0, minutes(e.start))) / span;
        const len = e.end && e.end !== e.start ? Math.max(20, minutes(e.end) - minutes(e.start)) / span : 0;
        const href = eventHref(e);
        const props = { class: `wg-ev ${len ? "" : "due-mark"} ${e.kind === "Klausur" ? "exam" : ""}`, "data-src": src, title: [e.start, e.end && e.end !== e.start && `– ${e.end}`, e.title, e.room].filter(Boolean).join(" "), vars: { "--top": top, "--len": len, "--k": k } };
        const inner = [h("b", { text: e.title }), h("span", { text: [e.start + (len ? `–${e.end}` : ""), e.room].filter(Boolean).join(" · ") })];
        return href ? h("a", { ...props, href }, inner) : h("div", props, inner);
      }),
      isToday && h("span", { class: "wg-now", vars: { "--top": Math.min(1, Math.max(0, ((new Date().getHours() - H0) * 60 + new Date().getMinutes()) / span)) } })));
    return col;
  }));
  // the now line moves with the clock
  const tick = setInterval(() => {
    const line = grid.querySelector(".wg-now");
    if (!line || !document.body.contains(grid)) return clearInterval(tick);
    const n = new Date();
    line.style.setProperty("--top", Math.min(1, Math.max(0, ((n.getHours() - H0) * 60 + n.getMinutes()) / span)));
  }, 60_000);
  return grid;
}

// ── grades ──────────────────────────────────────────────────────────────────

function thesisView(tr) {
  if (!tr) return emptyRow("Keine Angaben im CIS.");
  const label = (x) => typeof x === "string" ? x : [x.module_nr, x.title || x.name || x.topic].filter(Boolean).join(" ") || JSON.stringify(x);
  const mods = tr.modules_until_4th_missing || [], tls = tr.transferleistungen_missing || [];
  return [
    h("div", { class: "avg" }, h("span", { class: `big ${tr.fulfilled ? "ok-text" : ""}`, text: tr.fulfilled ? "✓" : String(mods.length + tls.length) }),
      h("span", { class: "empty", text: tr.fulfilled ? "Voraussetzungen erfüllt" : "noch offen bis zur Zulassung" })),
    !tr.fulfilled && h("ul", { class: "rows meter" }, [
      ...mods.map((m, j) => h("li", {}, h("a", { class: "row", href: `#/modul/${firstNr(label(m))}`, vars: { "--j": j } }, h("span", { class: "dot", "data-src": "cis" }), h("span", { class: "t" }, label(m), h("span", { class: "s", text: "Modulprüfung bis einschließlich 4. Semester" })), h("span", { class: "chip due", text: "fehlt" })))),
      ...tls.map((t, j) => h("li", {}, h("a", { class: "row", href: "#/transferleistungen", vars: { "--j": j + mods.length } }, h("span", { class: "dot", "data-src": "cis" }), h("span", { class: "t" }, `Transferleistung ${label(t)}`, h("span", { class: "s", text: "Transferleistungen 1–5" })), h("span", { class: "chip due", text: "fehlt" })))),
    ]),
    tr.rule && h("p", { class: "empty fine-note", text: tr.rule }),
  ];
}

// semesterBars: passed / open / failed per semester as stacked bars.
function semesterBars(sems) {
  if (!sems.length) return emptyRow("Kein Studienplan.");
  const max = Math.max(...sems.map((x) => (x.passed || 0) + (x.open || 0) + (x.failed || 0)), 1);
  return [h("div", { class: "sem-bars sens", role: "img", "aria-label": sems.map((x) => `Semester ${x.semester}: ${x.passed} bestanden, ${x.open} offen, ${x.failed} nicht bestanden`).join("; ") },
    sems.map((x, j) => h("div", { class: "sem", vars: { "--j": j } },
      h("div", { class: "sem-stack", vars: { "--h": ((x.passed || 0) + (x.open || 0) + (x.failed || 0)) / max } },
        [["failed", x.failed, "var(--bad)"], ["open", x.open, "var(--tile-2)"], ["passed", x.passed, "var(--ok)"]].filter(([, n]) => n > 0).map(([k, n, c]) => h("i", { class: k, vars: { "--n": n, "--c": c }, title: `${n} ${k === "passed" ? "bestanden" : k === "open" ? "offen" : "nicht bestanden"}` }))),
      h("small", { text: `${x.semester}.` })))),
    h("ul", { class: "legend" }, [["bestanden", "var(--ok)"], ["offen", "var(--dim)"], ["nicht bestanden", "var(--bad)"]].map(([l, c]) => h("li", { vars: { "--c": c }, text: l })))];
}

// gradeCalculator: expected grades for open modules → projected weighted average.
function gradeCalculator(g, p) {
  const graded = (g.overview?.modules || []).filter((m) => m.grade_value && m.grade_value <= 4);
  let sum = 0, weight = 0;
  for (const m of graded) { const c = num(m.credits); sum += m.grade_value * c; weight += c; }
  const open = [];
  const seen = new Set(graded.map((m) => firstNr(m.module_nr)));
  for (const sem of p?.semesters || []) for (const m of sem.modules || []) {
    const nr = firstNr(m.module_nr);
    if (m.status !== "bestanden" && num(m.credits) > 0 && !seen.has(nr)) { seen.add(nr); open.push({ ...m, nr, semester: sem.semester }); }
  }
  if (!open.length) return emptyRow("Keine offenen benoteten Module.");
  const grades = ["–", "1,0", "1,3", "1,7", "2,0", "2,3", "2,7", "3,0", "3,3", "3,7", "4,0"];
  const result = h("span", { class: "big sens", text: weight ? (sum / weight).toFixed(2).replace(".", ",") : "–" });
  const delta = h("span", { class: "empty" });
  const pick = new Map();
  const update = () => {
    let s2 = sum, w2 = weight;
    for (const [m, v] of pick) { const c = num(m.credits); s2 += v * c; w2 += c; }
    const now = weight ? sum / weight : 0, then = w2 ? s2 / w2 : 0;
    result.textContent = w2 ? then.toFixed(2).replace(".", ",") : "–";
    result.classList.remove("bump");
    void result.offsetWidth;
    result.classList.add("bump");
    delta.textContent = pick.size ? `${then < now ? "besser" : then > now ? "schlechter" : "gleich"} als heute (${now.toFixed(2).replace(".", ",")}) · ${pick.size} Modul${pick.size > 1 ? "e" : ""} eingerechnet` : "Wähle erwartete Noten, um den Schnitt hochzurechnen.";
  };
  const rows = open.map((m) => {
    const sel = h("select", { "aria-label": `Erwartete Note ${m.title}`, onchange: (e) => {
      const v = e.currentTarget.value;
      if (v === "–") pick.delete(m); else pick.set(m, num(v));
      update();
    } }, grades.map((x) => h("option", { value: x, text: x })));
    return h("li", { class: "calc-row" }, h("span", { class: "t" }, m.title, h("span", { class: "s", text: [m.nr, `${m.credits} Credits`, `${m.semester}. Semester`, m.status !== "offen" && m.status].filter(Boolean).join(" · ") })), sel);
  });
  update();
  return [h("div", { class: "avg calc-head" }, result, delta), h("ul", { class: "rows calc" }, rows),
    h("p", { class: "empty fine-note", text: "Hochrechnung, gewichtet nach Credits." })];
}

function gradeClass(m) {
  const s = `${m.status || ""} ${m.grade || ""}`.toLowerCase();
  if (s.includes("nicht") || (m.grade_value && m.grade_value > 4)) return "bad";
  if ((m.grade_value && m.grade_value <= 4) || s.includes("bestanden")) return "ok";
  return "";
}

function gradeState(m) {
  const c = gradeClass(m);
  return c === "ok" ? "passed" : c === "bad" ? "failed" : "open";
}

async function gradesPage(root) {
  root.append(h("div", { class: "page-head" }, h("h1", {}, "Noten", h("small", { text: "Leistungsübersicht aus dem CIS" }))));
  const grid = h("div", { class: "grid" });
  root.append(grid);
  const tSum = tile("Überblick", { cls: "w12", i: 0 });
  const tThesis = tile("Bachelorarbeit", { cls: "w6", i: 1 });
  const tPath = tile("Studienverlauf", { cls: "w6", i: 2 });
  const tCalc = tile("Notenrechner", { cls: "w12", i: 3 });
  const tList = tile("Module", { cls: "w12", i: 4 });
  grid.append(tSum, tThesis, tPath, tCalc, tList);
  const data = api("cis_grades");
  const prog = api("cis_progress");
  fill(tThesis, () => prog, (p) => thesisView(p.thesis_requirements));
  fill(tPath, () => prog, (p) => semesterBars(p.semesters || []));
  fill(tCalc, () => Promise.all([data, prog]), ([g, p]) => gradeCalculator(g, p));

  fill(tSum, () => Promise.all([data, api("cis_progress").catch(() => null)]), ([g, progress]) => {
    const st = g.stats || {};
    return h("div", { class: "grid" },
      h("div", { class: "tile-inner half" }, h("div", { class: "avg" }, decimal(st.weighted_average, 2), h("span", { class: "empty", text: "gewichteter Schnitt" }))),
      h("div", { class: "tile-inner half" },
        creditMeter(progress, g),
        h("p", { class: "empty", text: `${st.passed || 0} bestanden · ${st.open || 0} offen · ${st.failed || 0} nicht bestanden` })));
  });

  fill(tList, () => data, (g) => {
    const mods = g.overview?.modules || [];
    let filter = "all";
    const body = h("tbody");
    const draw = () => {
      const rows = mods.filter((m) => filter === "all" || gradeState(m) === filter);
      body.replaceChildren(...rows.map((m, j) => h("tr", { vars: { "--j": Math.min(j, 20) } },
        h("td", { class: "nr", text: m.module_nr }),
        h("td", { class: "name" }, h("a", { href: `#/modul/${firstNr(m.module_nr)}`, text: m.title })),
        h("td", { class: "nr", text: m.exam_date || "" }),
        h("td", { class: "nr r", text: m.attempt ? `${m.attempt}.` : "" }),
        h("td", { class: "nr r", text: m.credits || "" }),
        h("td", { class: "grade" }, h("span", { class: `chip sens ${gradeClass(m)}`, text: m.grade || m.status || "–" })))));
      if (!rows.length) body.append(h("tr", {}, h("td", { colspan: 6, class: "empty", text: "Keine Module in dieser Ansicht." })));
    };
    const filters = h("div", { class: "filters", role: "group", "aria-label": "Filter" },
      [["all", "Alle"], ["passed", "Bestanden"], ["open", "Offen"], ["failed", "Nicht bestanden"]].map(([k, label]) =>
        h("button", { type: "button", "aria-pressed": k === filter ? "true" : "false", text: label, onclick: (e) => {
          filter = k;
          for (const b of filters.children) b.setAttribute("aria-pressed", b === e.currentTarget ? "true" : "false");
          draw();
        } })));
    draw();
    return [filters, h("div", { class: "table-wrap" }, h("table", {},
      h("thead", {}, h("tr", {}, ["Nr.", "Modul", "Prüfung", "Versuch", "Credits", "Note"].map((x, i) => h("th", { class: i >= 3 ? "r" : "", text: x })))),
      body))];
  });
}

// ── modules ─────────────────────────────────────────────────────────────────

// ── search ──────────────────────────────────────────────────────────────────

function searchBox() {
  const input = h("input", { type: "search", class: "search-input", placeholder: "Suchen: Kurse, Module, Dateien, Aktivitäten …", "aria-label": "Suchen", autocomplete: "off", spellcheck: "false" });
  const list = h("div", { class: "search-results", role: "listbox", hidden: true });
  const wrap = h("div", { class: "search", role: "search" },
    h("span", { class: "search-icon" }, svg(["M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14z", "M20 20l-4-4"])),
    input, h("kbd", { text: "/", title: "Mit / springst du hierher, mit Strg+K öffnet sich die Befehlspalette" }), list);
  let timer, seq = 0, active = -1;

  function items() { return [...list.querySelectorAll(".hit")]; }
  function mark(i) {
    const all = items();
    active = Math.max(-1, Math.min(i, all.length - 1));
    all.forEach((el, j) => el.setAttribute("aria-selected", j === active ? "true" : "false"));
    all[active]?.scrollIntoView({ block: "nearest" });
  }
  function group(title, hits) {
    if (!hits.length) return null;
    return h("div", { class: "hit-group" }, h("h2", { text: title }), hits);
  }
  function hit(props, kids) {
    return h(props.href ? "a" : "button", { class: "hit", role: "option", ...props, ...(props.href ? {} : { type: "button" }) }, kids);
  }

  function unitHit(u) {
    const cur = u.courses.find((c) => c.state === "current");
    return hit({ href: `#/modul/${u.nr}` }, [
      h("span", { class: "dot", "data-src": cur ? "moodle" : "cis" }),
      h("span", { class: "t", text: u.title || u.nr }),
      h("span", { class: "s", text: [u.nr, unitLabel(u)].filter((x, i, a) => x && a.indexOf(x) === i).join(" · ") })]);
  }

  function contentHits(found) {
    return (found || []).slice(0, 10).map((x) => {
      // moodle_find returns either a matching file (file/fileurl) or a
      // matching activity (module/type, maybe with its files)
      const f = x.fileurl ? { name: x.file, fileurl: x.fileurl, size: x.size } : x.type === "resource" && x.files?.length === 1 ? x.files[0] : null;
      if (f) return hit({ onclick: () => openFile(f.fileurl) }, [h("span", { class: "dot", "data-src": "moodle" }), h("span", { class: "t", text: f.name || x.module }), h("span", { class: "s", text: [x.course, f.size].filter(Boolean).join(" · ") })]);
      return hit({ href: x.courseid ? `#/kurs/${x.courseid}` : "#/kurse" }, [h("span", { class: "dot", "data-src": "moodle" }), h("span", { class: "t", text: x.module }), h("span", { class: "s", text: [typeName[x.type] || x.type, x.course].filter(Boolean).join(" · ") })]);
    });
  }

  async function findContent(q, my, head) {
    list.replaceChildren(...head, h("p", { class: "empty hit-note", text: "Suche in Dateien und Aktivitäten …" }));
    let found = [];
    try {
      const r = await api("moodle_find", { query: q });
      found = Array.isArray(r) ? r : r?.results || r?.hits || [];
    } catch {}
    if (my !== seq) return;
    list.replaceChildren(...head, group("Dateien & Aktivitäten", contentHits(found)) || h("p", { class: "empty hit-note", text: `Nichts zu „${q}“ gefunden.` }));
    mark(-1);
  }

  async function run(q) {
    const my = ++seq;
    let units = [], orphans = [];
    try { ({ units, orphans } = await loadUnits()); } catch (e) { console.error("nak units", e); }
    if (my !== seq) return;
    const want = norm(q).join(" ");
    const uh = units
      .filter((u) => matches(q, norm([u.nr, ...u.aliases, u.title, ...u.courses.map((c) => c.name)].join(" "))))
      .sort((a, b) => (norm(b.title).join(" ") === want) - (norm(a.title).join(" ") === want)
        || b.courses.some((c) => c.state === "current") - a.courses.some((c) => c.state === "current"))
      .slice(0, 6);
    const oh = orphans.filter((c) => matches(q, norm(`${c.name} ${c.shortname}`))).slice(0, 4);
    list.hidden = false;
    if (!uh.length && !oh.length) {
      if (q.length >= 3) return findContent(q, my, []);
      list.replaceChildren(h("p", { class: "empty hit-note", text: "Weiter tippen …" }));
      return;
    }
    const head = [
      group("Module", uh.map(unitHit)),
      group("Weitere Kurse", oh.map((c) => hit({ href: `#/kurs/${c.id}` }, [h("span", { class: "dot", "data-src": "moodle" }), h("span", { class: "t", text: courseTitle(c) }), h("span", { class: "s", text: c.state === "current" ? "aktuell" : c.shortname })]))),
    ].filter(Boolean);
    const more = q.length >= 3 && hit({ class: "hit more", onclick: (e) => { e.stopPropagation(); findContent(q, ++seq, head); } }, [h("span", { class: "dot" }), h("span", { class: "t", text: `„${q}“ in Dateien und Aktivitäten suchen` }), h("span", { class: "s", text: "↵" })]);
    list.replaceChildren(...head, more || []);
    mark(uh.length + oh.length === 1 ? 0 : -1);
  }

  input.addEventListener("input", () => {
    clearTimeout(timer);
    const q = input.value.trim();
    if (!q) { list.hidden = true; seq++; return; }
    timer = setTimeout(() => run(q), 250);
  });
  input.addEventListener("keydown", (e) => {
    if (e.key === "ArrowDown") { e.preventDefault(); mark(active + 1); }
    else if (e.key === "ArrowUp") { e.preventDefault(); mark(active - 1); }
    else if (e.key === "Enter" && active >= 0) { e.preventDefault(); items()[active].click(); }
    else if (e.key === "Escape") { list.hidden = true; input.blur(); }
  });
  list.addEventListener("click", () => { list.hidden = true; });
  document.addEventListener("click", (e) => { if (!wrap.contains(e.target)) list.hidden = true; });
  return wrap;
}

addEventListener("keydown", (e) => {
  if (e.key === "/" && !/^(INPUT|TEXTAREA)$/.test(document.activeElement?.tagName)) {
    const s = $(".search-input");
    if (s) { e.preventDefault(); s.focus(); }
    else if (savedUI().search !== "home") { e.preventDefault(); openSearch(); }
  }
});

// ── files ───────────────────────────────────────────────────────────────────

// Opens a Moodle file inside nak: downloaded once on the server, then shown
// by the browser (PDF, images, text) or saved.
async function openFile(fileurl, btn) {
  const win = window.open("", "_blank"); // must open inside the click, before awaiting
  btn?.classList.add("busy");
  try {
    const r = await post("moodle_download", { fileurl });
    if (!r.download_url) throw new Error("Datei konnte nicht bereitgestellt werden");
    const u = `${r.download_url}?inline=1`;
    if (win) win.location = u;
    else location.href = u;
  } catch (err) {
    win?.close();
    alertBox(err.message);
  } finally {
    btn?.classList.remove("busy");
  }
}

function alertBox(msg) {
  const t = h("div", { class: "toast", role: "alert", text: msg });
  document.body.append(t);
  setTimeout(() => t.remove(), 5000);
}

function fileRow(f, j = 0) {
  const b = h("button", { class: "row file", type: "button", vars: { "--j": j }, onclick: (e) => openFile(f.fileurl, e.currentTarget) },
    h("span", { class: "ext", text: (String(f.name).split(".").pop() || "").slice(0, 4) }),
    h("span", { class: "t" }, f.name, h("span", { class: "s", text: [f.size, f.modified].filter(Boolean).join(" · ") })),
    h("span", { class: "chip moodle", text: "öffnen" }));
  return h("li", {}, b);
}

// ── courses ─────────────────────────────────────────────────────────────────

const typeName = {
  resource: "Datei", folder: "Ordner", url: "Link", page: "Seite", label: "Text", assign: "Abgabe", quiz: "Test",
  forum: "Forum", choice: "Abstimmung", feedback: "Umfrage", lesson: "Lektion", book: "Buch", h5pactivity: "H5P",
  bigbluebuttonbn: "Videokonferenz", scorm: "SCORM", wiki: "Wiki", glossary: "Glossar", workshop: "Workshop", data: "Datenbank",
};

async function coursesPage(root) {
  let which = "current";
  root.append(h("div", { class: "page-head" }, h("h1", {}, "Kurse", h("small", { text: "Alle Moodle-Kurse, direkt hier lesen" }))));
  const box = h("div", { class: "mods" });
  const filters = h("div", { class: "filters", role: "group", "aria-label": "Zeitraum" },
    [["current", "Aktuell"], ["past", "Vergangen"], ["all", "Alle"]].map(([k, label]) =>
      h("button", { type: "button", "aria-pressed": k === which ? "true" : "false", text: label, onclick: (e) => {
        which = k;
        for (const b of filters.children) b.setAttribute("aria-pressed", b === e.currentTarget ? "true" : "false");
        load();
      } })));
  root.querySelector(".page-head").append(filters);
  root.append(box);
  async function load() {
    box.replaceChildren(...[0, 1, 2].map((i) => tile("", { i }, skeleton())));
    try {
      const list = await api("moodle_courses", { classification: which });
      if (!list.length) return box.replaceChildren(emptyRow("Keine Kurse in dieser Ansicht."));
      box.replaceChildren(...list.map((c, i) => {
        const pct = parseInt(c.progress, 10);
        const t = tile("", { i: Math.min(i, 12) },
          h("a", { class: "mod", href: `#/kurs/${c.id}` },
            h("span", { class: "nr", text: firstNr(c.name) || c.shortname }),
            h("span", { class: "name", text: courseTitle(c) }),
            h("span", { class: "tags" }, c.last_access && h("span", { class: "chip", text: `zuletzt ${c.last_access.slice(0, 10)}` })),
            !isNaN(pct) && bar(pct / 100, "var(--moodle)")));
        t.querySelector(".tile-head").remove();
        return t;
      }));
    } catch (err) {
      box.replaceChildren(errorBox(err));
    }
  }
  await load();
}

function textBlock(text) {
  return text ? h("p", { class: "prose", text }) : null;
}

function moduleItem(m, courseId, assignByCm, j) {
  const kind = typeName[m.type] || m.type;
  const dates = Object.entries(m.dates || {}).map(([k, v]) => `${k} ${v}`).join(" · ");
  if (m.type === "label") return h("li", { class: "label" }, textBlock(m.text));
  const files = m.files || [];
  let action = null;
  if (m.type === "assign" && assignByCm[m.cmid]) action = h("a", { class: "chip due", href: `#/abgabe/${assignByCm[m.cmid]}`, text: "Aufgabe" });
  else if (m.type === "forum") action = h("a", { class: "chip moodle", href: `#/kurs/${courseId}/forum`, text: "Forum" });
  else if (m.type === "url" && m.links?.[0]) action = h("a", { class: "chip moodle", href: m.links[0], target: "_blank", rel: "noopener", text: "Link ↗" });
  else if (!files.length && m.type !== "page" && m.url) action = h("a", { class: "chip", href: m.url, target: "_blank", rel: "noopener", title: "Diese Aktivität geht nur in Moodle selbst", text: "in Moodle ↗" });
  return h("li", { class: "item", vars: { "--j": j } },
    h("div", { class: "item-head" },
      h("span", { class: "kind", text: kind }),
      h("span", { class: "t", text: m.name }),
      action),
    dates && h("span", { class: "s", text: dates }),
    textBlock(m.text),
    files.length > 0 && h("ul", { class: "rows files" }, files.map((f, k) => fileRow(f, k))));
}

function assignmentRows(as) {
  if (!as.length) return emptyRow("Keine Abgaben.");
  const now = Date.now() / 1000;
  return h("ul", { class: "rows" }, as.map((a, j) => {
    const open = !a.due_unix || a.due_unix > now;
    const days = a.due_unix ? dayDiff(new Date(a.due_unix * 1000)) : null;
    return h("li", {}, h("a", { class: "row", href: `#/abgabe/${a.assignid}`, vars: { "--j": j } },
      h("span", { class: "dot", "data-src": "moodle" }),
      h("span", { class: "t" }, a.name, h("span", { class: "s", text: [a.course, a.due ? `fällig ${a.due}` : "ohne Frist"].filter(Boolean).join(" · ") })),
      h("span", { class: `chip ${open && days !== null && days <= 3 ? "due" : ""}`, text: open ? (days === null ? "offen" : relDay(days)) : "vorbei" })));
  }));
}

// A Moodle choice (vote or group sign-up): options with seats, your answer,
// and voting through the confirm gate.
function choiceView(c) {
  const opts = c.options || [];
  const picked = new Set(opts.filter((o) => o.selected).map((o) => o.optionid));
  const inputs = opts.map((o) => h("input", { type: c.multiple ? "checkbox" : "radio", name: `choice-${c.choiceid}`, value: o.optionid, checked: o.selected, disabled: o.disabled || (!c.open) || (picked.size > 0 && !c.can_change_answer) }));
  const list = h("ul", { class: "choice-opts" }, opts.map((o, i) => h("li", {}, h("label", { class: "inline-check" }, inputs[i],
    h("span", { text: o.text }), o.taken != null && h("span", { class: "s", text: `${o.taken} gewählt` })))));
  const canVote = c.open && (picked.size === 0 || c.can_change_answer);
  return h("div", { class: "choice" },
    h("div", { class: "tl-head" }, h("b", { text: c.name }), h("span", { class: `chip ${c.open ? "ok" : ""}`, text: c.open ? "offen" : "geschlossen" }),
      c.closes && h("span", { class: "s", text: `bis ${c.closes}` })),
    textBlock(c.intro),
    list,
    canVote && confirmFlow({ tool: "moodle_choice_submit", label: picked.size ? "Wahl ändern" : "Abstimmen", confirm: "Stimme verbindlich abgeben", cls: "primary",
      args: () => { const ids = inputs.filter((x) => x.checked).map((x) => Number(x.value)); return ids.length ? { choiceid: c.choiceid, optionids: ids } : null; }, done: refreshPage }));
}

function quizRows(qs) {
  if (!qs.length) return emptyRow("Keine Tests.");
  return h("ul", { class: "rows" }, qs.map((q, j) => h("li", {}, h("a", { class: "row", href: q.url, target: "_blank", rel: "noopener", title: "Tests laufen in Moodle selbst", vars: { "--j": j } },
    h("span", { class: "dot", "data-src": "moodle" }),
    h("span", { class: "t" }, q.name, h("span", { class: "s", text: [q.opens && `ab ${q.opens}`, q.closes && `bis ${q.closes}`, q.attempts_allowed && `Versuche: ${q.attempts_allowed}`].filter(Boolean).join(" · ") })),
    h("span", { class: "chip", text: "in Moodle ↗" })))));
}

async function assignmentPage(root, id) {
  root.append(h("a", { class: "back", href: "#/abgaben" }, svg(icons.back), "Abgaben"));
  const head = h("div", { class: "page-head" }, h("h1", { text: "Aufgabe" }));
  root.append(head);
  const box = h("div", { class: "grid" }, tile("", { cls: "w12" }, skeleton()));
  root.append(box);
  try {
    const a = await api("moodle_assignment", { assignid: Number(id) });
    head.querySelector("h1").replaceChildren(a.name, h("small", { text: a.course }));
    const sub = a.submission || {};
    box.replaceChildren(
      tile("Aufgabe", { cls: "w8" },
        h("dl", { class: "kv" },
          a.opens && [h("dt", { text: "ab" }), h("dd", { text: a.opens })],
          h("dt", { text: "fällig" }), h("dd", { text: a.due || "ohne Frist" }),
          a.cutoff && [h("dt", { text: "letzte Abgabe" }), h("dd", { text: a.cutoff })],
          a.max_grade && [h("dt", { text: "max. Punkte" }), h("dd", { text: a.max_grade })]),
        textBlock(a.intro),
        (a.attachments || []).length > 0 && h("ul", { class: "rows files meter" }, a.attachments.map((f, k) => fileRow(f, k)))),
      tile("Meine Abgabe", { cls: "", i: 1 },
        h("dl", { class: "kv" }, Object.entries(sub).filter(([, v]) => typeof v !== "object" && v !== "").map(([k, v]) => [h("dt", { text: statusLabel[k] || k }), h("dd", { text: statusLabel[v] || v })])),
        (sub.files || []).length > 0 && h("ul", { class: "rows files meter" }, sub.files.map((f, k) => typeof f === "object" ? fileRow(f, k) : h("li", { class: "s", text: f }))),
        a.feedback && textBlock(typeof a.feedback === "string" ? a.feedback : JSON.stringify(a.feedback)),
        submitForm(a)));
  } catch (err) {
    box.replaceChildren(errorBox(err));
  }
}

// Upload goes to naknak first (/api/upload), the submission itself through
// the confirm gate: draft by default, "endgültig einreichen" only when ticked.
function submitForm(a) {
  const files = h("input", { type: "file", multiple: true, "aria-label": "Dateien" });
  const text = h("textarea", { rows: 3, placeholder: "Online-Text (falls die Aufgabe das erlaubt)" });
  const final = h("input", { type: "checkbox" });
  const note = h("p", { class: "empty" });
  const drop = h("label", { class: "drop" }, files, h("span", { text: "Dateien hierher ziehen oder auswählen" }));
  const list = h("ul", { class: "rows files" });
  const showFiles = () => list.replaceChildren(...[...files.files].map((f) => h("li", { class: "s", text: `${f.name} · ${Math.round(f.size / 1024)} KB` })));
  files.addEventListener("change", showFiles);
  drop.addEventListener("dragover", (e) => { e.preventDefault(); drop.classList.add("over"); });
  drop.addEventListener("dragleave", () => drop.classList.remove("over"));
  drop.addEventListener("drop", (e) => { e.preventDefault(); drop.classList.remove("over"); files.files = e.dataTransfer.files; showFiles(); });
  const flow = confirmFlow({
    tool: "moodle_assignment_submit", label: "Vorschau der Abgabe", cls: "primary",
    confirm: "Verbindlich an Moodle senden",
    args: async () => {
      let paths = [];
      if (files.files.length) {
        const fd = new FormData();
        for (const f of files.files) fd.append("file", f);
        note.textContent = "Lade Dateien zu naknak hoch …";
        const res = await fetch("/api/upload", { method: "POST", body: fd, credentials: "same-origin" });
        const body = await res.json().catch(() => ({}));
        if (!res.ok) { note.textContent = body.error || `Upload fehlgeschlagen (HTTP ${res.status})`; return null; }
        paths = body.files.map((f) => f.path);
        note.textContent = "";
      }
      if (!paths.length && !text.value.trim()) { note.textContent = "Wähle Dateien oder schreibe einen Online-Text."; return null; }
      return { assignid: a.assignid, files: paths, online_text: text.value.trim() || undefined, submit_for_grading: final.checked };
    },
    done: refreshPage,
  });
  return h("div", { class: "submit-form" },
    h("h2", { text: "Abgeben" }), drop, list, text,
    h("label", { class: "check" }, final, h("span", {}, h("b", { text: "Endgültig zur Bewertung einreichen" }), h("small", { text: "Sonst bleibt es ein Entwurf, den du noch ändern kannst." }))),
    note, flow);
}

const statusLabel = {
  status: "Status", gradingstatus: "Bewertung", last_modified: "zuletzt geändert", grade: "Note",
  new: "noch nichts abgegeben", draft: "Entwurf", submitted: "abgegeben", notgraded: "nicht bewertet", graded: "bewertet", reopened: "wieder geöffnet",
};

async function allAssignmentsPage(root) {
  root.append(h("div", { class: "page-head" }, h("h1", {}, "Abgaben & Tests", h("small", { text: "Offene Aufgaben aus allen Kursen" }))));
  const grid = h("div", { class: "grid" });
  root.append(grid);
  const ta = tile("Abgaben", { cls: "w6" });
  const tq = tile("Tests", { cls: "w6", i: 1 });
  grid.append(ta, tq);
  fill(ta, () => api("moodle_assignments", { only_open: true }), assignmentRows);
  fill(tq, () => api("moodle_quizzes", { only_open: true }), quizRows);
}

async function discussionPage(root, id) {
  root.append(h("a", { class: "back", href: "#", onclick: (e) => { e.preventDefault(); history.back(); } }, svg(icons.back), "zurück"));
  const head = h("div", { class: "page-head" }, h("h1", { text: "Diskussion" }));
  root.append(head);
  const box = h("div", { class: "thread" }, skeleton());
  root.append(box);
  try {
    const r = await api("moodle_forum_posts", { discussionid: Number(id) });
    const posts = Array.isArray(r) ? r : r?.posts || [];
    if (posts[0]) head.querySelector("h1").textContent = posts[0].subject;
    box.replaceChildren(...posts.map((p, i) => {
      const ta = h("textarea", { rows: 3, placeholder: `Antwort an ${p.author} …`, "aria-label": "Antwort" });
      const reply = h("details", { class: "reply" }, h("summary", { text: "Antworten" }), ta,
        confirmFlow({ tool: "moodle_forum_reply", label: "Vorschau", confirm: "Antwort verbindlich posten", cls: "primary",
          args: () => ta.value.trim() ? { postid: p.postid, message: ta.value.trim() } : null, done: refreshPage }));
      return h("article", { class: "post tile", vars: { "--i": Math.min(i, 10) } },
        h("header", {}, h("b", { text: p.author }), h("span", { class: "s", text: p.time })),
        i > 0 && p.subject && !String(p.subject).startsWith("Re:") && h("h3", { text: p.subject }),
        textBlock(p.message),
        p.postid && reply);
    }));
  } catch (err) {
    box.replaceChildren(errorBox(err));
  }
}

// ── news & messages ─────────────────────────────────────────────────────────

// messages and Moodle news share one place: the "Inbox" in the navigation
function inboxHead(root, current) {
  root.append(h("div", { class: "page-head" }, h("h1", {}, "Inbox", h("small", { text: "Moodle-Nachrichten und was sich in deinen Kursen getan hat" }))),
    h("nav", { class: "tabs", "aria-label": "Inbox" }, [["#/nachrichten", "Nachrichten"], ["#/neu", "Neuigkeiten"]].map(([href, label]) =>
      h("a", { href, text: label, ...(href === current ? { "aria-current": "page" } : {}) }))));
}

async function newsPage(root) {
  inboxHead(root, "#/neu");
  const grid = h("div", { class: "grid" });
  root.append(grid);
  const tNew = tile("Geändert in den letzten 14 Tagen", { cls: "w8" });
  const tNote = tile("Benachrichtigungen", { i: 1 });
  grid.append(tNew, tNote);
  fill(tNew, () => api("moodle_whats_new", { days: 14 }), (r) => {
    const list = Array.isArray(r) ? r : r?.changes || [];
    if (!list.length) return emptyRow("In den letzten 14 Tagen hat sich nichts geändert.");
    const byCourse = new Map();
    for (const x of list) {
      const k = x.course || "Kurs";
      if (!byCourse.has(k)) byCourse.set(k, { id: x.courseid, items: [] });
      byCourse.get(k).items.push(x);
    }
    return [...byCourse].map(([course, g]) => h("div", { class: "news-group" },
      h("a", { class: "news-course", href: `#/kurs/${g.id}`, text: course }),
      h("ul", { class: "rows" }, g.items.map((x, j) => h("li", {}, h("a", { class: "row", href: `#/kurs/${g.id}`, vars: { "--j": j } },
        h("span", { class: "dot", "data-src": "moodle" }),
        h("span", { class: "t" }, x.activity, h("span", { class: "s", text: (x.changes || []).map((c) => typeof c === "string" ? c : c.what || c.type || "").filter(Boolean).join(" · ") })),
        h("span", { class: "chip", text: typeName[x.type] || x.type || "" })))))));
  });
  fill(tNote, () => api("moodle_notifications", { limit: 30 }), (r) => {
    const list = Array.isArray(r) ? r : r?.notifications || [];
    if (!list.length) return emptyRow("Keine Benachrichtigungen.");
    const unread = list.filter((n) => n.unread).length;
    return [unread > 0 && confirmFlow({ tool: "moodle_mark_notifications_read", args: {}, label: `${unread} als gelesen markieren`, confirm: "In Moodle als gelesen markieren", done: refreshPage }), h("ul", { class: "rows" }, list.map((n, j) => h("li", {}, h("div", { class: `row ${n.unread ? "unread" : ""}`, vars: { "--j": j } },
      h("span", { class: "dot", "data-src": "moodle" }),
      h("span", { class: "t" }, n.subject, h("span", { class: "s", text: n.time })),
      n.unread && h("span", { class: "chip moodle", text: "neu" })))))];
  });
}

async function messagesPage(root, id) {
  inboxHead(root, "#/nachrichten");
  const layout = h("div", { class: "chat" });
  root.append(layout);
  const side = tile("Unterhaltungen", { cls: "chat-list" });
  const pane = h("section", { class: "tile chat-pane" });
  layout.append(side, pane);
  fill(side, () => api("moodle_conversations", { limit: 30 }), (r) => {
    const list = Array.isArray(r) ? r : r?.conversations || [];
    if (!list.length) return emptyRow("Keine Unterhaltungen.");
    return h("ul", { class: "rows" }, list.map((c, j) => h("li", {}, h("a", { class: "row", href: `#/nachrichten/${c.conversationid}`, vars: { "--j": j }, ...(String(c.conversationid) === String(id) ? { "aria-current": "true" } : {}) },
      h("span", { class: "dot", "data-src": "moodle" }),
      h("span", { class: "t" }, c.name || "Unterhaltung", h("span", { class: "s preview sens", text: c.last_message })),
      h("span", { class: "s", text: (c.last_time || "").slice(5, 10).split("-").reverse().join(".") })))));
  });
  if (!id) {
    pane.append(h("p", { class: "empty", text: "Wähle links eine Unterhaltung." }));
    return;
  }
  pane.append(skeleton());
  try {
    const r = await api("moodle_conversation_messages", { conversationid: Number(id), limit: 50 });
    const msgs = Array.isArray(r) ? r : r?.messages || [];
    const thread = h("div", { class: "msgs" }, msgs.map((m, j) => h("div", { class: `msg ${m.mine || m.from === "ich" || m.from === "me" ? "mine" : ""}`, vars: { "--j": Math.min(j, 12) } },
      h("span", { class: "s", text: [m.from, m.time].filter(Boolean).join(" · ") }),
      h("p", { class: "sens", text: m.text }))));
    pane.replaceChildren(thread, composer({ tool: "moodle_send_message", field: "text", base: { conversationid: Number(id) }, placeholder: "Antworten …" }));
    thread.scrollTop = thread.scrollHeight;
  } catch (err) {
    pane.replaceChildren(errorBox(err));
  }
}

// composer writes through the preview → confirm gate: the first click only
// shows what would be sent; nothing leaves nak without the second, explicit one.
function composer({ tool, field, base, placeholder }) {
  const ta = h("textarea", { rows: 2, placeholder, "aria-label": placeholder });
  const out = h("div", { class: "preview-box", hidden: true });
  const form = h("form", { class: "composer" }, ta, h("button", { class: "primary", type: "submit", text: "Vorschau" }), out);
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const args = { ...base, [field]: ta.value.trim() };
    if (!args[field]) return;
    out.hidden = false;
    out.replaceChildren(h("p", { class: "empty", text: "Vorschau wird erstellt …" }));
    try {
      const pre = await post(tool, args);
      const send = h("button", { class: "primary", type: "button", text: "Verbindlich senden" });
      send.addEventListener("click", async () => {
        send.disabled = true;
        try {
          await post(tool, { ...args, confirm: true }, "", { "X-Nak-Confirm": "JA" });
          ta.value = "";
          out.replaceChildren(h("p", { class: "ok-note", text: "Gesendet." }));
          fresh = true;
          await render();
          fresh = false;
        } catch (err) {
          out.replaceChildren(errorBox(err));
        }
      });
      out.replaceChildren(h("p", { class: "empty", text: "Noch nichts gesendet. So würde es rausgehen:" }),
        h("pre", { text: typeof pre.result?.preview === "string" ? pre.result.preview : JSON.stringify(pre.result?.preview ?? pre.result, null, 2) }),
        h("div", { class: "row-actions" }, send, h("button", { class: "ghost", type: "button", text: "Abbrechen", onclick: () => { out.hidden = true; } })));
    } catch (err) {
      out.replaceChildren(errorBox(err));
    }
  });
  return form;
}

// ── modules: one unit per module, CIS and Moodle together ───────────────────

// Module numbers are upper case letters + 3 digits; matched case-sensitively
// so "TuI168_I23" (a tutorial) still yields I168 but "I23" does not count.
const nrAllRe = /[A-Z]{1,2}\d{3}/g;
const nrRe = /([A-Z]{1,2}\d{3})/;
const firstNr = (s) => (String(s || "").match(nrRe)?.[1] || "");
const nrsIn = (s) => [...new Set(String(s || "").match(nrAllRe) || [])];

const roman = { i: "1", ii: "2", iii: "3", iv: "4", v: "5", vi: "6", vii: "7", viii: "8", ix: "9", x: "10" };

// norm makes titles comparable: case, umlauts kept, "Mathematik1" → "mathematik 1",
// "II" → "2"; module numbers stay one word ("i168").
function norm(s) {
  const out = [];
  for (const raw of String(s || "").toLowerCase().split(/[^a-z0-9äöüß]+/)) {
    if (!raw) continue;
    if (/^[a-z]{1,2}\d{3}$/.test(raw)) { out.push(raw); continue; }
    for (const w of raw.replace(/([a-zäöüß])(\d)/g, "$1 $2").replace(/(\d)([a-zäöüß])/g, "$1 $2").split(" ")) out.push(roman[w] || w);
  }
  return out;
}

let unitsP = null;
let historyP = null;
// The app stays open for days (Android keeps the WebView, a notification only
// changes the hash): reuse these for a minute, after that ask the server cache.
let unitsAt = 0;
let historyAt = 0;
const memoAge = 60_000;

let pendingP = null;

// written exams still waiting for their grade, with the PVO deadline
function loadPending() {
  if (!pendingP || fresh) pendingP = fetch("/api/grades/pending", { credentials: "same-origin" }).then((r) => r.ok ? r.json() : { pending: [] }).catch(() => ({ pending: [] }));
  return pendingP;
}

function pendingRows(p, keep = () => true) {
  const list = (p?.pending || []).filter(keep);
  if (!list.length) return null;
  return [h("h2", { class: "sub", text: "Note ausstehend" }),
    h("ul", { class: "rows" }, list.map((x, j) => {
      const due = x.due_known ? parseDE(x.due) : null;
      const chip = !due ? h("span", { class: "chip", text: "offen" })
        : x.overdue ? h("span", { class: "chip due", text: "Frist vorbei" })
        : h("span", { class: `chip ${dayDiff(due) <= 7 ? "due" : ""}`, text: `bis ${fmtShort.format(due)}` });
      return h("li", {}, h("div", { class: "row", vars: { "--j": j } },
        h("span", { class: "dot", "data-src": "cis" }),
        h("span", { class: "t" }, x.title, h("span", { class: "s", text: x.due_known
          ? `geschrieben ${x.written.split(" ")[0]} · Note spätestens ${x.due}`
          : `geschrieben ${x.written.split(" ")[0]} · ${x.note || "Frist noch nicht berechenbar"}` })),
        chip));
    }))];
}

function loadHistory() {
  if (!historyP || fresh || Date.now() - historyAt > memoAge) historyAt = Date.now(), historyP = fetch("/api/history", { credentials: "same-origin" }).then((r) => r.json()).catch(() => ({ exams: [], grades: {} }));
  return historyP;
}

// unitMatch: does a CIS record (number list + title) belong to unit u?
function unitMatch(u, nrs, title) {
  const mine = [u.nr, ...(u.aliases || [])];
  if (nrsIn(nrs).some((n) => mine.includes(n))) return true;
  const a = ` ${norm(title).join(" ")} `, b = ` ${norm(u.title).join(" ")} `;
  return b.trim() !== "" && a.trim() !== "" && (a === b || a.includes(b));
}

// lecturers from every source; course names end in "… - Brzezinski/Ullmann",
// the CIS writes "Brzezinski, Patryk" — shown as "Patryk Brzezinski", and one
// person from several sources counts once (matched by surname).
function lecturersOf(u, m, hist) {
  const now = new Date();
  const people = new Map(); // surname → { name, current }
  const add = (raw, current) => {
    let name = String(raw || "").trim().replace(/\s+/g, " ");
    if (name.length < 3 || /^(I\d|Kohorte|Tutorium|Q\d)/i.test(name)) return;
    if (name.includes(",")) { const [last, first] = name.split(",").map((x) => x.trim()); name = first ? `${first} ${last}` : last; }
    name = name.replace(/^(Prof\.|Dr\.|Dipl\.-\S+)\s+/g, "");
    const key = name.split(" ").pop().toLowerCase();
    const prev = people.get(key);
    // the longer form wins ("Patryk Brzezinski" over "Brzezinski")
    if (!prev) people.set(key, { name, current });
    else { if (name.length > prev.name.length) prev.name = name; prev.current ||= current; }
  };
  const many = (s, current) => String(s || "").split(/\s*(?:\/|;| und )\s*/).forEach((x) => add(x, current));
  for (const e of m?.next_sessions || []) many(e.lecturer, true);
  for (const c of u?.courses || []) {
    const parts = String(c.name).split(" - ");
    const last = parts.length > 2 ? parts[parts.length - 1] : "";
    if (last && !/\d{2}/.test(last)) many(last, c.state === "current");
  }
  // an exam exists once per group with its own examiners: only the one you
  // are (or were) registered for says who you have
  for (const e of [...(u?.exams || []), ...(hist?.exams || []).filter((x) => unitMatch(u, x.module_nr, x.title))]) {
    if (!e.registered) continue;
    const at = parseDE(e.start);
    for (const d of e.dozenten || []) add(d, !!at && at >= startOfDay(now));
  }
  for (const d of m?.distribution?.lecturers || []) add(d, false);
  const all = [...people.values()];
  return { cur: all.filter((p) => p.current).map((p) => p.name), past: all.filter((p) => !p.current).map((p) => p.name) };
}

function loadUnits() {
  if (unitsP && !fresh && Date.now() - unitsAt < memoAge) return unitsP;
  unitsAt = Date.now();
  unitsP = (async () => {
    const [g, p, c, x] = await Promise.allSettled([api("cis_grades"), api("cis_progress"), api("moodle_courses", { classification: "all" }), api("cis_list_klausuren")]);
    const map = new Map();
    const add = (nr, title) => {
      if (!nr) return null;
      let u = map.get(nr);
      if (!u) map.set(nr, (u = { nr, title: "", grade: null, plan: null, courses: [], exams: [], aliases: [] }));
      u.title ||= title;
      return u;
    };
    for (const sem of p.value?.semesters || []) for (const m of sem.modules || []) { const u = add(firstNr(m.module_nr), m.title); if (u) u.plan = m; }
    for (const m of g.value?.overview?.modules || []) { const u = add(firstNr(m.module_nr), m.title); if (u) u.grade = m; }
    const units = [...map.values()];
    const titled = units.map((u) => ({ u, t: ` ${norm(u.title).join(" ")} ` })).filter((x) => x.t.trim()).sort((a, b) => b.t.length - a.t.length);
    const byTitle = (s) => { const n = ` ${norm(s).join(" ")} `; return titled.find((x) => n.includes(x.t))?.u; };
    // Exams can run under other numbers than the grade (new exam regulations:
    // "A222,I222" for the module graded as I168) — match by number, then by
    // title, and keep the extra numbers as aliases of the same unit.
    const alias = new Map();
    for (const e of x.value || []) {
      const nrs = nrsIn(e.module_nr);
      const u = nrs.map((nr) => map.get(nr)).find(Boolean) || byTitle(e.title);
      if (!u) continue;
      u.exams.push(e);
      for (const nr of nrs) if (!map.has(nr)) { alias.set(nr, u); if (!u.aliases.includes(nr)) u.aliases.push(nr); }
    }
    const orphans = [];
    for (const course of c.value || []) {
      const label = `${course.name} ${course.shortname}`;
      let u = nrsIn(label).map((nr) => map.get(nr)).find(Boolean);
      u ||= nrsIn(label).map((nr) => alias.get(nr)).find(Boolean) || byTitle(courseTitle(course));
      if (u) u.courses.push(course);
      else orphans.push(course);
    }
    const rank = (c) => (c.state === "current" ? 0 : c.state === "future" ? 1 : 2) + (/tutori/i.test(c.name) ? 0.5 : 0);
    for (const u of units) u.courses.sort((a, b) => rank(a) - rank(b));
    const byNr = { get: (nr) => map.get(nr) || alias.get(nr) };
    return { units, orphans, byNr, unitOf: (s) => nrsIn(s).map((nr) => byNr.get(nr)).find(Boolean) || byTitle(s), unitOfCourse: (id) => units.find((u) => u.courses.some((x) => String(x.id) === String(id))) };
  })();
  return unitsP;
}

function matches(q, words) {
  const set = words;
  return norm(q).every((t) => /^\d+$/.test(t) ? set.includes(t) : set.some((w) => w.startsWith(t)));
}

function unitLabel(u) {
  if (u.grade?.grade) return `Note ${u.grade.grade}`;
  if (u.courses.some((c) => c.state === "current")) return "aktuell";
  return u.nr;
}

async function unitsPage(root) {
  root.append(h("div", { class: "page-head" }, h("h1", {}, "Module", h("small", { text: "CIS und Moodle zusammen" }))));
  const box = h("div", { class: "mods" }, [0, 1, 2].map((i) => tile("", { i }, skeleton())));
  root.append(box);
  try {
    const { units } = await loadUnits();
    const list = [...units].sort((a, b) => (Number(b.courses.some((c) => c.state === "current")) - Number(a.courses.some((c) => c.state === "current"))) || a.nr.localeCompare(b.nr));
    box.replaceChildren(...list.map((u, i) => unitCard(u, i)));
  } catch (err) {
    box.replaceChildren(errorBox(err));
  }
}

function unitCard(u, i) {
  const t = tile("", { i: Math.min(i, 12) },
    h("a", { class: "mod", href: `#/modul/${u.nr}` },
      h("span", { class: "nr", text: u.nr }),
      h("span", { class: "name", text: u.title || u.nr }),
      h("span", { class: "tags" },
        u.courses.some((c) => c.state === "current") && h("span", { class: "chip moodle", text: "läuft" }),
        u.grade?.grade ? h("span", { class: `chip sens ${gradeClass(u.grade)}`, text: u.grade.grade }) : u.grade?.status && h("span", { class: `chip sens ${gradeClass(u.grade)}`, text: u.grade.status }),
        u.courses.length > 0 && h("span", { class: "chip", text: `${u.courses.length} Moodle-Kurs${u.courses.length > 1 ? "e" : ""}` }))));
  t.querySelector(".tile-head").remove();
  return t;
}

// The unified module page: CIS facts and every linked Moodle course.
async function unitPage(root, nr, tab = "", courseArg = "") {
  root.append(h("a", { class: "back", href: "#", onclick: (e) => { e.preventDefault(); history.back(); } }, svg(icons.back), "zurück"));
  const title = h("h1", { text: nr });
  const head = h("div", { class: "page-head" }, title);
  root.append(head);
  const tabsBox = h("div");
  const body = h("div", { class: "course-body" }, skeleton());
  root.append(tabsBox, body);
  let u;
  try {
    u = (await loadUnits()).byNr.get(nr);
  } catch (err) {
    body.replaceChildren(errorBox(err));
    return;
  }
  const courses = u?.courses || [];
  const cid = courseArg ? Number(courseArg) : courses[0]?.id;
  if (u && u.nr !== nr) { location.replace(`#/modul/${u.nr}${tab ? "/" + tab : ""}${courseArg ? "/" + courseArg : ""}`); return; }
  title.replaceChildren(u?.title || nr, h("small", { text: [[nr, ...(u?.aliases || [])].join(", "), u?.plan?.credits && `${u.plan.credits} Credits`, u?.grade?.status].filter(Boolean).join(" · ") }));
  const base = `#/modul/${nr}`;
  const link = (t, c = cid) => `${base}${t ? "/" + t : ""}${c && c !== courses[0]?.id ? "/" + c : t && c ? "/" + c : ""}`;
  tabsBox.append(h("nav", { class: "tabs", "aria-label": "Bereiche" },
    [["", "Überblick"], ...(courses.length ? [["inhalt", "Inhalt"], ["forum", "Forum"], ["abgaben", "Abgaben & Tests"]] : [])].map(([k, label]) =>
      h("a", { href: link(k), text: label, ...(k === tab ? { "aria-current": "page" } : {}) }))));
  if (tab && courses.length > 1) {
    tabsBox.append(h("div", { class: "course-switch", role: "group", "aria-label": "Moodle-Kurs" }, courses.map((c) =>
      h("a", { href: link(tab, c.id), class: String(c.id) === String(cid) ? "on" : "", text: `${courseTitle(c)}${c.state === "current" ? "" : ` · ${c.state === "past" ? "vergangen" : c.state}`}` }))));
  }
  if (tab) return courseTab(body, cid, tab);

  // overview
  const grid = h("div", { class: "grid" });
  body.replaceChildren(grid);
  const tGrade = tile("Note & Prüfung", { cls: "half", i: 0 });
  const tPlan = tile("Studienplan", { cls: "half", i: 1 });
  const tNext = tile("Nächste Termine", { cls: "half", i: 2 });
  const tMoodle = tile("Moodle", { cls: "w6", i: 3 });
  const tNews = tile("Neu in Moodle", { cls: "w6", i: 4 });
  const tEv = tile("Altklausuren · EduVault", { cls: "w12", i: 5 });
  grid.append(tGrade, tPlan, tNext, tMoodle, tNews, tEv);
  // Transferleistungen written on this module's topic (only shown if any)
  api("cis_list_transfer").then((list) => {
    const mine = [u?.nr, ...(u?.aliases || []), nr].filter(Boolean);
    const here = (list || []).filter((x) => mine.includes(tlModuleNr(x)));
    if (!here.length) return;
    tEv.before(tile("Transferleistungen zu diesem Modul", { cls: "w12", i: 5, link: ["Alle", "#/transferleistungen"] },
      h("ul", { class: "rows" }, here.map((x, j) => tlRow({ no: Number(x.no), tl: x }, j)))));
  }).catch(() => {});
  fill(tEv, () => api("eduvault_module_exams", { title: u?.title || nr }), evRows).then(() => {
    const err = tEv.querySelector(".err");
    if (err && /eingerichtet|Einstellungen/.test(err.textContent)) err.replaceWith(h("p", { class: "empty" }, "Hinterlege in den ", h("a", { href: "#/einstellungen", text: "Einstellungen" }), " deinen EduVault-Zugang, dann stehen hier die Altklausuren zu diesem Modul."));
  });
  const data = api("nak_module", { module_nr: nr });
  const lect = h("p", { class: "lecturers" });
  head.append(lect);
  Promise.all([data.catch(() => ({})), loadHistory()]).then(([m, hist]) => {
    const { cur, past } = lecturersOf(u, m, hist);
    lect.replaceChildren(...[
      cur.length > 0 && h("span", {}, h("b", { text: "Lehrende " }), cur.join(", ")),
      past.length > 0 && h("span", {}, h("b", { text: cur.length ? "früher " : "Lehrende " }), past.join(", ")),
    ].filter(Boolean));
  });
  fill(tGrade, () => data, (m) => gradeTile(m, u));
  const examsOf = (m) => dedupeExams([...(m.exams || []), ...(u?.exams || [])]);
  fill(tPlan, () => data, (m) => planTile({ ...m, exams: [] }));
  const tExams = tile("Prüfungsverlauf", { cls: "w12", i: 2 });
  grid.insertBefore(tExams, tNext.nextSibling);
  fill(tExams, () => Promise.all([data, loadHistory(), loadPending()]), ([m, hist, p]) => [examTimeline(u, m, hist, examsOf(m)),
    ...(pendingRows(p, (x) => unitMatch(u, x.module_nr.split(","), x.title)) || [])]);
  fill(tNext, () => data, (m) => {
    const now = new Date();
    const exams = examsOf(m).filter((e) => e.registered && parseDE(e.start) >= startOfDay(now))
      .map((e) => ({ date: fmtShort.format(parseDE(e.start)), start: e.start.slice(11, 16), end: (e.ende || "").slice(11, 16), kind: "Klausur", room: "", lecturer: "", at: parseDE(e.start) }));
    const evs = [...exams, ...(m.next_sessions || []).map((e) => ({ ...e, at: parseDE(`${e.date} ${e.start}`) }))].sort((a, b) => (a.at || 0) - (b.at || 0));
    if (!evs.length) return emptyRow("Keine Termine in den nächsten 60 Tagen.");
    return h("ul", { class: "rows" }, evs.map((e, j) => h("li", {}, h("div", { class: "row", vars: { "--j": j } },
      h("span", { class: "dot", "data-src": "cis" }),
      h("span", { class: "t" }, `${e.at ? fmtShort.format(e.at) : e.date} · ${e.start}${e.end && e.end !== e.start ? `–${e.end}` : ""}`, h("span", { class: "s", text: [e.room, e.lecturer].filter(Boolean).join(" · ") })),
      h("span", { class: `chip ${e.kind === "Klausur" ? "due" : "cis"}`, text: e.kind || "CIS" })))));
  });
  fill(tMoodle, () => Promise.all([data, courses.length ? api("moodle_assignments", { only_open: true }) : []]), ([m, open]) => {
    if (!courses.length) return emptyRow("Kein Moodle-Kurs zu diesem Modul.");
    const ids = new Set(courses.map((c) => c.id));
    const mine = (open || []).filter((a) => ids.has(a.courseid));
    return [h("ul", { class: "rows" }, courses.map((c, j) => h("li", {}, h("a", { class: "row", href: link("inhalt", c.id), vars: { "--j": j } },
      h("span", { class: "dot", "data-src": "moodle" }),
      h("span", { class: "t" }, courseTitle(c), h("span", { class: "s", text: [c.state === "current" ? "aktuell" : c.state === "past" ? "vergangen" : c.state, c.progress && `Fortschritt ${c.progress}`].filter(Boolean).join(" · ") })),
      h("span", { class: "chip moodle", text: "Inhalt" }))))),
    mine.length > 0 && h("div", { class: "after" }, h("h2", { text: "Offene Abgaben" }), assignmentRows(mine))];
  });
  fill(tNews, () => courses.length ? api("moodle_whats_new", { days: 30, courseids: courses.map((c) => c.id) }) : Promise.resolve([]), (r) => {
    const list = Array.isArray(r) ? r : r?.changes || [];
    if (!list.length) return emptyRow(courses.length ? "In den letzten 30 Tagen nichts Neues." : "Kein Moodle-Kurs zu diesem Modul.");
    return h("ul", { class: "rows" }, list.slice(0, 8).map((x, j) => h("li", {}, h("a", { class: "row", href: link("inhalt", x.courseid), vars: { "--j": j } },
      h("span", { class: "dot", "data-src": "moodle" }),
      h("span", { class: "t" }, x.activity, h("span", { class: "s", text: [x.course, ...(x.changes || []).map((c) => typeof c === "string" ? c : c.what || c.type || "")].filter(Boolean).join(" · ") })),
      h("span", { class: "chip", text: typeName[x.type] || x.type || "" })))));
  });
}

function gradeTile(m, u) {
  const g = m.grade || u?.grade;
  const d = m.distribution;
  if (!g) return emptyRow("Noch keine Leistung eingetragen.");
  const out = [h("div", { class: "avg" }, g.grade_value ? decimal(g.grade_value, 1) : h("span", { class: "big", text: "–" }), h("span", { class: `chip sens ${gradeClass(g)}`, text: g.status || "" }))];
  out.push(h("dl", { class: "kv meter" },
    h("dt", { text: "Prüfung" }), h("dd", { text: g.exam_date || "–" }),
    h("dt", { text: "Versuch" }), h("dd", { text: g.attempt || "–" }),
    g.source === "notenuebersicht" && [h("dt", { text: "Quelle" }), h("dd", { text: `Notenübersicht vom ${g.entry_date || "–"}, noch nicht in der Leistungsübersicht` })],
    d?.count > 0 && [h("dt", { text: "Jahrgang" }), h("dd", { text: `Schnitt ${String(d.average).replace(".", ",")} · ${d.count} Ergebnisse${d.percentile ? ` · besser als ${Math.round(d.percentile)} %` : ""}` })]));
  if (d?.buckets?.length) {
    const max = Math.max(...d.buckets.map((b) => b.count), 1);
    out.push(h("div", { class: "dist sens", role: "img", "aria-label": "Notenverteilung" }, d.buckets.map((b, j) =>
      h("span", { class: `col ${b.grade === g.grade ? "mine" : ""}`, title: `${b.grade}: ${b.count}`, vars: { "--h": b.count / max, "--j": j } }, h("i"), h("small", { text: b.grade })))));
  }
  return out;
}

// One exam often exists once per group (Zenturie) with its own id; show it
// once, preferring the entry you are registered for.
function dedupeExams(list) {
  const out = new Map();
  for (const e of list) {
    const k = `${e.start}|${norm(e.title).join(" ")}`;
    const prev = out.get(k);
    if (!prev || (e.registered && !prev.registered)) out.set(k, e);
  }
  return [...out.values()].sort((a, b) => (parseDE(a.start) || 0) - (parseDE(b.start) || 0));
}

// Every exam date of the module: what the CIS lists now, what naknak saw
// earlier, and the attempts recorded on the grades page — with the result.
function examTimeline(u, m, hist, current) {
  const byKey = new Map();
  const put = (x) => {
    const at = parseDE(x.start || x.exam_date);
    if (!at) return;
    const k = isoDate(at);
    const prev = byKey.get(k) || { at, registered: false, title: "", dozenten: [], result: null, attempt: 0, upcoming: at >= startOfDay(new Date()) };
    prev.title ||= x.title || "";
    if (x.start && /\d{1,2}:\d{2}/.test(x.start)) prev.at = at;
    // same date, other groups: keep the examiners of the entry you are registered for
    if (x.dozenten?.length && (x.registered || (!prev.registered && !prev.dozenten.length))) prev.dozenten = x.dozenten;
    prev.registered ||= !!x.registered;
    if (x.grade) { prev.result = x; prev.attempt = x.attempt || prev.attempt; }
    // the CIS offers one action per exam (register before, deregister until the deadline)
    if (x.exam_id && "action" in x) {
      // your registered entry decides; otherwise every group's offer is listed
      // separately (with group and examiner) — never pick a group for you
      const offer = { action: x.action, label: x.action_label, examId: x.exam_id, deadlines: x.deadlines, group: (x.zenturien || []).join(", "), examiner: (x.dozenten || []).map((d) => d.includes(",") ? d.split(",").map((y) => y.trim()).reverse().join(" ") : d).join(", ") };
      prev.offers ||= [];
      if (x.registered) { prev.mine = true; prev.offers = x.action ? [offer] : []; prev.deadlines = x.deadlines; }
      else if (!prev.mine && x.action) { prev.offers.push(offer); prev.deadlines ||= x.deadlines; }
    }
    byKey.set(k, prev);
  };
  for (const e of current) put(e);
  for (const e of (hist?.exams || []).filter((x) => unitMatch(u, x.module_nr, x.title))) put(e);
  const steps = [...(hist?.grades?.[u?.nr] || [])];
  const g = m.grade || u?.grade;
  if (g?.exam_date && !steps.some((x) => x.exam_date === g.exam_date && x.grade === g.grade)) steps.push(g);
  for (const st of steps) put(st);
  const rows = [...byKey.values()].sort((a, b) => b.at - a.at);
  // a grade from the PDF has no exam date: it belongs to the last exam taken
  const last = g?.source === "notenuebersicht" && !g.exam_date && rows.find((r) => !r.upcoming);
  if (last && !last.result) last.result = g;
  if (!rows.length) return emptyRow("Noch keine Prüfungstermine bekannt. naknak merkt sich ab jetzt jeden Termin, den das CIS anzeigt.");
  return h("ol", { class: "timeline" }, rows.map((r, j) => {
    const res = r.result;
    const cls = res ? gradeClass(res) : "";
    return h("li", { class: `tl-item ${r.upcoming ? "upcoming" : "past"} ${cls}`, vars: { "--j": j } },
      h("span", { class: "tl-dot" }),
      h("div", { class: "tl-body" },
        h("div", { class: "tl-head" },
          h("b", { text: `${fmtDay.format(r.at)}${/00:00/.test(fmtTime.format(r.at)) ? "" : ` · ${fmtTime.format(r.at)}`}` }),
          r.upcoming && h("span", { class: `chip ${r.registered ? "ok" : "due"}`, text: r.registered ? (dayDiff(r.at) === 0 ? "heute · angemeldet" : `angemeldet · ${relDay(dayDiff(r.at))}`) : "nicht angemeldet" }),
          !r.upcoming && res && h("span", { class: `chip ${cls}`, text: res.grade }),
          !r.upcoming && !res && h("span", { class: "chip", text: r.registered ? "Ergebnis steht aus" : "vergangen" })),
        h("span", { class: "s", text: [r.title, r.attempt ? `${r.attempt}. Versuch` : "", r.dozenten.map((d) => d.includes(",") ? d.split(",").map((x) => x.trim()).reverse().join(" ") : d).join(", ")].filter(Boolean).join(" · ") }),
        r.upcoming && r.deadlines && h("span", { class: "s", text: [r.deadlines.register_closes && `Anmeldung bis ${r.deadlines.register_closes}`, r.deadlines.deregister_until && `Abmeldung bis ${r.deadlines.deregister_until}`].filter(Boolean).join(" · ") }),
        r.upcoming && (() => {
          // the CIS sometimes still offers an action after its deadline
          const open = (o) => {
            const until = parseDE(o.action === "register" ? o.deadlines?.register_closes : o.deadlines?.deregister_until);
            return !until || until > new Date();
          };
          const late = (r.offers || []).filter((o) => !open(o));
          return late.length > 0 && !(r.offers || []).some(open) && h("span", { class: "chip", text: late[0].action === "register" ? "Anmeldefrist vorbei" : "Abmeldefrist vorbei" });
        })(),
        r.upcoming && (r.offers || []).filter((o) => (o.action === "register" || o.action === "deregister") && (() => { const until = parseDE(o.action === "register" ? o.deadlines?.register_closes : o.deadlines?.deregister_until); return !until || until > new Date(); })()).map((o) => confirmFlow({
          tool: "cis_klausur_action", args: { exam_id: o.examId, action: o.action },
          label: [o.label || (o.action === "register" ? "Anmelden" : "Abmelden"), (r.offers.length > 1 || !r.mine) && o.group, (r.offers.length > 1 || !r.mine) && o.examiner].filter(Boolean).join(" · "),
          confirm: o.action === "register" ? `Verbindlich anmelden${o.group ? ` (${o.group})` : ""}` : "Verbindlich abmelden", done: refreshPage }))));
  }));
}

function planTile(m) {
  const sp = m.studienplan;
  const ex = m.exams || [];
  if (!sp && !ex.length) return emptyRow("Nicht im Studienplan.");
  return [sp && h("dl", { class: "kv" },
    h("dt", { text: "Credits" }), h("dd", { text: sp.credits }),
    h("dt", { text: "Prüfungsform" }), h("dd", { text: sp.exam_form_name || sp.exam_form || "–" }),
    sp.exam_semester && [h("dt", { text: "Prüfungssemester" }), h("dd", { text: sp.exam_semester })]),
    ex.length > 0 && h("ul", { class: "rows meter" }, ex.map((e, j) => h("li", {}, h("div", { class: "row", vars: { "--j": j } },
      h("span", { class: "dot", "data-src": "cis" }),
      h("span", { class: "t" }, e.title || "Prüfung", h("span", { class: "s", text: [e.start, e.deadlines?.register_closes && `Anmeldung bis ${e.deadlines.register_closes}`].filter(Boolean).join(" · ") })),
      h("span", { class: `chip ${e.registered ? "ok" : ""}`, text: e.registered ? "angemeldet" : (e.action_label || "offen") })))))];
}

// #/kurs/ID: a course that belongs to a module opens inside the module page.
async function coursePage(root, id, tab = "inhalt") {
  try {
    const u = (await loadUnits()).unitOfCourse(id);
    if (u) { location.replace(`#/modul/${u.nr}/${tab || "inhalt"}/${id}`); return; }
  } catch {}
  root.append(h("a", { class: "back", href: "#/kurse" }, svg(icons.back), "Kurse"));
  const title = h("h1", { text: "Kurs" });
  const tabs = h("nav", { class: "tabs", "aria-label": "Kursbereiche" },
    [["inhalt", "Inhalt"], ["forum", "Forum"], ["abgaben", "Abgaben & Tests"], ["info", "Info"]].map(([k, label]) =>
      h("a", { href: `#/kurs/${id}${k === "inhalt" ? "" : "/" + k}`, text: label, ...(k === tab ? { "aria-current": "page" } : {}) })));
  root.append(h("div", { class: "page-head" }, title), tabs);
  const body = h("div", { class: "course-body" }, skeleton());
  root.append(body);
  api("moodle_courses", { classification: "all" }).then((cs) => {
    const c = cs.find((x) => String(x.id) === String(id));
    if (c) title.replaceChildren(courseTitle(c), h("small", { text: [c.shortname, c.progress && `Fortschritt ${c.progress}`].filter(Boolean).join(" · ") }));
  }).catch(() => {});
  return courseTab(body, Number(id), tab);
}

async function courseTab(body, cid, tab) {
  try {
    if (tab === "inhalt") {
      const [secs, assigns] = await Promise.all([api("moodle_course_contents", { courseid: cid }), api("moodle_assignments", { courseid: cid, only_open: false }).catch(() => [])]);
      const byCm = Object.fromEntries((assigns || []).map((a) => [a.cmid, a.assignid]));
      const visible = (secs || []).filter((s) => (s.modules || []).length || s.summary);
      if (!visible.length) return body.replaceChildren(emptyRow("Dieser Kurs hat keine sichtbaren Inhalte."));
      body.replaceChildren(...visible.map((s, i) => h("details", { class: "section tile", open: i < 3 || visible.length <= 4, vars: { "--i": Math.min(i, 8) } },
        h("summary", {}, h("span", { class: "t", text: s.name || `Abschnitt ${s.section}` }), h("span", { class: "s", text: `${(s.modules || []).length} Einträge` })),
        textBlock(s.summary),
        h("ul", { class: "items" }, (s.modules || []).map((m, j) => moduleItem(m, cid, byCm, j))))));
    } else if (tab === "forum") {
      const ds = await api("moodle_forum_discussions", { courseid: cid, limit: 15 });
      const list = Array.isArray(ds) ? ds : ds?.discussions || [];
      if (!list.length) return body.replaceChildren(emptyRow("Keine Forendiskussionen."));
      body.replaceChildren(tile("Diskussionen", { cls: "w12" }, h("ul", { class: "rows" }, list.map((d, j) => h("li", {}, h("a", { class: "row", href: `#/diskussion/${d.discussionid}`, vars: { "--j": j } },
        h("span", { class: "dot", "data-src": "moodle" }),
        h("span", { class: "t" }, d.subject, h("span", { class: "s", text: [d.forum, d.author, d.last_activity].filter(Boolean).join(" · ") }), d.preview && h("span", { class: "s preview", text: d.preview })),
        h("span", { class: "chip", text: `${d.replies || 0} Antw.` })))))));
    } else if (tab === "abgaben") {
      const [as, qs, cs] = await Promise.all([api("moodle_assignments", { courseid: cid, only_open: false }), api("moodle_quizzes", { courseid: cid, only_open: false }).catch(() => []), api("moodle_choices", { courseid: cid }).catch(() => [])]);
      const choices = Array.isArray(cs) ? cs : cs?.choices || [];
      body.replaceChildren(h("div", { class: "grid" },
        tile("Abgaben", { cls: "w6" }, assignmentRows(as || [])),
        tile("Tests", { cls: "w6", i: 1 }, quizRows(qs || [])),
        choices.length > 0 && tile("Abstimmungen & Gruppenwahlen", { cls: "w12", i: 2 }, choices.map(choiceView))));
    } else {
      const info = await api("moodle_course_info", { courseid: cid });
      body.replaceChildren(tile("Kurs", { cls: "w12" },
        h("dl", { class: "kv" }, Object.entries(info).filter(([k, v]) => typeof v !== "object" && !["url", "summary", "id"].includes(k) && v !== "").map(([k, v]) => [h("dt", { text: k }), h("dd", { text: v })])),
        textBlock(info.summary)));
    }
  } catch (err) {
    body.replaceChildren(errorBox(err));
  }
}

// ── settings ────────────────────────────────────────────────────────────────

// listEditor: choose entries and their order (drag the grip, or arrow keys
// on it); used for the navigation bar and the dashboard
function listEditor({ title, intro, defs, current, items, min, max, endpoint, key, applied, i = 0 }) {
  let order = [...current];
  const all = items.filter((id) => defs[id]);
  const list = h("ol", { class: "nav-edit" });
  const msg = h("div", { class: "form-msg", "aria-live": "polite" });
  const draw = () => {
    const off = all.filter((id) => !order.includes(id));
    list.replaceChildren(...[...order, ...off].map((id) => {
      const on = order.includes(id);
      const i = order.indexOf(id);
      const move = (d) => { [order[i], order[i + d]] = [order[i + d], order[i]]; draw(); };
      const box = h("input", { type: "checkbox", "aria-label": `${defs[id][0]} in der Leiste`, ...(on ? { checked: true } : {}) });
      box.addEventListener("change", () => {
        if (box.checked && order.length >= max) { box.checked = false; msg.replaceChildren(h("p", { class: "empty", text: `Höchstens ${max} Einträge.` })); return; }
        if (!box.checked && order.length <= min) { box.checked = true; msg.replaceChildren(h("p", { class: "empty", text: `Mindestens ${min} ${min === 1 ? "Eintrag" : "Einträge"}.` })); return; }
        order = box.checked ? [...order, id] : order.filter((x) => x !== id);
        msg.replaceChildren();
        draw();
      });
      const grip = on && h("button", { class: "grip", type: "button", "aria-label": `${defs[id][0]} verschieben (Pfeiltasten)` }, svg(["M9 6h.01", "M15 6h.01", "M9 12h.01", "M15 12h.01", "M9 18h.01", "M15 18h.01"]));
      if (grip) {
        grip.addEventListener("pointerdown", (e) => dragRow(e, grip, i));
        grip.addEventListener("keydown", (e) => {
          const d = e.key === "ArrowUp" ? -1 : e.key === "ArrowDown" ? 1 : 0;
          if (!d || !order[i + d]) return;
          e.preventDefault();
          reorder(i, i + d);
          list.querySelectorAll(".grip")[i + d]?.focus();
        });
      }
      return h("li", { class: on ? "on" : "off" }, h("label", {}, box, svg(defs[id][3]), h("span", { text: defs[id][0] })), grip);
    }));
  };
  const reorder = (from, to) => {
    const [id] = order.splice(from, 1);
    order.splice(to, 0, id);
    draw();
  };
  // touch-friendly drag: the row follows the finger, the others make room
  const dragRow = (e, grip, from) => {
    e.preventDefault();
    const rows = [...list.querySelectorAll("li.on")];
    const li = rows[from];
    const rects = rows.map((r) => r.getBoundingClientRect());
    const step = rows.length > 1 ? Math.abs(rects[1].top - rects[0].top) : rects[0].height;
    const y0 = e.clientY;
    let to = from;
    grip.setPointerCapture(e.pointerId);
    window.NaknakApp?.holdGesture?.(true); // the Android app's pull-to-refresh
    li.classList.add("dragging");
    const move = (ev) => {
      const dy = Math.max(rects[0].top - rects[from].top, Math.min(rects[rows.length - 1].top - rects[from].top, ev.clientY - y0));
      li.style.translate = `0 ${dy}px`;
      const center = rects[from].top + rects[from].height / 2 + dy;
      to = rows.filter((r, k) => k !== from && rects[k].top + rects[k].height / 2 < center).length;
      rows.forEach((r, k) => {
        if (k === from) return;
        const shift = from < to && k > from && k <= to ? -step : to < from && k >= to && k < from ? step : 0;
        r.style.translate = shift ? `0 ${shift}px` : "";
      });
    };
    const end = () => {
      grip.removeEventListener("pointermove", move);
      grip.removeEventListener("pointerup", end);
      grip.removeEventListener("pointercancel", end);
      window.NaknakApp?.holdGesture?.(false);
      rows.forEach((r) => { r.style.translate = ""; });
      li.classList.remove("dragging");
      if (to !== from) reorder(from, to);
    };
    grip.addEventListener("pointermove", move);
    grip.addEventListener("pointerup", end);
    grip.addEventListener("pointercancel", end);
  };
  const save = async (nav) => {
    try {
      const res = await fetch(endpoint, { method: "PUT", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ [key]: nav }) });
      const body = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`);
      order = [...body[key]];
      applied(body[key]);
      draw();
      msg.replaceChildren(h("p", { class: "ok-note", text: "Gespeichert. Gilt in der App und im Browser." }));
    } catch (err) {
      msg.replaceChildren(errorBox(err));
    }
  };
  draw();
  return tile(title, { cls: "w6", i },
    h("p", { class: "empty", text: intro }),
    list,
    h("div", { class: "row-actions" },
      h("button", { class: "primary", type: "button", text: "Speichern", onclick: () => save(order) }),
      h("button", { class: "ghost", type: "button", text: "Standard", onclick: () => save([]) })),
    msg);
}

function navSettings(st) {
  return listEditor({ title: "Navigationsleiste", intro: "2 bis 7 Einträge, am Griff ziehen zum Sortieren.",
    defs: navDefs, current: st.nav || defaultNav, items: st.nav_items || Object.keys(navDefs), min: 2, max: 7,
    endpoint: "/api/settings/nav", key: "nav", applied: applyNav });
}

function homeSettings(st) {
  return listEditor({ title: "Startseite", intro: "Am Griff ziehen zum Sortieren.",
    defs: homeDefs, current: st.home || defaultHome, items: st.home_items || Object.keys(homeDefs), min: 1, max: Object.keys(homeDefs).length,
    endpoint: "/api/settings/home", key: "home", applied: applyHome, i: 1 });
}

const settingsTabs = [["darstellung", "Aussehen"], ["benachrichtigungen", "Mitteilungen"], ["verbindungen", "Dienste"], ["konto", "Konto"]];

async function settingsPage(root, tab = "darstellung") {
  root.append(h("div", { class: "page-head" }, h("h1", {}, "Einstellungen", h("small", { text: "Gilt für diese naknak-Instanz" }))));
  root.append(h("nav", { class: "tabs", "aria-label": "Bereiche" }, settingsTabs.map(([k, label]) => h("a", { href: `#/einstellungen/${k}`, text: label, ...(k === tab ? { "aria-current": "page" } : {}) }))));
  const grid = h("div", { class: "grid" });
  root.append(grid);
  let st;
  try {
    st = await (await fetch("/api/settings", { credentials: "same-origin" })).json();
  } catch (err) {
    grid.append(errorBox(err));
    return;
  }

  // only inside the Android app: lock, notifications and server live there
  let tApp = null;
  if (window.NaknakApp) {
    tApp = (tile("naknak-App", { cls: "w12", i: 0 },
      h("p", { class: "empty", text: `Version ${window.NaknakApp.version()}. App-Sperre, Benachrichtigungen und Server stellst du in der App selbst ein.` }),
      h("div", { class: "row-actions" }, h("button", { class: "primary", type: "button", text: "App-Einstellungen", onclick: () => window.NaknakApp.openSettings() }))));
  }

  // EduVault
  const ev = st.eduvault || {};
  const tEv = tile("EduVault · Altklausuren", { cls: "w8", i: 0 });
  const status = h("p", { class: "ev-status" }, ev.configured
    ? [h("span", { class: "chip ok", text: "verbunden" }), ` ${ev.token_hint || ""} · ${ev.url}`, ev.source === "env" ? " (aus Umgebungsvariablen)" : ""]
    : [h("span", { class: "chip", text: "nicht eingerichtet" }), " Mit Zugangsdaten zeigt naknak auf jeder Modulseite die passenden Altklausuren."]);
  const url = h("input", { name: "url", type: "url", value: ev.url || "https://eduvault4.de", autocomplete: "off", spellcheck: "false" });
  const token = h("input", { name: "token", autocomplete: "off", spellcheck: "false", placeholder: "evm_…" });
  const secret = h("input", { name: "secret", type: "password", autocomplete: "off", spellcheck: "false", placeholder: "Base32, z.B. JBSW…" });
  const msg = h("div", { class: "form-msg", "aria-live": "polite" });
  const save = h("button", { class: "primary", type: "submit" }, h("span", { class: "label", text: "Prüfen & speichern" }), h("span", { class: "spin", "aria-hidden": "true" }));
  const form = h("form", { class: "settings-form" },
    h("label", {}, "Adresse", url),
    h("label", {}, "Token", token),
    h("label", {}, "TOTP-Secret", secret),
    h("p", { class: "empty", text: "In EduVault unter Profil → MCP / KI-Zugang → Zugangsdaten erstellen." }),
    h("div", { class: "row-actions" }, save,
      ev.configured && ev.source === "settings" && h("button", { class: "ghost", type: "button", text: "Entfernen", onclick: async () => {
        await fetch("/api/settings/eduvault", { method: "DELETE", credentials: "same-origin" });
        unitsP = null;
        render();
      } })),
    msg);
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    save.classList.add("busy");
    save.disabled = true;
    msg.replaceChildren();
    try {
      const res = await fetch("/api/settings/eduvault", {
        method: "PUT", credentials: "same-origin", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ url: url.value, token: token.value, secret: secret.value }),
      });
      const body = await res.json().catch(() => ({}));
      if (!res.ok) throw new Error(body.error || `HTTP ${res.status}`);
      msg.replaceChildren(h("p", { class: "ok-note", text: "Verbunden. Altklausuren erscheinen jetzt auf den Modulseiten." }));
      token.value = secret.value = "";
      setTimeout(render, 900);
    } catch (err) {
      msg.replaceChildren(errorBox(err));
    } finally {
      save.classList.remove("busy");
      save.disabled = false;
    }
  });
  tEv.append(status, form);

  // account & instance
  const acc = st.account || {};
  if (st.demo) grid.prepend(tile("Demo", { cls: "w12", i: 0 }, h("p", { class: "empty", text: "Demo mit erfundenen Daten: Einstellungen werden nicht gespeichert." })));
  let tOp = null;
  if (st.operator?.name) {
    tOp = (tile("Betrieb", { cls: "w12", i: 1 }, h("p", { class: "empty", text:
      `Diese Instanz betreibt ${st.operator.name} für dich. Gespeichert sind dein NORDAKADEMIE-Login (für die Abfragen bei CIS und Moodle) und was naknak daraus zwischenspeichert; als Betreiber hat ${st.operator.name} technisch Zugriff darauf. Weiter unten kannst du alles exportieren oder die Instanz zurücksetzen (löscht alles)${st.operator.contact ? `; Fragen an ${st.operator.contact}` : ""}.` })));
  }
  const tAcc = tile("Konto & Instanz", { i: 1 },
    h("dl", { class: "kv" },
      h("dt", { text: "NAK-Konto" }), h("dd", { text: acc.user || "–" }),
      h("dt", { text: "Herkunft" }), h("dd", { text: acc.source === "env" ? "Umgebungsvariablen" : acc.source === "file" ? "Web-Anmeldung" : "–" }),
      h("dt", { text: "Modus" }), h("dd", {}, st.read_only ? h("span", { class: "chip due", text: "nur lesen" }) : h("span", { class: "chip ok", text: "voll" })),
      h("dt", { text: "Version" }), h("dd", { class: "version-dd", text: st.version || "–" }),
      h("dt", { text: "Daten" }), h("dd", { class: "mono-ish", text: st.data_dir || "–" })));

  fetch("/api/version", { credentials: "same-origin" }).then((r) => r.json()).then((v) => {
    const dd = tAcc.querySelector(".version-dd");
    if (dd && v.update) dd.append(" ", h("a", { class: "chip due", href: v.url || "https://github.com/Raindancer118/nak-api/releases", target: "_blank", rel: "noopener", text: `${v.latest} verfügbar` }));
    else if (dd && v.latest && !String(v.current).includes("-")) dd.append(" ", h("span", { class: "chip ok", text: "aktuell" }));
  }).catch(() => {});

  // appearance
  const tLook = tile("Design", { i: 2 });
  const cur = localStorage.getItem("nak-theme") || "system";
  const seg = h("div", { class: "filters", role: "group", "aria-label": "Design" }, themes.map((t) =>
    h("button", { type: "button", "aria-pressed": t === cur ? "true" : "false", text: { system: "System", light: "Hell", dark: "Dunkel" }[t], onclick: (e) => {
      localStorage.setItem("nak-theme", t);
      for (const b of seg.children) b.setAttribute("aria-pressed", b === e.currentTarget ? "true" : "false");
      applyTheme(t);
    } })));
  tLook.append(seg);

  // cache
  const tCache = tile("Zwischenspeicher", { cls: "w8", i: 3 },
    h("p", { class: "empty", text: "Zeigt Gespeichertes sofort und lädt im Hintergrund nach." }),
    h("div", { class: "row-actions" }, h("button", { class: "ghost", type: "button", text: "Zwischenspeicher leeren", onclick: async (e) => {
      e.currentTarget.disabled = true;
      await fetch("/api/cache/clear", { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: "{}" });
      unitsP = null;
      alertBox("Zwischenspeicher geleert.");
      e.currentTarget.disabled = false;
    } })));
  const statBox = h("div", { class: "stat-box" });
  tCache.append(statBox);
  fetch("/api/stats", { credentials: "same-origin" }).then((r) => r.json()).then((st) => {
    const total = st.fetches + st.cache_hits;
    const share = total ? Math.round((st.cache_hits / total) * 100) : 0;
    const rows = Object.entries(st.tools || {}).sort((a, b) => (b[1].fetches + b[1].hits) - (a[1].fetches + a[1].hits)).slice(0, 12);
    statBox.replaceChildren(
      h("div", { class: "stat-line" },
        h("span", {}, h("b", { text: String(st.fetches) }), " Abrufe bei CIS, Moodle & EduVault"),
        h("span", {}, h("b", { text: String(st.cache_hits) }), " aus dem Zwischenspeicher"),
        h("span", {}, h("b", { text: `${share} %` }), " gespart"),
        h("span", { class: "s", text: `seit dem Start (${relTime(st.since)})` })),
      total > 0 && bar(st.cache_hits / total, "var(--ok)"),
      rows.length > 0 && h("details", { class: "lazy" }, h("summary", { text: "Pro Datenquelle" }),
        h("div", { class: "table-wrap" }, h("table", {},
          h("thead", {}, h("tr", {}, ["Quelle", "Abrufe", "Cache", "Fehler"].map((x, i) => h("th", { class: i ? "r" : "", text: x })))),
          h("tbody", {}, rows.map(([k, v]) => h("tr", {}, h("td", { class: "nr", text: k }), h("td", { class: "r", text: v.fetches }), h("td", { class: "r", text: v.hits }), h("td", { class: "r", text: v.errors }))))))),
      h("p", { class: "empty fine-note", text: "Für Monitoring: /metrics (Prometheus-Format, gleicher Login oder Zugangsschlüssel)." }));
  }).catch(() => {});
  // calendar subscription
  const tCal = tile("Kalender-Abo", { cls: "w12", i: 4 });
  const calBox = h("div", { class: "cal-box" }, skeleton());
  tCal.append(h("p", { class: "empty", text: "Vorlesungen, Klausuren und Fristen als Kalender-Abo." }), calBox);
  const drawCal = (info) => {
    const abs = location.origin + info.path;
    const field = h("input", { class: "cal-url", readonly: true, value: abs, "aria-label": "Abo-Adresse", onfocus: (e) => e.currentTarget.select() });
    calBox.replaceChildren(field, h("div", { class: "row-actions" },
      h("button", { class: "primary", type: "button", text: "Adresse kopieren", onclick: async (e) => {
        await navigator.clipboard?.writeText(abs).catch(() => field.select());
        e.currentTarget.textContent = "Kopiert ✓";
      } }),
      h("a", { class: "ghost as-btn", href: abs.replace(/^https?:/, "webcal:"), text: "Im Kalender öffnen" }),
      h("button", { class: "ghost", type: "button", text: "Neuen Link erzeugen", title: "Der alte Link hört sofort auf zu funktionieren", onclick: async () => {
        const r = await fetch("/api/calendar/rotate", { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: "{}" });
        drawCal(await r.json());
        alertBox("Neuer Abo-Link erzeugt. Der alte funktioniert nicht mehr.");
      } })),
      h("p", { class: "empty meter", text: "Wer den Link kennt, sieht deinen Stundenplan." }));
  };
  fetch("/api/calendar", { credentials: "same-origin" }).then((r) => r.json()).then(drawCal).catch((err) => calBox.replaceChildren(errorBox(err)));
  // your data: export (data access) and instance reset (deletion)
  const word = h("input", { class: "cal-url", placeholder: "LÖSCHEN", "aria-label": "Zum Bestätigen LÖSCHEN eintippen", autocomplete: "off" });
  const wipe = h("button", { class: "ghost danger", type: "button", text: "Instanz zurücksetzen", disabled: true });
  word.addEventListener("input", () => { wipe.disabled = word.value !== "LÖSCHEN"; });
  wipe.addEventListener("click", async () => {
    wipe.disabled = true;
    const res = await fetch("/api/reset", { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ confirm: word.value }) });
    if (res.ok) {
      navigator.serviceWorker?.controller?.postMessage("clear");
      location.href = "/login";
    } else alertBox("Zurücksetzen fehlgeschlagen.");
  });
  const tData = tile("Deine Daten", { cls: "w12", i: 6 },
    h("p", { class: "empty", text: "Alles, was über dich gespeichert ist, als ZIP (ohne Passwort)." }),
    h("div", { class: "row-actions" }, h("a", { class: "primary as-btn", href: "/api/export", text: "Daten exportieren" }),
      h("button", { class: "ghost", type: "button", text: "Alle Geräte abmelden", title: "Neuer Zugangsschlüssel: jede Sitzung endet, auch diese", onclick: async () => {
        await fetch("/api/sessions/revoke", { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: "{}" });
        navigator.serviceWorker?.controller?.postMessage("clear");
        location.href = "/login";
      } })),
    h("h2", { class: "sub", text: "Instanz zurücksetzen" }),
    h("p", { class: "empty", text: st.account?.source === "env"
      ? "Löscht Einstellungen, Zwischenspeicher, Verlauf, Downloads und die CIS-Sitzung. Das Konto selbst kommt aus Umgebungsvariablen und bleibt bestehen."
      : "Löscht dein Konto in naknak und alle Daten dieser Instanz (Einstellungen, Zwischenspeicher, Verlauf, Downloads, CIS-Sitzung) und meldet alle Geräte ab. Danach kann sich wieder jemand als Erstes anmelden. In CIS und Moodle selbst wird nichts gelöscht." }),
    h("div", { class: "row-actions danger-row" }, word, wipe));
  const sections = {
    darstellung: () => [tLook, dashSettings(st), homeSettings(st), navSettings(st), topSettings(st), mensaSettings()],
    benachrichtigungen: () => [notifySettingsTile(st, 0)],
    verbindungen: () => [tEv, tCal],
    konto: () => [tApp, tOp, tAcc, tData, tCache],
  };
  grid.append(...(sections[tab] || sections.darstellung)().filter(Boolean));
}

// ── EduVault on module pages ────────────────────────────────────────────────

const evType = { exam: "Klausur", probeklausur: "Probeklausur", note: "Lernzettel", script: "Skript" };

function evRows(r) {
  const docs = r.documents || [];
  if (!docs.length) return emptyRow(`Noch keine Altklausuren zu „${r.module}“ in EduVault.`);
  let filter = "all", all = false;
  const counts = { exam: 0, probeklausur: 0, solved: 0 };
  for (const d of docs) { if (d.type in counts) counts[d.type]++; if (d.has_solutions) counts.solved++; }
  const list = h("ul", { class: "rows" });
  const more = h("button", { class: "ghost more-btn", type: "button" });
  const draw = () => {
    const shown = docs.filter((d) => filter === "all" || (filter === "solved" ? d.has_solutions : d.type === filter));
    list.replaceChildren(...(all ? shown : shown.slice(0, 8)).map((d, j) => h("li", {}, h("button", { class: "row file", type: "button", vars: { "--j": Math.min(j, 12) }, onclick: (e) => openEduVault(d.id, e.currentTarget) },
      h("span", { class: "ext ev", text: /^\d{4}$/.test(String(d.jahr)) ? String(d.jahr).slice(-2) : "EV" }),
      h("span", { class: "t" }, `${evType[d.type] || "Dokument"} ${d.jahr || ""}`.trim(), h("span", { class: "s", text: [d.modul, Array.isArray(d.dozenten) ? d.dozenten.join(", ") : d.dozenten, Number(d.rating) > 0 && `★ ${Number(d.rating).toFixed(1)}`, d.download_count > 0 && `${d.download_count}× geladen`].filter(Boolean).join(" · ") })),
      h("span", { class: `chip ${d.has_solutions ? "ok" : ""}`, text: d.has_solutions ? "mit Lösung" : "öffnen" })))));
    if (!shown.length) list.append(h("li", { class: "empty", text: "Keine Dokumente in dieser Auswahl." }));
    more.hidden = shown.length <= 8;
    more.textContent = all ? "Weniger anzeigen" : `Alle ${shown.length} anzeigen`;
  };
  more.addEventListener("click", () => { all = !all; draw(); });
  const opts = [["all", `Alle ${docs.length}`], ["exam", `Klausuren ${counts.exam}`], ["probeklausur", `Probeklausuren ${counts.probeklausur}`], ["solved", `mit Lösung ${counts.solved}`]].filter(([k], i) => i === 0 || (k === "solved" ? counts.solved : counts[k]));
  const filters = h("div", { class: "filters ev-filters", role: "group", "aria-label": "Auswahl" }, opts.map(([k, label]) =>
    h("button", { type: "button", "aria-pressed": k === filter ? "true" : "false", text: label, onclick: (e) => {
      filter = k;
      all = false;
      for (const b of filters.children) b.setAttribute("aria-pressed", b === e.currentTarget ? "true" : "false");
      draw();
    } })));
  draw();
  return [filters, list, more];
}

async function openEduVault(id, btn) {
  const win = window.open("", "_blank");
  btn?.classList.add("busy");
  try {
    const r = await post("eduvault_download", { exam_id: id });
    if (!r.download_url) throw new Error("Datei konnte nicht bereitgestellt werden");
    if (win) win.location = `${r.download_url}?inline=1`;
  } catch (err) {
    win?.close();
    alertBox(err.message);
  } finally {
    btn?.classList.remove("busy");
  }
}

// ── notifications ───────────────────────────────────────────────────────────

const bell = { el: null, badge: null, panel: null, items: [], unread: 0, es: null };

function relTime(iso) {
  const d = new Date(iso);
  const mins = Math.round((Date.now() - d) / 6e4);
  if (mins < 1) return "gerade eben";
  if (mins < 60) return `vor ${mins} min`;
  if (mins < 24 * 60) return `vor ${Math.round(mins / 60)} h`;
  return fmtShort.format(d);
}

const kindIcon = { grade: "Note", message: "Nachricht", news: "Moodle", moodle: "Moodle", deadline: "Frist" };

function drawBell() {
  if (!bell.el) return;
  bell.badge.textContent = bell.unread > 9 ? "9+" : String(bell.unread);
  bell.badge.hidden = bell.unread === 0;
  bell.el.setAttribute("aria-label", bell.unread ? `${bell.unread} neue Benachrichtigungen` : "Benachrichtigungen");
  if (bell.panel.hidden) return;
  bell.panel.replaceChildren(
    h("div", { class: "bell-head" }, h("h2", { text: "Benachrichtigungen" }),
      bell.unread > 0 && h("button", { class: "ghost small", type: "button", text: "Alle gelesen", onclick: markAllRead })),
    bell.items.length ? h("ul", { class: "rows bell-list" }, bell.items.slice(0, 30).map((n, j) => h("li", {}, h("a", { class: `row ${n.read ? "" : "unread"}`, href: n.url || "#/", vars: { "--j": Math.min(j, 10) }, onclick: () => { bell.panel.hidden = true; } },
      h("span", { class: `chip kind-${n.kind}`, text: kindIcon[n.kind] || "Neu" }),
      h("span", { class: `t ${n.kind === "grade" || n.kind === "message" ? "sens" : ""}` }, n.title, h("span", { class: "s", text: [n.body, relTime(n.at)].filter(Boolean).join(" · ") })),
      !n.read && h("span", { class: "dot new" })))))
      : h("p", { class: "empty", text: "Noch nichts." }),
    h("a", { class: "bell-foot", href: "#/einstellungen", onclick: () => { bell.panel.hidden = true; }, text: "Einstellungen" }));
}

async function loadBell() {
  try {
    const r = await (await fetch("/api/notifications", { credentials: "same-origin" })).json();
    bell.items = r.notifications || [];
    bell.unread = r.unread || 0;
    drawBell();
  } catch {}
}

async function markAllRead() {
  await fetch("/api/notifications/read", { method: "POST", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: "{}" });
  bell.items.forEach((n) => { n.read = true; });
  bell.unread = 0;
  drawBell();
}

const still = document.documentElement.classList.contains("still");

function listenBell() {
  // ?still (screenshots, UI tests): no live stream, so the page can finish loading
  if (!window.EventSource || still) return;
  bell.es?.close();
  bell.es = new EventSource("/api/events");
  bell.es.addEventListener("notification", (e) => {
    const n = JSON.parse(e.data);
    if (n.kind === "grade") unitsP = historyP = null;
    bell.items.unshift(n);
    bell.unread++;
    drawBell();
    bell.el.classList.remove("ring");
    void bell.el.offsetWidth; // restart the ring animation
    bell.el.classList.add("ring");
    if (document.hidden && "Notification" in window && Notification.permission === "granted") {
      const note = new Notification(n.title, { body: n.body || "", icon: "/assets/naknak.svg", tag: n.id });
      note.onclick = () => { window.focus(); location.hash = n.url || "#/"; note.close(); };
    } else if (!document.hidden) {
      alertBox(n.title);
    }
  });
  // EventSource reconnects by itself; a 401 after logout ends it for good
  bell.es.onerror = () => { if (bell.es.readyState === EventSource.CLOSED) setTimeout(listenBell, 30_000); };
}

function bellButton() {
  bell.badge = h("span", { class: "badge", hidden: true });
  bell.el = h("button", { class: "icon-btn bell", type: "button", "aria-haspopup": "true", title: "Benachrichtigungen" },
    svg(["M6 8a6 6 0 1 1 12 0c0 7 3 9 3 9H3s3-2 3-9", "M10.3 21a1.94 1.94 0 0 0 3.4 0"]), bell.badge);
  bell.panel = h("div", { class: "bell-panel", hidden: true, role: "dialog", "aria-label": "Benachrichtigungen" });
  bell.el.addEventListener("click", (e) => {
    e.stopPropagation();
    bell.panel.hidden = !bell.panel.hidden;
    drawBell();
  });
  document.addEventListener("click", (e) => { if (!bell.panel.contains(e.target)) bell.panel.hidden = true; });
  document.addEventListener("keydown", (e) => { if (e.key === "Escape") bell.panel.hidden = true; });
  loadBell();
  listenBell();
  // if a proxy buffers the live stream, the bell still catches up (local
  // list only — this never reaches the CIS or Moodle)
  setInterval(() => { if (!document.hidden) loadBell(); }, 3 * 60_000);
  document.addEventListener("visibilitychange", () => { if (!document.hidden) loadBell(); });
  return h("div", { class: "bell-wrap" }, bell.el, bell.panel);
}

function notifySettingsTile(st, i) {
  const n = st.notify || {};
  const t = tile("Benachrichtigungen", { cls: "w12", i });
  const on = h("input", { type: "checkbox", checked: !n.off });
  const night = h("input", { type: "checkbox", checked: !!n.night });
  const ntfy = h("input", { type: "url", value: n.ntfy_url || "", placeholder: "https://ntfy.sh/dein-geheimes-thema", spellcheck: "false" });
  const details = h("input", { type: "checkbox", checked: !!n.ntfy_details });
  const fast = h("input", { type: "checkbox", checked: !!n.fast_grades });
  const msg = h("div", { class: "form-msg", "aria-live": "polite" });
  const perm = h("button", { class: "ghost", type: "button" });
  const drawPerm = () => {
    const p = "Notification" in window ? Notification.permission : "unsupported";
    perm.textContent = p === "granted" ? "Browser-Benachrichtigungen: erlaubt ✓" : p === "denied" ? "Browser-Benachrichtigungen: im Browser blockiert" : p === "unsupported" ? "Browser unterstützt keine Benachrichtigungen" : "Browser-Benachrichtigungen erlauben";
    perm.disabled = p !== "default";
  };
  perm.addEventListener("click", async () => { await Notification.requestPermission(); drawPerm(); });
  drawPerm();
  const form = h("form", { class: "settings-form wide" },
    h("label", { class: "check" }, on, h("span", {}, h("b", { text: "Im Hintergrund nach Neuem schauen" }), h("small", { text: "Noten alle 3 h · Moodle stündlich · Nachrichten alle 15 min" }))),
    h("label", { class: "check" }, night, h("span", {}, h("b", { text: "Auch nachts (23–7 Uhr)" }), h("small", { text: "Sonst ruht naknak nachts und CIS/Moodle werden nicht gefragt." }))),
    h("label", { class: "check" }, fast, h("span", {}, h("b", { text: "Neue Noten schneller melden" }), h("small", { text: "Prüft alle 10 min die Notenübersicht (PDF), solange eine Note aussteht." }))),
    h("label", {}, "ntfy-Adresse für Push aufs Handy (optional)", ntfy),
    h("label", { class: "check" }, details, h("span", {}, h("b", { text: "Details mitschicken" }), h("small", { text: "Sonst nur „Neue Note in naknak“. ntfy.sh ist öffentlich." }))),
    h("div", { class: "row-actions" }, h("button", { class: "primary", type: "submit", text: "Speichern" }), perm),
    msg);
  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const res = await fetch("/api/settings/notify", { method: "PUT", credentials: "same-origin", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ off: !on.checked, night: night.checked, ntfy_url: ntfy.value.trim(), ntfy_details: details.checked, fast_grades: fast.checked }) });
    const body = await res.json().catch(() => ({}));
    msg.replaceChildren(res.ok ? h("p", { class: "ok-note", text: "Gespeichert." }) : errorBox(new Error(body.error || `HTTP ${res.status}`)));
  });
  t.append(form);
  return t;
}

// ── command palette (Ctrl/⌘+K) ──────────────────────────────────────────────

const palette = { dlg: null };

function paletteCommands() {
  const go = (hash) => () => { location.hash = hash; };
  return [
    { group: "Seiten", title: "Übersicht", hint: "g ü", run: go("#/") },
    { group: "Seiten", title: "Woche", run: go("#/woche") },
    { group: "Seiten", title: "Kurse", run: go("#/kurse") },
    { group: "Seiten", title: "Alle Module", run: go("#/module") },
    { group: "Seiten", title: "Abgaben & Tests", run: go("#/abgaben") },
    { group: "Seiten", title: "Neuigkeiten", run: go("#/neu") },
    { group: "Seiten", title: "Nachrichten", run: go("#/nachrichten") },
    { group: "Seiten", title: "Noten", run: go("#/noten") },
    { group: "Seiten", title: "Prüfungen & Anmeldungen", run: go("#/pruefungen") },
    { group: "Seiten", title: "Seminare", run: go("#/studium/seminare") },
    { group: "Seiten", title: "Wahlpflicht", run: go("#/studium/wahlpflicht") },
    { group: "Seiten", title: "Transferleistungen", run: go("#/transferleistungen") },
    { group: "Seiten", title: "Studienbescheinigung & Notenspiegel", run: go("#/studium/bescheinigungen") },
    { group: "Seiten", title: "Profil", run: go("#/studium/profil") },
    { group: "Seiten", title: "Einstellungen", run: go("#/einstellungen") },
    ...(window.NaknakApp ? [{ group: "Aktionen", title: "App-Einstellungen öffnen", run: () => window.NaknakApp.openSettings() }] : []),
    { group: "Aktionen", title: "Neu laden (frisch aus CIS und Moodle)", run: refreshPage },
    { group: "Aktionen", title: "Design wechseln (System → Hell → Dunkel)", run: () => { const cur = localStorage.getItem("nak-theme") || "system"; const next = themes[(themes.indexOf(cur) + 1) % themes.length]; localStorage.setItem("nak-theme", next); applyTheme(next); } },
    { group: "Aktionen", title: "Sensible Daten aus-/einblenden (Blur)", run: togglePrivate },
    { group: "Aktionen", title: "Alle Benachrichtigungen als gelesen markieren", run: markAllRead },
    { group: "Aktionen", title: "Kalender-Abo einrichten", run: go("#/einstellungen") },
    { group: "Aktionen", title: "Abmelden", run: () => $(".top form[action='/logout']")?.requestSubmit() },
  ];
}

async function openPalette() {
  if (palette.dlg?.open) return;
  const input = h("input", { class: "palette-input", placeholder: "Wohin? Seite, Modul, Kurs oder Aktion …", "aria-label": "Befehl", autocomplete: "off", spellcheck: "false" });
  const list = h("div", { class: "palette-list", role: "listbox" });
  const dlg = h("dialog", { class: "palette", "aria-label": "Befehlspalette" }, input, list,
    h("p", { class: "palette-foot", text: "↑↓ auswählen · ↵ öffnen · Esc schließen" }));
  palette.dlg = dlg;
  document.body.append(dlg);
  dlg.showModal();
  dlg.addEventListener("close", () => dlg.remove());
  dlg.addEventListener("click", (e) => { if (e.target === dlg) dlg.close(); });

  let units = [], orphans = [];
  loadUnits().then((r) => { ({ units, orphans } = r); draw(); }).catch(() => {});
  let active = 0, shown = [];
  const draw = () => {
    const q = input.value.trim();
    const cmds = paletteCommands();
    const unitCmds = units.map((u) => ({ group: "Module", title: u.title || u.nr, hint: u.nr, words: norm([u.nr, ...u.aliases, u.title].join(" ")), run: () => { location.hash = `#/modul/${u.nr}`; } }));
    const courseCmds = orphans.map((c) => ({ group: "Kurse", title: courseTitle(c), hint: c.state === "current" ? "aktuell" : "", words: norm(`${c.name} ${c.shortname}`), run: () => { location.hash = `#/kurs/${c.id}`; } }));
    const all = [...cmds, ...unitCmds, ...courseCmds];
    shown = (q ? all.filter((c) => matches(q, c.words || norm(c.title))) : [...cmds.slice(0, 9), ...unitCmds.filter((c) => units.find((u) => u.nr === c.hint)?.courses.some((x) => x.state === "current"))]).slice(0, 40);
    active = Math.min(active, Math.max(0, shown.length - 1));
    let last = "";
    list.replaceChildren(...shown.flatMap((c, i) => {
      const head = c.group !== last ? [h("h2", { text: c.group })] : [];
      last = c.group;
      return [...head, h("button", { type: "button", class: "hit", role: "option", "aria-selected": i === active ? "true" : "false", onclick: () => { dlg.close(); c.run(); } },
        h("span", { class: "t", text: c.title }), h("span", { class: "s", text: c.hint || "" }))];
    }));
    if (!shown.length) list.append(h("p", { class: "empty hit-note", text: "Nichts gefunden." }));
    list.querySelector('[aria-selected="true"]')?.scrollIntoView({ block: "nearest" });
  };
  input.addEventListener("input", () => { active = 0; draw(); });
  input.addEventListener("keydown", (e) => {
    if (e.key === "ArrowDown") { e.preventDefault(); active = Math.min(active + 1, shown.length - 1); draw(); }
    else if (e.key === "ArrowUp") { e.preventDefault(); active = Math.max(active - 1, 0); draw(); }
    else if (e.key === "Enter" && shown[active]) { e.preventDefault(); dlg.close(); shown[active].run(); }
  });
  draw();
  input.focus();
}

addEventListener("keydown", (e) => {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
    e.preventDefault();
    openPalette();
  }
});

// ── binding actions: preview first, then an explicit second click ───────────

const previewLabels = {
  action: "Aktion", exam: "Prüfung", title: "Titel", start: "Beginn", ende: "Ende", module_nr: "Modul", dozenten: "Prüfer",
  zenturien: "Gruppe", section: "Bereich", message: "Nachricht", subject: "Betreff", text: "Text", course: "Kurs", name: "Name",
  files: "Dateien", online_text: "Online-Text", submit_for_grading: "Endgültig einreichen", assignment: "Aufgabe", to: "An",
  conversation: "Unterhaltung", forum: "Forum", discussion: "Diskussion", count: "Anzahl", would: "Würde", status: "Status",
  email: "Private E-Mail", mobile: "Mobil", phone: "Festnetz", fax: "Fax", infopost: "Infopost", strasse: "Straße", plz: "PLZ", ort: "Ort",
  zusatz: "Zusatz", delete: "Löschen", noten_betrieb: "Noten für den Betrieb", anmeldungen_betrieb: "Anmeldungen für den Betrieb", conversations: "Unterhaltungen", names: "Namen",
};
const previewSkip = /(^|_)(id|ids|url|urls|token|cmid|hash)$|^(next|mode|tool|deadlines|action_url|days_until_exam|request|type)$/;

// previewView turns a tool preview into a readable list: German labels,
// no ids or URLs, nested objects flattened one level.
// CIS form fields as they appear in a preview's changes, readable
const fieldLabels = {
  "telefon.mobil": "Mobil", "telefon.fest": "Festnetz", "telefon.fax": "Fax", "email.privat": "Private E-Mail", "email.infopost": "Infopost",
  notenfreigabe: "Noten für den Betrieb", anmeldungfreigabe: "Anmeldungen für den Betrieb", Fest: "Festnetz (Kommiliton:innen)", Mail: "E-Mail (Kommiliton:innen)",
};
const shareLevels = { 0: "nicht anzeigen", 1: "nur meine Zenturie", 2: "alle Zenturien" };

function changeRow(c) {
  const i = c.field.indexOf("[");
  const key = i < 0 ? c.field : c.field.slice(i).replace(/\]\[/g, ".").replace(/[[\]]/g, "");
  const yesNo = /freigabe|infopost/.test(key);
  const kommi = ["Name", "Firma", "Geburtsdatum", "Adresse", "Fest", "Mobil", "Mail"].includes(key);
  const show = (v) => v === "" ? "leer" : yesNo ? (v === "1" ? "ja" : "nein") : kommi ? shareLevels[v] || v : v;
  return [h("dt", { text: fieldLabels[key] || (kommi ? `${key} (Kommiliton:innen)` : key.split(".").pop()) }), h("dd", { text: `${show(c.old)} → ${show(c.new)}` })];
}

function previewView(pre) {
  const p = pre?.preview ?? pre;
  if (typeof p === "string") return h("pre", { text: p });
  // several form submissions behind one action (e.g. e-mail and phone)
  if (Array.isArray(p) && p.length && p.every((x) => x && typeof x === "object")) return h("div", { class: "preview-list" }, p.map(previewView));
  if (p && Array.isArray(p.changes) && p.changes.every((c) => c && typeof c.field === "string")) {
    return h("dl", { class: "kv" }, p.action && [h("dt", { text: "Aktion" }), h("dd", { text: p.action })],
      p.changes.length ? p.changes.map(changeRow) : [h("dt", { text: "Änderungen" }), h("dd", { text: "keine" })]);
  }
  const rows = [];
  const val = (v) => Array.isArray(v) ? v.map((x) => typeof x === "object" ? JSON.stringify(x) : String(x)).join(", ") : typeof v === "boolean" ? (v ? "ja" : "nein") : String(v);
  const walk = (obj, depth) => {
    for (const [k, v] of Object.entries(obj || {})) {
      if (v === null || v === "" || previewSkip.test(k)) continue;
      if (typeof v === "object" && !Array.isArray(v)) {
        if (depth < 1) walk(v, depth + 1);
        continue;
      }
      rows.push([previewLabels[k] || k, val(v)]);
    }
  };
  if (p && typeof p === "object") walk(p, 0);
  if (!rows.length) return h("pre", { text: JSON.stringify(p, null, 2) });
  return h("dl", { class: "kv" }, rows.map(([k, v]) => [h("dt", { text: k }), h("dd", { text: v })]));
}

// confirmFlow renders a button; the first click asks the server for a preview
// (nothing is sent to CIS/Moodle), only the second, labelled click executes.
// steps: several write tools behind one preview and one confirmation
function confirmFlow({ tool, args, steps, label, confirm, done, cls = "ghost" }) {
  const box = h("div", { class: "preview-box", hidden: true });
  const btn = h("button", { class: cls, type: "button", text: label });
  btn.addEventListener("click", async () => {
    const a = typeof args === "function" ? await args() : args;
    if (!steps && !a) return;
    const todo = steps || [[tool, a]];
    box.hidden = false;
    box.replaceChildren(h("p", { class: "empty", text: "Vorschau wird erstellt …" }));
    try {
      const pres = await Promise.all(todo.map(([t, x]) => post(t, x)));
      const go = h("button", { class: "primary", type: "button", text: confirm });
      go.addEventListener("click", async () => {
        go.disabled = true;
        go.classList.add("busy");
        try {
          let r;
          for (const [t, x] of todo) r = await post(t, { ...x, confirm: true }, "", { "X-Nak-Confirm": "JA" });
          box.replaceChildren(h("p", { class: "ok-note", text: "Erledigt." }));
          unitsP = null;
          historyP = null;
          done?.(r);
        } catch (err) {
          box.replaceChildren(errorBox(err));
        }
      });
      box.replaceChildren(
        h("p", { class: "empty", text: "Noch nichts gesendet. Das würde passieren:" }),
        ...pres.map((pre) => previewView(pre.result)),
        h("div", { class: "row-actions" }, go, h("button", { class: "ghost", type: "button", text: "Abbrechen", onclick: () => { box.hidden = true; } })));
    } catch (err) {
      box.replaceChildren(errorBox(err));
    }
  });
  return h("div", { class: "action" }, btn, box);
}

async function refreshPage() {
  fresh = true;
  try { await render(); } finally { fresh = false; }
}

// ── all exams (incl. electives without a module number) ─────────────────────

async function examsPage(root) {
  root.append(h("div", { class: "page-head" }, h("h1", {}, "Prüfungen", h("small", { text: "Anmeldungen, offene Anmeldungen und Termine aus dem CIS" }))));
  const grid = h("div", { class: "grid" });
  root.append(grid);
  const tMine = tile("Angemeldet", { cls: "w6", i: 0 });
  const tOpen = tile("Anmeldung offen", { cls: "w6", i: 1 });
  grid.append(tMine, tOpen);
  loadPending().then((p) => {
    const rows = pendingRows(p);
    if (rows) grid.prepend(tile("Geschrieben", { cls: "w12", i: 0 }, ...rows));
  });
  const data = Promise.all([api("cis_list_klausuren"), loadUnits().catch(() => null)]);
  const row = (e, j, withAction, units) => {
    const at = parseDE(e.start);
    const u = units?.unitOf(`${e.module_nr} ${e.title}`);
    const until = parseDE(e.action === "register" ? e.deadlines?.register_closes : e.deadlines?.deregister_until);
    const canAct = withAction && (e.action === "register" || e.action === "deregister") && (!until || until > new Date());
    const examiner = (e.dozenten || []).map((d) => d.includes(",") ? d.split(",").map((y) => y.trim()).reverse().join(" ") : d).join(", ");
    return h("li", { class: "exam-row", vars: { "--j": Math.min(j, 12) } },
      h("div", { class: "row" },
        h("span", { class: "dot", "data-src": "cis" }),
        h("span", { class: "t" }, u ? h("a", { href: `#/modul/${u.nr}`, text: e.title }) : e.title,
          h("span", { class: "s", text: [at && fmtDay.format(at), at && !/00:00/.test(fmtTime.format(at)) && fmtTime.format(at), (e.zenturien || []).join(", "), examiner].filter(Boolean).join(" · ") }),
          until && h("span", { class: "s", text: `${e.action === "register" ? "Anmeldung" : "Abmeldung"} bis ${e.action === "register" ? e.deadlines.register_closes : e.deadlines.deregister_until}` })),
        at && h("span", { class: `chip ${dayDiff(at) <= 3 && dayDiff(at) >= 0 ? "due" : ""}`, text: relDay(dayDiff(at)) })),
      canAct && confirmFlow({ tool: "cis_klausur_action", args: { exam_id: e.exam_id, action: e.action },
        label: e.action_label || (e.action === "register" ? "Anmelden" : "Abmelden"),
        confirm: e.action === "register" ? "Verbindlich anmelden" : "Verbindlich abmelden", done: refreshPage }));
  };
  fill(tMine, () => data, ([list, units]) => {
    const mine = (list || []).filter((e) => e.registered).sort((a, b) => (parseDE(a.start) || 0) - (parseDE(b.start) || 0));
    return mine.length ? h("ul", { class: "rows" }, mine.map((e, j) => row(e, j, true, units))) : emptyRow("Zu keiner Prüfung angemeldet.");
  });
  fill(tOpen, () => data, ([list, units]) => {
    // one exam per group: hide offers for exams you are already registered for
    const mineKeys = new Set((list || []).filter((e) => e.registered).map((e) => `${e.start}|${norm(e.title).join(" ")}`));
    const open = (list || []).filter((e) => !e.registered && e.action === "register" && !mineKeys.has(`${e.start}|${norm(e.title).join(" ")}`))
      .filter((e) => { const until = parseDE(e.deadlines?.register_closes); return !until || until > new Date(); })
      .sort((a, b) => (parseDE(a.deadlines?.register_closes) || 0) - (parseDE(b.deadlines?.register_closes) || 0));
    return open.length ? h("ul", { class: "rows" }, open.map((e, j) => row(e, j, true, units))) : emptyRow("Gerade keine offenen Anmeldungen.");
  });
}

// ── studies: seminars, electives, Transferleistungen, certificates, profile ─

// openLocal runs a download tool on the server and shows the file.
async function openLocal(tool, args, btn) {
  const win = window.open("", "_blank");
  btn?.classList.add("busy");
  try {
    const r = await post(tool, args);
    if (!r.download_url) throw new Error("Datei konnte nicht bereitgestellt werden");
    if (win) win.location = `${r.download_url}?inline=1`;
  } catch (err) {
    win?.close();
    alertBox(err.message);
  } finally {
    btn?.classList.remove("busy");
  }
}

// genericView renders an unknown tool result readably (detail pages of the
// CIS differ a lot): labels, lists and tables, never raw HTML.
function genericView(v, depth = 0) {
  if (v == null || v === "") return null;
  if (typeof v !== "object") return h("span", { text: String(v) });
  if (Array.isArray(v)) {
    if (!v.length) return null;
    if (v.every((x) => typeof x !== "object")) return h("span", { text: v.join(", ") });
    const keys = [...new Set(v.flatMap((x) => Object.keys(x || {})))].filter((k) => !/(_|^)(url|id)$/.test(k)).slice(0, 6);
    return h("div", { class: "table-wrap" }, h("table", {},
      h("thead", {}, h("tr", {}, keys.map((k) => h("th", { text: k.replace(/_/g, " ") })))),
      h("tbody", {}, v.map((row, j) => h("tr", { vars: { "--j": Math.min(j, 15) } }, keys.map((k) => h("td", {}, typeof row?.[k] === "object" ? genericView(row[k], depth + 1) : String(row?.[k] ?? ""))))))));
  }
  const entries = Object.entries(v).filter(([k, x]) => x !== null && x !== "" && !/(_|^)(url|id)$/.test(k));
  return h("dl", { class: "kv generic" }, entries.map(([k, x]) => [h("dt", { text: k.replace(/_/g, " ") }), h("dd", {}, genericView(x, depth + 1))]));
}

function lazyDetails(summary, load) {
  const body = h("div", { class: "lazy-body" });
  const d = h("details", { class: "lazy" }, h("summary", { text: summary }), body);
  d.addEventListener("toggle", async () => {
    if (!d.open || body.dataset.loaded) return;
    body.dataset.loaded = "1";
    body.replaceChildren(skeleton()[1]);
    try { body.replaceChildren(...[await load()].flat().filter(Boolean)); } catch (err) { body.replaceChildren(errorBox(err)); }
  });
  return d;
}

async function studiesPage(root, tab = "seminare") {
  root.append(h("div", { class: "page-head" }, h("h1", {}, "Studium", h("small", { text: "Seminare, Wahlpflicht, Transferleistungen, Bescheinigungen" }))));
  const tabs = [["seminare", "Seminare"], ["wahlpflicht", "Wahlpflicht"], ["bescheinigungen", "Bescheinigungen"], ["profil", "Profil"]];
  root.append(h("nav", { class: "tabs", "aria-label": "Bereiche" }, tabs.map(([k, label]) => h("a", { href: `#/studium/${k}`, text: label, ...(k === tab ? { "aria-current": "page" } : {}) }))));
  const grid = h("div", { class: "grid" });
  root.append(grid);
  if (tab === "seminare") return seminarsTab(grid);
  if (tab === "wahlpflicht") return electivesTab(grid);
  if (tab === "transfer") { location.replace("#/transferleistungen"); return; }
  if (tab === "bescheinigungen") return certsTab(grid);
  return profileTab(grid);
}

function seminarRow(s, j) {
  const statuses = Array.isArray(s.status) ? s.status : s.status ? [s.status] : [];
  const actions = (s.actions || []).filter((a) => a.action);
  return h("li", { class: "seminar", vars: { "--j": Math.min(j, 14) } },
    h("div", { class: "row" },
      h("span", { class: "dot", "data-src": "cis" }),
      h("span", { class: "t" }, s.title, h("span", { class: "s", text: [s.lecturer, [s.from, s.to].filter(Boolean).join(" – "), s.info].filter(Boolean).join(" · ") })),
      h("span", { class: "chips" }, s.category && h("span", { class: "chip", text: s.category }), statuses.map((x) => h("span", { class: `chip ${/angemeldet|zugelassen|bestätigt|besucht|teilnehmer/i.test(x) ? "ok" : /warte/i.test(x) ? "due" : /abgesagt/i.test(x) ? "bad" : ""}`, text: x })))),
    lazyDetails("Details & Belegung", async () => {
      const [d, p] = await Promise.allSettled([api("cis_seminar_detail", { seminar_id: s.id }), api("cis_seminar_participation", { seminar_id: s.id })]);
      return [p.status === "fulfilled" && genericView(p.value), d.status === "fulfilled" ? genericView(d.value) : errorBox(d.reason)];
    }),
    actions.map((a) => confirmFlow({ tool: "cis_seminar_action", args: { seminar_id: s.id, action: a.action }, label: a.label || a.action, confirm: `Verbindlich: ${a.label || a.action}`, done: refreshPage })));
}

function seminarsTab(grid) {
  const tMine = tile("Meine Seminare", { cls: "w12", i: 0 });
  const tAll = tile("Seminarprogramm", { cls: "w12", i: 1 });
  grid.append(tMine, tAll);
  fill(tMine, () => api("cis_list_seminars", { mine: true }), (r) => {
    const list = r.seminars || [];
    return list.length ? h("ul", { class: "rows" }, list.map(seminarRow)) : emptyRow("Noch keine Seminare.");
  });
  let quarter = "", onlyOpen = true;
  const box = h("div");
  const load = () => fill(box.replaceChildren() || box, () => api("cis_list_seminars", { ...(quarter ? { quarter } : {}), available_only: onlyOpen }), (r) => {
    const list = r.seminars || [];
    const quarters = Object.keys(r.available_quarters || {});
    const sel = h("select", { "aria-label": "Quartal", onchange: (e) => { quarter = e.currentTarget.value; load(); } }, quarters.map((q) => h("option", { value: q, text: q, ...(q === (quarter || r.quarter) ? { selected: true } : {}) })));
    const tog = h("label", { class: "inline-check" }, h("input", { type: "checkbox", checked: onlyOpen, onchange: (e) => { onlyOpen = e.currentTarget.checked; load(); } }), "nur mit Anmeldung");
    return [h("div", { class: "toolbar" }, quarters.length > 0 && sel, tog), r.notice && h("p", { class: "empty", text: r.notice }),
      list.length ? h("ul", { class: "rows" }, list.map(seminarRow)) : emptyRow("Keine Seminare in dieser Auswahl.")];
  });
  tAll.append(box);
  load();
}

function electivesTab(grid) {
  const t = tile("Wahlpflichtmodule", { cls: "w12", i: 0 });
  grid.append(t);
  fill(t, () => api("cis_list_wahlpflicht"), (r) => {
    const chosen = r.chosen || [], avail = r.available || [];
    return [
      r.notice && h("p", { class: "empty", text: r.notice }),
      h("h2", { class: "sub", text: "Gewählt" }),
      chosen.length ? h("ul", { class: "rows" }, chosen.map((m, j) => h("li", {}, h("div", { class: "row", vars: { "--j": j } },
        h("span", { class: "dot", "data-src": "cis" }), h("span", { class: "t" }, m.name, h("span", { class: "s", text: m.termin || "" })), h("span", { class: "chip ok", text: "gewählt" }))))) : emptyRow("Noch nichts gewählt."),
      h("h2", { class: "sub", text: r.selection_open ? "Wählbar" : "Wahlzeitraum geschlossen" }),
      r.selection_open && (avail.length ? h("ul", { class: "rows" }, avail.map((m, j) => h("li", { class: "seminar" },
        h("div", { class: "row", vars: { "--j": j } }, h("span", { class: "dot", "data-src": "cis" }), h("span", { class: "t" }, m.name, h("span", { class: "s", text: [m.lecturer, m.vertiefung, m.termin].filter(Boolean).join(" · ") }))),
        lazyDetails("Details & Termine", async () => {
          const d = await api("cis_wahlpflicht_detail", { module_id: m.id });
          const termine = d.termine || d.dates || [];
          return [genericView(d), (termine.length ? termine : [null]).map((tm) => confirmFlow({
            tool: "cis_select_wahlpflicht", args: { module_id: m.id, ...(tm?.id ? { termin_id: tm.id } : {}) },
            label: tm ? `Wählen: ${tm.label || tm.name || tm.id}` : "Wählen", confirm: "Verbindlich wählen", done: refreshPage }))];
        })))) : emptyRow("Gerade nichts wählbar.")),
    ];
  });
}

// Transferleistungen (PO § 5 Abs. 3, PVO § 18): six Transfermodule
// Theorie/Praxis, 5 ECTS each, each written on a topic of a module of the
// required curriculum; only "bestanden"/"nicht bestanden", two retries, never
// part of the overall grade. T1–T5 (25 ECTS) are needed for the thesis.
const TL_SLOTS = 6, TL_ECTS = 5, TL_FOR_THESIS = 5;

const tlPassed = (x) => /bestanden/i.test(x?.wertung || "") && !/nicht/i.test(x?.wertung || "");
const tlModuleNr = (x) => (String(x?.module || "").match(/\(([A-Z]{1,3}\d{3})\)\s*$/) || [])[1];

function tlSlots(list, plan) {
  const byNo = new Map((list || []).map((x) => [Number(x.no), x]));
  const planNo = new Map((plan?.known ? plan.slots : []).map((p) => [p.no, p]));
  return Array.from({ length: TL_SLOTS }, (_, k) => ({ no: k + 1, tl: byNo.get(k + 1), plan: planNo.get(k + 1) }));
}

// credits and the thesis requirement, from the list itself
function tlState(list) {
  const passed = (list || []).filter(tlPassed);
  const forThesis = passed.filter((x) => Number(x.no) <= TL_FOR_THESIS).length;
  return { ects: passed.length * TL_ECTS, of: TL_SLOTS * TL_ECTS, forThesis, thesisOK: forThesis >= TL_FOR_THESIS };
}

// plan states (nak_transfer_plan) as the owner reads them
const tlStateLabel = {
  done: ["im Plan", "ok"], late: ["", "ok"], progress: ["in Arbeit", ""], behind: ["im Verzug", "bad"], now: ["jetzt dran", "due"], upcoming: ["geplant", ""],
};

function tlPhase(ps) {
  if (!ps) return "";
  return `Praxisphase ${ps.phase} (${ps.phase_from.slice(0, 6)}–${ps.phase_to})`;
}

function tlRow(slot, j, { details = false } = {}) {
  const x = slot.tl, ps = slot.plan;
  const nr = tlModuleNr(x);
  let chip;
  if (ps && (!x || !tlPassed(x) || ps.state === "late")) {
    const [label, cls] = tlStateLabel[ps.state] || [ps.state, ""];
    chip = h("span", { class: `chip ${cls}`, text: ps.state === "late" ? `bestanden · ${ps.weeks_off} Wo. später` : label });
  } else {
    chip = h("span", { class: `chip ${x ? (tlPassed(x) ? "ok" : /nicht/i.test(x.wertung) ? "bad" : "") : ""}`, text: x ? (x.wertung || x.status || "–") : `${TL_ECTS} ECTS` });
  }
  const when = ps && (ps.state === "now" || ps.state === "upcoming") ? ` · anmelden bis ${ps.register_by}` : "";
  const head = h("div", { class: "row" },
    h("span", { class: "ext tl", text: `T${slot.no}` }),
    x ? h("span", { class: "t" }, x.topic || "Thema offen", h("span", { class: "s" },
      nr ? h("a", { href: `#/modul/${nr}`, text: x.module }) : (x.module || ""),
      [x.abgabedatum && ` · Abgabe bis ${x.abgabedatum}`, x.versuch && ` · ${x.versuch}. Versuch (max. 3)`, ps && ` · ${tlPhase(ps)}`].filter(Boolean).join("")))
      : h("span", { class: "t" }, `Transfermodul Theorie/Praxis ${slot.no}`, h("span", { class: "s", text: [ps ? tlPhase(ps) : "noch offen"].filter(Boolean).join(" · ") + when })),
    chip);
  if (!x || !details) return h("li", { vars: { "--j": j } }, head);
  return h("li", { class: "seminar", vars: { "--j": j } }, head,
    lazyDetails("Bewertung & Dokumente", async () => {
      const b = await api("cis_transfer_bewertung", { transfer_id: x.id });
      const docs = b.documents || b.dokumente || [];
      return [b.has_gesamtnote && h("p", { class: "empty" }, "Kriterienwert: ", h("b", { class: "sens", text: String(b.gesamtnote.toFixed(2)).replace(".", ",") }), " (kein offizieller Notenwert)"),
        (b.kriterien || []).length > 0 && h("ul", { class: "rows" }, b.kriterien.map((k, n) => h("li", {}, h("div", { class: "row", vars: { "--j": n } },
          h("span", { class: "t" }, k.kriterium, h("span", { class: "s", text: [k.gewichtung, k.feedback].filter(Boolean).join(" · ") })),
          h("span", { class: "chip sens", text: String(k.note).replace(".", ",") }))))),
        docs.length > 0 && h("ul", { class: "rows files" }, docs.map((d, k) => h("li", {}, h("button", { class: "row file", type: "button", vars: { "--j": k }, onclick: (e) => openLocal("cis_download_transfer_document", { download_url: d.url || d.download_url }, e.currentTarget) },
          h("span", { class: "ext", text: "PDF" }), h("span", { class: "t", text: d.label || d.name || d.title || "Dokument" }), h("span", { class: "chip", text: "öffnen" })))))];
    }));
}

// what to do next, from the plan: behind or on track, the latest start of
// the next one, and what the bachelor thesis needs
function tlAdvice(plan, st, list) {
  if (!plan?.known) return null;
  const out = [];
  if (plan.behind > 0) out.push(h("p", { class: "tl-alert bad" }, h("b", { text: plan.behind === 1 ? "1 Transferleistung im Verzug" : `${plan.behind} Transferleistungen im Verzug` }), " gegenüber dem Studienverlaufsplan."));
  else out.push(h("p", { class: "tl-alert ok" }, h("b", { text: "Im Plan." }), " Keine Transferleistung im Verzug."));
  // running ones first, then the first one not started yet
  const started = new Set((list || []).map((x) => Number(x.no)));
  for (const p of plan.slots.filter((p) => p.state === "progress" || (started.has(p.no) && p.state === "behind" && !/bestanden/i.test(p.wertung || "")))) {
    const tl = (list || []).find((x) => Number(x.no) === p.no);
    out.push(h("p", { class: "empty" }, h("b", { text: `T${p.no} läuft` }), tl?.abgabedatum ? `: Abgabe bis ${tl.abgabedatum}.` : "."));
  }
  const n = plan.slots.find((p) => !started.has(p.no));
  if (n) {
    const asap = n.state === "behind" || n.tight;
    out.push(h("p", { class: "empty" }, h("b", { text: `Als Nächstes T${n.no}: ` }),
      asap ? `so bald wie möglich anmelden (die Praxisphase ${n.phase} ${n.state === "behind" ? "ist vorbei" : "reicht für die 9 Wochen nicht mehr ganz"}).` : `spätestens bis ${n.register_by} anmelden, um in der Praxisphase ${n.phase} fertig zu werden.`));
  }
  if (!st.thesisOK && plan.thesis_register_by) {
    out.push(h("p", { class: "empty" }, "Für die ", h("a", { href: "#/bachelorthesis", text: "Bachelorthesis" }),
      plan.thesis_register_until ? ` (Anmeldung spätestens bis ${plan.thesis_register_until})` : "",
      ` müssen T1–T5 bei der Anmeldung bestanden sein (${st.forThesis} von 5): die letzte davon spätestens am ${plan.thesis_register_by} beginnen.`));
  }
  return out;
}

// dashboard tile and page head: credits, plan advice, the six slots
function transferOverview(list, plan, { details = false } = {}) {
  const st = tlState(list);
  return [h("div", { class: "band-num" }, h("div", { class: "avg" }, count(st.ects), h("span", { class: "empty", text: `von ${st.of} ECTS` }))),
    ...(tlAdvice(plan, st, list) || [h("p", { class: "empty" }, st.thesisOK ? "T1–T5 bestanden: Voraussetzung für die Bachelorthesis erfüllt." : `Für die Bachelorthesis: T1–T5, davon ${st.forThesis} bestanden.`)]),
    h("ul", { class: "rows" }, tlSlots(list, plan).map((slot, j) => tlRow(slot, j, { details })))];
}

// the criteria values: orientation only, never an official grade
function transferGradeSummary(g, limit = Infinity) {
  const items = (g?.items || []).slice(0, limit);
  if (!items.length) return emptyRow("Noch keine bewertete Transferleistung.");
  return [h("div", { class: "band-num" }, h("div", { class: "avg" }, h("span", { class: "big sens", text: g.final || g.average || "–" }), h("span", { class: "empty sens", text: `Schnitt aus ${g.count} · Kriterienmittel ${g.average}` }))),
    h("ul", { class: "rows" }, items.map((x, j) => h("li", {}, h("div", { class: "row", vars: { "--j": j } },
      h("span", { class: "ext tl", text: `T${x.no}` }),
      h("span", { class: "t" }, x.topic, h("span", { class: "s" }, x.module_nr ? h("a", { href: `#/modul/${x.module_nr}`, text: x.module }) : x.module, h("span", { class: "sens", text: ` · Kriterienwert ${x.grade}` }))),
      h("span", { class: `chip sens ${x.must_pass_ok ? "" : "bad"}`, text: x.final || x.grade })))))];
}

// ── Mensa ───────────────────────────────────────────────────────────────────
// Menu public (mensa.nordakademie.de), card balance and bookings with the NAK
// login. The total spent stays hidden unless switched on in the settings.
const fmtEuro = (x) => (x ?? 0).toLocaleString("de-DE", { style: "currency", currency: "EUR" });
const mensaSpendingOn = () => localStorage.getItem("nak-mensa-spending") === "1";
const isoToday = () => { const d = new Date(); return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`; };
const dishVeg = (d) => (d.tags || []).some((t) => t === "Vegetarisch" || t === "Vegan") || /veg/i.test(d.line);

function dishRows(dishes) {
  if (!dishes.length) return emptyRow("Nichts auf dem Plan.");
  return h("ul", { class: "rows" }, dishes.map((d, j) => h("li", {}, h("div", { class: "row", vars: { "--j": j } },
    h("span", { class: `ext ${dishVeg(d) ? "veg" : ""}`, text: d.line.replace(/^Menü\s*/i, "").replace(/\s*veg\.?$/i, "") || "·" }),
    h("span", { class: "t" }, d.name, h("span", { class: "s", text: [...(d.tags || []), (d.allergens || []).length && `Allergene: ${d.allergens.join(", ")}`].filter(Boolean).join(" · ") })),
    h("span", { class: "chip", text: fmtEuro(d.price) })))));
}

// today, or the next day with a menu (weekend, evening)
function mensaNextDay(menu) {
  const today = isoToday();
  return (menu.days || []).find((d) => d.date >= today && d.dishes.length);
}

function mensaDayLabel(day) {
  const today = isoToday();
  if (day.date === today) return "Heute";
  const t = new Date(today); t.setDate(t.getDate() + 1);
  return day.date === t.toISOString().slice(0, 10) ? "Morgen" : `${day.weekday.slice(0, 2)} ${day.date.slice(8, 10)}.${day.date.slice(5, 7)}.`;
}

function mensaToday(menu, acc) {
  const day = mensaNextDay(menu);
  return [acc && h("div", { class: "band-num" }, h("div", { class: "avg" }, h("span", { class: "big sens", text: fmtEuro(acc.balance) }), h("span", { class: "empty", text: "Guthaben" }))),
    day ? [h("p", { class: "empty", text: mensaDayLabel(day) }), dishRows(day.dishes)] : emptyRow("Noch kein Speiseplan veröffentlicht.")].flat().filter(Boolean);
}

// the last few, more on demand
function bookingRows(bs, step = 3) {
  if (!bs.length) return emptyRow("Keine Buchungen.");
  const row = (b, j) => h("li", {}, h("div", { class: "row", vars: { "--j": j } },
    h("span", { class: "ext", text: new Intl.DateTimeFormat("de-DE", { day: "2-digit", month: "2-digit" }).format(new Date(b.at)) }),
    h("span", { class: "t" }, b.dish || b.item, h("span", { class: "s", text: [b.dish && b.item, b.qty > 1 && `${b.qty}×`].filter(Boolean).join(" · ") })),
    h("span", { class: `chip sens ${b.amount > 0 ? "ok" : ""}`, text: `${b.amount > 0 ? "+" : ""}${fmtEuro(b.amount)}` })));
  let shown = Math.min(step, bs.length);
  const list = h("ul", { class: "rows" }, bs.slice(0, shown).map(row));
  const more = h("button", { class: "ghost", type: "button", text: "Mehr anzeigen" });
  const actions = h("div", { class: "row-actions" }, more);
  actions.hidden = shown >= bs.length;
  more.addEventListener("click", () => {
    const next = Math.min(shown + 10, bs.length);
    list.append(...bs.slice(shown, next).map((b, j) => row(b, j)));
    shown = next;
    actions.hidden = shown >= bs.length;
  });
  return [list, actions];
}

function spendingView(r) {
  const s = r.spending || {};
  const months = (s.months || []).slice(-12);
  const top = Math.max(1, ...months.map((m) => m.food));
  const since = s.since ? new Intl.DateTimeFormat("de-DE", { month: "long", year: "numeric" }).format(new Date(s.since)) : "";
  return [h("div", { class: "band-num" }, h("div", { class: "avg" }, h("span", { class: "big sens", text: fmtEuro(s.food) }),
    h("span", { class: "empty", text: [since && `seit ${since}`, `${s.meals} Menüs`, (s.months || []).length && `⌀ ${fmtEuro(s.food / s.months.length)} im Monat`].filter(Boolean).join(" · ") }))),
    months.length ? h("ul", { class: "spend-bars" }, months.map((m) => h("li", { title: `${m.month}: ${fmtEuro(m.food)} · ${m.meals} Menüs` },
      h("span", { class: "bar sens", vars: { "--v": (m.food / top).toFixed(3) } }),
      h("span", { class: "lbl", text: new Intl.DateTimeFormat("de-DE", { month: "short" }).format(new Date(m.month + "-01")) })))) : null].filter(Boolean);
}

async function mensaPage(root) {
  root.append(h("div", { class: "page-head" }, h("h1", {}, "Mensa", h("small", { text: "Speiseplan und Mensakarte" }))));
  const grid = h("div", { class: "grid" });
  root.append(grid);
  const tMenu = tile("Speiseplan", { cls: "w8", i: 0 });
  const tCard = tile("Mensakarte", { i: 1 });
  grid.append(tMenu, tCard);
  if (mensaSpendingOn()) {
    const tSpend = tile("Ausgaben fürs Essen", { cls: "w12", i: 2 });
    grid.append(tSpend);
    fill(tSpend, () => api("mensa_spending"), spendingView);
  }
  fill(tMenu, () => api("mensa_menu", { weeks: 3 }), (menu) => {
    const days = (menu.days || []).filter((d) => d.date >= isoToday());
    if (!days.length) return emptyRow("Noch kein Speiseplan veröffentlicht.");
    let pick = days[0].date, vegOnly = localStorage.getItem("nak-mensa-veg") === "1";
    const seg = h("div", { class: "filters", role: "group", "aria-label": "Tag" });
    const list = h("div", {});
    const veg = h("button", { type: "button", text: "nur vegetarisch" });
    const draw = () => {
      seg.querySelectorAll("button[data-d]").forEach((b) => b.setAttribute("aria-pressed", String(b.dataset.d === pick)));
      veg.setAttribute("aria-pressed", String(vegOnly));
      const day = days.find((d) => d.date === pick);
      list.replaceChildren(dishRows(day.dishes.filter((d) => !vegOnly || dishVeg(d))));
    };
    for (const d of days) {
      const b = h("button", { type: "button", text: mensaDayLabel(d), onclick: () => { pick = d.date; draw(); } });
      b.dataset.d = d.date;
      seg.append(b);
    }
    veg.addEventListener("click", () => { vegOnly = !vegOnly; localStorage.setItem("nak-mensa-veg", vegOnly ? "1" : "0"); draw(); });
    draw();
    return [seg, h("div", { class: "filters mensa-veg" }, veg), list];
  });
  fill(tCard, () => api("mensa_account", { days: 365 }), (acc) => [
    h("div", { class: "band-num" }, h("div", { class: "avg" }, h("span", { class: "big sens", text: fmtEuro(acc.balance) }), h("span", { class: "empty", text: acc.balance < 5 ? "reicht für kein Menü mehr" : "Guthaben" }))),
    ...[bookingRows(acc.bookings || [])].flat()]);
}

// search placement and greeting
function dashSettings(st) {
  let ui = { ...savedUI(), ...(st.ui || {}) };
  const msg = h("div", { "aria-live": "polite" });
  const seg = (label, opts, get, set) => {
    const g = h("div", { class: "filters", role: "group", "aria-label": label });
    const draw = () => g.querySelectorAll("button").forEach((b) => b.setAttribute("aria-pressed", String(b.dataset.v === get())));
    for (const [v, text] of opts) {
      const b = h("button", { type: "button", text, onclick: () => { set(v); draw(); sync(); } });
      b.dataset.v = v;
      g.append(b);
    }
    draw();
    return g;
  };
  let mode = ui.greeting_off ? "off" : ui.greeting_text ? "custom" : "auto";
  const text = h("input", { value: ui.greeting_text || "", placeholder: "{gruß}, {name}.", maxlength: 80, "aria-label": "Eigene Begrüßung" });
  const textRow = h("label", {}, "Text ({gruß} und {name} werden ersetzt)", text);
  const preview = h("p", { class: "empty" });
  const sync = () => {
    textRow.hidden = mode !== "custom";
    preview.textContent = mode === "off" ? "Keine Begrüßung." : `Vorschau: ${greet(mode === "custom" ? text.value : "", new Date(), "Max")}`;
  };
  text.addEventListener("input", sync);
  const save = h("button", { class: "primary", type: "button", text: "Speichern", onclick: async () => {
    const body = { search: ui.search, greeting_off: mode === "off", greeting_text: mode === "custom" ? text.value.trim() : "" };
    const res = await fetch("/api/settings/ui", { method: "PUT", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) });
    const out = await res.json().catch(() => ({}));
    if (!res.ok) { msg.replaceChildren(errorBox(new Error(out.error || `HTTP ${res.status}`))); return; }
    ui = out.ui || body;
    applyUI(ui);
    msg.replaceChildren(h("p", { class: "ok-note", text: "Gespeichert." }));
  } });
  const form = h("div", { class: "settings-form" },
    h("b", { text: "Suche" }),
    seg("Suche", [["home", "Auf der Startseite"], ["top", "Oben"], ["nav", "In der Leiste"]], () => ui.search, (v) => { ui.search = v; }),
    h("b", { text: "Begrüßung" }),
    seg("Begrüßung", [["auto", "Automatisch"], ["custom", "Eigener Text"], ["off", "Aus"]], () => mode, (v) => { mode = v; }),
    textRow, preview,
    h("div", { class: "row-actions" }, save), msg);
  sync();
  return tile("Startseite oben", { i: 0 }, form);
}

// which top-bar buttons show; saved on the server like the bar below
function topSettings(st) {
  let hidden = Array.isArray(st.top_hidden) ? st.top_hidden : savedTopHidden();
  const msg = h("div", {});
  const boxes = Object.entries(topDefs).map(([id, label]) => {
    const box = h("input", { type: "checkbox" });
    box.checked = !hidden.includes(id);
    box.addEventListener("change", async () => {
      const next = Object.keys(topDefs).filter((k) => k === id ? !box.checked : hidden.includes(k));
      const res = await fetch("/api/settings/top", { method: "PUT", credentials: "same-origin", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ top_hidden: next }) });
      const body = await res.json().catch(() => ({}));
      if (!res.ok) { box.checked = !box.checked; msg.replaceChildren(errorBox(new Error(body.error || `HTTP ${res.status}`))); return; }
      hidden = body.top_hidden || next;
      applyTop(hidden);
      msg.replaceChildren();
    });
    return h("label", { class: "check" }, box, h("span", {}, h("b", { text: label })));
  });
  return tile("Kopfleiste", { i: 0 }, h("div", { class: "settings-form" }, ...boxes), msg);
}

function mensaSettings() {
  const on = h("input", { type: "checkbox" });
  on.checked = mensaSpendingOn();
  on.addEventListener("change", () => localStorage.setItem("nak-mensa-spending", on.checked ? "1" : "0"));
  return tile("Mensa", { i: 0 }, h("div", { class: "settings-form" },
    h("label", { class: "check" }, on, h("span", {}, h("b", { text: "Gesamtausgaben anzeigen" }), h("small", { text: "Auf der Mensa-Seite: wie viel du fürs Essen ausgegeben hast." })))));
}

async function transferPage(root) {
  root.append(h("div", { class: "page-head" }, h("h1", {}, "Transferleistungen", h("small", { text: "Transfermodule Theorie/Praxis 1–6" }))));
  const grid = h("div", { class: "grid" });
  root.append(grid);
  const tSlots = tile("Transfermodule", { cls: "w8", i: 0 });
  const tValues = tile("Noten · rechnerisch", { i: 1 });
  grid.append(tSlots, tValues);
  fill(tSlots, () => Promise.all([api("cis_list_transfer"), api("nak_transfer_plan").catch(() => null)]), ([list, plan]) => transferOverview(list, plan, { details: true }));
  fill(tValues, () => api("nak_transfer_grades"), (g) => [...[transferGradeSummary(g)].flat(),
    ...(g.items || []).filter((x) => !x.must_pass_ok).map((x) => h("p", { class: "err", text: `T${x.no}: Muss-Kriterium schlechter als 4,0` }))]);
}

// ── Bachelorthesis ──────────────────────────────────────────────────────────
// The rules of the NORDAKADEMIE (PO § 7, PVO §§ 17, 22, CIS page
// "Bachelorthesis" with the planning aid of the cohort): registration in a
// week, start the Monday two weeks later, two months, review in four lecture
// weeks, grades by the "letzte Noten" date of a graduation.

const isoOf = (de) => { const m = String(de || "").match(/(\d{2})\.(\d{2})\.(\d{4})/); return m ? `${m[3]}-${m[2]}-${m[1]}` : ""; };

// the dashboard tile: may I register, and the next date that matters
function thesisSummary(t) {
  const e = t.eligibility || {}, a = t.planning_aid || {};
  const steps = thesisSteps(a).filter((x) => x.at && x.at >= startOfDay(new Date()));
  const next = steps[0];
  return [h("p", { class: `tl-alert ${e.open ? "ok" : "bad"}` }, h("b", { text: e.open ? "Anmeldung möglich." : "Anmeldung noch nicht möglich." }), e.open ? "" : ` ${(e.reasons || []).join(" ")}`),
    next && h("div", { class: "band-num" }, h("div", { class: "avg" }, count(Math.max(0, dayDiff(next.at))), h("span", { class: "empty", text: `Tage bis: ${next.label} (${next.date})` }))),
    a.latest_register_kw && h("p", { class: "empty", text: `Späteste Anmeldung für ${a.last_grades ? "den Abschluss " + a.board : "den nächsten Abschluss"}: KW ${a.latest_register_kw} (${a.latest_register_from} – ${a.latest_register_to}).` })];
}

function thesisSteps(a) {
  return [
    ["Früheste Anmeldung", a.earliest_registration], ["Frühester Start", a.earliest_start],
    ["7. Theoriesemester (Repetitorien + Klausuren)", a.semester7_from && `${a.semester7_from} – ${a.semester7_to}`, a.semester7_from],
    [`Späteste Anmeldung (KW ${a.latest_register_kw || "?"})`, a.latest_register_from && `${a.latest_register_from} – ${a.latest_register_to}`, a.latest_register_to],
    ["Spätester Start", a.latest_start], ["Späteste Abgabe", a.latest_submission],
    ["Wiederholungsklausuren 6. + 7. Semester", a.repeat_exams_from && `${a.repeat_exams_from} – ${a.repeat_exams_to}`, a.repeat_exams_from],
    ["Letzte Noten beim Prüfungsamt", a.last_grades], ["Prüfungsausschuss = Ende des Studiums", a.board], ["Verabschiedung", a.ceremony],
  ].filter(([, d]) => d).map(([label, date, key]) => ({ label, date, at: parseDE(key || date) }));
}

function thesisChecklist(t, progress) {
  const tr = t.transfer || {}, req = progress?.thesis_requirements || {};
  const mods = req.modules_until_4th_missing || [];
  const item = (ok, text, sub, href) => h("li", {}, h(href ? "a" : "div", { class: "row", ...(href ? { href } : {}) },
    h("span", { class: `chip ${ok === true ? "ok" : ok === false ? "bad" : ""}`, text: ok === true ? "erfüllt" : ok === false ? "fehlt" : "selbst prüfen" }),
    h("span", { class: "t" }, text, sub && h("span", { class: "s", text: sub }))));
  return h("ul", { class: "rows" },
    item(tr.t1_5_passed >= 5, "Transferleistungen 1–5 bestanden", `${tr.t1_5_passed ?? "?"} von 5`, "#/transferleistungen"),
    item(progress ? mods.length === 0 : undefined, "Alle Modulprüfungen bis einschließlich 4. Semester bestanden", mods.length ? `offen: ${mods.join(", ")}` : "", "#/noten"),
    item(undefined, "Thema mit der gutachtenden Person abgestimmt", "Nach der Anmeldung lässt sich der Titel nicht mehr ändern."),
    item(tr.t6 === "bestanden" ? true : undefined, "Transferleistung 6 spätestens mit der Abgabe hochladen", tr.t6 === "bestanden" ? "bestanden" : "kann auch nach der Anmeldung kommen", "#/transferleistungen"));
}

function thesisCalc(t) {
  const a = t.planning_aid || {};
  const mode = h("select", { "aria-label": "Datum ist" }, h("option", { value: "registration", text: "Anmeldung am" }), h("option", { value: "start", text: "Beginn am" }));
  const date = h("input", { type: "date", value: isoOf(a.latest_register_to) || new Date().toISOString().slice(0, 10) });
  const out = h("div", { class: "thesis-result", "aria-live": "polite" });
  const run = async () => {
    if (!date.value) return;
    out.replaceChildren(...skeleton());
    try {
      const r = await api("nak_thesis_calc", { [mode.value]: date.value }, { wait: true });
      const start = parseDE(r.start), earliest = parseDE(a.earliest_start);
      const warn = [];
      if (earliest && start < earliest) warn.push(`Vor dem frühesten Start (${a.earliest_start}): so früh gibt das Prüfungsamt kein Thema aus.`);
      const s7f = parseDE(a.semester7_from), s7t = parseDE(a.semester7_to), sub = parseDE(r.submission);
      if (s7f && start <= s7t && sub >= s7f) warn.push(`Die Bearbeitungszeit läuft durch das 7. Theoriesemester (${a.semester7_from} – ${a.semester7_to}).`);
      const rf = parseDE(a.repeat_exams_from), rt = parseDE(a.repeat_exams_to);
      if (rf && start <= rt && sub >= rf) warn.push(`Die Wiederholungsklausuren (${a.repeat_exams_from} – ${a.repeat_exams_to}) fallen in die Bearbeitungszeit.`);
      if (r.buffer_days != null && r.graduation && r.buffer_days < 7) warn.push(`Nur ${r.buffer_days} Tage Puffer bis zu den letzten Noten; das Prüfungsamt rät zu mehr (Krankheit).`);
      out.replaceChildren(h("dl", { class: "kv" },
        h("dt", { text: "Beginn" }), h("dd", { text: r.start }),
        h("dt", { text: "Abgabe" }), h("dd", {}, h("b", { text: r.submission }), " bis 23:59"),
        h("dt", { text: "Mit voller Verlängerung" }), h("dd", { text: r.with_extension }),
        h("dt", { text: "Gutachten bis" }), h("dd", { text: r.review_until }),
        h("dt", { text: "Abschluss" }), h("dd", { text: r.graduation ? `${r.graduation.name}: letzte Noten ${r.graduation.last_grades}, Prüfungsausschuss ${r.graduation.board}${r.graduation.ceremony ? " · " + r.graduation.ceremony : ""}` : "kein Abschlusstermin bekannt" }),
        r.graduation && h("dt", { text: "Puffer" }), r.graduation && h("dd", { text: `${r.buffer_days} Tage bis zu den letzten Noten` }),
        r.graduation_with_extension && r.graduation_with_extension.name !== r.graduation?.name && h("dt", { text: "Mit Verlängerung" }),
        r.graduation_with_extension && r.graduation_with_extension.name !== r.graduation?.name && h("dd", { class: "bad-text", text: `rutscht der Abschluss auf ${r.graduation_with_extension.name}` })),
        ...warn.map((w) => h("p", { class: "err", text: w })));
    } catch (err) {
      out.replaceChildren(errorBox(err));
    }
  };
  const preset = (label, m, de) => de && h("button", { class: "ghost", type: "button", text: label, onclick: () => { mode.value = m; date.value = isoOf(de); run(); } });
  mode.addEventListener("change", run);
  date.addEventListener("change", run);
  run();
  return [h("div", { class: "settings-form thesis-form" }, h("div", { class: "pair" }, h("label", {}, "Datum ist", mode), h("label", {}, "Datum", date))),
    h("div", { class: "row-actions" }, preset("frühestmöglich", "registration", a.earliest_registration), preset("spätestmöglich", "registration", a.latest_register_to),
      h("button", { class: "ghost", type: "button", text: "heute anmelden", onclick: () => { mode.value = "registration"; date.value = new Date().toISOString().slice(0, 10); run(); } })),
    out];
}

function thesisReviewers() {
  const q = h("input", { type: "search", placeholder: "Thema, z. B. Datenbank, Cloud, Controlling", "aria-label": "Fachgebiet suchen" });
  const free = h("input", { type: "checkbox" });
  const list = h("div", {});
  let all = null, timer;
  const draw = () => {
    const words = q.value.toLowerCase().split(/\s+/).filter(Boolean);
    const rs = (all || []).filter((r) => (!free.checked || r.load === 0) && words.every((w) => `${r.name} ${r.department} ${(r.areas || []).join(" ")}`.toLowerCase().includes(w)));
    list.replaceChildren(rs.length ? h("ul", { class: "rows" }, rs.slice(0, 40).map((r, j) => h("li", {}, h("div", { class: "row", vars: { "--j": Math.min(j, 12) } },
      h("span", { class: "t" }, r.email ? h("a", { href: `mailto:${r.email}`, text: r.name }) : r.name, h("span", { class: "s", text: [r.department, (r.areas || []).join(", ")].filter(Boolean).join(" · ") })),
      h("span", { class: `chip ${r.load === 0 ? "ok" : r.load === 2 ? "bad" : r.load === 1 ? "due" : ""}`, text: r.load_label || "–" }))))) : emptyRow("Niemand passt dazu."),
      ...(rs.length > 40 ? [h("p", { class: "empty", text: `${rs.length - 40} weitere, Suche verfeinern.` })] : []));
  };
  q.addEventListener("input", () => { clearTimeout(timer); timer = setTimeout(draw, 120); });
  free.addEventListener("change", draw);
  const box = h("div", {}, h("div", { class: "settings-form wide" }, h("label", {}, "Fachgebiet", q), h("label", { class: "check" }, free, "nur mit freier Kapazität")), list);
  api("nak_thesis_reviewers").then((rs) => { all = rs || []; draw(); }).catch((err) => list.replaceChildren(errorBox(err)));
  list.append(...skeleton());
  return box;
}

// Betreuungs-Assistent: a topic → fitting reviewers, or a topic + reviewer →
// how well they fit. Matched on the server (nak_thesis_match), no external AI;
// with NAK_EMBED_URL also by meaning via a local model.
function matchCard(m, j, { compact = false } = {}) {
  const r = m.reviewer;
  return h("li", {}, h("div", { class: "row match", vars: { "--j": j } },
    h("span", { class: "match-pct", vars: { "--p": m.percent }, text: `${m.percent} %` }),
    h("span", { class: "t" }, r.email ? h("a", { href: `mailto:${r.email}`, text: r.name }) : r.name,
      h("span", { class: "s", text: (m.matched || []).map((x) => `${x.area} ← ${x.terms.join(", ")}`).join(" · ") || (r.areas || []).join(", ") }),
      !compact && (m.uncovered || []).length > 0 && h("span", { class: "s", text: `nicht abgedeckt: ${m.uncovered.join(", ")}` })),
    h("span", { class: `chip ${r.load === 0 ? "ok" : r.load === 2 ? "bad" : r.load === 1 ? "due" : ""}`, text: r.load_label || "–" })));
}

function thesisAssistant() {
  let mode = "suggest";
  const seg = h("div", { class: "filters", role: "group", "aria-label": "Assistent" });
  const topic = h("textarea", { rows: 2, placeholder: "Arbeitstitel oder Thema, z. B. „Datenqualität im Data Warehouse eines Versandhändlers“", "aria-label": "Thema" });
  const who = h("select", { "aria-label": "Gutachtende Person" }, h("option", { value: "", text: "Gutachtende Person wählen …" }));
  const whoRow = h("label", { hidden: true }, "Gutachtende Person", who);
  const go = h("button", { class: "primary", type: "button", text: "Vorschläge" });
  const out = h("div", { "aria-live": "polite" });
  const setMode = (m) => {
    mode = m;
    whoRow.hidden = m !== "check";
    go.textContent = m === "check" ? "Match prüfen" : "Vorschläge";
    seg.querySelectorAll("button").forEach((b) => b.setAttribute("aria-pressed", String(b.dataset.m === m)));
    out.replaceChildren();
  };
  for (const [m, label] of [["suggest", "Vorschlag finden"], ["check", "Passt das?"]]) {
    const b = h("button", { type: "button", text: label, onclick: () => setMode(m) });
    b.dataset.m = m;
    seg.append(b);
  }
  api("nak_thesis_reviewers").then((rs) => {
    for (const r of [...(rs || [])].sort((a, b) => a.name.localeCompare(b.name, "de"))) who.append(h("option", { value: r.name, text: `${r.name}${r.department ? " · " + r.department : ""}` }));
  }).catch(() => {});
  const run = async () => {
    const t = topic.value.trim();
    if (!t) { out.replaceChildren(h("p", { class: "empty", text: "Erst ein Thema eingeben." })); return; }
    if (mode === "check" && !who.value) { out.replaceChildren(h("p", { class: "empty", text: "Und eine gutachtende Person wählen." })); return; }
    out.replaceChildren(...skeleton());
    try {
      const r = await api("nak_thesis_match", mode === "check" ? { topic: t, reviewer: who.value } : { topic: t }, { wait: true });
      if (mode === "suggest") {
        const list = r.suggestions || [];
        out.replaceChildren(list.length ? h("ul", { class: "rows" }, list.map((m, j) => matchCard(m, j))) : emptyRow("Niemand passt erkennbar. Anders formulieren oder Fachbegriffe ergänzen."));
      } else {
        const m = r.match;
        out.replaceChildren(h("ul", { class: "rows" }, matchCard(m, 0)),
          m.percent >= 60 ? h("p", { class: "tl-alert ok" }, h("b", { text: "Passt gut." }))
            : m.percent >= 35 ? h("p", { class: "tl-alert" }, h("b", { text: "Passt teilweise." }))
            : h("p", { class: "tl-alert bad" }, h("b", { text: "Passt kaum." })),
          ...((r.better || []).length ? [h("h2", { class: "sub", text: "Würde besser passen" }), h("ul", { class: "rows" }, r.better.map((x, j) => matchCard(x, j, { compact: true })))] : []));
      }
    } catch (err) {
      out.replaceChildren(errorBox(err));
    }
  };
  go.addEventListener("click", run);
  topic.addEventListener("keydown", (e) => { if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) run(); });
  setMode("suggest");
  return h("div", {}, seg, h("div", { class: "settings-form wide" }, h("label", {}, "Thema", topic), whoRow, h("div", { class: "row-actions" }, go)), out);
}

async function thesisPage(root) {
  root.append(h("div", { class: "page-head" }, h("h1", {}, "Bachelorthesis", h("small", { text: "Anmeldung, Termine, Rechner und Gutachtende" }))));
  const grid = h("div", { class: "grid" });
  root.append(grid);
  const tStatus = tile("Stand", { cls: "w6", i: 0 });
  const tPlan = tile("Dein Fahrplan", { cls: "w6", i: 1 });
  const tCalc = tile("Rechner", { cls: "w6", i: 2 });
  const tDates = tile("Abschlusstermine", { cls: "w6", i: 3 });
  const tAssist = tile("Betreuungs-Assistent", { cls: "w12", i: 4 });
  const tRev = tile("Gutachtende finden", { cls: "w12", i: 4 });
  grid.append(tStatus, tPlan, tCalc, tDates, tAssist, tRev);
  const data = api("nak_thesis");
  fill(tStatus, () => Promise.all([data, api("cis_progress").catch(() => null)]), ([t, p]) => {
    const e = t.eligibility || {};
    return [h("p", { class: `tl-alert ${e.open ? "ok" : "bad"}` }, h("b", { text: e.open ? "Du kannst die Thesis anmelden." : "Anmeldung noch nicht möglich" }), e.open ? " (CIS → Bachelorthesis → Anmeldung)" : ""),
      ...(e.reasons || []).map((r) => h("p", { class: "empty", text: `CIS: ${r}` })), thesisChecklist(t, p)];
  });
  fill(tPlan, () => data, (t) => {
    const a = t.planning_aid || {};
    if (!a.found) return emptyRow("Noch keine Planungshilfe für deinen Jahrgang.");
    const today = startOfDay(new Date());
    const steps = thesisSteps(a);
    const next = steps.find((x) => x.at && x.at >= today);
    return [h("p", { class: "empty", text: `Planungshilfe Jahrgang ${a.cohort} aus dem CIS` }),
      h("ul", { class: "rows" }, steps.map((x, j) => h("li", {}, h("div", { class: `row ${x.at && x.at < today ? "past" : ""}`, vars: { "--j": j } },
        h("span", { class: "dot", "data-src": "cis" }), h("span", { class: "t" }, x.label, h("span", { class: "s", text: x.date })),
        x === next ? h("span", { class: "chip due", text: relDay(dayDiff(x.at)) }) : x.at && x.at < today ? h("span", { class: "chip", text: "vorbei" }) : null))))];
  });
  fill(tCalc, () => data, thesisCalc);
  fill(tDates, () => data, (t) => {
    // reachable first; the unreachable ones only say why
    const list = [...(t.latest || [])].sort((x, y) => (y.reachable === true) - (x.reachable === true));
    if (!list.length) return emptyRow("Keine kommenden Abschlusstermine im CIS.");
    return h("ul", { class: "rows" }, list.map(({ latest: l, reachable }, j) => h("li", {}, h("div", { class: `row ${reachable ? "" : "past"}`, vars: { "--j": j } },
      h("span", { class: "t" }, `Abschluss ${l.graduation.name}`, h("span", { class: "s", text: reachable
        ? `spätestens anmelden in KW ${l.kw} (${l.register_week_from} – ${l.register_week_to}) · Start ${l.start} · Abgabe ${l.submission} · letzte TL beginnen bis ${l.tl_start_by}`
        : "nicht erreichbar" })),
      h("span", { class: `chip ${reachable ? "" : "bad"}`, text: l.graduation.ceremony ? l.graduation.ceremony.replace("Bachelor-Graduierung: ", "Feier ") : `Ende ${l.graduation.board}` })))));
  });
  tAssist.append(thesisAssistant());
  tRev.append(thesisReviewers());
}

function certsTab(grid) {
  const tCert = tile("Studienbescheinigungen", { cls: "w8", i: 0 });
  const tTr = tile("Notenspiegel", { i: 1 },
    h("p", { class: "empty", text: "Offizieller Notenspiegel aus dem CIS als PDF." }),
    h("div", { class: "row-actions" },
      h("button", { class: "primary", type: "button", text: "Deutsch", onclick: (e) => openLocal("cis_transcript", { lang: "de" }, e.currentTarget) }),
      h("button", { class: "ghost", type: "button", text: "English", onclick: (e) => openLocal("cis_transcript", { lang: "en" }, e.currentTarget) })));
  grid.append(tCert, tTr);
  fill(tCert, () => api("cis_list_certs"), (list) => {
    if (!list?.length) return emptyRow("Keine Bescheinigungen.");
    return h("ul", { class: "rows files" }, list.map((c, j) => h("li", {}, h("button", { class: "row file", type: "button", vars: { "--j": Math.min(j, 12) }, onclick: (e) => openLocal("cis_download_cert", { download_url: c.download_url }, e.currentTarget) },
      h("span", { class: "ext", text: (c.lang || "de").toUpperCase() }),
      h("span", { class: "t" }, c.name || c.semester, h("span", { class: "s", text: c.period || "" })),
      h("span", { class: "chip", text: "öffnen" })))));
  });
}

function profileTab(grid) {
  const t = tile("Meine Daten", { cls: "w8", i: 0 });
  const tSide = tile("Konto", { i: 1 });
  const tContact = tile("Kontakt ändern", { cls: "w6", i: 2 });
  const tAddr = tile("Adresse ändern", { cls: "w6", i: 3 });
  const tShare = tile("Freigaben ändern", { cls: "w12", i: 4 });
  grid.append(t, tSide, tContact, tAddr, tShare);
  fill(t, () => api("cis_profile"), (p) => h("div", { class: "sens" }, genericView(p.all || p)));
  fill(tSide, () => api("cis_balance"), (bal) => genericView(bal));
  fill(tContact, () => api("cis_contact"), contactForm);
  fill(tAddr, () => api("cis_address"), addressForm);
  fill(tShare, () => api("cis_sharing"), sharingForm);
}

// The profile forms send only what was changed, behind the usual preview and
// confirmation; nothing reaches the CIS before "Ja, ändern".
function field(label, input) {
  return h("label", {}, label, input);
}

function changes(pairs) {
  const out = {};
  for (const [key, el, old] of pairs) {
    const v = el.type === "checkbox" ? el.checked : el.value.trim();
    if (v !== (el.type === "checkbox" ? Boolean(old) : String(old ?? "").trim())) out[key] = v;
  }
  return out;
}

function noChange(form) {
  form.querySelector(".form-msg")?.replaceChildren(h("p", { class: "empty", text: "Nichts geändert." }));
  return null;
}

function contactForm(c) {
  const email = h("input", { type: "email", value: c.email_privat || "", autocomplete: "email", spellcheck: "false" });
  const mobile = h("input", { type: "tel", value: c.telefon_mobil || "", placeholder: "0170-1234567", autocomplete: "tel" });
  const phone = h("input", { type: "tel", value: c.telefon_festnetz || "", placeholder: "040-123456" });
  const fax = h("input", { type: "tel", value: c.fax || "" });
  const infopost = h("input", { type: "checkbox", ...(c.infopost ? { checked: true } : {}) });
  const form = h("form", { class: "settings-form sens", onsubmit: (e) => e.preventDefault() },
    field("Private E-Mail", email), field("Mobil", mobile), field("Festnetz", phone), field("Fax", fax),
    h("label", { class: "check" }, infopost, "Infopost nach dem Studium"),
    h("p", { class: "empty", text: "Telefon als Vorwahl-Rufnummer; ein leeres Feld löscht die Nummer im CIS." }),
    h("div", { class: "form-msg", "aria-live": "polite" }));
  form.append(confirmFlow({
    tool: "cis_update_contact", label: "Änderungen prüfen", confirm: "Ja, ändern", cls: "primary", done: refreshPage,
    args: () => {
      const d = changes([["email", email, c.email_privat], ["mobile", mobile, c.telefon_mobil], ["phone", phone, c.telefon_festnetz], ["fax", fax, c.fax], ["infopost", infopost, c.infopost]]);
      return Object.keys(d).length ? d : noChange(form);
    },
  }));
  return form;
}

function addressForm(a) {
  const types = a.available_types || {};
  const type = h("select", {}, Object.entries(types).map(([id, name]) => h("option", { value: id, text: name, ...(id === a.type_id ? { selected: true } : {}) })));
  const box = h("div", {});
  const draw = (cur) => {
    const zusatz = h("input", { value: cur.adresszusatz || "" });
    const strasse = h("input", { value: cur.strasse || "", autocomplete: "street-address" });
    const plz = h("input", { value: cur.plz || "", inputmode: "numeric", autocomplete: "postal-code", class: "short-in" });
    const ort = h("input", { value: cur.ort || "", autocomplete: "address-level2" });
    const form = h("form", { class: "settings-form", onsubmit: (e) => e.preventDefault() },
      field("Straße und Nr.", strasse), field("Zusatz", zusatz), h("div", { class: "pair" }, field("PLZ", plz), field("Ort", ort)),
      cur.letter_form && h("p", { class: "empty", text: `So steht es in Briefen: ${cur.letter_form}` }),
      h("div", { class: "form-msg", "aria-live": "polite" }));
    form.append(confirmFlow({
      tool: "cis_update_address", label: "Änderungen prüfen", confirm: "Ja, ändern", cls: "primary", done: refreshPage,
      args: () => {
        const d = changes([["zusatz", zusatz, cur.adresszusatz], ["strasse", strasse, cur.strasse], ["plz", plz, cur.plz], ["ort", ort, cur.ort]]);
        return Object.keys(d).length ? { type: cur.type_id || type.value, ...d } : noChange(form);
      },
    }));
    // the main address cannot be removed, others (e.g. Semesteradresse) can
    if (cur.strasse && (cur.type_id || type.value) !== Object.keys(types)[0]) {
      form.append(confirmFlow({ tool: "cis_update_address", args: { type: cur.type_id || type.value, delete: true }, label: "Diese Adresse löschen", confirm: "Ja, löschen", done: refreshPage }));
    }
    box.replaceChildren(h("div", { class: "sens" }, form));
  };
  type.addEventListener("change", async () => {
    box.replaceChildren(...skeleton());
    try { draw(await api("cis_address", { type: type.value })); } catch (err) { box.replaceChildren(errorBox(err)); }
  });
  draw(a);
  return [Object.keys(types).length > 1 && h("div", { class: "settings-form" }, field("Adresstyp", type)), box];
}

function sharingForm(sh) {
  const b = sh.betrieb || {};
  const levels = sh.levels || { 0: "nicht anzeigen", 1: "nur meine Zenturie", 2: "alle Zenturien" };
  const noten = h("input", { type: "checkbox", ...(b.noten_fuer_betrieb_freigegeben ? { checked: true } : {}) });
  const anm = h("input", { type: "checkbox", ...(b.pruefungsanmeldungen_fuer_betrieb_freigegeben ? { checked: true } : {}) });
  const order = ["Name", "Firma", "Geburtsdatum", "Adresse", "Fest", "Mobil", "Mail"]; // as in the CIS
  const cur = Object.fromEntries(Object.entries(sh.kommilitonen || {}).sort(([a], [b]) => order.indexOf(a) - order.indexOf(b)).map(([k, v]) => [k, String(v).trim().charAt(0)]));
  const selects = Object.entries(cur).map(([k, v]) => [k, h("select", { "aria-label": k }, Object.entries(levels).map(([id, label]) => h("option", { value: id, text: label, ...(id === v ? { selected: true } : {}) })))]);
  const label = { Fest: "Festnetz", Mail: "E-Mail" };
  const form = h("form", { class: "settings-form", onsubmit: (e) => e.preventDefault() },
    h("h2", { class: "sub", text: "Ausbildungsbetrieb" }),
    h("label", { class: "check" }, noten, "Noten für den Betrieb freigeben"),
    h("label", { class: "check" }, anm, "Prüfungs- und Transferanmeldungen für den Betrieb freigeben"),
    selects.length > 0 && h("h2", { class: "sub", text: "Für Kommiliton:innen sichtbar" }),
    selects.length > 0 && h("div", { class: "share-grid" }, selects.map(([k, sel]) => field(label[k] || k, sel))),
    h("div", { class: "form-msg", "aria-live": "polite" }));
  form.append(confirmFlow({
    tool: "cis_set_sharing", label: "Änderungen prüfen", confirm: "Ja, ändern", cls: "primary", done: refreshPage,
    args: () => {
      const d = changes([["noten_betrieb", noten, b.noten_fuer_betrieb_freigegeben], ["anmeldungen_betrieb", anm, b.pruefungsanmeldungen_fuer_betrieb_freigegeben]]);
      const k = Object.fromEntries(selects.filter(([key, sel]) => sel.value !== cur[key]).map(([key, sel]) => [key, sel.value]));
      if (Object.keys(k).length) d.kommilitonen = k;
      return Object.keys(d).length ? d : noChange(form);
    },
  }));
  return form;
}

// ── header, theme, routing ──────────────────────────────────────────────────

const routes = [
  [/^#?\/?$/, overview, "#/"],
  [/^#\/woche$/, week, "#/woche"],
  [/^#\/kurse$/, coursesPage, "#/kurse"],
  [/^#\/kurs\/(\d+)(?:\/(forum|abgaben|info))?$/, coursePage, "#/kurse"],
  [/^#\/abgaben$/, allAssignmentsPage, "#/kurse"],
  [/^#\/abgabe\/(\d+)$/, assignmentPage, "#/kurse"],
  [/^#\/diskussion\/(\d+)$/, discussionPage, "#/kurse"],
  [/^#\/neu$/, newsPage, "#/nachrichten"],
  [/^#\/nachrichten(?:\/(\d+))?$/, messagesPage, "#/nachrichten"],
  [/^#\/noten$/, gradesPage, "#/noten"],
  [/^#\/pruefungen$/, examsPage, "#/noten"],
  [/^#\/(?:transferleistungen|transfernoten)$/, transferPage, "#/transferleistungen"],
  [/^#\/(?:bachelorthesis|thesis)$/, thesisPage, "#/bachelorthesis"],
  [/^#\/studium(?:\/(seminare|wahlpflicht|transfer|bescheinigungen|profil))?$/, studiesPage, "#/studium"],
  [/^#\/mensa$/, mensaPage, "#/mensa"],
  [/^#\/einstellungen(?:\/(darstellung|benachrichtigungen|verbindungen|konto))?$/, settingsPage, "#/einstellungen"],
  [/^#\/module$/, unitsPage, "#/kurse"],
  [/^#\/modul\/([A-Z]{1,2}\d{3})(?:\/(inhalt|forum|abgaben))?(?:\/(\d+))?$/, unitPage, "#/kurse"],
  [/^#\/module\/([A-Za-z]{1,2}\d{3})$/, (root, nr) => { location.replace(`#/modul/${nr.toUpperCase()}`); }, "#/kurse"],
];

function stamp() {
  const bar = $(".offline-bar");
  if (bar && offline) bar.textContent = newest ? `Offline · du siehst den Stand von ${fmtStamp(newest)}` : "Offline · keine Verbindung zu naknak";
  const el = $(".stamp");
  if (!el) return;
  el.classList.toggle("offline", offline);
  if (offline) { el.textContent = newest ? `offline · Stand ${fmtTime.format(newest)}` : "offline"; return; }
  if (!newest) return;
  const mins = Math.round((Date.now() - newest) / 6e4);
  el.textContent = mins < 1 ? "Stand: gerade eben" : `Stand: vor ${mins} min`;
}
setInterval(stamp, 30_000);

// Privacy mode (like banking apps): blurs grades, averages, credits and
// messages; a click on one value shows just that one.
function applyPrivate(on) {
  document.documentElement.classList.toggle("private", on);
  const b = $(".eye-btn");
  if (b) {
    b.replaceChildren(svg(on ? icons.eyeOff : icons.eye), h("span", { class: "label", text: on ? "Daten einblenden" : "Daten ausblenden" }));
    b.title = on ? "Sensible Daten einblenden" : "Sensible Daten ausblenden (Noten, Nachrichten …)";
    b.setAttribute("aria-pressed", on ? "true" : "false");
  }
}

function togglePrivate() {
  const on = !document.documentElement.classList.contains("private");
  localStorage.setItem("nak-private", on ? "1" : "");
  document.querySelectorAll(".sens.peek").forEach((el) => el.classList.remove("peek"));
  applyPrivate(on);
}

document.addEventListener("click", (e) => {
  if (!document.documentElement.classList.contains("private")) return;
  const el = e.target.closest?.(".sens");
  if (!el || el.classList.contains("peek")) return;
  // first click reveals, it does not follow the link around it
  e.preventDefault();
  e.stopPropagation();
  el.classList.add("peek");
}, true);

const themes = ["system", "light", "dark"];
const themeLabel = { system: "Design: wie System", light: "Design: hell", dark: "Design: dunkel" };

function applyTheme(t) {
  if (t === "system") delete document.documentElement.dataset.theme;
  else document.documentElement.dataset.theme = t;
  const btn = $(".theme-btn");
  if (btn) {
    btn.replaceChildren(svg(icons[t]), h("span", { class: "label", text: themeLabel[t] }));
    btn.title = themeLabel[t];
  }
}

function switchTheme(e) {
  const cur = localStorage.getItem("nak-theme") || "system";
  const next = themes[(themes.indexOf(cur) + 1) % themes.length];
  localStorage.setItem("nak-theme", next);
  if (!document.startViewTransition || reduced.matches) return applyTheme(next);
  const r = e.currentTarget.getBoundingClientRect();
  const x = r.left + r.width / 2, y = r.top + r.height / 2;
  const root = document.documentElement;
  root.style.setProperty("--tx", `${x}px`);
  root.style.setProperty("--ty", `${y}px`);
  root.style.setProperty("--tr", `${Math.hypot(Math.max(x, innerWidth - x), Math.max(y, innerHeight - y))}px`);
  root.classList.add("theme-switch");
  document.startViewTransition(() => applyTheme(next)).finished.finally(() => root.classList.remove("theme-switch"));
}

// every page the navigation bar can hold: [label, short label for the phone
// tab bar, route, icon]; which ones and their order is a setting
const navDefs = {
  start: ["Übersicht", "Start", "#/", ["M3 11l9-7 9 7", "M5 10v10h14V10", "M10 20v-6h4v6"]],
  woche: ["Woche", "Woche", "#/woche", ["M4 6h16v14H4z", "M4 10h16", "M9 3v4", "M15 3v4"]],
  kurse: ["Kurse", "Kurse", "#/kurse", ["M4 5a2 2 0 0 1 2-2h13v16H6a2 2 0 0 0-2 2z", "M4 19V5", "M8 7h7"]],
  inbox: ["Inbox", "Inbox", "#/nachrichten", ["M4 5h16v11H8l-4 4z"]],
  noten: ["Noten", "Noten", "#/noten", ["M12 3l2.6 5.3 5.9.9-4.3 4.1 1 5.8L12 16.4 6.8 19.1l1-5.8L3.5 9.2l5.9-.9z"]],
  studium: ["Studium", "Studium", "#/studium", ["M2 9l10-5 10 5-10 5z", "M6 11v5c3 2 9 2 12 0v-5", "M22 9v6"]],
  pruefungen: ["Prüfungen", "Prüfung", "#/pruefungen", ["M6 3h9l4 4v14H6z", "M14 3v5h5", "M9 13l2 2 4-4"]],
  abgaben: ["Abgaben", "Abgaben", "#/abgaben", ["M12 4v11", "M7 10l5 5 5-5", "M5 20h14"]],
  transfer: ["Transferleistungen", "Transfer", "#/transferleistungen", ["M4 19h16", "M7 15l3-4 3 2 4-6"]],
  thesis: ["Bachelorthesis", "Thesis", "#/bachelorthesis", ["M6 3h9l4 4v14H6z", "M14 3v5h5", "M9 13h6", "M9 17h4"]],
  mensa: ["Mensa", "Mensa", "#/mensa", ["M7 3v18", "M4 3v5a3 3 0 0 0 6 0V3", "M17 21V3c-2 1-3 4-3 8h3"]],
};
const defaultNav = ["start", "woche", "kurse", "inbox", "noten", "studium"];

// dashboard tiles for the settings editor: [label, -, -, icon]
const homeDefs = {
  next: ["Als Nächstes", "", "", ["M12 7v5l3 2", "M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18z"]],
  deadlines: ["Fristen", "", "", ["M5 4h14v16H5z", "M9 9h6", "M9 13h6"]],
  week: ["Diese Woche", "", "", navDefs.woche[3]],
  grades: ["Noten", "", "", navDefs.noten[3]],
  exams: ["Prüfungen", "", "", navDefs.pruefungen[3]],
  moodle: ["Moodle · aktuelle Kurse", "", "", navDefs.kurse[3]],
  messages: ["Nachrichten", "", "", navDefs.inbox[3]],
  pending: ["Noten ausstehend", "", "", ["M12 7v5l3 2", "M5 20h14"]],
  transfer: ["Transferleistungen", "", "", ["M5 4h14v16H5z", "M9 9h6", "M9 13h6"]],
  transfer_grades: ["TL-Noten (rechnerisch)", "", "", ["M4 19h16", "M7 15l3-4 3 2 4-6"]],
  thesis: ["Bachelorthesis", "", "", ["M6 3h9l4 4v14H6z", "M14 3v5h5", "M9 13h6", "M9 17h4"]],
  mensa: ["Mensa · heute und Guthaben", "", "", navDefs.mensa[3]],
  mensa_spending: ["Mensa-Ausgaben", "", "", ["M4 19h16", "M7 15l3-4 3 2 4-6"]],
};
const defaultHome = ["next", "deadlines", "week", "grades", "exams", "mensa", "moodle"];

function savedHome() {
  try {
    const n = JSON.parse(localStorage.getItem("nak-home") || "null");
    if (Array.isArray(n) && n.length >= 1 && n.every((id) => homeDefs[id])) return n;
  } catch {}
  return defaultHome;
}

function applyHome(ids) {
  if (!Array.isArray(ids) || !ids.every((id) => homeDefs[id])) return;
  const changed = JSON.stringify(ids) !== localStorage.getItem("nak-home");
  localStorage.setItem("nak-home", JSON.stringify(ids));
  if (changed && (location.hash || "#/") === "#/") render();
}

function savedNav() {
  try {
    const n = JSON.parse(localStorage.getItem("nak-nav") || "null");
    if (Array.isArray(n) && n.length >= 2 && n.every((id) => navDefs[id])) return n;
  } catch {}
  return defaultNav;
}

// where the search sits and how the dashboard greets (Einstellungen →
// Darstellung); kept on the server, localStorage for the first paint
function savedUI() {
  try {
    const v = JSON.parse(localStorage.getItem("nak-ui") || "null");
    if (v && typeof v === "object") return { search: v.search || "home", greeting_off: !!v.greeting_off, greeting_text: v.greeting_text || "" };
  } catch {}
  return { search: "home", greeting_off: false, greeting_text: "" };
}

function applyUI(ui) {
  if (!ui || typeof ui !== "object") return;
  const before = localStorage.getItem("nak-ui");
  localStorage.setItem("nak-ui", JSON.stringify(ui));
  if (before === JSON.stringify(ui)) return;
  $(".top nav")?.replaceWith(navBar());
  $(".top .search-top")?.classList.toggle("top-off", ui.search !== "top");
  markNav();
  if ((location.hash || "#/") === "#/") render();
}

const searchIcon = ["M11 18a7 7 0 1 0 0-14 7 7 0 0 0 0 14z", "M20 20l-4-4"];

function openSearch() {
  if ($("dialog.search-dlg")?.open) return;
  const box = searchBox();
  const dlg = h("dialog", { class: "search-dlg", "aria-label": "Suche" }, box);
  document.body.append(dlg);
  dlg.showModal();
  const close = () => dlg.open && dlg.close();
  dlg.addEventListener("close", () => { dlg.remove(); removeEventListener("hashchange", close); });
  dlg.addEventListener("click", (e) => { if (e.target === dlg) close(); });
  addEventListener("hashchange", close);
  box.querySelector("input")?.focus();
}

function navBar(ids = savedNav()) {
  const links = ids.map((id) => {
    const [t, short, href, icon] = navDefs[id];
    return h("a", { href, title: t }, svg(icon), h("span", { class: "full", text: t }), h("span", { class: "short", text: short }));
  });
  if (savedUI().search === "nav") links.push(h("a", { href: "#", role: "button", class: "nav-search", title: "Suchen", onclick: (e) => { e.preventDefault(); openSearch(); } }, svg(searchIcon), h("span", { class: "full", text: "Suchen" }), h("span", { class: "short", text: "Suche" })));
  return h("nav", { "aria-label": "Bereiche" }, links);
}

// the server keeps the setting (same bar in browser and app); localStorage
// only makes the first paint right
function applyNav(ids) {
  if (!Array.isArray(ids) || !ids.every((id) => navDefs[id])) return;
  if (JSON.stringify(ids) === localStorage.getItem("nak-nav") && $(".top nav")) return;
  localStorage.setItem("nak-nav", JSON.stringify(ids));
  $(".top nav")?.replaceWith(navBar(ids));
  markNav();
}

// top-bar elements that can be switched off (Einstellungen → Kopfleiste)
const topDefs = {
  stamp: "„Stand: vor … min“",
  refresh: "Neu laden",
  privacy: "Werte verbergen (Auge)",
  bell: "Benachrichtigungen",
  theme: "Hell/Dunkel",
  logout: "Abmelden",
};

function savedTopHidden() {
  try {
    const v = JSON.parse(localStorage.getItem("nak-top-hidden") || "[]");
    if (Array.isArray(v)) return v.filter((id) => topDefs[id]);
  } catch {}
  return [];
}

function applyTop(hidden) {
  if (!Array.isArray(hidden)) return;
  localStorage.setItem("nak-top-hidden", JSON.stringify(hidden));
  document.querySelectorAll(".top [data-top]").forEach((el) => el.classList.toggle("top-off", hidden.includes(el.dataset.top)));
}

function header() {
  const hidden = savedTopHidden();
  const tag = (id, el) => { el.dataset.top = id; el.classList.toggle("top-off", hidden.includes(id)); return el; };
  const refresh = h("button", { class: "icon-btn hide-sm", type: "button", title: "Neu laden (frisch aus CIS und Moodle)", "aria-label": "Neu laden" }, svg(icons.refresh));
  refresh.addEventListener("click", async () => {
    refresh.classList.add("spin");
    fresh = true;
    try { await render(); } finally { fresh = false; refresh.classList.remove("spin"); }
  });
  return h("header", { class: "top" }, h("div", { class: "top-in" },
    h("a", { class: "mark", href: "#/", "aria-label": "naknak – Übersicht" }, h("img", { src: "/assets/naknak.svg", alt: "", width: 26, height: 26 }), "naknak"),
    navBar(),
    h("div", { class: "tools" },
      tag("stamp", h("span", { class: "stamp", "aria-live": "polite" })),
      h("button", { class: `icon-btn search-top ${savedUI().search === "top" ? "" : "top-off"}`, type: "button", title: "Suchen (/)", "aria-label": "Suchen", onclick: openSearch }, svg(searchIcon)),
      tag("refresh", refresh),
      tag("privacy", h("button", { class: "icon-btn eye-btn", type: "button", onclick: togglePrivate })),
      tag("bell", bellButton()),
      h("a", { class: "icon-btn", href: "#/einstellungen", title: "Einstellungen", "aria-label": "Einstellungen" }, svg(icons.gear)),
      tag("theme", h("button", { class: "icon-btn theme-btn", type: "button", onclick: switchTheme })),
      tag("logout", h("form", { method: "post", action: "/logout", onsubmit: () => { navigator.serviceWorker?.controller?.postMessage("clear"); } }, h("button", { class: "icon-btn", type: "submit", title: "Abmelden", "aria-label": "Abmelden" }, svg(["M15 4h4v16h-4", "M10 8l-4 4 4 4", "M6 12h10"])))))));
}

let renderSeq = 0;

// the page's own entry if it is in the bar (Prüfungen, Abgaben), else the
// section it belongs to
function markNav(r = route()) {
  if (!r) return;
  const links = [...document.querySelectorAll(".top nav a")];
  const own = links.find((a) => a.getAttribute("href") === (location.hash || "#/"));
  const target = own || links.find((a) => a.getAttribute("href") === r.nav);
  for (const a of links) {
    if (a === target) a.setAttribute("aria-current", "page");
    else a.removeAttribute("aria-current");
  }
}

function route() {
  const hash = location.hash || "#/";
  const match = routes.find(([re]) => re.test(hash));
  if (!match) return null;
  const [re, page, nav] = match;
  const [, ...args] = hash.match(re);
  return { page, nav, args: args.map((a) => a && (/^[a-z]{1,2}\d{3}$/i.test(a) ? a.toUpperCase() : a)) };
}

async function render() {
  const seq = ++renderSeq;
  const r = route();
  if (!r) { location.hash = "#/"; return; }
  markNav(r);
  newest = null;
  staleSeen.clear();
  const main = h("main", { id: "main" });
  const swap = () => { $("#main").replaceWith(main); scrollTo(0, 0); };
  if (document.startViewTransition && !reduced.matches && seq > 1) await document.startViewTransition(swap).updateCallbackDone;
  else swap();
  await r.page(main, ...r.args);
  await settle();
  if (seq === renderSeq && staleSeen.size) revalidate(seq, r);
}

// revalidate asks the server for fresh versions of everything that came from
// stale cache and redraws the page in place — without animation — if any of
// it changed.
async function revalidate(seq, r) {
  const calls = [...staleSeen.values()];
  staleSeen.clear();
  const el = $(".stamp");
  if (el) el.textContent = "aktualisiere …";
  const results = await Promise.allSettled(calls.map((c) => api(c.tool, c.args, { wait: true })));
  const changed = results.some((x, i) => x.status === "fulfilled" && JSON.stringify(x.value) !== calls[i].text);
  if (seq !== renderSeq) return;
  if (!changed) { newest = new Date(); stamp(); return; }
  const next = h("main", { id: "main", class: "quiet" });
  const keepY = scrollY;
  newest = null;
  await r.page(next, ...r.args);
  await settle();
  if (seq !== renderSeq) return;
  $("#main").replaceWith(next);
  scrollTo(0, keepY);
}

function boot() {
  if ("serviceWorker" in navigator && !document.documentElement.classList.contains("still")) navigator.serviceWorker.register("/sw.js").catch(() => {});
  const app = $("#app");
  app.replaceWith(header(), h("main", { id: "main" }));
  applyTheme(new URLSearchParams(location.search).get("theme") || localStorage.getItem("nak-theme") || "system");
  applyPrivate(localStorage.getItem("nak-private") === "1");
  addEventListener("hashchange", render);
  render();
  if (!document.documentElement.classList.contains("still")) {
    fetch("/api/settings", { credentials: "same-origin" }).then((r) => r.ok ? r.json() : null).then((st) => { if (st) { applyUI(st.ui); applyNav(st.nav); applyHome(st.home); applyTop(st.top_hidden); } }).catch(() => {});
  }
}

boot();
