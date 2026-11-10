# Design

How Milo looks and behaves, and the reasons, so a new screen matches the old ones. The tokens live in `ios/Milo/DesignSystem`; this file explains them. Product intent is in `PRODUCT.md`.

## Character

Dark, neutral, focused: a tool used between sets, one-handed. One focal point per screen. Nothing decorative. The system's own Liquid Glass does the chrome; Milo's identity is in the content (type, the orange, the mark).

## Colour (Floodlight)

| Token | Hex | Use |
|---|---|---|
| `bg` | `#0A0E1A` | The ground. Deepens toward the top and bottom edges, under the glass |
| `s1` | `#121929` | The one card surface |
| `s2` | `#1A2236` | Wells, chips, bar tracks |
| `s3` | `#253049` | Quiet fills, unselected bars |
| `well` | `#0C1120` | Inset fields inside a card |
| `line` / `line2` | white 7% / 12% | Hairlines and card edges |
| `tx` | `#E8EDF7` | Text, and primary buttons (off-white with dark text) |
| `tx2` | `#9BA6BD` | Secondary text |
| `tx3` | `#626E88` | Labels, captions, placeholders |
| `or` | `#FF8F45` | The accent: ticked sets, the selected tab, links, the mark, a lift that went up |
| `bl` | `#69B7FF` | Body weight, and a second line on a chart |
| `red` | `#FF7A70` | Deleting only |

Orange is spent sparingly and never on a large surface or on toolbar glass. A dip in a lift is stated in grey, not red: red means something will be removed.

## Type

Sofia Sans, regular width, at calm sizes; tabular figures for numbers. No giant condensed numerals.

| Token | Size / weight | Use |
|---|---|---|
| `large` | 34 bold | Tab titles |
| `title` | 26 bold | Pushed screen titles |
| `h2` | 20 bold | Section headers |
| `h3` | 17 semibold | Card and row titles |
| `body16` / `bodyStrong` | 16 | Body |
| `sub` | 14.5 | Secondary lines |
| `cap` | 12.5 semibold | Small labels |

Sofia Sans sits tight vertically, so stacked lines need explicit room.

## Space

Named in `Space` (`Theme.swift`). Use the names, not literals.

| Token | Points | Between |
|---|---|---|
| `underTitleText` | 2 | A tab's large title and the line under it (`TitleLine`) |
| `underTitle` | 20 | That line and the first card |
| `headingText` | 8 | A screen's own heading and its line |
| `rowText` | 4 | A row's name and the smaller line under it |

Screens have a 16 point side margin; cards pad 18; `SectionHeader` leaves 30 above and 12 below; rows are at least 58 high.

## Shape and motion

Radii: 24 for cards, 13 for wells, capsules for buttons and chips. Motion is quick and settled, never bouncy: `.quick` (0.2 s) for a control answering a touch, `.settle` (0.32 s) for things arriving or moving, `.arrive` for a row fading up into place. Respect Reduce Motion.

## Glass

Follow Apple's Liquid Glass guidance; don't restyle it.

- System components first: the tab bar, toolbars, sheets, the Ask Milo `tabViewBottomAccessory`, `.searchable`.
- Custom glass only on floating controls (Milo's message field, the workout's add field, the rest bar, the Undo bar), `.regular`, grouped in a `GlassEffectContainer` when they sit together. Never glass in the content layer.
- Toolbar icons are monochrome. One prominent action per toolbar (Save, Finish, Done), off-white with dark text.

## Behaviour

- Sets are added and edited inline on the workout screen, never in a pop-up or sheet.
- Common destructive actions happen at once with an Undo bar. Pop-ups are for the rare and irreversible (deleting the account) and for unsaved changes.
- Logging never waits on the network.
- Templates are plain ordered lists the user makes and edits; no assumed rotation.

## Milo in the conversation

- The mark is four bars spelling an M that ends on the tallest. It stands on the baseline of the text beside it and its bars move while Milo works.
- Replies are paragraphs, short lists and bold exercise names. What Milo read collapses into one line that opens.
- A card answers by showing: a line chart (orange, a second line in blue), bars a week or month with the latest picked out in off-white, lifts side by side with a small trend line and the change, shares as neutral bars. When a card carries the numbers the text doesn't repeat them.
- Up to three suggested replies sit under the latest answer as chips, only once Milo has finished.
- Anything Milo did straight away folds to one line with Undo. Anything that rewrites or deletes waits on a card with its button.

## Words

Short and plain, in the lifter's terms: "60 kg × 8", "3 × 45 s", "12 each side". An estimated one-rep max is always called an estimate. No cheerleading.

## Before showing a screen

Look at it in the simulator: the empty state, a long name, the largest text size. Compare with the design canvas when one exists for it.
