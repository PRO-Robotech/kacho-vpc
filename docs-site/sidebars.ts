import type { SidebarsConfig } from '@docusaurus/plugin-content-docs'

const sidebars: SidebarsConfig = {
  vpcSidebar: [
    'intro',
    'getting-started',
    {
      type: 'category',
      label: 'Архитектура',
      collapsed: false,
      items: [
        'architecture/overview',
        'architecture/data-model',
        'architecture/operations',
        'architecture/ipam',
        'architecture/authz',
        'architecture/data-plane',
      ],
    },
    {
      type: 'category',
      label: 'Установка',
      collapsed: true,
      items: ['install/deploy', 'install/configuration'],
    },
    {
      type: 'category',
      label: 'API',
      collapsed: false,
      items: [
        'api/overview',
        'api/network',
        'api/subnet',
        'api/address',
        'api/route-table',
        'api/security-group',
        'api/gateway',
        'api/network-interface',
        'api/address-pool',
        'api/operations',
      ],
    },
    {
      type: 'category',
      label: 'Дополнительно',
      collapsed: true,
      items: ['advanced/observability', 'advanced/known-divergences'],
    },
  ],
}

export default sidebars
