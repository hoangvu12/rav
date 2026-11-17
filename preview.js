// rav preview: fits the stage to the window and puts a scrubber under it. Space plays, arrows step a frame
// (shift: a second).
(() => {
  const r = window.__rav;
  const stage = document.getElementById('rav');
  const m = r.meta();
  const [n, d] = String(m.fps).split('/').map(Number);
  const fps = n / (d || 1);
  const track = document.querySelector('audio[data-track]');
  const trackStart = () => Number(track?.dataset.start || 0);
  const trackOffset = () => Number(track?.dataset.offset || 0);

  document.documentElement.style.cssText += ';height:100%;background:#111;overflow:hidden';
  document.body.style.cssText +=
    ';margin:0;height:100%;display:flex;align-items:center;justify-content:center;background:#111';
  const bar = document.createElement('div');
  bar.style.cssText =
    'position:fixed;left:0;right:0;bottom:0;display:flex;gap:12px;align-items:center;padding:10px 16px;' +
    'background:#000d;color:#eee;font:12px ui-monospace,Menlo,Consolas,monospace;z-index:2147483647';
  bar.innerHTML = '<button style="font:inherit;width:64px">play</button><input type="range" min="0" step="1" style="flex:1"><span></span>';
  document.body.append(bar);
  const [btn, range, label] = bar.children;
  range.max = m.frames - 1;

  function fit() {
    const s = Math.min(innerWidth / m.width, (innerHeight - 56) / m.height, 1);
    stage.style.flex = 'none';
    stage.style.transform = `translateY(-24px) scale(${s})`;
  }

  let want = 0;
  let running = false;
  let playing = false;
  let f0 = 0;
  let t0 = 0;
  async function show(k) {
    want = Math.max(0, Math.min(m.frames - 1, k));
    range.value = want;
    label.textContent = `${(want / fps).toFixed(3)} s   f${want} / ${m.frames - 1}`;
    if (running) return;
    running = true;
    try {
      let shown;
      do {
        shown = want;
        await r.seek(shown / fps);
      } while (shown !== want);
    } finally {
      running = false;
    }
  }

  function tick() {
    if (!playing) return;
    const k =
      track && !track.paused
        ? Math.floor((track.currentTime - trackOffset() + trackStart()) * fps)
        : f0 + Math.floor(((r.realNow() - t0) / 1000) * fps);
    if (k >= m.frames) {
      pause();
      show(m.frames - 1);
      return;
    }
    show(k);
    r.realRaf(tick);
  }

  function play() {
    if (want >= m.frames - 1) want = 0;
    playing = r.playing = true;
    btn.textContent = 'pause';
    f0 = want;
    t0 = r.realNow();
    if (track) {
      const at = f0 / fps - trackStart() + trackOffset();
      if (at >= 0) {
        track.currentTime = at;
        track.play().catch(() => {});
      }
    }
    r.realRaf(tick);
  }

  function pause() {
    playing = r.playing = false;
    btn.textContent = 'play';
    track?.pause();
    show(want);
  }

  btn.onclick = () => (playing ? pause() : play());
  range.oninput = () => {
    if (playing) pause();
    show(Number(range.value));
  };
  addEventListener('keydown', (e) => {
    if (e.code === 'Space') {
      e.preventDefault();
      playing ? pause() : play();
    } else if (e.code === 'ArrowLeft' || e.code === 'ArrowRight') {
      e.preventDefault();
      if (playing) pause();
      const step = e.shiftKey ? Math.round(fps) : 1;
      show(want + (e.code === 'ArrowLeft' ? -step : step));
    }
  });
  addEventListener('resize', fit);
  const start = () => {
    fit();
    show(0);
  };
  if (document.readyState === 'complete') start();
  else addEventListener('load', start);
})();
