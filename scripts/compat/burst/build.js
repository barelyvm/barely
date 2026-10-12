// Stand-in for a heavy bundler run: fills the V8 heap with small objects
// (like a module graph and ASTs), then drops them. With "serve" it keeps
// running afterwards, the way a dev server or `build --watch` does.
const target = Number(process.env.HEAP_MB || 4200) * 1024 * 1024;
const mode = process.argv[2] || 'once';

function build() {
  let graph = [];
  while (process.memoryUsage().heapUsed < target) {
    const chunk = new Array(50000);
    for (let i = 0; i < chunk.length; i++) {
      chunk[i] = { id: i, src: 'x'.repeat(40) + i, deps: [i - 1, i + 1] };
    }
    graph.push(chunk);
  }
  const peak = process.memoryUsage();
  console.log(`BUILD peak heapUsed=${(peak.heapUsed / 2 ** 20) | 0}MB rss=${(peak.rss / 2 ** 20) | 0}MB`);
  graph = null;
}

build();
if (mode === 'serve') {
  if (global.gc) {
    global.gc();
  }
  const keep = Buffer.alloc(200 * 1024 * 1024, 1);
  setInterval(() => {
    const m = process.memoryUsage();
    console.log(`SERVE heapUsed=${(m.heapUsed / 2 ** 20) | 0}MB rss=${(m.rss / 2 ** 20) | 0}MB keep=${keep.length >> 20}MB`);
  }, 10000);
}
