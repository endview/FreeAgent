import { defineConfig } from 'vite';

// KaTeX lists woff2, woff and ttf for every face. WebView2 and current
// browsers load woff2, so the other formats are dropped before Vite resolves
// url() references and never reach dist.
function katexWoff2Only() {
  return {
    name: 'katex-woff2-only',
    enforce: 'pre',
    transform(code, id) {
      if (!/katex(\.min)?\.css$/.test(id.split('?')[0])) return null;
      return code.replace(/src\s*:\s*([^;}]+)/g, (declaration, list) => {
        const kept = list.split(/,(?=\s*url\()/).filter((item) => /\.woff2\b/.test(item));
        if (!kept.length) throw new Error(`katex-woff2-only: no woff2 source in ${id}`);
        return `src:${kept.join(',').trim()}`;
      });
    },
  };
}

// Third-party code sits in its own chunks so the app chunk stays below Vite's
// size warning and a real growth in app code still shows up in the build.
// Every chunk still loads at startup. CSS stays in the single app stylesheet.
function vendorChunk(id) {
  const path = id.split('?')[0].replaceAll('\\', '/');
  if (!path.includes('/node_modules/') || path.endsWith('.css')) return undefined;
  if (/\/node_modules\/katex\//.test(path)) return 'katex';
  if (/\/node_modules\/highlight\.js\//.test(path)) return 'highlight';
  return 'vendor';
}

export default defineConfig({
  plugins: [katexWoff2Only()],
  build: {
    assetsInlineLimit: 0,
    rollupOptions: { output: { manualChunks: vendorChunk } },
  },
});
