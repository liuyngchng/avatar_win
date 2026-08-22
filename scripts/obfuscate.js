// obfuscate.js — 混淆 web/index.html 中的内联 <script type="module"> JS。
//
// 由 build.ps1 在构建前调用，就地混淆 index.html 的内联脚本，构建完成后
// build.ps1 会恢复原始文件。本脚本不修改任何其他文件。
//
// 关键点：
//   - 只混淆内联 module 脚本，不动 importmap 和第三方库（js/ 下已公开）。
//   - goBridge_* 与 handleMessage 是 Go↔JS 桥接名，必须保留。
//   - import 绑定名（THREE/GLTFLoader 等）必须保留，否则引用断链。
//
// 混淆强度刻意保持"中等"：只做标识符重命名（十六进制），这是防止
// 从 exe 提取可读源码的核心手段；关闭字符串加密与控制流扁平化，避免
// 破坏 Go↔JS 桥接、也避免拖慢 3D 动画的 60fps 渲染循环。

const fs = require('fs');
const path = require('path');
const JavaScriptObfuscator = require('javascript-obfuscator');

const indexHtmlPath = path.join(__dirname, '..', 'web', 'index.html');
const html = fs.readFileSync(indexHtmlPath, 'utf8');

// 匹配内联 module 脚本（含 import 的那个，不是 importmap）。
const scriptRe = /<script type="module">([\s\S]*?)<\/script>/;
const match = html.match(scriptRe);
if (!match) {
  console.error('obfuscate: no inline <script type="module"> found in web/index.html');
  process.exit(1);
}
const code = match[1];

const result = JavaScriptObfuscator.obfuscate(code, {
  compact: true,
  sourceType: 'module',

  // 标识符重命名（十六进制）：让所有局部变量名失去语义，这是混淆的核心。
  identifierNamesGenerator: 'hexadecimal',
  renameGlobals: false, // 全局变量不动，避免破坏 window.* 桥接

  // 字符串加密：关闭。
  // 开启后 obfuscator 会把所有字符串字面量（包括 window.goBridge_xxx 这种
  // 属性访问 key）移入加密数组，导致 Go 端 Eval/Bind 找不到桥接函数。
  // JS 字符串的防护价值有限（业务逻辑在 Go 端，garble -literals 已处理），
  // 标识符重命名才是混淆 JS 的核心手段。
  stringArray: false,

  // 性能敏感：关闭控制流扁平化与死代码注入，保持渲染帧率。
  controlFlowFlattening: false,
  deadCodeInjection: false,
  selfDefending: false,
  debugProtection: false,
  disableConsoleOutput: false,
  transformObjectKeys: false,

  reservedNames: [
    // import 绑定名
    'THREE', 'GLTFLoader', 'FBXLoader', 'VRMLoaderPlugin', 'VRMUtils',
    // Go↔JS 桥接（Go 端 Eval 与 Bind 依赖这些名字）
    'goBridge_sendEvent', 'goBridge_moveWindow', 'goBridge_setWindowSize',
    'handleMessage',
  ],
});

const obfuscated = result.getObfuscatedCode();
const outHtml = html.replace(scriptRe, `<script type="module">\n${obfuscated}\n</script>`);
fs.writeFileSync(indexHtmlPath, outHtml, 'utf8');
console.log('obfuscate: web/index.html inline JS obfuscated OK');
