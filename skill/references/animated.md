# Animated and painted films

In a hand-painted or 3D-animated film the character, the world and the camera are one picture, so the layers do not come apart. Decide shot by shot: build in code what code can carry, and play the rest as footage cut from the original.

Say so before starting, in the person's language. For example:

> This film is painted, so I'll rebuild what I can in code and keep the rest as clips from the original, shot by shot. The code parts stay editable; the clips can be swapped, not changed.

## What is code and what is footage

- **Code:** text and handwriting, logos, the end card, light that sits on the picture (flashes, glows, a portal's ring), and a camera moving over a still world: measure the camera and rebuild the world as separate layers from clean plates (`rav cut ... --at t` with the moving parts painted out by `--remove`), never the whole frame.
- **Footage:** a character acting, a picture that morphs, and fine flowing detail such as streamlines, which code draws in the wrong shape. Cut it per shot as [footage.md](footage.md) says.
- **Both:** in a mixed shot, play the acting as footage and lay the code over it.

## Reading the cuts

Sudden light in a shot can list as a cut. Look at the frames either side of each one with `rav frames`: the same picture under different light is one shot, and its clip runs across that light change in one piece.

`RAV.md` lists which shots are footage and which layers are code.

---
Adapted from HyperFrames' `reverse-any-video` skill (Apache-2.0, see `third_party/hyperframes/LICENSE`); its generated-shot recipe is replaced by footage cut from the original.
