// KaTeX lists woff2, woff and ttf for every face. WebView2 and current
// browsers load woff2, so the other formats are dropped before Vite resolves
// url() references and never reach dist.
export function katexWoff2Only() {
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

