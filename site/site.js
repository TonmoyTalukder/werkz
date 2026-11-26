// werkz site: theme, tabs, copy buttons, menu, docs helpers.
(() => {
  const root = document.documentElement;

  // Theme: light by default; the toggle switches to dark and remembers the choice.
  document.querySelector(".theme-toggle")?.addEventListener("click", () => {
    const next = root.dataset.theme === "dark" ? "light" : "dark";
    root.dataset.theme = next;
    try { localStorage.setItem("werkz-theme", next); } catch {}
  });

  // Hero headline types itself in. Every letter is laid out from the start (just invisible), so
  // nothing shifts; screen readers get the whole heading; reduced motion shows it at once.
  const typed = document.querySelector("[data-type]");
  if (typed && !matchMedia("(prefers-reduced-motion: reduce)").matches) {
    const text = typed.textContent.trim();
    typed.setAttribute("aria-label", text);
    typed.textContent = "";
    const chars = [];
    text.split(" ").forEach((word, n) => {
      if (n) typed.append(" "); // spaces stay outside the words, so lines can break there
      const w = document.createElement("span");
      w.className = "tw-word";
      w.setAttribute("aria-hidden", "true");
      for (const ch of word) {
        const c = document.createElement("span");
        c.className = "tw-char";
        c.textContent = ch;
        // every typed letter lands a little differently: tilt, baseline and ink weight
        c.style.setProperty("--r", (Math.random() * 3 - 1.5).toFixed(2) + "deg");
        c.style.setProperty("--y", (Math.random() * 0.05 - 0.025).toFixed(3) + "em");
        c.style.setProperty("--ink", (0.84 + Math.random() * 0.16).toFixed(2));
        w.append(c);
        chars.push(c);
      }
      typed.append(w);
    });
    typed.classList.add("typing");
    let i = 0;
    const step = () => {
      if (i < chars.length) {
        chars[i - 1]?.classList.remove("cur");
        chars[i].classList.add("on", "cur");
        // a short pause after each word and a longer one at the comma, like real typing
        const c = chars[i++];
        const endOfWord = !c.nextSibling;
        setTimeout(step, c.textContent === "," ? 300 : endOfWord ? 110 : 45 + Math.random() * 45);
      } else {
        typed.classList.add("typed");
        setTimeout(() => typed.classList.add("caret-gone"), 2600);
      }
    };
    // start once the typewriter face has loaded, so letters never strike in a fallback font
    Promise.race([document.fonts?.load('1em "Special Elite"') ?? Promise.resolve(), new Promise((r) => setTimeout(r, 1500))])
      .then(() => setTimeout(step, 250));
  }

  // The hero window replays a recorded werkz session as live text: frames carry their real timing,
  // the last frame holds, then it loops. It only plays while visible; phones and reduced motion
  // keep the still image.
  const win = document.querySelector(".window");
  const term = win?.querySelector(".term");
  if (term && matchMedia("(min-width: 700px)").matches && !matchMedia("(prefers-reduced-motion: reduce)").matches) {
    fetch(term.dataset.src || "assets/session.json")
      .then((r) => (r.ok ? r.json() : Promise.reject(r.status)))
      .then((s) => {
        const pre = document.createElement("pre");
        const rows = Array.from({ length: s.rows }, () => pre.appendChild(document.createElement("div")));
        term.append(pre);
        const shown = new Array(s.rows).fill(-1);
        const draw = (frame) => frame[1].forEach((li, r) => {
          if (shown[r] !== li) { rows[r].innerHTML = s.lines[li] || " "; shown[r] = li; }
        });
        draw(s.frames[0]);
        win.classList.add("live");
        let i = 0, timer = null, visible = false;
        const next = () => {
          timer = null;
          if (!visible) return;
          i = (i + 1) % s.frames.length;
          draw(s.frames[i]);
          const wait = i === s.frames.length - 1 ? 5200 : i === 0 ? 1200 : s.frames[i + 1][0] - s.frames[i][0];
          timer = setTimeout(next, Math.max(wait, 30));
        };
        new IntersectionObserver(([en]) => {
          visible = en.isIntersecting;
          if (visible && !timer) timer = setTimeout(next, 900);
        }, { threshold: 0.25 }).observe(win);
      })
      .catch(() => {}); // the still image stays
  }

  // Tabs (WAI-ARIA: click, arrow keys, Home/End).
  for (const group of document.querySelectorAll("[data-tabs]")) {
    const tabs = [...group.querySelectorAll('[role="tab"]')];
    const select = (tab, focus) => {
      for (const t of tabs) {
        const on = t === tab;
        t.setAttribute("aria-selected", on);
        t.tabIndex = on ? 0 : -1;
        document.getElementById(t.getAttribute("aria-controls")).hidden = !on;
      }
      if (focus) tab.focus();
    };
    tabs.forEach((tab, i) => {
      tab.addEventListener("click", () => select(tab, false));
      tab.addEventListener("keydown", (e) => {
        const n = tabs.length;
        const next = { ArrowRight: i + 1, ArrowDown: i + 1, ArrowLeft: i - 1, ArrowUp: i - 1, Home: 0, End: n - 1 }[e.key];
        if (next === undefined) return;
        e.preventDefault();
        select(tabs[(next + n) % n], true);
      });
    });
  }

  // On phones the docs menu starts folded, so the page itself comes first.
  const docsMenu = document.querySelector(".docs-nav details");
  if (docsMenu && matchMedia("(max-width: 820px)").matches) docsMenu.open = false;

  // Docs code blocks get the same command-and-copy layout as the landing page.
  const copyButton = () => {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "copy";
    b.innerHTML = '<svg class="icon copy-icon" aria-hidden="true"><use href="#i-copy"/></svg>' +
      '<svg class="icon done-icon" aria-hidden="true"><use href="#i-check"/></svg><span>Copy</span>';
    return b;
  };
  for (const pre of document.querySelectorAll(".doc > pre")) {
    const wrap = document.createElement("div");
    wrap.className = "cmd";
    pre.replaceWith(wrap);
    wrap.append(pre, copyButton());
  }

  // Long commands wrap only after a "/" (never inside a word); copying is unaffected.
  for (const el of document.querySelectorAll(".cmd pre, .doc :not(pre) > code")) {
    const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
    const nodes = [];
    while (walker.nextNode()) nodes.push(walker.currentNode);
    for (const node of nodes) {
      if (!node.data.includes("/")) continue;
      const frag = document.createDocumentFragment();
      node.data.split(/(?<=\/)/).forEach((part, i) => {
        if (i) frag.append(document.createElement("wbr"));
        frag.append(part);
      });
      node.replaceWith(frag);
    }
  }

  document.addEventListener("click", async (e) => {
    const btn = e.target.closest(".copy");
    if (!btn) return;
    const pre = btn.parentElement.querySelector("pre");
    const text = (pre.querySelector("code") || pre).innerText.replace(/\n$/, "");
    const label = btn.querySelector("span");
    try {
      await navigator.clipboard.writeText(text);
      label.textContent = "Copied";
    } catch {
      const r = document.createRange();
      r.selectNodeContents(pre);
      const s = getSelection();
      s.removeAllRanges();
      s.addRange(r);
      label.textContent = "Selected";
    }
    btn.classList.add("done");
    clearTimeout(btn._t);
    btn._t = setTimeout(() => { label.textContent = "Copy"; btn.classList.remove("done"); }, 1800);
  });

  // Header rule once the page scrolls; the mobile menu.
  const top = document.querySelector(".top");
  const onScroll = () => top?.classList.toggle("scrolled", scrollY > 8);
  addEventListener("scroll", onScroll, { passive: true });
  onScroll();
  const menu = document.querySelector(".menu-toggle");
  const nav = document.getElementById("topnav");
  menu?.addEventListener("click", () => menu.setAttribute("aria-expanded", nav.classList.toggle("open")));
  nav?.addEventListener("click", (e) => {
    if (e.target.closest("a")) { nav.classList.remove("open"); menu?.setAttribute("aria-expanded", false); }
  });

  // Feature tour: each feature plays as it looks in werkz. Frames show one after another; within a
  // frame, typed lines appear letter by letter and the rest line by line. While the tour is on
  // screen and nobody has picked a feature, it moves on by itself. Reduced motion shows the last
  // frame of the chosen feature, still.
  const tour = document.querySelector("[data-tour]");
  if (tour) {
    const tabs = [...tour.querySelectorAll(".tour-tab")];
    const still = matchMedia("(prefers-reduced-motion: reduce)").matches;
    let cur = 0, auto = !still, visible = false, timers = [], anims = [], progress = null;

    const clear = () => {
      timers.forEach(clearTimeout); timers = [];
      anims.forEach((a) => a.cancel()); anims = [];
      progress?.cancel(); progress = null;
    };
    const later = (fn, ms) => timers.push(setTimeout(fn, ms));

    // play one frame's lines; returns how long they take (ms)
    const playFrame = (frame) => {
      let t = 120;
      for (const line of frame.children) {
        const how = line.dataset.a || line.querySelector("[data-a]")?.dataset.a;
        if (!how) continue;
        const el = line.dataset.a ? line : line.querySelector("[data-a]");
        if (how === "type") {
          const n = Math.max(el.textContent.length, 1);
          anims.push(el.animate([{ clipPath: "inset(0 100% 0 0)" }, { clipPath: "inset(0 0 0 0)" }],
            { duration: n * 34, delay: t, easing: `steps(${n})`, fill: "backwards" }));
          t += n * 34 + 260;
        } else if (how === "sweep") {
          anims.push(el.animate([{ backgroundSize: "0% 100%" }, { backgroundSize: "100% 100%" }],
            { duration: 700, delay: t + 300, easing: "cubic-bezier(0.65, 0, 0.35, 1)", fill: "backwards" }));
          t += 1100;
        } else {
          anims.push(el.animate([{ opacity: 0, transform: "translateY(5px)" }, { opacity: 1, transform: "none" }],
            { duration: 320, delay: t, easing: "cubic-bezier(0.16, 1, 0.3, 1)", fill: "backwards" }));
          t += 150;
        }
      }
      return t + 200;
    };

    const show = (i, fromUser) => {
      clear();
      cur = (i + tabs.length) % tabs.length;
      tabs.forEach((tab, k) => {
        const on = k === cur;
        tab.setAttribute("aria-selected", on);
        tab.tabIndex = on ? 0 : -1;
        document.getElementById(tab.getAttribute("aria-controls")).hidden = !on;
        tab.closest(".tour-item").style.removeProperty("--p");
      });
      if (fromUser) auto = false;
      const scene = document.getElementById(tabs[cur].getAttribute("aria-controls"));
      const frames = [...scene.querySelectorAll(".frame")];
      if (still || !visible) {
        frames.forEach((f, k) => (f.hidden = k !== frames.length - 1));
        return;
      }
      let at = 0;
      frames.forEach((f, k) => {
        f.hidden = k !== 0;
        later(() => {
          frames.forEach((g, j) => (g.hidden = j !== k));
          const took = playFrame(f);
          if (k === frames.length - 1 && auto) {
            const hold = took + 2600;
            const item = tabs[cur].closest(".tour-item");
            progress = item.animate([{ "--p": 0 }, { "--p": 1 }], { duration: hold, easing: "linear", fill: "forwards" });
            later(() => auto && show(cur + 1), hold);
          }
        }, at);
        if (k < frames.length - 1) at += estimate(f) + 1300;
      });
    };
    // how long a frame plays, without playing it (to schedule the next one)
    const estimate = (frame) => {
      let t = 120;
      for (const line of frame.children) {
        const el = line.dataset.a ? line : line.querySelector("[data-a]");
        if (!el) continue;
        const how = el.dataset.a;
        t += how === "type" ? Math.max(el.textContent.length, 1) * 34 + 260 : how === "sweep" ? 1100 : 150;
      }
      return t + 200;
    };

    tabs.forEach((tab, k) => tab.addEventListener("click", () => show(k, true)));
    tour.querySelector(".tour-list").addEventListener("keydown", (e) => {
      const k = { ArrowDown: cur + 1, ArrowUp: cur - 1, Home: 0, End: tabs.length - 1 }[e.key];
      if (k === undefined) return;
      e.preventDefault();
      show(k, true);
      tabs[cur].focus();
    });
    // CSS can't interpolate a custom property without @property; register it where supported
    try { CSS.registerProperty({ name: "--p", syntax: "<number>", inherits: false, initialValue: "0" }); } catch {}
    new IntersectionObserver(([en]) => {
      const was = visible;
      visible = en.isIntersecting;
      if (visible && !was) show(cur);
      if (!visible) clear();
    }, { threshold: 0.35 }).observe(tour);
    show(0);
  }

  // Docs: mark the section in view in the table of contents.
  const links = [...document.querySelectorAll(".toc a")];
  const targets = new Map(links.map((a) => [document.getElementById(decodeURIComponent(a.hash.slice(1))), a]).filter(([el]) => el));
  if (targets.size) {
    const io = new IntersectionObserver((entries) => {
      for (const en of entries) {
        if (!en.isIntersecting) continue;
        links.forEach((l) => l.classList.remove("active"));
        targets.get(en.target)?.classList.add("active");
      }
    }, { rootMargin: "-20% 0px -70% 0px" });
    targets.forEach((_, el) => io.observe(el));
  }
})();
