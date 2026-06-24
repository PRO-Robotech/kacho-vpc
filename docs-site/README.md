# Kachō VPC — продуктовая документация

Документация control-plane сервиса **kacho-vpc** на [Docusaurus](https://docusaurus.io/).
Структура и стиль — по образцу [`PRO-Robotech/sgroups-docs`](https://github.com/PRO-Robotech/sgroups-docs).

## Структура

```
docs-site/
├── docs/
│   ├── intro.mdx                 # обзор продукта
│   ├── architecture/             # overview · data-model · operations · ipam · authz
│   ├── api/                      # overview + per-resource (Network/Subnet/Address/...) + operations
│   ├── install/                  # deploy · configuration
│   └── advanced/                 # observability · known-divergences
├── src/
│   ├── components/commonBlocks/  # ApiOperation · Restrictions · Codes · StatusTable
│   ├── constants/                # dictionary · types · restrictions · codes (DRY)
│   └── css/custom.css
├── docusaurus.config.ts
└── sidebars.ts
```

## Локальный запуск

```bash
cd docs-site
npm install
npm run start      # dev-сервер http://localhost:3000
npm run build      # production-сборку в build/
npm run serve      # отдать собранный build/
```

## Источники истины

Контент выведен из:
- `kacho-proto/proto/kacho/cloud/vpc/v1/*.proto` — контракт API;
- `kacho-vpc/docs/architecture/` — data-flows, conventions, ER-диаграмма;
- исходный код сервиса (`internal/`) — поведение, инварианты, error-тексты.
