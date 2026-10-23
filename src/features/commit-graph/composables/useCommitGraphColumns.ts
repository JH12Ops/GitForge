import { computed, ref } from 'vue'
import { storeToRefs } from 'pinia'
import type { MenuItem } from '@/components/ui/menu'
import type { CommitColumnDropSide, CommitColumnWidthKey } from '@/lib/preferences'
import { useUiStore } from '@/stores/ui'

export const ROW_HORIZONTAL_PADDING = 24
/** Matches Tailwind `pl-3` on commit graph rows. */
export const ROW_LEFT_PADDING = ROW_HORIZONTAL_PADDING / 2

/**
 * Horizontal distance from the refs column's right edge to the graph
 * column's left edge: 0 when adjacent, otherwise the summed widths of any
 * columns dragged between them. Returns `-Infinity` when the graph column
 * sits left of the refs column, which collapses the rightward-only
 * refs→node connector lines to zero width.
 */
export function refsToGraphGap(): number {
  const ui = useUiStore()
  const order = ui.columnOrder
  const refsIdx = order.indexOf('refs')
  const graphIdx = order.indexOf('graph')
  if (refsIdx < 0 || graphIdx < 0) return 0
  if (refsIdx >= graphIdx) return Number.NEGATIVE_INFINITY
  let gap = 0
  for (let i = refsIdx + 1; i < graphIdx; i++) {
    gap += ui.columnWidths[order[i]] ?? 0
  }
  return gap
}

export function useCommitGraphColumns() {
  const ui = useUiStore()
  const { columns, columnWidths } = storeToRefs(ui)

  const refsColumnWidth = computed(() => columnWidths.value.refs)
  const graphColumnWidth = computed(() => columnWidths.value.graph)

  /** All renderable column keys — incl. the pinned refs/graph pair — in saved order. */
  const orderedColumnOrder = computed<CommitColumnWidthKey[]>(
    () => ui.orderedColumnOrder,
  )

  const columnMenu = computed<MenuItem[]>(() => [
    {
      label: 'Author',
      checked: ui.isColumnVisible('author'),
      onSelect: () => ui.toggleColumn('author'),
    },
    {
      label: 'SHA',
      checked: ui.isColumnVisible('sha'),
      onSelect: () => ui.toggleColumn('sha'),
    },
    {
      label: 'When',
      checked: ui.isColumnVisible('when'),
      onSelect: () => ui.toggleColumn('when'),
    },
  ])

  const rowContentWidth = computed(() => {
    let width =
      refsColumnWidth.value + graphColumnWidth.value + columnWidths.value.commit
    if (columns.value.sha) width += columnWidths.value.sha
    if (columns.value.when) width += columnWidths.value.when
    return width + ROW_HORIZONTAL_PADDING
  })

  function resizeColumn(key: CommitColumnWidthKey, deltaX: number): void {
    ui.setColumnWidth(key, columnWidths.value[key] + deltaX)
  }

  // Drag to reorder: pointer-based, because HTML5 drag & drop is unreliable
  // inside the WKWebView the Wails app runs on.

  const dragColumnKey = ref<CommitColumnWidthKey | null>(null)
  const dropMarker = ref<{ key: CommitColumnWidthKey; side: CommitColumnDropSide } | null>(null)

  function hitTestDrag(x: number, y: number, sourceKey: CommitColumnWidthKey) {
    const el = document.elementFromPoint(x, y)
    const cell = el instanceof Element ? el.closest<HTMLElement>('[data-graph-column]') : null
    const key = cell?.dataset.graphColumn
    if (!key || key === sourceKey) return null
    const rect = cell!.getBoundingClientRect()
    const side: CommitColumnDropSide = x - rect.left >= rect.width / 2 ? 'right' : 'left'
    return { key: key as CommitColumnWidthKey, side }
  }

  /**
   * Start a header drag: track the pointer with document listeners (the same
   * pattern as `ColumnResizeHandle`), preview the drop position, and commit
   * the reorder on mouseup. Escape cancels.
   */
  function onColumnDragBegin(key: CommitColumnWidthKey, event: MouseEvent): void {
    if (event.button !== 0 || dragColumnKey.value) return
    dragColumnKey.value = key
    document.body.style.cursor = 'grabbing'
    document.body.style.userSelect = 'none'

    function cleanup(): void {
      document.removeEventListener('mousemove', onMove)
      document.removeEventListener('mouseup', onUp)
      document.removeEventListener('keydown', onKey)
      document.body.style.cursor = ''
      document.body.style.userSelect = ''
    }

    function terminate(): void {
      dragColumnKey.value = null
      dropMarker.value = null
      cleanup()
    }

    function onMove(ev: MouseEvent): void {
      if (ev.buttons === 0) {
        onUp(ev)
        return
      }
      dropMarker.value = hitTestDrag(ev.clientX, ev.clientY, key)
    }

    function onUp(ev: MouseEvent): void {
      void ev
      const dragged = dragColumnKey.value
      const marker = dropMarker.value
      terminate()
      if (dragged && marker) ui.moveColumn(dragged, marker.key, marker.side)
    }

    function onKey(ev: KeyboardEvent): void {
      if (ev.key === 'Escape') terminate()
    }

    document.addEventListener('mousemove', onMove)
    document.addEventListener('mouseup', onUp)
    document.addEventListener('keydown', onKey)
  }

  return {
    columns,
    columnWidths,
    refsColumnWidth,
    graphColumnWidth,
    orderedColumnOrder,
    columnMenu,
    rowContentWidth,
    resizeColumn,
    dragColumnKey,
    dropMarker,
    onColumnDragBegin,
  }
}
