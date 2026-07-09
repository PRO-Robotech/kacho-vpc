// Правила валидации полей — единый источник для компонента <Restrictions />.
export const RESTRICTIONS = {
  name: [
    "regex ^([a-zA-Z]([-_a-zA-Z0-9]{0,61}[a-zA-Z0-9])?)?$",
    'длина 0..63; пустая строка / uppercase / underscore допустимы (permissive — конвенция Kachō)',
  ],
  nameGateway: [
    "regex ^([a-z]([-a-z0-9]{0,61}[a-z0-9])?)?$ (strict: lowercase, без uppercase/underscore)",
    'длина 0..63; пустая строка допустима',
  ],
  description: ['UTF-8 длина ≤ 256'],
  labels: [
    '≤ 64 пар',
    "key: ^[a-z][-_./\\@a-z0-9]{0,62}$ (1..63 байт)",
    'value: ≤ 63 байт (пустое значение допустимо)',
  ],
  projectId: ['обязателен при Create', 'существование проверяется через kacho-iam ProjectService.Get'],
  cidr: [
    'валидный CIDR-префикс',
    'host-биты = 0 (10.0.0.0/24 — OK; 10.0.0.5/24 → InvalidArgument)',
    'CIDR не должен пересекаться с соседними Subnet (EXCLUDE-constraint, FailedPrecondition)',
  ],
  zoneId: ['для Subnet — обязателен при ZONAL-placement (region_id должен быть пуст); для external Address — при неявном адресе', 'immutable после Create', 'существование валидируется через kacho-geo ZoneService.Get'],
  regionId: ['для Subnet — обязателен при REGIONAL-placement (zone_id должен быть пуст)', 'immutable после Create', 'существование валидируется через kacho-geo RegionService.Get'],
  placementType: ['обязателен при Create: ZONAL | REGIONAL', 'UNSPECIFIED → InvalidArgument (не дефолтит в ZONAL)', 'immutable после Create'],
  updateMask: [
    'неизвестное поле → InvalidArgument',
    'hard-immutable поле в mask → InvalidArgument («<field> is immutable after <Resource>.Create»)',
    'пустой mask → full-PATCH (immutable из тела silently игнорируются)',
  ],
  pagination: ['page_size: 0 → 50, max 1000', 'page_token: opaque base64; невалидный → InvalidArgument'],
  resourceId: ["нераспознанный 3-char префикс → InvalidArgument «invalid <res> id '<X>'»"],
  nicCardinality: ['≤ 1 IPv4 и ≤ 1 IPv6 на NIC (DB-level CHECK + sync-валидация)'],
} as const

export type RestrictionKey = keyof typeof RESTRICTIONS
