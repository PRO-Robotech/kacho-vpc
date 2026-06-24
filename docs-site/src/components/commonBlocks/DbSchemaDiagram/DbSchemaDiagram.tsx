import React, {
  FC,
  KeyboardEvent,
  MouseEvent,
  useCallback,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
} from 'react'
import { createPortal } from 'react-dom'
import clsx from 'clsx'
import type {
  TDbSchemaDiagram,
  TDbSchemaField,
  TDbSchemaRelation,
  TDbSchemaTable,
  TDbSchemaTone,
} from '@site/src/customTypes/databaseSchema'
import styles from './DbSchemaDiagram.module.css'

/* ==========================================================================
   Layout constants
   ========================================================================== */

const NODE_WIDTH = 268
const NODE_WIDTH_COMPACT = 200
const NODE_HEADER_HEIGHT = 44
const NODE_META_HEIGHT = 28
const NODE_NOTE_HEIGHT = 36
const NODE_FIELD_HEIGHT = 28
const NODE_FIELD_LIST_PADDING_Y = 4

const NODE_HEADER_HEIGHT_COMPACT = 32
const NODE_META_HEIGHT_COMPACT = 22
const NODE_FIELD_HEIGHT_COMPACT = 20

const CANVAS_PADDING = 56
const COLUMN_GAP = 88
const ROW_GAP = 32
const RELAX_GAP = 28

const EDGE_LEAD = 26
const EDGE_RADIUS = 12
const EDGE_BUS_OFFSET = 18

const ZOOM_MIN = 0.2
const ZOOM_MAX = 1.8
const ZOOM_STEP = 0.1
const ZOOM_DEFAULT = 1

/* ==========================================================================
   Types
   ========================================================================== */

type TGraphPortSide = 'left' | 'right'
type TGraphColumn = 'left' | 'center' | 'right' | 'all'

type TGraphPoint = {
  x: number
  y: number
}

type TGraphRect = {
  x: number
  y: number
  width: number
  height: number
}

type TGraphNode = {
  id: string
  table: TDbSchemaTable
  column: TGraphColumn
  compact: boolean
  x: number
  y: number
  width: number
  height: number
}

type TGraphEdgeLayout = {
  relation: TDbSchemaRelation
  source: TGraphNode
  target: TGraphNode
  sourceField?: string
  targetField?: string
  sourceSide: TGraphPortSide
  targetSide: TGraphPortSide
  start: TGraphPoint
  end: TGraphPoint
  path: string
}

type TFieldAnchor = {
  y: number
  height: number
}

type TFieldAnchors = Record<string, TFieldAnchor>

type TPanState = {
  isPanning: boolean
  startX: number
  startY: number
  basePanX: number
  basePanY: number
}

type TNodeDragState = {
  nodeId: string
  startX: number
  startY: number
  baseOffsetX: number
  baseOffsetY: number
}

type TGraphNodeOffsets = Record<string, TGraphPoint>

type TGraphView = {
  panX: number
  panY: number
}

const WHEEL_PAN_STEP = 1

/* ==========================================================================
   Helpers
   ========================================================================== */

const TONE_LABEL: Record<TDbSchemaTone, string> = {
  resource: 'Ресурсы',
  binding: 'Bindings',
  rule: 'Rule-таблицы',
  system: 'Служебные',
}

const TONE_CLASS: Record<TDbSchemaTone, string> = {
  resource: styles.tableResource,
  binding: styles.tableBinding,
  rule: styles.tableRule,
  system: styles.tableSystem,
}

const FIELD_KEY_CLASS: Record<NonNullable<TDbSchemaField['key']>, string> = {
  pk: styles.fieldKeyPk,
  fk: styles.fieldKeyFk,
  uk: styles.fieldKeyUk,
}

const getToneClass = (tone?: TDbSchemaTone) => TONE_CLASS[tone ?? 'resource']

const getNodeMetrics = (compact: boolean) => ({
  headerHeight: compact ? NODE_HEADER_HEIGHT_COMPACT : NODE_HEADER_HEIGHT,
  metaHeight: compact ? NODE_META_HEIGHT_COMPACT : NODE_META_HEIGHT,
  fieldHeight: compact ? NODE_FIELD_HEIGHT_COMPACT : NODE_FIELD_HEIGHT,
})

const getNodeBaseHeight = (table: TDbSchemaTable, compact: boolean) => {
  const { headerHeight, metaHeight, fieldHeight } = getNodeMetrics(compact)
  const noteHeight = !compact && table.note ? NODE_NOTE_HEIGHT : 0
  return headerHeight + metaHeight + noteHeight + table.fields.length * fieldHeight + NODE_FIELD_LIST_PADDING_Y * 2
}

const getFieldDefaultY = (node: TGraphNode, fieldName: string | undefined) => {
  if (!fieldName) return node.y + node.height / 2
  const fieldIndex = node.table.fields.findIndex((field) => field.name === fieldName)
  if (fieldIndex < 0) return node.y + node.height / 2

  const { headerHeight, metaHeight, fieldHeight } = getNodeMetrics(node.compact)
  const noteHeight = !node.compact && node.table.note ? NODE_NOTE_HEIGHT : 0
  return node.y + headerHeight + metaHeight + noteHeight + NODE_FIELD_LIST_PADDING_Y + fieldIndex * fieldHeight + fieldHeight / 2
}

const getFieldAnchorKey = (nodeId: string, fieldName: string) => `${nodeId}::${fieldName}`

const getFieldAnchorY = (node: TGraphNode, fieldName: string | undefined, anchors: TFieldAnchors) => {
  if (fieldName) {
    const anchor = anchors[getFieldAnchorKey(node.id, fieldName)]
    if (anchor) return node.y + anchor.y + anchor.height / 2
  }
  return getFieldDefaultY(node, fieldName)
}

const getFieldPort = (node: TGraphNode, side: TGraphPortSide, fieldName: string | undefined, anchors: TFieldAnchors): TGraphPoint => ({
  x: side === 'right' ? node.x + node.width : node.x,
  y: getFieldAnchorY(node, fieldName, anchors),
})

const isRelationConnectedTo = (relation: TDbSchemaRelation, tableId: string) => {
  return relation.fromTable === tableId || relation.toTable === tableId
}

const sortTablesByTitle = (a: TDbSchemaTable, b: TDbSchemaTable) => a.title.localeCompare(b.title)

const getRelationKey = (relation: TDbSchemaRelation, index: number) => {
  return `${relation.fromTable}.${relation.fromField ?? ''}-${relation.toTable}.${relation.toField ?? ''}-${index}`
}

const formatSettingLabel = (setting: string) => {
  const [rawKey, ...rawValueParts] = setting.split(':')
  const key = rawKey.trim().toUpperCase()
  const value = rawValueParts.join(':').trim()
  return value ? `${key} ${value}` : key
}

const getNodeRect = (node: TGraphNode): TGraphRect => ({
  x: node.x,
  y: node.y,
  width: node.width,
  height: node.height,
})

const expandRect = (rect: TGraphRect, gap: number): TGraphRect => ({
  x: rect.x - gap,
  y: rect.y - gap,
  width: rect.width + gap * 2,
  height: rect.height + gap * 2,
})

const doRectsOverlap = (a: TGraphRect, b: TGraphRect) => {
  return a.x < b.x + b.width
    && a.x + a.width > b.x
    && a.y < b.y + b.height
    && a.y + a.height > b.y
}

const relaxNodes = (nodes: TGraphNode[], maxIterations = 200) => {
  for (let iteration = 0; iteration < maxIterations; iteration += 1) {
    let moved = false

    for (let i = 0; i < nodes.length; i += 1) {
      for (let j = i + 1; j < nodes.length; j += 1) {
        const a = nodes[i]
        const b = nodes[j]
        const aRect = expandRect(getNodeRect(a), RELAX_GAP)
        const bRect = expandRect(getNodeRect(b), RELAX_GAP)
        if (!doRectsOverlap(aRect, bRect)) continue

        const overlapX = Math.min(aRect.x + aRect.width - bRect.x, bRect.x + bRect.width - aRect.x)
        const overlapY = Math.min(aRect.y + aRect.height - bRect.y, bRect.y + bRect.height - aRect.y)
        const useHorizontal = overlapX < overlapY
        const aCenter = { x: a.x + a.width / 2, y: a.y + a.height / 2 }
        const bCenter = { x: b.x + b.width / 2, y: b.y + b.height / 2 }
        const shiftX = aCenter.x <= bCenter.x ? -overlapX / 2 : overlapX / 2
        const shiftY = aCenter.y <= bCenter.y ? -overlapY / 2 : overlapY / 2
        const dx = useHorizontal ? shiftX : 0
        const dy = useHorizontal ? 0 : shiftY

        a.x += dx
        a.y += dy
        b.x -= dx
        b.y -= dy
        moved = true
      }
    }

    if (!moved) break
  }
}

const buildRoundedPath = (points: TGraphPoint[]) => {
  if (points.length === 0) return ''
  if (points.length === 1) return `M ${points[0].x} ${points[0].y}`

  const commands = [`M ${points[0].x} ${points[0].y}`]

  for (let index = 1; index < points.length - 1; index += 1) {
    const previous = points[index - 1]
    const current = points[index]
    const next = points[index + 1]
    const incomingDistance = Math.hypot(current.x - previous.x, current.y - previous.y)
    const outgoingDistance = Math.hypot(next.x - current.x, next.y - current.y)
    const radius = Math.min(EDGE_RADIUS, incomingDistance / 2, outgoingDistance / 2)

    if (radius <= 0.1) {
      commands.push(`L ${current.x} ${current.y}`)
      continue
    }

    const incomingPoint = {
      x: current.x - ((current.x - previous.x) / incomingDistance) * radius,
      y: current.y - ((current.y - previous.y) / incomingDistance) * radius,
    }
    const outgoingPoint = {
      x: current.x + ((next.x - current.x) / outgoingDistance) * radius,
      y: current.y + ((next.y - current.y) / outgoingDistance) * radius,
    }
    commands.push(`L ${incomingPoint.x} ${incomingPoint.y}`)
    commands.push(`Q ${current.x} ${current.y} ${outgoingPoint.x} ${outgoingPoint.y}`)
  }

  const lastPoint = points[points.length - 1]
  commands.push(`L ${lastPoint.x} ${lastPoint.y}`)
  return commands.join(' ')
}

const buildOrthogonalPath = (
  start: TGraphPoint,
  end: TGraphPoint,
  sourceSide: TGraphPortSide,
  targetSide: TGraphPortSide,
) => {
  const sourceDirection = sourceSide === 'right' ? 1 : -1
  const targetDirection = targetSide === 'right' ? 1 : -1
  const startLeadX = start.x + sourceDirection * EDGE_LEAD
  const endLeadX = end.x + targetDirection * EDGE_LEAD

  if (sourceSide !== targetSide) {
    const busX = (startLeadX + endLeadX) / 2
    return buildRoundedPath([
      start,
      { x: startLeadX, y: start.y },
      { x: busX, y: start.y },
      { x: busX, y: end.y },
      { x: endLeadX, y: end.y },
      end,
    ])
  }

  const outerOffset = EDGE_LEAD + EDGE_BUS_OFFSET
  const busX = sourceSide === 'right'
    ? Math.max(start.x, end.x) + outerOffset
    : Math.min(start.x, end.x) - outerOffset

  return buildRoundedPath([
    start,
    { x: startLeadX, y: start.y },
    { x: busX, y: start.y },
    { x: busX, y: end.y },
    { x: endLeadX, y: end.y },
    end,
  ])
}

/* ==========================================================================
   Layout builders
   ========================================================================== */

type TBuildLayoutArgs = {
  tables: TDbSchemaTable[]
  relations: TDbSchemaRelation[]
  focusedTableId: string
  tablesById: Record<string, TDbSchemaTable>
  fieldAnchors: TFieldAnchors
  nodeOffsets: TGraphNodeOffsets
}

type TGraphLayout = {
  nodes: TGraphNode[]
  edges: TGraphEdgeLayout[]
  width: number
  height: number
}

const stackVerticalNodes = (
  tables: TDbSchemaTable[],
  column: TGraphColumn,
  x: number,
  centerY: number,
): TGraphNode[] => {
  const heights = tables.map((table) => getNodeBaseHeight(table, false))
  const totalHeight = heights.reduce((acc, h) => acc + h, 0) + Math.max(0, tables.length - 1) * ROW_GAP
  let cursor = centerY - totalHeight / 2

  return tables.map((table, index) => {
    const height = heights[index]
    const node: TGraphNode = {
      id: table.id,
      table,
      column,
      compact: false,
      x,
      y: Math.round(cursor),
      width: NODE_WIDTH,
      height,
    }
    cursor += height + ROW_GAP
    return node
  })
}

const buildFocusedLayout = ({
  tables,
  relations,
  focusedTableId,
  tablesById,
  fieldAnchors,
  nodeOffsets,
}: TBuildLayoutArgs): TGraphLayout | undefined => {
  const focusedTable = tablesById[focusedTableId]
  if (!focusedTable) return undefined

  const focusedRelations = relations.filter((relation) => isRelationConnectedTo(relation, focusedTableId))
  const relatedIds = new Set<string>()
  focusedRelations.forEach((relation) => {
    relatedIds.add(relation.fromTable)
    relatedIds.add(relation.toTable)
  })
  relatedIds.delete(focusedTableId)

  const sideScores = new Map<string, number>()
  focusedRelations.forEach((relation) => {
    if (relation.fromTable === focusedTableId) {
      sideScores.set(relation.toTable, (sideScores.get(relation.toTable) ?? 0) + 1)
    }
    if (relation.toTable === focusedTableId) {
      sideScores.set(relation.fromTable, (sideScores.get(relation.fromTable) ?? 0) - 1)
    }
  })

  const fieldOrderForTable = (otherId: string) => {
    return Math.min(
      ...focusedRelations
        .filter((relation) => relation.fromTable === otherId || relation.toTable === otherId)
        .map((relation) => {
          if (relation.fromTable === focusedTableId) {
            return focusedTable.fields.findIndex((field) => field.name === relation.fromField)
          }
          return focusedTable.fields.findIndex((field) => field.name === relation.toField)
        })
        .map((value) => (value < 0 ? Number.POSITIVE_INFINITY : value)),
    )
  }

  const sortByFieldOrder = (a: TDbSchemaTable, b: TDbSchemaTable) => {
    const orderDelta = fieldOrderForTable(a.id) - fieldOrderForTable(b.id)
    return orderDelta !== 0 ? orderDelta : a.title.localeCompare(b.title)
  }

  const relatedTables = Array.from(relatedIds)
    .map((id) => tablesById[id])
    .filter(Boolean)
  const leftRaw = relatedTables.filter((table) => (sideScores.get(table.id) ?? 0) < 0)
  const rightRaw = relatedTables.filter((table) => (sideScores.get(table.id) ?? 0) >= 0)

  // Balance sides: if one side is significantly heavier (>4 tables), move the
  // tallest items to the other side until they're roughly balanced.
  const balancedLeft = [...leftRaw]
  const balancedRight = [...rightRaw]
  while (balancedRight.length > balancedLeft.length + 2 && balancedRight.length > 4) {
    const item = balancedRight.shift()
    if (item) balancedLeft.push(item)
  }
  while (balancedLeft.length > balancedRight.length + 2 && balancedLeft.length > 4) {
    const item = balancedLeft.shift()
    if (item) balancedRight.push(item)
  }

  const leftTables = balancedLeft.sort(sortByFieldOrder)
  const rightTables = balancedRight.sort(sortByFieldOrder)

  const leftHeight = leftTables.reduce((acc, table) => acc + getNodeBaseHeight(table, false), 0)
    + Math.max(0, leftTables.length - 1) * ROW_GAP
  const rightHeight = rightTables.reduce((acc, table) => acc + getNodeBaseHeight(table, false), 0)
    + Math.max(0, rightTables.length - 1) * ROW_GAP
  const centerHeight = getNodeBaseHeight(focusedTable, false)
  const tallest = Math.max(leftHeight, rightHeight, centerHeight)
  const centerY = CANVAS_PADDING + tallest / 2

  const leftX = CANVAS_PADDING
  const centerX = leftX + NODE_WIDTH + COLUMN_GAP
  const rightX = centerX + NODE_WIDTH + COLUMN_GAP

  const centerNode: TGraphNode = {
    id: focusedTable.id,
    table: focusedTable,
    column: 'center',
    compact: false,
    x: centerX,
    y: centerY - centerHeight / 2,
    width: NODE_WIDTH,
    height: centerHeight,
  }

  const nodes: TGraphNode[] = [
    centerNode,
    ...stackVerticalNodes(leftTables, 'left', leftX, centerY),
    ...stackVerticalNodes(rightTables, 'right', rightX, centerY),
  ]

  applyOffsets(nodes, nodeOffsets)
  applyMeasuredHeights(nodes, fieldAnchors)

  const nodesById = Object.fromEntries(nodes.map((node) => [node.id, node]))
  const edges = focusedRelations
    .map<TGraphEdgeLayout | undefined>((relation) => buildEdgeLayout(relation, nodesById, fieldAnchors))
    .filter(Boolean) as TGraphEdgeLayout[]

  return finalizeLayout(nodes, edges)
}

const buildOverviewLayout = ({
  tables,
  relations,
  tablesById,
  fieldAnchors,
  nodeOffsets,
}: TBuildLayoutArgs): TGraphLayout => {
  const tablesByColumn = new Map<number, TDbSchemaTable[]>()
  tables.forEach((table) => {
    const col = table.position.column
    const list = tablesByColumn.get(col) ?? []
    list.push(table)
    tablesByColumn.set(col, list)
  })

  const sortedColumns = Array.from(tablesByColumn.keys()).sort((a, b) => a - b)

  const columnX = new Map<number, number>()
  sortedColumns.forEach((col, index) => {
    columnX.set(col, CANVAS_PADDING + index * (NODE_WIDTH + COLUMN_GAP))
  })

  const columnsMeta = sortedColumns.map((col) => {
    const columnTables = (tablesByColumn.get(col) ?? []).slice().sort((a, b) => a.position.row - b.position.row)
    const heights = columnTables.map((table) => getNodeBaseHeight(table, false))
    return { col, columnTables, heights }
  })

  const nodes: TGraphNode[] = []
  columnsMeta.forEach(({ col, columnTables, heights }) => {
    let cursorY = CANVAS_PADDING

    columnTables.forEach((table, index) => {
      const node: TGraphNode = {
        id: table.id,
        table,
        column: 'all',
        compact: false,
        x: columnX.get(col) ?? CANVAS_PADDING,
        y: Math.round(cursorY),
        width: NODE_WIDTH,
        height: heights[index],
      }
      cursorY += heights[index] + ROW_GAP
      nodes.push(node)
    })
  })

  applyOffsets(nodes, nodeOffsets)
  applyMeasuredHeights(nodes, fieldAnchors)

  const nodesById = Object.fromEntries(nodes.map((node) => [node.id, node]))
  const edges = relations
    .map<TGraphEdgeLayout | undefined>((relation) => buildEdgeLayout(relation, nodesById, fieldAnchors))
    .filter(Boolean) as TGraphEdgeLayout[]

  return finalizeLayout(nodes, edges)
}

const applyOffsets = (nodes: TGraphNode[], offsets: TGraphNodeOffsets) => {
  nodes.forEach((node) => {
    const offset = offsets[node.id]
    if (!offset) return
    node.x += offset.x
    node.y += offset.y
  })
}

const applyMeasuredHeights = (nodes: TGraphNode[], anchors: TFieldAnchors) => {
  nodes.forEach((node) => {
    if (!node.table.fields.length) return
    const lastField = node.table.fields[node.table.fields.length - 1]
    const anchor = anchors[getFieldAnchorKey(node.id, lastField.name)]
    if (!anchor) return
    const measuredBottom = anchor.y + anchor.height + NODE_FIELD_LIST_PADDING_Y
    if (measuredBottom > node.height - 4 && measuredBottom < node.height + 200) {
      node.height = measuredBottom
    }
  })
}

const buildEdgeLayout = (
  relation: TDbSchemaRelation,
  nodesById: Record<string, TGraphNode>,
  anchors: TFieldAnchors,
): TGraphEdgeLayout | undefined => {
  const source = nodesById[relation.fromTable]
  const target = nodesById[relation.toTable]
  if (!source || !target) return undefined

  const sourceCenter = source.x + source.width / 2
  const targetCenter = target.x + target.width / 2
  const sourceSide: TGraphPortSide = sourceCenter <= targetCenter ? 'right' : 'left'
  const targetSide: TGraphPortSide = sourceCenter < targetCenter ? 'left' : 'right'

  const start = getFieldPort(source, sourceSide, relation.fromField, anchors)
  const end = getFieldPort(target, targetSide, relation.toField, anchors)

  return {
    relation,
    source,
    target,
    sourceField: relation.fromField,
    targetField: relation.toField,
    sourceSide,
    targetSide,
    start,
    end,
    path: buildOrthogonalPath(start, end, sourceSide, targetSide),
  }
}

const finalizeLayout = (nodes: TGraphNode[], edges: TGraphEdgeLayout[]): TGraphLayout => {
  if (nodes.length === 0) {
    return { nodes, edges, width: CANVAS_PADDING * 2, height: CANVAS_PADDING * 2 }
  }

  let maxX = Number.NEGATIVE_INFINITY
  let maxY = Number.NEGATIVE_INFINITY

  nodes.forEach((node) => {
    if (node.x + node.width > maxX) maxX = node.x + node.width
    if (node.y + node.height > maxY) maxY = node.y + node.height
  })

  const width = Math.max(CANVAS_PADDING * 2, maxX + CANVAS_PADDING)
  const height = Math.max(CANVAS_PADDING * 2, maxY + CANVAS_PADDING)

  return { nodes, edges, width, height }
}

/* ==========================================================================
   Renderers (small bits)
   ========================================================================== */

const renderFieldKey = (field: TDbSchemaField) => {
  if (!field.key) return null
  return <span className={clsx(styles.fieldKey, FIELD_KEY_CLASS[field.key])}>{field.key.toUpperCase()}</span>
}

type TFieldTooltipProps = {
  field: TDbSchemaField
  enumValues?: string[]
}

type TTooltipPlacement = {
  left: number
  top: number
  side: 'right' | 'left' | 'top' | 'bottom'
}

const TOOLTIP_WIDTH = 320
const TOOLTIP_GAP = 10

const normalizeTooltipSetting = (setting: string) => {
  const formatted = formatSettingLabel(setting)
  const [key, ...valueParts] = formatted.split(' ')
  const value = valueParts.join(' ')

  return {
    key,
    value,
  }
}

const FieldTooltip: FC<TFieldTooltipProps> = ({ field, enumValues }) => {
  const triggerRef = useRef<HTMLSpanElement>(null)
  const [placement, setPlacement] = useState<TTooltipPlacement | undefined>(undefined)

  if (!field.type && !field.key && !field.settings?.length) return null

  const computePlacement = () => {
    const trigger = triggerRef.current
    if (!trigger) return
    const rect = trigger.getBoundingClientRect()
    const viewportWidth = window.innerWidth
    const viewportHeight = window.innerHeight
    const spaceRight = viewportWidth - rect.right
    const spaceLeft = rect.left

    if (spaceRight > TOOLTIP_WIDTH + TOOLTIP_GAP) {
      setPlacement({
        left: rect.right + TOOLTIP_GAP,
        top: rect.top + rect.height / 2,
        side: 'right',
      })
      return
    }
    if (spaceLeft > TOOLTIP_WIDTH + TOOLTIP_GAP) {
      setPlacement({
        left: rect.left - TOOLTIP_GAP,
        top: rect.top + rect.height / 2,
        side: 'left',
      })
      return
    }
    if (rect.bottom + 140 < viewportHeight) {
      setPlacement({
        left: Math.min(viewportWidth - TOOLTIP_GAP, rect.left + rect.width / 2),
        top: rect.bottom + TOOLTIP_GAP,
        side: 'bottom',
      })
      return
    }
    setPlacement({
      left: Math.min(viewportWidth - TOOLTIP_GAP, rect.left + rect.width / 2),
      top: rect.top - TOOLTIP_GAP,
      side: 'top',
    })
  }

  const clearPlacement = () => setPlacement(undefined)

  const sideClass = placement
    ? {
        right: styles.fieldTooltipRight,
        left: styles.fieldTooltipLeft,
        top: styles.fieldTooltipTop,
        bottom: styles.fieldTooltipBottom,
      }[placement.side]
    : ''

  const tooltipNode = placement
    ? (
      <span
        className={clsx(styles.fieldTooltip, styles.fieldTooltipFloating, sideClass)}
        role="tooltip"
        style={{ left: placement.left, top: placement.top }}
      >
        <span className={styles.fieldTooltipTitle}>
          <code>{field.name}</code>
          {field.type ? <code>{field.type}</code> : null}
        </span>
        {enumValues?.length ? (
          <span className={styles.fieldTooltipEnumLine}>
            <b>ENUM</b>
            <span>
              <code>{field.type}</code>
              {enumValues.map((value) => (
                <em key={`${field.name}-${value}`}>{value}</em>
              ))}
            </span>
          </span>
        ) : null}
        {field.key ? (
          <span className={styles.fieldTooltipLine}>
            <b>KEY</b>
            <span>{field.key.toUpperCase()}</span>
          </span>
        ) : null}
        {field.settings?.map((setting) => {
          const { key, value } = normalizeTooltipSetting(setting)
          return (
            <span key={`${field.name}-${setting}`} className={styles.fieldTooltipLine}>
              <b>{key}</b>
              <span>{value || setting}</span>
            </span>
          )
        })}
      </span>
    )
    : null

  return (
    <span
      ref={triggerRef}
      className={styles.fieldInfo}
      tabIndex={0}
      aria-label={`Подробности поля ${field.name}`}
      onMouseEnter={computePlacement}
      onMouseLeave={clearPlacement}
      onFocus={computePlacement}
      onBlur={clearPlacement}
    >
      i
      {tooltipNode && typeof document !== 'undefined'
        ? createPortal(tooltipNode, document.body)
        : null}
    </span>
  )
}

/* ==========================================================================
   Main component
   ========================================================================== */

export const DbSchemaDiagram: FC<TDbSchemaDiagram> = ({
  title,
  description,
  tables,
  relations,
  enums = [],
  legend,
}) => {
  const [isWorkspaceOpen, setIsWorkspaceOpen] = useState(false)
  const [focusedTableId, setFocusedTableId] = useState<string>('')
  const [activeRelationKey, setActiveRelationKey] = useState<string>('')
  const [hoveredRelationKey, setHoveredRelationKey] = useState<string>('')
  const [graphFieldAnchors, setGraphFieldAnchors] = useState<TFieldAnchors>({})
  const [graphNodeOffsets, setGraphNodeOffsets] = useState<TGraphNodeOffsets>({})
  const [zoom, setZoom] = useState<number>(ZOOM_DEFAULT)
  const [view, setView] = useState<TGraphView>({ panX: 0, panY: 0 })
  const [isPanning, setIsPanning] = useState(false)

  const graphBoardRef = useRef<HTMLDivElement>(null)
  const graphCanvasRef = useRef<HTMLDivElement>(null)
  const panStateRef = useRef<TPanState>({
    isPanning: false,
    startX: 0,
    startY: 0,
    basePanX: 0,
    basePanY: 0,
  })
  const nodeDragStateRef = useRef<TNodeDragState | undefined>(undefined)
  const nodeDragFrameRef = useRef<number | undefined>(undefined)
  const pendingNodeOffsetRef = useRef<{ nodeId: string; offset: TGraphPoint } | undefined>(undefined)
  const hasDraggedRef = useRef(false)
  const viewRef = useRef<TGraphView>({ panX: 0, panY: 0 })
  const zoomRef = useRef<number>(zoom)
  const hasInitializedViewRef = useRef(false)

  const tablesById = useMemo(
    () => Object.fromEntries(tables.map((table) => [table.id, table])),
    [tables],
  )

  const enumValuesByTitle = useMemo(() => {
    return Object.fromEntries(enums.map((enumItem) => [enumItem.title, enumItem.values]))
  }, [enums])

  const focusedTable = focusedTableId ? tablesById[focusedTableId] : undefined
  const focusedRelations = useMemo(() => {
    if (!focusedTableId) return relations
    return relations.filter((relation) => isRelationConnectedTo(relation, focusedTableId))
  }, [focusedTableId, relations])

  const layoutInput: TBuildLayoutArgs = useMemo(() => ({
    tables,
    relations,
    focusedTableId,
    tablesById,
    fieldAnchors: graphFieldAnchors,
    nodeOffsets: graphNodeOffsets,
  }), [tables, relations, focusedTableId, tablesById, graphFieldAnchors, graphNodeOffsets])

  const graphLayout: TGraphLayout = useMemo(() => {
    if (focusedTableId) {
      const layout = buildFocusedLayout(layoutInput)
      if (layout) return layout
    }
    return buildOverviewLayout(layoutInput)
  }, [focusedTableId, layoutInput])

  const activeEdge = useMemo(() => {
    if (!activeRelationKey) return undefined
    return graphLayout.edges.find((edge, index) => getRelationKey(edge.relation, index) === activeRelationKey)
  }, [activeRelationKey, graphLayout.edges])

  const hoveredEdge = useMemo(() => {
    if (!hoveredRelationKey) return undefined
    return graphLayout.edges.find((edge, index) => getRelationKey(edge.relation, index) === hoveredRelationKey)
  }, [hoveredRelationKey, graphLayout.edges])

  const accentEdge = activeEdge ?? hoveredEdge

  const accentSourceFieldKey = accentEdge && accentEdge.sourceField
    ? getFieldAnchorKey(accentEdge.source.id, accentEdge.sourceField)
    : undefined
  const accentTargetFieldKey = accentEdge && accentEdge.targetField
    ? getFieldAnchorKey(accentEdge.target.id, accentEdge.targetField)
    : undefined

  const relatedToFocused = useMemo(() => {
    const set = new Set<string>()
    if (!focusedTableId) return set
    set.add(focusedTableId)
    focusedRelations.forEach((relation) => {
      set.add(relation.fromTable)
      set.add(relation.toTable)
    })
    return set
  }, [focusedTableId, focusedRelations])

  /* ----- Sync view/zoom refs for non-React event handlers ----- */

  useEffect(() => {
    viewRef.current = view
  }, [view])

  useEffect(() => {
    zoomRef.current = zoom
  }, [zoom])

  /* ----- Field anchors measurement ----- */

  useLayoutEffect(() => {
    if (!isWorkspaceOpen || !graphCanvasRef.current) return undefined

    const canvas = graphCanvasRef.current

    const measureAnchors = () => {
      const next: TFieldAnchors = {}
      canvas.querySelectorAll<HTMLElement>('[data-graph-node-id]').forEach((nodeEl) => {
        const nodeId = nodeEl.dataset.graphNodeId
        if (!nodeId) return
        const nodeRect = nodeEl.getBoundingClientRect()
        nodeEl.querySelectorAll<HTMLElement>('[data-graph-field-name]').forEach((fieldEl) => {
          const fieldName = fieldEl.dataset.graphFieldName
          if (!fieldName) return
          const fieldRect = fieldEl.getBoundingClientRect()
          next[getFieldAnchorKey(nodeId, fieldName)] = {
            y: (fieldRect.top - nodeRect.top) / zoom,
            height: fieldRect.height / zoom,
          }
        })
      })

      setGraphFieldAnchors((prev) => {
        const prevKeys = Object.keys(prev)
        const nextKeys = Object.keys(next)
        if (prevKeys.length !== nextKeys.length) return next
        for (const key of nextKeys) {
          const prevAnchor = prev[key]
          const nextAnchor = next[key]
          if (!prevAnchor) return next
          if (Math.abs(prevAnchor.y - nextAnchor.y) > 0.5) return next
          if (Math.abs(prevAnchor.height - nextAnchor.height) > 0.5) return next
        }
        return prev
      })
    }

    measureAnchors()
    const frameId = window.requestAnimationFrame(measureAnchors)
    const observer = new ResizeObserver(measureAnchors)
    observer.observe(canvas)
    window.addEventListener('resize', measureAnchors)

    return () => {
      window.cancelAnimationFrame(frameId)
      observer.disconnect()
      window.removeEventListener('resize', measureAnchors)
    }
  }, [isWorkspaceOpen, focusedTableId, graphLayout.nodes.length, zoom])

  /* ----- Pan / drag handlers ----- */

  const handlePanStart = (event: MouseEvent<HTMLDivElement>) => {
    if (event.button !== 0) return
    if (nodeDragStateRef.current) return

    panStateRef.current = {
      isPanning: true,
      startX: event.clientX,
      startY: event.clientY,
      basePanX: viewRef.current.panX,
      basePanY: viewRef.current.panY,
    }
    hasDraggedRef.current = false
    setIsPanning(true)
  }

  const moveInteraction = useCallback((clientX: number, clientY: number) => {
    if (nodeDragStateRef.current) {
      const drag = nodeDragStateRef.current
      const deltaX = (clientX - drag.startX) / zoomRef.current
      const deltaY = (clientY - drag.startY) / zoomRef.current
      if (Math.abs(deltaX) > 3 || Math.abs(deltaY) > 3) {
        hasDraggedRef.current = true
      }
      pendingNodeOffsetRef.current = {
        nodeId: drag.nodeId,
        offset: {
          x: drag.baseOffsetX + deltaX,
          y: drag.baseOffsetY + deltaY,
        },
      }

      if (nodeDragFrameRef.current === undefined) {
        nodeDragFrameRef.current = window.requestAnimationFrame(() => {
          const pending = pendingNodeOffsetRef.current
          nodeDragFrameRef.current = undefined
          if (!pending) return
          setGraphNodeOffsets((prev) => ({ ...prev, [pending.nodeId]: pending.offset }))
        })
      }
      return
    }

    if (!panStateRef.current.isPanning) return

    const deltaX = clientX - panStateRef.current.startX
    const deltaY = clientY - panStateRef.current.startY
    if (Math.abs(deltaX) > 3 || Math.abs(deltaY) > 3) {
      hasDraggedRef.current = true
    }

    setView({
      panX: panStateRef.current.basePanX + deltaX,
      panY: panStateRef.current.basePanY + deltaY,
    })
  }, [])

  const handlePanEnd = useCallback(() => {
    const pending = pendingNodeOffsetRef.current
    if (nodeDragFrameRef.current !== undefined) {
      window.cancelAnimationFrame(nodeDragFrameRef.current)
      nodeDragFrameRef.current = undefined
    }
    if (pending) {
      setGraphNodeOffsets((prev) => ({ ...prev, [pending.nodeId]: pending.offset }))
      pendingNodeOffsetRef.current = undefined
    }
    panStateRef.current.isPanning = false
    nodeDragStateRef.current = undefined
    setIsPanning(false)
  }, [])

  const handleNodeDragStart = (event: MouseEvent<HTMLElement>, nodeId: string) => {
    if (event.button !== 0) return
    event.preventDefault()
    event.stopPropagation()
    panStateRef.current.isPanning = false
    const offset = graphNodeOffsets[nodeId] ?? { x: 0, y: 0 }
    nodeDragStateRef.current = {
      nodeId,
      startX: event.clientX,
      startY: event.clientY,
      baseOffsetX: offset.x,
      baseOffsetY: offset.y,
    }
    hasDraggedRef.current = false
    setIsPanning(true)
  }

  useEffect(() => {
    if (!isPanning) return undefined

    const onMove = (event: globalThis.MouseEvent) => moveInteraction(event.clientX, event.clientY)

    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', handlePanEnd)
    return () => {
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', handlePanEnd)
    }
  }, [isPanning, moveInteraction, handlePanEnd])

  /* ----- Zoom & view ----- */

  const centerView = useCallback((nextZoom: number = zoomRef.current) => {
    const board = graphBoardRef.current
    if (!board) return
    const { clientWidth, clientHeight } = board
    if (clientWidth === 0 || clientHeight === 0) return
    setView({
      panX: (clientWidth - graphLayout.width * nextZoom) / 2,
      panY: (clientHeight - graphLayout.height * nextZoom) / 2,
    })
  }, [graphLayout.height, graphLayout.width])

  const zoomAtPoint = useCallback((nextZoom: number, pivotClientX?: number, pivotClientY?: number) => {
    const clamped = Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, Number(nextZoom.toFixed(2))))
    const currentZoom = zoomRef.current
    if (clamped === currentZoom) return

    const board = graphBoardRef.current
    if (!board) {
      setZoom(clamped)
      return
    }

    const boardRect = board.getBoundingClientRect()
    const pivotX = pivotClientX ?? boardRect.left + board.clientWidth / 2
    const pivotY = pivotClientY ?? boardRect.top + board.clientHeight / 2

    const localX = pivotX - boardRect.left
    const localY = pivotY - boardRect.top
    const { panX, panY } = viewRef.current
    const worldX = (localX - panX) / currentZoom
    const worldY = (localY - panY) / currentZoom

    setZoom(clamped)
    setView({
      panX: localX - worldX * clamped,
      panY: localY - worldY * clamped,
    })
  }, [])

  const handleZoomIn = useCallback(() => zoomAtPoint(zoomRef.current + ZOOM_STEP), [zoomAtPoint])
  const handleZoomOut = useCallback(() => zoomAtPoint(zoomRef.current - ZOOM_STEP), [zoomAtPoint])
  const handleZoomReset = useCallback(() => {
    setZoom(ZOOM_DEFAULT)
    requestAnimationFrame(() => centerView(ZOOM_DEFAULT))
  }, [centerView])

  const handleZoomFit = useCallback(() => {
    const board = graphBoardRef.current
    if (!board) return
    const { clientWidth, clientHeight } = board
    if (clientWidth === 0 || clientHeight === 0) return
    const fitZoomX = (clientWidth - 32) / graphLayout.width
    const fitZoomY = (clientHeight - 32) / graphLayout.height
    const fit = Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, Math.min(fitZoomX, fitZoomY)))
    setZoom(fit)
    requestAnimationFrame(() => centerView(fit))
  }, [centerView, graphLayout.height, graphLayout.width])

  /* Native wheel listener with passive: false to allow preventDefault for
     ctrl-zoom and shift-pan (React's onWheel is passive in modern browsers). */
  useEffect(() => {
    if (!isWorkspaceOpen) return undefined
    const board = graphBoardRef.current
    if (!board) return undefined

    const onWheel = (event: globalThis.WheelEvent) => {
      event.preventDefault()

      if (event.ctrlKey || event.metaKey) {
        const delta = event.deltaY > 0 ? -ZOOM_STEP : ZOOM_STEP
        zoomAtPoint(zoomRef.current + delta, event.clientX, event.clientY)
        return
      }

      const stepX = (event.shiftKey ? event.deltaY : event.deltaX) * WHEEL_PAN_STEP
      const stepY = (event.shiftKey ? 0 : event.deltaY) * WHEEL_PAN_STEP

      setView((prev) => ({
        panX: prev.panX - stepX,
        panY: prev.panY - stepY,
      }))
    }

    board.addEventListener('wheel', onWheel, { passive: false })
    return () => board.removeEventListener('wheel', onWheel)
  }, [isWorkspaceOpen, zoomAtPoint])

  /* ----- Workspace lifecycle ----- */

  useEffect(() => {
    if (!isWorkspaceOpen) return undefined

    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'

    const onKeyDown = (event: globalThis.KeyboardEvent) => {
      if (event.key === 'Escape') {
        if (activeRelationKey) {
          setActiveRelationKey('')
          return
        }
        if (focusedTableId) {
          setFocusedTableId('')
          setGraphNodeOffsets({})
          return
        }
        setIsWorkspaceOpen(false)
      }
    }

    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.body.style.overflow = previousOverflow
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [isWorkspaceOpen, activeRelationKey, focusedTableId])

  /* ----- Reset state when focus changes ----- */

  useEffect(() => {
    setActiveRelationKey('')
    setHoveredRelationKey('')
  }, [focusedTableId])

  /* ----- Auto-fit & auto-center on workspace open or focus change ----- */

  useEffect(() => {
    if (!isWorkspaceOpen) {
      hasInitializedViewRef.current = false
      return
    }
    const board = graphBoardRef.current
    if (!board) return

    const frame = window.requestAnimationFrame(() => {
      if (!board) return
      if (board.clientWidth === 0 || board.clientHeight === 0) return

      const padding = 32
      const fitX = (board.clientWidth - padding * 2) / graphLayout.width
      const fitY = (board.clientHeight - padding * 2) / graphLayout.height
      const fitZoom = Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, Math.min(fitX, fitY, 1)))

      setZoom(fitZoom)

      if (focusedTableId) {
        const focusedNode = graphLayout.nodes.find((node) => node.id === focusedTableId)
        if (focusedNode) {
          const cx = focusedNode.x + focusedNode.width / 2
          const cy = focusedNode.y + focusedNode.height / 2
          setView({
            panX: board.clientWidth / 2 - cx * fitZoom,
            panY: board.clientHeight / 2 - cy * fitZoom,
          })
          hasInitializedViewRef.current = true
          return
        }
      }

      // Center the whole canvas inside the viewport.
      setView({
        panX: (board.clientWidth - graphLayout.width * fitZoom) / 2,
        panY: (board.clientHeight - graphLayout.height * fitZoom) / 2,
      })
      hasInitializedViewRef.current = true
    })
    return () => window.cancelAnimationFrame(frame)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [focusedTableId, isWorkspaceOpen])

  /* ----- Re-center when board resizes while open ----- */

  useEffect(() => {
    if (!isWorkspaceOpen) return undefined
    const board = graphBoardRef.current
    if (!board) return undefined

    const observer = new ResizeObserver(() => {
      if (!hasInitializedViewRef.current) return
      // Recenter only if the user hasn't manually panned much — heuristic: if
      // the canvas would be entirely off-screen after resize, recenter it.
      const { panX, panY } = viewRef.current
      const z = zoomRef.current
      const canvasRight = panX + graphLayout.width * z
      const canvasBottom = panY + graphLayout.height * z
      if (canvasRight < 80 || canvasBottom < 80
        || panX > board.clientWidth - 80 || panY > board.clientHeight - 80) {
        centerView(z)
      }
    })
    observer.observe(board)
    return () => observer.disconnect()
  }, [centerView, graphLayout.height, graphLayout.width, isWorkspaceOpen])

  const focusTable = useCallback((tableId: string) => {
    if (!tableId) {
      setFocusedTableId('')
      setGraphNodeOffsets({})
      return
    }
    setFocusedTableId(tableId)
    setGraphNodeOffsets({})
    setActiveRelationKey('')
  }, [])

  const handleNodeClick = (nodeId: string) => {
    if (hasDraggedRef.current) return
    if (focusedTableId === nodeId) {
      setFocusedTableId('')
      setGraphNodeOffsets({})
      return
    }
    focusTable(nodeId)
  }

  const handleNodeKeyDown = (event: KeyboardEvent<HTMLElement>, nodeId: string) => {
    if (event.key !== 'Enter' && event.key !== ' ') return
    event.preventDefault()
    focusTable(nodeId)
  }

  const handleEdgeClick = (relationKey: string) => {
    setActiveRelationKey((current) => (current === relationKey ? '' : relationKey))
  }

  /* ----- Workspace JSX ----- */

  const workspace = (
    <div className={styles.workspace} role="dialog" aria-modal="true" aria-label={title ?? 'Схема базы данных'}>
      <div className={styles.workspaceHeader}>
        <div className={styles.workspaceHeaderInfo}>
          <div className={styles.workspaceTitle}>{title ?? 'Схема базы данных'}</div>
          <div className={styles.workspaceSubtitle}>
            {focusedTable ? (
              <>
                <span>Контекст:</span>
                <span className={styles.workspaceCrumb}>
                  <code>{focusedTable.title}</code>
                  <button
                    type="button"
                    className={styles.workspaceCrumbReset}
                    onClick={() => focusTable('')}
                    aria-label="Показать все таблицы"
                  >
                    ✕
                  </button>
                </span>
              </>
            ) : (
              <span>Интерактивная схема базы данных</span>
            )}
          </div>
          {legend?.length ? (
            <div className={styles.workspaceLegend}>
              {legend.map((item) => (
                <span key={item.label} className={styles.legendItem}>
                  <span className={clsx(styles.legendSwatch, getToneClass(item.tone))} />
                  <span>{item.label}</span>
                </span>
              ))}
            </div>
          ) : null}
        </div>

        <div className={styles.workspaceActions}>
          <select
            className={styles.selectInput}
            value={focusedTableId}
            onChange={(event) => focusTable(event.target.value)}
            aria-label="Выбрать таблицу"
          >
            <option value="">Все таблицы</option>
            {tables.slice().sort(sortTablesByTitle).map((table) => (
              <option key={table.id} value={table.id}>
                {TONE_LABEL[(table.tone ?? 'resource') as TDbSchemaTone]} · {table.title}
              </option>
            ))}
          </select>

          <button
            type="button"
            className={styles.iconButton}
            onClick={() => {
              setGraphNodeOffsets({})
              setActiveRelationKey('')
            }}
            aria-label="Сбросить расположение"
            title="Сбросить расположение карточек"
          >
            ⟲
          </button>

          <button
            type="button"
            className={clsx(styles.controlButton, styles.controlButtonPrimary)}
            onClick={() => setIsWorkspaceOpen(false)}
          >
            Закрыть
          </button>
        </div>
      </div>

      <div className={styles.workspaceBody}>
        <div
          ref={graphBoardRef}
          className={clsx(styles.graphBoard, isPanning && styles.graphBoardPanning)}
          onMouseDown={handlePanStart}
          tabIndex={0}
          style={{
            backgroundPosition: `${view.panX}px ${view.panY}px, 0 0`,
          }}
        >
          {graphLayout.nodes.length === 0 ? (
            <div className={styles.graphEmpty}>
              Не удалось построить граф схемы.
            </div>
          ) : (
            <div
              className={styles.graphCanvasPan}
              style={{
                left: `${Math.round(view.panX)}px`,
                top: `${Math.round(view.panY)}px`,
              }}
            >
              <div
                ref={graphCanvasRef}
                className={styles.graphCanvas}
                style={{
                  width: `${graphLayout.width}px`,
                  height: `${graphLayout.height}px`,
                  zoom,
                }}
              >
              <svg
                className={styles.graphEdges}
                viewBox={`0 0 ${graphLayout.width} ${graphLayout.height}`}
                width={graphLayout.width}
                height={graphLayout.height}
              >
                <defs>
                  <marker
                    id="dbschema-arrow"
                    viewBox="0 0 12 12"
                    refX="10"
                    refY="6"
                    markerWidth="13"
                    markerHeight="13"
                    orient="auto"
                    markerUnits="userSpaceOnUse"
                  >
                    <path d="M 0 1 L 10 6 L 0 11 z" />
                  </marker>
                  <marker
                    id="dbschema-arrow-active"
                    viewBox="0 0 12 12"
                    refX="10"
                    refY="6"
                    markerWidth="15"
                    markerHeight="15"
                    orient="auto"
                    markerUnits="userSpaceOnUse"
                    className={styles.markerActive}
                  >
                    <path d="M 0 1 L 10 6 L 0 11 z" />
                  </marker>
                  <marker
                    id="dbschema-dot"
                    viewBox="0 0 8 8"
                    refX="4"
                    refY="4"
                    markerWidth="9"
                    markerHeight="9"
                    orient="auto"
                    markerUnits="userSpaceOnUse"
                  >
                    <circle cx="4" cy="4" r="3" />
                  </marker>
                </defs>

                <g className={styles.graphEdgeLayer}>
                  {graphLayout.edges.map((edge, index) => {
                    const relationKey = getRelationKey(edge.relation, index)
                    const isActive = relationKey === activeRelationKey
                    const isHovered = relationKey === hoveredRelationKey
                    const isDimmed = activeRelationKey !== '' && !isActive

                    return (
                      <g
                        key={relationKey}
                        className={styles.graphEdgeGroup}
                        onMouseEnter={() => setHoveredRelationKey(relationKey)}
                        onMouseLeave={() => setHoveredRelationKey((current) => current === relationKey ? '' : current)}
                        onMouseDown={(event) => event.stopPropagation()}
                        onClick={() => handleEdgeClick(relationKey)}
                      >
                        <title>{`${edge.relation.fromTable}.${edge.relation.fromField ?? '*'} → ${edge.relation.toTable}.${edge.relation.toField ?? '*'}`}</title>
                        <path className={styles.graphEdgeHitPath} d={edge.path} />
                        <path
                          className={clsx(
                            styles.graphEdgePath,
                            isActive && styles.graphEdgePathActive,
                            isHovered && !isActive && styles.graphEdgePathHovered,
                            isDimmed && styles.graphEdgePathDimmed,
                          )}
                          d={edge.path}
                          markerStart="url(#dbschema-dot)"
                          markerEnd={isActive ? 'url(#dbschema-arrow-active)' : 'url(#dbschema-arrow)'}
                        />
                        {isActive ? (
                          <>
                            <path className={styles.graphEdgeGlowPath} d={edge.path} />
                            <path className={styles.graphEdgeAnimatedPath} d={edge.path} />
                            <circle className={styles.graphEdgeEndpoint} cx={edge.start.x} cy={edge.start.y} r={3.5} />
                            <circle className={styles.graphEdgeEndpoint} cx={edge.end.x} cy={edge.end.y} r={3.5} />
                          </>
                        ) : null}
                      </g>
                    )
                  })}
                </g>
              </svg>

              {graphLayout.nodes.map((node) => {
                const isFocused = node.id === focusedTableId
                const isRelated = focusedTableId !== '' && relatedToFocused.has(node.id) && !isFocused
                const isDimmed = focusedTableId !== '' && !isFocused && !isRelated

                return (
                  <article
                    key={node.id}
                    className={clsx(
                      styles.graphNode,
                      getToneClass(node.table.tone),
                      isFocused && styles.graphNodeFocused,
                      isRelated && styles.graphNodeRelated,
                      isDimmed && styles.graphNodeDimmed,
                    )}
                    style={{
                      left: `${node.x}px`,
                      top: `${node.y}px`,
                      width: `${node.width}px`,
                    }}
                    role="button"
                    tabIndex={0}
                    aria-pressed={isFocused}
                    data-graph-node-id={node.id}
                    onMouseDown={(event) => handleNodeDragStart(event, node.id)}
                    onClick={() => handleNodeClick(node.id)}
                    onKeyDown={(event) => handleNodeKeyDown(event, node.id)}
                  >
                    <header className={styles.tableHeader}>
                      <code>{node.table.title}</code>
                      <span className={styles.tableRelationCount}>
                        {relations.filter((relation) => isRelationConnectedTo(relation, node.id)).length}
                      </span>
                    </header>

                    <div className={styles.tableMetaBar}>
                      <span className={styles.metaAccent}>{node.table.fields.length} полей</span>
                      {node.table.indexes?.length ? <span>{node.table.indexes.length} инд.</span> : null}
                      {node.table.note ? <span title={node.table.note}>note</span> : null}
                    </div>

                    {node.table.note ? (
                      <div className={styles.tableNote}>{node.table.note}</div>
                    ) : null}

                    <div className={styles.fieldList}>
                      {node.table.fields.map((field) => {
                        const fieldKey = getFieldAnchorKey(node.id, field.name)
                        const isSourceField = accentSourceFieldKey === fieldKey
                        const isTargetField = accentTargetFieldKey === fieldKey
                        const isHighlighted = isSourceField || isTargetField
                        const isDimmedField = accentEdge !== undefined && !isHighlighted

                        return (
                          <div
                            key={`${node.id}-${field.name}`}
                            className={clsx(
                              styles.fieldRow,
                              isSourceField && styles.fieldRowHighlightedSource,
                              isTargetField && styles.fieldRowHighlightedTarget,
                              isDimmedField && styles.fieldRowDimmed,
                            )}
                            data-graph-field-name={field.name}
                          >
                            <div className={styles.fieldName}>
                              <code>{field.name}</code>
                              {renderFieldKey(field)}
                              <FieldTooltip
                                field={field}
                                enumValues={field.type ? enumValuesByTitle[field.type] : undefined}
                              />
                            </div>
                            {field.type ? <code className={styles.fieldType}>{field.type}</code> : null}
                          </div>
                        )
                      })}
                    </div>
                  </article>
                )
              })}
              </div>
            </div>
          )}

          <div className={styles.graphZoomBar}>
            <button type="button" className={styles.iconButton} onClick={handleZoomOut} aria-label="Уменьшить">−</button>
            <button type="button" className={styles.zoomValueButton} onClick={handleZoomReset} aria-label="100%" title="100%">
              {Math.round(zoom * 100)}%
            </button>
            <button type="button" className={styles.iconButton} onClick={handleZoomIn} aria-label="Увеличить">+</button>
            <button type="button" className={styles.iconButton} onClick={handleZoomFit} aria-label="По размеру" title="По размеру">⤢</button>
          </div>

          <div className={styles.graphHints}>
            <span>Перетаскивайте карточки и фон.</span>
            <span><kbd>Ctrl</kbd>+колесо — масштаб.</span>
          </div>
        </div>

      </div>
    </div>
  )

  return (
    <div className={styles.wrapper}>
      <div className={styles.preview}>
        <div className={styles.previewContent}>
          {title ? <div className={styles.title}>{title}</div> : null}
          {description ? <p className={styles.description}>{description}</p> : null}

          {legend?.length ? (
            <div className={styles.legend}>
              {legend.map((item) => (
                <div key={item.label} className={styles.legendItem}>
                  <span className={clsx(styles.legendSwatch, getToneClass(item.tone))} />
                  <span>{item.label}</span>
                </div>
              ))}
            </div>
          ) : null}
        </div>

        <button
          type="button"
          className={clsx(styles.openButton, styles.controlButtonPrimary)}
          onClick={() => setIsWorkspaceOpen(true)}
        >
          Открыть схему
        </button>
      </div>

      {isWorkspaceOpen ? workspace : null}
    </div>
  )
}
