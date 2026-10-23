<script setup lang="ts">
import ColumnResizeHandle from '@/components/ui/ColumnResizeHandle.vue'
import type { CommitColumnDropSide } from '@/lib/preferences'

withDefaults(
  defineProps<{
    label: string
    width?: number
    align?: 'left' | 'right'
    resizable?: boolean
    /** Opt in to pointer drag for reordering; pinned columns stay put. */
    draggable?: boolean
    /** Stable hit-test key used to find the drop cell via `data-graph-column`. */
    columnKey?: string
    dragging?: boolean
    dropSide?: CommitColumnDropSide | null
  }>(),
  {
    width: undefined,
    align: undefined,
    resizable: false,
    draggable: false,
    columnKey: undefined,
    dragging: false,
    dropSide: null,
  },
)

const emit = defineEmits<{
  resize: [deltaX: number]
  dragBegin: [MouseEvent]
}>()
</script>

<template>
  <div
    class="relative flex shrink-0 items-center truncate px-2"
    :class="[
      align === 'right' ? 'justify-end text-right' : '',
      draggable ? 'app-no-drag cursor-grab active:cursor-grabbing' : '',
      dragging ? 'opacity-40' : '',
    ]"
    :style="width != null ? { width: `${width}px` } : undefined"
    :data-graph-column="columnKey"
    @mousedown.left="draggable ? emit('dragBegin', $event) : undefined"
  >
    <span class="truncate">{{ label }}</span>
    <span
      v-if="dropSide"
      class="pointer-events-none absolute top-1/2 z-20 h-4 w-[2px] -translate-y-1/2 rounded-full bg-[var(--color-accent)]"
      :class="dropSide === 'left' ? 'left-0 -translate-x-1/2' : 'right-0 translate-x-1/2'"
      aria-hidden="true"
    />
    <ColumnResizeHandle v-if="resizable" @resize="emit('resize', $event)" />
  </div>
</template>
