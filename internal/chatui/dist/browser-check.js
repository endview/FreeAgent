(function () {
  var agent = navigator.userAgent, match, supported = !!(window.CSS && CSS.supports && CSS.supports('color', 'oklch(0.5 0.2 240)'));
  if ((match = /(?:Chrome|Chromium|Edg)\/(\d+)/.exec(agent))) supported = supported && Number(match[1]) >= 111;
  else if ((match = /Firefox\/(\d+)/.exec(agent))) supported = supported && Number(match[1]) >= 128;
  else if ((match = /Version\/(\d+)\.(\d+).*Safari\//.exec(agent))) supported = supported && (Number(match[1]) > 16 || Number(match[1]) === 16 && Number(match[2]) >= 4);
  if (supported) return;
  document.addEventListener('DOMContentLoaded', function () {
    var note = document.createElement('p');
    note.setAttribute('role', 'alert');
    note.className = 'browser-update';
    note.textContent = '当前浏览器版本较旧，请升级到 Chrome / Edge 111、Firefox 128、Safari 16.4 或更新版本后使用 FreeAgent。';
    document.body.insertBefore(note, document.body.firstChild);
  });
})();
