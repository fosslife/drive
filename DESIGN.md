---
name: drive
description: A self-hosted drive published as an archival finding aid — a collection that states its own extent, is arranged in series, and is described down to the item.
colors:
  paper: "#f4f5f2"
  paper-2: "#e7eae4"
  paper-3: "#dde1d9"
  ink: "#15191b"
  ink-2: "#545c58"
  rule: "#d1d6ce"
  rule-2: "#aeb5aa"
  green: "#1d3b2e"
  green-2: "#163024"
  on-green: "#e9ddc3"
  on-green-2: "#a9b6a8"
  buff: "#b4914f"
  buff-2: "#c7a978"
  oxblood: "#7d211c"
  oxblood-field: "#f1e2e0"
  on-oxblood: "#fdf7f6"
  plate-ground: "#0b110d"
typography:
  wordmark:
    fontFamily: "Archivo Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "1.1rem"
    fontWeight: 600
    letterSpacing: "0.2em"
    fontVariation: "wdth 118"
  headline:
    fontFamily: "Archivo Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "1rem"
    fontWeight: 600
    letterSpacing: "0.07em"
    fontVariation: "wdth 112"
  extent-figure:
    fontFamily: "Martian Mono Variable, ui-monospace, SF Mono, monospace"
    fontSize: "1.35rem"
    fontWeight: 500
    lineHeight: 1.15
    letterSpacing: "-0.01em"
    fontVariation: "wdth 75"
    fontFeature: "tabular-nums"
  reference:
    fontFamily: "Martian Mono Variable, ui-monospace, SF Mono, monospace"
    fontSize: "0.95rem"
    letterSpacing: "0.01em"
    fontVariation: "wdth 75"
    fontFeature: "tabular-nums"
  body:
    fontFamily: "Archivo Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.875rem"
    lineHeight: 1.5
    letterSpacing: "normal"
  note:
    fontFamily: "Archivo Variable, ui-sans-serif, system-ui, sans-serif"
    fontSize: "0.78rem"
    lineHeight: 1.55
  measurement:
    fontFamily: "Martian Mono Variable, ui-monospace, SF Mono, monospace"
    fontSize: "0.72rem"
    letterSpacing: "0.02em"
    fontVariation: "wdth 75"
    fontFeature: "tabular-nums"
  column-label:
    fontFamily: "Martian Mono Variable, ui-monospace, SF Mono, monospace"
    fontSize: "0.62rem"
    fontWeight: 500
    letterSpacing: "0.11em"
    fontVariation: "wdth 75"
  status-label:
    fontFamily: "Martian Mono Variable, ui-monospace, SF Mono, monospace"
    fontSize: "0.66rem"
    letterSpacing: "0.08em"
    fontVariation: "wdth 75"
rounded:
  none: "0"
  hair: "1px"
  control: "2px"
spacing:
  gut: "1.25rem"
  gut-narrow: "0.85rem"
  rail: "13.5rem"
  notes: "18rem"
  masthead: "3.5rem"
  series-row: "2.1rem"
  list-row: "40px"
components:
  button:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "0.3rem 0.7rem"
    typography: "{typography.body}"
  button-hover:
    backgroundColor: "{colors.paper-2}"
    textColor: "{colors.ink}"
  button-primary:
    backgroundColor: "{colors.green}"
    textColor: "{colors.on-green}"
    rounded: "{rounded.control}"
    padding: "0.3rem 0.7rem"
  button-primary-hover:
    backgroundColor: "{colors.green-2}"
    textColor: "{colors.on-green}"
  button-destructive:
    backgroundColor: "transparent"
    textColor: "{colors.oxblood}"
    rounded: "{rounded.control}"
    padding: "0.3rem 0.7rem"
  button-destructive-hover:
    backgroundColor: "{colors.oxblood-field}"
    textColor: "{colors.oxblood}"
  input:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    rounded: "{rounded.control}"
    padding: "0.35rem 0.5rem"
  card:
    backgroundColor: "{colors.paper}"
    textColor: "{colors.ink}"
    rounded: "{rounded.none}"
    padding: "1.4rem 1.5rem 1.5rem"
    width: "min(30rem, 100%)"
  row:
    backgroundColor: "transparent"
    textColor: "{colors.ink}"
    rounded: "{rounded.none}"
    height: "{spacing.list-row}"
    padding: "0 {spacing.gut}"
  row-selected:
    backgroundColor: "{colors.paper-3}"
    textColor: "{colors.ink}"
  series-item:
    backgroundColor: "{colors.green}"
    textColor: "{colors.on-green-2}"
    height: "{spacing.series-row}"
    padding: "0 {spacing.gut}"
  series-item-active:
    textColor: "{colors.on-green}"
  masthead:
    backgroundColor: "{colors.green}"
    textColor: "{colors.on-green}"
    height: "{spacing.masthead}"
    padding: "0 {spacing.gut}"
---

# Design System: drive

## Overview

**Creative North Star: "The Finding Aid"**

A finding aid is the document an archive prints about itself: the collection states its own extent, is arranged in series, and is described down to the item. This interface is that document, rendered. There is no content card floating on a page, no tree that discloses itself, no blue primary button. There is a masthead band, a series index, a ruled inventory, and a notes column that says out loud what the inventory currently is — including when the answer is honestly partial.

The register is institutional and drafted rather than friendly: an expanded, letter-spaced sans for the voice of the institution; a condensed typed mono for every figure it states. Density is high on purpose — the operator scans a hundred thousand entries by eye, so the list is one metronomic ruled surface, never a set of cards. Depth exists, but it is printed depth: rule weight, indent and numbering. Nothing is lifted off the page, because a printed document has margins, not elevation.

Colour is spent, not spread. One committed institutional colour — bindery green — owns the masthead, the series rail and the single filled control on each screen. Buff manila is container furniture and is never promoted to an accent. Oxblood is not in the palette at all in the ordinary sense: it is a reservation, held for restriction and destruction, and its appearance anywhere else is a defect. Dark mode is the same document after hours rather than an inverted token set: the bindery green leaves the masthead and becomes the ground, and the buff stops being furniture and becomes the ink.

**Key Characteristics:**

- Printed depth only: rule weight and indent, no elevation, no radius above 2px
- One metronomic 40px row governs the whole inventory
- Every figure is a measurement, set in condensed mono with tabular figures
- One committed institutional colour; one reserved restriction ink
- A permanent notes column that always tells the truth about the listing on screen
- Two self-hosted variable faces, both using their width axis as structure

## Colors

An archival reading room: acid-free board with a green cast, one bindery-green institutional colour, buff manila furniture, and a restriction ink that is held in reserve.

### Primary

- **Bindery Green** (`#1d3b2e`): The committed institutional colour. It owns the masthead band at full strength, the full-height series rail, the caret, the upload progress fill, the 3px top edge of a record card, the focus ring on paper, and the one filled control on each screen (`.primary`). On the gate it takes the entire surface — the only screen where it does, because nothing is in the collection yet to keep legible.
- **Bindery Green Deep** (`#163024`): Pressed state of the filled control, and the hairline the masthead and rail draw against their own ground.
- **Board Buff** (`#e9ddc3`): The ink that sits on bindery green — wordmark, series labels, viewer chrome. Its muted companion (`#a9b6a8`) carries secondary text on green.

### Secondary

- **Manila Buff** (`#b4914f`) and **Manila Buff Light** (`#c7a978`): Container furniture and nothing else. It is the buff mark that travels down the series rail, the folder mark in a row and in a register entry, the caveat hairline in the notes column, the top edge of a one-time reveal card, the underline a hovered reference or entry draws, the text-selection ground, and — in dark mode only — the fill of the primary control and the progress bar. It never sets running text on paper and is never used as a general accent.

### Neutral

- **Acid-Free Board** (`#f4f5f2`): The ground. Cool grey-white with a green cast, deliberately not cream.
- **Board Lifted** (`#e7eae4`) / **Board Lifted Twice** (`#dde1d9`): The two steps above ground. The first is a pressed control and a read-only field; the second is a selected row and a thumbnail's backing.
- **Record Ink** (`#15191b`): Body text, titles, the selection tick.
- **Record Ink Muted** (`#545c58`): Column heads, measurements in a row, notes prose, disabled control text.
- **Hairline** (`#d1d6ce`): The item rule — every row edge, every table cell edge, the notes column's own edge.
- **Hairline Strong** (`#aeb5aa`): The series-level rule — control and field borders, the column-head rule under the inventory, the scrollbar thumb.
- **Plate Ground** (`#0b110d`): The viewer's green-black, used at 88% over the page and at 92% behind the viewer bar. Never pure black.

### Named Rules

**The Reservation Rule.** Oxblood (`#7d211c`) is not a palette entry. It is a reservation held for restriction and destruction: the Delete control, the error bar, an upload that failed, a share whose target has left the collection, and the trash screen. If oxblood appears anywhere a byte is not at stake, that is a bug, not a variant.

**The Furniture Rule.** Buff is container furniture: active-series mark, folder marks, caveat hairline, reveal edge, and the dark-mode primary fill. A caveat is furniture, not an error — it takes buff, never oxblood. Buff is never borrowed for emphasis, links, or a second accent.

**The After-Hours Rule.** Dark mode is not an inverted token set. The ground becomes bindery green (`#15271e`) and the ink goes warm buff (`#e8dcbe`) — the same document printed on the cloth rather than the board. One relationship cannot survive the move: on white board the band stands 11:1 clear of the field, and on a green ground no green band can. The band therefore lifts one step (`#27412f`) and leans on its 1px border; the separation is carried by the rule, not by value alone. Do not chase the light-mode contrast ratio by darkening the ground toward black — that switches the committed colour off and leaves a generic dark interface with a green sidebar.

## Typography

**Display / UI Font:** Archivo Variable (weight 100–900, width 62–125%), falling back to `ui-sans-serif, system-ui, sans-serif`
**Measurement Font:** Martian Mono Variable (weight 100–800, width 75–112.5%), falling back to `ui-monospace, 'SF Mono', monospace`

**Character:** A drafted institutional pairing. Archivo goes expanded (103–118% width) and letter-spaced for anything that speaks in the institution's voice; Martian Mono goes condensed (75% width) so a column of reference codes, extents and dates stays narrow enough to leave the title its room. The width axis is load-bearing here, not decoration.

Both faces are self-hosted and declared by hand, `@font-face` by `@font-face`, rather than imported whole. The reason is shipping, not style: the packages also ship Cyrillic and Vietnamese cuts, and while `unicode-range` means a browser never downloads them, `go:embed` does not read `unicode-range` — every one of those cuts would sit inside every release binary forever. Latin and Latin Extended are declared; a filename in a script these faces do not carry falls back to the system stack for those glyphs, which is what `unicode-range` does anyway.

### Hierarchy

- **Wordmark** (Archivo 600, 1.1rem, width 118%, tracking 0.2em): The masthead name, and the gate's centred mark at 1rem. Always on bindery green in buff.
- **Headline** (Archivo 600, 1rem, width 112%, tracking 0.07em, uppercase): The screen title in the toolbar and the record card's `h1` (1.05rem, tracking 0.06em).
- **Extent Figure** (Martian Mono 500, 1.35rem, width 75%, tabular, tracking −0.01em): The number a collection states about itself before anything else. The largest type on the page after the masthead; drops to 1.05rem below 58rem and to 0.82rem below 44rem.
- **Reference** (Martian Mono, 0.95rem, width 75%, tabular): The path as a reference code in the reference head. Ellipsized, never wrapped.
- **Body** (Archivo, 0.875rem, line-height 1.5): Row titles, field values, control labels.
- **Note** (Archivo, 0.78rem, line-height 1.55, max 34ch in the notes column, 48ch in a card): Scope and arrangement prose.
- **Measurement** (Martian Mono, 0.72rem, width 75%, tabular, right-aligned): Extents, sizes and dates in any column. The row ordinal sets at 0.66rem.
- **Column Label** (Martian Mono 500, 0.62rem, width 75%, tracking 0.11em, uppercase): Inventory column head, table `thead`, card field labels, notes section heads.
- **Status Label** (Martian Mono, 0.66rem, width 75%, tracking 0.08em, uppercase): The survey mark while the reconciler is walking the disk, and the upload meta line.

### Named Rules

**The Typed-Record Rule.** Every figure in the interface is a measurement — a count, an extent, a byte total, a date, an ordinal — and is set in the condensed mono with `font-variant-numeric: tabular-nums`. The sans never sets a number in a column. This extends to surfaces the browser draws for you: the `datetime-local` field and each of its shadow sub-fields carry the mono explicitly, because they do not inherit it and would otherwise announce `dd/mm/yyyy, --:--` in the browser's own proportional face beside a column of typed dates.

**The Whole-Figure Rule.** A figure is never split from the noun it counts. The extent breakdown wraps between its clauses and never inside one (`.clause { white-space: nowrap }`, and `extentParts()` returns its clauses unjoined for exactly this reason). "3 / folders" is the one thing an extent statement may not say.

## Layout

**The shell** is a two-row, two-column grid: a masthead spanning the full width (3.5rem, 3.1rem below 44rem) over a full-height series rail (13.5rem) beside the main area. The `<header>` element is `display: contents` so the masthead and the rail hand themselves straight to the shell grid while staying one landmark in the markup. A public share link uses `.shell.public`: no account, so no series to index, and the masthead sits straight on top of the single thing the visitor was sent.

**The main area** is a four-row grid — toolbar, alert, column head, list — beside a fixed notes column (18rem; 13rem below 68rem). Giving the error bar its own grid row means it collapses to nothing when there is no error rather than shoving the listing down the page. The list area owns its own scroll container (`contain: layout paint`, deliberately not `strict`, because size containment would take away the height the virtualiser measures).

**The inventory grid** is five columns — ordinal 3rem, mark 1.75rem, title `minmax(0,1fr)`, extent 6rem, modified 8.5rem — with a 0.8rem gap and the page gutter for padding. The column head and every row share that template exactly, so the ruled surface lines up without a table.

**Rhythm.** The page gutter (`--gut`) is 1.25rem, narrowing to 0.85rem below 44rem. Series entries are 2.1rem tall; inventory rows are a fixed 40px. Vertical gaps run on a small ad-hoc scale (0.25 / 0.35 / 0.5 / 0.7 / 0.85 / 1.15 / 1.25rem) rather than a formal step ladder.

**Responsive.** At 68rem the notes column narrows. At 58rem the document reflows the way a printed one would if it were narrower: the notes column moves above the inventory and becomes a wrapping row; the series rail becomes a horizontal band under the masthead with its roman numerals dropped. At 44rem the inventory drops the two columns a phone cannot afford — the ordinal and the timestamp — keeping the title and the extent; the notes column keeps only what is changing while you look at it (the extent, anything arriving, anything not yet true) and hides its reference prose, because on a phone every line the notes hold is a row of inventory pushed off the screen.

**Motion.** This is a printed document, so motion sets type rather than animating objects: control and link colour transitions at 90ms linear, the notes column's text crossfading at 90ms (`settle`, opacity only, so a count ticking upward never shifts anything around it), the buff series mark sliding at 140ms `cubic-bezier(0.2,0,0,1)`, and the viewer image seating from 98% scale at 120ms. `prefers-reduced-motion: reduce` clamps every animation and transition to 1ms.

### Named Rules

**The Notes-Column Rule.** Everything the interface has to say about the listing lives in the notes column and stays there: the extent, the selection's own extent, what is arriving, and what is not yet true. Nothing in that column is a toast, and nothing in it shoves the list down the page.

**The Metronome Rule.** One row height (40px) governs the entire inventory. No cards, no group headers, no row that varies with its content. A hundred thousand entries have to read as one ruled surface, and anything that breaks the metronome breaks the scan.

## Elevation & Depth

This system has no elevation. There is no ambient shadow, no drop shadow, no gradient, and no surface that floats above another. Depth is printed depth, and it is spelled three ways: **rule weight** (hairline `--rule` between items, the stronger `--rule-2` under a column head, a 3px bindery-green top edge on a record card), **tonal step** (`--paper` → `--paper-2` → `--paper-3`, used for hover, press and selection), and **indent and numbering** (the roman numeral in the series margin, the row ordinal, the reference head's slash-separated path).

The one `box-shadow` in the build is not elevation and must not be read as permission for it: below 58rem the series rail becomes a horizontal band, its entries become as wide as their words, and the buff mark can no longer be arithmetic — so `.series a.on` draws `inset 0 -3px 0 var(--buff-2)`, an inset rule standing in for the mark, under the entry rather than beside it. Inset, flat, no blur, no offset colour.

### Named Rules

**The No-Elevation Rule.** No outer `box-shadow`, no `filter: drop-shadow`, no gradient, anywhere. A printed document has rule weight and indent, not elevation. A `box-shadow` is admissible only as an inset hairline standing in for a rule that cannot be drawn another way.

**The Own-Surfaces Rule.** The surfaces nobody draws still carry the design: `::selection`, the scrollbar, the focus ring, the progress bar and the date field's shadow parts are all themed from the palette. Left at their defaults they belong to no design system, and they are the cheapest tell that a page was assembled rather than built.

## Shapes

Rectilinear throughout. Three radii exist and none is larger than 2px: **2px** for controls, fields and the search shell; **1px** for the focus ring and a thumbnail's corner; **0** for the record card, which is explicitly `border-radius: 0` because a card in this world is a sheet, not a tile. Nothing is a pill, nothing is a circle, nothing is clipped to a curve.

Borders do the shaping. A 1px `--rule-2` stroke defines every control and field; a 1px `--rule` hairline separates items; the record card adds a 3px bindery-green top edge (buff on the gate and on a one-time reveal) as its only ornament.

The mark set is one authored family — 16px box, 1.5 stroke, square caps, mitre joins, `fill: none`, `stroke: currentColor` — so every mark takes the ink of whatever it sits in and can take a state. The squared-off geometry is the same drafted register as the type; a rounded stroke would read as a consumer app wearing a document's clothes. These marks replaced the emoji an earlier iteration used: an emoji is whatever the operating system decides it is, changes shape per platform, ignores the palette, and cannot take a state.

### Named Rules

**The Two-Pixel Rule.** No radius above 2px anywhere, and a card takes none at all.

**The Drawn-Mark Rule.** Every mark is an authored SVG on the 16px / 1.5-stroke / square-cap / mitre grid, inheriting `currentColor`. No emoji, no icon font, no third-party icon package, no raster icon.

## Components

### Buttons

- **Shape:** Square-cornered with a barely-there softening (2px radius), 1px `--rule-2` border, 0.3rem × 0.7rem padding, sans at weight 500, width 103%, tracking 0.01em, `white-space: nowrap`.
- **Default:** Board ground, record ink. Hover lifts the ground one step (`--paper-2`) and darkens the border to `--ink-2`; active presses to `--paper-3`; both transition over 90ms linear. Disabled goes transparent with muted ink and a hairline border.
- **Primary:** The only filled control in the interface — bindery green ground, buff text, no underline. It marks the act that puts material into the collection or takes it out of one (Upload, Restore, Open, Sign in), which is what makes it findable without being loud. Hover deepens to `--green-2`. In dark mode it fills with buff and sets `#131a16` ink, because a green fill on a green ground is not a control.
- **Destructive:** Never filled. Oxblood text on a border mixed 40% oxblood into the hairline; hover fills with the oxblood field and brings the border to full oxblood.
- **On the masthead:** Transparent ground, buff text, a border mixed 32% from the buff ink; hover fills 12% and brings the border to full.

### Inputs / Fields

- **Style:** 1px `--rule-2` stroke, 2px radius, board ground, 0.35rem × 0.5rem padding, sans at body size, bindery-green caret (buff in dark). Placeholder sets in `--ink-2`.
- **Focus:** The border takes the focus colour; the global 2px focus ring (offset 2px, 1px radius) handles everything else, switching to buff on the green masthead, rail and gate where a green ring would vanish.
- **Search:** A bordered shell holding a 14px `Glass` mark in muted ink and a borderless input; `:focus-within` moves the border to the focus colour and the inner input suppresses its own ring so the shell reads as one field. It flexes `1 1 16rem` to a 28rem ceiling and sits beside the reference head, not behind an icon.
- **Date field:** The mono is declared on the input and on every `-webkit-datetime-edit-*` part; the separators set in muted ink; the calendar indicator sits at 0.5 opacity, goes to 1 on hover, and is `invert(1)` in dark mode because it is a fixed dark bitmap with no other handle on it.

### Cards / Containers

- **Corner Style:** None (`border-radius: 0`).
- **Border:** 1px `--rule-2` on three sides with a 3px bindery-green top edge; buff-light on the gate, buff on a one-time reveal.
- **Background:** Board. **Shadow:** none, ever (see Elevation & Depth).
- **Internal Padding:** 1.4rem 1.5rem 1.5rem, contents stacked at 0.85rem, width `min(30rem, 100%)` (38rem for a reveal).
- Field labels inside a card set as column labels (uppercase mono, 0.62rem, tracking 0.11em) and reset their input back to the sans at body size, so the label is typed and the value is written.

### Navigation

The **series index**: a full-height bindery-green rail of plain-word entries, each a 2.1rem two-column grid of a right-aligned roman numeral and a label (sans 500, width 105%). Resting ink is `--on-green-2`; hover and active lift to `--on-green` over a 7% / 9% wash of the same ink. The active entry's numeral turns buff-light.

The **series mark** is one 3px buff-light bar, absolutely positioned, moved to the active entry rather than redrawn on it — `top: calc(var(--series-pad) + var(--at) * var(--series-h))`, so its position is arithmetic and never needs measuring. It is the single piece of motion the navigation owns (140ms). Below 58rem the rail becomes a scrolling horizontal band, the numerals and the travelling mark are dropped, and the active entry takes an inset 3px buff underline instead.

### Container List (signature)

The inventory is the system's centrepiece: a fixed-height 40px row on the five-column template, hairline-ruled edge to edge, `user-select: none` (so a shift-click range does not also drag a text selection across half the listing), and `white-space: nowrap` with ellipsis on the title. Hover is a 4% ink wash. Selection is marked the way an archivist marks an entry — the ground lifts to `--paper-3`, the title goes to weight 600, and a 2px × 11px ink tick is drawn in the left margin, in ink rather than buff so the selection survives a screen that cannot show colour. Folder marks take buff; file and plate marks take muted ink; photographic material replaces its mark with a 22px contact print (1px radius, board-lifted-twice backing). The empty state sits in the list's own grid area, indented to the title column so the ruled surface carries on into the message, balanced and capped at 72ch.

### Notes Column (signature)

A permanent 18rem column ruled off by a single hairline, holding stacked sections each led by an uppercase mono head over a hairline. It carries the extent figure and its breakdown, the selection's extent, the accession register, and caveats. Its live text crossfades at 90ms rather than moving. A **caveat** — the aid saying its own description is incomplete — is a 1px buff border-inline-start with 0.7rem of indent, at the same weight as every other rule in the document; a 2px accent stripe would be a callout bar from a different design system.

### Accession Register

Uploads list in the notes column while they arrive, never floating over the rows you are trying to drop files next to. Each is a two-column grid with an ellipsized name, a right-aligned mono meta line, and a 3px full-width progress bar drawn from the palette (`--paper-3` track, bindery-green fill, buff in dark) because the native widget is a different shape and colour in every browser. A failed upload turns its name and its reason oxblood.

### Register Sheets (tables)

The other three series render as tables: collapsed borders, left-aligned cells at 0.55rem × gutter, hairline row rules, and a sticky mono column head over the stronger rule. `max-width: 0` on cells is what lets the title ellipsize in an auto-layout table, and it is lifted again on `.num` and `.actions` columns — otherwise a date renders as "23" and "Never used" renders as "Nev", and a value the interface mangles is a value it is getting wrong. An `.entry` keeps the same mark-then-title reading order as an inventory row, so the two never feel like different applications. A stale row keeps its place, mutes to `--ink-2`, and states its reason in oxblood.

### The Plate (viewer)

Full-bleed fixed overlay on the room's own green-black at 88%, never pure black. The image caps at `min(92vw, 1400px)` × 76dvh and seats from 98% scale in 120ms. Every word about the item sits on its own plate below the image — a bordered bar at 92% ground with buff text, 14px marks, and its own buff focus ring — never over the pixels it describes. The share dialog reuses the same ground, because it is the same act: taking one item out of the listing to work on it.

### The Gate

The one screen where the committed colour takes the entire surface. Nothing is in the collection yet, so there is no inventory to keep legible — the room is simply closed, and it looks closed. A centred record card with a buff-light top edge sits under the wordmark set in buff.

## Do's and Don'ts

### Do:

- **Do** set every figure — count, extent, byte total, date, ordinal — in Martian Mono at width 75% with `font-variant-numeric: tabular-nums`, including inside browser-drawn fields.
- **Do** keep the inventory at one fixed 40px row height, hairline-ruled, edge to edge.
- **Do** show depth with rule weight, tonal step and indent: `--rule` between items, `--rule-2` under a head, `--paper-2`/`--paper-3` for hover, press and selection.
- **Do** keep bindery green for the masthead, the series rail and the single filled control per screen; keep buff for container furniture and the dark-mode primary fill.
- **Do** put everything the interface has to say about the listing in the notes column, and let it crossfade in place.
- **Do** pair every state with a word and a non-colour mark — the selection tick, the survey ticks, the caveat rule — so a screen without colour still reads.
- **Do** theme the surfaces nobody draws: selection, scrollbar, focus ring, progress bar, date-field parts.
- **Do** draw new marks on the authored 16px / 1.5-stroke / square-cap / mitre grid with `stroke: currentColor`.
- **Do** declare any new self-hosted face by hand, Latin and Latin Extended only, because `go:embed` puts every declared cut in every release binary.

### Don't:

- **Don't** add an outer `box-shadow`, a `drop-shadow` filter or a gradient anywhere; the only admissible `box-shadow` is an inset hairline standing in for a rule.
- **Don't** exceed a 2px radius, and don't give a card any radius at all.
- **Don't** use oxblood for anything but restriction and destruction. A caveat, a warning, a stale hint and an "unsaved" state all take buff or muted ink.
- **Don't** promote buff to a general accent, a link colour or an emphasis ink.
- **Don't** introduce a card, a group header or a variable-height row into the inventory.
- **Don't** build dark mode by darkening the ground toward black; the ground is bindery green and the ink is warm buff, and the band's separation is carried by its 1px border.
- **Don't** set a number in the sans, and don't put a proportional figure in a column.
- **Don't** ship an emoji, an icon font, a third-party icon pack or a raster icon.
- **Don't** announce anything in a toast, a floating banner or an overlay that shoves the list down the page; it belongs in the notes column or in the collapsible alert row.
- **Don't** put type over the pixels it describes; item text sits on its own plate.

## Unspent Devices of This World

These are defined-but-unexercised parts of the finding-aid world, recorded so they are not mistaken for oversights and not lost. They are gaps in the world's own vocabulary, not defects in the build.

- **The reference code.** The reference head prints the literal label "Top level" where a finding aid prints an actual reference code. The slash-separated path is doing the work of a reference code without being one.
- **Extent per container.** The collection states its extent; a container does not. Folder rows print an em dash in the extent column because the list API returns no child count and no recursive size. The device is built and only the data is missing.
- **Depth by indent.** Indent is named as the world's depth mechanism, but each folder view lists flat, so nothing is ever indented. The rule stands unexercised.
- **A span statement.** No date-range or span statement exists anywhere, though a finding aid states one about every level it describes.
- **A running head.** Nothing repeats the reference during a long scroll; the reference head scrolls away with the toolbar and does not come back.
