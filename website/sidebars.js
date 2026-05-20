/** @type {import('@docusaurus/plugin-content-docs').SidebarsConfig} */
const sidebars = {
  docsSidebar: [
    'overview',
    'quickstart',
    'installation',
    'project-layout',
    {
      type: 'category',
      label: 'Usage',
      items: ['manifest', 'commands', 'sources', 'lockfile'],
    },
    {
      type: 'category',
      label: 'Automation',
      items: ['authentication', 'json-output', 'troubleshooting'],
    },
    'development',
    'releases',
  ],
};

module.exports = sidebars;
