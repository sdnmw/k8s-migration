#!/usr/bin/env node
// Render the existing MDX tutorials as standalone GitHub Markdown.
// Text edits are emitted as an apply_patch patch; --copy-assets only copies PNGs.
import fs from 'node:fs';
import path from 'node:path';
import {fileURLToPath} from 'node:url';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const shots = JSON.parse(fs.readFileSync(path.join(root, 'docs-site/src/data/guide-screenshots.json'), 'utf8'));
const used = new Set();
const assetDir = 'docs/images/nextcloud-tutorial';

function screenshot(id) {
  const shot = shots.find(item => item.id === Number(id));
  if (!shot || !/^[\w-]+\.png$/.test(shot.file)) throw new Error(`Invalid screenshot ${id}`);
  used.add(shot.file);
  return `![图 ${shot.id}：${shot.title}](./${assetDir}/${shot.file})\n\n` +
    `*图 ${shot.id}：${shot.title}。点击图片可查看原图。*\n\n` +
    `- **操作**：${shot.action}\n- **预期结果**：${shot.expected}\n- **失败处理**：${shot.failure}\n`;
}

function setup(kind) {
  const compose = kind === 'compose';
  return `## 开始前：登录并检查迁移环境\n\n` +
    `本教程面向 v0.5.0。截图来自已完成的实验室迁移，环境名称、地址、端口和任务编号仅作示例；实际填写自己的环境。已登记的环境无需重复添加。\n\n` +
    `1. 浏览器打开部署脚本输出的平台地址，使用管理员账号登录。新安装默认密码见安装说明；升级保留已有密码，不会重置。登录后从右上角管理员菜单修改初始密码。\n` +
    `2. 进入 **环境 → 源环境**，点击 **添加源环境**，选择 **${compose ? 'Docker Compose' : 'Kubernetes'}**。${compose ? '填写 SSH 地址、用户及认证信息。SSH 用户需要能够操作 Docker 并读取 Compose、.env 和引用文件；如校验主机指纹，应通过可信渠道核对，不要盲目信任陌生主机。' : '导入 sida 工作负载集群的完整 kubeconfig 内容，而不是在输入框填写文件路径；不要导入 Cluster API 管控集群。'}\n` +
    `3. 保存后点击 **测试连接**，确认已连接；再点击 **能力发现**，核对版本和${compose ? '项目目录、配置路径及 Docker/Compose 能力' : '存储、网络及迁移组件能力'}。\n` +
    `4. 进入 **环境 → SmartX SKS**，导入目标工作负载集群 kubeconfig，测试连接并刷新能力快照。确认集群有足够容量，且节点能访问迁移镜像仓库。\n` +
    `5. 进入 **系统设置 → 对象存储**，确认已有对象存储连接正常、可读写且空间充足。${compose ? '源端与平台必须能访问用于迁移的对象存储；镜像仓库凭证需允许推送。' : '源端和目标端都要准备 Velero、node-agent 与同一迁移对象存储；发现组件缺失时，先在环境操作中修复迁移组件，再重新检查。'}\n` +
    `6. 本教程使用独立目标 Namespace。进入 **迁移策略 → 资源映射**，为目标 SKS 配置下文的 Namespace 规则，再在创建迁移时选择该映射。一般迁移不强制填写映射。\n\n` +
    screenshot(1) + '\n' + screenshot(4) + '\n' + screenshot(compose ? 24 : 23) + '\n' +
    screenshot(compose ? 35 : 36) + '\n' + screenshot(5) + '\n' + screenshot(10) + '\n' + screenshot(6) +
    `\n> 安全提示：不要把 SSH 密码、Token、Secret 值、私钥或 kubeconfig 提交到仓库。迁移会停止源业务，先安排维护窗口。教程结束后的源端恢复只适用于实验验证；生产切流后不要同时把源端和目标端接入流量，避免双写。\n\n`;
}

const configs = [
  {kind: 'compose', source: 'compose-nextcloud.md', output: 'Compose-Nextcloud-迁移教程.md', other: 'Kubernetes-Nextcloud-迁移教程.md'},
  {kind: 'kubernetes', source: 'kubernetes-nextcloud.md', output: 'Kubernetes-Nextcloud-迁移教程.md', other: 'Compose-Nextcloud-迁移教程.md'},
];
const results = configs.map(config => {
  let content = fs.readFileSync(path.join(root, 'docs-site/docs/tutorials', config.source), 'utf8');
  content = content.replace(/^---\n[\s\S]*?\n---\n/, '').replace(/^import .+\n/gm, '').trimStart();
  const headingEnd = content.indexOf('\n');
  content = content.slice(0, headingEnd) + '\n\n' +
    `[返回 README](./README.md) · [平台使用指引](./平台使用指引.md) · [另一套 Nextcloud 教程](./${config.other})\n\n` + content.slice(headingEnd).trimStart();
  content = content.replace(/:::caution ([^\n]+)\n([^\n]+)\n:::/g, '> **$1**：$2');
  content = content.replace(/<OfflineDownload path="([^"]+)" label="([^"]+)" \/>/g, '[$2](.$1)');
  content = content.replace('文档站内提供 [Nextcloud Kubernetes 示例清单（离线下载）]', '仓库提供 [Nextcloud Kubernetes 示例清单（下载后保存到本地）]');
  content = content.replace(/<GuideShot id=\{(\d+)\}[^\n]*?\/>/g, (_, id) => screenshot(id));
  content = content.replace('## 1. ', setup(config.kind) + '## 1. ');
  content = content.replace('### Compose 等待人工切流', '### 创建任务与等待人工切流（按顺序）');
  content = content.replace('4. 记录文件大小与 SHA-256；迁移后必须使用相同方法复核。', '4. 下载源文件并记录文件大小与 SHA-256；迁移后下载目标文件，用相同方法复核。Linux 可用 `sha256sum compose-nextcloud-migration.txt`，macOS 可用 `shasum -a 256 compose-nextcloud-migration.txt`。');
  content = content.replace('4. 记录大小和 SHA-256。', '4. 下载源文件并记录大小和 SHA-256。Linux 可用 `sha256sum k8s-nextcloud-migration.txt`，macOS 可用 `shasum -a 256 k8s-nextcloud-migration.txt`；迁移后下载目标文件再次执行。');
  content += '\n## 任务操作与结束检查\n\n' + screenshot(34) +
    '\n- **恢复源端**：只恢复停止的源服务或副本，不删除目标；恢复完成后检查源应用可访问。\n' +
    '- **编辑任务 / 重新执行**：先根据执行时序中的具体错误修正配置，再创建新的执行记录；不要把旧记录覆盖成新记录。\n' +
    '- **下载完整报告**：保存资源验证、卷传输与切流证据，便于交接和排查。\n' +
    '- **删除任务 / 清理备份**：确认不再需要追溯和恢复后再操作，先核对确认框的删除范围；不要把删除记录当成回滚业务。\n' +
    '- **判断成功**：目标资源验证通过、文件校验一致、目标业务可登录；不能仅以进度 100% 或 Pod Running 判断数据迁移成功。\n';
  if (/<GuideShot|<OfflineDownload|^import |^:::/m.test(content)) throw new Error(`Unconverted MDX in ${config.output}`);
  return {file: config.output, content};
});

if (process.argv.includes('--patch')) {
  console.log('*** Begin Patch');
  for (const {file, content} of results) {
    if (fs.existsSync(path.join(root, file))) throw new Error(`Refuse to overwrite ${file}; use --check`);
    console.log(`*** Add File: ${file}`);
    console.log(content.trimEnd().split('\n').map(line => '+' + line).join('\n'));
  }
  console.log('*** End Patch');
} else if (process.argv.includes('--copy-assets')) {
  fs.mkdirSync(path.join(root, assetDir), {recursive: true});
  for (const file of used) fs.copyFileSync(path.join(root, 'docs-site/static/img/guide', file), path.join(root, assetDir, file));
  console.log(`Copied ${used.size} existing screenshots; no image content changed.`);
} else if (process.argv.includes('--check')) {
  for (const {file, content} of results) {
    if (fs.readFileSync(path.join(root, file), 'utf8').trimEnd() !== content.trimEnd()) throw new Error(`Tutorial out of date: ${file}`);
    for (const match of content.matchAll(/\]\((\.\/[^)]+)\)/g)) {
      if (!fs.existsSync(path.resolve(root, match[1].split('#')[0]))) throw new Error(`Missing link in ${file}: ${match[1]}`);
    }
  }
  for (const file of used) {
    const png = fs.readFileSync(path.join(root, assetDir, file));
    if (png.readUInt32BE(16) !== 1440 || png.readUInt32BE(20) !== 900) throw new Error(`Unexpected screenshot dimensions: ${file}`);
  }
  console.log(`PASS: ${results.length} standalone tutorials, ${used.size} screenshots, all local links and PNG dimensions.`);
} else {
  console.log('Usage: node scripts/export-github-tutorials.mjs --patch | --copy-assets | --check');
}
