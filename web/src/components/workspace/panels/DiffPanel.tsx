import type { WorkspacePanel } from '@/types/workspace'

const MOCK_DIFF = `--- a/src/server.go
+++ b/src/server.go
@@ -42,7 +42,7 @@ func handleRPC(w http.ResponseWriter, r *http.Request) {
-	if err != nil { log.Fatal(err) }
+	if err != nil { log.Printf("rpc error: %v", err); return }
 
 	w.Header().Set("Content-Type", "application/json")
 	json.NewEncoder(w).Encode(result)
`

function renderDiff(raw: string) {
  return raw.split('\n').map((line, i) => {
    let cls = 'text-[var(--text-secondary)]'
    if (line.startsWith('+') && !line.startsWith('+++')) cls = 'bg-[var(--green-bg)] text-[var(--green)]'
    else if (line.startsWith('-') && !line.startsWith('---')) cls = 'bg-[var(--red-bg)] text-[var(--red)]'
    else if (line.startsWith('@@')) cls = 'text-[var(--blue)]'
    return (
      <div key={i} className={`font-mono text-[11px] leading-5 px-2 whitespace-pre ${cls}`}>
        {line || ' '}
      </div>
    )
  })
}

export function DiffPanel({ panel: _panel }: { panel: WorkspacePanel }) {
  return (
    <div className="flex h-full flex-col gap-2 p-3">
      <div className="text-[11px] uppercase tracking-[0.18em] text-[var(--text-muted)]">Diff</div>
      <div className="flex-1 overflow-auto rounded-lg border border-[var(--border)] bg-[var(--bg-subtle)] py-2">
        {renderDiff(MOCK_DIFF)}
      </div>
    </div>
  )
}
