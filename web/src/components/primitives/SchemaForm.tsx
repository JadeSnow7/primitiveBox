import Form from '@rjsf/core'
import validator from '@rjsf/validator-ajv8'
import type { RJSFSchema } from '@rjsf/utils'

const uiSchema = {
  'ui:globalOptions': { label: true }
}

const widgets = {
  TextWidget: (props: {
    value?: string
    onChange: (value: string) => void
    placeholder?: string
  }) => (
    <input
      className="field-input"
      value={props.value ?? ''}
      onChange={(e) => props.onChange(e.target.value)}
      placeholder={props.placeholder}
    />
  )
}

interface SchemaFormProps {
  schema: RJSFSchema
  onSubmit: (data: object) => void
  loading?: boolean
}

export function SchemaForm({ schema, onSubmit, loading }: SchemaFormProps) {
  return (
    <div className="rounded-xl border border-[var(--border)] bg-[var(--bg-surface)] p-4">
      <Form
        schema={schema}
        uiSchema={uiSchema}
        widgets={widgets}
        validator={validator}
        onSubmit={({ formData }) => onSubmit((formData as object | undefined) ?? {})}
        liveValidate={false}
        noHtml5Validate
      >
        <button
          type="submit"
          disabled={loading}
          className="mt-3 w-full rounded border border-[var(--border-strong)] py-2 text-[12px] font-medium text-[var(--text-primary)] transition-colors duration-[120ms] hover:bg-[var(--bg-subtle)] disabled:cursor-not-allowed disabled:opacity-40"
        >
          {loading ? '执行中...' : '执行原语'}
        </button>
      </Form>
    </div>
  )
}
