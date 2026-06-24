import type {
  TDbSchemaDiagram,
  TDbSchemaEnum,
  TDbSchemaField,
  TDbSchemaIndex,
  TDbSchemaRelation,
  TDbSchemaTable,
} from '@site/src/customTypes/databaseSchema'

type TDbmlDiagramOptions = Pick<TDbSchemaDiagram, 'title' | 'description' | 'columns' | 'legend'> & {
  dbml: string
  tableMeta?: Record<string, Partial<Pick<TDbSchemaTable, 'tone' | 'position'>>>
}

type TParsedEndpoint = {
  table: string
  field: string
}

type TTableBlock = {
  tableId: string
  body: string
}

type TTrailingSettings = {
  body: string
  settings: string[]
}

const stripComments = (source: string) => {
  return source
    .replace(/\/\*[\s\S]*?\*\//g, '')
    .split('\n')
    .map((line) => line.replace(/\s*\/\/.*$/, ''))
    .join('\n')
}

const splitTopLevel = (source: string, delimiter: string) => {
  const parts: string[] = []
  let current = ''
  let quote: '"' | "'" | '`' | undefined
  let squareDepth = 0
  let parenDepth = 0

  for (let index = 0; index < source.length; index += 1) {
    const char = source[index]

    if (quote) {
      current += char
      if (char === quote && source[index - 1] !== '\\') quote = undefined
      continue
    }

    if (char === '"' || char === "'" || char === '`') {
      quote = char
      current += char
      continue
    }

    if (char === '[') squareDepth += 1
    if (char === ']') squareDepth = Math.max(0, squareDepth - 1)
    if (char === '(') parenDepth += 1
    if (char === ')') parenDepth = Math.max(0, parenDepth - 1)

    if (char === delimiter && squareDepth === 0 && parenDepth === 0) {
      parts.push(current.trim())
      current = ''
      continue
    }

    current += char
  }

  if (current.trim()) parts.push(current.trim())
  return parts
}

const splitSettings = (settings?: string) => {
  if (!settings) return []

  return splitTopLevel(settings, ',')
}

const normalizeIdentifier = (identifier: string) => {
  return identifier.trim().replace(/^"|"$/g, '')
}

const splitQualifiedIdentifier = (identifier: string) => {
  return identifier
    .trim()
    .split('.')
    .map(normalizeIdentifier)
    .filter(Boolean)
}

const normalizeQualifiedIdentifier = (identifier: string) => {
  return splitQualifiedIdentifier(identifier).join('.')
}

const normalizeTableId = (identifier: string) => {
  const parts = splitQualifiedIdentifier(identifier)
  return parts[parts.length - 1] ?? normalizeIdentifier(identifier)
}

const stripWrappedQuotes = (value: string) => {
  return value.trim().replace(/^["'`]|["'`]$/g, '')
}

const extractTrailingSettings = (line: string): TTrailingSettings => {
  const trimmedLine = line.trim()

  if (!trimmedLine.endsWith(']')) return { body: trimmedLine, settings: [] }

  let quote: '"' | "'" | '`' | undefined
  let depth = 0

  for (let index = trimmedLine.length - 1; index >= 0; index -= 1) {
    const char = trimmedLine[index]

    if (quote) {
      if (char === quote && trimmedLine[index - 1] !== '\\') quote = undefined
      continue
    }

    if (char === '"' || char === "'" || char === '`') {
      quote = char
      continue
    }

    if (char === ']') {
      depth += 1
      continue
    }

    if (char === '[') {
      depth -= 1

      if (depth === 0) {
        return {
          body: trimmedLine.slice(0, index).trim(),
          settings: splitSettings(trimmedLine.slice(index + 1, -1)),
        }
      }
    }
  }

  return { body: trimmedLine, settings: [] }
}

const parseEndpoint = (endpoint: string): TParsedEndpoint | undefined => {
  const parts = splitQualifiedIdentifier(endpoint)

  if (parts.length < 2) return undefined

  const field = parts.pop()
  const table = parts.pop()

  if (!field || !table) return undefined

  return { table, field }
}

const mapRelationOperator = (
  left: TParsedEndpoint,
  operator: string,
  right: TParsedEndpoint,
  label?: string,
): TDbSchemaRelation => {
  if (operator === '>') {
    return {
      fromTable: right.table,
      fromField: right.field,
      toTable: left.table,
      toField: left.field,
      label,
      fromCardinality: '1',
      toCardinality: 'N',
    }
  }

  return {
    fromTable: left.table,
    fromField: left.field,
    toTable: right.table,
    toField: right.field,
    label,
    fromCardinality: '1',
    toCardinality: operator === '-' ? '1' : 'N',
  }
}

const parseRelationLine = (line: string): TDbSchemaRelation | undefined => {
  const { body: normalizedLine, settings } = extractTrailingSettings(line)
  const refMatch = normalizedLine.match(/^Ref(?:\s+(?:"([^"]+)"|([\w-]+)))?\s*:\s*(.+?)\s*([<>-])\s*(.+)$/)

  if (!refMatch) return undefined

  const [, quotedLabel, plainLabel, leftRaw, operator, rightRaw] = refMatch
  const left = parseEndpoint(leftRaw)
  const right = parseEndpoint(rightRaw)

  if (!left || !right) return undefined

  return {
    ...mapRelationOperator(left, operator, right, quotedLabel ?? plainLabel),
    settings,
  }
}

const parseInlineRef = (
  tableId: string,
  fieldName: string,
  settings: string[],
): TDbSchemaRelation[] => {
  return settings.flatMap((setting) => {
    const match = setting.match(/^ref:\s*([<>-])\s*(.+)$/i)

    if (!match) return []

    const [, operator, targetRaw] = match
    const local = { table: tableId, field: fieldName }
    const target = parseEndpoint(targetRaw)

    if (!target) return []

    return [mapRelationOperator(local, operator, target)]
  })
}

const parseFieldLine = (line: string) => {
  const { body, settings } = extractTrailingSettings(line)
  const fieldMatch = body.match(/^("[^"]+"|[^\s]+)\s+(.+)$/)

  if (!fieldMatch) return undefined

  const [, rawName, rawType] = fieldMatch
  const normalizedSettings = settings.map((setting) => setting.toLowerCase())
  const key: TDbSchemaField['key'] = normalizedSettings.some((setting) => setting === 'pk' || setting === 'primary key')
    ? 'pk'
    : normalizedSettings.includes('unique')
      ? 'uk'
      : normalizedSettings.some((setting) => setting.startsWith('ref:'))
        ? 'fk'
        : undefined

  return {
    field: {
      name: rawName.replace(/^"|"$/g, ''),
      type: stripWrappedQuotes(rawType),
      key,
      settings,
    } satisfies TDbSchemaField,
    settings,
  }
}

const parseIndexLine = (line: string): TDbSchemaIndex | undefined => {
  const { body, settings } = extractTrailingSettings(line)
  const normalizedBody = body.trim()

  if (!normalizedBody) return undefined

  const fieldsSource = normalizedBody.startsWith('(') && normalizedBody.endsWith(')')
    ? normalizedBody.slice(1, -1)
    : normalizedBody
  const fields = splitTopLevel(fieldsSource, ',')
    .map(stripWrappedQuotes)
    .filter(Boolean)

  if (fields.length === 0) return undefined

  return { fields, settings }
}

const parseNoteLine = (line: string) => {
  const noteMatch = line.trim().match(/^Note:\s*(.+)$/i)
  if (!noteMatch) return undefined

  return stripWrappedQuotes(noteMatch[1])
}

const extractTableBlocks = (source: string) => {
  const blocks: TTableBlock[] = []
  const tableStartRegex = /Table\s+([^\s{[]+)(?:\s+as\s+\w+)?(?:\s*\[.*?\])?\s*\{/g
  let tableStartMatch: RegExpExecArray | null

  while ((tableStartMatch = tableStartRegex.exec(source))) {
    const [, rawTableId] = tableStartMatch
    let depth = 1
    let cursor = tableStartRegex.lastIndex

    while (cursor < source.length && depth > 0) {
      const char = source[cursor]

      if (char === '{') depth += 1
      if (char === '}') depth -= 1
      cursor += 1
    }

    blocks.push({
      tableId: normalizeTableId(rawTableId),
      body: source.slice(tableStartRegex.lastIndex, cursor - 1),
    })
    tableStartRegex.lastIndex = cursor
  }

  return blocks
}

const extractEnumBlocks = (source: string) => {
  const blocks: TTableBlock[] = []
  const enumStartRegex = /Enum\s+([^\s{[]+)(?:\s*\[.*?\])?\s*\{/g
  let enumStartMatch: RegExpExecArray | null

  while ((enumStartMatch = enumStartRegex.exec(source))) {
    const [, rawEnumId] = enumStartMatch
    let depth = 1
    let cursor = enumStartRegex.lastIndex

    while (cursor < source.length && depth > 0) {
      const char = source[cursor]

      if (char === '{') depth += 1
      if (char === '}') depth -= 1
      cursor += 1
    }

    blocks.push({
      tableId: normalizeQualifiedIdentifier(rawEnumId),
      body: source.slice(enumStartRegex.lastIndex, cursor - 1),
    })
    enumStartRegex.lastIndex = cursor
  }

  return blocks
}

const parseEnums = (source: string): TDbSchemaEnum[] => {
  return extractEnumBlocks(source).map(({ tableId, body }) => ({
    id: tableId,
    title: tableId,
    values: body
      .split('\n')
      .map((line) => stripWrappedQuotes(line.trim()))
      .filter(Boolean),
  }))
}

const parseTables = (source: string) => {
  const tables: TDbSchemaTable[] = []
  const inlineRelations: TDbSchemaRelation[] = []

  extractTableBlocks(source).forEach(({ tableId, body }) => {
    const fields: TDbSchemaField[] = []
    const indexes: TDbSchemaIndex[] = []
    let note: string | undefined
    let nestedBlock: 'indexes' | 'other' | undefined
    let nestedBlockDepth = 0

    body
      .split('\n')
      .map((line) => line.trim())
      .filter(Boolean)
      .forEach((line) => {
        if (line.endsWith('{')) {
          nestedBlock = /^Indexes\s*\{/i.test(line) ? 'indexes' : 'other'
          nestedBlockDepth += 1
          return
        }

        if (line === '}') {
          nestedBlockDepth = Math.max(0, nestedBlockDepth - 1)
          if (nestedBlockDepth === 0) nestedBlock = undefined
          return
        }

        if (nestedBlockDepth > 0) {
          if (nestedBlock === 'indexes') {
            const parsedIndex = parseIndexLine(line)
            if (parsedIndex) indexes.push(parsedIndex)
          }
          return
        }

        const parsedNote = parseNoteLine(line)
        if (parsedNote) {
          note = parsedNote
          return
        }

        const parsedField = parseFieldLine(line)

        if (!parsedField) return

        fields.push(parsedField.field)
        inlineRelations.push(...parseInlineRef(tableId, parsedField.field.name, parsedField.settings))
      })

    tables.push({
      id: tableId,
      title: tableId,
      position: {
        column: 0,
        row: tables.length,
      },
      fields,
      indexes,
      note,
    })
  })

  return { tables, inlineRelations }
}

export const defineDbSchemaDiagramFromDbml = ({
  dbml,
  tableMeta,
  ...diagramOptions
}: TDbmlDiagramOptions): TDbSchemaDiagram => {
  const source = stripComments(dbml)
  const enums = parseEnums(source)
  const { tables, inlineRelations } = parseTables(source)
  const blockRelations = source
    .split('\n')
    .map((line) => parseRelationLine(line))
    .filter(Boolean) as TDbSchemaRelation[]
  const relations = [...blockRelations, ...inlineRelations]
  const foreignKeyFields = relations.reduce((acc, relation) => {
    if (!relation.toField) return acc

    acc.add(`${relation.toTable}.${relation.toField}`)
    return acc
  }, new Set<string>())
  const tablesWithMeta = tables.map((table, index) => {
    const meta = tableMeta?.[table.id]
    const position = meta?.position ?? {
      column: Math.floor(index / Math.max(1, diagramOptions.columns)),
      row: index % Math.max(1, diagramOptions.columns),
    }

    return {
      ...table,
      tone: meta?.tone,
      position,
      fields: table.fields.map((field) => ({
        ...field,
        key: field.key ?? (foreignKeyFields.has(`${table.id}.${field.name}`) ? 'fk' : undefined),
      })),
    }
  })

  return {
    ...diagramOptions,
    tables: tablesWithMeta,
    relations,
    enums,
  }
}
