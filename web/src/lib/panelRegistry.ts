import type { ComponentType } from 'react'
import { CheckpointPanel } from '@/components/workspace/panels/CheckpointPanel'
import { DiffPanel } from '@/components/workspace/panels/DiffPanel'
import { DynamicPrimitivePanel } from '@/components/workspace/panels/DynamicPrimitivePanel'
import { EventStreamPanel } from '@/components/workspace/panels/EventStreamPanel'
import { GoalPanel } from '@/components/workspace/panels/GoalPanel'
import { SandboxPanel } from '@/components/workspace/panels/SandboxPanel'
import { TracePanel } from '@/components/workspace/panels/TracePanel'
import type { PanelType, WorkspacePanel } from '@/types/workspace'

type PanelComponent = ComponentType<{ panel: WorkspacePanel }>

const PANEL_COMPONENTS: Record<PanelType, PanelComponent> = {
  trace: TracePanel,
  event_stream: EventStreamPanel,
  sandbox: SandboxPanel,
  checkpoint: CheckpointPanel,
  diff: DiffPanel,
  primitive: DynamicPrimitivePanel,
  goal: GoalPanel,
}

export function resolvePanelView(type: PanelType): PanelComponent {
  return PANEL_COMPONENTS[type] ?? DynamicPrimitivePanel
}
