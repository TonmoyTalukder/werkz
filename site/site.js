// werkz site: theme, tabs, copy buttons, menu, docs helpers.
(() => {
  const root = document.documentElement;

  // Theme: light by default; the toggle switches to dark and remembers the choice.
  document.querySelector(".theme-toggle")?.addEventListener("click", () => {
    const next = root.dataset.theme === "dark" ? "light" : "dark";
    root.dataset.theme = next;
    try { localStorage.setItem("werkz-theme", next); } catch {}
  });

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
