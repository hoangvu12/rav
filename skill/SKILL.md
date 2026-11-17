---
name: rav
description: Rebuild a video as an editable HTML composition that matches the original frame for frame, with the local rav CLI (ffmpeg and headless Chrome, fully offline). Use to reverse, recreate, remake or copy a video, or 「复刻这个视频」.
---

# Rebuild a video with rav

The result is a project folder (`index.html` and `assets/`) that `rav render` turns into a video matching the original frame for frame, and that a person keeps editing as HTML. Rebuild videos the person has the right to use.

`rav help` lists every command. If `rav` is missing, install it with `go install github.com/hoangvu12/rav@latest` (Go 1.24+); it also needs ffmpeg and Chrome, Edge, Brave or Chromium. Times are seconds (`2.5`) or frames (`75f`). Run rav from inside the project folder, so the asset paths it prints are the paths the page uses.

## 1. Measure the original

1. `rav init <project> --video <video>`: a stage at the original's exact size, frame rate and frame count, with the video copied in and its sound wired as the track. Those root attributes are the original's; build inside them.
2. `rav cuts <video>`: each shot starts at a printed time. Write times exactly as printed; they are rounded down so a layer never lands a frame late.
3. Read consecutive frames at every change (each cut, entrance, exit and transition, three or four frames either side) with `rav frames <video> --range 80f,90f [--roi x,y,w,h]`, and open the sheet. Sampling a frame a second misses 1–3-frame timing, the direction things move, dissolves at cuts and one-frame flashes.
4. `rav color <video> --at t x,y,r` for every flat colour you will build, `rav changes <video> --roi <box>` for the exact frames a box changes (captions, counters, pop-ons), and `rav ink <video> --at t --roi <box>` for the exact box of a line of text.

Done when `RAV.md` holds the shot list: every shot with its start frame, and every element in it with the frame it enters, the frame it leaves, and how it moves.

## 2. Build every layer

Each thing on screen is one of three kinds:

- **Drawn**: text, captions, shapes, cards, UI, charts, stickers, simple vector motion. Rebuild it in HTML, CSS or SVG. Read [references/match.md](references/match.md) before the first drawn layer.
- **Picture**: a photo, logo, product shot, screenshot or clip placed inside the frame. Cut it from the original on its own box: `rav cut <video> -o assets/x.png --at t --roi x,y,w,h` for a still, `-o assets/x.mp4 --from t --to t` for a clip (`--to` is the first frame left out). Cut a logo where it is largest. rav prints the tag to paste.
- **Footage**: people, places, anything a camera or a 3D renderer made. Read [references/footage.md](references/footage.md); for a painted or 3D-animated film, [references/animated.md](references/animated.md) too.

### Built to edit

The person will change this page after it matches, so every choice they might change lives in one place:

- **Look**: colours, fonts and sizes are CSS custom properties in one `:root` block at the top of the stylesheet, named for their role (`--brand`, `--caption-bg`, `--title-font`), and the rules read them with `var()`.
- **Timing**: each moment is a named custom property in that same block (`--t-headline-in: 2.766666s`, used as `animation-delay: var(--t-headline-in)`), or for GSAP a `const T = { headlineIn: 2.766666 }` at the top of the script.
- **Words**: every piece of text is live HTML text, written once, in an element with a readable id (`#headline`, `#caption-3`).
- **Files**: assets carry descriptive names (`assets/logo.png`, `assets/shot03.mp4`).
- **Order**: layers sit back to front, each shot's group headed by a comment naming the shot and its frames.

### The clock

rav renders by seeking the page to each frame's time and capturing it, so everything visible is a function of composition time:

- **CSS animations and `el.animate()`** all run from composition time 0. Place each with `animation-delay` and hold it with `animation-fill-mode: both`; declare them in the stylesheet or at load, so they exist from the first frame.
- **A hard switch** (a caption changes, a layer pops on) is `animation: on 1s 2.766666s steps(1, jump-start) both` with `@keyframes on { from { opacity: 0 } to { opacity: 1 } }`.
- **GSAP**: one timeline at `window.__tl`, every tween at an absolute time (`tl.to(el, {x: 200}, 2.766666)`), with `gsap.min.js` copied into `assets/`.
- **`<video>`** stays `muted`; `data-start`, `data-duration`, `data-offset` and `data-rate` place it on the clock. rav seeks it every frame and hides it outside its window.
- **Canvas and custom code** draw from `performance.now()` inside `requestAnimationFrame` (rav runs the queued callbacks once per frame, at composition time), or set state in `window.__seek = async (t) => {}`.
- **Sound** is the one `<audio data-track>` that `init` wrote: the original's own sound, whole. Every clip is muted.
- **Fonts and images** are local files in `assets/` (fonts through `@font-face`), so every render is offline and identical.

Timing lives on this clock alone: `setTimeout`, `setInterval` and `new Date()` run on wall-clock time, and CSS transitions jump to their end.

## 3. Check while building

`rav diff <video> <project> --at t,t,...` renders just those frames, in seconds, and writes original | yours | difference pictures with the worst cell boxed. Check a held moment of every shot and the frames either side of every change you built, and open the pictures. `rav serve <project>` previews with a scrubber.

Done when every shot has content and each shot's held moment scores SSIM 0.95 or more and colour ΔE under 3, or its remaining gap is one you can name (footage grain, a substituted font).

## 4. Render and diff, twice

1. `rav render <project> -o out.mp4`, then `rav diff <video> out.mp4`.
2. Fix what it names that you can see: a missing, extra or late cut; lengths that differ; missing content; a shot with low SSIM or high ΔE (open its worst-moment picture).
3. Render and diff once more, then stop. The last points on footage grain and compression cost more than they return.

## 5. Hand over

1. `rav diff <video> out.mp4 --sbs compare.mp4`, and give the person `out.mp4` and `compare.mp4`.
2. Finish `RAV.md`: the shot list; what is rebuilt, cut and approximate (a substituted font, painted-out text); the last diff's numbers; and a short "How to edit" list naming where the text, colours, fonts, timings and swappable files live.
3. `rav serve <project>`, so they can scrub and keep editing.

Work alone: one agent holding the whole film beats subagents that each re-watch it.
