// rav's clock. rav injects this before a composition's own scripts, when rendering and when previewing, then
// calls __rav.seek(t) for every frame. CSS and Web Animations, a GSAP timeline at window.__tl, <video> elements,
// requestAnimationFrame, performance.now and Date.now all show composition time t, never wall-clock time.
(() => {
  if (window.__rav) return;
  const realRaf = window.requestAnimationFrame.bind(window);
  const realNow = performance.now.bind(performance);
  const epoch = Date.now();
  let now = 0; // composition time, ms
  let playing = false; // preview playback: videos play instead of seeking every frame
  let queued = new Map();
  let nextId = 1;

  window.requestAnimationFrame = (cb) => {
    const id = nextId++;
    queued.set(id, cb);
    return id;
  };
  window.cancelAnimationFrame = (id) => queued.delete(id);
  performance.now = () => now;
  Date.now = () => epoch + now;

  const root = () => {
    const r = document.getElementById('rav');
    if (!r) throw new Error('no #rav element: the stage is <div id="rav" data-width data-height data-fps data-frames>');
    return r;
  };
  const num = (v, def) => (v == null || v === '' ? def : Number(v));
  const rate = () => {
    const [n, d] = String(root().dataset.fps).split('/').map(Number);
    return n / (d || 1);
  };

  function meta() {
    const d = root().dataset;
    const track = document.querySelector('audio[data-track]');
    return {
      width: num(d.width, 0),
      height: num(d.height, 0),
      fps: String(d.fps),
      frames: num(d.frames, 0),
      audio: track
        ? { src: track.getAttribute('src'), start: num(track.dataset.start, 0), offset: num(track.dataset.offset, 0) }
        : null,
    };
  }

  const once = (el, type, ms) =>
    new Promise((resolve) => {
      const done = () => {
        clearTimeout(timer);
        el.removeEventListener(type, done);
        resolve();
      };
      const timer = setTimeout(done, ms);
      el.addEventListener(type, done);
    });

  // A <video> shows from data-start (composition seconds) for data-duration, from data-offset seconds into its
  // file, at data-rate. Outside that window it is hidden.
  async function seekVideo(v, t) {
    const start = num(v.dataset.start, 0);
    // The window is judged a quarter into the frame, so a start or end written to the microsecond never lands a
    // frame early or late.
    const at = t + 0.25 / rate();
    const active = at >= start && at < start + num(v.dataset.duration, Infinity);
    v.style.visibility = active ? '' : 'hidden';
    v.muted = true;
    if (!active) {
      if (!v.paused) v.pause();
      return;
    }
    const speed = num(v.dataset.rate, 1);
    if (v.readyState < 1) await once(v, 'loadedmetadata', 10000);
    const end = Number.isFinite(v.duration) ? v.duration - 0.001 : Infinity;
    const target = num(v.dataset.offset, 0) + (t - start) * speed;
    if (playing) {
      v.playbackRate = speed;
      if (v.paused) {
        v.currentTime = Math.min(target, end);
        v.play().catch(() => {});
      } else if (Math.abs(v.currentTime - target) > 0.15) v.currentTime = Math.min(target, end);
      return;
    }
    if (!v.paused) v.pause();
    const inside = Math.min(target + 0.25 / rate(), end); // a quarter into the frame, not on its edge
    if (Math.abs(v.currentTime - inside) > 1e-6 || v.readyState < 2) {
      v.currentTime = inside;
      await once(v, 'seeked', 10000);
    }
    if (v.readyState < 2) await once(v, 'loadeddata', 10000);
  }

  // Animations are sampled just after the frame starts: Chrome treats a seek that lands exactly on a start time
  // as before it, which would show a hard switch a frame late.
  const AFTER = 0.05; // ms

  async function seek(t) {
    now = t * 1000;
    const tl = window.__tl;
    if (tl && typeof tl.seek === 'function') {
      if (typeof tl.pause === 'function') tl.pause();
      tl.seek(t + AFTER / 1000, false);
    }
    // Every animation runs on the composition clock from 0; animation-delay places it.
    for (const a of document.getAnimations()) {
      if (typeof CSSTransition !== 'undefined' && a instanceof CSSTransition) {
        a.finish();
        continue;
      }
      if (a.playState !== 'paused') a.pause();
      a.currentTime = now + AFTER;
    }
    await Promise.all([...document.querySelectorAll('video')].map((v) => seekVideo(v, t)));
    if (typeof window.__seek === 'function') await window.__seek(t);
    const callbacks = [...queued.values()];
    queued = new Map();
    for (const cb of callbacks) {
      try {
        cb(now);
      } catch (e) {
        console.error(e);
      }
    }
    if (playing) return;
    await document.fonts.ready;
    await Promise.all([...document.images].filter((i) => !i.complete).map((i) => i.decode().catch(() => {})));
    await new Promise((resolve) => {
      const timer = setTimeout(resolve, 200);
      realRaf(() => realRaf(() => {
        clearTimeout(timer);
        resolve();
      }));
    });
  }

  window.__rav = {
    seek,
    meta,
    realRaf,
    realNow,
    get time() {
      return now / 1000;
    },
    get playing() {
      return playing;
    },
    set playing(v) {
      playing = !!v;
    },
  };
})();
