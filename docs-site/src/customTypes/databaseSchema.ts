export type TDbSchemaTone = 'resource' | 'binding' | 'rule' | 'system'

export type TDbSchemaField = {
  name: string
  type?: string
  key?: 'pk' | 'fk' | 'uk'
  settings?: string[]
}

export type TDbSchemaIndex = {
  fields: string[]
  settings?: string[]
}

export type TDbSchemaTable = {
  id: string
  title: string
  tone?: TDbSchemaTone
  position: {
    column: number
    row: number
  }
  fields: TDbSchemaField[]
  indexes?: TDbSchemaIndex[]
  note?: string
}

export type TDbSchemaRelation = {
  fromTable: string
  toTable: string
  fromField?: string
  toField?: string
  label?: string
  fromCardinality?: '1' | 'N'
  toCardinality?: '1' | 'N'
  settings?: string[]
}

export type TDbSchemaEnum = {
  id: string
  title: string
  values: string[]
}

export type TDbSchemaDiagram = {
  title?: string
  description?: string
  columns: number
  tables: TDbSchemaTable[]
  relations: TDbSchemaRelation[]
  enums?: TDbSchemaEnum[]
  legend?: {
    label: string
    tone: TDbSchemaTone
  }[]
}
