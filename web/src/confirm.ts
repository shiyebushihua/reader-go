import { ref } from 'vue'

export interface ConfirmOptions {
  title?: string
  message: string
  confirmText?: string
  cancelText?: string
  danger?: boolean
}

interface ConfirmState extends ConfirmOptions {
  visible: boolean
  resolve?: (value: boolean) => void
}

export const confirmState = ref<ConfirmState>({
  visible: false,
  message: '',
  title: '提示',
  confirmText: '确认',
  cancelText: '取消',
  danger: false
})

export function showConfirm(options: string | ConfirmOptions): Promise<boolean> {
  const opts: ConfirmOptions = typeof options === 'string'
    ? { message: options }
    : options

  return new Promise((resolve) => {
    confirmState.value = {
      visible: true,
      title: opts.title || (opts.danger ? '危险操作确认' : '确认提示'),
      message: opts.message,
      confirmText: opts.confirmText || (opts.danger ? '确认删除' : '确认'),
      cancelText: opts.cancelText || '取消',
      danger: !!opts.danger,
      resolve
    }
  })
}

export function handleConfirm(ok: boolean) {
  const r = confirmState.value.resolve
  confirmState.value.visible = false
  if (r) {
    r(ok)
  }
}
