# rav

Reverse any video. rav rebuilds a video as an HTML page that renders back to the same video, frame for frame. You can then edit the page like any web page and render it again.

https://github.com/user-attachments/assets/d43b66a9-143e-4e94-8fd8-39bda43f1fe8

The original on the left, rav's rebuild on the right, with sound. Details are under [Example](#example).

rav is an agent skill plus a small command-line tool. The agent measures the original with the tool, writes the page, renders it, and diffs the render against the original until they match. The tool is one Go binary with no dependencies. It drives the ffmpeg and Chrome already on your machine and makes no network calls.

## Install

You need Go 1.24 or later, ffmpeg, and Chrome, Edge, Brave or Chromium.

```sh
go install github.com/hoangvu12/rav@latest
git clone https://github.com/hoangvu12/rav
cp -r rav/skill ~/.claude/skills/rav
```

On Windows, copy the skill with `Copy-Item -Recurse rav\skill $HOME\.claude\skills\rav`.

## Use

Ask your agent to rebuild a video:

> Rebuild launch.mp4 with rav.

It leaves a project folder:

```
launch/
  index.html   the rebuild, with text, colours, fonts and timings as CSS variables
  assets/      fonts, and clips cut from the original
  RAV.md       shot list, what was rebuilt or approximated, how to edit
```

Change a word or a colour in `index.html`, then run `rav render launch -o out.mp4`. Run `rav serve launch` to scrub through it in the browser.

## Example

The test video is [Opennote's launch video](https://www.youtube.com/watch?v=yU3HqFyj2DI): 54 seconds of text cards, captions over screen recordings, emojis and a logo. The rebuild has 44 lines of live text, 18 clips and 5 pictures cut from the original. These are some of the commands the agent ran:

```sh
rav init opennote --video opennote.mp4         # 1920x1080 stage, 29.97 fps, 1605 frames
rav frames opennote.mp4 --range 0f,30f         # every frame of the opening fade
rav ink opennote.mp4 --at 53f --roi 0,450,1920,170
                                               # exact pixel box of "interactive."
rav cut opennote.mp4 -o assets/ui-intro.mp4 --from 95f --to 120f
rav render . -o out.mp4
rav diff opennote.mp4 out.mp4
```

The final diff:

```
frames   1605 vs 1605
SSIM     mean 0.985  p05 0.940  min 0.920
colour   ΔE mean 0.0  p95 0.1  (2 is just visible, 10 obvious)
```

Every line of text sits within 2 px of the original and appears on the same frame. The screen-recording shots score 0.99 to 1.00. The text cards score 0.93 to 0.98 because of the typeface. The original uses a font we could not get, so the rebuild uses IBM Plex Mono, sized and weighted from measurements. The whole video renders in 47 seconds.

## Commands

```
rav probe   <video>                         size, frame rate, frames, sound
rav cuts    <video>                         every cut, jump cut and punch-in
rav frames  <video> --range 80f,90f         consecutive frames and a contact sheet
rav color   <video> --at 2.5 120,40,3       exact colours
rav changes <video> --roi x,y,w,h           every frame a box changes
rav ink     <video> --at 2.5 --roi x,y,w,h  the exact box of a line of text
rav cut     <video> -o assets/x.mp4 ...     a cropped clip or still, with marks painted out
rav init    <project> --video <video>       a stage at the original's size, rate and length
rav serve   <project>                       preview with a scrubber
rav still   <project> --at 1.2,3.4          single frames
rav render  <project> -o out.mp4            render, with the original's sound
rav diff    <video> out.mp4                 per-frame SSIM and colour error, cuts, worst moments
rav diff    <video> <project> --at 1.2,3.4  the same at a few frames, without a full render
```

Times are seconds (`2.5`) or frames (`75f`). `rav help` lists every flag.

## How it works

rav injects a small script into the page that replaces its clock. CSS animations, Web Animations, a GSAP timeline at `window.__tl`, `<video>` elements, `requestAnimationFrame`, `performance.now` and `Date.now` all read a time that rav sets for each frame. The same frame always renders the same way.

To render, rav opens one headless browser window per worker. Each worker seeks the page, takes a screenshot, and pipes the frames into ffmpeg. rav joins the pieces and adds the original's sound.

`rav diff` scores each frame with SSIM over a 4 by 4 grid, plus CIE ΔE colour error per cell, because SSIM alone barely notices a wrong flat colour. It also matches cuts, flags content that is missing, and saves the worst frames as original, rebuild and difference side by side.

## Credits

The idea comes from HeyGen's HyperFrames [`reverse-any-video`](https://github.com/heygen-com/hyperframes/tree/main/skills/reverse-any-video) skill. rav's code is its own. `skill/references/match.md` and `animated.md` are adapted from that skill's references under Apache-2.0, with the license in `third_party/hyperframes/LICENSE`.

Rebuild videos you have the right to use.
