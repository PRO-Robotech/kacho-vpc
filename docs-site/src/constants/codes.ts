// gRPC error codes — единый словарь для компонента <Codes />.
// kacho-vpc маппит repo-sentinel'ы в gRPC-коды через serviceerr.MapRepoErr;
// формат ошибки (конвенция Kachō) — google.rpc.Status {code, message, details:[]}.
export const CODES = {
  invalidArgument: {
    grpc: 'INVALID_ARGUMENT',
    http: '400',
    when: 'Некорректный аргумент: формат id, regex имени, host-биты CIDR, неизвестное поле update_mask, immutable-поле в mask',
  },
  notFound: {
    grpc: 'NOT_FOUND',
    http: '404',
    when: 'Ресурс с указанным id не существует (well-formed id, но строки нет)',
  },
  alreadyExists: {
    grpc: 'ALREADY_EXISTS',
    http: '409',
    when: 'Нарушение UNIQUE — дубль (project_id, name) для ресурса',
  },
  failedPrecondition: {
    grpc: 'FAILED_PRECONDITION',
    http: '400',
    when: 'Состояние не позволяет операцию: CIDR overlap, «network is not empty», deletion_protection, IPAM pool not resolved',
  },
  unavailable: {
    grpc: 'UNAVAILABLE',
    http: '503',
    when: 'Недоступен peer-сервис (kacho-iam / kacho-compute) при валидации на request-path (fail-closed)',
  },
  unauthenticated: {
    grpc: 'UNAUTHENTICATED',
    http: '401',
    when: 'Отсутствует / невалиден JWT (проверяется на api-gateway)',
  },
  permissionDenied: {
    grpc: 'PERMISSION_DENIED',
    http: '403',
    when: 'Субъект не имеет нужного отношения (relation) на ресурс/проект в OpenFGA (authz-интерсептор)',
  },
  internal: {
    grpc: 'INTERNAL',
    http: '500',
    when: 'Внутренняя ошибка БД — текст не раскрывается («internal database error»)',
  },
} as const

export type CodeKey = keyof typeof CODES
