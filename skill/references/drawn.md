# Drawn layers

## Timing

- Drawn things change on exact frames. Something fully there on frame k switches on at k; tween only what you saw move over several frames.
- A fade has a curve. Sample it per frame (`rav color` on a solid spot, or the mean brightness of its box) and write it as keyframes.
- Captions and word-by-word text: `rav changes <video> --roi <its box> --min 1` lists every frame the box changes. Read the words off those frames.
- A flash, dissolve or wipe at a cut belongs to the cut. Rebuild it over its measured frames.
- Follow each large word from the frame it appears to the frame it leaves with `rav frames <video> --range a,b --roi <its box>`: how it arrives (pop, scale, fade, type-on, wipe), what changes while it holds (a fill sweeping across, a counter, a colour), and how it leaves.

## Fitting text

Size and place every line from its ink box, the tight box around its dark pixels.

1. `rav ink <video> --at <held frame> --roi <box>` gives the original's box. The ROI holds this one line and nothing else; a box that touches the ROI's edge has caught a neighbour.
2. `rav still <project> --at <frame>`, then `rav ink <still> --roi <the same box>` gives yours.
3. Correct in this order: `font-size` by the ratio of the heights; `letter-spacing` by the width difference spread over the gaps between letters; then position by the box offset. A centred line shifts by the difference of the box centres, since CSS adds tracking after the last letter too.
4. Repeat until both boxes agree within 2 px, usually two or three passes. With more than a handful of lines, script the loop over all of them.

Fit each line on its own: a design often sets lines at different sizes. Share a value between lines once the fits agree.

Weight: `rav ink` also prints `fill`, the share of ink inside the box. A higher fill is a heavier weight. Between two weights of a font, thicken the lighter with `-webkit-text-stroke: 1px currentColor`.

Family: compare traits (one- or two-storey a and g, slabs on i and l, x-height, terminals), then render two or three candidates at the fitted size beside a full-size crop of the original. Use a font already on the machine or a free family saved into `assets/fonts/`, with its italic as its own file. Record substitutes in `RAV.md`.

Decide size and weight from these numbers. Crops shown at different scales mislead the eye about both.

## Look

- The backdrop covers most of a motion-graphics frame. Sample a grid of it with `rav color` at several moments and match its gradient and brightness over time.
- Fast moves carry motion blur in the original. Blur along the move while it is fast.
- Dots and pixel textures: measure the lattice (pitch, offset, colour) and draw from that grid.
- Flat graphics that move like footage (a scrolling diagram, a map under a camera): trace the plate into flat shapes and fit the camera move.

## Recipes that seek cleanly

- Fill sweeping across letters: two copies of the word, the lit one on top, its `clip-path: inset(0 100% 0 0)` animated to `inset(0 0 0 0)` over the measured frames.
- Gradient text: `background: linear-gradient(...)` with `background-clip: text` and a transparent fill.
- Outline text: `-webkit-text-stroke` at the measured width, transparent fill.
- Directional motion blur: an SVG `<feGaussianBlur>` on the moving wrapper with `stdDeviation="24 0"` for a horizontal smear, animated with the move. CSS `blur()` smears every direction.
- Ghost trail: two to four copies behind the element along its path, at opacity 0.5, 0.3 and 0.15.
- Glow: stacked `text-shadow` or `drop-shadow`, animating radius and alpha.
- Masks and wipes: `clip-path` or `mask-image` with an animated stop.
- Flash: a full-frame layer on exactly the measured frames.
- Glass letters over footage: a copy of the shot clipped by an SVG `<clipPath>` holding the text, bent with `feTurbulence` (fixed seed) into `feDisplacementMap`, with a dark rim and a light top edge.
- Grain: SVG `feTurbulence` with a fixed seed, stepped per frame. Match its look; grain never matches pixel for pixel.
