---
name: werkz
description: The coding agent for your terminal, on the models you already have.
colors:
  saffron: "#ff9933"
  saffron-ink: "#e07a10"
  red: "#e8344e"
  blue: "#3b6cf6"
  tile: "#1e1f2c"
  tile-deep: "#0c0d14"
  bg: "#ffffff"
  bg-soft: "#f6f6f9"
  card: "#ffffff"
  ink: "#0c0d14"
  ink-2: "#4a4c5e"
  ink-3: "#6b6e82"
  line: "#e6e6ee"
  line-strong: "#d3d4e0"
  link: "#2d5be3"
  code-bg: "#f3f3f7"
  success: "#16a34a"
  success-ink: "#15803d"
  caution-ink: "#b45309"
  dark-bg: "#0c0d14"
  dark-bg-soft: "#11121b"
  dark-card: "#161722"
  dark-ink: "#f4f4f7"
  dark-ink-2: "#b4b6c8"
  dark-ink-3: "#8c8ea3"
  dark-line: "#24253a"
  dark-line-strong: "#33354d"
  dark-link: "#8fb0ff"
  dark-code-bg: "#1e1f2c"
typography:
  display:
    fontFamily: "Geist, ui-sans-serif, system-ui, -apple-system, Segoe UI, sans-serif"
    fontSize: "clamp(2.4rem, 5.4vw, 4.1rem)"
    fontWeight: 600
    lineHeight: 1.04
    letterSpacing: "-0.04em"
  headline:
    fontFamily: "Geist, ui-sans-serif, system-ui, sans-serif"
    fontSize: "clamp(1.9rem, 3.6vw, 2.75rem)"
    fontWeight: 600
    lineHeight: 1.1
    letterSpacing: "-0.03em"
  doc-title:
    fontFamily: "Geist, ui-sans-serif, system-ui, sans-serif"
    fontSize: "clamp(2rem, 4.2vw, 2.75rem)"
    fontWeight: 600
    lineHeight: 1.1
    letterSpacing: "-0.03em"
  doc-heading:
    fontFamily: "Geist, ui-sans-serif, system-ui, sans-serif"
    fontSize: "1.5rem"
    fontWeight: 600
    letterSpacing: "-0.02em"
  title:
    fontFamily: "Geist, ui-sans-serif, system-ui, sans-serif"
    fontSize: "1.1rem"
    fontWeight: 600
    letterSpacing: "-0.01em"
  lede:
    fontFamily: "Geist, ui-sans-serif, system-ui, sans-serif"
    fontSize: "clamp(1.08rem, 1.6vw, 1.24rem)"
    fontWeight: 400
    lineHeight: 1.6
  body:
    fontFamily: "Geist, ui-sans-serif, system-ui, sans-serif"
    fontSize: "1.0625rem"
    fontWeight: 400
    lineHeight: 1.6
  label:
    fontFamily: "Geist, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.9rem"
    fontWeight: 500
  small:
    fontFamily: "Geist, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.8rem"
    fontWeight: 500
  mono:
    fontFamily: "Geist Mono, ui-monospace, SFMono-Regular, Menlo, monospace"
    fontSize: "0.88rem"
    fontWeight: 400
    lineHeight: 1.6
rounded:
  xs: "6px"
  sm: "8px"
  md: "10px"
  button: "11px"
  lg: "14px"
  window: "16px"
  xl: "18px"
  feature: "24px"
  full: "999px"
spacing:
  gutter: "clamp(20px, 4vw, 40px)"
  page: "1180px"
  header: "64px"
  section: "clamp(72px, 10vw, 120px)"
  section-head: "48px"
  grid-gap: "14px"
  card-gap: "20px"
components:
  button-primary:
    backgroundColor: "{colors.ink}"
    textColor: "{colors.bg}"
    typography: "{typography.label}"
    rounded: "{rounded.button}"
    padding: "0 20px"
    height: "46px"
  button-primary-hover:
    backgroundColor: "{colors.tile}"
  button-secondary:
    backgroundColor: "{colors.card}"
    textColor: "{colors.ink}"
    rounded: "{rounded.button}"
    padding: "0 20px"
    height: "46px"
  icon-button:
    backgroundColor: "{colors.card}"
    textColor: "{colors.ink}"
    rounded: "9px"
    padding: "0 10px"
    height: "36px"
  copy-button:
    backgroundColor: "{colors.card}"
    textColor: "{colors.ink-2}"
    rounded: "{rounded.sm}"
    padding: "0 10px"
    height: "30px"
  command-block:
    backgroundColor: "{colors.code-bg}"
    textColor: "{colors.ink}"
    typography: "{typography.mono}"
    rounded: "{rounded.md}"
    padding: "13px 4px 13px 15px"
  card:
    backgroundColor: "{colors.card}"
    rounded: "{rounded.lg}"
    padding: "16px"
  plan-card:
    backgroundColor: "{colors.card}"
    rounded: "{rounded.xl}"
    padding: "30px 28px 26px"
  chip:
    backgroundColor: "{colors.card}"
    textColor: "{colors.ink-2}"
    rounded: "{rounded.full}"
    padding: "7px 14px"
  status-pill:
    rounded: "{rounded.full}"
    padding: "3px 10px"
  tab:
    textColor: "{colors.ink-3}"
    typography: "{typography.label}"
    padding: "9px 12px 11px"
  tab-selected:
    textColor: "{colors.ink}"
  nav-link:
    textColor: "{colors.ink-2}"
    rounded: "{rounded.sm}"
    padding: "7px 12px"
  nav-link-active:
    backgroundColor: "{colors.bg-soft}"
    textColor: "{colors.ink}"
  product-window:
    backgroundColor: "{colors.tile-deep}"
    rounded: "{rounded.window}"
  closing-panel:
    backgroundColor: "{colors.tile-deep}"
    textColor: "{colors.dark-ink}"
    rounded: "{rounded.feature}"
---

# Design System: werkz

## Overview

**Creative North Star: "The Lit Window"**

The werkz site is a calm, professional product site in the category standard set by developer-tool leaders: a white canvas (or a near-black one when the visitor chooses dark), crisp Geist type, quiet zinc-tinted borders, and one real product shot. All the colour lives in that product shot. The logo's saffron, red and blue spread into a single soft glow behind a dark window holding a real capture of the werkz terminal UI. Everything around the window stays neutral so the product is the brightest thing on the page.

The pages are dense with facts and light on decoration. Commands are shown in full, in mono, with a copy button. Tables say exactly what each mode or install method does. Cards are flat white panels with a 1px border and at most a whisper of shadow. The brand colours appear outside the glow only as small, purposeful marks: the window's three dots, 20px line icons, plan dots, status pills, and blue for focus and selection.

Light is the default. Dark is opt-in through the header toggle (`data-theme` on `<html>`, stored in `localStorage` as `werkz-theme`, previewable with `?theme=dark`). It is a complete theme built on the logo tile's own near-black, not an inverted afterthought. The Silk Canopy world was built, seen and rejected, and nothing from it carries forward.

**Key Characteristics:**
- Neutral white or tile-black canvas; brand colour concentrated behind the product window.
- Geist Sans for everything readable, Geist Mono for every command, path and key. Both are self-hosted.
- 1px borders in two strengths, radii from 6px to 24px, quiet layered shadows.
- One real terminal capture, framed as a window, with no illustrations or stock imagery.
- Line icons on a 24px grid at a 1.8 stroke, coloured by a tri-tone rotation.
- Full light and dark themes, with light as the default.

## Colors

A neutral system tinted toward the logo tile's blue-black, with the logo's three colours used sparingly and on purpose.

### Primary
- **Signal Blue** (`blue`): the interaction colour. It marks the focus ring (2px outline, 3px offset), the selected tab underline, text selection (28% mix), the checklist icons in the safety list, the "latest" release node, and the border tint on plan chips. Body links use a deepened variant, **Link Blue** (`link`, 5.4:1 on white), and dark mode uses **Pale Link Blue** (`dark-link`).

### Secondary
- **Logo Saffron** (`saffron`): the first stop of the brand gradient, the first window dot, the ChatGPT plan dot, and the "Asks" pill wash. In light mode, icons use **Burnt Saffron** (`saffron-ink`) so they stay legible on white. Dark mode returns to pure saffron.

### Tertiary
- **Logo Red** (`red`): the middle stop of the gradient, the second window dot, the "Blocked" pill, and the Bypass row label. On this site red means danger. It is never used as a general accent.

### Neutral
- **Tile** (`tile`) and **Tile Deep** (`tile-deep`): the logo tile's two stops. Tile Deep is the product window body, the closing call-to-action panel, light-mode ink and the dark-mode canvas. Tile is the window's title bar, the primary button's hover state and the dark-mode code background.
- **Paper** (`bg`, `card`): white canvas and card surface in light mode.
- **Mist** (`bg-soft`): alternating "soft" section bands, table headers, active nav item backgrounds and step-number discs.
- **Ink ramp** (`ink`, `ink-2`, `ink-3`): headings and strong text, then body and secondary copy, then captions, meta and inactive tabs.
- **Hairline** (`line`) and **Rule** (`line-strong`): the two border strengths. Hairline goes on cards, tables and dividers. Rule goes on secondary buttons, copy buttons and the top borders of fact and contribute columns.
- **Code Wash** (`code-bg`): the background of command blocks and inline code.
- **Dark set** (`dark-*`): the same roles re-valued on the tile black. The canvas is `#0c0d14` and cards are lifted to `#161722`.

### Status
- **Allowed** (`success` wash, `success-ink` text), **Asks** (saffron wash, `caution-ink` text) and **Blocked** (red wash and text) appear only in permission-mode pills. `success` is also the colour of the "Copied" check.

### Named Rules
**The One Glow Rule.** The saffron → red → blue gradient (100deg, red at 48%) appears exactly once per page: blurred 64px behind the product window, at 0.7 opacity in light and 0.55 in dark. Never use it on text, buttons, borders, section backgrounds or the closing panel.

**The Small Marks Rule.** Outside the glow, brand colours appear only at mark size: 11px window dots, 10px plan dots, 20px icons, pill washes and a 2px tab underline. A brand colour never fills a surface larger than a pill.

**The Blue Means Interactive Rule.** Focus, selection, the selected tab and the active table-of-contents entry are blue. Do not use blue for decoration that a visitor might mistake for a control.

## Typography

**Display Font:** Geist (self-hosted variable, weights 300–700; fallback ui-sans-serif, system-ui)
**Body Font:** Geist
**Label/Mono Font:** Geist Mono (self-hosted variable, weights 400–600; fallback ui-monospace, SFMono-Regular, Menlo)

**Character:** One neo-grotesque family does all the work. Headings are set at weight 600 with tight negative tracking, and body copy is plain 400. The mono face is reserved for things a user types or reads in a terminal.

### Hierarchy
- **Display** (600, `clamp(2.4rem, 5.4vw, 4.1rem)`, line-height 1.04, -0.04em): the landing hero headline only. Centred, `text-wrap: balance`, max 19ch.
- **Headline** (600, `clamp(1.9rem, 3.6vw, 2.75rem)`, 1.1, -0.03em): landing section titles and the closing panel title.
- **Doc Title** (600, `clamp(2rem, 4.2vw, 2.75rem)`, 1.1, -0.03em): the H1 on docs pages.
- **Doc Heading** (600, 1.5rem, -0.02em): docs H2, always preceded by a hairline rule and 22px of padding.
- **Title** (600, 1.05–1.35rem, -0.01 to -0.015em): card, feature, plan and install-method headings.
- **Lede** (400, `clamp(1.08rem, 1.6vw, 1.24rem)`, 1.6, `ink-2`): the hero subhead (max 60ch). Section intros use 1.12rem.
- **Body** (400, 1.0625rem, 1.6): running text. Docs columns cap at 74ch and install-method prose at 64ch.
- **Label** (500, 0.9–0.98rem): buttons, tabs, nav links and copy buttons.
- **Small** (500–600, 0.8–0.82rem, sentence case): table headers, docs-nav and table-of-contents group labels, release group names.
- **Mono** (400, 0.88rem, 1.6): command blocks. Inline code is 0.9em inside a 6px-radius chip.

### Named Rules
**The Sentence Case Rule.** Every label is set in sentence case at normal tracking, including group labels, table headers and the "Latest" badge. Nothing is uppercase-tracked, and no label sits above a heading as an eyebrow.

**The Mono Is Literal Rule.** Geist Mono is used only for text a user would type or see in a terminal: commands, paths, flags, slash commands and keys. Never use it for decoration or headings.

**The Tabular Numbers Rule.** Release dates and table cells use `font-variant-numeric: tabular-nums`.

## Layout

The page width is `min(1180px, 100vw − 2 × gutter)`, with a fluid gutter of `clamp(20px, 4vw, 40px)`. The header is sticky at 64px with a translucent background (82% canvas mix, 14px backdrop blur). It gains a hairline bottom border once the page has scrolled 8px.

The landing page reads in a single column of full-width sections, each with `clamp(72px, 10vw, 120px)` of vertical padding. "Soft" sections (Mist background with hairlines top and bottom) alternate with plain ones to separate topics without cards. Section heads are left-aligned at max 680px with a 48px gap below. Only the features head is centred. Content grids are:
- three columns for features, docs and contribute steps (two columns at ≤960px, one at ≤620px);
- two columns for plans and facts;
- a 5fr/7fr split for safety and releases (one column at ≤900px);
- a 250px vertical tab rail plus a panel for install methods (horizontal tabs at ≤860px).

Docs pages use a three-column shell: a 230px sticky nav, a content column of at most 74ch, and a 210px sticky table of contents. The table of contents disappears at ≤1160px. At ≤820px the shell becomes one column and the nav becomes a collapsed bordered disclosure, so the page content comes first. The main nav collapses into a menu button at ≤880px.

On phones (≤640px), the permission table becomes one labelled block per mode with no horizontal scrolling. At ≤600px the hero swaps to a mobile-specific capture.

## Elevation & Depth

Depth is mostly tonal and drawn with lines: white cards on white or Mist, separated by 1px borders. Shadows are quiet and layered. The only dramatic depth on any page is the product window, which floats on a large soft shadow over the glow.

**The live window.** On screens 700px and wider, the product window replays a recorded werkz session as
text (`assets/session.json`: unique terminal lines plus timed frames, played by `site.js`), set in Term Mono,
a DejaVu Sans Mono subset that carries every box and symbol glyph the TUI draws. It plays only while visible,
holds the final answer, then loops. The still capture stays as the fallback for phones, reduced motion and
no JavaScript. Re-record it whenever the TUI changes, so the window never shows an outdated werkz.

### Shadow Vocabulary
- **Rest** (`0 1px 2px rgba(12,13,20,.05), 0 4px 10px -6px rgba(12,13,20,.12)`): the install card, plan cards, the modes table, the methods panel, open release entries and hovered doc cards.
- **Window** (`0 2px 6px rgba(12,13,20,.06), 0 30px 60px -30px rgba(12,13,20,.35)`): the product window only.
- **Dark-mode values** keep the same geometry with black at 0.4/0.6 (rest) and 0.4/0.8 (window).
- **Glow**: the brand gradient blurred 64px at 0.7 opacity in light and 0.55 in dark. It sits behind the window only.

### Named Rules
**The Lift On Intent Rule.** Surfaces rest flat or at Rest. Hover adds at most a 1–2px lift (buttons -1px, doc cards -2px plus Rest shadow). Nothing floats until a visitor points at it, except the product window.

## Shapes

Shapes are softly rounded rectangles that scale with the size of the object: 6px for inline code, kbd keys and the focus ring; 8px for nav links and copy buttons; 9px for icon buttons; 10px for command blocks and docs callouts; 11px for buttons; 14px for cards and tables; 16px for the product window; 18px for plan cards and the methods panel; and 24px for the closing panel. Chips, pills, the figcaption and the "Latest" badge are fully rounded (999px). Circles are used only for window dots, plan dots, step numbers and release timeline nodes.

Borders are always 1px. Hairline is the default and Rule is the stronger edge. Kbd keys get a 2px bottom border. Selected horizontal tabs use a 2px blue underline.

## Components

### Buttons
Buttons are solid and quiet, in the same ink as the text.
- **Shape:** gently rounded (11px), 46px tall, 20px side padding, Label type at 0.98rem/500 with an optional 18px line icon.
- **Primary:** ink fill with canvas text. In dark mode this inverts to a near-white fill with tile text. Hover moves to Tile and lifts 1px.
- **Secondary:** card fill, ink text and a Rule border. Hover darkens the border to `ink-3`.
- **Focus:** the global 2px blue outline at a 3px offset.
- **Icon button (header):** 36px square or pill, 9px radius, Hairline border. Hover moves to a Rule border and Mist fill.
- **On the closing panel:** the primary becomes white on tile and the secondary becomes transparent with a 30% white border.

### Command Block
This is the signature workhorse. It is a Code Wash panel (10px radius, Hairline border) holding mono text and a copy button in its top-right corner. The copy button is 30px tall with an 8px radius, a Rule border and a 14px copy icon plus the label "Copy". On success it changes to a green check and "Copied" for 1.8s, or "Selected" if clipboard access is refused. Long commands wrap only after a `/`. Docs code blocks are upgraded into the same component automatically.

### Tabs
- **Horizontal (install card):** borderless 0.9rem/500 buttons in `ink-3`. Selected tabs use `ink` with a 2px blue underline sitting on the tablist hairline.
- **Vertical (install methods):** 10px-radius rows on a Mist rail with a bold name and an `ink-3` hint. The selected row lifts to a card fill with a 1px Hairline ring and the Rest shadow.
- Both follow WAI-ARIA keyboard behaviour (arrow keys, Home and End).

### Cards / Containers
- **Corner Style:** 14px (`lg`) by default and 18px for plan cards and the methods panel.
- **Background:** `card` on canvas or Mist.
- **Shadow Strategy:** Rest, or none (doc cards rest flat and gain Rest on hover).
- **Border:** 1px Hairline.
- **Internal Padding:** 16px (install panel), 18–20px (doc cards), 28–30px (plan cards and method panels).
- **Open columns:** features, facts and contribute steps are not boxed. Each column gets a single top border (Hairline or Rule) and 18–20px of top padding.

### Chips and Pills
- **Provider chips:** full-round, Hairline border, card fill, 0.92rem/500 in `ink-2`. Subscription chips tint the border 35% toward blue and use `ink` text.
- **Status pills:** full-round, 0.82rem/500, a 6px currentColor dot and a 11–18% tinted wash. Green means Allowed, saffron/amber means Asks and red means Blocked.

### Navigation
- **Header:** brand lockup (28px logo tile with a 7px radius and wordmark at 600/1.12rem), then 0.94rem `ink-2` links with 8px-radius hover pills (Mist fill, `ink` text), then theme and GitHub icon buttons on the right. `aria-current` pages use the hover state.
- **Docs nav:** 0.94rem `ink-2` links. The current page is bold `ink` on Mist.
- **Table of contents:** 0.88rem `ink-3` links. The section in view turns Link Blue at 500 weight.
- **Mobile:** the menu button opens a full-width drop panel under the header, with 12px link padding.

### Product Window (signature)
A dark window (Tile Deep body, 16px radius, 14% ink border, Window shadow) holds a real capture of the werkz TUI. It has a 40px Tile title bar with three 11px dots in saffron, red and blue and a centred `~/code/my-app — werkz` title. It sits over the One Glow and rises into place on load (0.9s, 18px translate and 0.985 scale, `cubic-bezier(0.16, 1, 0.3, 1)`, 0.15s delay). A full-round caption pill sits below it. The image is always a genuine capture with alt text describing what the session shows.


On screens 1240px and wider the hero is one row aligned to the page grid: the headline, lede, install card and
buttons on the left (0.84fr) and the product window on the right (1.16fr, the larger share because the replay is
a 100-column terminal that scales with its width). Narrower screens stack them, the window full width. At
1280×720 and 1366×768 everything in the hero, buttons included, is in the first screen.
### Feature Tour

The features section is a tour, not a card grid: a list of features beside a sticky product window that plays
each feature the way it looks in werkz (scenes written in Term Mono with werkz's own terminal colours). The
selected feature expands with its description and a docs link; a 2px line in its tone fills while its scene
plays, then the next one starts. Picking a feature stops the auto-advance. Scenes are 54 columns and 15 rows,
sized to the window; below 1080px the window sits above the list, and reduced motion shows each scene's last
frame, still. This is the page's one authored motion after the hero.

### Release Timeline
A 2px Hairline spine runs through 14px circular nodes. The newest node is filled blue with a 4px 18%-blue halo. Each release is a `details` card (14px radius, Hairline border) with a rotating chevron. Its summary holds the version (600/1.08rem), a full-round ink "Latest" badge and a tabular `ink-3` date.

### Closing Panel
A 24px-radius Tile Deep panel holds a headline, a short paragraph in `#c9cbdb`, a translucent command block and the inverted buttons. It looks the same in light and dark mode and carries no gradient.

## Do's and Don'ts

### Do:
- **Do** keep the canvas neutral (`bg` / `bg-soft` in light, `dark-bg` / `dark-bg-soft` in dark) and let the product window carry the colour.
- **Do** show real commands in a command block with a copy button, in Geist Mono at 0.88rem.
- **Do** use line icons on a 24px viewBox with a 1.8 stroke and round caps and joins, at 18–20px, inlined as SVG symbols. Colour feature icons with the saffron/red/blue tone rotation (`saffron-ink` in light mode).
- **Do** separate topics with alternating Mist bands and hairlines before reaching for boxed cards.
- **Do** build every new colour choice for both themes through the `data-theme` custom properties. Light remains the default.
- **Do** respect `prefers-reduced-motion`. The build disables all transitions and animations when it is set.
- **Do** use the shared easing `cubic-bezier(0.16, 1, 0.3, 1)` at 0.2s for state changes and 0.3s for theme changes.

### Don't:
- **Don't** use the brand gradient anywhere except the single glow behind the product window: no gradient text, buttons, borders or section washes.
- **Don't** fill large surfaces with saffron, red or blue. Brand colours stay at mark size.
- **Don't** use red for anything but danger (Blocked, Bypass).
- **Don't** put uppercase tracked eyebrows or kickers above headings. Labels are sentence case.
- **Don't** use fabricated product imagery, mock dashboards, illustrations, stock photos, testimonials, user counts or invented benchmarks. The product shot is a real capture.
- **Don't** use hard offset shadows or heavy drop shadows on cards. Card depth is Rest at most.
- **Don't** use emoji or text glyphs as icons. Icons are stroked SVG symbols.
- **Don't** load fonts from a CDN or fall back to a different display family. Geist and Geist Mono are self-hosted.
