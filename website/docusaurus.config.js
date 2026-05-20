const {themes: prismThemes} = require('prism-react-renderer');

/** @type {import('@docusaurus/types').Config} */
const config = {
  title: 'gpm',
  tagline: 'Manifest-driven addon management for Godot projects',
  url: 'https://cafecito-games.github.io',
  baseUrl: '/godot-package-manager/',
  organizationName: 'cafecito-games',
  projectName: 'godot-package-manager',
  onBrokenLinks: 'throw',
  markdown: {
    hooks: {
      onBrokenMarkdownLinks: 'warn',
    },
  },
  trailingSlash: false,
  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },
  presets: [
    [
      'classic',
      {
        docs: {
          sidebarPath: require.resolve('./sidebars.js'),
          routeBasePath: 'docs',
          editUrl: 'https://github.com/cafecito-games/godot-package-manager/tree/main/website/',
          showLastUpdateAuthor: false,
          showLastUpdateTime: false,
        },
        blog: false,
        theme: {
          customCss: require.resolve('./src/css/custom.css'),
        },
      },
    ],
  ],
  themeConfig: {
    metadata: [
      {
        name: 'description',
        content:
          'Documentation for gpm, a command-line addon manager for Godot projects.',
      },
    ],
    navbar: {
      title: 'gpm',
      items: [
        {
          type: 'docSidebar',
          sidebarId: 'docsSidebar',
          position: 'left',
          label: 'Docs',
        },
        {
          type: 'docsVersionDropdown',
          position: 'right',
        },
        {
          href: 'https://github.com/cafecito-games/godot-package-manager',
          label: 'GitHub',
          position: 'right',
        },
      ],
    },
    footer: {
      style: 'dark',
      links: [
        {
          title: 'Docs',
          items: [
            {label: 'Quickstart', to: '/docs/quickstart'},
            {label: 'Manifest', to: '/docs/manifest'},
            {label: 'Commands', to: '/docs/commands'},
          ],
        },
        {
          title: 'Project',
          items: [
            {
              label: 'Cafecito Games',
              href: 'https://www.cafecito.games/',
            },
            {
              label: 'GitHub',
              href: 'https://github.com/cafecito-games/godot-package-manager',
            },
            {
              label: 'Releases',
              href: 'https://github.com/cafecito-games/godot-package-manager/releases',
            },
          ],
        },
      ],
      copyright: `Copyright © ${new Date().getFullYear()} <a href="https://www.cafecito.games/">Cafecito Games LLC</a>.`,
    },
    prism: {
      theme: prismThemes.github,
      darkTheme: prismThemes.dracula,
      additionalLanguages: ['bash', 'toml', 'yaml'],
    },
  },
};

module.exports = config;
