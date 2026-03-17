import { useSandboxStore } from '@/store/sandboxStore'

export function useSandbox() {
  const sandboxes = useSandboxStore((s) => s.sandboxes)
  const selectedId = useSandboxStore((s) => s.selectedId)
  const loading = useSandboxStore((s) => s.loading)
  const error = useSandboxStore((s) => s.error)
  const capabilityNotice = useSandboxStore((s) => s.capabilityNotice)
  const load = useSandboxStore((s) => s.load)
  const refreshSelected = useSandboxStore((s) => s.refreshSelected)
  const select = useSandboxStore((s) => s.select)
  const create = useSandboxStore((s) => s.create)
  const destroy = useSandboxStore((s) => s.destroy)

  const selectedSandbox = sandboxes.find((item) => item.id === selectedId) ?? null

  return {
    sandboxes,
    selectedId,
    selectedSandbox,
    loading,
    error,
    capabilityNotice,
    load,
    refreshSelected,
    select,
    create,
    destroy
  }
}
