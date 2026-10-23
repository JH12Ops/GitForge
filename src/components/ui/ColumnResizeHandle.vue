<script setup lang="ts">
const emit = defineEmits<{ resize: [deltaX: number] }>()

function onMouseDown(event: MouseEvent): void {
  event.preventDefault()
  event.stopPropagation()
  let lastX = event.clientX

  function onMove(ev: MouseEvent): void {
    const delta = ev.clientX - lastX
    lastX = ev.clientX
    emit('resize', delta)
  }

  function onUp(): void {
    document.removeEventListener('mousemove', onMove)
    document.removeEventListener('mouseup', onUp)
    document.body.style.cursor = ''
    document.body.style.userSelect = ''
  }

  document.body.style.cursor = 'col-resize'
  document.body.style.userSelect = 'none'
  document.addEventListener('mousemove', onMove)
  document.addEventListener('mouseup', onUp)
}
</script>

<template>
  <div
    class="app-no-drag group absolute top-0 right-0 z-10 h-full w-2 cursor-col-resize touch-none hover:bg-[var(--color-accent)]/40"
    @mousedown="onMouseDown"
  >
    <span
      class="pointer-events-none absolute inset-y-0 right-0 w-px bg-[var(--color-border-strong)] transition-colors group-hover:bg-[var(--color-accent)]"
      aria-hidden="true"
    />
  </div>
</template>
