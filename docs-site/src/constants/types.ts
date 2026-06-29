// Типы полей — для колонки «Тип» в таблицах запроса/ответа.
export const TYPES = {
  string: 'string',
  bool: 'bool',
  int32: 'int32',
  int64: 'int64',
  mapStringString: 'map<string,string>',
  stringArray: 'string[]',
  timestamp: 'google.protobuf.Timestamp',
  fieldMask: 'google.protobuf.FieldMask',
  operation: 'operation.Operation',
  reference: 'Reference',
  cidrSpec: 'CidrBlocks',
  addressSpec: 'AddressSpec (oneof)',
} as const

export type TypeKey = keyof typeof TYPES
