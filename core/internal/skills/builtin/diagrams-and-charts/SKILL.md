---
name: diagrams-and-charts
description: Explain with a picture - choose the right diagram or chart and draw it with the visual tool in Paladin's calm theme.
---
# Diagrams and charts

Use this when a picture explains better than prose: an architecture, a request flow, a state machine, a
timeline, a comparison, numbers over time. Draw it with the `visual` tool.

## Pick the form
- **Structure** (components and how they connect): boxes and arrows, grouped by layer or boundary.
- **Sequence** (who calls whom, in order): lanes per actor, arrows top to bottom.
- **States**: rounded states, labelled transitions.
- **Change over time**: a line chart. **Parts of a whole or ranking**: a sorted bar chart (avoid pies beyond 3
  slices). **Distribution**: a histogram. **Two measures**: a scatter plot.
- **Exact values** someone will look up: a table, not a chart.

## Draw it
- SVG for diagrams and static charts (always a `viewBox`, width 100%); an HTML fragment with inline `<script>`
  when it needs interaction (hover values, toggles, sliders). No `<html>`, `<head>` or `<body>`.
- Everything inline: no external scripts, fonts, images or fetch (the frame has no network). Compute chart
  geometry yourself in the script or precompute it.
- Colors only from the theme variables: `var(--v-fg)` text, `var(--v-muted)` secondary text and gridlines,
  `var(--v-border)` outlines, `var(--v-accent)` the one thing to notice, `var(--v-series-1)`…`(--v-series-6)` for
  series, `var(--v-good)` / `var(--v-warn)` / `var(--v-bad)` for status. Background stays transparent.
- Calm and readable: text 12px or larger with `var(--v-font)`, generous spacing, thin 1px lines, at most 6
  colors, direct labels instead of legends where possible, no 3D, no shadows, no decoration.
- Label axes with units, start bar charts at zero, sort categories by value unless order matters.
- Keep it small enough to read without scrolling: a few hundred pixels tall.

## Interaction (HTML only)
- Keep it light: hover tooltips, a toggle between views, a slider. `paladin.sendPrompt(text)` can put a follow-up
  question into the user's message box (they decide whether to send it).

## After drawing
Refer to the visual in one line ("The diagram shows…") and add only what the picture cannot say. Give it a short
title and, when useful, a one-line caption with the data source.
