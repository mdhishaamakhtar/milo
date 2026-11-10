# Product

<!-- impeccable:product-schema 1 -->

## Platform

ios

## Stack

Native SwiftUI app (iPhone only for now), Go backend, PostgreSQL, ConnectRPC, Claude for Milo (Haiku by default). The contract is `proto/milo`. How it looks is in `DESIGN.md`; how it works is in `docs/architecture.md`.

## Users

Hishaam, training with a personal trainer, is the only user for now. He logs during a gym session between sets, on an iPhone, and reviews afterwards. Decisions are made as if for anyone through the App Store: nothing may depend on his log's quirks.

## Product Purpose

A workout log you can talk to. When the trainer prescribes a movement no preset list knows, Milo works out what it is, saves it as a personal exercise, and logs it, so volume, progress and calorie numbers stay honest. Success: every set he does gets logged truthfully, and he can see clearly whether he is getting stronger.

## Positioning

Logging apps break on movements outside their preset list. Milo's agent turns a plain description ("barbell in a corner, twisting side to side, 12 each side, 20 kg") into a known exercise with muscles, movement pattern, equipment and an effort model, then reuses it next time.

## Operating Context

- In the gym, between sets: **tapping sets is the primary way to log.** The structured workout is the main surface; Milo is always one tap away for unknown exercises, questions about past sessions, corrections, planning and edits.
- Short glances between sets, often sweaty or one-handed, sometimes with poor signal (logging must work offline; the agent needs the network and says so).
- Afterwards: reviewing history and progress.
- Trainer-given workouts include variations, circuits and supersets, per-side reps, timed and distance sets, bodyweight and assisted movements.

## Capabilities and Constraints

- Hand logging works fully offline and syncs later.
- New exercises the agent infers appear as a proposal the user accepts or edits; sets for known exercises log immediately and can be undone.
- Templates are plain ordered exercise lists (supersets as 2a/2b), with no sets, reps or weights. Starting one pre-fills each exercise with what was done last time; numbers are changed inline on the day.
- The most common path is a blank workout built one exercise at a time as the trainer calls them out. Unknown exercises get a one-tap "Milo's guess" mid-session and can be refined with Milo afterwards from History.
- Navigation (redesigned 2026-11-10): three tabs, Today, History, Progress. No Library tab; exercises live under Progress and in the add field. An Ask Milo bar sits above the tab bar on every screen (native `tabViewBottomAccessory`) with a prompt that follows the current screen. A workout is a full-screen cover whose bottom field both matches known exercises and hands descriptions to Milo; it minimises to a live card on Today. The avatar opens Profile (body, Apple Health, units, rest timer, exercises, password, export, sign out).
- Milo's scope: a conversational agent over the whole log, not only an exercise adder. It answers questions about any workout or date, proposes edits as before/after cards (sets, names, exercises), applies them only on Apply and offers Undo, drafts templates in conversation, and states what it read.
- Milo shows rather than tells (since 2026-11-11): a question about progress, consistency or balance gets a card whether or not the word "chart" is used (lifts side by side with a trend line, compared lines, bars a week or month, sets by muscle), then two or three sentences on what it means, then up to three tappable next questions. It says so when a logged set is the heaviest yet. It stays on training and declines anything else in a sentence.
- Milo is built for anyone, not only for one log: it is checked against a battery of about 60 questions people really ask (`make battery`).
- Units: every exercise has its own unit (kg or lb), switched from a control above the weight column and remembered for that exercise; switching converts existing numbers. Weights are stored exactly and shown in the chosen unit; totals and charts use one unit set in Profile. His gym mixes pound dumbbells with kilo and pound machines.
- Apple Health: reads body weight (calories), height, date of birth, optionally body fat; writes workouts as Traditional Strength Training plus active energy, and warns about double counting with Apple Watch.
- Progress views: heavier top sets (best weight × reps, estimated one-rep max), total volume (per exercise and per week), and consistency (how often he trains). All three matter; the views will keep evolving with use.
- Apple Health is part of version one, not a later add-on: finished workouts and their active energy are written to Health, body data is read from it. Data export ("Export my data") is also in version one.
- Not a health or nutrition app; no photos or form checking. Not a remake of Hevy.
- iPhone only for now; no iPad, Watch or web.

## Brand Commitments

Confirmed by Hishaam on 2026-11-10:
- Dark theme only, neutral colours, workout minimalism: focused and sharp, no decoration.
- Apple Liquid Glass for the functional layer (native SwiftUI glass), with Milo's own identity in the content. The design canvas only approximates glass in CSS; the app uses the real system components (tab bar, `tabViewBottomAccessory`, toolbars, sheets, `glassEffect`), never a hand-drawn imitation.
- Palette: Floodlight. Ground #0A0E1A, surface #141B2B, text #E8EDF7, muted #8E98AE, accent #FF8F45 (stadium orange), second #69B7FF (sky blue, for new bests).
- The orange is used sparingly: done-set checks, the active tab, the logo, text buttons and focus rings. Never on large surfaces. Primary buttons are off-white (#E8EDF7) with dark text.
- Calm type: regular-width Sofia Sans at moderate sizes and weights; no giant condensed numbers.
- Sets are added and edited inline in the workout list, never in a pop-up.
- Logo: Bar M. Four bars that spell an M and end on the tallest: progress isn't linear, but it rises.
- Typeface: Sofia Sans (OFL, free), regular width throughout, tabular figures for numbers. (Sofia Sans Extra Condensed was dropped in the final design.)
- Radii: 24 for cards, 13 for wells, capsules for buttons.

## Evidence on Hand

- His own training history from another app, loaded once by a personal script and never committed. Real exercise names and numbers can come from it for design work.
- No testimonials, users, or brand assets exist; do not invent any.

## Product Principles

1. Logging a set must be faster than not logging it.
2. Honest numbers over flattering ones: an unknown movement is understood, never forced into the nearest preset.
3. The agent assists the log; it never takes it hostage. Everything it does is visible, editable and undoable.
4. Progress is the payoff: make getting stronger (or not) obvious.
5. Built for one person now, to App Store standard.
