<script setup lang="ts">
import { onMounted, onUnmounted } from 'vue'
import { AlertTriangle, Info } from 'lucide-vue-next'
import { confirmState, handleConfirm } from '../confirm'

function onKeyDown(e: KeyboardEvent) {
  if (!confirmState.value.visible) return
  if (e.key === 'Escape') {
    e.preventDefault()
    handleConfirm(false)
  }
}

onMounted(() => {
  window.addEventListener('keydown', onKeyDown)
})

onUnmounted(() => {
  window.removeEventListener('keydown', onKeyDown)
})
</script>

<template>
  <Teleport to="body">
    <div
      v-if="confirmState.visible"
      class="confirm-backdrop"
      role="dialog"
      aria-modal="true"
      aria-labelledby="confirm-modal-title"
      @click.self="handleConfirm(false)"
    >
      <div class="confirm-card" :class="{ 'is-danger': confirmState.danger }">
        <div class="confirm-header">
          <div class="confirm-icon" :class="confirmState.danger ? 'icon-danger' : 'icon-info'">
            <AlertTriangle v-if="confirmState.danger" :size="22" />
            <Info v-else :size="22" />
          </div>
          <div class="confirm-heading">
            <h3 id="confirm-modal-title" class="confirm-title">{{ confirmState.title }}</h3>
            <p class="confirm-message">{{ confirmState.message }}</p>
          </div>
        </div>

        <div class="confirm-actions">
          <button
            type="button"
            class="button confirm-btn-cancel"
            @click="handleConfirm(false)"
          >
            {{ confirmState.cancelText }}
          </button>
          <button
            type="button"
            class="button"
            :class="confirmState.danger ? 'danger-button' : 'primary'"
            @click="handleConfirm(true)"
          >
            {{ confirmState.confirmText }}
          </button>
        </div>
      </div>
    </div>
  </Teleport>
</template>
