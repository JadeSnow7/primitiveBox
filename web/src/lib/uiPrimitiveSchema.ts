import { z } from 'zod'

export const PANEL_TYPES = ['trace', 'event_stream', 'sandbox', 'checkpoint', 'diff', 'primitive', 'goal'] as const
export const PanelTypeSchema = z.enum(PANEL_TYPES)

const SemanticRefSchema = z.object({
  type: PanelTypeSchema,
  index: z.number().int().min(0).optional(),
})

export const UIPrimitiveSchema = z.discriminatedUnion('method', [
  z.object({
    method: z.literal('ui.panel.open'),
    params: z.object({
      type: PanelTypeSchema,
      props: z.record(z.unknown()).default({}),
      target: SemanticRefSchema.optional(),
      entityId: z.string().optional(),
      entityIds: z.array(z.string()).optional(),
    }),
  }),
  z.object({
    method: z.literal('ui.panel.close'),
    params: z.object({ target: SemanticRefSchema }),
  }),
  z.object({
    method: z.literal('ui.layout.split'),
    params: z.object({
      target: SemanticRefSchema,
      direction: z.enum(['horizontal', 'vertical']),
    }),
  }),
  z.object({
    method: z.literal('ui.focus.panel'),
    params: z.object({ target: SemanticRefSchema }),
  }),
])

export const UIPrimitivesArraySchema = z.array(UIPrimitiveSchema)

export type ValidatedUIPrimitive = z.infer<typeof UIPrimitiveSchema>
