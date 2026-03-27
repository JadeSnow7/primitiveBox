export interface PrimitiveInfo {
  name: string
  description: string
  kind: 'system' | 'app'
}

export interface PrimitiveIntentPayload {
  category: string
  side_effect: string
  reversible: boolean
  risk_level: 'low' | 'medium' | 'high'
}

export interface AppPrimitiveManifest {
  app_id: string
  name: string
  description: string
  input_schema: object
  output_schema: object
  ui_layout_hint?: string
  socket_path: string
  intent: {
    category: string
    reversible: boolean
    risk_level: string
    side_effect?: string
  }
}

export interface PrimitiveSchema extends PrimitiveInfo {
  input_schema: object
  output_schema: object
  ui_layout_hint?: string
  namespace?: string
  side_effect?: string
  timeout_ms?: number
  scope?: string
  adapter?: string
  status?: string
  intent: PrimitiveIntentPayload
}
