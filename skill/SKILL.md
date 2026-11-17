---
name: rav
description: Rebuild a video as an editable HTML page that renders back to it frame for frame, with the local rav CLI. Use when the user wants a video reversed, recreated or copied 1:1 (复刻), or a rav project edited or re-rendered.
---

# Reverse a video with rav

The result is a project folder (`index.html`, `assets/`, `RAV.md`) that `rav render` turns back into the original frame for frame, and that the person keeps editing as HTML. Rebuild videos the person has the right to use.

`rav help` lists every command and flag. If `rav` is missing, install it with `go install github.com/hoangvu12/rav@latest`; it also needs ffmpeg and Chrome, Edge, Brave or Chromium. Times are seconds (`2.5`) or frames (`75f`). Work in frames, and run rav from inside the project folder so the asset paths it prints are the paths the page uses.

## 1. Shot list

1. `rav init <project> --video <video>` makes a stage at the original's size, frame rate and length, copies the video in and wires its sound as the track. Build inside those numbers.
2. Find every boundary. `rav cuts <video>` finds cuts in footage. A switch on a flat background (a card changing, a word appearing) is too small for it, so also run `rav changes <video> --roi 0,0,<width>,<height> --min 1`, and look through the whole film on sheets: `rav frames <video> --range 0f,<last>f --every 8`.
3. At each boundary, read three or four frames either side with `rav frames <video> --range a,b [--roi box]` for the exact frame, fades, flashes, and how things enter and leave.
4. Sort every layer into one of the three kinds in step 2.

Done when `RAV.md` holds the shot list: each shot's first frame and contents, and each layer's kind, first frame and last frame.

## 2. Build

- **Drawn**: text, captions, cards, shapes, charts, UI simple enough to draw, flat vector motion. Rebuild it in HTML, CSS or SVG. Read [references/drawn.md](references/drawn.md) before the first drawn layer.
- **Picture**: a still inside the frame (logo, emoji, product shot, screenshot). Cut it where it is largest: `rav cut <video> -o assets/<name>.png --at <frame> --roi x,y,w,h`. rav prints the tag to paste.
- **Footage**: anything filmed, screen-recorded or rendered in 3D. It plays as clips cut from the original, with the drawn things on top painted out and rebuilt. Read [references/footage.md](references/footage.md) before the first footage shot.

The person edits this page after it matches, so each choice lives in one place:

- Colours, fonts and sizes are CSS custom properties in one `:root` block, named for their role (`--brand`, `--caption-bg`).
- Each moment is a custom property in the same block (`--t-headline-in: 2.769433s; /* f83 */`), read with `animation-delay: var(...)`. For GSAP, a `const T = {...}` at the top of the script.
- Every word is live text in an element with a readable id (`#headline`, `#caption-3`).
- Assets have descriptive names (`assets/logo.png`, `assets/shot03.mp4`).
- Layers sit back to front, each shot's group headed by a comment with its frames.

### The clock

rav seeks the page to each frame's time and screenshots it, so everything visible follows composition time:

- CSS animations and `el.animate()` run from time 0. Place them with `animation-delay`, hold them with `animation-fill-mode: both`, and create them at load.
- A hard switch is `animation: on 1s <time> steps(1, jump-start) both` with `@keyframes on { from { opacity: 0 } to { opacity: 1 } }`.
- GSAP: one timeline at `window.__tl`, every tween at an absolute time, `gsap.min.js` copied into `assets/`.
- `<video>` is `muted`, placed with `data-start`, `data-duration`, `data-offset` and `data-rate`. rav seeks it every frame and hides it outside its window.
- Canvas and custom code draw from `performance.now()` inside `requestAnimationFrame`, or set state in `window.__seek = async (t) => {}`.
- Sound is the `<audio data-track>` that `init` wrote. Fonts and images are files in `assets/`.

Frame k starts at k × den / num seconds (k × 1001 / 30000 at 29.97 fps). Write it rounded down to the microsecond, so a layer never lands a frame late. Keep all timing on this clock: `setTimeout`, `setInterval` and `new Date()` follow the wall clock, and CSS transitions jump to their end.

## 3. Check while building

`rav diff <video> <project> --at <frames>` renders just those frames and writes original | yours | difference pictures with the worst cell boxed. Check a held moment of every shot and the frames either side of every change, and open the pictures.

Done when every shot has content and each held moment scores SSIM 0.95 or more and ΔE under 3, or its remaining gap has a name (a substitute font, footage grain).

## 4. Render and diff, twice

1. `rav render <project> -o out.mp4`, then `rav diff <video> out.mp4`.
2. Fix what it names: a missing, late or extra cut; a different length; missing content; a shot with low SSIM or high ΔE (open its worst-moment picture). A cut listed as extra or missing whose frames score 1.000 on both sides with `--at` is the same cut in both videos; leave it.
3. Render and diff once more.

Done when the second diff names nothing you can fix.

## 5. Hand over

1. `rav diff <video> out.mp4 --sbs compare.mp4`, and give the person `out.mp4` and `compare.mp4`.
2. Finish `RAV.md`: the shot list, what is rebuilt, cut and approximate, the last diff's numbers, and a short "How to edit" naming where the words, colours, fonts, timings and swappable files live.
3. `rav serve <project>`, so they can scrub and keep editing.

Do the whole film in one agent: the shot list, the measurements and the fixes all depend on each other.
