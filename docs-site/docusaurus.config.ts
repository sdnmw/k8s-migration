import type { Config } from '@docusaurus/types'
import type { Options, ThemeConfig } from '@docusaurus/preset-classic'

const baseUrl = process.env.DOCS_BASE_URL || '/'

const config: Config = {
  title: 'SKS Migration Center',
  tagline: '面向工作负载集群的迁移中心',
  favicon: 'img/brand-logo.png',
  url: process.env.DOCS_URL || 'https://docs.example.invalid',
  baseUrl,
  organizationName: 'sdnmw',
  projectName: 'k8s-migration',
  trailingSlash: true,
  onBrokenLinks: 'throw',
  onBrokenMarkdownLinks: 'throw',
  i18n: { defaultLocale: 'zh-Hans', locales: ['zh-Hans'], localeConfigs: { 'zh-Hans': { label: '简体中文' } } },
  presets: [[
    'classic',
    {
      docs: { sidebarPath: './sidebars.ts', routeBasePath: '/' },
      blog: false,
      theme: { customCss: './src/css/custom.css' },
      sitemap: false,
    } satisfies Options,
  ]],
  plugins: [
    ['@easyops-cn/docusaurus-search-local', { hashed: true, language: ['en', 'zh'], docsRouteBasePath: '/', indexDocs: true, indexBlog: false, indexPages: true, highlightSearchTermsOnTargetPage: true }],
    ['docusaurus-plugin-image-zoom', { selector: '.markdown img', background: { light: 'rgba(247,248,250,.97)', dark: 'rgba(20,24,31,.97)' } }],
  ],
  themeConfig: {
    colorMode: { defaultMode: 'light', disableSwitch: true, respectPrefersColorScheme: false },
    navbar: {
      title: 'SKS Migration Center',
      logo: { alt: 'SKS Migration Center', src: 'img/brand-logo.png' },
      items: [
        { type: 'docSidebar', sidebarId: 'guideSidebar', position: 'left', label: '文档' },
        { to: '/tutorials/compose-nextcloud/', label: '使用教程', position: 'left' },
        { to: '/installation/offline/', label: '安装指南', position: 'left' },
        { to: '/operations/health/', label: '运维指南', position: 'left' },
        ...(baseUrl === '/docs/'
          ? [{ type: 'html' as const, value: '<a class="navbar__link" href="/">返回迁移平台</a>', position: 'right' as const }]
          : [{ href: 'https://github.com/sdnmw/k8s-migration', label: '项目仓库', position: 'right' as const }]),
      ],
    },
    footer: {
      style: 'light',
      links: [
        { title: '开始使用', items: [{ label: '快速开始', to: '/quick-start/' }, { label: 'Compose 教程', to: '/tutorials/compose-nextcloud/' }, { label: 'Kubernetes 教程', to: '/tutorials/kubernetes-nextcloud/' }] },
        { title: '管理与维护', items: [{ label: '功能手册', to: '/manual/overview/' }, { label: '运维指南', to: '/operations/health/' }, { label: '故障排查', to: '/troubleshooting/' }] },
      ],
      copyright: `Copyright © ${new Date().getFullYear()} SKS Migration Center`,
    },
    prism: { additionalLanguages: ['bash', 'json', 'yaml'] },
    zoom: { selector: '.markdown img', config: { margin: 24, scrollOffset: 0 } },
  } satisfies ThemeConfig,
}

export default config
