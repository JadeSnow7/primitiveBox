export interface PrimitiveInfo {
  name: string
  description: string
  kind: 'system' | 'app'
}

export interface AppPrimitiveManifest {
  app_id: string
  name: string
  description: string
  input_schema: object
  output_schema: object
  socket_path: string
  intent: {
    category: string
    reversible: boolean
    risk_level: string
  }
}

export interface PrimitiveSchema extends PrimitiveInfo {
  input_schema: object
  output_schema: object
  namespace?: string
  side_effect?: string
  timeout_ms?: number
  scope?: string
}
