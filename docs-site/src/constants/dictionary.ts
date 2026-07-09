// Словарь описаний полей — единый источник для таблиц запроса/ответа во всех
// API-страницах (DRY: одно описание поля переиспользуется на всех ресурсах).
export const DICTIONARY = {
  id: { short: 'Идентификатор ресурса — TEXT: 3-символьный префикс + 17-символьный crockford-base32 (output-only, генерируется сервером)' },
  projectId: { short: 'Идентификатор проекта kacho-iam, которому принадлежит ресурс (обязателен при создании)' },
  name: { short: 'Имя ресурса (permissive: допустимы пустая строка / uppercase / underscore; ≤63)' },
  description: { short: 'Описание ресурса (UTF-8, ≤256)' },
  labels: { short: 'Пользовательские метки key→value (≤64 пар) для поиска ресурса' },
  createdAt: { short: 'Время создания (output-only; truncate до секунд)' },
  updateMask: { short: 'FieldMask: список изменяемых полей. Неизвестное поле / immutable → InvalidArgument; пустой mask = full-PATCH' },
  status: { short: 'Грубый статус ресурса (output-only enum)' },
  filter: { short: 'Строка фильтра (конвенция Kachō; поддерживается name="<value>")' },
  pageSize: { short: 'Размер страницы (0 → 50, max 1000)' },
  pageToken: { short: 'Opaque cursor (base64 от {created_at, id}); невалидный → InvalidArgument' },
  networkId: { short: 'Идентификатор Network, которому принадлежит ресурс' },
  subnetId: { short: 'Идентификатор Subnet' },
  zoneId: { short: 'Идентификатор зоны (region-1-a); существование валидируется через kacho-geo' },
  regionId: { short: 'Идентификатор региона (region-1); существование валидируется через kacho-geo' },
  placementType: { short: 'Дискриминатор размещения подсети: ZONAL (zone_id) или REGIONAL (region_id); обязателен и immutable' },
  v4CidrBlocks: { short: 'Список IPv4 CIDR-блоков; host-биты = 0' },
  v6CidrBlocks: { short: 'Список IPv6 CIDR-блоков' },
  deletionProtection: { short: 'Защита от удаления — при true Delete отклоняется (FailedPrecondition)' },
  securityGroupIds: { short: 'Список id SecurityGroup, привязанных к NIC' },
  v4AddressIds: { short: 'Список id IPv4-Address (≤1 на NIC)' },
  v6AddressIds: { short: 'Список id IPv6-Address (≤1 на NIC)' },
  macAddress: { short: 'MAC-адрес NIC (output-only, аллоцируется сервером, уникален в облаке)' },
  usedBy: { short: 'Ссылка «кто использует ресурс» (output-only denormalised mirror)' },
} as const

export type DictKey = keyof typeof DICTIONARY
