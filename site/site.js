// werkz site: tabs, copy buttons, the canopy's solo state, menu, and the scroll rail.
(() => {
  // Tabs (WAI-ARIA tabs: click, arrow keys, Home/End).
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
  for (const pre of document.querySelectorAll(".doc pre")) {
    const wrap = document.createElement("div");
    wrap.className = "cmd";
    pre.replaceWith(wrap);
    const btn = document.createElement("button");
    btn.type = "button";
    btn.className = "copy";
    btn.textContent = "Copy";
    wrap.append(pre, btn);
  }

  // Long commands wrap only after a "/" (never inside a word); copying is unaffected.
  for (const code of document.querySelectorAll(".cmd pre code, .cmd pre, .doc :not(pre) > code")) {
    const walker = document.createTreeWalker(code, NodeFilter.SHOW_TEXT);
    const nodes = [];
    while (walker.nextNode()) nodes.push(walker.currentNode);
    for (const node of nodes) {
      if (!node.data.includes("/") || node.parentElement.closest("wbr")) continue;
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
    const code = pre.querySelector("code") || pre;
    const text = code.innerText.replace(/\n$/, "");
    try {
      await navigator.clipboard.writeText(text);
      btn.textContent = "Copied";
    } catch {
      const r = document.createRange();
      r.selectNodeContents(code);
      const s = getSelection();
      s.removeAllRanges();
      s.addRange(r);
      btn.textContent = "Selected";
    }
    btn.classList.add("done");
    clearTimeout(btn._t);
    btn._t = setTimeout(() => { btn.textContent = "Copy"; btn.classList.remove("done"); }, 1800);
  });

  // The canopy: hovering or focusing a band, or its card, solos that band.
  const canopy = document.querySelector(".canopy");
  if (canopy) {
    const solo = (band) => {
      if (band) canopy.dataset.solo = band; else delete canopy.dataset.solo;
      document.querySelectorAll(".silk").forEach((s) => s.classList.toggle("solo", s.dataset.band === band));
    };
    for (const el of document.querySelectorAll(".band, .silk")) {
      el.addEventListener("pointerenter", () => solo(el.dataset.band));
      el.addEventListener("pointerleave", () => solo(null));
      el.addEventListener("focusin", () => solo(el.dataset.band));
      el.addEventListener("focusout", () => solo(null));
    }
    for (const band of document.querySelectorAll(".band")) {
      band.addEventListener("click", () => {
        document.querySelector(`.silk.${band.dataset.band}`)?.scrollIntoView({ behavior: "smooth", block: "center" });
      });
    }
  }

  // Header: a rule once the page scrolls; the mobile menu.
  const top = document.querySelector(".top");
  const onScroll = () => top.classList.toggle("scrolled", scrollY > 8);
  addEventListener("scroll", onScroll, { passive: true });
  onScroll();
  const toggle = document.querySelector(".menu-toggle");
  const nav = document.getElementById("topnav");
  toggle?.addEventListener("click", () => {
    const open = nav.classList.toggle("open");
    toggle.setAttribute("aria-expanded", open);
  });
  nav?.addEventListener("click", (e) => {
    if (e.target.closest("a")) { nav.classList.remove("open"); toggle.setAttribute("aria-expanded", false); }
  });

  // Rail (landing) and table of contents (docs): mark the section in view.
  const spy = (links) => {
    const map = new Map();
    for (const a of links) {
      const id = decodeURIComponent((a.getAttribute("href") || "").split("#")[1] || "");
      const el = id && document.getElementById(id);
      if (el) map.set(el, a);
    }
    if (!map.size) return;
    const io = new IntersectionObserver((entries) => {
      for (const en of entries) {
        if (!en.isIntersecting) continue;
        links.forEach((l) => l.classList.remove("active"));
        map.get(en.target)?.classList.add("active");
      }
    }, { rootMargin: "-30% 0px -60% 0px" });
    map.forEach((_, el) => io.observe(el));
  };
  spy([...document.querySelectorAll(".rail a")]);
  spy([...document.querySelectorAll(".toc a")]);
})();
