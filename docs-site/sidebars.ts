import type { SidebarsConfig } from '@docusaurus/plugin-content-docs'

const sidebars: SidebarsConfig = {
  guideSidebar: [
    { type: 'doc', id: 'index', label: '文档首页' },
    { type: 'category', label: '快速开始', collapsed: false, items: ['quick-start/index', 'quick-start/first-migration'] },
    { type: 'category', label: '核心概念', items: ['concepts/platform', 'concepts/migration-lifecycle', 'concepts/storage-and-mapping', 'concepts/status-and-evidence'] },
    { type: 'doc', id: 'features/index', label: '关键特性' },
    { type: 'category', label: '使用教程', collapsed: false, items: ['tutorials/compose-nextcloud', 'tutorials/kubernetes-nextcloud', 'tutorials/strategy-selection', 'tutorials/retry-and-recovery'] },
    { type: 'category', label: '功能操作手册', items: ['manual/overview', 'manual/migrations', 'manual/environments', 'manual/mappings', 'manual/settings', 'manual/account'] },
    { type: 'category', label: '安装指南', items: ['installation/offline', 'installation/upgrade-uninstall'] },
    { type: 'category', label: '运维指南', items: ['operations/health', 'operations/addons-storage', 'operations/security-backup'] },
    { type: 'category', label: '故障排查与参考', items: ['troubleshooting/index', 'troubleshooting/reference'] },
  ],
}

export default sidebars
