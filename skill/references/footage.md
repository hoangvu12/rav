# Footage

Footage is what HTML cannot draw: people, places, products in motion, 3D renders. It plays as clips cut from the original, with every drawn thing on top of it painted out and rebuilt in HTML. That separation is what makes the rebuild editable.

1. **One clip per shot**: `rav cut <video> -o assets/shot03.mp4 --from <its cut> --to <the next cut>`. A clip starts at its shot's cut and ends at the next, so the cuts stay exactly where the original has them.
2. **Paint out the drawn things** burned into the shot: `--remove "x,y,w,h;x,y,w,h"`, each box tight around one drawn element (the text's own box, a sticker, a logo bug). The painting fills from the surrounding pixels, so a tight box keeps the picture clean. A drawn element that moves gets a box covering its whole path, or the shot is cut into stretches where it holds still.
3. **Rebuild every painted-out element** in HTML on top of the clip, at its own frames.
4. **Text that passes behind a person** stays burned into the footage; record it in `RAV.md`.
5. **Animated or painted films** are decided shot by shot: flat-colour vector shapes in simple motion are drawn layers (SVG and CSS); painted, textured or 3D shots are footage.

A footage shot's SSIM sits below a drawn shot's, since the clip is re-encoded and painted boxes are approximate. Judge it by the triptych: the right frames, the cuts in place, the drawn things rebuilt above it.
