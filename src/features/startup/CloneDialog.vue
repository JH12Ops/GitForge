<script setup lang="ts">
import { ref, watch } from 'vue'
import Dialog from '@/components/ui/Dialog.vue'
import Button from '@/components/ui/Button.vue'
import { pickFolder } from '@/lib/shell/repoCommands'

const props = defineProps<{ open: boolean; busy: boolean }>()

const emit = defineEmits<{
  close: []
  submit: [{ url: string; parentPath: string; name: string }]
}>()

const url = ref('')
const parentPath = ref('')
const name = ref('')
const error = ref('')

watch(
  () => props.open,
  (isOpen) => {
    if (isOpen) {
      error.value = ''
    }
  },
)

async function pickParent(): Promise<void> {
  const picked = await pickFolder()
  if (picked) parentPath.value = picked
}

function onSubmit(): void {
  if (!url.value.trim()) {
    error.value = 'Enter a repository URL.'
    return
  }
  if (!parentPath.value.trim()) {
    error.value = 'Choose a destination folder.'
    return
  }
  error.value = ''
  emit('submit', { url: url.value.trim(), parentPath: parentPath.value.trim(), name: name.value.trim() })
}
</script>

<template>
  <Dialog :open="open" title="Clone repository" @confirm="onSubmit" @cancel="emit('close')">
    <label class="block text-xs font-medium text-[var(--color-fg-muted)]">
      Repository URL
      <input
        v-model="url"
        type="text"
        placeholder="https://github.com/org/repo.git"
        spellcheck="false"
        class="focus-ring mt-1 w-full rounded-md border border-[var(--color-border-strong)] bg-[var(--color-panel-raised)] px-3 py-2 text-sm text-[var(--color-fg)] outline-none"
        @keydown.enter="onSubmit"
      />
    </label>
    <label class="block text-xs font-medium text-[var(--color-fg-muted)]">
      Destination folder
      <div class="mt-1 flex gap-2">
        <input
          v-model="parentPath"
          type="text"
          placeholder="/Users/you/Projects"
          spellcheck="false"
          class="focus-ring min-w-0 flex-1 rounded-md border border-[var(--color-border-strong)] bg-[var(--color-panel-raised)] px-3 py-2 text-sm text-[var(--color-fg)] outline-none"
        />
        <Button size="sm" @click="pickParent">Browse…</Button>
      </div>
    </label>
    <label class="block text-xs font-medium text-[var(--color-fg-muted)]">
      Folder name <span class="font-normal">(optional — derived from URL)</span>
      <input
        v-model="name"
        type="text"
        placeholder="repo"
        spellcheck="false"
        class="focus-ring mt-1 w-full rounded-md border border-[var(--color-border-strong)] bg-[var(--color-panel-raised)] px-3 py-2 text-sm text-[var(--color-fg)] outline-none"
        @keydown.enter="onSubmit"
      />
    </label>
    <p class="text-xs text-[var(--color-fg-muted)]">
      Authentication uses your SSH keys or system credential manager — no sign-in inside GitForge.
    </p>
    <p v-if="error" class="text-xs text-[var(--color-danger)]">{{ error }}</p>

    <template #footer>
      <Button variant="ghost" :disabled="busy" @click="emit('close')">Cancel</Button>
      <Button variant="primary" :busy="busy" :disabled="busy" @click="onSubmit">
        {{ busy ? 'Cloning…' : 'Clone' }}
      </Button>
    </template>
  </Dialog>
</template>
