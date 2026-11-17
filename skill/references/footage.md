# Footage

Footage is what HTML cannot draw: people, places, screen recordings, products in motion, 3D renders. It plays as clips cut from the original. Drawn things burned into it are painted out and rebuilt on top, so they stay editable.

1. Cut one clip per stretch: `rav cut <video> -o assets/<name>.mp4 --from <first frame> --to <next boundary>`. `--to` is the first frame left out, so the cuts land exactly where the original has them.
2. Paint out each drawn thing burned into the stretch with `--remove "x,y,w,h;x,y,w,h"`, one tight box per element (a caption line, a sticker, a logo bug). The fill comes from the pixels around each box, so tight boxes on a plain surround leave the cleanest picture.
3. `--remove` covers the whole clip. When a caption shows for only part of a shot, split the shot at the frames it appears and leaves, and paint only the stretch that has it. A drawn thing that moves gets a box over its whole path, or more stretches.
4. Rebuild every painted-out element on top of its clip, on its own frames, as [drawn.md](drawn.md) says.
5. Text that passes behind a person stays in the footage. Record it in `RAV.md`.

A footage shot scores a little below a drawn one, because the clip is re-encoded and painted boxes are approximate. Judge it by its difference pictures: the right frames, the cuts in place, and the drawn things rebuilt above it.

## Painted and 3D-animated films

In a painted or 3D film the character, the world and the camera are one picture. Decide shot by shot, and tell the person before starting that painted shots stay clips they can swap but not edit.

- Code: text and handwriting, logos, the end card, light laid over the picture (flashes, glows), and a camera moving over a still world. Rebuild that world as layers from clean plates (`rav cut --at` with the moving parts painted out) and fit the camera move.
- Footage: a character acting, a picture that morphs, fine flowing detail.
- Both: in a mixed shot, footage underneath and code on top.

Sudden light inside a shot can list as a cut. Look at the frames either side: the same picture under new light is one shot, cut as one clip.

`RAV.md` lists which shots are footage and which layers are code.
